package cliharness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/mcpserver"
	"github.com/Dawn-kim-official/blueclaw/internal/policy"
	"github.com/Dawn-kim-official/blueclaw/internal/security"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

const (
	toolCatalogServerName           = "blueclaw"
	toolCatalogTokenEnvironmentName = "BLUECLAW_TOOL_CATALOG_TOKEN"
)

type ToolCatalogPublisher interface {
	PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (endpointURL string, bearerToken string, revoke func(), errorValue error)
}

type AgentCommand struct {
	Path                       string
	PromptArguments            []string
	ToolCatalogArguments       func(toolCatalogConfigurationPath string) []string
	ToolCatalogInlineArguments func(endpointURL string, bearerTokenEnvironmentName string) []string
	Environment                []string
}

type RequesterProcessRunner interface {
	Requester(context.Context, security.WorkspaceActorRequest) (security.WorkspaceActor, error)
}

type Harness struct {
	agentCommand           AgentCommand
	toolCatalogPublisher   ToolCatalogPublisher
	taskRunStore           taskstate.TaskRunStore
	requesterProcessRunner RequesterProcessRunner
	workspaceRootPath      string
	agentTimeoutSecond     int
}

func New(agentCommand AgentCommand, toolCatalogPublisher ToolCatalogPublisher, taskRunStore taskstate.TaskRunStore) *Harness {
	return &Harness{agentCommand: agentCommand, toolCatalogPublisher: toolCatalogPublisher, taskRunStore: taskRunStore, agentTimeoutSecond: 600}
}

func (harness *Harness) UseRequesterProcessRunner(requesterProcessRunner RequesterProcessRunner, workspaceRootPath string) {
	harness.requesterProcessRunner = requesterProcessRunner
	harness.workspaceRootPath = workspaceRootPath
}

func (harness *Harness) RunTurn(ctx context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if strings.TrimSpace(harness.agentCommand.Path) == "" {
		return agentcontract.AgentTurnResult{}, errors.New("cli harness needs an agent command to run")
	}
	if strings.TrimSpace(request.RequesterPersonID) == "" {
		return agentcontract.AgentTurnResult{}, errors.New("cli harness refuses a turn with no requester, because tools execute as the requester")
	}
	endpointURL, bearerToken, revokeToolCatalog, errorValue := harness.toolCatalogPublisher.PublishToolCatalog(mcpserver.RequesterToolSet{
		RequesterPersonID: request.RequesterPersonID,
		ToolSet:           request.ToolSet,
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	defer revokeToolCatalog()

	configurationPath, errorValue := writeToolCatalogConfiguration(endpointURL, bearerToken)
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	defer os.Remove(configurationPath)

	arguments := append([]string{}, harness.agentCommand.PromptArguments...)
	if harness.agentCommand.ToolCatalogArguments != nil {
		arguments = append(arguments, harness.agentCommand.ToolCatalogArguments(configurationPath)...)
	}
	if harness.agentCommand.ToolCatalogInlineArguments != nil {
		arguments = append(arguments, harness.agentCommand.ToolCatalogInlineArguments(endpointURL, toolCatalogTokenEnvironmentName)...)
	}
	if harness.requesterProcessRunner != nil {
		return harness.runAsRequester(ctx, request, arguments)
	}
	command := exec.CommandContext(ctx, harness.agentCommand.Path, arguments...)
	command.Dir = request.WorkspaceRootPath
	command.Stdin = strings.NewReader(request.Prompt)
	command.Env = append(harness.commandEnvironment(), toolCatalogTokenEnvironmentName+"="+bearerToken)
	standardOutput := &bytes.Buffer{}
	standardError := &bytes.Buffer{}
	command.Stdout = standardOutput
	command.Stderr = standardError
	if errorValue := command.Run(); errorValue != nil {
		return agentcontract.AgentTurnResult{}, errors.New(strings.TrimSpace(standardError.String()) + " (" + errorValue.Error() + ")")
	}
	return harness.turnResult(request, strings.TrimSpace(standardOutput.String())), nil
}

func (harness *Harness) turnResult(request agentcontract.AgentTurnRequest, finishMessage string) agentcontract.AgentTurnResult {
	taskRun := taskstate.TaskRun{Status: taskstate.TaskStatusCompleted}
	if harness.taskRunStore != nil && strings.TrimSpace(request.ExistingTaskRunID) != "" {
		if existingTaskRun, isFound := harness.taskRunStore.FindTaskRun(request.ExistingTaskRunID); isFound {
			taskRun = existingTaskRun
		}
	}
	return agentcontract.AgentTurnResult{TaskRun: taskRun, FinishMessage: finishMessage, UserNotice: finishMessage}
}

func writeToolCatalogConfiguration(endpointURL string, bearerToken string) (string, error) {
	document, errorValue := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			toolCatalogServerName: map[string]any{
				"type":    "http",
				"url":     endpointURL,
				"headers": map[string]string{"Authorization": "Bearer " + bearerToken},
			},
		},
	})
	if errorValue != nil {
		return "", errorValue
	}
	configurationFile, errorValue := os.CreateTemp("", "blueclaw-tool-catalog-*.json")
	if errorValue != nil {
		return "", errorValue
	}
	defer configurationFile.Close()
	if _, errorValue := configurationFile.Write(document); errorValue != nil {
		return "", errorValue
	}
	return configurationFile.Name(), nil
}

