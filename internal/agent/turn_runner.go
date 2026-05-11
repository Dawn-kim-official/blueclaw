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

type TurnOptions struct {
	MaxIterationCount    int
	MaxToolCallCount     int
	MaxElapsedSecond     int
	RecoveryAttemptLimit int
	EffortLevel          EffortLevel
	ToolResultMaxBytes   int
	GenerationOptions    llm.GenerationOptions
}

const defaultRecoveryAttemptLimit = 3

type AgentTurnRunner struct {
	taskRunService      *task.TaskRunService
	taskStepService     *task.TaskStepService
	taskArtifactService *task.TaskArtifactService
	languageModel       llm.LanguageModelProvider
	options             TurnOptions
}

type AgentTurnRequest struct {
	RequesterPersonID          string
	RequesterEmail             string
	RequesterName              string
	RequesterPlatformUserID    string
	IsApprovalContinuation     bool
	Platform                   string
	RequesterCallingName       string
	RequesterHandle            string
	RequesterCircles           []string
	ProfileName                string
	ConversationID             string
	Prompt                     string
	VisibleContext             VisibleContext
	MemoryFacts                []memory.MemoryFact
	ToolSet                    *ToolSet
	WorkspaceRootPath          string
	ActivePaths                []string
	InstructionPrompt          string
	InstructionSources         []InstructionSource
	SkillDecisions             []SkillSelectionDecision
	SkillRetrievalMode         string
	SkillIndexStatus           string
	SkillCandidateCount        int
	RequiredEvidenceTools      []string
	RequiredAttachmentSuffixes []string
	QualityAcceptanceGuidance  []string
	TurnStartedAt              time.Time
}

type AgentTurnResult struct {
	TaskRun         task.TaskRun
	FinalReply      string
	Attachments     []FileAttachment
	RecoveryActions []RecoveryAction
	ToolNames       []string
}

type turnActionDocument struct {
	Action             string                        `json:"action"`
	FinalReply         string                        `json:"finalReply"`
	ToolName           string                        `json:"toolName"`
	ToolInput          json.RawMessage               `json:"toolInput"`
	Reason             string                        `json:"reason"`
	Reply              string                        `json:"reply"`
	GoalStatus         string                        `json:"goalStatus"`
	GoalSatisfied      *bool                         `json:"goalSatisfied"`
	CompletionEvidence []completionEvidenceReference `json:"completionEvidence"`
	QualityCriteria    []qualityCriterion            `json:"qualityCriteria"`
	QualityReview      []qualityReviewItem           `json:"qualityReview"`
	RemainingWork      string                        `json:"remainingWork"`
}

type turnObservation struct {
	ObservationID        string           `json:"observationID"`
	Action               string           `json:"action"`
	Tool                 string           `json:"tool,omitempty"`
	Content              string           `json:"content"`
	Summary              string           `json:"summary,omitempty"`
	IsError              bool             `json:"isError"`
	Message              string           `json:"message,omitempty"`
	ErrorCode            string           `json:"errorCode,omitempty"`
	FailureStage         string           `json:"failureStage,omitempty"`
	Retryable            bool             `json:"retryable,omitempty"`
	SafeRetry            bool             `json:"safeRetry,omitempty"`
	ToolInputKey         string           `json:"toolInputKey,omitempty"`
	RecoveryAttemptKey   string           `json:"recoveryAttemptKey,omitempty"`
	RecoveryAttemptSpent bool             `json:"recoveryAttemptSpent,omitempty"`
	Attachments          []FileAttachment `json:"attachments,omitempty"`
	RecoveryActions      []RecoveryAction `json:"recoveryActions,omitempty"`
}

type completionEvidenceReference struct {
	ObservationID   string `json:"observationID"`
	ToolName        string `json:"toolName"`
	AttachmentIndex *int   `json:"attachmentIndex,omitempty"`
}

type qualityCriterion struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type qualityReviewItem struct {
	ID       string                        `json:"id"`
	Passed   bool                          `json:"passed"`
	Evidence []completionEvidenceReference `json:"evidence"`
	Notes    string                        `json:"notes,omitempty"`
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
	if options.RecoveryAttemptLimit <= 0 {
		options.RecoveryAttemptLimit = defaultRecoveryAttemptLimit
	}
	return options
}

