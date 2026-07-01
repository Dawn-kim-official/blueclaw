package security

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"blueclaw/internal/config"
)

type CommandGuardrailService struct {
	terminalConfiguration config.TerminalConfiguration
}

type CommandGuardrailError struct {
	Reason               string
	Token                string
	ResolvedPath         string
	WorkspaceRootPath    string
	WorkingDirectoryPath string
	RecoveryHint         string
}

func (commandGuardrailError CommandGuardrailError) Error() string {
	parts := []string{strings.TrimSpace(commandGuardrailError.Reason)}
	if strings.TrimSpace(commandGuardrailError.Token) != "" {
		parts = append(parts, `token "`+strings.TrimSpace(commandGuardrailError.Token)+`"`)
	}
	if strings.TrimSpace(commandGuardrailError.ResolvedPath) != "" {
		parts = append(parts, `resolves to "`+strings.TrimSpace(commandGuardrailError.ResolvedPath)+`"`)
	}
	if strings.TrimSpace(commandGuardrailError.WorkspaceRootPath) != "" {
		parts = append(parts, `workspace root "`+strings.TrimSpace(commandGuardrailError.WorkspaceRootPath)+`"`)
	}
	if strings.TrimSpace(commandGuardrailError.RecoveryHint) != "" {
		parts = append(parts, "recovery: "+strings.TrimSpace(commandGuardrailError.RecoveryHint))
	}
	return strings.Join(parts, "; ")
}

func IsCommandPathGuardrailError(errorValue error) bool {
	var commandGuardrailError CommandGuardrailError
	return errors.As(errorValue, &commandGuardrailError)
}

func NewCommandGuardrailService(terminalConfiguration config.TerminalConfiguration) CommandGuardrailService {
	return CommandGuardrailService{
		terminalConfiguration: terminalConfiguration,
	}
}

func (commandGuardrailService CommandGuardrailService) BuildCommandPlan(commandRequest CommandRequest) (CommandPlan, error) {
	if os.Geteuid() == 0 {
		return CommandPlan{}, errors.New("terminal execution is denied for root")
	}

	workspaceRootPath, errorValue := filepath.Abs(commandGuardrailService.terminalConfiguration.WorkspaceRootPath)
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}

	workingDirectoryPath, errorValue := resolveWorkingDirectoryPath(commandRequest.WorkingDirectoryPath, workspaceRootPath)
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}

	if strings.TrimSpace(commandRequest.Command) != "" || commandRequest.IsPTY {
		return commandGuardrailService.buildBashCommandPlan(commandRequest, workingDirectoryPath, workspaceRootPath)
	}

	resolvedExecutablePath, errorValue := commandGuardrailService.resolveExecutablePath(commandRequest.ExecutableName)
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}

	errorValue = commandGuardrailService.validateExecutable(resolvedExecutablePath)
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}

	errorValue = commandGuardrailService.validateInlineEval(commandRequest.ExecutableName, commandRequest.Arguments)
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}

	if commandRequest.IsInteractive && !commandGuardrailService.terminalConfiguration.AllowInteractiveShell {
		return CommandPlan{}, errors.New("interactive shell is disabled")
	}

	errorValue = commandGuardrailService.validateArgumentPaths(commandRequest.Arguments, workingDirectoryPath, workspaceRootPath)
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}

	commandPlan := CommandPlan{
		ExecutablePath:       resolvedExecutablePath,
		Arguments:            append([]string{}, commandRequest.Arguments...),
		WorkingDirectoryPath: workingDirectoryPath,
		EnvironmentVariables: sanitizeEnvironmentVariables(commandRequest.EnvironmentVariables, workspaceRootPath),
		Timeout:              time.Duration(commandGuardrailService.timeoutSecond(commandRequest.TimeoutSecond)) * time.Second,
		ExecutionIdentity:    commandRequest.ExecutionIdentity,
	}

	if commandGuardrailService.terminalConfiguration.Mode == "sandbox" {
		if commandGuardrailService.sandboxProvider() != "bubblewrap" {
			return CommandPlan{}, errors.New("only bubblewrap sandbox provider is supported in v1")
		}
		return commandGuardrailService.buildSandboxCommandPlan(commandPlan, workspaceRootPath)
	}

	if !commandGuardrailService.terminalConfiguration.AllowNetwork {
		return CommandPlan{}, errors.New("native execution without network is not allowed when terminal.allowNetwork is false")
	}

	return commandGuardrailService.applyPOSIXRunner(commandPlan, workspaceRootPath)
}

