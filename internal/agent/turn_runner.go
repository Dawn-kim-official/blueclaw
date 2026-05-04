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
const StaticLimitReachedReply = "I hit the limit for this run before I could finish cleanly. I saved the task state so it can be resumed or retried with a larger scope."

type TurnOptions struct {
	MaxIterationCount  int
	MaxToolCallCount   int
	MaxElapsedSecond   int
	EffortLevel        EffortLevel
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
	RequesterPersonID          string
	RequesterName              string
	RequesterCallingName       string
	RequesterHandle            string
	ProfileName                string
	ConversationID             string
	Prompt                     string
	VisibleContext             VisibleContext
	MemoryFacts                []memory.MemoryFact
	ToolRegistry               *ToolRegistry
	WorkspaceRootPath          string
	InstructionPrompt          string
	InstructionSources         []InstructionSource
	SkillDecisions             []SkillSelectionDecision
	RequiredEvidenceTools      []string
	RequiredAttachmentSuffixes []string
	QualityRecommendedChecks   []string
	TurnStartedAt              time.Time
}

type AgentTurnResult struct {
	TaskRun     task.TaskRun
	FinalReply  string
	Attachments []FileAttachment
}

type turnActionDocument struct {
	Action             string                        `json:"action"`
	FinalReply         string                        `json:"finalReply"`
	ToolName           string                        `json:"toolName"`
	ToolInput          json.RawMessage               `json:"toolInput"`
	Query              string                        `json:"query"`
	Reason             string                        `json:"reason"`
	Reply              string                        `json:"reply"`
	GoalStatus         string                        `json:"goalStatus"`
	GoalSatisfied      *bool                         `json:"goalSatisfied"`
	CompletionEvidence []completionEvidenceReference `json:"completionEvidence"`
	RemainingWork      string                        `json:"remainingWork"`
}

type turnObservation struct {
	ObservationID string           `json:"observationID"`
	Action        string           `json:"action"`
	Tool          string           `json:"tool,omitempty"`
	Content       string           `json:"content"`
	Summary       string           `json:"summary,omitempty"`
	IsError       bool             `json:"isError"`
	Attachments   []FileAttachment `json:"attachments,omitempty"`
}

type completionEvidenceReference struct {
	ObservationID   string `json:"observationID"`
	ToolName        string `json:"toolName"`
	AttachmentIndex *int   `json:"attachmentIndex,omitempty"`
}

type completionGateResult struct {
	IsSatisfied   bool
	Message       string
	Attachments   []FileAttachment
	ValidityState ValidityState
}