func (agentTurnRunner *AgentTurnRunner) RunTurn(ctx context.Context, request AgentTurnRequest) (AgentTurnResult, error) {
	if agentTurnRunner.languageModel == nil {
		return AgentTurnResult{}, errors.New("language model provider is not configured")
	}

	turnContext, cancel := context.WithTimeout(ctx, time.Duration(agentTurnRunner.options.MaxElapsedSecond)*time.Second)
	defer cancel()
	turnContext = llm.ContextWithRequestContext(turnContext, llm.RequestContext{
		RequesterPersonID:       request.RequesterPersonID,
		RequesterEmail:          request.RequesterEmail,
		RequesterName:           request.RequesterName,
		RequesterPlatformUserID: request.RequesterPlatformUserID,
		ConversationID:          request.ConversationID,
		Platform:                request.Platform,
	})
	if request.TurnStartedAt.IsZero() {
		request.TurnStartedAt = time.Now().Add(-2 * time.Second)
	}

	taskRun := agentTurnRunner.taskRunService.CreateTaskRun(request.RequesterPersonID, request.ConversationID, request.Prompt)
	runningTaskRun, errorValue := agentTurnRunner.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue == nil {
		taskRun = runningTaskRun
	}
	agentTurnRunner.appendInstructionEvent(taskRun.TaskRunID, request)

	state := buildInitialAgentTaskState(request, agentTurnRunner.options, taskRun.TaskRunID)
	state.Status = taskRun.Status
	toolUseRequirements := state.Requirements
	successfulToolCalls := map[string]turnObservation{}
	recoveryAttempts := map[string]int{}
	limitPressureWarnings := map[string]bool{}
	for iteration := 1; iteration <= agentTurnRunner.options.MaxIterationCount; iteration++ {
		state.IterationCount = iteration - 1
		if warning := agentTurnRunner.nextLimitPressureWarning(iteration-1, state.ToolCallCount, len(state.Observations)+1, limitPressureWarnings); warning != nil {
			state.Observations = append(state.Observations, warning.Observation)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.limit_pressure", marshalEventBody(warning.EventBody))
			limitPressureWarnings[warning.Level] = true
		}
		stepID := fmt.Sprintf("%s:turn-%03d", taskRun.TaskRunID, iteration)
		agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusRunning, "agent turn iteration", "")

		transition := agentTurnRunner.applyCompletionState(turnContext, taskRun.TaskRunID, stepID, request, toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria)
		state.Observations = transition.Observations
		state.Attachments = transition.Attachments
		if transition.IsCompleted {
			return transition.Result, nil
		}
		if transition.DidTransition {
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "completion_state "+string(transition.Action), "")
			continue
		}

		actionDocument, actionError := agentTurnRunner.nextAction(turnContext, request, toolUseRequirements, state.Observations, len(state.QualityCriteria) == 0)
		if actionError != nil {
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "agent turn iteration", actionError.Error())
			if errors.Is(actionError, context.DeadlineExceeded) {
				return agentTurnRunner.stopForLimit(taskRun.TaskRunID, request, "max_elapsed", state.Observations, state.Attachments, iteration-1, state.ToolCallCount)
			}
			return agentTurnRunner.failTurn(taskRun.TaskRunID, request, "llm action failed: "+actionError.Error(), state.Observations, state.Attachments)
		}

		agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.action", marshalEventBody(actionDocument))
		switch strings.TrimSpace(actionDocument.Action) {
		case "set_quality_criteria":
			state.QualityCriteria = normalizeQualityCriteria(actionDocument.QualityCriteria)
			observation := turnObservation{
				ObservationID: nextObservationID(len(state.Observations) + 1),
				Action:        "set_quality_criteria",
				Content:       marshalEventBody(map[string]any{"criteria": state.QualityCriteria}),
			}
			state.Observations = append(state.Observations, observation)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.quality_criteria", marshalEventBody(map[string]any{
				"criteria": state.QualityCriteria,
			}))
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "set_quality_criteria", marshalEventBody(map[string]any{"criteria": state.QualityCriteria}))
			continue
		case "final_reply":
			completionGateResult := validateCompletionGateForRequest(request, toolUseRequirements, state.Observations, state.QualityCriteria, actionDocument)
			agentTurnRunner.appendValidityReview(taskRun.TaskRunID, "final_reply", completionGateResult.ValidityState)
			if !completionGateResult.IsSatisfied {
				observation := turnObservation{
					ObservationID: nextObservationID(len(state.Observations) + 1),
					Action:        "policy",
					Content:       completionGateResult.Message,
					IsError:       true,
				}
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.completion_required", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "completion_required", observation.Content)
				continue
			}
			agentTurnRunner.appendQualityReview(taskRun.TaskRunID, state.QualityCriteria, actionDocument.QualityReview, state.Observations)
			reply := strings.TrimSpace(actionDocument.FinalReply)
			if reply == "" {
				reply = strings.TrimSpace(actionDocument.Reply)
			}
			if reply == "" {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "final_reply", "empty final reply")
				return agentTurnRunner.failTurn(taskRun.TaskRunID, request, "empty final reply", state.Observations, state.Attachments)
			}
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "final_reply", reply)
			completedTaskRun, _ := agentTurnRunner.taskRunService.CompleteTaskRun(taskRun.TaskRunID, reply)
			return AgentTurnResult{TaskRun: completedTaskRun, FinalReply: reply, Attachments: completionGateResult.Attachments, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, nil
		case "call_tool":
			if shouldRejectUnnecessarySiteApprovalRequest(request, actionDocument.ToolName, actionDocument.ToolInput) {
				observation := turnObservation{ObservationID: nextObservationID(len(state.Observations) + 1), Action: "policy", Tool: strings.TrimSpace(actionDocument.ToolName), Content: unnecessarySiteApprovalMessage(), IsError: true}
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.approval_request_rejected", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "approval_request_rejected", observation.Content)
				continue
			}
			if validationError := validateBrowserToolInput(actionDocument.ToolName, actionDocument.ToolInput); validationError != nil {
				observation := turnObservation{ObservationID: nextObservationID(len(state.Observations) + 1), Action: "call_tool", Tool: strings.TrimSpace(actionDocument.ToolName), Content: validationError.Error(), IsError: true}
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.tool_input_malformed", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "malformed_tool_input "+actionDocument.ToolName, observation.Content)
				continue
			}
			if validationError := validateTerminalToolInput(actionDocument.ToolName, actionDocument.ToolInput); validationError != nil {
				observation := turnObservation{ObservationID: nextObservationID(len(state.Observations) + 1), Action: "call_tool", Tool: strings.TrimSpace(actionDocument.ToolName), Content: validationError.Error(), IsError: true}
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.tool_input_malformed", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "malformed_tool_input "+actionDocument.ToolName, observation.Content)
				continue
			}
			if duplicateObservation, isDuplicate := successfulToolCalls[canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)]; isDuplicate && handlesDuplicateSuccessfulToolCall(actionDocument.ToolName) {
				observation := turnObservation{
					ObservationID: nextObservationID(len(state.Observations) + 1),
					Action:        "policy",
					Tool:          strings.TrimSpace(actionDocument.ToolName),
					Content:       "This exact tool call already succeeded as " + duplicateObservation.ObservationID + ". Use that observation for completionEvidence instead of running it again.",
					IsError:       true,
				}
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.duplicate_tool_call_rejected", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "duplicate_tool_call "+actionDocument.ToolName, observation.Content)
				continue
			}
			if exhaustedFailure, isExhausted := previousExhaustedSafeRetry(state.Observations, actionDocument.ToolName, actionDocument.ToolInput, recoveryAttempts); isExhausted {
				observation := recoveryGuidanceObservation(len(state.Observations)+1, exhaustedFailure)
				observation.Content = "This exact tool call already used its one safe retry. Try a different method or a read-only diagnostic before failing. " + observation.Content
				observation.Summary = observation.Content
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.safe_retry_exhausted", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "safe_retry_exhausted "+actionDocument.ToolName, observation.Content)
				continue
			}
			if duplicateFailure, isDuplicateFailure := previousUnsafeFailedToolCall(state.Observations, actionDocument.ToolName, actionDocument.ToolInput); isDuplicateFailure {
				observation := recoveryGuidanceObservation(len(state.Observations)+1, duplicateFailure)
				observation.Content = "This exact tool call already failed and is not safe to repeat. Try a different method or a read-only diagnostic before failing. " + observation.Content
				observation.Summary = observation.Content
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.unsafe_retry_rejected", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "unsafe_retry_rejected "+actionDocument.ToolName, observation.Content)
				continue
			}
			state.ToolCallCount++
			if state.ToolCallCount > agentTurnRunner.options.MaxToolCallCount {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusBlocked, "limit stop", "max_tool_calls")
				return agentTurnRunner.finalizeOrStopForLimit(turnContext, taskRun.TaskRunID, request, "max_tool_calls", toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria, iteration, agentTurnRunner.options.MaxToolCallCount)
			}
			observation := agentTurnRunner.invokeTool(turnContext, request.ToolSet, taskRun.TaskRunID, nextObservationID(len(state.Observations)+1), actionDocument.ToolName, actionDocument.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt)
			state.Observations = append(state.Observations, observation)
			state.Attachments = appendObservationAttachments(state.Attachments, observation)
			if retriedObservation, didRetry := agentTurnRunner.retryFailedToolObservation(turnContext, request, taskRun.TaskRunID, observation, actionDocument, recoveryAttempts, &state); didRetry {
				observation = retriedObservation
			}
			if observation.IsError && recoveryAttemptCount(state.Observations) < agentTurnRunner.options.RecoveryAttemptLimit {
				recoveryObservation := recoveryGuidanceObservation(len(state.Observations)+1, observation)
				state.Observations = append(state.Observations, recoveryObservation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.recovery_guidance", marshalEventBody(recoveryObservation))
			}
			if pausedResult, isPaused := agentTurnRunner.pausedTaskResult(taskRun.TaskRunID, observation, state.Attachments); isPaused {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, pausedResult.TaskRun.Status, "call_tool "+actionDocument.ToolName, observation.Content)
				return pausedResult, nil
			}
			if !observation.IsError {
				successfulToolCalls[canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)] = observation
			}
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "call_tool "+actionDocument.ToolName, observation.Content)
		case "fail":
			if failedObservation, canRecover := latestFailedToolObservation(state.Observations); canRecover && recoveryAttemptCount(state.Observations) < agentTurnRunner.options.RecoveryAttemptLimit {
				observation := recoveryGuidanceObservation(len(state.Observations)+1, failedObservation)
				observation.RecoveryAttemptSpent = true
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.recovery_blocked_fail", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "recovery_required", observation.Content)
				continue
			}
			reason := firstNonEmptyString(actionDocument.Reason, "agent reported failure")
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "fail", reason)
			return agentTurnRunner.failTurn(taskRun.TaskRunID, request, reason, state.Observations, state.Attachments)
		default:
			observation := turnObservation{ObservationID: nextObservationID(len(state.Observations) + 1), Action: "invalid_action", Content: "unknown action: " + actionDocument.Action, IsError: true}
			state.Observations = append(state.Observations, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "invalid_action", observation.Content)
		}
	}

	return agentTurnRunner.finalizeOrStopForLimit(turnContext, taskRun.TaskRunID, request, "max_iterations", toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria, agentTurnRunner.options.MaxIterationCount, state.ToolCallCount)
}

func (agentTurnRunner *AgentTurnRunner) pausedTaskResult(taskRunID string, observation turnObservation, attachments []FileAttachment) (AgentTurnResult, bool) {
	taskRun, isFound := agentTurnRunner.taskRunService.FindTaskRun(taskRunID)
	if !isFound || !isWaitingForUser(taskRun.Status) {
		return AgentTurnResult{}, false
	}
	reply := firstNonEmptyString(taskRun.FailureReason, toolObservationMessage(observation), observation.Content)
	return AgentTurnResult{TaskRun: taskRun, FinalReply: reply, Attachments: attachments, RecoveryActions: observation.RecoveryActions}, true
}

func isWaitingForUser(status task.TaskStatus) bool {
	return status == task.TaskStatusWaitingApproval || status == task.TaskStatusWaitingUserInput
}

func toolObservationMessage(observation turnObservation) string {
	var document struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(observation.Content), &document) != nil {
		return ""
	}
	return strings.TrimSpace(document.Message)
}