func ClaudeCodeAgentCommand(commandPath string, disallowedToolNames []string) AgentCommand {
	return AgentCommand{
		Path:            commandPath,
		PromptArguments: []string{"--print", "--strict-mcp-config", "--disallowedTools", strings.Join(disallowedToolNames, ",")},
		ToolCatalogArguments: func(toolCatalogConfigurationPath string) []string {
			return []string{"--mcp-config", toolCatalogConfigurationPath}
		},
	}
}

func (harness *Harness) runAsRequester(ctx context.Context, request agentcontract.AgentTurnRequest, arguments []string) (agentcontract.AgentTurnResult, error) {
	workspaceRootPath := harness.workspaceRootPath
	if strings.TrimSpace(workspaceRootPath) == "" {
		workspaceRootPath = request.WorkspaceRootPath
	}
	requesterActor, errorValue := harness.requesterProcessRunner.Requester(ctx, security.WorkspaceActorRequest{
		PersonAccess:      policy.PersonAccess{PersonID: request.RequesterPersonID},
		WorkspaceRootPath: workspaceRootPath,
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	commandResult, errorValue := requesterActor.Run(ctx, security.CommandRequest{
		ExecutableName:       harness.agentCommand.Path,
		Arguments:            arguments,
		Stdin:                request.Prompt,
		WorkingDirectoryPath: request.WorkspaceRootPath,
		TimeoutSecond:        harness.agentTimeoutSecond,
		OutputMaximumBytes:   1 << 20,
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	if commandResult.ExitCode != 0 {
		return agentcontract.AgentTurnResult{}, errors.New(strings.TrimSpace(commandResult.Stderr))
	}
	return harness.turnResult(request, strings.TrimSpace(commandResult.Stdout)), nil
}

func CodexAgentCommand(commandPath string) AgentCommand {
	return AgentCommand{
		Path:            commandPath,
		PromptArguments: []string{"exec", "--sandbox", "read-only", "--skip-git-repo-check"},
		ToolCatalogArguments: func(toolCatalogConfigurationPath string) []string {
			return nil
		},
		ToolCatalogInlineArguments: func(endpointURL string, bearerTokenEnvironmentName string) []string {
			return []string{"-c", "mcp_servers." + toolCatalogServerName + "={url=" + strconv.Quote(endpointURL) + ",bearer_token_env_var=" + strconv.Quote(bearerTokenEnvironmentName) + "}"}
		},
	}
}

func (harness *Harness) commandEnvironment() []string {
	if len(harness.agentCommand.Environment) > 0 {
		return append([]string{}, harness.agentCommand.Environment...)
	}
	return os.Environ()
}
