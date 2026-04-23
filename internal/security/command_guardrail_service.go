package security

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"blueclaw/internal/config"
)

type CommandGuardrailService struct {
	terminalConfiguration config.TerminalConfiguration
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
		Timeout:              time.Duration(commandGuardrailService.timeoutSecond()) * time.Second,
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

	return commandPlan, nil
}

func (commandGuardrailService CommandGuardrailService) sandboxProvider() string {
	if strings.TrimSpace(commandGuardrailService.terminalConfiguration.SandboxProvider) == "" {
		return "bubblewrap"
	}

	return commandGuardrailService.terminalConfiguration.SandboxProvider
}

func (commandGuardrailService CommandGuardrailService) timeoutSecond() int {
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

	searchPaths := []string{
		"/usr/local/sbin",
		"/usr/local/bin",
		"/usr/sbin",
		"/usr/bin",
		"/sbin",
		"/bin",
	}

	for _, searchPath := range searchPaths {
		candidatePath := filepath.Join(searchPath, executableName)
		information, errorValue := os.Stat(candidatePath)
		if errorValue == nil && !information.IsDir() {
			return filepath.EvalSymlinks(candidatePath)
		}
	}

	return exec.LookPath(executableName)
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

		if !isWithinRootPath(workspaceRootPath, resolvedPath) {
			return errors.New("path argument escapes workspace root")
		}

		for _, deniedPathPrefix := range commandGuardrailService.terminalConfiguration.DeniedPathPrefixes {
			if strings.HasPrefix(resolvedPath, deniedPathPrefix) {
				return errors.New("path argument targets a denied system path")
			}
		}
	}

	return nil
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

	if strings.HasPrefix(pathArgument, "~") {
		return "", errors.New("home-relative paths are not allowed")
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
	return strings.HasPrefix(argument, "/") || strings.HasPrefix(argument, ".") || strings.HasPrefix(argument, "~") || strings.Contains(argument, "/")
}

func isWithinRootPath(rootPath string, targetPath string) bool {
	relativePath, errorValue := filepath.Rel(rootPath, targetPath)
	if errorValue != nil {
		return false
	}
	return relativePath == "." || (!strings.HasPrefix(relativePath, "..") && relativePath != "..")
}

func sanitizeEnvironmentVariables(environmentVariables map[string]string, workspaceRootPath string) map[string]string {
	sanitizedEnvironmentVariables := map[string]string{
		"HOME": workspaceRootPath,
		"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM": "xterm-256color",
		"LANG": "C.UTF-8",
	}

	allowedEnvironmentVariableName := map[string]bool{
		"TERM":      true,
		"LANG":      true,
		"LC_ALL":    true,
		"COLORTERM": true,
	}

	for name, value := range environmentVariables {
		if allowedEnvironmentVariableName[name] {
			sanitizedEnvironmentVariables[name] = value
		}
	}

	return sanitizedEnvironmentVariables
}
