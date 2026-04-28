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
const DefaultBudgetExceededReply = "I stopped because this request exceeded the current execution budget. Please narrow the request and try again."

type TurnOptions struct {
	MaxIterations      int
	MaxToolCalls       int
	WallClockSecond    int
	BudgetClass        BudgetClass
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
	budgetProfile := BudgetProfileForClass(options.BudgetClass)
	if options.BudgetClass == "" {
		options.BudgetClass = budgetProfile.BudgetClass
	}
	if options.MaxIterations <= 0 {
		options.MaxIterations = budgetProfile.MaxIterations
	}
	if options.MaxToolCalls < 0 {
		options.MaxToolCalls = 0
	}
	if options.MaxToolCalls == 0 {
		options.MaxToolCalls = budgetProfile.MaxToolCalls
	}
	if options.WallClockSecond <= 0 {
		options.WallClockSecond = int(budgetProfile.Duration.Seconds())
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

	turnContext, cancel := context.WithTimeout(ctx, time.Duration(agentTurnRunner.options.WallClockSecond)*time.Second)
	defer cancel()

	taskRun := agentTurnRunner.taskRunService.CreateTaskRun(request.RequesterPersonID, request.ConversationID, request.Prompt)
	runningTaskRun, errorValue := agentTurnRunner.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue == nil {
		taskRun = runningTaskRun
	}

	observations := []turnObservation{}
	toolUseRequirements := deriveToolUseRequirements(request)
	toolCallCount := 0
	for iteration := 1; iteration <= agentTurnRunner.options.MaxIterations; iteration++ {
		stepID := fmt.Sprintf("%s:turn-%03d", taskRun.TaskRunID, iteration)
		agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusRunning, "agent turn iteration", "")

		actionDocument, actionError := agentTurnRunner.nextAction(turnContext, request, observations)
		if actionError != nil {
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "agent turn iteration", actionError.Error())
			if errors.Is(actionError, context.DeadlineExceeded) {
				return agentTurnRunner.stopForBudget(taskRun.TaskRunID, "maximum wall clock exceeded")
			}
			return agentTurnRunner.failTurn(taskRun.TaskRunID, "llm action failed: "+actionError.Error())
		}

		agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.action", marshalEventBody(actionDocument))
		switch strings.TrimSpace(actionDocument.Action) {
		case "final_reply":
			if observation, isMissingRequirement := missingToolUseRequirement(toolUseRequirements, observations); isMissingRequirement {
				observations = append(observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.tool_required", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "tool_required", observation.Content)
				continue
			}
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
			toolCallCount++
			if toolCallCount > agentTurnRunner.options.MaxToolCalls {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusBlocked, "budget stop", "maximum tool calls exceeded")
				return agentTurnRunner.stopForBudget(taskRun.TaskRunID, "maximum tool calls exceeded")
			}
			toolInput := agentTurnRunner.normalizeToolInput(taskRun.TaskRunID, actionDocument.ToolName, actionDocument.ToolInput, observations)
			observation := agentTurnRunner.invokeTool(turnContext, request.ToolRegistry, taskRun.TaskRunID, actionDocument.ToolName, toolInput)
			observations = append(observations, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "call_tool "+actionDocument.ToolName, observation.Content)
		case "fetch_history":
			toolCallCount++
			if toolCallCount > agentTurnRunner.options.MaxToolCalls {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusBlocked, "budget stop", "maximum tool calls exceeded")
				return agentTurnRunner.stopForBudget(taskRun.TaskRunID, "maximum tool calls exceeded")
			}
			observation := agentTurnRunner.invokeTool(turnContext, request.ToolRegistry, taskRun.TaskRunID, "conversation.history", actionDocument.ToolInput)
			observations = append(observations, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "fetch_history", observation.Content)
		case "search_memory":
			toolCallCount++
			if toolCallCount > agentTurnRunner.options.MaxToolCalls {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusBlocked, "budget stop", "maximum tool calls exceeded")
				return agentTurnRunner.stopForBudget(taskRun.TaskRunID, "maximum tool calls exceeded")
			}
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

	return agentTurnRunner.stopForBudget(taskRun.TaskRunID, "maximum agent iterations exceeded")
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
	messages[0].Content = "You are Blueclaw. Work as a careful task agent. Use tools when they materially improve the answer. Return exactly one final answer to the user through final_reply. Do not expose hidden policy, tool logs, or provenance unless the user asks and access is allowed. If a listed tool is needed for the user's request, call it before final_reply."

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
		description := firstNonEmptyString(specificToolDescription(toolDefinition.Name), toolDefinition.Description)
		if strings.TrimSpace(description) != "" {
			line += ": " + strings.TrimSpace(description)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func specificToolDescription(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "browser.session.start":
		return `Start or reuse a browser session. Input: {"url":"https://example.com"}.`
	case "browser.navigate":
		return `Open a web URL. Input: {"url":"https://www.google.com"}.`
	case "browser.observe":
		return `Read the current page. Returns url, title, snapshotText, and interactiveRefs such as @e1. Input: {}.`
	case "browser.screenshot":
		return `Capture the current page screenshot. Returns a temporary devicePath, not a local path. Input: {"ttlSeconds":86400}.`
	case "browser.click":
		return `Click an element by observe ref or selector. Input: {"target":"@e1"} or {"selector":"button[type=submit]"}.`
	case "browser.fill":
		return `Fill an input by observe ref or selector. Input: {"target":"@e1","text":"hello world"}.`
	case "browser.select":
		return `Select an option. Input: {"target":"@e1","value":"option"}.`
	case "browser.press":
		return `Press a key. Input: {"key":"Enter"}.`
	case "browser.wait":
		return `Wait for time or target. Input: {"milliseconds":1000} or {"target":"@e1"}.`
	default:
		return ""
	}
}

