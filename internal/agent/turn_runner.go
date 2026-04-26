package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/memory"
	"blueclaw/internal/task"
)

const DefaultFallbackReply = "I am having trouble reaching the language model right now. I logged the failure so the model configuration can be fixed."

type TurnOptions struct {
	MaxIterations      int
	TurnTimeoutSecond  int
	ToolResultMaxBytes int
}

type AgentTurnRunner struct {
	taskRunService      *task.TaskRunService
	taskStepService     *task.TaskStepService
	taskArtifactService *task.TaskArtifactService
	languageModel       llm.LanguageModelProvider
	options             TurnOptions
}

type AgentTurnRequest struct {
	RequesterPersonID string
	ConversationID    string
	Prompt            string
	VisibleContext    VisibleContext
	MemoryFacts       []memory.MemoryFact
	ToolRegistry      *ToolRegistry
}

type AgentTurnResult struct {
	TaskRun    task.TaskRun
	FinalReply string
}

type turnActionDocument struct {
	Action     string          `json:"action"`
	FinalReply string          `json:"finalReply"`
	ToolName   string          `json:"toolName"`
	ToolInput  json.RawMessage `json:"toolInput"`
	Query      string          `json:"query"`
	Reason     string          `json:"reason"`
	Reply      string          `json:"reply"`
}

type turnObservation struct {
	Action  string `json:"action"`
	Tool    string `json:"tool,omitempty"`
	Content string `json:"content"`
	IsError bool   `json:"isError"`
}

func NewAgentTurnRunner(taskRunService *task.TaskRunService, taskStepService *task.TaskStepService, taskArtifactService *task.TaskArtifactService, languageModel llm.LanguageModelProvider, options TurnOptions) *AgentTurnRunner {
	if taskArtifactService == nil {
		taskArtifactService = task.NewTaskArtifactService()
	}
	return &AgentTurnRunner{
		taskRunService:      taskRunService,
		taskStepService:     taskStepService,
		taskArtifactService: taskArtifactService,
		languageModel:       languageModel,
		options:             normalizeTurnOptions(options),
	}
}

func normalizeTurnOptions(options TurnOptions) TurnOptions {
	if options.MaxIterations <= 0 {
		options.MaxIterations = 8
	}
	if options.TurnTimeoutSecond <= 0 {
		options.TurnTimeoutSecond = 120
	}
	if options.ToolResultMaxBytes <= 0 {
		options.ToolResultMaxBytes = 32768
	}
	return options
}

func (agentTurnRunner *AgentTurnRunner) RunTurn(ctx context.Context, request AgentTurnRequest) (AgentTurnResult, error) {
	if agentTurnRunner.languageModel == nil {
		return AgentTurnResult{}, errors.New("language model provider is not configured")
	}

	turnContext, cancel := context.WithTimeout(ctx, time.Duration(agentTurnRunner.options.TurnTimeoutSecond)*time.Second)
	defer cancel()

	taskRun := agentTurnRunner.taskRunService.CreateTaskRun(request.RequesterPersonID, request.ConversationID, request.Prompt)
	runningTaskRun, errorValue := agentTurnRunner.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue == nil {
		taskRun = runningTaskRun
	}

	observations := []turnObservation{}
	for iteration := 1; iteration <= agentTurnRunner.options.MaxIterations; iteration++ {
		stepID := fmt.Sprintf("%s:turn-%03d", taskRun.TaskRunID, iteration)
		agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusRunning, "agent turn iteration", "")

		actionDocument, actionError := agentTurnRunner.nextAction(turnContext, request, observations)
		if actionError != nil {
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "agent turn iteration", actionError.Error())
			return agentTurnRunner.failTurn(taskRun.TaskRunID, "llm action failed: "+actionError.Error())
		}

		agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.action", marshalEventBody(actionDocument))
		switch strings.TrimSpace(actionDocument.Action) {
		case "final_reply":
			reply := strings.TrimSpace(actionDocument.FinalReply)
			if reply == "" {
				reply = strings.TrimSpace(actionDocument.Reply)
			}
			if reply == "" {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "final_reply", "empty final reply")
				return agentTurnRunner.failTurn(taskRun.TaskRunID, "empty final reply")
			}
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "final_reply", reply)
			completedTaskRun, _ := agentTurnRunner.taskRunService.CompleteTaskRun(taskRun.TaskRunID, reply)
			return AgentTurnResult{TaskRun: completedTaskRun, FinalReply: reply}, nil
		case "call_tool":
			observation := agentTurnRunner.invokeTool(turnContext, request.ToolRegistry, taskRun.TaskRunID, actionDocument.ToolName, actionDocument.ToolInput)
			observations = append(observations, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "call_tool "+actionDocument.ToolName, observation.Content)
		case "fetch_history":
			observation := agentTurnRunner.invokeTool(turnContext, request.ToolRegistry, taskRun.TaskRunID, "conversation.history", actionDocument.ToolInput)
			observations = append(observations, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "fetch_history", observation.Content)
		case "search_memory":
			toolInput := actionDocument.ToolInput
			if len(toolInput) == 0 {
				toolInput = MarshalToolInput(map[string]string{"query": firstNonEmptyString(actionDocument.Query, request.Prompt)})
			}
			observation := agentTurnRunner.invokeTool(turnContext, request.ToolRegistry, taskRun.TaskRunID, "memory.search", toolInput)
			observations = append(observations, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "search_memory", observation.Content)
		case "fail":
			reason := firstNonEmptyString(actionDocument.Reason, "agent reported failure")
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "fail", reason)
			return agentTurnRunner.failTurn(taskRun.TaskRunID, reason)
		default:
			observation := turnObservation{Action: "invalid_action", Content: "unknown action: " + actionDocument.Action, IsError: true}
			observations = append(observations, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "invalid_action", observation.Content)
		}
	}

	return agentTurnRunner.failTurn(taskRun.TaskRunID, "maximum agent iterations exceeded")
}