type completionTransition struct {
	Observations  []turnObservation
	Attachments   []FileAttachment
	Result        AgentTurnResult
	IsCompleted   bool
	DidTransition bool
	Action        completionRecommendedAction
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
	effortProfile := EffortLimitProfileForLevel(options.EffortLevel)
	if options.EffortLevel == "" {
		options.EffortLevel = effortProfile.EffortLevel
	}
	if options.MaxIterationCount <= 0 {
		options.MaxIterationCount = effortProfile.MaxIterationCount
	}
	if options.MaxToolCallCount < 0 {
		options.MaxToolCallCount = 0
	}
	if options.MaxToolCallCount == 0 {
		options.MaxToolCallCount = effortProfile.MaxToolCallCount
	}
	if options.MaxElapsedSecond <= 0 {
		options.MaxElapsedSecond = int(effortProfile.Duration.Seconds())
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

	turnContext, cancel := context.WithTimeout(ctx, time.Duration(agentTurnRunner.options.MaxElapsedSecond)*time.Second)
	defer cancel()
	if request.TurnStartedAt.IsZero() {
		request.TurnStartedAt = time.Now().Add(-2 * time.Second)
	}

	taskRun := agentTurnRunner.taskRunService.CreateTaskRun(request.RequesterPersonID, request.ConversationID, request.Prompt)
	runningTaskRun, errorValue := agentTurnRunner.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue == nil {
		taskRun = runningTaskRun
	}
	agentTurnRunner.appendInstructionEvent(taskRun.TaskRunID, request)

	observations := []turnObservation{}
	attachments := []FileAttachment{}
	toolUseRequirements := deriveToolUseRequirements(request)
	toolCallCount := 0
	successfulToolCalls := map[string]turnObservation{}
	limitPressureWarnings := map[string]bool{}
	for iteration := 1; iteration <= agentTurnRunner.options.MaxIterationCount; iteration++ {
		if warning := agentTurnRunner.nextLimitPressureWarning(iteration-1, toolCallCount, len(observations)+1, limitPressureWarnings); warning != nil {
			observations = append(observations, warning.Observation)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.limit_pressure", marshalEventBody(warning.EventBody))
			limitPressureWarnings[warning.Level] = true
		}
		stepID := fmt.Sprintf("%s:turn-%03d", taskRun.TaskRunID, iteration)
		agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusRunning, "agent turn iteration", "")

		transition := agentTurnRunner.applyCompletionState(turnContext, taskRun.TaskRunID, stepID, request, toolUseRequirements, observations, attachments)
		observations = transition.Observations
		attachments = transition.Attachments
		if transition.IsCompleted {
			return transition.Result, nil
		}
		if transition.DidTransition {
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "completion_state "+string(transition.Action), "")
			continue
		}

		actionDocument, actionError := agentTurnRunner.nextAction(turnContext, request, observations)
		if actionError != nil {
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "agent turn iteration", actionError.Error())
			if errors.Is(actionError, context.DeadlineExceeded) {
				return agentTurnRunner.stopForLimit(taskRun.TaskRunID, request, "max_elapsed", observations, attachments, iteration-1, toolCallCount)
			}
			return agentTurnRunner.failTurn(taskRun.TaskRunID, "llm action failed: "+actionError.Error())
		}

		agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.action", marshalEventBody(actionDocument))
		switch strings.TrimSpace(actionDocument.Action) {
		case "final_reply":
			completionGateResult := validateCompletionGateForRequest(request, toolUseRequirements, observations, actionDocument)
			agentTurnRunner.appendValidityReview(taskRun.TaskRunID, "final_reply", completionGateResult.ValidityState)
			if !completionGateResult.IsSatisfied {
				observation := turnObservation{
					ObservationID: nextObservationID(len(observations) + 1),
					Action:        "policy",
					Content:       completionGateResult.Message,
					IsError:       true,
				}
				observations = append(observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.completion_required", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "completion_required", observation.Content)
				continue
			}
			agentTurnRunner.appendQualityReview(taskRun.TaskRunID, request, observations, completionGateResult.ValidityState)
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
			return AgentTurnResult{TaskRun: completedTaskRun, FinalReply: reply, Attachments: completionGateResult.Attachments}, nil
		case "call_tool":
			if validationError := validateBrowserToolInput(actionDocument.ToolName, actionDocument.ToolInput); validationError != nil {
				observation := turnObservation{ObservationID: nextObservationID(len(observations) + 1), Action: "call_tool", Tool: strings.TrimSpace(actionDocument.ToolName), Content: validationError.Error(), IsError: true}
				observations = append(observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.tool_input_malformed", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "malformed_tool_input "+actionDocument.ToolName, observation.Content)
				continue
			}
			if duplicateObservation, isDuplicate := successfulToolCalls[canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)]; isDuplicate && handlesDuplicateSuccessfulToolCall(actionDocument.ToolName) {
				observation := turnObservation{
					ObservationID: nextObservationID(len(observations) + 1),
					Action:        "policy",
					Tool:          strings.TrimSpace(actionDocument.ToolName),
					Content:       "This exact tool call already succeeded as " + duplicateObservation.ObservationID + ". Use that observation for completionEvidence instead of running it again.",
					IsError:       true,
				}
				observations = append(observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.duplicate_tool_call_rejected", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "duplicate_tool_call "+actionDocument.ToolName, observation.Content)
				continue
			}
			toolCallCount++
			if toolCallCount > agentTurnRunner.options.MaxToolCallCount {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusBlocked, "limit stop", "max_tool_calls")
				return agentTurnRunner.finalizeOrStopForLimit(turnContext, taskRun.TaskRunID, request, "max_tool_calls", toolUseRequirements, observations, attachments, iteration, agentTurnRunner.options.MaxToolCallCount)
			}
			observation := agentTurnRunner.invokeTool(turnContext, request.ToolRegistry, taskRun.TaskRunID, nextObservationID(len(observations)+1), actionDocument.ToolName, actionDocument.ToolInput)
			observations = append(observations, observation)
			attachments = appendObservationAttachments(attachments, observation)
			if !observation.IsError {
				successfulToolCalls[canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)] = observation
			}
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "call_tool "+actionDocument.ToolName, observation.Content)
		case "fetch_history":
			toolCallCount++
			if toolCallCount > agentTurnRunner.options.MaxToolCallCount {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusBlocked, "limit stop", "max_tool_calls")
				return agentTurnRunner.finalizeOrStopForLimit(turnContext, taskRun.TaskRunID, request, "max_tool_calls", toolUseRequirements, observations, attachments, iteration, agentTurnRunner.options.MaxToolCallCount)
			}
			observation := agentTurnRunner.invokeTool(turnContext, request.ToolRegistry, taskRun.TaskRunID, nextObservationID(len(observations)+1), "conversation.history", actionDocument.ToolInput)
			observations = append(observations, observation)
			attachments = appendObservationAttachments(attachments, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "fetch_history", observation.Content)
		case "search_memory":
			toolCallCount++
			if toolCallCount > agentTurnRunner.options.MaxToolCallCount {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusBlocked, "limit stop", "max_tool_calls")
				return agentTurnRunner.finalizeOrStopForLimit(turnContext, taskRun.TaskRunID, request, "max_tool_calls", toolUseRequirements, observations, attachments, iteration, agentTurnRunner.options.MaxToolCallCount)
			}
			toolInput := actionDocument.ToolInput
			if len(toolInput) == 0 {
				toolInput = MarshalToolInput(map[string]string{"query": firstNonEmptyString(actionDocument.Query, request.Prompt)})
			}
			observation := agentTurnRunner.invokeTool(turnContext, request.ToolRegistry, taskRun.TaskRunID, nextObservationID(len(observations)+1), "memory.search", toolInput)
			observations = append(observations, observation)
			attachments = appendObservationAttachments(attachments, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "search_memory", observation.Content)
		case "fail":
			reason := firstNonEmptyString(actionDocument.Reason, "agent reported failure")
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "fail", reason)
			return agentTurnRunner.failTurn(taskRun.TaskRunID, reason)
		default:
			observation := turnObservation{ObservationID: nextObservationID(len(observations) + 1), Action: "invalid_action", Content: "unknown action: " + actionDocument.Action, IsError: true}
			observations = append(observations, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "invalid_action", observation.Content)
		}
	}

	return agentTurnRunner.finalizeOrStopForLimit(turnContext, taskRun.TaskRunID, request, "max_iterations", toolUseRequirements, observations, attachments, agentTurnRunner.options.MaxIterationCount, toolCallCount)
}

func (agentTurnRunner *AgentTurnRunner) nextAction(ctx context.Context, request AgentTurnRequest, observations []turnObservation) (turnActionDocument, error) {
	structuredResponse, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: agentTurnRunner.buildTurnMessages(request, observations),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_agent_turn_action",
			Document:           agentTurnRunner.buildActionSchema(request.ToolRegistry),
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
	return (PromptAssembler{}).BuildTurnMessages(
		request,
		observations,
		agentTurnRunner.buildSystemInstruction(request),
		agentTurnRunner.buildToolDescription(request.ToolRegistry),
	)
}