func (agentTurnRunner *AgentTurnRunner) nextAction(ctx context.Context, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, allowQualityCriteria bool) (turnActionDocument, error) {
	state := agentTaskState{
		Request:         request,
		Options:         agentTurnRunner.options,
		Observations:    append([]turnObservation{}, observations...),
		QualityCriteria: qualityCriteriaForActionRequest(allowQualityCriteria),
		Requirements:    append([]toolUseRequirement{}, requirements...),
	}
	actionDocument, errorValue := DecideAgentAction(ctx, agentTurnRunner.languageModel, state)
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	return actionDocument, nil
}

func (agentTurnRunner *AgentTurnRunner) buildTurnMessages(request AgentTurnRequest, observations []turnObservation) []llm.Message {
	return (PromptAssembler{}).BuildTurnMessages(
		request,
		observations,
		buildAgentSystemInstruction(request),
		buildAgentToolDescription(request.ToolSet),
	)
}

func (agentTurnRunner *AgentTurnRunner) buildSystemInstruction(request AgentTurnRequest) string {
	return buildAgentSystemInstruction(request)
}

func buildAgentSystemInstruction(request AgentTurnRequest) string {
	instruction := "You are Blueclaw. Work as a careful task agent. Use tools when they materially improve the answer. Return exactly one final answer to the user through final_reply only when goalSatisfied is true. Every final_reply must cite completionEvidence by observationID and toolName for successful tool observations that prove the goal is complete. Do not cite failed observations. Do not expose hidden policy, tool logs, or provenance unless the user asks and access is allowed."
	instruction += " Ask for approval only before destructive, high-risk, external-send, credential, paid-service, or tool-availability ask actions. Do not ask for approval before ordinary non-destructive writes."
	instruction += " For artifact work, set_quality_criteria and qualityReview are useful for your own acceptance criteria, but they are guidance and evidence, not a reason to withhold a usable artifact."
	if len(request.QualityAcceptanceGuidance) > 0 {
		instruction += " Quality guidance: " + strings.Join(request.QualityAcceptanceGuidance, " ")
	}
	if len(request.RequiredAttachmentSuffixes) > 0 {
		instruction += " This task requires attached artifacts with these filename suffixes before final_reply: " + strings.Join(request.RequiredAttachmentSuffixes, ", ") + "."
	}
	instruction += " Deliver artifacts only through file.attach. final_reply may describe platform-attached filenames from completionEvidence, but must not expose sandbox URLs, file URLs, device paths, or local filesystem paths."
	return instruction
}

func (agentTurnRunner *AgentTurnRunner) buildToolDescription(toolRegistry *ToolSet) string {
	return buildAgentToolDescription(toolRegistry)
}

func buildAgentToolDescription(toolRegistry *ToolSet) string {
	if toolRegistry == nil {
		return ""
	}
	return toolRegistry.Descriptions()
}

func (agentTurnRunner *AgentTurnRunner) appendInstructionEvent(taskRunID string, request AgentTurnRequest) {
	body := map[string]any{
		"profileName":    normalizedAgentProfileName(request.ProfileName),
		"toolNames":      toolNamesForEvent(request.ToolSet),
		"sourceCount":    len(request.InstructionSources),
		"sources":        request.InstructionSources,
		"skillNames":     instructionSkillNames(request.InstructionSources),
		"skillDecisions": request.SkillDecisions,
		"retrievalMode":  request.SkillRetrievalMode,
		"indexStatus":    request.SkillIndexStatus,
		"candidateCount": request.SkillCandidateCount,
	}
	if strings.TrimSpace(request.InstructionPrompt) == "" {
		body["status"] = "empty"
	} else {
		body["status"] = "loaded"
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.instructions_loaded", marshalEventBody(body))
}

func toolNamesForEvent(toolSet *ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	return toolSet.ListToolNames()
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

func validateTerminalToolInput(toolName string, toolInput json.RawMessage) error {
	if !isTerminalExecutionTool(toolName) {
		return nil
	}
	inputDocument, errorValue := parseToolInputDocument(toolName, toolInput)
	if errorValue != nil {
		return errorValue
	}
	command := strings.TrimSpace(stringValue(inputDocument["command"]))
	if command == "" {
		return nil
	}
	for _, toolAlias := range []string{"file.write", "file.attach", "set_quality_criteria", "final_reply"} {
		if strings.Contains(command, toolAlias) {
			return errors.New(strings.TrimSpace(toolName) + " command cannot call Blueclaw action " + toolAlias + "; call that action directly instead")
		}
	}
	return nil
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
	if strings.TrimSpace(toolName) == "terminal.run" {
		return true
	}
	return isOneShotCompletionEvidenceTool(toolName)
}

func previousUnsafeFailedToolCall(observations []turnObservation, toolName string, toolInput json.RawMessage) (turnObservation, bool) {
	if !isUnsafeRepeatSensitiveTool(toolName) {
		return turnObservation{}, false
	}
	expectedKey := canonicalToolCallKey(toolName, toolInput)
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Action != "call_tool" || !observation.IsError || observation.SafeRetry {
			continue
		}
		if strings.TrimSpace(observation.ToolInputKey) == expectedKey {
			return observation, true
		}
	}
	return turnObservation{}, false
}

func previousExhaustedSafeRetry(observations []turnObservation, toolName string, toolInput json.RawMessage, recoveryAttempts map[string]int) (turnObservation, bool) {
	expectedKey := canonicalToolCallKey(toolName, toolInput)
	if recoveryAttempts[expectedKey] == 0 {
		return turnObservation{}, false
	}
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Action != "call_tool" || strings.TrimSpace(observation.ToolInputKey) != expectedKey {
			continue
		}
		if observation.IsError && observation.SafeRetry {
			return observation, true
		}
		return turnObservation{}, false
	}
	return turnObservation{}, false
}

func isUnsafeRepeatSensitiveTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "platform.dm.send", "mail.message.send", "google.gmail.send", "slack.message.send":
		return true
	default:
		return false
	}
}

func shouldRejectUnnecessarySiteApprovalRequest(request AgentTurnRequest, toolName string, toolInput json.RawMessage) bool {
	if strings.TrimSpace(toolName) != "approval.request" {
		return false
	}
	if !sitePublishTaskToolsAreAvailable(request.ToolSet) {
		return false
	}
	approvalText := strings.ToLower(strings.TrimSpace(string(toolInput)))
	if containsAny(approvalText, []string{"rollback", "roll back", "unpublish", "delete", "remove", "take down", "삭제", "되돌", "내려", "중단"}) {
		return false
	}
	if requiredEvidenceContains(request.RequiredEvidenceTools, "site.app.publish") {
		return true
	}
	return containsAny(approvalText, []string{"deploy", "publish", "external", "website", "site", "배포", "웹사이트", "외부"})
}

func sitePublishTaskToolsAreAvailable(toolSet *ToolSet) bool {
	if toolSet == nil {
		return false
	}
	toolNames := map[string]bool{}
	for _, toolName := range toolSet.ListToolNames() {
		toolNames[strings.TrimSpace(toolName)] = true
	}
	return toolNames["site.app.create"] && toolNames["site.app.publish"] && toolNames["terminal.run"]
}

func requiredEvidenceContains(requiredEvidenceTools []string, expectedToolName string) bool {
	for _, toolName := range requiredEvidenceTools {
		if strings.TrimSpace(toolName) == expectedToolName {
			return true
		}
	}
	return false
}

func unnecessarySiteApprovalMessage() string {
	return "Approval is not required for site.app.create, terminal.run builds, or site.app.publish. Continue with the site tools directly. Ask approval only for site.app.rollback, site.app.unpublish, or site.app.delete."
}

func isTerminalExecutionTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "terminal.run", "terminal.session":
		return true
	default:
		return false
	}
}

