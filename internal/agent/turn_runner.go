package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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
	RecoveryBudget       RecoveryBudget
	EffortLevel          EffortLevel
	ToolResultMaxBytes   int
	GenerationOptions    llm.GenerationOptions
}

type RecoveryBudget struct {
	CorrectedRetry int
	AlternateRoute int
	AdjacentTool   int
	NoToolFallback int
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
	RequesterEmail             string
	RequesterName              string
	RequesterPlatformUserID    string
	IsApprovalContinuation     bool
	ExistingTaskRunID          string
	Platform                   string
	RequesterCallingName       string
	RequesterHandle            string
	RequesterCircles           []string
	ProfileName                string
	ConversationID             string
	Prompt                     string
	ResponseLanguage           string
	VisibleContext             VisibleContext
	MemoryFacts                []memory.MemoryFact
	ToolSet                    *ToolSet
	AvailableSkills            []SkillInstruction
	PinnedToolNames            []string
	PinnedSkillNames           []string
	WorkspaceRootPath          string
	WorkspaceDefaultPath       string
	ActivePaths                []string
	InstructionPrompt          string
	InstructionSources         []InstructionSource
	SkillDecisions             []SkillSelectionDecision
	SkillRetrievalMode         string
	SkillIndexStatus           string
	SkillCandidateCount        int
	SkillQueries               []string
	RequiredEvidenceTools      []string
	RequiredAttachmentSuffixes []string
	OutcomeContract            OutcomeContract
	ActiveGoal                 ActiveGoal
	QualityAcceptanceGuidance  []string
	TurnStartedAt              time.Time
}

type AgentTurnResult struct {
	TaskRun         task.TaskRun
	FinalReply      string
	UserNotice      string
	ReplySuppressed bool
	Attachments     []FileAttachment
	RecoveryActions []RecoveryAction
	ToolNames       []string
}

type turnActionDocument struct {
	Action               string                        `json:"action"`
	FinalReply           string                        `json:"finalReply"`
	ToolName             string                        `json:"toolName"`
	ToolInput            json.RawMessage               `json:"toolInput"`
	ToolNames            []string                      `json:"toolNames"`
	SkillNames           []string                      `json:"skillNames"`
	Reason               string                        `json:"reason"`
	Reply                string                        `json:"reply"`
	FailureResolution    string                        `json:"failureResolution"`
	GoalStatus           string                        `json:"goalStatus"`
	GoalSatisfied        *bool                         `json:"goalSatisfied"`
	CompletionEvidence   []completionEvidenceReference `json:"completionEvidence"`
	QualityCriteria      []qualityCriterion            `json:"qualityCriteria"`
	QualityReview        []qualityReviewItem           `json:"qualityReview"`
	RemainingWork        string                        `json:"remainingWork"`
	UsedFailureFacts     failureReportFacts            `json:"usedFailureFacts"`
	ExecutionStateUpdate ExecutionState                `json:"executionStateUpdate"`
}

type turnObservation struct {
	ObservationID        string               `json:"observationID"`
	Action               string               `json:"action"`
	Tool                 string               `json:"tool,omitempty"`
	Output               ToolOutput           `json:"output,omitempty"`
	Failure              *ToolFailure         `json:"failure,omitempty"`
	Summary              string               `json:"summary,omitempty"`
	ImageRefs            []ToolResultImageRef `json:"imageRefs,omitempty"`
	ToolInputKey         string               `json:"toolInputKey,omitempty"`
	AttemptFingerprint   string               `json:"attemptFingerprint,omitempty"`
	RecoveryAttemptKey   string               `json:"recoveryAttemptKey,omitempty"`
	RecoveryStep         string               `json:"recoveryStep,omitempty"`
	RecoveryAttemptSpent bool                 `json:"recoveryAttemptSpent,omitempty"`
	Attachments          []FileAttachment     `json:"attachments,omitempty"`
	RecoveryActions      []RecoveryAction     `json:"recoveryActions,omitempty"`
}

type ToolResultImageRef struct {
	ObservationID   string `json:"observationID"`
	AttachmentIndex int    `json:"attachmentIndex"`
	MimeType        string `json:"mimeType,omitempty"`
	Filename        string `json:"filename,omitempty"`
}

func (observation turnObservation) Failed() bool {
	return observation.Failure != nil
}

func (observation turnObservation) ContentText() string {
	if strings.TrimSpace(observation.Output.Content) != "" {
		return observation.Output.Content
	}
	if len(observation.Output.Data) > 0 {
		return string(observation.Output.Data)
	}
	return ""
}

func (observation turnObservation) FailureCode() string {
	if observation.Failure == nil {
		return ""
	}
	return strings.TrimSpace(observation.Failure.Code)
}

func (observation turnObservation) FailureStage() string {
	if observation.Failure == nil {
		return ""
	}
	return strings.TrimSpace(observation.Failure.Stage)
}

func (observation turnObservation) FailureSummary() string {
	if observation.Failure == nil {
		return ""
	}
	return strings.TrimSpace(observation.Failure.UserSafeSummary)
}

func (observation turnObservation) Retryable() bool {
	return observation.Failure != nil && observation.Failure.Retryable
}

func (observation turnObservation) SafeRetry() bool {
	return observation.Failure != nil && observation.Failure.SafeRetry
}

func newContentObservation(observationID string, action string, tool string, content string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        strings.TrimSpace(action),
		Tool:          strings.TrimSpace(tool),
		Output:        ToolOutput{Content: strings.TrimSpace(content)},
	}
}

func newFailureObservation(observationID string, action string, tool string, content string, kind FailureKind, code FailureCode, stage string) turnObservation {
	observation := newContentObservation(observationID, action, tool, content)
	observation.Failure = &ToolFailure{
		Kind:            normalizeFailureKind(kind),
		Code:            CanonicalFailureCode(code),
		Stage:           strings.TrimSpace(stage),
		UserSafeSummary: strings.TrimSpace(content),
	}
	return observation
}