func (agentTurnRunner *AgentTurnRunner) nextAction(ctx context.Context, request AgentTurnRequest, observations []turnObservation) (turnActionDocument, error) {
	structuredResponse, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: agentTurnRunner.buildTurnMessages(request, observations),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_agent_turn_action",
			Document:           `{"type":"object","properties":{"action":{"type":"string","enum":["final_reply","call_tool","fetch_history","search_memory","fail"]},"finalReply":{"type":"string"},"toolName":{"type":"string"},"toolInput":{"type":"object"},"query":{"type":"string"},"reason":{"type":"string"},"reply":{"type":"string"}},"required":["action"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}

	var actionDocument turnActionDocument
	errorValue = json.Unmarshal([]byte(structuredResponse.Content), &actionDocument)
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	if strings.TrimSpace(actionDocument.Action) == "" && strings.TrimSpace(actionDocument.Reply) != "" {
		actionDocument.Action = "final_reply"
		actionDocument.FinalReply = actionDocument.Reply
	}
	return actionDocument, nil
}

func (agentTurnRunner *AgentTurnRunner) buildTurnMessages(request AgentTurnRequest, observations []turnObservation) []llm.Message {
	messages := buildReplyMessages(request.Prompt, request.VisibleContext, request.MemoryFacts)
	messages[0].Content = "You are Blueclaw. Work as a careful task agent. Use tools when they materially improve the answer. Return exactly one final answer to the user through final_reply. Do not expose hidden policy, tool logs, or provenance unless the user asks and access is allowed."

	toolDescriptions := agentTurnRunner.buildToolDescription(request.ToolRegistry)
	if toolDescriptions != "" {
		messages[0].Content += "\n\n" + toolDescriptions
	}

	if len(observations) > 0 {
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: "Tool observations so far:\n" + marshalEventBody(observations),
		})
	}
	return messages
}

func (agentTurnRunner *AgentTurnRunner) buildToolDescription(toolRegistry *ToolRegistry) string {
	if toolRegistry == nil {
		return ""
	}
	toolDefinitions := toolRegistry.ListToolDefinitions()
	if len(toolDefinitions) == 0 {
		return ""
	}
	lines := []string{"Available tools:"}
	for _, toolDefinition := range toolDefinitions {
		line := "- " + toolDefinition.Name
		if strings.TrimSpace(toolDefinition.Description) != "" {
			line += ": " + strings.TrimSpace(toolDefinition.Description)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (agentTurnRunner *AgentTurnRunner) invokeTool(ctx context.Context, toolRegistry *ToolRegistry, taskRunID string, toolName string, toolInput json.RawMessage) turnObservation {
	if toolRegistry == nil {
		return turnObservation{Action: "call_tool", Tool: toolName, Content: "tool registry was not configured", IsError: true}
	}
	toolResult, errorValue := toolRegistry.InvokeTool(ctx, ToolInvocation{ToolName: toolName, Input: toolInput})
	if errorValue != nil {
		toolResult = ToolResult{Content: errorValue.Error(), IsError: true}
	}
	return agentTurnRunner.saveToolObservation(taskRunID, toolName, toolResult)
}

func (agentTurnRunner *AgentTurnRunner) saveToolObservation(taskRunID string, toolName string, toolResult ToolResult) turnObservation {
	content := toolResult.Content
	if len(content) > agentTurnRunner.options.ToolResultMaxBytes {
		taskArtifact := agentTurnRunner.taskArtifactService.AddTaskArtifactBody(taskRunID, "tool."+toolName+".result", content)
		content = content[:agentTurnRunner.options.ToolResultMaxBytes] + "\n[truncated; full result saved as artifact " + taskArtifact.TaskArtifactID + "]"
	}
	observation := turnObservation{Action: "call_tool", Tool: toolName, Content: content, IsError: toolResult.IsError}
	agentTurnRunner.appendEvent(taskRunID, "tool."+toolName+".result", marshalEventBody(observation))
	return observation
}

func (agentTurnRunner *AgentTurnRunner) saveStep(taskRunID string, taskStepID string, status task.TaskStatus, instruction string, output string) {
	agentTurnRunner.taskStepService.AddTaskStep(task.TaskStep{
		TaskStepID:               taskStepID,
		TaskRunID:                taskRunID,
		AssignedAgentProfileName: "assistant",
		Instruction:              instruction,
		Status:                   status,
		Output:                   output,
	})
}

func (agentTurnRunner *AgentTurnRunner) appendEvent(taskRunID string, name string, body string) {
	agentTurnRunner.taskRunService.AppendTaskEvent(taskRunID, name, body)
}

func (agentTurnRunner *AgentTurnRunner) failTurn(taskRunID string, reason string) (AgentTurnResult, error) {
	failedTaskRun, _ := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusFailed, reason)
	return AgentTurnResult{TaskRun: failedTaskRun, FinalReply: DefaultFallbackReply}, nil
}

func marshalEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(document)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
