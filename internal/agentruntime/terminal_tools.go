package agentruntime

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"blueclaw/internal/access"
	"blueclaw/internal/agent"
	"blueclaw/internal/security"
	"blueclaw/internal/workspacepath"
)

var terminalRunHeartbeatInterval = 60 * time.Second

func (input terminalRunToolInput) commandRequest() security.CommandRequest {
	return security.CommandRequest{
		Command:              input.Command,
		WorkingDirectoryPath: input.WorkingDirectoryPath,
		TimeoutSecond:        input.TimeoutSecond,
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerTerminalTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[terminalRunToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "terminal.run",
			Description: "Run one command inside the requester workspace.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Runs workspace commands, build scripts, render checks, or tests.",
				Produces:   "Command stdout, stderr, exit status, and runtime diagnostics.",
				SideEffect: "workspace_write",
				UseWhen:    "You need to execute a toolchain command, build, render, test, list files, or inspect environment state.",
				AvoidWhen:  "A dedicated bundled skill script or typed capability tool can perform the action more directly.",
			},
			InputSchema: terminalRunInputSchema,
		},
		Handler: func(toolContext context.Context, input terminalRunToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.runTerminalRunTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) runTerminalRunTool(toolContext context.Context, input terminalRunToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if errorValue := validateTerminalRunInput(input); errorValue != nil {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "terminal_run", errorValue.Error())
		return normalizedTerminalRunFailure(result), nil
	}
	result, errorValue := toolCatalogBuilder.runTerminalTool(toolContext, input.commandRequest(), handlerContext)
	return normalizedTerminalRunFailure(result), errorValue
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
		if runtimePathFailure := terminalRuntimePathFailure(input, commandResult, content); runtimePathFailure != nil {
			return *runtimePathFailure, nil
		}
		document := terminalCommandResult(commandResult, false)
		content = marshalToolResult(document)
		return agent.ToolFailureWithOutput(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_run", content, json.RawMessage(content)), nil
	}
	document := terminalCommandResult(commandResult, true)
	content = marshalToolResult(document)
	return agent.ToolSuccessData(content, json.RawMessage(content)), nil
}

func terminalCommandResult(commandResult security.CommandResult, isCompleted bool) terminalCommandResultDocument {
	return terminalCommandResultDocument{
		Mode:          terminalRunModeCommand,
		Completed:     isCompleted && commandResult.ExitCode == 0 && !commandResult.TimedOut,
		ExitCode:      commandResult.ExitCode,
		Stdout:        commandResult.Stdout,
		Stderr:        commandResult.Stderr,
		TimedOut:      commandResult.TimedOut,
		OutputTrimmed: commandResult.OutputTrimmed,
	}
}

func normalizedTerminalRunFailure(result agent.ToolResult) agent.ToolResult {
	if !result.Failed() {
		return result
	}
	document := terminalFailureDocument(result)
	document["mode"] = terminalRunModeCommand
	document["completed"] = false
	completeTerminalCommandFailureDocument(document, result)
	data := json.RawMessage(marshalToolResult(document))
	result.Output.Data = data
	return result
}

func terminalFailureDocument(result agent.ToolResult) map[string]any {
	document := map[string]any{}
	_ = json.Unmarshal(result.Output.Data, &document)
	return document
}

func completeTerminalCommandFailureDocument(document map[string]any, result agent.ToolResult) {
	setTerminalFailureDefault(document, "exitCode", -1)
	setTerminalFailureDefault(document, "stdout", "")
	setTerminalFailureDefault(document, "stderr", terminalFailureSummary(result))
	setTerminalFailureDefault(document, "timedOut", false)
	setTerminalFailureDefault(document, "outputTrimmed", false)
}

func setTerminalFailureDefault(document map[string]any, fieldName string, value any) {
	if _, isFound := document[fieldName]; !isFound {
		document[fieldName] = value
	}
}

func terminalFailureSummary(result agent.ToolResult) string {
	if result.Failure != nil && strings.TrimSpace(result.Failure.UserSafeSummary) != "" {
		return strings.TrimSpace(result.Failure.UserSafeSummary)
	}
	return strings.TrimSpace(result.Output.Content)
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

func (toolCatalogBuilder *ToolCatalogBuilder) terminalEnvironmentVariables(environmentVariables map[string]string, scope WorkspaceScope) map[string]string {
	mergedEnvironmentVariables := mergeWorkspaceEnvironment(environmentVariables, scope.EnvironmentVariables())
	if builtinSkillsPythonPath := strings.TrimSpace(os.Getenv("BLUECLAW_BUILTIN_SKILLS_PYTHON")); builtinSkillsPythonPath != "" {
		mergedEnvironmentVariables["BLUECLAW_BUILTIN_SKILLS_PYTHON"] = builtinSkillsPythonPath
	}
	if endpoint := strings.TrimSpace(toolCatalogBuilder.capabilityClient.Endpoint); endpoint != "" {
		mergedEnvironmentVariables["CAPABILITY_BRIDGE_URL"] = endpoint
	}
	return toolCatalogBuilder.resolveAgentWorkspaceEnvironment(mergedEnvironmentVariables)
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