func blockedToolNamesForPreconditions(toolRegistry *ToolSet, requirements []toolUseRequirement, observations []turnObservation) map[string]bool {
	return map[string]bool{}
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

func (agentTurnRunner *AgentTurnRunner) invokeTool(ctx context.Context, toolRegistry *ToolSet, taskRunID string, observationID string, toolName string, toolInput json.RawMessage, workspaceRootPath string, minimumModifiedAt time.Time) turnObservation {
	trimmedToolName := strings.TrimSpace(toolName)
	toolInputKey := canonicalToolCallKey(trimmedToolName, toolInput)
	if toolRegistry == nil {
		observation := toolFailureObservation(observationID, trimmedToolName, "tool registry was not configured")
		observation.ToolInputKey = toolInputKey
		return observation
	}
	agentTurnRunner.appendEvent(taskRunID, "tool."+trimmedToolName+".requested", marshalEventBody(map[string]any{
		"observationID": observationID,
		"toolName":      trimmedToolName,
		"input":         json.RawMessage(toolInput),
	}))
	toolResult, errorValue := toolRegistry.Invoke(WithTaskRunID(ctx, taskRunID), ToolInvocation{ToolName: trimmedToolName, Input: toolInput})
	if errorValue != nil {
		toolResult = ToolResult{Content: errorValue.Error(), Message: errorValue.Error(), IsError: true, ErrorCode: "tool_failed", FailureStage: trimmedToolName}
	}
	observation := agentTurnRunner.saveToolObservation(taskRunID, observationID, trimmedToolName, toolResult, workspaceRootPath, minimumModifiedAt)
	observation.ToolInputKey = toolInputKey
	return observation
}

func toolFailureObservation(observationID string, toolName string, message string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        "call_tool",
		Tool:          toolName,
		Content:       message,
		Message:       message,
		IsError:       true,
		ErrorCode:     "tool_failed",
		FailureStage:  firstNonEmptyString(toolName, "tool"),
	}
}

func (agentTurnRunner *AgentTurnRunner) retryFailedToolObservation(ctx context.Context, request AgentTurnRequest, taskRunID string, observation turnObservation, actionDocument turnActionDocument, recoveryAttempts map[string]int, state *agentTaskState) (turnObservation, bool) {
	if !shouldRetryFailedToolObservation(observation) {
		return observation, false
	}
	if recoveryAttemptCount(state.Observations) >= agentTurnRunner.options.RecoveryAttemptLimit {
		return observation, false
	}
	recoveryKey := canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)
	if recoveryAttempts[recoveryKey] > 0 {
		return observation, false
	}
	if state.ToolCallCount >= agentTurnRunner.options.MaxToolCallCount {
		agentTurnRunner.appendEvent(taskRunID, "agent.recovery_attempt", marshalEventBody(map[string]any{
			"status":        "skipped",
			"reason":        "max_tool_calls",
			"observationID": observation.ObservationID,
			"toolName":      observation.Tool,
			"errorCode":     observation.ErrorCode,
			"failureStage":  observation.FailureStage,
		}))
		return observation, false
	}
	recoveryAttempts[recoveryKey]++
	state.ToolCallCount++
	agentTurnRunner.appendEvent(taskRunID, "agent.recovery_attempt", marshalEventBody(map[string]any{
		"status":        "retrying",
		"attempt":       recoveryAttempts[recoveryKey],
		"observationID": observation.ObservationID,
		"toolName":      observation.Tool,
		"errorCode":     observation.ErrorCode,
		"failureStage":  observation.FailureStage,
	}))
	retryObservation := agentTurnRunner.invokeTool(ctx, request.ToolSet, taskRunID, nextObservationID(len(state.Observations)+1), actionDocument.ToolName, actionDocument.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt)
	retryObservation.RecoveryAttemptKey = recoveryKey
	retryObservation.RecoveryAttemptSpent = true
	state.Observations = append(state.Observations, retryObservation)
	state.Attachments = appendObservationAttachments(state.Attachments, retryObservation)
	return retryObservation, true
}

func shouldRetryFailedToolObservation(observation turnObservation) bool {
	return observation.IsError && observation.Retryable && observation.SafeRetry
}

func recoveryGuidanceObservation(index int, observation turnObservation) turnObservation {
	content := recoveryGuidanceContent(observation)
	return turnObservation{
		ObservationID:        nextObservationID(index),
		Action:               "recovery_guidance",
		Tool:                 observation.Tool,
		Content:              content,
		Summary:              content,
		IsError:              true,
		Message:              observation.Message,
		ErrorCode:            observation.ErrorCode,
		FailureStage:         observation.FailureStage,
		ToolInputKey:         observation.ToolInputKey,
		RecoveryAttemptKey:   observation.RecoveryAttemptKey,
		RecoveryAttemptSpent: observation.RecoveryAttemptSpent,
	}
}

func recoveryGuidanceContent(observation turnObservation) string {
	parts := []string{"Analyze the latest failed tool result before responding."}
	if observation.ErrorCode != "" {
		parts = append(parts, "errorCode="+observation.ErrorCode)
	}
	if observation.FailureStage != "" {
		parts = append(parts, "failureStage="+observation.FailureStage)
	}
	if observation.Message != "" {
		parts = append(parts, "message="+observation.Message)
	}
	if observation.RecoveryAttemptKey != "" {
		parts = append(parts, "A safe automatic retry has already been attempted for this tool input.")
	}
	return strings.Join(parts, " ")
}

func recoveryAttemptCount(observations []turnObservation) int {
	count := 0
	for _, observation := range observations {
		if observation.RecoveryAttemptSpent {
			count++
		}
	}
	return count
}

func latestFailedToolObservation(observations []turnObservation) (turnObservation, bool) {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if !observation.IsError || observation.Action != "call_tool" {
			continue
		}
		return observation, true
	}
	return turnObservation{}, false
}

func (agentTurnRunner *AgentTurnRunner) saveToolObservation(taskRunID string, observationID string, toolName string, toolResult ToolResult, workspaceRootPath string, minimumModifiedAt time.Time) turnObservation {
	toolResult = normalizeToolFailureResult(toolName, toolResult)
	content := toolResult.Content
	if strings.TrimSpace(content) == "" {
		content = toolResult.Message
	}
	originalContent := content
	isError := toolResult.IsError
	artifactID := ""
	if len(content) > agentTurnRunner.options.ToolResultMaxBytes {
		taskArtifact := agentTurnRunner.taskArtifactService.AddTaskArtifactBody(taskRunID, "tool."+toolName+".result", content)
		artifactID = taskArtifact.TaskArtifactID
		content = content[:agentTurnRunner.options.ToolResultMaxBytes] + "\n[truncated; full result saved as artifact " + taskArtifact.TaskArtifactID + "]"
	}
	attachments := []FileAttachment{}
	if !isError {
		attachments = append(attachments, toolResult.Attachments...)
	}
	if !isError && toolName == "file.attach" && len(attachments) > 0 {
		validityState := buildAttachmentValidityState(workspaceRootPath, attachments)
		if !validityState.Passed {
			content = validityFailureMessage(validityState)
			originalContent = content
			isError = true
			attachments = nil
			agentTurnRunner.appendEvent(taskRunID, "agent.artifact_attach_rejected", marshalEventBody(validityState))
		}
	}
	observation := turnObservation{
		ObservationID:   observationID,
		Action:          "call_tool",
		Tool:            toolName,
		Content:         content,
		Summary:         buildToolResultSummary(toolName, originalContent, isError, attachments, artifactID, toolResult),
		IsError:         isError,
		Message:         toolResult.Message,
		ErrorCode:       toolResult.ErrorCode,
		FailureStage:    toolResult.FailureStage,
		Retryable:       toolResult.Retryable,
		SafeRetry:       toolResult.SafeRetry,
		Attachments:     attachments,
		RecoveryActions: append([]RecoveryAction{}, toolResult.RecoveryActions...),
	}
	agentTurnRunner.appendEvent(taskRunID, "tool."+toolName+".result", marshalEventBody(observation))
	return observation
}

func normalizeToolFailureResult(toolName string, toolResult ToolResult) ToolResult {
	if !toolResult.IsError {
		return toolResult
	}
	if strings.TrimSpace(toolResult.Message) == "" {
		toolResult.Message = strings.TrimSpace(toolResult.Content)
	}
	if strings.TrimSpace(toolResult.Content) == "" {
		toolResult.Content = strings.TrimSpace(toolResult.Message)
	}
	if strings.TrimSpace(toolResult.ErrorCode) == "" {
		toolResult.ErrorCode = "tool_failed"
	}
	if strings.TrimSpace(toolResult.FailureStage) == "" {
		toolResult.FailureStage = firstNonEmptyString(toolName, "tool")
	}
	return toolResult
}

func recoveryActionsFromObservations(observations []turnObservation) []RecoveryAction {
	recoveryActions := []RecoveryAction{}
	seen := map[string]bool{}
	for _, observation := range observations {
		for _, recoveryAction := range observation.RecoveryActions {
			key := recoveryAction.Kind + "\x00" + recoveryAction.Delivery + "\x00" + recoveryAction.DownloadURL + "\x00" + recoveryAction.ConnectCommand
			if strings.TrimSpace(recoveryAction.Kind) == "" || seen[key] {
				continue
			}
			seen[key] = true
			recoveryActions = append(recoveryActions, recoveryAction)
		}
	}
	return recoveryActions
}