func withObservationContent(observation turnObservation, content string) turnObservation {
	observation.Output.Content = strings.TrimSpace(content)
	return observation
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
	if recoveryBudgetIsUnset(options.RecoveryBudget) {
		options.RecoveryBudget = defaultRecoveryBudget()
	} else {
		options.RecoveryBudget = normalizeRecoveryBudget(options.RecoveryBudget)
	}
	if options.RecoveryAttemptLimit <= 0 {
		options.RecoveryAttemptLimit = recoveryToolBudgetTotal(options.RecoveryBudget)
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
	request.ResponseLanguage = ResolveResponseLanguage(request.ResponseLanguage)
	request, _ = applyManualCapabilityRequirement(request, manualCapabilityRequirement{
		ToolNames:  request.PinnedToolNames,
		SkillNames: request.PinnedSkillNames,
	})

	taskRun := agentTurnRunner.taskRunForRequest(request)
	runningTaskRun, errorValue := agentTurnRunner.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue == nil {
		taskRun = runningTaskRun
	}
	taskContext, taskCancel := context.WithCancel(turnContext)
	unregisterTaskCancel := agentTurnRunner.taskRunService.RegisterTaskRunCancel(taskRun.TaskRunID, taskCancel)
	defer unregisterTaskCancel()
	defer taskCancel()
	agentTurnRunner.appendInstructionEvent(taskRun.TaskRunID, request)

	state := buildInitialAgentTaskState(request, agentTurnRunner.options, taskRun.TaskRunID)
	state.Status = taskRun.Status
	toolUseRequirements := state.Requirements
	successfulToolCalls := map[string]turnObservation{}
	limitPressureWarnings := map[string]bool{}
	lastProgressEventCount := progressEventCount(state.Observations)
	consecutiveNoProgressActionCount := 0
	stopForNoProgress := func(stepID string) (AgentTurnResult, bool) {
		currentProgressEventCount := progressEventCount(state.Observations)
		if currentProgressEventCount > lastProgressEventCount {
			lastProgressEventCount = currentProgressEventCount
			consecutiveNoProgressActionCount = 0
			return AgentTurnResult{}, false
		}
		consecutiveNoProgressActionCount++
		if consecutiveNoProgressActionCount < 3 {
			return AgentTurnResult{}, false
		}
		reason := "stopped after 3 consecutive model actions without workspace, tool, artifact, attachment, or new failure progress"
		agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.no_progress_loop_stopped", marshalEventBody(map[string]any{
			"reason":                           reason,
			"consecutiveNoProgressActionCount": consecutiveNoProgressActionCount,
			"progressEventCount":               currentProgressEventCount,
		}))
		agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "no_progress_loop_stopped", reason)
		result, _ := agentTurnRunner.failTurn(taskRun.TaskRunID, request, reason, state.Observations, state.Attachments, state.ExecutionState)
		return result, true
	}
	for iteration := 1; iteration <= agentTurnRunner.options.MaxIterationCount; iteration++ {
		if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRun.TaskRunID, state.Attachments); isCancelled {
			return cancelledResult, nil
		}
		state.IterationCount = iteration - 1
		if warning := agentTurnRunner.nextLimitPressureWarning(iteration-1, state.ToolCallCount, len(state.Observations)+1, limitPressureWarnings); warning != nil {
			state.Observations = append(state.Observations, warning.Observation)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.limit_pressure", marshalEventBody(warning.EventBody))
			limitPressureWarnings[warning.Level] = true
		}
		stepID := fmt.Sprintf("%s:turn-%03d", taskRun.TaskRunID, iteration)
		agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusRunning, "agent turn iteration", "")

		transition := agentTurnRunner.applyCompletionState(taskContext, taskRun.TaskRunID, stepID, request, toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria)
		state.Observations = transition.Observations
		state.Attachments = transition.Attachments
		if transition.IsCompleted {
			return transition.Result, nil
		}
		if transition.DidTransition {
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "completion_state "+string(transition.Action), "")
			continue
		}

		actionDocument, actionError := agentTurnRunner.nextAction(taskContext, request, toolUseRequirements, state.Observations, state.ExecutionState, len(state.QualityCriteria) == 0)
		if actionError != nil {
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "agent turn iteration", actionError.Error())
			if errors.Is(actionError, context.Canceled) {
				return agentTurnRunner.cancelledTaskResultOrCurrent(taskRun.TaskRunID, state.Attachments), nil
			}
			if errors.Is(actionError, context.DeadlineExceeded) {
				return agentTurnRunner.stopForLimit(taskRun.TaskRunID, request, "max_elapsed", state.Observations, state.Attachments, state.ExecutionState, iteration-1, state.ToolCallCount)
			}
			return agentTurnRunner.failTurn(taskRun.TaskRunID, request, "llm action failed: "+actionError.Error(), state.Observations, state.Attachments, state.ExecutionState)
		}

		if !executionStateIsEmpty(actionDocument.ExecutionStateUpdate) {
			state.ExecutionState = normalizeExecutionState(actionDocument.ExecutionStateUpdate)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.execution_state", marshalEventBody(state.ExecutionState))
		}
		agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.action", marshalEventBody(actionDocument))
		switch strings.TrimSpace(actionDocument.Action) {
		case "require_capabilities":
			requirement := manualCapabilityRequirement{
				ToolNames:  append([]string{}, actionDocument.ToolNames...),
				SkillNames: append([]string{}, actionDocument.SkillNames...),
				Reason:     actionDocument.Reason,
			}
			nextRequest, requirementResult := applyManualCapabilityRequirement(request, requirement)
			request = nextRequest
			state.Request = nextRequest
			observation := manualCapabilityObservation(len(state.Observations)+1, requirement, requirementResult)
			state.Observations = append(state.Observations, observation)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.capabilities_required", marshalEventBody(map[string]any{
				"requirement": requirement,
				"result":      requirementResult,
				"source":      "manual_require",
			}))
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "require_capabilities", observation.ContentText())
			continue
		case "set_quality_criteria":
			state.QualityCriteria = normalizeQualityCriteria(actionDocument.QualityCriteria)
			observation := turnObservation{
				ObservationID: nextObservationID(len(state.Observations) + 1),
				Action:        "set_quality_criteria",
				Output:        ToolOutput{Content: marshalEventBody(map[string]any{"criteria": state.QualityCriteria})},
			}
			state.Observations = append(state.Observations, observation)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.quality_criteria", marshalEventBody(map[string]any{
				"criteria": state.QualityCriteria,
			}))
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "set_quality_criteria", marshalEventBody(map[string]any{"criteria": state.QualityCriteria}))
			continue
		case "final_reply":
			completionGateResult := validateCompletionGateForRequestWithRecoveryBudget(request, toolUseRequirements, state.Observations, state.QualityCriteria, actionDocument, agentTurnRunner.options.RecoveryBudget)
			agentTurnRunner.appendValidityReview(taskRun.TaskRunID, "final_reply", completionGateResult.ValidityState)
			if !completionGateResult.IsSatisfied {
				observation := completionGateObservation(len(state.Observations)+1, completionGateResult.Message)
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, completionGateEventName(observation), marshalEventBody(observation))
				if observation.Action == "evidence_missing" {
					agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.completion_required", marshalEventBody(observation))
				}
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, observation.Action, observation.ContentText())
				if result, shouldStop := stopForNoProgress(stepID); shouldStop {
					return result, nil
				}
				continue
			}
			agentTurnRunner.appendQualityReview(taskRun.TaskRunID, state.QualityCriteria, actionDocument.QualityReview, state.Observations)
			reply := strings.TrimSpace(actionDocument.FinalReply)
			if reply == "" {
				reply = strings.TrimSpace(actionDocument.Reply)
			}
			if reply == "" {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "final_reply", "empty final reply")
				return agentTurnRunner.failTurn(taskRun.TaskRunID, request, "empty final reply", state.Observations, state.Attachments, state.ExecutionState)
			}
			if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRun.TaskRunID, state.Attachments); isCancelled {
				return cancelledResult, nil
			}
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "final_reply", reply)
			completedTaskRun, completeError := agentTurnRunner.taskRunService.CompleteTaskRun(taskRun.TaskRunID, reply)
			if completeError != nil {
				return agentTurnRunner.cancelledTaskResultOrCurrent(taskRun.TaskRunID, state.Attachments), nil
			}
			return AgentTurnResult{TaskRun: completedTaskRun, FinalReply: reply, Attachments: completionGateResult.Attachments, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, nil
		case "call_tool":
			if !toolAvailableForAction(request.ToolSet, actionDocument.ToolName) {
				observation := agentTurnRunner.recordUnavailableToolRequest(taskRun.TaskRunID, len(state.Observations)+1, actionDocument.ToolName, actionDocument.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt)
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "tool_unavailable "+actionDocument.ToolName, observation.ContentText())
				if result, shouldStop := stopForNoProgress(stepID); shouldStop {
					return result, nil
				}
				continue
			}
			if shouldRejectUnnecessarySiteApprovalRequest(request, actionDocument.ToolName, actionDocument.ToolInput) {
				observation := newFailureObservation(nextObservationID(len(state.Observations)+1), "policy", actionDocument.ToolName, unnecessarySiteApprovalMessage(), FailurePolicyBlocked, FailureCodes.PolicyBlocked, "policy")
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.approval_request_rejected", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "approval_request_rejected", observation.ContentText())
				if result, shouldStop := stopForNoProgress(stepID); shouldStop {
					return result, nil
				}
				continue
			}
			if validationError := validateBrowserToolInput(actionDocument.ToolName, actionDocument.ToolInput); validationError != nil {
				observation := newFailureObservation(nextObservationID(len(state.Observations)+1), "call_tool", actionDocument.ToolName, validationError.Error(), FailureInvalidInput, FailureCodes.InvalidInput, "tool_input")
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.tool_input_malformed", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "malformed_tool_input "+actionDocument.ToolName, observation.ContentText())
				if result, shouldStop := stopForNoProgress(stepID); shouldStop {
					return result, nil
				}
				continue
			}
			if validationError := validateTerminalToolInput(actionDocument.ToolName, actionDocument.ToolInput, request.ToolSet); validationError != nil {
				failureCode := FailureCodes.InvalidInput
				if isTerminalToolNameError(validationError) {
					failureCode = FailureCodes.ToolNameInShell
				}
				observation := newFailureObservation(nextObservationID(len(state.Observations)+1), "call_tool", actionDocument.ToolName, validationError.Error(), FailureInvalidInput, failureCode, "tool_input")
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.tool_input_malformed", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "malformed_tool_input "+actionDocument.ToolName, observation.ContentText())
				if result, shouldStop := stopForNoProgress(stepID); shouldStop {
					return result, nil
				}
				continue
			}
			if sentObservation, wasSent := previousSuccessfulExternalSend(state.Observations, actionDocument.ToolName); wasSent {
				observation := turnObservation{
					ObservationID: nextObservationID(len(state.Observations) + 1),
					Action:        "policy",
					Tool:          strings.TrimSpace(actionDocument.ToolName),
					Output:        ToolOutput{Content: "This task already completed an external send as " + sentObservation.ObservationID + ". Do not send another message in the same task. Use that observation for completionEvidence and finish."},
					Failure:       &ToolFailure{Kind: FailurePolicyBlocked, Code: FailureCodes.PolicyBlocked.String(), Stage: "policy", UserSafeSummary: "This task already completed an external send."},
				}
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.external_send_repeat_rejected", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "external_send_repeat_rejected "+actionDocument.ToolName, observation.ContentText())
				if result, shouldStop := stopForNoProgress(stepID); shouldStop {
					return result, nil
				}
				continue
			}
			if duplicateObservation, isDuplicate := successfulToolCalls[canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)]; isDuplicate && handlesDuplicateSuccessfulToolCall(actionDocument.ToolName) {
				observation := turnObservation{
					ObservationID: nextObservationID(len(state.Observations) + 1),
					Action:        "policy",
					Tool:          strings.TrimSpace(actionDocument.ToolName),
					Output:        ToolOutput{Content: "This exact tool call already succeeded as " + duplicateObservation.ObservationID + ". Use that observation for completionEvidence instead of running it again."},
					Failure:       &ToolFailure{Kind: FailurePolicyBlocked, Code: FailureCodes.PolicyBlocked.String(), Stage: "policy", UserSafeSummary: "This exact tool call already succeeded."},
				}
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.duplicate_tool_call_rejected", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "duplicate_tool_call "+actionDocument.ToolName, observation.ContentText())
				if result, shouldStop := stopForNoProgress(stepID); shouldStop {
					return result, nil
				}
				continue
			}
			if duplicateFailure, isDuplicateFailure := previousFailedToolInput(state.Observations, actionDocument.ToolName, actionDocument.ToolInput); isDuplicateFailure {
				observation := repeatedFailedAttemptObservation(len(state.Observations)+1, duplicateFailure)
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.failed_fingerprint_rejected", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "failed_fingerprint_rejected "+actionDocument.ToolName, observation.ContentText())
				if result, shouldStop := stopForNoProgress(stepID); shouldStop {
					return result, nil
				}
				continue
			}
			recoveryStep := ""
			if failureDebt, hasFailureDebt := activeFailureDebt(state.Observations); hasFailureDebt {
				recoveryStep = classifyRecoveryStep(failureDebt, actionDocument.ToolName)
				if !recoveryBudgetAllowsStep(state.Observations, agentTurnRunner.options.RecoveryBudget, recoveryStep) {
					observation := recoveryBudgetExhaustedObservation(len(state.Observations)+1, failureDebt.LatestFailure, recoveryStep)
					state.Observations = append(state.Observations, observation)
					agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.recovery_budget_exhausted", marshalEventBody(observation))
					agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "recovery_budget_exhausted "+actionDocument.ToolName, observation.ContentText())
					if result, shouldStop := stopForNoProgress(stepID); shouldStop {
						return result, nil
					}
					continue
				}
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.recovery_attempt", marshalEventBody(map[string]any{
					"status":       "started",
					"recoveryStep": recoveryStep,
					"toolName":     strings.TrimSpace(actionDocument.ToolName),
					"debt":         failureDebt,
				}))
			}
			state.ToolCallCount++
			if state.ToolCallCount > maxToolCallCountWithRecovery(agentTurnRunner.options, state.Observations) {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusBlocked, "limit stop", "max_tool_calls")
				return agentTurnRunner.finalizeOrStopForLimit(taskContext, taskRun.TaskRunID, request, "max_tool_calls", toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria, state.ExecutionState, iteration, maxToolCallCountWithRecovery(agentTurnRunner.options, state.Observations))
			}
			observation := agentTurnRunner.invokeTool(taskContext, request.ToolSet, taskRun.TaskRunID, nextObservationID(len(state.Observations)+1), actionDocument.ToolName, actionDocument.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage)
			if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRun.TaskRunID, state.Attachments); isCancelled {
				return cancelledResult, nil
			}
			if recoveryStep != "" {
				observation.RecoveryStep = recoveryStep
				observation.RecoveryAttemptSpent = true
				observation.RecoveryAttemptKey = canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)
			}
			state.Observations = append(state.Observations, observation)
			state.Attachments = appendObservationAttachments(state.Attachments, observation)
			if observation.Failed() {
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.failure_debt_created", marshalEventBody(activeFailureDebtEventBody(state.Observations, agentTurnRunner.options.RecoveryBudget)))
				if recoveryAttemptCount(state.Observations) < agentTurnRunner.options.RecoveryAttemptLimit {
					recoveryObservation := recoveryGuidanceObservation(len(state.Observations)+1, observation)
					state.Observations = append(state.Observations, recoveryObservation)
					agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.recovery_guidance", marshalEventBody(recoveryObservation))
				}
			}
			if pausedResult, isPaused := agentTurnRunner.pausedTaskResult(taskRun.TaskRunID, observation, state.Attachments); isPaused {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, pausedResult.TaskRun.Status, "call_tool "+actionDocument.ToolName, observation.ContentText())
				return pausedResult, nil
			}
			if !observation.Failed() {
				successfulToolCalls[canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)] = observation
			}
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "call_tool "+actionDocument.ToolName, observation.ContentText())
		case "fail":
			if failureDebt, hasFailureDebt := activeFailureDebt(state.Observations); hasFailureDebt && !recoveryToolBudgetExhaustedForRequest(state.Observations, request.ToolSet, agentTurnRunner.options.RecoveryBudget, failureDebt) {
				observation := recoveryGuidanceObservation(len(state.Observations)+1, failureDebt.LatestFailure)
				observation = withObservationContent(observation, "FailureDebt is still active. Try a different recovery step within budget, answer without tools using failureResolution=no_tool_fallback if enough context exists, or fail only after recovery budget is exhausted. "+observation.ContentText())
				observation.Summary = observation.ContentText()
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.recovery_blocked_fail", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "recovery_required", observation.ContentText())
				if result, shouldStop := stopForNoProgress(stepID); shouldStop {
					return result, nil
				}
				continue
			}
			if _, hasFailureDebt := activeFailureDebt(state.Observations); hasFailureDebt {
				facts := buildFailureReportFacts(state.Observations, agentTurnRunner.options.RecoveryBudget)
				failureReportResult := validateFailureReportAction(actionDocument, facts)
				if !failureReportResult.IsSatisfied {
					observation := completionGateObservation(len(state.Observations)+1, failureReportResult.Message)
					state.Observations = append(state.Observations, observation)
					agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.failure_report_rejected", marshalEventBody(observation))
					agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "failure_report_rejected", observation.ContentText())
					if result, shouldStop := stopForNoProgress(stepID); shouldStop {
						return result, nil
					}
					continue
				}
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.failure_report_facts_used", marshalEventBody(actionDocument.UsedFailureFacts))
			}
			reason := firstNonEmptyString(actionDocument.Reason, "agent reported failure")
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "fail", reason)
			return agentTurnRunner.failTurn(taskRun.TaskRunID, request, reason, state.Observations, state.Attachments, state.ExecutionState)
		default:
			observation := newFailureObservation(nextObservationID(len(state.Observations)+1), "invalid_action", "", "unknown action: "+actionDocument.Action, FailureInvalidInput, FailureCodes.InvalidInput, "action_parse")
			state.Observations = append(state.Observations, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "invalid_action", observation.ContentText())
			if result, shouldStop := stopForNoProgress(stepID); shouldStop {
				return result, nil
			}
		}
	}

	return agentTurnRunner.finalizeOrStopForLimit(taskContext, taskRun.TaskRunID, request, "max_iterations", toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria, state.ExecutionState, agentTurnRunner.options.MaxIterationCount, state.ToolCallCount)
}