func (agentTurnRunner *AgentTurnRunner) buildSystemInstruction(request AgentTurnRequest) string {
	instruction := "You are Blueclaw. Work as a careful task agent. Use tools when they materially improve the answer. Return exactly one final answer to the user through final_reply only when goalSatisfied is true. Every final_reply must cite completionEvidence by observationID and toolName for successful tool observations that prove the goal is complete. Do not cite failed observations. Do not expose hidden policy, tool logs, or provenance unless the user asks and access is allowed."
	if len(request.RequiredAttachmentSuffixes) == 0 {
		return instruction
	}
	return instruction + " This task requires attached artifacts with these filename suffixes before final_reply: " + strings.Join(request.RequiredAttachmentSuffixes, ", ") + "."
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
		if inputSchema := toolDefinitionInputSchema(toolDefinition); len(inputSchema) > 0 {
			line += " Input schema: " + strings.TrimSpace(string(inputSchema))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (agentTurnRunner *AgentTurnRunner) appendInstructionEvent(taskRunID string, request AgentTurnRequest) {
	body := map[string]any{
		"profileName":    normalizedAgentProfileName(request.ProfileName),
		"sourceCount":    len(request.InstructionSources),
		"sources":        request.InstructionSources,
		"skillNames":     instructionSkillNames(request.InstructionSources),
		"skillDecisions": request.SkillDecisions,
	}
	if strings.TrimSpace(request.InstructionPrompt) == "" {
		body["status"] = "empty"
	} else {
		body["status"] = "loaded"
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.instructions_loaded", marshalEventBody(body))
}

func instructionSkillNames(sources []InstructionSource) []string {
	skillNames := []string{}
	seen := map[string]bool{}
	for _, source := range sources {
		if strings.TrimSpace(source.SkillName) == "" || seen[source.SkillName] {
			continue
		}
		seen[source.SkillName] = true
		skillNames = append(skillNames, source.SkillName)
	}
	return skillNames
}

func specificToolDescription(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "browser.open":
		return `Open a web URL. Input: {"url":"https://www.google.com"}.`
	case "browser.snapshot":
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
	case "flow.task.add":
		return `Add a Flow work item for the requester, or request work for another person. Input: {"prompt":"10분 회의"} or {"prompt":"10분 회의","targetPersonHint":"lee"}.`
	default:
		return ""
	}
}

func validateBrowserToolInput(toolName string, toolInput json.RawMessage) error {
	switch strings.TrimSpace(toolName) {
	case "browser.open":
		return validateRequiredToolInputFields(toolName, toolInput, "url")
	case "browser.fill":
		return validateBrowserTargetToolInput(toolName, toolInput, "text")
	case "browser.click":
		return validateBrowserTargetToolInput(toolName, toolInput)
	case "browser.select":
		return validateBrowserTargetToolInput(toolName, toolInput, "value")
	case "browser.press":
		return validateRequiredToolInputFields(toolName, toolInput, "key")
	case "browser.wait":
		return validateBrowserWaitInput(toolInput)
	default:
		return nil
	}
}

func validateBrowserTargetToolInput(toolName string, toolInput json.RawMessage, fieldNames ...string) error {
	inputDocument, errorValue := parseToolInputDocument(toolName, toolInput)
	if errorValue != nil {
		return errorValue
	}
	missingFieldNames := []string{}
	if firstNonEmptyString(stringValue(inputDocument["target"]), stringValue(inputDocument["ref"]), stringValue(inputDocument["selector"])) == "" {
		missingFieldNames = append(missingFieldNames, "target/ref/selector")
	}
	for _, fieldName := range fieldNames {
		if strings.TrimSpace(stringValue(inputDocument[fieldName])) == "" {
			missingFieldNames = append(missingFieldNames, fieldName)
		}
	}
	if len(missingFieldNames) > 0 {
		return errors.New("missing required tool input for " + strings.TrimSpace(toolName) + ": " + strings.Join(missingFieldNames, ", ") + validInputExampleSuffix(toolName))
	}
	return nil
}

func validateRequiredToolInputFields(toolName string, toolInput json.RawMessage, fieldNames ...string) error {
	inputDocument, errorValue := parseToolInputDocument(toolName, toolInput)
	if errorValue != nil {
		return errorValue
	}
	missingFieldNames := []string{}
	for _, fieldName := range fieldNames {
		if strings.TrimSpace(stringValue(inputDocument[fieldName])) == "" {
			missingFieldNames = append(missingFieldNames, fieldName)
		}
	}
	if len(missingFieldNames) > 0 {
		return errors.New("missing required tool input for " + strings.TrimSpace(toolName) + ": " + strings.Join(missingFieldNames, ", ") + validInputExampleSuffix(toolName))
	}
	return nil
}

func validateBrowserWaitInput(toolInput json.RawMessage) error {
	inputDocument, errorValue := parseToolInputDocument("browser.wait", toolInput)
	if errorValue != nil {
		return errorValue
	}
	if strings.TrimSpace(stringValue(inputDocument["target"])) != "" {
		return nil
	}
	if strings.TrimSpace(stringValue(inputDocument["ref"])) != "" {
		return nil
	}
	if strings.TrimSpace(stringValue(inputDocument["selector"])) != "" {
		return nil
	}
	if numberValue(inputDocument["milliseconds"]) > 0 {
		return nil
	}
	return errors.New("missing required tool input for browser.wait: target or milliseconds")
}

func toolDefinitionInputSchema(toolDefinition ToolDefinition) json.RawMessage {
	if len(toolDefinition.InputSchema) > 0 {
		return toolDefinition.InputSchema
	}
	return specificToolInputSchema(toolDefinition.Name)
}

func validInputExampleSuffix(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "browser.open":
		return `. Valid input example: {"url":"https://www.google.com"}`
	case "browser.fill":
		return `. Valid input example: {"target":"@e1","text":"hello world"}`
	case "browser.click":
		return `. Valid input example: {"target":"@e1"}`
	case "browser.select":
		return `. Valid input example: {"target":"@e1","value":"option"}`
	case "browser.press":
		return `. Valid input example: {"key":"Enter"}`
	case "browser.wait":
		return `. Valid input example: {"target":"@e1"} or {"milliseconds":1000}`
	default:
		return ""
	}
}

func parseToolInputDocument(toolName string, toolInput json.RawMessage) (map[string]any, error) {
	inputDocument := map[string]any{}
	if len(toolInput) == 0 {
		return inputDocument, nil
	}
	if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
		return nil, errors.New("tool input for " + strings.TrimSpace(toolName) + " is not valid json: " + errorValue.Error())
	}
	return inputDocument, nil
}

func canonicalToolCallKey(toolName string, toolInput json.RawMessage) string {
	return strings.TrimSpace(toolName) + "\x00" + canonicalToolInput(toolInput)
}

