package agentruntime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"blueclaw/internal/access"
	"blueclaw/internal/agent"
	"blueclaw/internal/security"
	"blueclaw/internal/workspacepath"
)

type terminalSessionToolInput struct {
	Action               string            `json:"action"`
	SessionID            string            `json:"sessionID"`
	Command              string            `json:"command"`
	Input                string            `json:"input"`
	WorkingDirectoryPath string            `json:"workingDirectoryPath"`
	EnvironmentVariables map[string]string `json:"environmentVariables"`
	TimeoutSecond        int               `json:"timeoutSecond"`
}

type terminalRunToolInput struct {
	Mode                 string            `json:"mode"`
	Command              string            `json:"command"`
	ExecutableName       string            `json:"executableName"`
	Arguments            []string          `json:"arguments"`
	Stdin                string            `json:"stdin"`
	WorkingDirectoryPath string            `json:"workingDirectoryPath"`
	EnvironmentVariables map[string]string `json:"environmentVariables"`
	TimeoutSecond        int               `json:"timeoutSecond"`
	SessionID            string            `json:"sessionID"`
	Input                string            `json:"input"`
}

func (input terminalRunToolInput) commandRequest() security.CommandRequest {
	command := strings.TrimSpace(input.Command)
	arguments := normalizedTerminalRunArguments(command, input.Arguments)
	if command != "" && len(arguments) > 0 && isSingleExecutableCommand(command) {
		return security.CommandRequest{
			ExecutableName:       command,
			Arguments:            arguments,
			Stdin:                input.Stdin,
			WorkingDirectoryPath: input.WorkingDirectoryPath,
			EnvironmentVariables: input.EnvironmentVariables,
			TimeoutSecond:        input.TimeoutSecond,
		}
	}
	return security.CommandRequest{
		Command:              input.Command,
		ExecutableName:       input.ExecutableName,
		Arguments:            arguments,
		Stdin:                input.Stdin,
		WorkingDirectoryPath: input.WorkingDirectoryPath,
		EnvironmentVariables: input.EnvironmentVariables,
		TimeoutSecond:        input.TimeoutSecond,
	}
}

func normalizedTerminalRunArguments(command string, arguments []string) []string {
	normalizedArguments := append([]string{}, arguments...)
	if len(normalizedArguments) == 0 {
		return normalizedArguments
	}
	firstArgument := strings.TrimSpace(normalizedArguments[0])
	if firstArgument == strings.TrimSpace(command) || filepath.Base(firstArgument) == filepath.Base(strings.TrimSpace(command)) {
		return append([]string{}, normalizedArguments[1:]...)
	}
	return normalizedArguments
}

func isSingleExecutableCommand(command string) bool {
	trimmedCommand := strings.TrimSpace(command)
	if trimmedCommand == "" {
		return false
	}
	return !strings.ContainsAny(trimmedCommand, " \t\n\r;&|<>$`'\"")
}