func (agentTurnRunner *AgentTurnRunner) taskRunForRequest(request AgentTurnRequest) task.TaskRun {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := agentTurnRunner.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun
		}
	}
	return agentTurnRunner.taskRunService.CreateTaskRun(request.RequesterPersonID, request.ConversationID, request.Prompt)
}

func (agentTurnRunner *AgentTurnRunner) pausedTaskResult(taskRunID string, observation turnObservation, attachments []FileAttachment) (AgentTurnResult, bool) {
	taskRun, isFound := agentTurnRunner.taskRunService.FindTaskRun(taskRunID)
	if !isFound || !isWaitingForUser(taskRun.Status) {
		return AgentTurnResult{}, false
	}
	if taskRun.Status == task.TaskStatusWaitingApproval {
		reply := approvalObservationUserFacingMessage(observation)
		if reply == "" {
			agentTurnRunner.appendEvent(taskRunID, "agent.approval_user_facing_message_missing", marshalEventBody(observation))
		}
		return AgentTurnResult{TaskRun: taskRun, UserNotice: reply, Attachments: attachments, RecoveryActions: observation.RecoveryActions}, true
	}
	reply := firstNonEmptyString(taskRun.FailureReason, toolObservationMessage(observation), observation.ContentText())
	return AgentTurnResult{TaskRun: taskRun, UserNotice: reply, Attachments: attachments, RecoveryActions: observation.RecoveryActions}, true
}

func (agentTurnRunner *AgentTurnRunner) cancelledTaskResult(taskRunID string, attachments []FileAttachment) (AgentTurnResult, bool) {
	taskRun, isFound := agentTurnRunner.taskRunService.FindTaskRun(taskRunID)
	if !isFound || taskRun.Status != task.TaskStatusCancelled {
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, "task.stop.outbox_suppressed", "task run was cancelled before reply delivery")
	return AgentTurnResult{TaskRun: taskRun, ReplySuppressed: true, Attachments: attachments}, true
}

func (agentTurnRunner *AgentTurnRunner) cancelledTaskResultOrCurrent(taskRunID string, attachments []FileAttachment) AgentTurnResult {
	if result, isCancelled := agentTurnRunner.cancelledTaskResult(taskRunID, attachments); isCancelled {
		return result
	}
	taskRun, _ := agentTurnRunner.taskRunService.FindTaskRun(taskRunID)
	return AgentTurnResult{TaskRun: taskRun, ReplySuppressed: true, Attachments: attachments}
}

func isWaitingForUser(status task.TaskStatus) bool {
	return status == task.TaskStatusWaitingApproval || status == task.TaskStatusWaitingUserInput
}

func toolObservationMessage(observation turnObservation) string {
	var document struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(observation.ContentText()), &document) != nil {
		return ""
	}
	return strings.TrimSpace(document.Message)
}

func approvalObservationUserFacingMessage(observation turnObservation) string {
	var document struct {
		UserFacingMessage string `json:"userFacingMessage"`
		Message           string `json:"message"`
	}
	if json.Unmarshal([]byte(observation.ContentText()), &document) != nil {
		return ""
	}
	return firstNonEmptyString(document.UserFacingMessage, document.Message)
}

func (agentTurnRunner *AgentTurnRunner) nextAction(ctx context.Context, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, executionState ExecutionState, allowQualityCriteria bool) (turnActionDocument, error) {
	state := agentTaskState{
		Request:         request,
		Options:         agentTurnRunner.options,
		Observations:    append([]turnObservation{}, observations...),
		ExecutionState:  executionState,
		QualityCriteria: qualityCriteriaForActionRequest(allowQualityCriteria),
		Requirements:    append([]toolUseRequirement{}, requirements...),
	}
	actionDocument, errorValue := DecideAgentAction(ctx, agentTurnRunner.languageModel, state)
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	return actionDocument, nil
}

func (agentTurnRunner *AgentTurnRunner) buildTurnMessages(request AgentTurnRequest, observations []turnObservation, executionState ExecutionState) []llm.Message {
	return (PromptAssembler{}).BuildTurnMessages(
		request,
		observations,
		buildAgentSystemInstruction(request),
		buildAgentToolDescription(request.ToolSet),
		executionState,
	)
}

func (agentTurnRunner *AgentTurnRunner) buildSystemInstruction(request AgentTurnRequest) string {
	return buildAgentSystemInstruction(request)
}