func canonicalToolInput(toolInput json.RawMessage) string {
	if len(toolInput) == 0 {
		return "{}"
	}
	var document any
	if errorValue := json.Unmarshal(toolInput, &document); errorValue != nil {
		return strings.TrimSpace(string(toolInput))
	}
	content, errorValue := json.Marshal(document)
	if errorValue != nil {
		return strings.TrimSpace(string(toolInput))
	}
	return string(content)
}

func handlesDuplicateSuccessfulToolCall(toolName string) bool {
	return strings.TrimSpace(toolName) == "terminal.run"
}

func stringValue(value any) string {
	typedValue, isString := value.(string)
	if !isString {
		return ""
	}
	return typedValue
}

func numberValue(value any) float64 {
	switch typedValue := value.(type) {
	case float64:
		return typedValue
	case int:
		return float64(typedValue)
	default:
		return 0
	}
}

func (agentTurnRunner *AgentTurnRunner) invokeTool(ctx context.Context, toolRegistry *ToolRegistry, taskRunID string, observationID string, toolName string, toolInput json.RawMessage) turnObservation {
	trimmedToolName := strings.TrimSpace(toolName)
	if toolRegistry == nil {
		return turnObservation{ObservationID: observationID, Action: "call_tool", Tool: trimmedToolName, Content: "tool registry was not configured", IsError: true}
	}
	agentTurnRunner.appendEvent(taskRunID, "tool."+trimmedToolName+".requested", marshalEventBody(map[string]any{
		"observationID": observationID,
		"toolName":      trimmedToolName,
		"input":         json.RawMessage(toolInput),
	}))
	toolResult, errorValue := toolRegistry.InvokeTool(WithTaskRunID(ctx, taskRunID), ToolInvocation{ToolName: trimmedToolName, Input: toolInput})
	if errorValue != nil {
		toolResult = ToolResult{Content: errorValue.Error(), IsError: true}
	}
	return agentTurnRunner.saveToolObservation(taskRunID, observationID, trimmedToolName, toolResult)
}

func (agentTurnRunner *AgentTurnRunner) saveToolObservation(taskRunID string, observationID string, toolName string, toolResult ToolResult) turnObservation {
	content := toolResult.Content
	originalContent := content
	artifactID := ""
	if len(content) > agentTurnRunner.options.ToolResultMaxBytes {
		taskArtifact := agentTurnRunner.taskArtifactService.AddTaskArtifactBody(taskRunID, "tool."+toolName+".result", content)
		artifactID = taskArtifact.TaskArtifactID
		content = content[:agentTurnRunner.options.ToolResultMaxBytes] + "\n[truncated; full result saved as artifact " + taskArtifact.TaskArtifactID + "]"
	}
	attachments := []FileAttachment{}
	if !toolResult.IsError {
		attachments = append(attachments, toolResult.Attachments...)
	}
	observation := turnObservation{
		ObservationID: observationID,
		Action:        "call_tool",
		Tool:          toolName,
		Content:       content,
		Summary:       buildToolResultSummary(toolName, originalContent, toolResult.IsError, attachments, artifactID),
		IsError:       toolResult.IsError,
		Attachments:   attachments,
	}
	agentTurnRunner.appendEvent(taskRunID, "tool."+toolName+".result", marshalEventBody(observation))
	return observation
}