func (commandGuardrailService CommandGuardrailService) buildBashCommandPlan(commandRequest CommandRequest, workingDirectoryPath string, workspaceRootPath string) (CommandPlan, error) {
	if commandRequest.IsInteractive && !commandGuardrailService.terminalConfiguration.AllowInteractiveShell {
		return CommandPlan{}, errors.New("interactive shell is disabled")
	}
	if errorValue := commandGuardrailService.validateCommandString(commandRequest.Command, workingDirectoryPath, workspaceRootPath); errorValue != nil {
		return CommandPlan{}, errorValue
	}
	resolvedExecutablePath, errorValue := commandGuardrailService.resolveExecutablePath("bash")
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}
	if commandGuardrailService.terminalConfiguration.Mode != "firecrackerGuest" {
		if errorValue := commandGuardrailService.validateExecutable(resolvedExecutablePath); errorValue != nil {
			return CommandPlan{}, errorValue
		}
	}
	arguments := []string{"--noprofile", "--norc", "-s"}
	if commandRequest.IsPTY {
		arguments = []string{"-i"}
	}
	commandPlan := CommandPlan{
		ExecutablePath:       resolvedExecutablePath,
		Arguments:            arguments,
		Stdin:                joinCommandInput(commandRequest.Command, commandRequest.Stdin),
		WorkingDirectoryPath: workingDirectoryPath,
		EnvironmentVariables: sanitizeEnvironmentVariables(commandRequest.EnvironmentVariables, workspaceRootPath),
		Timeout:              time.Duration(commandGuardrailService.timeoutSecond(commandRequest.TimeoutSecond)) * time.Second,
		IsPTY:                commandRequest.IsPTY,
		ExecutionIdentity:    commandRequest.ExecutionIdentity,
	}
	return commandGuardrailService.applyPOSIXRunner(commandPlan, workspaceRootPath)
}

func (commandGuardrailService CommandGuardrailService) applyPOSIXRunner(commandPlan CommandPlan, workspaceRootPath string) (CommandPlan, error) {
	if strings.TrimSpace(commandGuardrailService.terminalConfiguration.POSIXHelperPath) == "" {
		return commandPlan, nil
	}
	if strings.TrimSpace(commandPlan.ExecutionIdentity.UserName) == "" {
		return commandPlan, nil
	}

	resolvedIdentity, errorValue := ResolveExecutionIdentity(commandPlan.ExecutionIdentity)
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}
	targetWorkingDirectoryPath := commandPlan.WorkingDirectoryPath
	helperArguments := []string{
		"exec",
		"--uid", formatUnsignedID(resolvedIdentity.UserID),
		"--gid", formatUnsignedID(resolvedIdentity.GroupID),
		"--groups", joinUnsignedIDs(resolvedIdentity.SupplementaryGroupIDs),
		"--cwd", targetWorkingDirectoryPath,
		"--",
		commandPlan.ExecutablePath,
	}
	helperArguments = append(helperArguments, commandPlan.Arguments...)
	commandPlan.ExecutablePath = commandGuardrailService.terminalConfiguration.POSIXHelperPath
	commandPlan.Arguments = helperArguments
	commandPlan.WorkingDirectoryPath = workspaceRootPath
	commandPlan.EnvironmentVariables = applyPOSIXEnvironment(commandPlan.EnvironmentVariables, resolvedIdentity)
	commandPlan.ExecutionIdentity = resolvedIdentity
	return commandPlan, nil
}

func (commandGuardrailService CommandGuardrailService) sandboxProvider() string {
	if strings.TrimSpace(commandGuardrailService.terminalConfiguration.SandboxProvider) == "" {
		return "bubblewrap"
	}

	return commandGuardrailService.terminalConfiguration.SandboxProvider
}