func (input terminalRunToolInput) sessionInput(action string) terminalSessionToolInput {
	return terminalSessionToolInput{
		Action:               action,
		SessionID:            input.SessionID,
		Command:              input.Command,
		Input:                input.Input,
		WorkingDirectoryPath: input.WorkingDirectoryPath,
		EnvironmentVariables: input.EnvironmentVariables,
		TimeoutSecond:        input.TimeoutSecond,
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerTerminalTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[terminalRunToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "terminal.run",
			Description: "Run a guarded command or manage a PTY session inside the Blueclaw workspace. mode defaults to command; use session_start, session_write, session_status, or session_close for long-running interactive work.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Runs workspace commands, build scripts, render checks, tests, or PTY session operations.",
				Produces:   "Command stdout, stderr, exit status, and runtime diagnostics.",
				SideEffect: "workspace_write",
				UseWhen:    "You need to execute a toolchain command, build, render, test, list files, or inspect environment state.",
				AvoidWhen:  "A dedicated bundled skill script or capability.invoke can perform the action more safely.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"mode":{"type":"string","enum":["command","session_start","session_write","session_status","session_close"]},"command":{"type":"string"},"executableName":{"type":"string"},"arguments":{"type":"array","items":{"type":"string"}},"stdin":{"type":"string"},"workingDirectoryPath":{"type":"string"},"environmentVariables":{"type":"object","additionalProperties":{"type":"string"}},"timeoutSecond":{"type":"number"},"sessionID":{"type":"string"},"input":{"type":"string"}}}`),
		},
		Handler: func(toolContext context.Context, input terminalRunToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.runTerminalRunTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) runTerminalRunTool(toolContext context.Context, input terminalRunToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	switch normalizedTerminalRunMode(input.Mode) {
	case "command":
		return toolCatalogBuilder.runTerminalTool(toolContext, input.commandRequest(), handlerContext)
	case "session_start":
		return toolCatalogBuilder.startTerminalSession(toolContext, input.sessionInput("start"), handlerContext)
	case "session_write":
		return toolCatalogBuilder.writeTerminalSession(input.sessionInput("write"))
	case "session_status":
		return toolCatalogBuilder.statusTerminalSession(input.sessionInput("status"))
	case "session_close":
		return toolCatalogBuilder.closeTerminalSession(input.sessionInput("close"))
	default:
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "terminal_run", "terminal.run mode must be command, session_start, session_write, session_status, or session_close"), nil
	}
}

func normalizedTerminalRunMode(mode string) string {
	trimmedMode := strings.TrimSpace(mode)
	if trimmedMode == "" {
		return "command"
	}
	return trimmedMode
}

func (toolCatalogBuilder *ToolCatalogBuilder) runTerminalTool(toolContext context.Context, input security.CommandRequest, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "terminal_run", "terminal service is unavailable"), nil
	}
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	resolver := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath)
	workingDirectory, errorValue := resolver.ResolveDirectory(input.WorkingDirectoryPath, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "terminal_working_directory", errorValue.Error()), nil
	}
	input.Command = toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Command)
	input.Stdin = toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Stdin)
	input.EnvironmentVariables = toolCatalogBuilder.terminalEnvironmentVariables(input.EnvironmentVariables, scope)
	input.WorkingDirectoryPath = workingDirectory.ConcretePath
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, input.WorkingDirectoryPath) {
		return terminalWorkspaceAccessFailure(input.WorkingDirectoryPath), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	if errorValue := workspaceActor.MkdirAll(toolContext, workspacepath.Directory(workingDirectory), workspaceDirectoryCreateMode(workspacepath.Directory(workingDirectory))); errorValue != nil {
		return actorToolFailure("mkdir_all", "terminal_working_directory", workingDirectory.VirtualPath, errorValue), nil
	}
	if toolFailure := toolCatalogBuilder.materializeTerminalRuntimeDirectories(toolContext, workspaceActor, scope, input.EnvironmentVariables); toolFailure != nil {
		return *toolFailure, nil
	}
	if toolFailure := terminalSourceWriteMisuseFailure(input.Command); toolFailure != nil {
		return *toolFailure, nil
	}
	if toolFailure := preflightNodePackageBuild(toolContext, workspaceActor, workspacepath.Directory(workingDirectory), input.Command); toolFailure != nil {
		return *toolFailure, nil
	}
	input.ExecutionIdentity = toolCatalogBuilder.executionIdentityForRequester(handlerContext.request)
	commandResult, errorValue := workspaceActor.Run(toolContext, input)
	content := marshalToolResult(commandResult)
	if errorValue != nil {
		if security.IsCommandPathGuardrailError(errorValue) {
			return terminalPathGuardrailFailure(commandResult, content), nil
		}
		if runtimePathFailure := terminalRuntimePathFailure(input, commandResult, content); runtimePathFailure != nil {
			return *runtimePathFailure, nil
		}
		return agent.ToolFailureWithOutput(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_run", content, json.RawMessage(content)), nil
	}
	_ = toolContext
	return agent.ToolSuccess(content), nil
}

func terminalWorkspaceAccessFailure(workingDirectoryPath string) agent.ToolResult {
	message := "current account cannot use this workspace path: terminal workingDirectoryPath " + strings.TrimSpace(workingDirectoryPath) + "; recovery: use ~/documents for document work, then deliver accepted files with file.deliver"
	document := json.RawMessage(marshalToolResult(map[string]any{
		"failureClass":      "workspace_permission",
		"path":              strings.TrimSpace(workingDirectoryPath),
		"requiredAccess":    "write",
		"suggestedNextTool": "terminal.run",
		"message":           message,
	}))
	result := agent.ToolFailureWithOutput(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "workspace_permission", message, document)
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	return result
}

func terminalPathGuardrailFailure(commandResult security.CommandResult, content string) agent.ToolResult {
	message := strings.TrimSpace(commandResult.Stderr)
	if message == "" {
		message = content
	}
	result := agent.ToolFailureWithOutput(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "terminal_path_guardrail", message, json.RawMessage(content))
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	return result
}

func terminalRuntimePathFailure(commandRequest security.CommandRequest, commandResult security.CommandResult, content string) *agent.ToolResult {
	combinedText := strings.ToLower(commandResult.Stderr + "\n" + commandResult.Stdout + "\n" + content)
	if !strings.Contains(combinedText, "not found in $path") && !strings.Contains(combinedText, "command not found") && !strings.Contains(combinedText, "executable file not found") {
		return nil
	}
	document := json.RawMessage(marshalToolResult(map[string]any{
		"failureClass":      "terminal_runtime_path",
		"command":           commandRequest.Command,
		"actualPATH":        commandRequest.EnvironmentVariables["PATH"],
		"canonicalPATH":     security.CanonicalRuntimePATH,
		"executionUser":     commandRequest.ExecutionIdentity.UserName,
		"workingDirectory":  commandRequest.WorkingDirectoryPath,
		"commandResult":     commandResult,
		"recommendedAction": "Fix Blueclaw runtime PATH propagation; do not change site source or ask the user to use external hosting.",
	}))
	result := agent.ToolFailureWithOutput(agent.FailureDependencyUnavailable, agent.FailureCode("terminal_runtime_path"), "terminal_runtime_path", "terminal runtime PATH did not expose a managed executable", document)
	result.Failure.Retryable = true
	result.Failure.SafeRetry = false
	return &result
}

func terminalSourceWriteMisuseFailure(command string) *agent.ToolResult {
	target := terminalSourceWriteTarget(command)
	if target == "" {
		return nil
	}
	message := "terminal.run is for commands, builds, renders, and inspection; use bundled skill scripts or capability.invoke operations for source creation and edits"
	document := json.RawMessage(marshalToolResult(map[string]any{
		"failureClass":       "source_edit_tool_misuse",
		"command":            command,
		"detectedTarget":     target,
		"suggestedNextTools": []string{"terminal.run", "skill.search"},
		"message":            message,
	}))
	result := agent.ToolFailureWithOutput(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "terminal_source_write", message, document)
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	result.Failure.RetryPolicy = "different_tool"
	result.Failure.RecoveryHints = []agent.RecoveryHint{{
		Action:    "edit_resource",
		ToolNames: []string{"terminal.run", "skill.search"},
		Reason:    "Use bundled skill scripts or capability.invoke operations so the runtime can preserve context, permissions, and recovery state.",
	}}
	return &result
}

func terminalSourceWriteTarget(command string) string {
	if strings.Contains(command, "<<") {
		return "shell heredoc"
	}
	fields := strings.Fields(command)
	for index, field := range fields {
		if sourcePath := shellRedirectSourcePath(field, nextShellField(fields, index)); sourcePath != "" {
			return sourcePath
		}
	}
	return ""
}

func nextShellField(fields []string, index int) string {
	nextIndex := index + 1
	if nextIndex >= len(fields) {
		return ""
	}
	return fields[nextIndex]
}

func shellRedirectSourcePath(field string, nextField string) string {
	if strings.Contains(field, ">&") || strings.Contains(field, "<&") {
		return ""
	}
	switch field {
	case ">", ">>", "1>", "1>>":
		if terminalWritePathLooksLikeSource(nextField) {
			return nextField
		}
		return ""
	}
	for _, prefix := range []string{">>", ">", "1>>", "1>"} {
		if strings.HasPrefix(field, prefix) {
			candidatePath := strings.TrimPrefix(field, prefix)
			if terminalWritePathLooksLikeSource(candidatePath) {
				return candidatePath
			}
		}
	}
	return ""
}

func terminalWritePathLooksLikeSource(path string) bool {
	cleanPath := strings.Trim(strings.TrimSpace(path), `"'`)
	if cleanPath == "" {
		return false
	}
	slashPath := filepath.ToSlash(cleanPath)
	if strings.HasPrefix(slashPath, "dist/") || strings.HasPrefix(slashPath, "build/") || strings.Contains(slashPath, "/dist/") || strings.Contains(slashPath, "/build/") {
		return false
	}
	extension := strings.ToLower(filepath.Ext(cleanPath))
	if !terminalWriteExtensionLooksLikeSource(extension) {
		return false
	}
	baseName := strings.ToLower(filepath.Base(cleanPath))
	if terminalWriteBaseNameLooksLikeSource(baseName) {
		return true
	}
	return strings.HasPrefix(slashPath, "src/") || strings.HasPrefix(slashPath, "app/src/") || strings.Contains(slashPath, "/src/")
}

func terminalWriteExtensionLooksLikeSource(extension string) bool {
	switch extension {
	case ".ts", ".tsx", ".js", ".jsx", ".css", ".scss", ".html", ".md", ".mdx", ".json", ".toml", ".yaml", ".yml", ".svelte", ".vue", ".go", ".py", ".sh":
		return true
	default:
		return false
	}
}

func terminalWriteBaseNameLooksLikeSource(baseName string) bool {
	switch baseName {
	case "package.json", "vite.config.ts", "vite.config.js", "tsconfig.json", "tailwind.config.ts", "tailwind.config.js", "design.md", "presentation.md":
		return true
	default:
		return false
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) sessionTerminalTool(toolContext context.Context, input terminalSessionToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "terminal_session", "terminal service is unavailable"), nil
	}
	switch strings.TrimSpace(input.Action) {
	case "start":
		return toolCatalogBuilder.startTerminalSession(toolContext, input, handlerContext)
	case "write":
		return toolCatalogBuilder.writeTerminalSession(input)
	case "status":
		return toolCatalogBuilder.statusTerminalSession(input)
	case "close":
		return toolCatalogBuilder.closeTerminalSession(input)
	default:
		_ = toolContext
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "terminal_session", "terminal session action must be start, write, status, or close"), nil
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) startTerminalSession(toolContext context.Context, input terminalSessionToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	workingDirectory, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).ResolveDirectory(input.WorkingDirectoryPath, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "terminal_session", errorValue.Error()), nil
	}
	workingDirectoryPath := workingDirectory.ConcretePath
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, workingDirectoryPath) {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "terminal_session", "current account cannot use this workspace path"), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	if errorValue := workspaceActor.MkdirAll(toolContext, workspacepath.Directory(workingDirectory), workspaceDirectoryCreateMode(workspacepath.Directory(workingDirectory))); errorValue != nil {
		return actorToolFailure("mkdir_all", "terminal_session", workingDirectory.VirtualPath, errorValue), nil
	}
	environmentVariables := toolCatalogBuilder.terminalEnvironmentVariables(input.EnvironmentVariables, scope)
	if toolFailure := toolCatalogBuilder.materializeTerminalRuntimeDirectories(toolContext, workspaceActor, scope, environmentVariables); toolFailure != nil {
		return *toolFailure, nil
	}
	sessionID, errorValue := toolCatalogBuilder.terminalService.StartInteractiveSession(security.CommandRequest{
		Command:              toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Command),
		WorkingDirectoryPath: workingDirectoryPath,
		EnvironmentVariables: environmentVariables,
		TimeoutSecond:        input.TimeoutSecond,
		IsInteractive:        true,
		IsPTY:                true,
		ExecutionIdentity:    toolCatalogBuilder.executionIdentityForRequester(handlerContext.request),
	})
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_session", errorValue.Error()), nil
	}
	status, errorValue := toolCatalogBuilder.terminalService.StatusSession(sessionID)
	return statusToolResult(status, errorValue), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) writeTerminalSession(input terminalSessionToolInput) (agent.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "terminal_run", "terminal service is unavailable"), nil
	}
	commandResult, errorValue := toolCatalogBuilder.terminalService.WriteSessionInput(input.SessionID, input.Input)
	return terminalSessionToolResult(commandResult, errorValue), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) statusTerminalSession(input terminalSessionToolInput) (agent.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "terminal_run", "terminal service is unavailable"), nil
	}
	status, errorValue := toolCatalogBuilder.terminalService.StatusSession(input.SessionID)
	return statusToolResult(status, errorValue), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) closeTerminalSession(input terminalSessionToolInput) (agent.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "terminal_run", "terminal service is unavailable"), nil
	}
	errorValue := toolCatalogBuilder.terminalService.CloseSession(input.SessionID)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_run", errorValue.Error()), nil
	}
	return agent.ToolSuccess(marshalToolResult(map[string]string{"sessionID": input.SessionID, "status": "closed"})), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) terminalEnvironmentVariables(environmentVariables map[string]string, scope WorkspaceScope) map[string]string {
	mergedEnvironmentVariables := mergeWorkspaceEnvironment(environmentVariables, scope.EnvironmentVariables())
	if endpoint := strings.TrimSpace(toolCatalogBuilder.capabilityClient.Endpoint); endpoint != "" {
		mergedEnvironmentVariables["CAPABILITY_BRIDGE_URL"] = endpoint
	}
	return toolCatalogBuilder.resolveAgentWorkspaceEnvironment(mergedEnvironmentVariables)
}