func buildToolResultSummary(toolName string, content string, isError bool, attachments []FileAttachment, artifactID string) string {
	observation := turnObservation{
		Tool:        toolName,
		Content:     content,
		IsError:     isError,
		Attachments: attachments,
	}
	summary := summarizeObservationContent(observation)
	if strings.TrimSpace(artifactID) != "" {
		summary = strings.TrimSpace(summary) + " Full result stored as artifact " + strings.TrimSpace(artifactID) + "."
	}
	return strings.TrimSpace(summary)
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

func (agentTurnRunner *AgentTurnRunner) appendValidityReview(taskRunID string, phase string, validityState ValidityState) {
	if len(validityState.CheckedArtifacts) == 0 {
		return
	}
	body := map[string]any{
		"phase":            phase,
		"passed":           validityState.Passed,
		"checkedArtifacts": validityState.CheckedArtifacts,
		"invalidArtifacts": validityState.InvalidArtifacts,
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.validity_review", marshalEventBody(body))
}

func (agentTurnRunner *AgentTurnRunner) appendQualityReview(taskRunID string, request AgentTurnRequest, observations []turnObservation, validityState ValidityState) {
	if len(request.QualityRecommendedChecks) == 0 {
		return
	}
	qualityState := buildQualityState(request, observations, validityState)
	agentTurnRunner.appendEvent(taskRunID, "agent.quality_review", marshalEventBody(qualityState))
}

func (agentTurnRunner *AgentTurnRunner) failTurn(taskRunID string, reason string) (AgentTurnResult, error) {
	failedTaskRun, _ := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusFailed, reason)
	return AgentTurnResult{TaskRun: failedTaskRun, FinalReply: DefaultFallbackReply}, nil
}

func (agentTurnRunner *AgentTurnRunner) applyCompletionState(ctx context.Context, taskRunID string, taskStepID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment) completionTransition {
	state := buildCompletionState(request, requirements, observations)
	switch state.RecommendedAction {
	case completionActionAttachExistingArtifacts:
		return agentTurnRunner.attachCompletionArtifacts(ctx, taskRunID, request, observations, attachments, state)
	case completionActionFinalizeWithEvidence:
		return agentTurnRunner.finalizeCompletionState(taskRunID, taskStepID, request, requirements, observations, attachments, state)
	case completionActionBlockedInvalidArtifact:
		return agentTurnRunner.blockInvalidCompletionArtifacts(taskRunID, observations, attachments, state)
	default:
		return completionTransition{Observations: observations, Attachments: attachments}
	}
}

func (agentTurnRunner *AgentTurnRunner) attachCompletionArtifacts(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation, attachments []FileAttachment, state CompletionState) completionTransition {
	agentTurnRunner.appendValidityReview(taskRunID, "pre_attach", state.ValidityState)
	observation := agentTurnRunner.invokeTool(ctx, request.ToolRegistry, taskRunID, nextObservationID(len(observations)+1), "file.attach", MarshalToolInput(map[string]any{"paths": state.AttachmentPaths}))
	if observation.IsError {
		observation.Content = completionAttachmentFailureContent(observation.Content, state.AttachmentPaths)
	}
	observations = append(observations, observation)
	attachments = appendObservationAttachments(attachments, observation)
	agentTurnRunner.appendEvent(taskRunID, "agent.completion_state_transition", marshalEventBody(map[string]any{
		"action":        completionActionAttachExistingArtifacts,
		"observationID": observation.ObservationID,
		"artifactCount": len(state.AttachmentPaths),
	}))
	return completionTransition{
		Observations:  observations,
		Attachments:   attachments,
		DidTransition: true,
		Action:        completionActionAttachExistingArtifacts,
	}
}

func (agentTurnRunner *AgentTurnRunner) blockInvalidCompletionArtifacts(taskRunID string, observations []turnObservation, attachments []FileAttachment, state CompletionState) completionTransition {
	observation := turnObservation{
		ObservationID: nextObservationID(len(observations) + 1),
		Action:        "policy",
		Content:       invalidCompletionArtifactObservationContent(state),
		IsError:       true,
	}
	observations = append(observations, observation)
	agentTurnRunner.appendValidityReview(taskRunID, "completion_state", state.ValidityState)
	agentTurnRunner.appendEvent(taskRunID, "agent.completion_required", marshalEventBody(observation))
	return completionTransition{
		Observations:  observations,
		Attachments:   attachments,
		DidTransition: true,
		Action:        completionActionBlockedInvalidArtifact,
	}
}

func invalidCompletionArtifactObservationContent(state CompletionState) string {
	lines := []string{validityFailureMessage(state.ValidityState)}
	for _, path := range completionValidityPaths(state) {
		if strings.TrimSpace(path) != "" {
			lines = append(lines, "path: "+strings.TrimSpace(path))
		}
	}
	return strings.Join(lines, "\n")
}

func completionAttachmentFailureContent(content string, paths []string) string {
	trimmedContent := strings.TrimSpace(content)
	if len(paths) == 0 {
		return trimmedContent
	}
	if trimmedContent == "" {
		trimmedContent = "file.attach failed"
	}
	return trimmedContent + "\nrequested paths: " + strings.Join(paths, "\n")
}

func (agentTurnRunner *AgentTurnRunner) finalizeCompletionState(taskRunID string, taskStepID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, state CompletionState) completionTransition {
	actionDocument := completionStateFinalReplyDocument(state)
	completionGateResult := validateCompletionGateForRequest(request, requirements, observations, actionDocument)
	agentTurnRunner.appendValidityReview(taskRunID, "completion_state", completionGateResult.ValidityState)
	if !completionGateResult.IsSatisfied {
		agentTurnRunner.appendEvent(taskRunID, "agent.completion_state_rejected", marshalEventBody(map[string]string{"reason": completionGateResult.Message}))
		return completionTransition{Observations: observations, Attachments: attachments}
	}
	agentTurnRunner.appendQualityReview(taskRunID, request, observations, completionGateResult.ValidityState)
	agentTurnRunner.appendEvent(taskRunID, "agent.completion_state_finalized", marshalEventBody(map[string]any{
		"attachmentCount": len(completionGateResult.Attachments),
		"evidenceCount":   len(state.EvidenceReferences),
	}))
	reply := strings.TrimSpace(actionDocument.FinalReply)
	agentTurnRunner.saveStep(taskRunID, taskStepID, task.TaskStatusCompleted, "completion_state "+string(completionActionFinalizeWithEvidence), reply)
	completedTaskRun, _ := agentTurnRunner.taskRunService.CompleteTaskRun(taskRunID, reply)
	return completionTransition{
		Observations:  observations,
		Attachments:   appendUniqueAttachments(attachments, completionGateResult.Attachments),
		Result:        AgentTurnResult{TaskRun: completedTaskRun, FinalReply: reply, Attachments: completionGateResult.Attachments},
		IsCompleted:   true,
		DidTransition: true,
		Action:        completionActionFinalizeWithEvidence,
	}
}

func completionStateFinalReplyDocument(state CompletionState) turnActionDocument {
	goalSatisfied := true
	return turnActionDocument{
		Action:             "final_reply",
		FinalReply:         completionStateFinalReply(state),
		GoalStatus:         "satisfied",
		GoalSatisfied:      &goalSatisfied,
		CompletionEvidence: state.EvidenceReferences,
	}
}

func completionStateFinalReply(state CompletionState) string {
	filenames := completionStateFilenames(state)
	if len(filenames) == 0 {
		return "요청하신 작업을 완료했습니다."
	}
	return "요청하신 파일을 생성해 첨부했습니다: " + strings.Join(filenames, ", ")
}

func completionStateFilenames(state CompletionState) []string {
	filenames := []string{}
	seenFilename := map[string]bool{}
	for _, evidence := range state.AttachedEvidence {
		filename := strings.TrimSpace(evidence.Filename)
		if filename == "" || seenFilename[filename] {
			continue
		}
		seenFilename[filename] = true
		filenames = append(filenames, filename)
	}
	return filenames
}

func appendObservationAttachments(attachments []FileAttachment, observation turnObservation) []FileAttachment {
	if observation.IsError || len(observation.Attachments) == 0 {
		return attachments
	}
	nextAttachments := append([]FileAttachment{}, attachments...)
	if observation.Tool == "browser.screenshot" {
		nextAttachments = removeBrowserScreenshotAttachments(nextAttachments)
	}
	for _, attachment := range observation.Attachments {
		if strings.TrimSpace(attachment.DevicePath) == "" || hasAttachmentDevicePath(nextAttachments, attachment.DevicePath) {
			continue
		}
		nextAttachments = append(nextAttachments, attachment)
	}
	return nextAttachments
}

func removeBrowserScreenshotAttachments(attachments []FileAttachment) []FileAttachment {
	filteredAttachments := []FileAttachment{}
	for _, attachment := range attachments {
		if strings.HasPrefix(strings.TrimSpace(attachment.Filename), "browser-screenshot-") {
			continue
		}
		filteredAttachments = append(filteredAttachments, attachment)
	}
	return filteredAttachments
}

func hasAttachmentDevicePath(attachments []FileAttachment, devicePath string) bool {
	normalizedDevicePath := strings.TrimSpace(devicePath)
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.DevicePath) == normalizedDevicePath {
			return true
		}
	}
	return false
}