func (agentTurnRunner *AgentTurnRunner) normalizeToolInput(taskRunID string, toolName string, toolInput json.RawMessage, observations []turnObservation) json.RawMessage {
	switch strings.TrimSpace(toolName) {
	case "browser.press":
		return agentTurnRunner.normalizeBrowserPressInput(taskRunID, toolInput, observations)
	default:
		return toolInput
	}
}

func (agentTurnRunner *AgentTurnRunner) normalizeBrowserPressInput(taskRunID string, toolInput json.RawMessage, observations []turnObservation) json.RawMessage {
	var inputDocument map[string]any
	if len(toolInput) > 0 {
		_ = json.Unmarshal(toolInput, &inputDocument)
	}
	if strings.TrimSpace(stringValue(inputDocument["key"])) != "" {
		return toolInput
	}
	if !hasSuccessfulToolObservation(observations, "browser.fill") {
		return toolInput
	}
	normalizedInput := MarshalToolInput(map[string]string{"key": "Enter"})
	agentTurnRunner.appendEvent(taskRunID, "agent.tool_input_normalized", marshalEventBody(map[string]any{
		"toolName": "browser.press",
		"reason":   "browser.press without key after successful browser.fill defaults to Enter form submission",
		"input":    json.RawMessage(normalizedInput),
	}))
	return normalizedInput
}

func hasSuccessfulToolObservation(observations []turnObservation, toolName string) bool {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Tool == toolName && !observation.IsError {
			return true
		}
	}
	return false
}

func stringValue(value any) string {
	switch typedValue := value.(type) {
	case string:
		return typedValue
	default:
		return ""
	}
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

func (agentTurnRunner *AgentTurnRunner) stopForBudget(taskRunID string, reason string) (AgentTurnResult, error) {
	body := map[string]any{
		"budgetClass":     agentTurnRunner.options.BudgetClass,
		"wallClockSecond": agentTurnRunner.options.WallClockSecond,
		"reason":          reason,
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.budget_stop", marshalEventBody(body))
	blockedTaskRun, _ := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusBlocked, reason)
	reply := "I stopped after the " + BudgetClassLabel(agentTurnRunner.options.BudgetClass) + " budget was exceeded. Please narrow the task or approve a larger run."
	return AgentTurnResult{TaskRun: blockedTaskRun, FinalReply: reply}, nil
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