func terminalSessionToolResult(commandResult security.CommandResult, errorValue error) agent.ToolResult {
	content := marshalToolResult(commandResult)
	if errorValue != nil {
		return agent.ToolFailureWithOutput(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_session", content, json.RawMessage(content))
	}
	return agent.ToolSuccess(content)
}

func preflightNodePackageBuild(ctx context.Context, workspaceActor security.WorkspaceActor, workingDirectory workspacepath.Directory, command string) *agent.ToolResult {
	if !commandRunsNodePackageBuild(command) {
		return nil
	}
	packagePath := workingDirectory.JoinVirtualFile("package.json")
	document, errorValue := workspaceActor.ReadFile(ctx, packagePath, 256*1024)
	if errorValue != nil {
		return packageManifestInvalidFailure(packagePath.VirtualPath, "package.json is missing or unreadable: "+errorValue.Error())
	}
	var packageDocument map[string]any
	if errorValue := json.Unmarshal(document, &packageDocument); errorValue != nil {
		return packageManifestInvalidFailure(packagePath.VirtualPath, "package.json is not valid JSON: "+errorValue.Error())
	}
	scripts, isObject := packageDocument["scripts"].(map[string]any)
	if !isObject {
		return packageManifestInvalidFailure(packagePath.VirtualPath, "package.json has no scripts object")
	}
	buildScript, isString := scripts["build"].(string)
	if !isString || strings.TrimSpace(buildScript) == "" {
		return packageManifestInvalidFailure(packagePath.VirtualPath, "package.json has no scripts.build command")
	}
	return nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) materializeTerminalRuntimeDirectories(ctx context.Context, workspaceActor security.WorkspaceActor, scope WorkspaceScope, environmentVariables map[string]string) *agent.ToolResult {
	for _, directory := range terminalRuntimeDirectories(scope, environmentVariables) {
		if errorValue := workspaceActor.MkdirAll(ctx, directory, workspaceDirectoryCreateMode(directory)); errorValue != nil {
			result := actorToolFailure("mkdir_all", "terminal_runtime_environment", directory.VirtualPath, errorValue)
			return &result
		}
	}
	return nil
}

func terminalRuntimeDirectories(scope WorkspaceScope, environmentVariables map[string]string) []workspacepath.Directory {
	seenDirectoryPaths := map[string]bool{}
	var directories []workspacepath.Directory
	for _, name := range []string{
		"TMPDIR",
		"TMP",
		"TEMP",
		"XDG_CACHE_HOME",
		"XDG_CONFIG_HOME",
		"XDG_RUNTIME_DIR",
		"BUN_TMPDIR",
		"BUN_INSTALL",
		"BUN_INSTALL_CACHE_DIR",
		"npm_config_cache",
	} {
		directoryPath := filepath.Clean(strings.TrimSpace(environmentVariables[name]))
		if directoryPath == "." || !strings.HasPrefix(directoryPath, filepath.Clean(scope.RequesterRootPath)+string(filepath.Separator)) {
			continue
		}
		if seenDirectoryPaths[directoryPath] {
			continue
		}
		seenDirectoryPaths[directoryPath] = true
		directories = append(directories, requesterOwnedRuntimeDirectory(scope, directoryPath))
	}
	return directories
}

func requesterOwnedRuntimeDirectory(scope WorkspaceScope, directoryPath string) workspacepath.Directory {
	virtualPath := filepath.ToSlash(directoryPath)
	if relativePath, errorValue := filepath.Rel(scope.RequesterRootPath, directoryPath); errorValue == nil && relativePath != "." && !strings.HasPrefix(relativePath, "..") {
		virtualPath = filepath.ToSlash(relativePath)
	}
	return workspacepath.Directory{
		ConcretePath: directoryPath,
		VirtualPath:  virtualPath,
		Kind:         workspacepath.KindWorkspace,
	}
}

func commandRunsNodePackageBuild(command string) bool {
	tokens := terminalCommandWords(command)
	for index := 0; index < len(tokens); index++ {
		switch tokens[index] {
		case "bun", "npm", "pnpm":
			if index+2 < len(tokens) && tokens[index+1] == "run" && tokens[index+2] == "build" {
				return true
			}
		case "yarn":
			if index+1 < len(tokens) && tokens[index+1] == "build" {
				return true
			}
			if index+2 < len(tokens) && tokens[index+1] == "run" && tokens[index+2] == "build" {
				return true
			}
		}
	}
	return false
}

func terminalCommandWords(command string) []string {
	replacer := strings.NewReplacer("\n", " ", "\t", " ", ";", " ", "&&", " ", "||", " ")
	fields := strings.Fields(replacer.Replace(command))
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		word := strings.Trim(field, `"'`)
		if word != "" {
			words = append(words, word)
		}
	}
	return words
}