type limitPressureWarning struct {
	Level       string
	Observation turnObservation
	EventBody   map[string]any
}

func (agentTurnRunner *AgentTurnRunner) nextLimitPressureWarning(usedIterationCount int, usedToolCallCount int, observationIndex int, sentWarnings map[string]bool) *limitPressureWarning {
	if sentWarnings["finalize"] {
		return nil
	}
	if agentTurnRunner.options.MaxIterationCount < 10 && agentTurnRunner.options.MaxToolCallCount < 5 {
		return nil
	}
	level := agentTurnRunner.limitPressureLevel(usedIterationCount, usedToolCallCount)
	if level == "" || sentWarnings[level] {
		return nil
	}
	message := limitPressureMessage(level)
	return &limitPressureWarning{
		Level: level,
		Observation: turnObservation{
			ObservationID: nextObservationID(observationIndex),
			Action:        "limit_pressure",
			Content:       message,
		},
		EventBody: map[string]any{
			"level":              level,
			"effortLevel":        agentTurnRunner.options.EffortLevel,
			"usedIterationCount": usedIterationCount,
			"usedToolCallCount":  usedToolCallCount,
		},
	}
}

func (agentTurnRunner *AgentTurnRunner) limitPressureLevel(usedIterationCount int, usedToolCallCount int) string {
	if limitUsageReached(usedIterationCount, agentTurnRunner.options.MaxIterationCount, 90) || limitUsageReached(usedToolCallCount, agentTurnRunner.options.MaxToolCallCount, 90) {
		return "finalize"
	}
	if limitUsageReached(usedIterationCount, agentTurnRunner.options.MaxIterationCount, 70) || limitUsageReached(usedToolCallCount, agentTurnRunner.options.MaxToolCallCount, 70) {
		return "consolidate"
	}
	return ""
}

func limitUsageReached(usedCount int, maxCount int, thresholdPercent int) bool {
	if maxCount <= 0 || usedCount <= 0 {
		return false
	}
	return usedCount*100 >= maxCount*thresholdPercent
}

func limitPressureMessage(level string) string {
	if level == "finalize" {
		return "The current run is very close to its limit. Do not start new tool work. Prepare the best final answer from completed observations."
	}
	return "The current run is getting close to its limit. Consolidate completed work, reuse existing observations, and avoid opening new branches unless essential."
}

func (agentTurnRunner *AgentTurnRunner) finalizeOrStopForLimit(ctx context.Context, taskRunID string, request AgentTurnRequest, reason string, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, usedIterationCount int, usedToolCallCount int) (AgentTurnResult, error) {
	if ctx.Err() == nil {
		transition := agentTurnRunner.applyCompletionState(ctx, taskRunID, taskRunID+":completion", request, requirements, observations, attachments)
		if transition.IsCompleted {
			return transition.Result, nil
		}
		if transition.DidTransition {
			transition = agentTurnRunner.applyCompletionState(ctx, taskRunID, taskRunID+":completion", request, requirements, transition.Observations, transition.Attachments)
			if transition.IsCompleted {
				return transition.Result, nil
			}
			observations = transition.Observations
			attachments = transition.Attachments
		}
		if completionRequirementsHaveEvidence(requirements, observations) {
			if result, isFinalized := agentTurnRunner.finalizeSatisfiedTurn(ctx, taskRunID, request, requirements, observations); isFinalized {
				return result, nil
			}
		}
	}
	return agentTurnRunner.stopForLimit(taskRunID, request, reason, observations, attachments, usedIterationCount, usedToolCallCount)
}

func (agentTurnRunner *AgentTurnRunner) finalizeSatisfiedTurn(ctx context.Context, taskRunID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation) (AgentTurnResult, bool) {
	actionDocument, errorValue := agentTurnRunner.finalizerAction(ctx, request, observations)
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_failed", marshalEventBody(map[string]string{"error": errorValue.Error()}))
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_action", marshalEventBody(actionDocument))
	if strings.TrimSpace(actionDocument.Action) != "final_reply" {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_rejected", marshalEventBody(map[string]string{"reason": "finalizer did not return final_reply"}))
		return AgentTurnResult{}, false
	}
	completionGateResult := validateCompletionGateForRequest(request, requirements, observations, actionDocument)
	if !completionGateResult.IsSatisfied {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_rejected", marshalEventBody(map[string]string{"reason": completionGateResult.Message}))
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendValidityReview(taskRunID, "limit_finalizer", completionGateResult.ValidityState)
	agentTurnRunner.appendQualityReview(taskRunID, request, observations, completionGateResult.ValidityState)
	reply := strings.TrimSpace(actionDocument.FinalReply)
	if reply == "" {
		reply = strings.TrimSpace(actionDocument.Reply)
	}
	if reply == "" {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_rejected", marshalEventBody(map[string]string{"reason": "empty final reply"}))
		return AgentTurnResult{}, false
	}
	completedTaskRun, _ := agentTurnRunner.taskRunService.CompleteTaskRun(taskRunID, reply)
	return AgentTurnResult{TaskRun: completedTaskRun, FinalReply: reply, Attachments: completionGateResult.Attachments}, true
}

func (agentTurnRunner *AgentTurnRunner) finalizerAction(ctx context.Context, request AgentTurnRequest, observations []turnObservation) (turnActionDocument, error) {
	messages := agentTurnRunner.buildTurnMessages(request, observations)
	messages = append(messages, llm.Message{
		Role:    "system",
		Content: "The current run is near its limit. Do not call tools. If the goal is satisfied by successful observations, return final_reply with goalSatisfied=true and cite the completionEvidence. If the goal is not satisfied, return fail.",
	})
	structuredResponse, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_agent_turn_finalizer",
			Document:           finalizerActionSchema(),
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	var actionDocument turnActionDocument
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &actionDocument); errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	return actionDocument, nil
}