func (commandGuardrailService CommandGuardrailService) timeoutSecond(requestedTimeoutSecond int) int {
	if requestedTimeoutSecond > 0 && requestedTimeoutSecond < commandGuardrailService.maxTimeoutSecond() {
		return requestedTimeoutSecond
	}
	if commandGuardrailService.terminalConfiguration.TimeoutSecond <= 0 {
		return 120
	}

	return commandGuardrailService.terminalConfiguration.TimeoutSecond
}

func (commandGuardrailService CommandGuardrailService) maxTimeoutSecond() int {
	if commandGuardrailService.terminalConfiguration.TimeoutSecond <= 0 {
		return 120
	}
	return commandGuardrailService.terminalConfiguration.TimeoutSecond
}

func (commandGuardrailService CommandGuardrailService) resolveExecutablePath(executableName string) (string, error) {
	if strings.TrimSpace(executableName) == "" {
		return "", errors.New("executableName is required")
	}

	if filepath.IsAbs(executableName) {
		return filepath.EvalSymlinks(executableName)
	}

	for _, searchPath := range strings.Split(CanonicalRuntimePATH, ":") {
		candidatePath := filepath.Join(searchPath, executableName)
		information, errorValue := os.Stat(candidatePath)
		if errorValue == nil && !information.IsDir() {
			return filepath.EvalSymlinks(candidatePath)
		}
	}

	return "", errors.New("executable was not found in canonical runtime PATH")
}

func (commandGuardrailService CommandGuardrailService) validateExecutable(resolvedExecutablePath string) error {
	executableName := filepath.Base(resolvedExecutablePath)

	for _, deniedExecutableName := range commandGuardrailService.terminalConfiguration.DeniedExecutableNames {
		if executableName == deniedExecutableName {
			return errors.New("system modification executable is denied")
		}
	}

	for _, allowedExecutableName := range commandGuardrailService.terminalConfiguration.AllowedExecutableNames {
		if executableName == allowedExecutableName {
			return nil
		}
	}

	return errors.New("executable is not allowlisted")
}

func (commandGuardrailService CommandGuardrailService) validateInlineEval(executableName string, arguments []string) error {
	inlineEvalFlagByExecutableName := map[string][]string{
		"bash":      {"-c"},
		"sh":        {"-c"},
		"zsh":       {"-c"},
		"fish":      {"-c"},
		"node":      {"-e", "--eval", "-p"},
		"bun":       {"-e", "--eval"},
		"deno":      {"eval"},
		"python":    {"-c", "-m"},
		"python3":   {"-c", "-m"},
		"ruby":      {"-e"},
		"perl":      {"-e", "-E"},
		"php":       {"-r"},
		"lua":       {"-e"},
		"osascript": {"-e"},
	}

	deniedFlags := inlineEvalFlagByExecutableName[filepath.Base(executableName)]
	for _, argument := range arguments {
		for _, deniedFlag := range deniedFlags {
			if argument == deniedFlag {
				return errors.New("inline eval is denied by hard guardrail")
			}
		}
	}

	return nil
}

func (commandGuardrailService CommandGuardrailService) validateArgumentPaths(arguments []string, workingDirectoryPath string, workspaceRootPath string) error {
	for _, argument := range arguments {
		if !looksLikePath(argument) {
			continue
		}

		resolvedPath, errorValue := resolvePathArgument(argument, workingDirectoryPath)
		if errorValue != nil {
			return errorValue
		}

		if isAllowedStandardDevicePath(resolvedPath) {
			continue
		}

		for _, deniedPathPrefix := range commandGuardrailService.terminalConfiguration.DeniedPathPrefixes {
			if strings.HasPrefix(resolvedPath, deniedPathPrefix) {
				return newCommandGuardrailError("path argument targets a denied system path", argument, resolvedPath, workspaceRootPath, workingDirectoryPath)
			}
		}
	}

	return nil
}