func packageManifestInvalidFailure(path string, detail string) *agent.ToolResult {
	content := marshalToolResult(map[string]string{
		"code":   "package_manifest_invalid",
		"path":   strings.TrimSpace(path),
		"detail": strings.TrimSpace(detail),
	})
	return &agent.ToolResult{
		Output: agent.ToolOutput{Content: content, Data: json.RawMessage(content)},
		Failure: &agent.ToolFailure{
			Kind:            agent.FailureInvalidInput,
			Code:            "package_manifest_invalid",
			Stage:           "package_manifest_preflight",
			UserSafeSummary: content,
			Retryable:       true,
			SafeRetry:       true,
		},
	}
}

func statusToolResult(status security.TerminalSessionStatus, errorValue error) agent.ToolResult {
	content := marshalToolResult(status)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_session", errorValue.Error())
	}
	return agent.ToolSuccess(content)
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveTerminalWorkingDirectoryPath(value string, conversationScope ConversationResourceScope) string {
	trimmedPath := toolCatalogBuilder.resolveAgentWorkspacePath(value)
	defaultDirectoryPath := firstNonEmptyString(conversationScope.DefaultDirectoryPath, toolCatalogBuilder.workspaceRootPath)
	if trimmedPath == "" {
		return defaultDirectoryPath
	}
	if filepath.IsAbs(trimmedPath) {
		return trimmedPath
	}
	return filepath.Join(defaultDirectoryPath, trimmedPath)
}