func completionRequirementsHaveEvidence(requirements []toolUseRequirement, observations []turnObservation) bool {
	if len(requirements) == 0 {
		return false
	}
	for _, requirement := range requirements {
		isSatisfied, _ := completionRequirementStatus(requirement, observations)
		if !isSatisfied {
			return false
		}
	}
	return true
}

func (agentTurnRunner *AgentTurnRunner) stopForLimit(taskRunID string, request AgentTurnRequest, reason string, observations []turnObservation, attachments []FileAttachment, usedIterationCount int, usedToolCallCount int) (AgentTurnResult, error) {
	body := map[string]any{
		"effortLevel":        agentTurnRunner.options.EffortLevel,
		"maxIterationCount":  agentTurnRunner.options.MaxIterationCount,
		"maxElapsedSecond":   agentTurnRunner.options.MaxElapsedSecond,
		"maxToolCallCount":   agentTurnRunner.options.MaxToolCallCount,
		"usedIterationCount": usedIterationCount,
		"usedToolCallCount":  usedToolCallCount,
		"limitStopReason":    reason,
		"attachmentCount":    len(attachments),
		"observationCount":   len(observations),
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.limit_stop", marshalEventBody(body))
	blockedTaskRun, _ := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusBlocked, reason)
	reply := agentTurnRunner.GenerateLimitReachedReply(request, reason, observations, attachments)
	blockedTaskRun.Result = reply
	return AgentTurnResult{TaskRun: blockedTaskRun, FinalReply: reply}, nil
}

func validateCompletionGate(requirements []toolUseRequirement, observations []turnObservation, actionDocument turnActionDocument) completionGateResult {
	if actionDocument.GoalSatisfied == nil || !*actionDocument.GoalSatisfied {
		return completionGateResult{Message: "final_reply requires goalSatisfied=true"}
	}
	if strings.TrimSpace(actionDocument.GoalStatus) != "" && strings.TrimSpace(actionDocument.GoalStatus) != "satisfied" {
		return completionGateResult{Message: "final_reply requires goalStatus=satisfied"}
	}
	attachments, errorValue := validateCompletionEvidence(requirements, observations, actionDocument.CompletionEvidence)
	if errorValue != nil {
		return completionGateResult{Message: errorValue.Error()}
	}
	return completionGateResult{IsSatisfied: true, Attachments: attachments}
}

func validateCompletionGateForRequest(request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, actionDocument turnActionDocument) completionGateResult {
	result := validateCompletionGate(requirements, observations, actionDocument)
	if !result.IsSatisfied {
		return result
	}
	result.ValidityState = buildAttachmentValidityState(request.WorkspaceRootPath, result.Attachments)
	if !result.ValidityState.Passed {
		result.IsSatisfied = false
		result.Message = validityFailureMessage(result.ValidityState)
		result.Attachments = nil
	}
	return result
}

func validateCompletionEvidence(requirements []toolUseRequirement, observations []turnObservation, references []completionEvidenceReference) ([]FileAttachment, error) {
	if len(requirements) == 0 {
		return collectReferencedAttachments(observations, references)
	}
	attachments := collectReferenceAttachments(observations, references)
	for _, requirement := range requirements {
		matchingReferences := completionReferencesForRequirement(requirement, observations, references)
		if len(matchingReferences) == 0 {
			return nil, errors.New("completionEvidence must cite successful observation for " + requirementLabel(requirement))
		}
		if !requirement.RequiresAttachment {
			continue
		}
		requirementAttachments := collectReferenceAttachments(observations, matchingReferences)
		if len(requirementAttachments) == 0 {
			return nil, errors.New("completionEvidence for " + requirementLabel(requirement) + " must include an attachment")
		}
		if missingSuffix := missingRequiredAttachmentSuffix(requirementAttachments, requirement.AttachmentSuffixes); missingSuffix != "" {
			return nil, errors.New("completionEvidence for " + requirementLabel(requirement) + " must include attachment suffix " + missingSuffix)
		}
	}
	return attachments, nil
}

func missingRequiredAttachmentSuffix(attachments []FileAttachment, suffixes []string) string {
	missingSuffixes := missingRequiredAttachmentSuffixes(attachments, suffixes)
	if len(missingSuffixes) == 0 {
		return ""
	}
	return missingSuffixes[0]
}

func missingRequiredAttachmentSuffixes(attachments []FileAttachment, suffixes []string) []string {
	missingSuffixes := []string{}
	for _, suffix := range suffixes {
		if !attachmentsContainSuffix(attachments, suffix) {
			missingSuffixes = append(missingSuffixes, suffix)
		}
	}
	return missingSuffixes
}

func attachmentsContainSuffix(attachments []FileAttachment, suffix string) bool {
	for _, attachment := range attachments {
		if attachmentMatchesSuffix(attachment, suffix) {
			return true
		}
	}
	return false
}

func attachmentMatchesSuffix(attachment FileAttachment, suffix string) bool {
	return strings.HasSuffix(attachment.Filename, suffix) || strings.HasSuffix(attachment.DevicePath, suffix)
}

func collectReferencedAttachments(observations []turnObservation, references []completionEvidenceReference) ([]FileAttachment, error) {
	attachments := []FileAttachment{}
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if !isFound {
			return nil, errors.New("completionEvidence references an unknown or failed observation")
		}
		attachments = appendUniqueAttachments(attachments, attachmentsForReference(observation, reference))
	}
	return attachments, nil
}

func completionReferencesForRequirement(requirement toolUseRequirement, observations []turnObservation, references []completionEvidenceReference) []completionEvidenceReference {
	matchingReferences := []completionEvidenceReference{}
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if !isFound {
			continue
		}
		if requirementMatchesObservation(requirement, observation) {
			matchingReferences = append(matchingReferences, reference)
		}
	}
	return matchingReferences
}

func matchingCompletionObservations(requirement toolUseRequirement, observations []turnObservation) []turnObservation {
	matchingObservations := []turnObservation{}
	for _, observation := range observations {
		if observation.IsError || !requirementMatchesObservation(requirement, observation) {
			continue
		}
		if requirement.RequiresAttachment && len(observation.Attachments) == 0 {
			continue
		}
		matchingObservations = append(matchingObservations, observation)
	}
	return matchingObservations
}