func (commandGuardrailService CommandGuardrailService) validateCommandString(command string, workingDirectoryPath string, workspaceRootPath string) error {
	for _, token := range commandTokens(command) {
		if commandGuardrailService.isDeniedExecutableToken(token) {
			return errors.New("command references a denied executable")
		}
		if !looksLikePath(token) {
			continue
		}
		resolvedPath, errorValue := resolvePathArgument(token, workingDirectoryPath)
		if errorValue != nil {
			return errorValue
		}
		if isAllowedStandardDevicePath(resolvedPath) {
			continue
		}
		for _, deniedPathPrefix := range commandGuardrailService.terminalConfiguration.DeniedPathPrefixes {
			if strings.HasPrefix(resolvedPath, deniedPathPrefix) {
				return newCommandGuardrailError("command path targets a denied system path", token, resolvedPath, workspaceRootPath, workingDirectoryPath)
			}
		}
	}
	return nil
}

func newCommandGuardrailError(reason string, token string, resolvedPath string, workspaceRootPath string, workingDirectoryPath string) CommandGuardrailError {
	return CommandGuardrailError{
		Reason:               strings.TrimSpace(reason),
		Token:                strings.TrimSpace(token),
		ResolvedPath:         strings.TrimSpace(resolvedPath),
		WorkspaceRootPath:    strings.TrimSpace(workspaceRootPath),
		WorkingDirectoryPath: strings.TrimSpace(workingDirectoryPath),
		RecoveryHint:         "use ~ paths in tool fields: do document work in ~/documents/ and save finished documents as ~/documents/<name>.<ext>; run built-in artifact skills through /workspace/skills/<skill>/scripts/skill_runtime.py instead of runtime-internal interpreters",
	}
}

func (commandGuardrailService CommandGuardrailService) isDeniedExecutableToken(token string) bool {
	executableName := filepath.Base(strings.TrimSpace(token))
	for _, deniedExecutableName := range commandGuardrailService.terminalConfiguration.DeniedExecutableNames {
		if executableName == deniedExecutableName {
			return true
		}
	}
	return false
}