func buildToolResultSummary(toolName string, content string, isError bool, attachments []FileAttachment, artifactID string, toolResult ToolResult) string {
	observation := turnObservation{
		Tool:         toolName,
		Content:      content,
		IsError:      isError,
		Message:      toolResult.Message,
		ErrorCode:    toolResult.ErrorCode,
		FailureStage: toolResult.FailureStage,
		Attachments:  attachments,
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

func (agentTurnRunner *AgentTurnRunner) appendQualityReview(taskRunID string, criteria []qualityCriterion, review []qualityReviewItem, observations []turnObservation) {
	if len(criteria) == 0 {
		return
	}
	qualityState := buildQualityState(criteria, review, observations)
	agentTurnRunner.appendEvent(taskRunID, "agent.quality_review", marshalEventBody(qualityState))
}

func (agentTurnRunner *AgentTurnRunner) failTurn(taskRunID string, request AgentTurnRequest, reason string, observations []turnObservation, attachments []FileAttachment) (AgentTurnResult, error) {
	failedTaskRun, _ := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusFailed, reason)
	reply, replyStatus := agentTurnRunner.generateFailureReply(request, reason, observations, attachments)
	agentTurnRunner.appendEvent(taskRunID, "agent.failure_reply", marshalEventBody(replyStatus))
	failedTaskRun.Result = reply
	return AgentTurnResult{TaskRun: failedTaskRun, FinalReply: reply, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
}

func (agentTurnRunner *AgentTurnRunner) applyCompletionState(ctx context.Context, taskRunID string, taskStepID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion) completionTransition {
	state := buildCompletionState(request, requirements, observations)
	agentState := agentTaskState{
		TaskRunID:       taskRunID,
		Request:         request,
		Observations:    append([]turnObservation{}, observations...),
		Attachments:     append([]FileAttachment{}, attachments...),
		QualityCriteria: append([]qualityCriterion{}, criteria...),
		Requirements:    append([]toolUseRequirement{}, requirements...),
		TurnStartedAt:   request.TurnStartedAt,
		ToolCallCount:   len(observations),
		IterationCount:  len(observations),
	}
	transition := advanceAgentTask(agentState)
	switch transition.Effect.Kind {
	case agentEffectCallTool:
		if transition.Effect.ToolCall != nil && transition.Effect.ToolCall.ToolName == "file.attach" {
			return agentTurnRunner.attachCompletionArtifactsFromEffect(ctx, taskRunID, request, observations, attachments, state, *transition.Effect.ToolCall)
		}
	case agentEffectFinalReply:
		return agentTurnRunner.finalizeCompletionState(taskRunID, taskStepID, request, requirements, observations, attachments, criteria, state)
	case agentEffectNone:
		if len(transition.State.Observations) > len(observations) {
			return agentTurnRunner.blockInvalidCompletionArtifactsFromTransition(taskRunID, observations, attachments, state, transition)
		}
	default:
		return completionTransition{Observations: observations, Attachments: attachments}
	}
	return completionTransition{Observations: observations, Attachments: attachments}
}

func (agentTurnRunner *AgentTurnRunner) attachCompletionArtifacts(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation, attachments []FileAttachment, state CompletionState) completionTransition {
	return agentTurnRunner.attachCompletionArtifactsFromEffect(ctx, taskRunID, request, observations, attachments, state, ToolInvocation{
		ToolName: "file.attach",
		Input:    MarshalToolInput(map[string]any{"paths": state.AttachmentPaths}),
	})
}

func (agentTurnRunner *AgentTurnRunner) attachCompletionArtifactsFromEffect(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation, attachments []FileAttachment, state CompletionState, invocation ToolInvocation) completionTransition {
	agentTurnRunner.appendValidityReview(taskRunID, "pre_attach", state.ValidityState)
	observation := agentTurnRunner.invokeTool(ctx, request.ToolSet, taskRunID, nextObservationID(len(observations)+1), invocation.ToolName, invocation.Input, request.WorkspaceRootPath, request.TurnStartedAt)
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

func (agentTurnRunner *AgentTurnRunner) blockInvalidCompletionArtifactsFromTransition(taskRunID string, observations []turnObservation, attachments []FileAttachment, state CompletionState, transition agentTransition) completionTransition {
	nextObservations := transition.State.Observations
	observation := nextObservations[len(nextObservations)-1]
	agentTurnRunner.appendValidityReview(taskRunID, "completion_state", state.ValidityState)
	agentTurnRunner.appendEvent(taskRunID, "agent.completion_required", marshalEventBody(observation))
	return completionTransition{
		Observations:  nextObservations,
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

func (agentTurnRunner *AgentTurnRunner) finalizeCompletionState(taskRunID string, taskStepID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion, state CompletionState) completionTransition {
	actionDocument := completionStateFinalReplyDocument(state)
	completionGateResult := validateCompletionGateForRequest(request, requirements, observations, criteria, actionDocument)
	agentTurnRunner.appendValidityReview(taskRunID, "completion_state", completionGateResult.ValidityState)
	if !completionGateResult.IsSatisfied {
		agentTurnRunner.appendEvent(taskRunID, "agent.completion_state_rejected", marshalEventBody(map[string]string{"reason": completionGateResult.Message}))
		observation := turnObservation{
			ObservationID: nextObservationID(len(observations) + 1),
			Action:        "policy",
			Content:       completionGateResult.Message,
			IsError:       true,
		}
		observations = append(observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.completion_required", marshalEventBody(observation))
		return completionTransition{Observations: observations, Attachments: attachments}
	}
	agentTurnRunner.appendQualityReview(taskRunID, criteria, actionDocument.QualityReview, observations)
	agentTurnRunner.appendEvent(taskRunID, "agent.completion_state_finalized", marshalEventBody(map[string]any{
		"attachmentCount": len(completionGateResult.Attachments),
		"evidenceCount":   len(state.EvidenceReferences),
		"evidence":        state.EvidenceReferences,
	}))
	reply := strings.TrimSpace(actionDocument.FinalReply)
	agentTurnRunner.saveStep(taskRunID, taskStepID, task.TaskStatusCompleted, "completion_state "+string(completionActionFinalizeWithEvidence), reply)
	completedTaskRun, _ := agentTurnRunner.taskRunService.CompleteTaskRun(taskRunID, reply)
	return completionTransition{
		Observations:  observations,
		Attachments:   appendUniqueAttachments(attachments, completionGateResult.Attachments),
		Result:        AgentTurnResult{TaskRun: completedTaskRun, FinalReply: reply, Attachments: completionGateResult.Attachments, RecoveryActions: recoveryActionsFromObservations(observations)},
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
		return completionStateToolReply(state)
	}
	return "요청하신 파일을 생성해 첨부했습니다: " + strings.Join(filenames, ", ")
}

func completionStateToolReply(state CompletionState) string {
	for _, reference := range state.EvidenceReferences {
		switch strings.TrimSpace(reference.ToolName) {
		case "schedule.create":
			return "예약을 만들었습니다."
		case "schedule.cancel":
			return "예약을 취소했습니다."
		case "calendar.event.add":
			return "일정을 등록했습니다."
		case "calendar.event.update":
			return "일정을 수정했습니다."
		case "calendar.event.delete":
			return "일정을 삭제했습니다."
		case "flow.task.add":
			return "업무를 등록했습니다."
		case "google.gmail.send":
			return "메일을 보냈습니다."
		}
	}
	return "요청하신 작업을 완료했습니다."
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

func (agentTurnRunner *AgentTurnRunner) finalizeOrStopForLimit(ctx context.Context, taskRunID string, request AgentTurnRequest, reason string, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion, usedIterationCount int, usedToolCallCount int) (AgentTurnResult, error) {
	if ctx.Err() == nil {
		transition := agentTurnRunner.applyCompletionState(ctx, taskRunID, taskRunID+":completion", request, requirements, observations, attachments, criteria)
		if transition.IsCompleted {
			return transition.Result, nil
		}
		if transition.DidTransition {
			transition = agentTurnRunner.applyCompletionState(ctx, taskRunID, taskRunID+":completion", request, requirements, transition.Observations, transition.Attachments, criteria)
			if transition.IsCompleted {
				return transition.Result, nil
			}
			observations = transition.Observations
			attachments = transition.Attachments
		}
		if completionRequirementsHaveEvidence(requirements, observations) {
			if result, isFinalized := agentTurnRunner.finalizeSatisfiedTurn(ctx, taskRunID, request, requirements, observations, criteria); isFinalized {
				return result, nil
			}
		}
	}
	return agentTurnRunner.stopForLimit(taskRunID, request, reason, observations, attachments, usedIterationCount, usedToolCallCount)
}

func (agentTurnRunner *AgentTurnRunner) finalizeSatisfiedTurn(ctx context.Context, taskRunID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, criteria []qualityCriterion) (AgentTurnResult, bool) {
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
	completionGateResult := validateCompletionGateForRequest(request, requirements, observations, criteria, actionDocument)
	if !completionGateResult.IsSatisfied {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_rejected", marshalEventBody(map[string]string{"reason": completionGateResult.Message}))
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendValidityReview(taskRunID, "limit_finalizer", completionGateResult.ValidityState)
	agentTurnRunner.appendQualityReview(taskRunID, criteria, actionDocument.QualityReview, observations)
	reply := strings.TrimSpace(actionDocument.FinalReply)
	if reply == "" {
		reply = strings.TrimSpace(actionDocument.Reply)
	}
	if reply == "" {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_rejected", marshalEventBody(map[string]string{"reason": "empty final reply"}))
		return AgentTurnResult{}, false
	}
	completedTaskRun, _ := agentTurnRunner.taskRunService.CompleteTaskRun(taskRunID, reply)
	return AgentTurnResult{TaskRun: completedTaskRun, FinalReply: reply, Attachments: completionGateResult.Attachments, RecoveryActions: recoveryActionsFromObservations(observations)}, true
}

func (agentTurnRunner *AgentTurnRunner) finalizerAction(ctx context.Context, request AgentTurnRequest, observations []turnObservation) (turnActionDocument, error) {
	messages := agentTurnRunner.buildTurnMessages(request, observations)
	messages = append(messages, llm.Message{
		Role:    "system",
		Content: "The current run is near its limit. Do not call tools. If a useful result or attachment already exists, return final_reply with goalSatisfied=true and cite successful completionEvidence. If the goal is not satisfied, return a concise fail reply that accurately says what stopped and what evidence exists.",
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
		"actionCounts":       observationActionCounts(observations),
		"toolCounts":         observationToolCounts(observations),
		"recentObservations": recentProgressObservations(observations),
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.limit_stop", marshalEventBody(body))
	blockedTaskRun, _ := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusBlocked, reason)
	reply, replyStatus := agentTurnRunner.generateLimitReachedReply(request, reason, observations, nil)
	agentTurnRunner.appendEvent(taskRunID, "agent.limit_reply", marshalEventBody(replyStatus))
	blockedTaskRun.Result = reply
	return AgentTurnResult{TaskRun: blockedTaskRun, FinalReply: reply, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
}

func validateCompletionGate(requirements []toolUseRequirement, observations []turnObservation, criteria []qualityCriterion, actionDocument turnActionDocument) completionGateResult {
	if actionDocument.GoalSatisfied == nil || !*actionDocument.GoalSatisfied {
		return completionGateResult{Message: "final_reply requires goalSatisfied=true"}
	}
	if strings.TrimSpace(actionDocument.GoalStatus) != "" && strings.TrimSpace(actionDocument.GoalStatus) != "satisfied" {
		return completionGateResult{Message: "final_reply requires goalStatus=satisfied"}
	}
	if errorValue := validateObservedToolRequirements(requirements, observations); errorValue != nil {
		return completionGateResult{Message: errorValue.Error()}
	}
	attachments, errorValue := validateCompletionEvidence(requirements, observations, actionDocument.CompletionEvidence)
	if errorValue != nil {
		return completionGateResult{Message: errorValue.Error()}
	}
	if FinalReplyClaimsAttachmentDelivery(actionDocument.FinalReply) && len(attachments) == 0 {
		return completionGateResult{Message: "final_reply claims attached files but completionEvidence does not cite an attachment"}
	}
	requiresAttachmentEvidence := FinalReplyClaimsAttachmentDelivery(actionDocument.FinalReply) || len(attachments) > 0
	if errorValue := ValidateFinalReplyDelivery(actionDocument.FinalReply, attachments, requiresAttachmentEvidence); errorValue != nil {
		return completionGateResult{Message: errorValue.Error()}
	}
	return completionGateResult{IsSatisfied: true, Attachments: attachments}
}

func validateCompletionGateForRequest(request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, criteria []qualityCriterion, actionDocument turnActionDocument) completionGateResult {
	result := validateCompletionGate(requirements, observations, criteria, actionDocument)
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
		if !requirement.RequiresAttachment {
			continue
		}
		matchingReferences := completionReferencesForRequirement(requirement, observations, references)
		if len(matchingReferences) == 0 {
			return nil, errors.New("completionEvidence must cite successful observation for " + requirementLabel(requirement))
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

func validateObservedToolRequirements(requirements []toolUseRequirement, observations []turnObservation) error {
	for _, requirement := range requirements {
		if requirement.RequiresAttachment {
			continue
		}
		isSatisfied, _ := completionRequirementStatus(requirement, observations)
		if !isSatisfied {
			return errors.New("final_reply requires successful observation for " + requirementLabel(requirement))
		}
	}
	return nil
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

func observationActionCounts(observations []turnObservation) map[string]int {
	counts := map[string]int{}
	for _, observation := range observations {
		action := strings.TrimSpace(observation.Action)
		if action == "" {
			action = "unknown"
		}
		counts[action]++
	}
	return counts
}

func observationToolCounts(observations []turnObservation) map[string]int {
	counts := map[string]int{}
	for _, observation := range observations {
		toolName := strings.TrimSpace(observation.Tool)
		if toolName == "" {
			continue
		}
		counts[toolName]++
	}
	return counts
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

type limitReplyStatus struct {
	Source       string `json:"source"`
	FirstInvalid bool   `json:"firstInvalid"`
	RepairCount  int    `json:"repairCount"`
	Fallback     bool   `json:"fallback"`
	Reason       string `json:"reason,omitempty"`
}

type failureReplyStatus struct {
	Source string `json:"source"`
	Reason string `json:"reason,omitempty"`
}

type recoveryLanguageModelProvider interface {
	GenerateRecoveryResponse(context.Context, string) (string, error)
}

func (agentTurnRunner *AgentTurnRunner) generateFailureReply(request AgentTurnRequest, failureReason string, observations []turnObservation, attachments []FileAttachment) (string, failureReplyStatus) {
	prompt := buildFailureReplyPrompt(request, failureReason, observations, attachments)
	reply, errorValue := agentTurnRunner.generateRecoveryText(prompt)
	if errorValue == nil && reply != "" && !failureReplyIsInvalidForRequest(reply, request, failureReason, observations, attachments) {
		return reply, failureReplyStatus{Source: "generated"}
	}
	fallbackReply := buildDynamicRecoveryReply(request, failureReason, observations, attachments, "failure")
	if !failureReplyIsInvalidForRequest(fallbackReply, request, failureReason, observations, attachments) {
		return fallbackReply, failureReplyStatus{Source: "dynamic", Reason: firstNonEmptyString(errorString(errorValue), "invalid_generated_reply")}
	}
	return buildLastResortRecoveryReply(request, "failure"), failureReplyStatus{Source: "dynamic", Reason: "last_resort"}
}

func (agentTurnRunner *AgentTurnRunner) generateLimitReachedReply(request AgentTurnRequest, stopReason string, observations []turnObservation, attachments []FileAttachment) (string, limitReplyStatus) {
	finalizationPrompt := buildLimitReachedPrompt(request, stopReason, observations, attachments)
	reply, errorValue := agentTurnRunner.generateRecoveryText(finalizationPrompt)
	if errorValue != nil || reply == "" {
		return buildDynamicRecoveryReply(request, stopReason, observations, attachments, "limit"), limitReplyStatus{Source: "dynamic", Reason: firstNonEmptyString(errorString(errorValue), "empty_reply")}
	}
	if limitReachedReplyIsInvalid(reply, attachments) {
		for repairCount := 1; repairCount <= 2; repairCount++ {
			repairedReply, repairError := agentTurnRunner.generateRecoveryText(buildLimitReachedRepairPrompt(finalizationPrompt, reply, attachments, repairCount))
			if repairError != nil || repairedReply == "" {
				return buildDynamicRecoveryReply(request, stopReason, observations, attachments, "limit"), limitReplyStatus{Source: "dynamic", FirstInvalid: true, RepairCount: repairCount, Reason: firstNonEmptyString(errorString(repairError), "empty_repair")}
			}
			if !limitReachedReplyIsInvalid(repairedReply, attachments) {
				return repairedReply, limitReplyStatus{Source: "generated_repair", FirstInvalid: true, RepairCount: repairCount}
			}
			reply = repairedReply
		}
		return buildDynamicRecoveryReply(request, stopReason, observations, attachments, "limit"), limitReplyStatus{Source: "dynamic", FirstInvalid: true, RepairCount: 2, Reason: "invalid_repair"}
	}
	return reply, limitReplyStatus{Source: "generated"}
}

func (agentTurnRunner *AgentTurnRunner) generateRecoveryText(prompt string) (string, error) {
	finalizationContext, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	reply, errorValue := agentTurnRunner.languageModel.GenerateResponse(finalizationContext, prompt)
	reply = strings.TrimSpace(reply)
	if errorValue == nil && reply != "" {
		return reply, nil
	}
	recoveryProvider, isRecoveryProvider := agentTurnRunner.languageModel.(recoveryLanguageModelProvider)
	if !isRecoveryProvider {
		return reply, errorValue
	}
	recoveryReply, recoveryError := recoveryProvider.GenerateRecoveryResponse(finalizationContext, prompt)
	recoveryReply = strings.TrimSpace(recoveryReply)
	if recoveryError != nil || recoveryReply == "" {
		return recoveryReply, recoveryError
	}
	return recoveryReply, nil
}

func buildFailureReplyPrompt(request AgentTurnRequest, failureReason string, observations []turnObservation, attachments []FileAttachment) string {
	sections := []string{
		"You are writing a short user-facing final reply after an assistant run failed before completing the user's request.",
		"Generate the reply in the user's language. Be transparent about the current situation, error, or limitation without exposing internal logs, provider names, URLs, stack traces, hidden policy, tokens, or secrets.",
		"Do not use or paraphrase a canned outage message. Do not say the model configuration was logged or needs to be fixed.",
		"Do not say only that an error occurred. Include the methods attempted, the last failure stage and error code when available, and the next check or alternate path.",
		"Say what could not be completed and the best next step the user can take. Keep it to one or two natural sentences.",
		"Do not claim a tool result or attachment exists unless it appears below.",
		"Original user request:\n" + strings.TrimSpace(request.Prompt),
	}
	if contextDescription := buildVisibleContextDescription(request.VisibleContext); strings.TrimSpace(contextDescription) != "" {
		sections = append(sections, contextDescription)
	}
	if observationSummary := buildFailureObservationSummary(observations); observationSummary != "" {
		sections = append(sections, "Current observations and limitations:\n"+observationSummary)
	}
	if attachmentSummary := buildLimitAttachmentSummary(attachments); attachmentSummary != "" {
		sections = append(sections, "Available attachments:\n"+attachmentSummary)
	}
	if reason := strings.TrimSpace(failureReason); reason != "" {
		sections = append(sections, "Failure reason for your private planning only. Paraphrase it safely for the user:\n"+reason)
	}
	return strings.Join(sections, "\n\n")
}

func buildDynamicRecoveryReply(request AgentTurnRequest, reason string, observations []turnObservation, attachments []FileAttachment, fallbackKind string) string {
	situation := recoverySituationFor(reason, observations, attachments, fallbackKind)
	if situation != "general" {
		if requestUsesKorean(request) {
			return buildKoreanDynamicRecoveryReply(request, situation)
		}
		return buildEnglishDynamicRecoveryReply(situation)
	}
	if failure, isFound := latestStructuredFailureObservation(observations); isFound {
		return buildStructuredFailureRecoveryReply(request, failure, observations)
	}
	if requestUsesKorean(request) {
		return buildKoreanDynamicRecoveryReply(request, situation)
	}
	return buildEnglishDynamicRecoveryReply(situation)
}

func latestStructuredFailureObservation(observations []turnObservation) (turnObservation, bool) {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if !observation.IsError {
			continue
		}
		if strings.TrimSpace(observation.ErrorCode) == "" && strings.TrimSpace(observation.FailureStage) == "" {
			continue
		}
		return observation, true
	}
	return turnObservation{}, false
}

func buildStructuredFailureRecoveryReply(request AgentTurnRequest, observation turnObservation, observations []turnObservation) string {
	if requestUsesKorean(request) {
		return buildKoreanStructuredFailureRecoveryReply(request, observation, observations)
	}
	return buildEnglishStructuredFailureRecoveryReply(observation, observations)
}

func buildKoreanStructuredFailureRecoveryReply(request AgentTurnRequest, observation turnObservation, observations []turnObservation) string {
	prefix := koreanRequesterPrefix(request)
	stage := firstNonEmptyString(observation.FailureStage, observation.Tool, "tool")
	errorCode := firstNonEmptyString(observation.ErrorCode, "unknown_error")
	detail := firstNonEmptyString(observation.Message, observation.Content)
	attempts := attemptedActionSummary(observations)
	nextStep := koreanStructuredFailureNextStep(observation)
	if attempts != "" {
		return prefix + attempts + "까지 시도했지만 " + stage + "/" + errorCode + " 단계에서 막혔습니다. " + truncateText(compactWhitespace(detail), 180) + nextStep
	}
	return prefix + "요청을 끝내지 못한 지점은 " + stage + "이고 오류 코드는 " + errorCode + "입니다. " + truncateText(compactWhitespace(detail), 180) + nextStep
}

func koreanStructuredFailureNextStep(observation turnObservation) string {
	if observation.Retryable {
		return " 일시적 오류로 분류되어 재시도 대상이지만, 이번 실행 안에서는 완료 확인까지 가지 못했습니다."
	}
	return " 같은 방식의 즉시 재시도는 안전하지 않아서 추가 확인이 필요합니다."
}

func buildEnglishStructuredFailureRecoveryReply(observation turnObservation, observations []turnObservation) string {
	stage := firstNonEmptyString(observation.FailureStage, observation.Tool, "tool")
	errorCode := firstNonEmptyString(observation.ErrorCode, "unknown_error")
	detail := firstNonEmptyString(observation.Message, observation.Content)
	attempts := attemptedActionSummary(observations)
	nextStep := " Additional verification is needed before retrying the same action."
	if observation.Retryable {
		nextStep = " It was classified as retryable, but this run still did not reach confirmed completion."
	}
	if attempts != "" {
		return "I tried " + attempts + ", but got stuck at " + stage + "/" + errorCode + ". " + truncateText(compactWhitespace(detail), 180) + nextStep
	}
	return "I could not finish the request. The failed stage was " + stage + " with errorCode=" + errorCode + ". " + truncateText(compactWhitespace(detail), 180) + nextStep
}

func attemptedActionSummary(observations []turnObservation) string {
	actions := []string{}
	for _, observation := range observations {
		if observation.Action != "call_tool" || strings.TrimSpace(observation.Tool) == "" {
			continue
		}
		label := strings.TrimSpace(observation.Tool)
		if observation.IsError && strings.TrimSpace(observation.FailureStage) != "" {
			label += "(" + strings.TrimSpace(observation.FailureStage) + ")"
		}
		if len(actions) == 0 || actions[len(actions)-1] != label {
			actions = append(actions, label)
		}
		if len(actions) >= 4 {
			break
		}
	}
	return strings.Join(actions, " -> ")
}

func BuildIncompleteTaskRecoveryReply(prompt string, reason string) string {
	return buildDynamicRecoveryReply(AgentTurnRequest{Prompt: prompt}, reason, nil, nil, "failure")
}

func buildKoreanDynamicRecoveryReply(request AgentTurnRequest, situation string) string {
	prefix := koreanRequesterPrefix(request)
	switch situation {
	case "browser_blocked":
		return prefix + koreanRequestAction(request) + " 시도했는데 페이지가 자동화 접근을 막아서 정확한 확인을 끝내지 못했어요. 다른 출처나 직접 열 수 있는 링크가 있으면 거기서 다시 확인해볼게요."
	case "attachment_unavailable":
		return prefix + "파일을 만들었거나 보냈다고 확인할 첨부 근거가 없어서 완료됐다고 말할 수는 없어요. 지금 단계에서는 다시 실행해서 파일 생성부터 확인해야 해요."
	case "limit":
		return prefix + "요청을 진행하던 중 이번 실행에서 더 이어가기 어려운 한계에 닿아서 끝까지 마치지 못했어요. 지금까지 확인된 상태를 바탕으로 다시 시도하면 이어서 처리할 수 있어요."
	case "model_unavailable":
		return prefix + "지금 답을 이어서 만들 모델 호출이 끊겨서 요청을 끝까지 처리하지 못했어요. 잠시 뒤 다시 시도하면 현재 상태를 바탕으로 이어서 해볼게요."
	default:
		return prefix + "지금 이 방식으로는 요청을 끝까지 처리하지 못했어요. 원인을 기록해 두었으니 다시 시도하면 현재 상태를 바탕으로 이어서 확인해볼게요."
	}
}

func buildEnglishDynamicRecoveryReply(situation string) string {
	switch situation {
	case "browser_blocked":
		return "I tried to check it, but the page blocked automated access, so I could not finish verifying the exact result. If you share another source or a page I can access, I can try from there."
	case "attachment_unavailable":
		return "I do not have attachment evidence that the file was created or sent, so I cannot honestly say it was delivered. This run is not complete yet, and the file creation needs to be tried again."
	case "limit":
		return "I started working on it, but this run hit a limit before I could finish. I can try again from the current state and continue the work."
	case "model_unavailable":
		return "The model call I needed to continue the answer dropped, so I could not finish the request this time. If you try again shortly, I can pick it back up from the current state."
	default:
		return "I could not finish the request in the current run. The reason is recorded, and I can try again from the current state."
	}
}

func buildLastResortRecoveryReply(request AgentTurnRequest, fallbackKind string) string {
	if requestUsesKorean(request) {
		if fallbackKind == "limit" {
			return koreanRequesterPrefix(request) + "요청을 진행했지만 이번 실행 안에서는 끝까지 마치지 못했어요. 다시 시도하면 이어서 처리해볼게요."
		}
		return koreanRequesterPrefix(request) + "지금은 요청을 끝까지 처리하지 못했어요. 다시 시도하면 이어서 확인해볼게요."
	}
	if fallbackKind == "limit" {
		return "I started the request but could not finish it in this run. I can try again and continue from here."
	}
	return "I could not finish the request this time. I can try again and pick it back up from here."
}

func recoverySituationFor(reason string, observations []turnObservation, attachments []FileAttachment, fallbackKind string) string {
	combinedText := strings.ToLower(strings.Join([]string{reason, buildFailureObservationSummary(observations)}, "\n"))
	if strings.Contains(combinedText, "blocked_by_captcha") || strings.Contains(combinedText, "bot-detection") || strings.Contains(combinedText, "captcha") {
		return "browser_blocked"
	}
	if strings.Contains(combinedText, "attachment") || strings.Contains(combinedText, "file.attach") || strings.Contains(combinedText, "첨부") {
		return "attachment_unavailable"
	}
	if fallbackKind == "limit" || strings.Contains(combinedText, "max_") || strings.Contains(combinedText, "limit") {
		return "limit"
	}
	if strings.Contains(combinedText, "llm") || strings.Contains(combinedText, "language model") || strings.Contains(combinedText, "model") {
		return "model_unavailable"
	}
	if len(attachments) == 0 && strings.Contains(combinedText, "file") {
		return "attachment_unavailable"
	}
	return "general"
}

func requestUsesKorean(request AgentTurnRequest) bool {
	return containsKoreanText(strings.Join([]string{request.Prompt, request.RequesterCallingName, request.RequesterName}, " "))
}

func koreanRequesterPrefix(request AgentTurnRequest) string {
	name := firstNonEmptyString(request.RequesterCallingName, request.RequesterName)
	if name == "" {
		return ""
	}
	return name + " 님, "
}

func koreanRequestAction(request AgentTurnRequest) string {
	prompt := strings.TrimSpace(request.Prompt)
	switch {
	case strings.Contains(prompt, "날씨"):
		return "날씨를 확인하려고"
	case strings.Contains(prompt, "검색"):
		return "검색하려고"
	case strings.Contains(prompt, "파일") || strings.Contains(strings.ToLower(prompt), "html") || strings.Contains(strings.ToLower(prompt), "ppt"):
		return "파일을 만들거나 확인하려고"
	default:
		return "요청을 처리하려고"
	}
}

func failureReplyIsInvalid(reply string, attachments []FileAttachment) bool {
	if strings.Contains(reply, "I am having trouble reaching the language model") {
		return true
	}
	if strings.Contains(reply, "model configuration") {
		return true
	}
	if strings.Contains(reply, "configuration can be fixed") {
		return true
	}
	if ValidateFinalReplyDelivery(reply, attachments, true) != nil {
		return true
	}
	return len(attachments) == 0 && FinalReplyClaimsAttachmentDelivery(reply)
}

func failureReplyIsInvalidForRequest(reply string, request AgentTurnRequest, failureReason string, observations []turnObservation, attachments []FileAttachment) bool {
	if failureReplyIsInvalid(reply, attachments) {
		return true
	}
	return structuredFailureDetailsAreMissing(reply, failureReason, observations, attachments)
}

func structuredFailureDetailsAreMissing(reply string, failureReason string, observations []turnObservation, attachments []FileAttachment) bool {
	if recoverySituationFor(failureReason, observations, attachments, "failure") != "general" {
		return false
	}
	failure, isFound := latestStructuredFailureObservation(observations)
	if !isFound {
		return false
	}
	return !containsFailureDetail(reply, failure.FailureStage) || !containsFailureDetail(reply, failure.ErrorCode)
}

func containsFailureDetail(reply string, value string) bool {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return true
	}
	return strings.Contains(strings.ToLower(reply), strings.ToLower(trimmedValue))
}

func containsKoreanText(value string) bool {
	for _, character := range value {
		if character >= '\uac00' && character <= '\ud7a3' {
			return true
		}
		if character >= '\u3131' && character <= '\u318e' {
			return true
		}
	}
	return false
}

func (agentTurnRunner *AgentTurnRunner) GenerateLimitReachedReply(request AgentTurnRequest, stopReason string, observations []turnObservation, attachments []FileAttachment) string {
	reply, _ := agentTurnRunner.generateLimitReachedReply(request, stopReason, observations, attachments)
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

func buildLimitReachedRepairPrompt(originalPrompt string, rejectedReply string, attachments []FileAttachment, repairCount int) string {
	sections := []string{
		originalPrompt,
		"Previous draft was rejected because it either exposed internal runtime details or claimed an attachment/tool result that is not available.",
		"Rewrite the final reply in natural user-facing language. Do not mention budgets, counters, exact limits, tool-call counts, iterations, seconds, or minutes. Do not use the exact canned sentence from any previous fallback.",
	}
	if len(attachments) == 0 {
		sections = append(sections, "No attachments are available. You may say the requested file or HTML was not completed or not attached. Do not say that a file, HTML, PPTX, PDF, deck, slide, or notes were attached, sent, delivered, completed, or created successfully.")
	}
	if repairCount > 1 {
		sections = append(sections, "Use one or two Korean sentences. Apologize briefly, say the run stopped before completion, and say the user can retry. Avoid all attachment-success wording.")
	}
	sections = append(sections, "Rejected draft:\n"+strings.TrimSpace(rejectedReply))
	return strings.Join(sections, "\n\n")
}

func limitReachedReplyIsInvalid(reply string, attachments []FileAttachment) bool {
	if containsForbiddenLimitReplyFragment(reply) {
		return true
	}
	if ValidateFinalReplyDelivery(reply, attachments, true) != nil {
		return true
	}
	return len(attachments) == 0 && FinalReplyClaimsAttachmentDelivery(reply)
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

func buildFailureObservationSummary(observations []turnObservation) string {
	lines := []string{}
	for _, observation := range observations {
		content := strings.TrimSpace(observation.Content)
		if observation.IsError {
			content = firstNonEmptyString(summarizeStructuredFailure(observation), content)
		}
		if content == "" {
			content = strings.TrimSpace(observation.Summary)
		}
		if content == "" {
			continue
		}
		label := strings.TrimSpace(observation.Tool)
		if label == "" {
			label = strings.TrimSpace(observation.Action)
		}
		if observation.IsError {
			label = label + " failed"
		}
		lines = append(lines, "- "+label+": "+truncateText(compactWhitespace(redactUnsafeText(content)), 360))
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

func errorString(errorValue error) string {
	if errorValue == nil {
		return ""
	}
	return errorValue.Error()
}