func requirementMatchesObservation(requirement toolUseRequirement, observation turnObservation) bool {
	toolName := strings.TrimSpace(observation.Tool)
	if strings.TrimSpace(requirement.ToolName) != "" {
		return toolName == strings.TrimSpace(requirement.ToolName)
	}
	if strings.TrimSpace(requirement.ToolPrefix) != "" {
		return strings.HasPrefix(toolName, strings.TrimSpace(requirement.ToolPrefix))
	}
	return false
}

func findSuccessfulObservation(observations []turnObservation, reference completionEvidenceReference) (turnObservation, bool) {
	for _, observation := range observations {
		if observation.IsError {
			continue
		}
		if strings.TrimSpace(observation.ObservationID) != strings.TrimSpace(reference.ObservationID) {
			continue
		}
		if strings.TrimSpace(observation.Tool) != strings.TrimSpace(reference.ToolName) {
			continue
		}
		return observation, true
	}
	return turnObservation{}, false
}

func collectReferenceAttachments(observations []turnObservation, references []completionEvidenceReference) []FileAttachment {
	attachments := []FileAttachment{}
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if !isFound {
			continue
		}
		attachments = appendUniqueAttachments(attachments, attachmentsForReference(observation, reference))
	}
	return attachments
}

func attachmentsForReference(observation turnObservation, reference completionEvidenceReference) []FileAttachment {
	if reference.AttachmentIndex == nil {
		return observation.Attachments
	}
	index := *reference.AttachmentIndex
	if index < 0 || index >= len(observation.Attachments) {
		return nil
	}
	return []FileAttachment{observation.Attachments[index]}
}

func appendUniqueAttachments(attachments []FileAttachment, candidates []FileAttachment) []FileAttachment {
	nextAttachments := append([]FileAttachment{}, attachments...)
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.DevicePath) == "" || hasAttachmentDevicePath(nextAttachments, candidate.DevicePath) {
			continue
		}
		nextAttachments = append(nextAttachments, candidate)
	}
	return nextAttachments
}

func requirementLabel(requirement toolUseRequirement) string {
	if strings.TrimSpace(requirement.ToolName) != "" {
		return strings.TrimSpace(requirement.ToolName)
	}
	return strings.TrimSpace(requirement.ToolPrefix)
}

func nextObservationID(index int) string {
	return fmt.Sprintf("obs-%03d", index)
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

func (agentTurnRunner *AgentTurnRunner) GenerateLimitReachedReply(request AgentTurnRequest, stopReason string, observations []turnObservation, attachments []FileAttachment) string {
	finalizationPrompt := buildLimitReachedPrompt(request, stopReason, observations, attachments)
	finalizationContext, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	reply, errorValue := agentTurnRunner.languageModel.GenerateResponse(finalizationContext, finalizationPrompt)
	reply = strings.TrimSpace(reply)
	if errorValue != nil || reply == "" || containsForbiddenLimitReplyFragment(reply) {
		return StaticLimitReachedReply
	}
	return reply
}

func buildLimitReachedPrompt(request AgentTurnRequest, stopReason string, observations []turnObservation, attachments []FileAttachment) string {
	sections := []string{
		"You are writing a short user-facing final reply after a Blueclaw run reached its scope limit.",
		"Do not mention internal runtime jargon, counters, percentages, elapsed time, or exact limits.",
		"Say what was completed, what remains, and the best partial answer available from completed work.",
		"Do not claim a tool result or attachment exists unless it appears below.",
		"Original user request:\n" + strings.TrimSpace(request.Prompt),
	}
	if contextDescription := buildVisibleContextDescription(request.VisibleContext); strings.TrimSpace(contextDescription) != "" {
		sections = append(sections, contextDescription)
	}
	if memoryDescription := buildMemoryContext(request.MemoryFacts); strings.TrimSpace(memoryDescription) != "" {
		sections = append(sections, "Relevant memory summaries:\n"+memoryDescription)
	}
	if observationSummary := buildLimitObservationSummary(observations); observationSummary != "" {
		sections = append(sections, "Completed observations:\n"+observationSummary)
	}
	if attachmentSummary := buildLimitAttachmentSummary(attachments); attachmentSummary != "" {
		sections = append(sections, "Available attachments:\n"+attachmentSummary)
	}
	if requirementSummary := buildLimitRequirementSummary(request, observations); requirementSummary != "" {
		sections = append(sections, "Remaining completion requirements:\n"+requirementSummary)
	}
	if reason := strings.TrimSpace(stopReason); reason != "" {
		sections = append(sections, "Internal stop reason for your planning only: "+reason)
	}
	return strings.Join(sections, "\n\n")
}

func buildLimitObservationSummary(observations []turnObservation) string {
	lines := []string{}
	for _, observation := range observations {
		if observation.IsError {
			continue
		}
		summary := strings.TrimSpace(observation.Summary)
		if summary == "" {
			summary = summarizeObservationContent(observation)
		}
		if summary == "" {
			continue
		}
		label := strings.TrimSpace(observation.Tool)
		if label == "" {
			label = strings.TrimSpace(observation.Action)
		}
		lines = append(lines, "- "+label+": "+summary)
		if len(lines) >= 8 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func buildLimitAttachmentSummary(attachments []FileAttachment) string {
	lines := []string{}
	for _, attachment := range attachments {
		filename := strings.TrimSpace(attachment.Filename)
		if filename == "" {
			continue
		}
		lines = append(lines, "- "+filename)
	}
	return strings.Join(lines, "\n")
}

func buildLimitRequirementSummary(request AgentTurnRequest, observations []turnObservation) string {
	requirements := deriveToolUseRequirements(request)
	lines := []string{}
	for _, requirement := range requirements {
		if matchingCompletionObservations(requirement, observations) != nil {
			continue
		}
		lines = append(lines, "- "+requirementLabel(requirement))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func containsForbiddenLimitReplyFragment(reply string) bool {
	normalizedReply := strings.ToLower(reply)
	for _, fragment := range []string{"budget", "예산", "%", "percent", "percentage", "iteration", "tool call", "tool-call", "counter", "minute", "minutes", "second", "seconds", "분 ", "초 "} {
		if strings.Contains(normalizedReply, fragment) {
			return true
		}
	}
	return false
}