func commandTokens(command string) []string {
	command = commandWithoutHereDocumentBodies(command)
	replacer := strings.NewReplacer(
		"\n", " ",
		";", " ",
		"&&", " ",
		"||", " ",
		"|", " ",
		"(", " ",
		")", " ",
		"=", " ",
		"<", " ",
		">", " ",
	)
	fields := strings.Fields(replacer.Replace(command))
	tokens := []string{}
	for _, field := range fields {
		token := strings.Trim(field, `"'`)
		if token != "" {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func commandWithoutHereDocumentBodies(command string) string {
	lines := strings.Split(command, "\n")
	keptLines := []string{}
	pendingDelimiters := []string{}
	for _, line := range lines {
		if len(pendingDelimiters) > 0 {
			if strings.TrimSpace(line) == pendingDelimiters[0] {
				pendingDelimiters = pendingDelimiters[1:]
			}
			continue
		}
		keptLines = append(keptLines, line)
		pendingDelimiters = append(pendingDelimiters, hereDocumentDelimiters(line)...)
	}
	return strings.Join(keptLines, "\n")
}

func hereDocumentDelimiters(line string) []string {
	delimiters := []string{}
	for index := 0; index+1 < len(line); index++ {
		if line[index] != '<' || line[index+1] != '<' {
			continue
		}
		readIndex := index + 2
		if readIndex < len(line) && line[readIndex] == '<' {
			index = readIndex
			continue
		}
		if readIndex < len(line) && line[readIndex] == '-' {
			readIndex++
		}
		for readIndex < len(line) && (line[readIndex] == ' ' || line[readIndex] == '\t') {
			readIndex++
		}
		delimiter, nextIndex := readHereDocumentDelimiter(line, readIndex)
		if delimiter != "" {
			delimiters = append(delimiters, delimiter)
		}
		index = nextIndex
	}
	return delimiters
}

func readHereDocumentDelimiter(line string, startIndex int) (string, int) {
	if startIndex >= len(line) {
		return "", startIndex
	}
	if line[startIndex] == '\'' || line[startIndex] == '"' {
		quote := line[startIndex]
		endIndex := startIndex + 1
		for endIndex < len(line) && line[endIndex] != quote {
			endIndex++
		}
		if endIndex >= len(line) {
			return "", endIndex
		}
		return line[startIndex+1 : endIndex], endIndex
	}
	endIndex := startIndex
	for endIndex < len(line) && !isShellDelimiterByte(line[endIndex]) {
		endIndex++
	}
	return line[startIndex:endIndex], endIndex
}

func isShellDelimiterByte(value byte) bool {
	return value == ' ' || value == '\t' || value == ';' || value == '&' || value == '|' || value == '<' || value == '>' || value == '(' || value == ')'
}

func joinCommandInput(command string, stdin string) string {
	if strings.TrimSpace(command) == "" {
		return stdin
	}
	if strings.TrimSpace(stdin) == "" {
		return command
	}
	if strings.HasSuffix(command, "\n") {
		return command + stdin
	}
	return command + "\n" + stdin
}

func resolveWorkingDirectoryPath(workingDirectoryPath string, workspaceRootPath string) (string, error) {
	if strings.TrimSpace(workingDirectoryPath) == "" {
		return workspaceRootPath, nil
	}

	resolvedPath, errorValue := resolvePathArgument(workingDirectoryPath, workspaceRootPath)
	if errorValue != nil {
		return "", errorValue
	}
	if !isWithinRootPath(workspaceRootPath, resolvedPath) {
		return "", errors.New("working directory escapes workspace root")
	}
	return resolvedPath, nil
}

func resolvePathArgument(pathArgument string, basePath string) (string, error) {
	if filepath.IsAbs(pathArgument) {
		return filepath.Clean(pathArgument), nil
	}

	return filepath.Abs(filepath.Join(basePath, pathArgument))
}

func looksLikePath(argument string) bool {
	if argument == "" {
		return false
	}
	if strings.Contains(argument, "://") {
		return false
	}
	return strings.HasPrefix(argument, "/") || strings.HasPrefix(argument, ".") || strings.Contains(argument, "/")
}

func isWithinRootPath(rootPath string, targetPath string) bool {
	relativePath, errorValue := filepath.Rel(rootPath, targetPath)
	if errorValue != nil {
		return false
	}
	return relativePath == "." || (!strings.HasPrefix(relativePath, "..") && relativePath != "..")
}

var allowedStandardDevicePaths = map[string]bool{
	"/dev/null":    true,
	"/dev/zero":    true,
	"/dev/full":    true,
	"/dev/random":  true,
	"/dev/urandom": true,
	"/dev/stdin":   true,
	"/dev/stdout":  true,
	"/dev/stderr":  true,
	"/dev/tty":     true,
}

func isAllowedStandardDevicePath(resolvedPath string) bool {
	return allowedStandardDevicePaths[resolvedPath]
}

func sanitizeEnvironmentVariables(environmentVariables map[string]string, workspaceRootPath string) map[string]string {
	sanitizedEnvironmentVariables := map[string]string{
		"HOME": workspaceRootPath,
		"TERM": "xterm-256color",
		"LANG": "C.UTF-8",
	}

	allowedEnvironmentVariableName := map[string]bool{
		"TERM":                         true,
		"LANG":                         true,
		"LC_ALL":                       true,
		"COLORTERM":                    true,
		"BLUECLAW_REQUESTER_TMP":       true,
		"BLUECLAW_TASK_TMP":            true,
		"BLUECLAW_REQUESTER_ARTIFACTS": true,
		"BLUECLAW_DEPENDENCY_CACHE":    true,
		"HOME":                         true,
		"TMPDIR":                       true,
		"TMP":                          true,
		"TEMP":                         true,
		"XDG_CACHE_HOME":               true,
		"XDG_CONFIG_HOME":              true,
		"XDG_RUNTIME_DIR":              true,
		"BUN_TMPDIR":                   true,
		"BUN_INSTALL":                  true,
		"BUN_INSTALL_CACHE_DIR":        true,
		"CAPABILITY_BRIDGE_URL":        true,
		"npm_config_cache":             true,
	}

	for name, value := range environmentVariables {
		if allowedEnvironmentVariableName[name] {
			sanitizedEnvironmentVariables[name] = value
		}
	}

	return enforceCanonicalRuntimePATH(sanitizedEnvironmentVariables)
}
