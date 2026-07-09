package agentruntime

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"blueclaw/internal/access"
	"blueclaw/internal/agent"
	"blueclaw/internal/security"
	"blueclaw/internal/workspacepath"
)

var terminalRunHeartbeatInterval = 60 * time.Second

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
	actorStartedAt := time.Now()
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	slog.Info("terminal.run actor acquired", "durationMs", time.Since(actorStartedAt).Milliseconds())
	workingDirectoryStartedAt := time.Now()
	if errorValue := workspaceActor.MkdirAll(toolContext, workspacepath.Directory(workingDirectory), workspaceDirectoryCreateMode(workspacepath.Directory(workingDirectory))); errorValue != nil {
		return actorToolFailure("mkdir_all", "terminal_working_directory", workingDirectory.VirtualPath, errorValue), nil
	}
	slog.Info("terminal.run working directory prepared", "durationMs", time.Since(workingDirectoryStartedAt).Milliseconds())
	materializeStartedAt := time.Now()
	if toolFailure := toolCatalogBuilder.materializeTerminalRuntimeDirectories(toolContext, workspaceActor, scope, input.EnvironmentVariables); toolFailure != nil {
		return *toolFailure, nil
	}
	slog.Info("terminal.run runtime directories materialized", "durationMs", time.Since(materializeStartedAt).Milliseconds())
	input.ExecutionIdentity = toolCatalogBuilder.executionIdentityForRequester(handlerContext.request)
	runStartedAt := time.Now()
	stopHeartbeat := toolCatalogBuilder.startTerminalRunHeartbeat(toolContext, input.Command)
	commandResult, errorValue := workspaceActor.Run(toolContext, input)
	stopHeartbeat()
	slog.Info("terminal.run command completed", "durationMs", time.Since(runStartedAt).Milliseconds(), "exitCode", commandResult.ExitCode, "timedOut", commandResult.TimedOut)
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
	return agent.ToolSuccess(content), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) startTerminalRunHeartbeat(toolContext context.Context, command string) func() {
	taskRunID := agent.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return func() {}
	}
	commandHead := terminalCommandHead(command)
	startedAt := time.Now()
	stopChannel := make(chan struct{})
	go func() {
		heartbeatTicker := time.NewTicker(terminalRunHeartbeatInterval)
		defer heartbeatTicker.Stop()
		for {
			select {
			case <-stopChannel:
				return
			case <-heartbeatTicker.C:
				toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "terminal.run.heartbeat", marshalToolResult(map[string]any{
					"elapsedSeconds": int(time.Since(startedAt).Seconds()),
					"command":        commandHead,
				}))
			}
		}
	}()
	return func() { close(stopChannel) }
}

func terminalCommandHead(command string) string {
	commandRunes := []rune(strings.TrimSpace(command))
	if len(commandRunes) <= 80 {
		return string(commandRunes)
	}
	return string(commandRunes[:80])
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