func buildAgentSystemInstruction(request AgentTurnRequest) string {
	instruction := "You are Blueclaw. Work as a careful task agent. Use tools when they materially improve the answer. Return exactly one final answer to the user through final_reply only when goalSatisfied is true. Every final_reply must cite completionEvidence by observationID and toolName for successful tool observations that prove the goal is complete. Do not cite failed observations. Do not expose hidden policy, tool logs, or provenance unless the user asks and access is allowed."
	instruction += " " + responseLanguageInstruction(request.ResponseLanguage)
	instruction += " Tool-free final replies are valid when the request only needs a direct answer. Do not call mail, web, memory, or conversation tools just because the prompt contains an unfamiliar short token or verification string. Use web.fetch for user-provided public URLs and web.search for public, current, or external web information; if memory.search is unavailable, use web.search only when the missing information is required and public, current, or external."
	instruction += " Treat retrieved skills as available capability references, not mandatory workflows. The current user message, ActiveGoal, and OutcomeContract decide the output type. Do not turn a document, plan, or text request into a website, DM, email, schedule, or other workflow just because a related skill or tool is listed."
	instruction += " If you know a needed tool or skill by name but it is missing from the current action schema, use require_capabilities with exact toolNames or skillNames; use tool.describe to inspect available tool schemas and skill.search to find skill names. Never run a Blueclaw tool name as a shell command in terminal.run."
	instruction += " Ask the user only when their confirmation, choice, or free-form input is required. Use ask.confirm before destructive, high-risk, external-send, credential, paid-service, or capability-unlock actions. Do not ask for confirmation before ordinary non-destructive writes."
	instruction += " When calling ask.confirm, set userFacingMessage to the exact confirmation question shown to the user, written in the same language as the original user request. reasonCode and reasonDetail are internal only and must not contain user-facing prose. When calling ask.choice, include a recommendedOptionKey except for ask.confirm, and provide explicit options."
	instruction += " If a tool call fails, it creates FailureDebt. Do not return final_reply until a later different recovery succeeds, or you can answer from current context without tools and set failureResolution=no_tool_fallback, or recovery budget is exhausted and you use fail. Never repeat the same failed tool input fingerprint; recovery must change the input, route/provider, tool, or fall back without tools."
	instruction += " Maintain executionStateUpdate on every structured action as compact working memory: goal, workspace, knownFacts, triedAndFailed, currentBlocker, and nextPlan. Keep it short and update it from the latest observation instead of copying raw logs."
	instruction += " For artifact work, set_quality_criteria and qualityReview are useful for your own acceptance criteria, but they are guidance and evidence, not a reason to withhold a usable artifact."
	if len(request.QualityAcceptanceGuidance) > 0 {
		instruction += " Quality guidance: " + strings.Join(request.QualityAcceptanceGuidance, " ")
	}
	if len(request.RequiredAttachmentSuffixes) > 0 {
		instruction += " This task requires attached artifacts with these filename suffixes before final_reply: " + strings.Join(request.RequiredAttachmentSuffixes, ", ") + "."
	}
	instruction += " Artifact workflow: write source under tmp/<slug>, run builds with terminal.run workingDirectoryPath tmp/<slug>, create outputs under build/, promote final outputs with file.promote to artifacts/<slug> or an allowed circle/shared destination, then attach promoted files with file.attach. final_reply may describe platform-attached filenames from completionEvidence, but must not expose sandbox URLs, file URLs, device paths, or local filesystem paths."
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
		"profileName":     normalizedAgentProfileName(request.ProfileName),
		"toolNames":       toolNamesForEvent(request.ToolSet),
		"sourceCount":     len(request.InstructionSources),
		"sources":         request.InstructionSources,
		"skillNames":      instructionSkillNames(request.InstructionSources),
		"skillDecisions":  request.SkillDecisions,
		"retrievalMode":   request.SkillRetrievalMode,
		"indexStatus":     request.SkillIndexStatus,
		"candidateCount":  request.SkillCandidateCount,
		"skillQueries":    request.SkillQueries,
		"activeGoal":      request.ActiveGoal,
		"outcomeContract": request.OutcomeContract,
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

type terminalToolNameError struct {
	toolName string
}

func (errorValue terminalToolNameError) Error() string {
	return errorValue.toolName + " is a Blueclaw tool, not a shell command. Use require_capabilities if the tool is hidden, then call " + errorValue.toolName + " directly with its tool schema."
}

func isTerminalToolNameError(errorValue error) bool {
	var typedError terminalToolNameError
	return errors.As(errorValue, &typedError)
}

func validateTerminalToolInput(toolName string, toolInput json.RawMessage, toolSets ...*ToolSet) error {
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
	var toolSet *ToolSet
	if len(toolSets) > 0 {
		toolSet = toolSets[0]
	}
	if commandToolName := firstTerminalCommandToken(command); toolSet != nil && toolSet.IsRegistered(commandToolName) {
		return terminalToolNameError{toolName: commandToolName}
	}
	for _, toolAlias := range []string{"file.write", "file.promote", "file.attach", "set_quality_criteria", "final_reply"} {
		if strings.Contains(command, toolAlias) {
			return errors.New(strings.TrimSpace(toolName) + " command cannot call Blueclaw action " + toolAlias + "; call that action directly instead")
		}
	}
	workingDirectoryPath := strings.TrimSpace(stringValue(inputDocument["workingDirectoryPath"]))
	if strings.HasPrefix(workingDirectoryPath, "tmp/") && commandContainsVirtualWorkspacePath(command) {
		return errors.New(strings.TrimSpace(toolName) + " command uses tmp/ or artifacts/ inside an already scoped tmp workingDirectoryPath; set workingDirectoryPath to tmp/<slug> and use relative paths such as . or presentation.md inside the command")
	}
	return nil
}

func firstTerminalCommandToken(command string) string {
	for _, token := range terminalCommandTokens(command) {
		token = strings.Trim(token, `"'`)
		if strings.TrimSpace(token) != "" {
			return token
		}
	}
	return ""
}

func commandContainsVirtualWorkspacePath(command string) bool {
	for _, token := range terminalCommandTokens(command) {
		token = strings.Trim(token, `"'`)
		if strings.HasPrefix(token, "tmp/") || strings.HasPrefix(token, "artifacts/") {
			return true
		}
	}
	return false
}

func terminalCommandTokens(command string) []string {
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
	return strings.Fields(replacer.Replace(command))
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

func previousSuccessfulExternalSend(observations []turnObservation, toolName string) (turnObservation, bool) {
	if !isUnsafeRepeatSensitiveTool(toolName) {
		return turnObservation{}, false
	}
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Action != "call_tool" || observation.Failed() {
			continue
		}
		if strings.TrimSpace(observation.Tool) == strings.TrimSpace(toolName) {
			return observation, true
		}
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
	if strings.TrimSpace(toolName) != "ask.confirm" {
		return false
	}
	if !sitePublishTaskToolsAreAvailable(request.ToolSet) {
		return false
	}
	if !selectedSkillNameSet(request.SkillDecisions)["site-prototype"] {
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

func toolAvailableForAction(toolRegistry *ToolSet, toolName string) bool {
	if toolRegistry == nil {
		return false
	}
	return toolRegistry.IsAllowed(strings.TrimSpace(toolName))
}

func (agentTurnRunner *AgentTurnRunner) recordUnavailableToolRequest(taskRunID string, index int, toolName string, toolInput json.RawMessage, workspaceRootPath string, minimumModifiedAt time.Time) turnObservation {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		trimmedToolName = "unknown_tool"
	}
	observationID := nextObservationID(index)
	toolInputKey := canonicalToolCallKey(trimmedToolName, toolInput)
	agentTurnRunner.appendEvent(taskRunID, "tool."+trimmedToolName+".requested", marshalEventBody(map[string]any{
		"observationID": observationID,
		"toolName":      trimmedToolName,
		"input":         json.RawMessage(toolInput),
	}))
	return agentTurnRunner.saveToolObservation(context.Background(), taskRunID, observationID, trimmedToolName, toolInputKey, ToolFailureResult(FailurePolicyBlocked, FailureCodes.PolicyBlocked, "tool_availability", "tool is not allowed"), workspaceRootPath, minimumModifiedAt)
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

func (agentTurnRunner *AgentTurnRunner) invokeTool(ctx context.Context, toolRegistry *ToolSet, taskRunID string, observationID string, toolName string, toolInput json.RawMessage, workspaceRootPath string, minimumModifiedAt time.Time, responseLanguage string) turnObservation {
	trimmedToolName := strings.TrimSpace(toolName)
	toolInputKey := canonicalToolCallKey(trimmedToolName, toolInput)
	if toolRegistry == nil {
		observation := toolFailureObservation(observationID, trimmedToolName, "tool registry was not configured")
		observation.ToolInputKey = toolInputKey
		observation.AttemptFingerprint = attemptFingerprint(toolInputKey, observation.FailureCode())
		return observation
	}
	agentTurnRunner.appendEvent(taskRunID, "tool."+trimmedToolName+".requested", marshalEventBody(map[string]any{
		"observationID": observationID,
		"toolName":      trimmedToolName,
		"input":         json.RawMessage(toolInput),
	}))
	toolContext := WithResponseLanguage(WithTaskRunID(ctx, taskRunID), responseLanguage)
	toolResult, errorValue := toolRegistry.Invoke(toolContext, ToolInvocation{ToolName: trimmedToolName, Input: toolInput})
	if errorValue != nil {
		toolResult = ToolFailureResult(FailureUnknown, FailureCodes.OperationFailed, trimmedToolName, errorValue.Error())
	}
	observation := agentTurnRunner.saveToolObservation(ctx, taskRunID, observationID, trimmedToolName, toolInputKey, toolResult, workspaceRootPath, minimumModifiedAt)
	return observation
}

func toolFailureObservation(observationID string, toolName string, message string) turnObservation {
	return newFailureObservation(observationID, "call_tool", toolName, message, FailureUnknown, FailureCodes.OperationFailed, firstNonEmptyString(toolName, "tool"))
}

func recoveryGuidanceObservation(index int, observation turnObservation) turnObservation {
	content := recoveryGuidanceContent(observation)
	return turnObservation{
		ObservationID:        nextObservationID(index),
		Action:               "recovery_guidance",
		Tool:                 observation.Tool,
		Output:               ToolOutput{Content: content},
		Summary:              content,
		Failure:              observation.Failure,
		ToolInputKey:         observation.ToolInputKey,
		RecoveryAttemptKey:   observation.RecoveryAttemptKey,
		RecoveryAttemptSpent: observation.RecoveryAttemptSpent,
	}
}

func recoveryGuidanceContent(observation turnObservation) string {
	parts := []string{"Analyze the latest failed tool result before responding."}
	if observation.FailureCode() != "" {
		parts = append(parts, "errorCode="+observation.FailureCode())
	}
	if observation.FailureStage() != "" {
		parts = append(parts, "failureStage="+observation.FailureStage())
	}
	if observation.FailureSummary() != "" {
		parts = append(parts, "message="+observation.FailureSummary())
	}
	if observation.RecoveryAttemptKey != "" {
		parts = append(parts, "A safe automatic retry has already been attempted for this tool input.")
	}
	if terminalRecoveryGuidance := terminalPathRecoveryGuidance(observation); terminalRecoveryGuidance != "" {
		parts = append(parts, terminalRecoveryGuidance)
	}
	if terminalDependencyGuidance := terminalPythonDependencyRecoveryGuidance(observation); terminalDependencyGuidance != "" {
		parts = append(parts, terminalDependencyGuidance)
	}
	for _, recoveryRoute := range recoveryRoutesForObservation(observation) {
		parts = append(parts, recoveryRoute.Guidance())
	}
	return strings.Join(parts, " ")
}

func terminalPathRecoveryGuidance(observation turnObservation) string {
	if strings.TrimSpace(observation.Tool) != "terminal.run" {
		return ""
	}
	switch strings.TrimSpace(observation.FailureStage()) {
	case "terminal_path_guardrail":
		return "Recovery route: retry terminal.run with virtual workspace paths: tmp/<slug> for draft work, home/<path> for requester-private durable source work, and artifacts/<slug> only after promotion. Do not call /opt/blueclaw, /tmp, concrete private POSIX paths, or runtime-internal paths directly. For built-in artifact skills, execute /workspace/skills/<skill>/scripts/skill_runtime.py and let the wrapper choose dependencies."
	case "terminal_working_directory_access":
		return "Recovery route: retry terminal.run with workingDirectoryPath set to tmp/<slug> or home/<path>, use relative paths inside the command, then promote accepted output to artifacts/<slug> or an allowed circle/shared path."
	default:
		return ""
	}
}

func terminalPythonDependencyRecoveryGuidance(observation turnObservation) string {
	if strings.TrimSpace(observation.Tool) != "terminal.run" || strings.TrimSpace(observation.FailureStage()) != "terminal_run" {
		return ""
	}
	summary := observation.FailureSummary()
	switch {
	case strings.Contains(summary, "ModuleNotFoundError: No module named 'pptx'"):
		return "Recovery route: do not probe or install python-pptx with system Python. Use the PPTX skill wrapper instead: create work under tmp/<deck-slug>, then run python3 /workspace/skills/pptx/scripts/skill_runtime.py python /workspace/skills/pptx/scripts/create_pptx.py deck.json output.pptx, or use /workspace/skills/simple-slides/scripts/build.sh after writing DESIGN.md and presentation.md."
	case strings.Contains(summary, "ModuleNotFoundError: No module named 'docx'"):
		return "Recovery route: do not probe or install python-docx with system Python. Use python3 /workspace/skills/docx/scripts/skill_runtime.py python /workspace/skills/docx/scripts/create_docx.py document.json output.docx from tmp/<document-slug>."
	case strings.Contains(summary, "ModuleNotFoundError: No module named 'openpyxl'"):
		return "Recovery route: do not probe or install openpyxl with system Python. Use python3 /workspace/skills/xlsx/scripts/skill_runtime.py python /workspace/skills/xlsx/scripts/create_xlsx.py workbook.json output.xlsx from tmp/<workbook-slug>."
	default:
		return ""
	}
}

type RecoveryRoute struct {
	ToolName     string
	UseWhen      string
	DoNotUseWhen string
}

func (recoveryRoute RecoveryRoute) Guidance() string {
	return "Recovery route: use " + recoveryRoute.ToolName + " only when " + recoveryRoute.UseWhen + "; do not use it when " + recoveryRoute.DoNotUseWhen + "."
}

func recoveryRoutesForObservation(observation turnObservation) []RecoveryRoute {
	if strings.TrimSpace(observation.Tool) != "memory.search" || observation.FailureCode() != FailureCodes.Unavailable.String() {
		return nil
	}
	return []RecoveryRoute{{
		ToolName:     "web.search",
		UseWhen:      "the missing information is required and public, current, or external",
		DoNotUseWhen: "private person or circle memory is required",
	}}
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

func (agentTurnRunner *AgentTurnRunner) saveToolObservation(ctx context.Context, taskRunID string, observationID string, toolName string, toolInputKey string, toolResult ToolResult, workspaceRootPath string, minimumModifiedAt time.Time) turnObservation {
	toolResult = normalizeToolFailureResult(toolName, toolResult)
	content := toolResult.ContentText()
	originalContent := content
	isError := toolResult.Failed()
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
			toolResult.Failure = &ToolFailure{Kind: FailureInvalidInput, Code: FailureCodes.InvalidInput.String(), Stage: "artifact_validation", UserSafeSummary: content}
			attachments = nil
			agentTurnRunner.appendEvent(taskRunID, "agent.artifact_attach_rejected", marshalEventBody(validityState))
		}
	}
	observation := turnObservation{
		ObservationID:   observationID,
		Action:          "call_tool",
		Tool:            toolName,
		Output:          toolResult.Output,
		Failure:         toolResult.Failure,
		Attachments:     attachments,
		RecoveryActions: append([]RecoveryAction{}, toolResult.RecoveryActions...),
	}
	observation.Output.Content = content
	observation.ImageRefs = toolResultImageRefs(observationID, attachments)
	observation.Summary = agentTurnRunner.buildToolResultSummary(ctx, taskRunID, toolName, originalContent, isError, attachments, artifactID, toolResult)
	observation.ToolInputKey = toolInputKey
	if observation.Failed() {
		observation.AttemptFingerprint = attemptFingerprint(toolInputKey, observation.FailureCode())
	}
	agentTurnRunner.appendEvent(taskRunID, "tool."+toolName+".result", marshalEventBody(observation))
	return observation
}

func normalizeToolFailureResult(toolName string, toolResult ToolResult) ToolResult {
	if !toolResult.Failed() {
		return toolResult
	}
	if toolResult.Failure.Kind == "" {
		toolResult.Failure.Kind = FailureUnknown
	}
	if strings.TrimSpace(toolResult.Failure.Code) == "" {
		toolResult.Failure.Code = FailureCodes.OperationFailed.String()
	} else {
		toolResult.Failure.Code = CanonicalFailureCode(FailureCode(toolResult.Failure.Code))
	}
	if strings.TrimSpace(toolResult.Failure.Stage) == "" {
		toolResult.Failure.Stage = firstNonEmptyString(toolName, "tool")
	}
	if strings.TrimSpace(toolResult.Failure.UserSafeSummary) == "" {
		toolResult.Failure.UserSafeSummary = strings.TrimSpace(toolResult.ContentText())
	}
	if strings.TrimSpace(toolResult.Output.Content) == "" {
		toolResult.Output.Content = toolResult.Failure.UserSafeSummary
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

func (agentTurnRunner *AgentTurnRunner) buildToolResultSummary(ctx context.Context, taskRunID string, toolName string, content string, isError bool, attachments []FileAttachment, artifactID string, toolResult ToolResult) string {
	observation := turnObservation{
		Tool:        toolName,
		Output:      ToolOutput{Content: content},
		Attachments: attachments,
	}
	if isError {
		observation.Failure = toolResult.Failure
	}
	summary := modelVisibleToolResultSummary(ctx, agentTurnRunner.languageModel, toolName, observation)
	if strings.TrimSpace(artifactID) != "" {
		summary = strings.TrimSpace(summary) + " Full result stored as artifact " + strings.TrimSpace(artifactID) + "."
	}
	return strings.TrimSpace(summary)
}

const rawToolResultInlineLimit = 2000
const semanticToolSummaryTarget = 1200

func modelVisibleToolResultSummary(ctx context.Context, languageModel llm.LanguageModelProvider, toolName string, observation turnObservation) string {
	content := strings.TrimSpace(observation.ContentText())
	if content == "" {
		return summarizeObservationContent(observation)
	}
	if shouldUseSanitizedToolPresenter(toolName) {
		return sanitizedToolResultSummary(observation)
	}
	if len(content) <= rawToolResultInlineLimit {
		return content
	}
	if shouldSummarizeLongToolResult(toolName) && languageModel != nil {
		summary, errorValue := summarizeLongToolResult(ctx, languageModel, toolName, content)
		if errorValue == nil && strings.TrimSpace(summary) != "" {
			return strings.TrimSpace(summary)
		}
	}
	return deterministicLongToolResultSummary(content)
}

func shouldUseSanitizedToolPresenter(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "browser.snapshot", "browser.observe", "browser.screenshot", "file.pick", "file.attach", "terminal.run":
		return true
	default:
		return false
	}
}

func shouldSummarizeLongToolResult(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "web.fetch", "web.search", "memory.search", "conversation.history":
		return true
	default:
		return false
	}
}

func sanitizedToolResultSummary(observation turnObservation) string {
	switch strings.TrimSpace(observation.Tool) {
	case "browser.snapshot", "browser.observe":
		return summarizeBrowserSnapshot(observation.ContentText())
	case "browser.screenshot":
		if len(observation.Attachments) > 0 {
			return "Screenshot captured. Use the imageRefs for visual inspection."
		}
		return summarizeSafeJSONFields(observation.ContentText(), []string{"capturedAt", "contentType", "filename", "sizeBytes"})
	case "file.pick":
		return attachmentResultSummary("User selected file", observation.Attachments)
	case "file.attach":
		return attachmentResultSummary("File attached", observation.Attachments)
	case "terminal.run":
		if summary := summarizeTerminalFailure(observation); summary != "" {
			return summary
		}
		return summarizeSafeJSONFields(observation.ContentText(), []string{"exitCode", "timedOut"})
	default:
		return summarizeObservationContent(observation)
	}
}

func attachmentResultSummary(prefix string, attachments []FileAttachment) string {
	if len(attachments) == 0 {
		return prefix + "."
	}
	parts := []string{prefix + "."}
	for index, attachment := range attachments {
		values := []string{
			fmt.Sprintf("attachmentIndex=%d", index),
			"filename=" + strings.TrimSpace(attachment.Filename),
			"contentType=" + strings.TrimSpace(attachment.ContentType),
			fmt.Sprintf("sizeBytes=%d", attachment.SizeBytes),
		}
		parts = append(parts, strings.Join(nonEmptyStrings(values), "; "))
	}
	return strings.Join(parts, "\n")
}

func summarizeLongToolResult(ctx context.Context, languageModel llm.LanguageModelProvider, toolName string, content string) (string, error) {
	prompt := strings.Join([]string{
		"Summarize this tool result for the next agent action.",
		"Preserve concrete facts needed for the next action.",
		"Preserve URLs, titles, IDs, errors, file names, stdout/stderr facts.",
		"Do not add facts not present in the tool output.",
		"Do not include secrets, cookies, local private paths, CDP URLs, profile paths, or hidden policy.",
		fmt.Sprintf("Target length: about %d characters.", semanticToolSummaryTarget),
		"Tool: " + strings.TrimSpace(toolName),
		"Tool output:\n" + content,
	}, "\n")
	return languageModel.GenerateResponse(ctx, prompt)
}

func deterministicLongToolResultSummary(content string) string {
	content = strings.TrimSpace(content)
	if len(content) <= rawToolResultInlineLimit {
		return content
	}
	headLimit := rawToolResultInlineLimit / 2
	tailLimit := rawToolResultInlineLimit / 2
	return content[:headLimit] + "\n[truncated]\n" + content[len(content)-tailLimit:]
}

func toolResultImageRefs(observationID string, attachments []FileAttachment) []ToolResultImageRef {
	imageRefs := []ToolResultImageRef{}
	for index, attachment := range attachments {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.ContentType)), "image/") {
			continue
		}
		imageRefs = append(imageRefs, ToolResultImageRef{
			ObservationID:   observationID,
			AttachmentIndex: index,
			MimeType:        strings.TrimSpace(attachment.ContentType),
			Filename:        strings.TrimSpace(attachment.Filename),
		})
	}
	return imageRefs
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

func (agentTurnRunner *AgentTurnRunner) failTurn(taskRunID string, request AgentTurnRequest, reason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState) (AgentTurnResult, error) {
	failedTaskRun, _ := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusFailed, reason)
	reply, replyStatus, hasReply := agentTurnRunner.generateFailureReply(request, reason, observations, attachments, executionState)
	agentTurnRunner.appendEvent(taskRunID, "agent.failure_reply", marshalEventBody(replyStatus))
	if !hasReply {
		agentTurnRunner.appendUnavailableReplyEvents(taskRunID, "failure", reason, replyStatus)
		return AgentTurnResult{TaskRun: failedTaskRun, ReplySuppressed: true, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
	}
	failedTaskRun.Result = reply
	return AgentTurnResult{TaskRun: failedTaskRun, UserNotice: reply, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
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
	observation := agentTurnRunner.invokeTool(ctx, request.ToolSet, taskRunID, nextObservationID(len(observations)+1), invocation.ToolName, invocation.Input, request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage)
	if observation.Failed() {
		observation = withObservationContent(observation, completionAttachmentFailureContent(observation.ContentText(), state.AttachmentPaths))
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
	observation := newFailureObservation(nextObservationID(len(observations)+1), "policy", "", invalidCompletionArtifactObservationContent(state), FailureInvalidInput, FailureCodes.InvalidInput, "completion_state")
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
	completionGateResult := validateCompletionGateForRequestWithRecoveryBudget(request, requirements, observations, criteria, actionDocument, agentTurnRunner.options.RecoveryBudget)
	agentTurnRunner.appendValidityReview(taskRunID, "completion_state", completionGateResult.ValidityState)
	if !completionGateResult.IsSatisfied {
		agentTurnRunner.appendEvent(taskRunID, "agent.completion_state_rejected", marshalEventBody(map[string]string{"reason": completionGateResult.Message}))
		observation := newFailureObservation(nextObservationID(len(observations)+1), "policy", "", completionGateResult.Message, FailureInvalidInput, FailureCodes.InvalidInput, "completion_state")
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
	if observation.Failed() || len(observation.Attachments) == 0 {
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
		Level:       level,
		Observation: newContentObservation(nextObservationID(observationIndex), "limit_pressure", "", message),
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

func (agentTurnRunner *AgentTurnRunner) finalizeOrStopForLimit(ctx context.Context, taskRunID string, request AgentTurnRequest, reason string, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion, executionState ExecutionState, usedIterationCount int, usedToolCallCount int) (AgentTurnResult, error) {
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
			if result, isFinalized := agentTurnRunner.finalizeSatisfiedTurn(ctx, taskRunID, request, requirements, observations, criteria, executionState); isFinalized {
				return result, nil
			}
		}
	}
	return agentTurnRunner.stopForLimit(taskRunID, request, reason, observations, attachments, executionState, usedIterationCount, usedToolCallCount)
}

func (agentTurnRunner *AgentTurnRunner) finalizeSatisfiedTurn(ctx context.Context, taskRunID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, criteria []qualityCriterion, executionState ExecutionState) (AgentTurnResult, bool) {
	actionDocument, errorValue := agentTurnRunner.finalizerAction(ctx, request, observations, executionState)
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_failed", marshalEventBody(map[string]string{"error": errorValue.Error()}))
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_action", marshalEventBody(actionDocument))
	if strings.TrimSpace(actionDocument.Action) != "final_reply" {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_rejected", marshalEventBody(map[string]string{"reason": "finalizer did not return final_reply"}))
		return AgentTurnResult{}, false
	}
	completionGateResult := validateCompletionGateForRequestWithRecoveryBudget(request, requirements, observations, criteria, actionDocument, agentTurnRunner.options.RecoveryBudget)
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

func (agentTurnRunner *AgentTurnRunner) finalizerAction(ctx context.Context, request AgentTurnRequest, observations []turnObservation, executionState ExecutionState) (turnActionDocument, error) {
	messages := agentTurnRunner.buildTurnMessages(request, observations, executionState)
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

func (agentTurnRunner *AgentTurnRunner) stopForLimit(taskRunID string, request AgentTurnRequest, reason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState, usedIterationCount int, usedToolCallCount int) (AgentTurnResult, error) {
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
	reply, replyStatus, hasReply := agentTurnRunner.generateLimitReachedReply(request, reason, observations, nil, executionState)
	agentTurnRunner.appendEvent(taskRunID, "agent.limit_reply", marshalEventBody(replyStatus))
	if !hasReply {
		agentTurnRunner.appendUnavailableReplyEvents(taskRunID, "limit", reason, replyStatus)
		return AgentTurnResult{TaskRun: blockedTaskRun, ReplySuppressed: true, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
	}
	blockedTaskRun.Result = reply
	return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: reply, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
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
	if errorValue := validateQualityReview(criteria, actionDocument.QualityReview, observations); errorValue != nil {
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
	return validateCompletionGateForRequestWithRecoveryBudget(request, requirements, observations, criteria, actionDocument, defaultRecoveryBudget())
}

func validateCompletionGateForRequestWithRecoveryBudget(request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, criteria []qualityCriterion, actionDocument turnActionDocument, recoveryBudget RecoveryBudget) completionGateResult {
	requirements = requirementsWithFailureDebtWaiver(requirements, observations, actionDocument)
	result := validateCompletionGate(requirements, observations, criteria, actionDocument)
	if !result.IsSatisfied {
		return result
	}
	failureDebtResult := failureDebtFinalizationGate(observations, actionDocument, recoveryBudget)
	if !failureDebtResult.IsSatisfied {
		result.IsSatisfied = false
		result.Message = failureDebtResult.Message
		result.Attachments = nil
		return result
	}
	result.ValidityState = buildAttachmentValidityState(request.WorkspaceRootPath, result.Attachments)
	if !result.ValidityState.Passed {
		result.IsSatisfied = false
		result.Message = validityFailureMessage(result.ValidityState)
		result.Attachments = nil
		return result
	}
	if request.OutcomeContract.ArtifactRequirement == ArtifactRequirementRequired && !hasDurableArtifactAttachment(result.Attachments) {
		result.IsSatisfied = false
		result.Message = "required artifact completion must cite file.attach evidence from artifacts/<slug>, /workspace/circles/<circleID>/..., or /workspace/shared/public/..."
		result.Attachments = nil
	}
	return result
}

func hasDurableArtifactAttachment(attachments []FileAttachment) bool {
	for _, attachment := range attachments {
		if isDurableArtifactDevicePath(attachment.DevicePath) {
			return true
		}
	}
	return false
}

func isDurableArtifactDevicePath(devicePath string) bool {
	normalizedPath := filepath.ToSlash(strings.TrimSpace(devicePath))
	return strings.HasPrefix(normalizedPath, "artifacts/") ||
		strings.HasPrefix(normalizedPath, "/workspace/private/people/") && strings.Contains(normalizedPath, "/artifacts/") ||
		strings.HasPrefix(normalizedPath, "/workspace/circles/") ||
		strings.HasPrefix(normalizedPath, "/workspace/shared/public/")
}

func requirementsWithFailureDebtWaiver(requirements []toolUseRequirement, observations []turnObservation, actionDocument turnActionDocument) []toolUseRequirement {
	if strings.TrimSpace(actionDocument.FailureResolution) != failureResolutionNoToolFallback {
		return requirements
	}
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return requirements
	}
	failedToolName := strings.TrimSpace(failureDebt.LatestFailure.Tool)
	if failedToolName == "" {
		return requirements
	}
	filteredRequirements := []toolUseRequirement{}
	for _, requirement := range requirements {
		if !requirement.RequiresAttachment && strings.TrimSpace(requirement.ToolName) == failedToolName {
			continue
		}
		filteredRequirements = append(filteredRequirements, requirement)
	}
	return filteredRequirements
}

func completionGateObservation(index int, message string) turnObservation {
	evidenceKind := evidenceMissingKind(message)
	if evidenceKind == "" {
		return newFailureObservation(nextObservationID(index), "policy", "", message, FailureInvalidInput, FailureCodes.InvalidInput, "completion_gate")
	}
	content := evidenceMissingGuidance(evidenceKind, message)
	observation := newFailureObservation(nextObservationID(index), "evidence_missing", "", message, FailureInvalidInput, FailureCodes.InvalidInput, evidenceKind)
	observation = withObservationContent(observation, content)
	observation.Summary = content
	observation.Failure.Retryable = true
	observation.Failure.SafeRetry = true
	return observation
}

func completionGateEventName(observation turnObservation) string {
	if observation.Action == "evidence_missing" {
		return "agent.evidence_missing"
	}
	return "agent.completion_required"
}

func evidenceMissingKind(message string) string {
	normalizedMessage := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(normalizedMessage, "requires successful observation"):
		return "required_tool_missing"
	case strings.Contains(normalizedMessage, "must include an attachment"):
		return "attachment_missing"
	case strings.Contains(normalizedMessage, "must include attachment suffix") || strings.Contains(normalizedMessage, "artifact"):
		return "attachment_invalid"
	case strings.Contains(normalizedMessage, "unknown or failed observation") || strings.Contains(normalizedMessage, "completionevidence"):
		return "evidence_reference_invalid"
	default:
		return ""
	}
}

func evidenceMissingGuidance(evidenceKind string, message string) string {
	switch evidenceKind {
	case "required_tool_missing":
		return "The final reply needs successful tool evidence before completion. Use the required tool if it has not run, or cite an existing successful observation. " + message
	case "attachment_missing":
		return "The final reply needs an attached artifact before completion. Find or create the artifact, then use file.attach before final_reply. " + message
	case "attachment_invalid":
		return "The final reply needs valid attachment evidence. Recheck the artifact path and required suffix, then attach a valid file. " + message
	case "evidence_reference_invalid":
		return "The final reply cited missing or failed evidence. Cite only existing successful observations, or run the missing tool first. " + message
	default:
		return message
	}
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
		if observation.Failed() || !requirementMatchesObservation(requirement, observation) {
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
		if observation.Failed() {
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
	Source                  string             `json:"source"`
	FirstInvalid            bool               `json:"firstInvalid"`
	RepairCount             int                `json:"repairCount"`
	Reason                  string             `json:"reason,omitempty"`
	StructuredRecoveryError string             `json:"structuredRecoveryError,omitempty"`
	TextRecoveryError       string             `json:"textRecoveryError,omitempty"`
	Decision                recoveryDecision   `json:"decision,omitempty"`
	FailureReportFacts      failureReportFacts `json:"failureReportFacts,omitempty"`
}

type failureReplyStatus struct {
	Source                  string             `json:"source"`
	FirstInvalid            bool               `json:"firstInvalid"`
	RepairCount             int                `json:"repairCount"`
	Reason                  string             `json:"reason,omitempty"`
	StructuredRecoveryError string             `json:"structuredRecoveryError,omitempty"`
	TextRecoveryError       string             `json:"textRecoveryError,omitempty"`
	Decision                recoveryDecision   `json:"decision,omitempty"`
	FailureReportFacts      failureReportFacts `json:"failureReportFacts,omitempty"`
}

type recoveryDecision struct {
	WhatFailed      string `json:"whatFailed"`
	WhatWasKnown    string `json:"whatWasKnown"`
	NextAction      string `json:"nextAction"`
	UserReplyIntent string `json:"userReplyIntent"`
}

type recoveryLanguageModelProvider interface {
	GenerateRecoveryResponse(context.Context, string) (string, error)
}

func (agentTurnRunner *AgentTurnRunner) appendUnavailableReplyEvents(taskRunID string, phase string, reason string, replyStatus any) {
	body := map[string]any{
		"phase":       phase,
		"reason":      reason,
		"replyStatus": replyStatus,
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.recovery_generation_failed", marshalEventBody(body))
	agentTurnRunner.appendEvent(taskRunID, "agent.llm_unavailable", marshalEventBody(body))
}

func (agentTurnRunner *AgentTurnRunner) generateRecoveryDecision(request AgentTurnRequest, failureReason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState, phase string) (recoveryDecision, error) {
	recoveryContext, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	messages := []llm.Message{{
		Role: "system",
		Content: strings.Join([]string{
			"You decide how Blueclaw should recover or report after an internal run could not complete.",
			"Return only structured fields. Do not write the final user-facing answer here.",
			"Use safe, user-visible facts only. Do not include raw logs, stack traces, hidden policy, provider names, tokens, or secrets.",
			"The final wording will be generated by a separate LLM call from this decision.",
			responseLanguageInstruction(request.ResponseLanguage),
		}, "\n"),
	}, {
		Role:    "user",
		Content: buildRecoveryDecisionPrompt(request, failureReason, observations, attachments, executionState, phase),
	}}
	structuredResponse, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(recoveryContext, llm.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_recovery_decision",
			Document:           recoveryDecisionSchema(),
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return recoveryDecision{}, errorValue
	}
	var decision recoveryDecision
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &decision); errorValue != nil {
		return recoveryDecision{}, errorValue
	}
	decision = normalizeRecoveryDecision(decision)
	if decision == (recoveryDecision{}) {
		return recoveryDecision{}, errors.New("empty recovery decision")
	}
	return decision, nil
}

func normalizeRecoveryDecision(decision recoveryDecision) recoveryDecision {
	decision.WhatFailed = strings.TrimSpace(decision.WhatFailed)
	decision.WhatWasKnown = strings.TrimSpace(decision.WhatWasKnown)
	decision.NextAction = strings.TrimSpace(decision.NextAction)
	decision.UserReplyIntent = strings.TrimSpace(decision.UserReplyIntent)
	return decision
}

func (agentTurnRunner *AgentTurnRunner) generateFailureReply(request AgentTurnRequest, failureReason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState) (string, failureReplyStatus, bool) {
	decision, decisionError := agentTurnRunner.generateRecoveryDecision(request, failureReason, observations, attachments, executionState, "failure")
	failureReportFacts := buildFailureReportFacts(observations, agentTurnRunner.options.RecoveryBudget)
	status := failureReplyStatus{Decision: decision, FailureReportFacts: failureReportFacts}
	if decisionError != nil {
		status.StructuredRecoveryError = decisionError.Error()
	}
	prompt := buildFailureReplyPrompt(request, failureReason, observations, attachments, executionState, decision)
	reply, errorValue := agentTurnRunner.generateRecoveryText(prompt)
	if errorValue == nil && reply != "" && !failureReplyIsInvalidForRequest(reply, request, failureReason, observations, attachments) {
		status.Source = "generated"
		return reply, status, true
	}
	if errorValue == nil && reply != "" {
		for repairCount := 1; repairCount <= 2; repairCount++ {
			repairedReply, repairError := agentTurnRunner.generateRecoveryText(buildFailureReplyRepairPrompt(prompt, reply, request, failureReason, observations, attachments, executionState, repairCount))
			if repairError != nil || repairedReply == "" {
				if degradedFailureReplyCanBeDelivered(reply, request, attachments) {
					status.Source = "generated_degraded"
					status.FirstInvalid = true
					status.RepairCount = repairCount
					status.Reason = "repair_failed_delivered_last_safe_reply"
					status.TextRecoveryError = firstNonEmptyString(errorString(repairError), "empty_repair")
					return reply, status, true
				}
				status.Source = "suppressed"
				status.FirstInvalid = true
				status.RepairCount = repairCount
				status.Reason = "repair_failed"
				status.TextRecoveryError = firstNonEmptyString(errorString(repairError), "empty_repair")
				return "", status, false
			}
			if !failureReplyIsInvalidForRequest(repairedReply, request, failureReason, observations, attachments) {
				status.Source = "generated_repair"
				status.FirstInvalid = true
				status.RepairCount = repairCount
				return repairedReply, status, true
			}
			reply = repairedReply
		}
		status.FirstInvalid = true
		status.RepairCount = 2
	}
	if degradedFailureReplyCanBeDelivered(reply, request, attachments) {
		status.Source = "generated_degraded"
		status.FirstInvalid = true
		status.RepairCount = 2
		status.Reason = "strict_failure_detail_missing"
		return reply, status, true
	}
	status.Source = "suppressed"
	status.Reason = firstNonEmptyString(status.Reason, "text_recovery_failed")
	status.TextRecoveryError = firstNonEmptyString(errorString(errorValue), "invalid_generated_reply")
	return "", status, false
}

func degradedFailureReplyCanBeDelivered(reply string, request AgentTurnRequest, attachments []FileAttachment) bool {
	if requiredArtifactWithoutAttachment(request, attachments) {
		return false
	}
	return strings.TrimSpace(reply) != "" && !failureReplyIsInvalid(reply, attachments)
}

func (agentTurnRunner *AgentTurnRunner) generateLimitReachedReply(request AgentTurnRequest, stopReason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState) (string, limitReplyStatus, bool) {
	decision, decisionError := agentTurnRunner.generateRecoveryDecision(request, stopReason, observations, attachments, executionState, "limit")
	status := limitReplyStatus{Decision: decision, FailureReportFacts: buildFailureReportFacts(observations, agentTurnRunner.options.RecoveryBudget)}
	if decisionError != nil {
		status.StructuredRecoveryError = decisionError.Error()
	}
	finalizationPrompt := buildLimitReachedPrompt(request, stopReason, observations, attachments, executionState, decision)
	reply, errorValue := agentTurnRunner.generateRecoveryText(finalizationPrompt)
	if errorValue != nil || reply == "" {
		status.Source = "suppressed"
		status.Reason = "text_recovery_failed"
		status.TextRecoveryError = firstNonEmptyString(errorString(errorValue), "empty_reply")
		return "", status, false
	}
	if limitReachedReplyIsInvalid(reply, request, attachments) {
		for repairCount := 1; repairCount <= 2; repairCount++ {
			repairedReply, repairError := agentTurnRunner.generateRecoveryText(buildLimitReachedRepairPrompt(finalizationPrompt, reply, request, attachments, repairCount))
			if repairError != nil || repairedReply == "" {
				status.Source = "suppressed"
				status.FirstInvalid = true
				status.RepairCount = repairCount
				status.Reason = "repair_failed"
				status.TextRecoveryError = firstNonEmptyString(errorString(repairError), "empty_repair")
				return "", status, false
			}
			if !limitReachedReplyIsInvalid(repairedReply, request, attachments) {
				status.Source = "generated_repair"
				status.FirstInvalid = true
				status.RepairCount = repairCount
				return repairedReply, status, true
			}
			reply = repairedReply
		}
		status.Source = "suppressed"
		status.FirstInvalid = true
		status.RepairCount = 2
		status.Reason = "invalid_repair"
		return "", status, false
	}
	status.Source = "generated"
	return reply, status, true
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

func buildRecoveryDecisionPrompt(request AgentTurnRequest, failureReason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState, phase string) string {
	sections := []string{
		"Phase: " + strings.TrimSpace(phase),
		"Original user request:\n" + strings.TrimSpace(request.Prompt),
	}
	if contextDescription := buildVisibleContextDescription(request.VisibleContext); strings.TrimSpace(contextDescription) != "" {
		sections = append(sections, contextDescription)
	}
	if observationSummary := buildFailureObservationSummary(observations); observationSummary != "" {
		sections = append(sections, "Current observations and limitations:\n"+observationSummary)
	}
	if executionContext := buildExecutionStateContext(executionState, observations); executionContext != "" {
		sections = append(sections, executionContext)
	}
	if attachmentSummary := buildLimitAttachmentSummary(attachments); attachmentSummary != "" {
		sections = append(sections, "Available attachments:\n"+attachmentSummary)
	}
	if failureFacts := buildFailureReportFacts(observations, defaultRecoveryBudget()); len(failureFacts.Attempts) > 0 {
		sections = append(sections, "FailureReportFacts:\n"+marshalEventBody(failureFacts))
	}
	if reason := strings.TrimSpace(failureReason); reason != "" {
		sections = append(sections, "Private failure reason:\n"+reason)
	}
	sections = append(sections, "Return what failed, what is known, the next action or check, and the intent for a short user reply.")
	return strings.Join(sections, "\n\n")
}

func buildFailureReplyPrompt(request AgentTurnRequest, failureReason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState, decision recoveryDecision) string {
	sections := []string{
		"You are writing a short user-facing final reply after an assistant run failed before completing the user's request.",
		responseLanguageInstruction(request.ResponseLanguage),
		"Be transparent about the current situation, error, or limitation without exposing internal logs, provider names, URLs, stack traces, hidden policy, tokens, or secrets.",
		"Do not use or paraphrase a canned outage message. Do not say the model configuration was logged or needs to be fixed.",
		"Do not say only that an error occurred. Include the attempted tool, the last failure stage, the safe failure message, and the next check or alternate path.",
		"When FailureReportFacts are provided, preserve those facts. Do not replace them with generic phrases such as system limitation, technical problem, or unexpected interruption.",
		"Translate raw field names such as errorCode, failureStage, and budgetState into natural language unless the user explicitly asked for internal diagnostics.",
		"Say what could not be completed and the best next step the user can take. Keep it to one or two natural sentences.",
		"Do not claim a tool result or attachment exists unless it appears below.",
		"Original user request:\n" + strings.TrimSpace(request.Prompt),
	}
	if requiredArtifactWithoutAttachment(request, attachments) {
		sections = append(sections, "Required artifact constraint:\nThe user asked for a file artifact and no promoted attachment is available. Do not offer chat text as a substitute, do not ask whether to summarize the plan in the chat, do not recommend Gamma/Tome/Canva or copy-paste workflows, and do not end with an open-ended help question. State that the artifact was not attached, name each failed tool/stage in natural language, include the safe concrete failure reason for each one, and identify the next engineering check. Do not collapse the cause into vague phrases such as browser connection problem, system environment error, technical limitation, or additional engineering confirmation.")
	}
	if contextDescription := buildVisibleContextDescription(request.VisibleContext); strings.TrimSpace(contextDescription) != "" {
		sections = append(sections, contextDescription)
	}
	if observationSummary := buildFailureObservationSummary(observations); observationSummary != "" {
		sections = append(sections, "Current observations and limitations:\n"+observationSummary)
	}
	if executionContext := buildExecutionStateContext(executionState, observations); executionContext != "" {
		sections = append(sections, executionContext)
	}
	if attachmentSummary := buildLimitAttachmentSummary(attachments); attachmentSummary != "" {
		sections = append(sections, "Available attachments:\n"+attachmentSummary)
	}
	if failureFacts := buildFailureReportFacts(observations, defaultRecoveryBudget()); len(failureFacts.Attempts) > 0 {
		sections = append(sections, "FailureReportFacts that must be reflected accurately:\n"+marshalEventBody(failureFacts))
	}
	if reason := strings.TrimSpace(failureReason); reason != "" {
		sections = append(sections, "Failure reason for your private planning only. Paraphrase it safely for the user:\n"+reason)
	}
	sections = append(sections, "Structured recovery decision:\n"+marshalEventBody(decision))
	return strings.Join(sections, "\n\n")
}

func buildFailureReplyRepairPrompt(originalPrompt string, rejectedReply string, request AgentTurnRequest, failureReason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState, repairCount int) string {
	sections := []string{
		originalPrompt,
		"Previous draft was rejected because it was too vague, offered an invalid substitute, exposed raw diagnostics, or missed concrete failure facts.",
		"Rewrite the final reply in natural user-facing language. Preserve the concrete safe failure facts from FailureReportFacts instead of summarizing them as a system or browser problem.",
	}
	if requiredArtifactWithoutAttachment(request, attachments) {
		sections = append(sections, "This was a required artifact request. Do not offer chat text as a substitute, do not ask an open-ended follow-up, do not recommend external slide or document tools, and do not expose raw identifiers such as errorCode or operation_failed. Say the artifact was not attached, name each failed tool or stage in natural language, include each safe failure reason, and identify the next engineering check.")
	}
	if failureFacts := buildFailureReportFacts(observations, defaultRecoveryBudget()); len(failureFacts.Attempts) > 0 {
		sections = append(sections, "FailureReportFacts that must be reflected accurately:\n"+marshalEventBody(failureFacts))
	}
	if executionContext := buildExecutionStateContext(executionState, observations); executionContext != "" {
		sections = append(sections, executionContext)
	}
	if reason := strings.TrimSpace(failureReason); reason != "" {
		sections = append(sections, "Private failure reason:\n"+reason)
	}
	if repairCount > 1 {
		sections = append(sections, "Use one or two Korean sentences. Be specific about the failed operation and the safe reason, but do not include internal paths or raw field names.")
	}
	sections = append(sections, "Rejected draft:\n"+strings.TrimSpace(rejectedReply))
	return strings.Join(sections, "\n\n")
}

func latestStructuredFailureObservation(observations []turnObservation) (turnObservation, bool) {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if !observation.Failed() {
			continue
		}
		if strings.TrimSpace(observation.FailureCode()) == "" && strings.TrimSpace(observation.FailureStage()) == "" {
			continue
		}
		return observation, true
	}
	return turnObservation{}, false
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
	if strings.Contains(reply, "예상치 못한 중단") {
		return true
	}
	if strings.Contains(reply, "정보를 정리하여 답변") {
		return true
	}
	if strings.Contains(reply, "창을 새로고침") {
		return true
	}
	if ValidateUserNoticeDelivery(reply) != nil {
		return true
	}
	return false
}

func failureReplyIsInvalidForRequest(reply string, request AgentTurnRequest, failureReason string, observations []turnObservation, attachments []FileAttachment) bool {
	if failureReplyIsInvalid(reply, attachments) {
		return true
	}
	if requiredArtifactWithoutAttachment(request, attachments) && requiredArtifactFailureReplyIsInvalid(reply) {
		return true
	}
	if requiredArtifactWithoutAttachment(request, attachments) {
		return false
	}
	return structuredFailureDetailsAreMissing(reply, failureReason, observations, attachments)
}

func requiredArtifactWithoutAttachment(request AgentTurnRequest, attachments []FileAttachment) bool {
	return requestRequiresDurableArtifact(request) && !hasDurableArtifactAttachment(attachments)
}

func requestRequiresDurableArtifact(request AgentTurnRequest) bool {
	if request.OutcomeContract.ArtifactRequirement == ArtifactRequirementRequired {
		return true
	}
	if len(request.RequiredAttachmentSuffixes) > 0 {
		return true
	}
	if requiredEvidenceContains(request.RequiredEvidenceTools, "file.attach") {
		return true
	}
	return evidenceAnyOfContainsTool(request.OutcomeContract.RequiredEvidenceAnyOf, "file.attach")
}

func requiredArtifactFailureReplyIsInvalid(reply string) bool {
	normalizedReply := strings.ToLower(strings.TrimSpace(reply))
	if normalizedReply == "" {
		return true
	}
	for _, fragment := range requiredArtifactFailureForbiddenFragments() {
		if strings.Contains(normalizedReply, fragment) {
			return true
		}
	}
	return false
}

func requiredArtifactFailureForbiddenFragments() []string {
	return []string{
		"errorcode",
		"failurestage",
		"budgetstate",
		"operation_failed",
		"externally-managed-environment",
		"modulenotfounderror",
		"browser connection problem",
		"system environment error",
		"technical limitation",
		"additional engineering",
		"브라우저 연결 문제",
		"시스템 환경 오류",
		"시스템 환경의 오류",
		"시스템 환경상",
		"기술적인 제약",
		"기술적으로 불가능",
		"추가적인 엔지니어링",
		"엔지니어링 확인",
		"텍스트로",
		"텍스트 형태",
		"정리해 드릴까요",
		"정리해드릴까요",
		"다른 방식으로 도움",
		"도움이 필요",
		"말씀해 주세요",
		"말씀해주세요",
		"gamma",
		"tome",
		"canva",
		"복사하여",
		"복사해서",
		"붙여넣",
		"온라인 도구",
		"외부 도구",
		"대신 드",
		"대신 제공",
	}
}

func structuredFailureDetailsAreMissing(reply string, failureReason string, observations []turnObservation, attachments []FileAttachment) bool {
	situation := recoverySituationFor(failureReason, observations, attachments, "failure")
	if situation == "browser_blocked" || situation == "attachment_unavailable" || situation == "limit" || situation == "model_unavailable" {
		return false
	}
	failure, isFound := latestStructuredFailureObservation(observations)
	if !isFound {
		return false
	}
	return !containsFailureDetail(reply, failure.FailureStage()) || !containsFailureDetail(reply, failure.FailureCode())
}

func containsFailureDetail(reply string, value string) bool {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return true
	}
	return strings.Contains(strings.ToLower(reply), strings.ToLower(trimmedValue))
}

func (agentTurnRunner *AgentTurnRunner) GenerateLimitReachedReply(request AgentTurnRequest, stopReason string, observations []turnObservation, attachments []FileAttachment) string {
	reply, _, _ := agentTurnRunner.generateLimitReachedReply(request, stopReason, observations, attachments, ExecutionState{})
	return reply
}

func buildLimitReachedPrompt(request AgentTurnRequest, stopReason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState, decision recoveryDecision) string {
	sections := []string{
		"You are writing a short user-facing final reply after a Blueclaw run reached its scope limit.",
		responseLanguageInstruction(request.ResponseLanguage),
		"Do not mention internal runtime jargon, counters, percentages, elapsed time, or exact limits.",
		"Say what was completed, what remains, and the best partial answer available from completed work.",
		"When FailureReportFacts are provided, preserve those facts. Do not replace them with generic phrases such as system limitation, technical problem, or unexpected interruption.",
		"Translate raw field names such as errorCode, failureStage, and budgetState into natural language unless the user explicitly asked for internal diagnostics.",
		"Do not claim a tool result or attachment exists unless it appears below.",
		"Original user request:\n" + strings.TrimSpace(request.Prompt),
	}
	if requiredArtifactWithoutAttachment(request, attachments) {
		sections = append(sections, "Required artifact constraint:\nThe user asked for a file artifact and no promoted attachment is available. Do not offer chat text as a substitute, do not ask whether to summarize the plan in the chat, do not recommend Gamma/Tome/Canva or copy-paste workflows, and do not end with an open-ended help question. State that the artifact was not attached, name each failed tool/stage in natural language, include the safe concrete failure reason for each one, and identify the next engineering check. Do not collapse the cause into vague phrases such as browser connection problem, system environment error, technical limitation, or additional engineering confirmation.")
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
	if executionContext := buildExecutionStateContext(executionState, observations); executionContext != "" {
		sections = append(sections, executionContext)
	}
	if attachmentSummary := buildLimitAttachmentSummary(attachments); attachmentSummary != "" {
		sections = append(sections, "Available attachments:\n"+attachmentSummary)
	}
	if requirementSummary := buildLimitRequirementSummary(request, observations); requirementSummary != "" {
		sections = append(sections, "Remaining completion requirements:\n"+requirementSummary)
	}
	if failureFacts := buildFailureReportFacts(observations, defaultRecoveryBudget()); len(failureFacts.Attempts) > 0 {
		sections = append(sections, "FailureReportFacts that must be reflected accurately:\n"+marshalEventBody(failureFacts))
	}
	if reason := strings.TrimSpace(stopReason); reason != "" {
		sections = append(sections, "Internal stop reason for your planning only: "+reason)
	}
	sections = append(sections, "Structured recovery decision:\n"+marshalEventBody(decision))
	return strings.Join(sections, "\n\n")
}

func buildLimitReachedRepairPrompt(originalPrompt string, rejectedReply string, request AgentTurnRequest, attachments []FileAttachment, repairCount int) string {
	sections := []string{
		originalPrompt,
		"Previous draft was rejected because it either exposed internal runtime details or claimed an attachment/tool result that is not available.",
		"Rewrite the final reply in natural user-facing language. Do not mention budgets, counters, exact limits, tool-call counts, iterations, seconds, or minutes. Do not use the exact canned sentence from any previous fallback.",
	}
	if requiredArtifactWithoutAttachment(request, attachments) {
		sections = append(sections, "This was a required artifact request. Do not offer chat text as a substitute, do not ask an open-ended follow-up, do not recommend external slide or document tools, and do not expose raw identifiers such as errorCode or operation_failed. Say the artifact was not attached, name the failing tool or stage in natural language, and identify the next engineering check.")
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

func limitReachedReplyIsInvalid(reply string, request AgentTurnRequest, attachments []FileAttachment) bool {
	if containsForbiddenLimitReplyFragment(reply) {
		return true
	}
	if requiredArtifactWithoutAttachment(request, attachments) && requiredArtifactFailureReplyIsInvalid(reply) {
		return true
	}
	if ValidateUserNoticeDelivery(reply) != nil {
		return true
	}
	return false
}

func buildLimitObservationSummary(observations []turnObservation) string {
	lines := []string{}
	for _, observation := range observations {
		if observation.Failed() {
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
		content := strings.TrimSpace(observation.ContentText())
		if observation.Failed() {
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
		if observation.Failed() {
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
