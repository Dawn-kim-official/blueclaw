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
	ContextWindowTokens  int
	RecoveryAttemptLimit int
	RecoveryBudget       RecoveryBudget
	TaskLevel            TaskLevel
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
	taskRunService        *task.TaskRunService
	taskStepService       *task.TaskStepService
	taskArtifactService   *task.TaskArtifactService
	languageModel         llm.LanguageModelProvider
	recoveryLanguageModel llm.LanguageModelProvider
	options               TurnOptions
}

type AgentTurnRequest struct {
	RequesterPersonID          string
	RequesterEmail             string
	RequesterName              string
	RequesterPlatformUserID    string
	SourceReference            string
	IsApprovalContinuation     bool
	IsRuntimeRestartResume     bool
	ExistingTaskRunID          string
	OriginReplyTargetID        string
	OriginIsThread             bool
	Platform                   string
	RequesterCallingName       string
	RequesterHandle            string
	RequesterCircles           []string
	Company                    CompanyContext
	ProfileName                string
	ConversationID             string
	ConversationType           string
	Prompt                     string
	InputParts                 []AgentPart
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
	PriorTask                  PriorTaskContext
	ScheduledRun               ScheduledRunContext
	ToolExposure               ToolExposureEvent
	QualityAcceptanceGuidance  []string
	PrecomputedTurnDecision    *TurnDecision
	IsPrecomputedDecisionExact bool
	SkipSkillSelection         bool
	AmbientDuty                AmbientDutyContext
	TaskShape                  TaskShape
	TaskLevel                  TaskLevel
	EstimatedMinutes           int
	TurnStartedAt              time.Time
	EffortStartedAt            time.Time
	CheckpointSender           AgentCheckpointSender
	StepBudgetContext          string
	ArtifactManifest           []ArtifactManifestEntry
}

type AgentTurnResult struct {
	TaskRun                task.TaskRun
	TurnRoute              TurnRoute
	ReactionEmojiName      string
	FinishMessage          string
	UserNotice             string
	FailureNotice          FailureNotice
	ReplySuppressed        bool
	ReplySuppressionReason string
	Attachments            []FileAttachment
	RecoveryActions        []RecoveryAction
	ToolNames              []string
}

type AgentCheckpointSender func(context.Context, AgentCheckpoint) error

type AgentCheckpoint struct {
	TaskRunID string
	Message   string
	ToolName  string
	Durable   bool
}

type turnActionDocument struct {
	Action                string                        `json:"action"`
	Message               string                        `json:"message"`
	ReplyParts            []AgentPart                   `json:"replyParts,omitempty"`
	CompletionSummary     string                        `json:"completionSummary,omitempty"`
	ToolName              string                        `json:"toolName"`
	ToolInput             json.RawMessage               `json:"toolInput"`
	ToolNames             []string                      `json:"toolNames"`
	SkillNames            []string                      `json:"skillNames"`
	RequestTools          []string                      `json:"requestTools"`
	RequestSkills         []string                      `json:"requestSkills"`
	Reason                string                        `json:"reason"`
	Reply                 string                        `json:"reply"`
	FailureResolution     string                        `json:"failureResolution"`
	GoalStatus            string                        `json:"goalStatus"`
	GoalSatisfied         *bool                         `json:"goalSatisfied"`
	HasRemainingWork      bool                          `json:"hasRemainingWork"`
	CompletionEvidenceIDs []string                      `json:"completionEvidenceIDs"`
	CompletionEvidence    []completionEvidenceReference `json:"completionEvidence"`
	QualityCriteria       []string                      `json:"qualityCriteria"`
	QualityReview         []qualityReviewItem           `json:"qualityReview"`
	RemainingWork         string                        `json:"remainingWork"`
	UsedFailureFacts      failureReportFacts            `json:"usedFailureFacts"`
	ExecutionStateUpdate  ExecutionState                `json:"executionStateUpdate"`
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
	PolicyCode           string               `json:"policyCode,omitempty"`
	RelatedResultIDs     []string             `json:"relatedResultIDs,omitempty"`
	RelatedPaths         []string             `json:"relatedPaths,omitempty"`
	RecoveryPacket       *RecoveryPacket      `json:"recoveryPacket,omitempty"`
	Attachments          []FileAttachment     `json:"attachments,omitempty"`
	RecoveryActions      []RecoveryAction     `json:"recoveryActions,omitempty"`
	DurationMS           int64                `json:"durationMs"`
}

type toolCallActionOutcome struct {
	Result       AgentTurnResult
	ShouldReturn bool
	WasHandled   bool
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

func NewAgentTurnRunner(taskRunService *task.TaskRunService, taskStepService *task.TaskStepService, taskArtifactService *task.TaskArtifactService, languageModel llm.LanguageModelProvider, options TurnOptions) *AgentTurnRunner {
	return NewAgentTurnRunnerWithRecoveryModel(taskRunService, taskStepService, taskArtifactService, languageModel, languageModel, options)
}

func NewAgentTurnRunnerWithRecoveryModel(taskRunService *task.TaskRunService, taskStepService *task.TaskStepService, taskArtifactService *task.TaskArtifactService, languageModel llm.LanguageModelProvider, recoveryLanguageModel llm.LanguageModelProvider, options TurnOptions) *AgentTurnRunner {
	if taskArtifactService == nil {
		taskArtifactService = task.NewTaskArtifactService()
	}
	if recoveryLanguageModel == nil {
		recoveryLanguageModel = languageModel
	}
	return &AgentTurnRunner{
		taskRunService:        taskRunService,
		taskStepService:       taskStepService,
		taskArtifactService:   taskArtifactService,
		languageModel:         languageModel,
		recoveryLanguageModel: recoveryLanguageModel,
		options:               normalizeTurnOptions(options),
	}
}

func normalizeTurnOptions(options TurnOptions) TurnOptions {
	taskLevelProfile := TaskLevelProfileForLevel(options.TaskLevel)
	if options.TaskLevel == "" {
		options.TaskLevel = taskLevelProfile.TaskLevel
	}
	if options.MaxIterationCount <= 0 {
		options.MaxIterationCount = taskLevelProfile.MaxIterationCount
	}
	if options.MaxToolCallCount < 0 {
		options.MaxToolCallCount = 0
	}
	if options.MaxToolCallCount == 0 {
		options.MaxToolCallCount = taskLevelProfile.MaxToolCallCount
	}
	if options.MaxElapsedSecond <= 0 {
		options.MaxElapsedSecond = int(taskLevelProfile.Duration.Seconds())
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

	maxTaskLevelProfile := TaskLevelProfileForLevel(TaskLevelMax)
	turnContext, cancel := context.WithTimeout(ctx, maxTaskLevelProfile.Duration)
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
	if request.EffortStartedAt.IsZero() {
		request.EffortStartedAt = time.Now()
	}
	request.ResponseLanguage = ResolveResponseLanguage(request.ResponseLanguage)
	request, _ = applyToolRequest(request, requestToolsArguments{
		ToolNames:  request.PinnedToolNames,
		SkillNames: request.PinnedSkillNames,
	})

	taskRun := agentTurnRunner.taskRunForRequest(request)
	agentTurnRunner.appendTaskSourceEvent(taskRun.TaskRunID, request.SourceReference)
	observeRecord := func(record llmCallRecord) {
		record.ModelTier = string(agentTurnRunner.options.TaskLevel)
		agentTurnRunner.appendEvent(taskRun.TaskRunID, "llm.call", marshalEventBody(record))
	}
	agentTurnRunner.languageModel = observeLanguageModel(agentTurnRunner.languageModel, observeRecord)
	if agentTurnRunner.recoveryLanguageModel == nil {
		agentTurnRunner.recoveryLanguageModel = agentTurnRunner.languageModel
	} else {
		agentTurnRunner.recoveryLanguageModel = observeLanguageModel(agentTurnRunner.recoveryLanguageModel, observeRecord)
	}
	runningTaskRun, errorValue := agentTurnRunner.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue != nil {
		return agentTurnRunner.failLaunchStep(context.Background(), taskRun, request, "start_attempt", errorValue), nil
	}
	taskRun = runningTaskRun
	taskContext, taskCancel := context.WithCancel(turnContext)
	unregisterTaskCancel := agentTurnRunner.taskRunService.RegisterTaskRunCancel(taskRun.TaskRunID, taskCancel)
	defer unregisterTaskCancel()
	defer taskCancel()
	agentTurnRunner.appendInstructionEvent(taskRun.TaskRunID, request)

	state, errorValue := agentTaskStateForTurn(request, agentTurnRunner.options, taskRun, agentTurnRunner.taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if errorValue != nil {
		return agentTurnRunner.failLaunchStep(context.Background(), taskRun, request, "restore_state", errorValue), nil
	}
	toolUseRequirements := state.Requirements
	successfulToolCalls := map[string]turnObservation{}
	if request.IsApprovalContinuation {
		var approvedResult AgentTurnResult
		var shouldReturn bool
		request, approvedResult, shouldReturn = agentTurnRunner.executeApprovedHeldCall(taskContext, taskRun.TaskRunID, request, &state, successfulToolCalls)
		if shouldReturn {
			return approvedResult, nil
		}
	}
	limitPressureWarnings := map[string]bool{}
	progressTracker := newActionProgressTracker(state.Observations)
	appliedSteerEventIDs := appliedSteerEventIDsFromTaskEvents(agentTurnRunner.taskRunService.ListTaskEvent(taskRun.TaskRunID))
	noProgressStopEvaluation := func() (actionProgressEvaluation, bool) {
		progressEvaluation := progressTracker.evaluate(state.Observations)
		if progressEvaluation.HasProgress {
			return progressEvaluation, false
		}
		return progressEvaluation, progressEvaluation.shouldStop()
	}
	stopForNoProgress := func(stepID string) (AgentTurnResult, bool) {
		progressEvaluation, shouldStop := noProgressStopEvaluation()
		if !shouldStop {
			return AgentTurnResult{}, false
		}
		recoveryAllowance := evaluateRecoveryAllowance(state.Observations, agentTurnRunner.options.RecoveryBudget)
		if agentTurnRunner.continueStalledRecoveryIfAllowed(taskRun.TaskRunID, &state, &progressTracker, recoveryAllowance) {
			return AgentTurnResult{}, false
		}
		if agentTurnRunner.steerStalledTurnTowardNextTool(taskRun.TaskRunID, &state, &progressTracker) {
			return AgentTurnResult{}, false
		}
		if agentTurnRunner.steerStalledTurnTowardExit(taskRun.TaskRunID, &state, &progressTracker) {
			return AgentTurnResult{}, false
		}
		reason := "stopped after repeated model actions without workspace, tool, artifact, attachment, or new failure progress, including after stall guidance"
		if agentTurnRunner.shouldPauseForStalledRecovery(taskRun.TaskRunID, state.Observations) {
			if result, isPaused := agentTurnRunner.pauseTurnForStall(taskRun.TaskRunID, stepID, request, reason, progressEvaluation, recoveryAllowance, state); isPaused {
				return result, true
			}
		}
		result, isBlocked := agentTurnRunner.blockTurnForStall(taskRun.TaskRunID, stepID, request, reason, progressEvaluation, recoveryAllowance, state)
		return result, isBlocked
	}
	stopForRequestToolsNoProgress := func(stepID string) (AgentTurnResult, bool) {
		progressEvaluation, shouldStop := noProgressStopEvaluation()
		if !shouldStop {
			return AgentTurnResult{}, false
		}
		recoveryAllowance := evaluateRecoveryAllowance(state.Observations, agentTurnRunner.options.RecoveryBudget)
		reason := "stopped after 3 consecutive model actions without workspace, tool, artifact, attachment, or new failure progress"
		if _, hasFailureDebt := activeFailureDebt(state.Observations); hasFailureDebt && !recoveryAllowance.CanRecover {
			result := agentTurnRunner.runTerminalNoToolsStep(taskContext, taskRun.TaskRunID, stepID, request, &state, "recovery_tool_budget_exhausted")
			return result, true
		}
		if agentTurnRunner.continueStalledRecoveryIfAllowed(taskRun.TaskRunID, &state, &progressTracker, recoveryAllowance) {
			return AgentTurnResult{}, false
		}
		if agentTurnRunner.steerStalledTurnTowardNextTool(taskRun.TaskRunID, &state, &progressTracker) {
			return AgentTurnResult{}, false
		}
		if agentTurnRunner.steerStalledTurnTowardExit(taskRun.TaskRunID, &state, &progressTracker) {
			return AgentTurnResult{}, false
		}
		result, isBlocked := agentTurnRunner.blockTurnForStall(taskRun.TaskRunID, stepID, request, reason, progressEvaluation, recoveryAllowance, state)
		return result, isBlocked
	}
	for iteration := 1; ; iteration++ {
		if iteration > agentTurnRunner.options.MaxIterationCount {
			result, shouldContinue, errorValue := agentTurnRunner.finalizeEscalateOrStopForLimit(taskContext, taskRun.TaskRunID, request, "max_iterations", toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria, state.ExecutionState, iteration-1, state.ToolCallCount)
			if errorValue != nil || !shouldContinue {
				return result, errorValue
			}
		}
		if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRun.TaskRunID, state.Attachments); isCancelled {
			return cancelledResult, nil
		}
		if agentTurnRunner.currentEffortElapsed(request.EffortStartedAt) {
			result, shouldContinue, errorValue := agentTurnRunner.finalizeEscalateOrStopForLimit(taskContext, taskRun.TaskRunID, request, "max_elapsed", toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria, state.ExecutionState, iteration-1, state.ToolCallCount)
			if errorValue != nil || !shouldContinue {
				return result, errorValue
			}
		}
		state.Observations = agentTurnRunner.applyPendingSteeringEvents(taskRun.TaskRunID, state.Observations, appliedSteerEventIDs)
		state.IterationCount = iteration - 1
		if warning := agentTurnRunner.nextLimitPressureWarning(iteration-1, state.ToolCallCount, agentTurnRunner.turnElapsed(request.EffortStartedAt), len(state.Observations)+1, limitPressureWarnings); warning != nil {
			state.Observations = append(state.Observations, warning.Observation)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.limit_pressure", marshalEventBody(warning.EventBody))
			limitPressureWarnings[warning.Level] = true
		}
		stepID := fmt.Sprintf("%s:turn-%03d", taskRun.TaskRunID, iteration)
		agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusRunning, "agent turn iteration", "")

		transition := agentTurnRunner.applyCompletionState(taskContext, taskRun.TaskRunID, stepID, request, toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria, state.LastModelMessage)
		state.Observations = transition.Observations
		state.Attachments = transition.Attachments
		if transition.IsCompleted {
			return transition.Result, nil
		}
		if transition.DidTransition {
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "completion_state "+string(transition.Action), "")
			continue
		}
		iterationRequest := agentTurnRunner.requestForStep(taskContext, request, state)
		agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.step_working_set", marshalEventBody(map[string]any{
			"step":     iteration,
			"exposure": iterationRequest.ToolExposure,
		}))
		agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.tool_palette.built", marshalEventBody(map[string]any{
			"step":     iteration,
			"exposure": iterationRequest.ToolExposure,
		}))
		actionContext, cancelAction := agentTurnRunner.currentEffortContext(taskContext, request.EffortStartedAt)
		actionDocument, actionError := agentTurnRunner.nextAction(actionContext, taskRun.TaskRunID, iterationRequest, toolUseRequirements, state.Observations, state.ExecutionState, state.ContextSummary, len(state.QualityCriteria) == 0)
		cancelAction()
		if actionError != nil {
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "agent turn iteration", actionError.Error())
			if errors.Is(actionError, context.Canceled) {
				return agentTurnRunner.cancelledTaskResultOrCurrent(taskRun.TaskRunID, state.Attachments), nil
			}
			if errors.Is(actionError, context.DeadlineExceeded) {
				finalization := agentTurnRunner.finalizeLimitIfPossible(taskContext, taskRun.TaskRunID, request, toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria, state.ExecutionState)
				if finalization.IsCompleted {
					return finalization.Result, nil
				}
				return agentTurnRunner.stopForLimit(taskRun.TaskRunID, request, "max_elapsed", finalization.Observations, finalization.Attachments, state.ExecutionState, iteration-1, state.ToolCallCount)
			}
			return agentTurnRunner.finalizeIfSatisfiedOrFail(taskContext, taskRun.TaskRunID, request, "llm action failed: "+actionError.Error(), toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria, state.ExecutionState)
		}

		if message := strings.TrimSpace(actionDocument.Message); message != "" {
			state.LastModelMessage = message
		}

		if !executionStateIsEmpty(actionDocument.ExecutionStateUpdate) {
			state.ExecutionState = normalizeExecutionState(actionDocument.ExecutionStateUpdate)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.execution_state", marshalEventBody(state.ExecutionState))
		}
		if strings.TrimSpace(actionDocument.Action) == "continue" {
			request = agentTurnRunner.applyInlineToolRequest(taskRun.TaskRunID, request, &state, actionDocument)
		}
		agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.action", marshalEventBody(actionDocument))
		switch strings.TrimSpace(actionDocument.Action) {
		case "tool.request":
			requestArguments := requestToolsArguments{
				ToolNames:  append([]string{}, actionDocument.ToolNames...),
				SkillNames: append([]string{}, actionDocument.SkillNames...),
				Reason:     actionDocument.Reason,
			}
			if request.AmbientDuty.IsMatch {
				observation := ambientFixedPaletteObservation(len(state.Observations)+1, requestArguments)
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.tool_palette.fixed", marshalEventBody(map[string]any{
					"request": requestArguments,
					"source":  "ambient_capture",
				}))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "tool.request", observation.ContentText())
				continue
			}
			nextRequest, selectionResult := applyToolRequest(request, requestArguments)
			addedNothing := toolRequestAddedNothing(request, nextRequest, selectionResult)
			request = nextRequest
			state.Request = nextRequest
			var observation turnObservation
			if addedNothing {
				observation = redundantToolSelectionObservation(len(state.Observations)+1, requestArguments, selectionResult)
			} else {
				observation = toolRequestObservation(len(state.Observations)+1, requestArguments, selectionResult)
			}
			state.Observations = append(state.Observations, observation)
			eventName := "agent.tool_palette.applied"
			if addedNothing {
				eventName = "agent.tool_palette.redundant"
			} else if toolRequestResultFailed(selectionResult) {
				eventName = "agent.tool_palette.failed"
			}
			agentTurnRunner.appendEvent(taskRun.TaskRunID, eventName, marshalEventBody(map[string]any{
				"request": requestArguments,
				"result":  selectionResult,
				"source":  "model_action",
			}))
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "tool.request", observation.ContentText())
			if result, shouldStop := stopForRequestToolsNoProgress(stepID); shouldStop {
				return result, nil
			}
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
		case "finish":
			completionGateResult := agentTurnRunner.validateCompletionGateForRequestWithExpectedResults(taskContext, taskRun.TaskRunID, request, toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria, actionDocument)
			agentTurnRunner.appendValidityReview(taskRun.TaskRunID, "finish", completionGateResult.ValidityState)
			if !completionGateResult.IsSatisfied {
				observation := completionGateObservation(len(state.Observations)+1, completionGateResult)
				observation = withCompletionGateRecoveryPacket(observation, completionGateResult)
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
			reply := finishActionMessage(actionDocument)
			if reply == "" {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusFailed, "finish", "empty finish message")
				return agentTurnRunner.finalizeIfSatisfiedOrFail(taskContext, taskRun.TaskRunID, request, "empty finish message", toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria, state.ExecutionState)
			}
			reply = agentTurnRunner.prepareFinishMessageForPlatform(request, reply, completionGateResult.Attachments)
			if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRun.TaskRunID, state.Attachments); isCancelled {
				return cancelledResult, nil
			}
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "finish", reply)
			completedTaskRun, completeError := agentTurnRunner.taskRunService.CompleteTaskRun(taskRun.TaskRunID, reply)
			if completeError != nil {
				return agentTurnRunner.cancelledTaskResultOrCurrent(taskRun.TaskRunID, state.Attachments), nil
			}
			return AgentTurnResult{TaskRun: completedTaskRun, FinishMessage: reply, Attachments: completionGateResult.Attachments, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, nil
		case "continue":
			outcome := agentTurnRunner.handleToolCallAction(taskContext, taskRun.TaskRunID, stepID, iteration, iterationRequest, toolUseRequirements, &state, actionDocument, successfulToolCalls, stopForNoProgress)
			if outcome.ShouldReturn {
				return outcome.Result, nil
			}
			if outcome.WasHandled {
				continue
			}
		case "fail":
			if recoverableResult, shouldContinue := recoverableWorkflowFailResult(request, state.Observations); shouldContinue {
				observation := completionGateObservation(len(state.Observations)+1, recoverableResult)
				observation = withCompletionGateRecoveryPacket(observation, recoverableResult)
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.recoverable_fail_rejected", marshalEventBody(observation))
				agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.completion_required", marshalEventBody(observation))
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "recoverable_fail_rejected", observation.ContentText())
				if result, shouldStop := stopForNoProgress(stepID); shouldStop {
					return result, nil
				}
				continue
			}
			if _, hasFailureDebt := activeFailureDebt(state.Observations); hasFailureDebt {
				facts := buildFailureReportFacts(state.Observations, agentTurnRunner.options.RecoveryBudget)
				failureReportResult := validateFailureReportAction(actionDocument, facts)
				if !failureReportResult.IsSatisfied {
					observation := completionGateObservation(len(state.Observations)+1, failureReportResult)
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
			return agentTurnRunner.finalizeIfSatisfiedOrFail(taskContext, taskRun.TaskRunID, request, reason, toolUseRequirements, state.Observations, state.Attachments, state.QualityCriteria, state.ExecutionState)
		default:
			observation := newFailureObservation(nextObservationID(len(state.Observations)+1), "invalid_action", "", "unknown action: "+actionDocument.Action, FailureInvalidInput, FailureCodes.InvalidInput, "action_parse")
			state.Observations = append(state.Observations, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, task.TaskStatusCompleted, "invalid_action", observation.ContentText())
			if result, shouldStop := stopForNoProgress(stepID); shouldStop {
				return result, nil
			}
		}
	}
}

func (agentTurnRunner *AgentTurnRunner) failLaunchStep(ctx context.Context, taskRun task.TaskRun, request AgentTurnRequest, stepName string, errorValue error) AgentTurnResult {
	reason := errorString(errorValue)
	agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.launch_step.error", marshalEventBody(map[string]string{
		"phase":    "launch",
		"stepName": strings.TrimSpace(stepName),
		"error":    reason,
	}))
	failedTaskRun, failError := agentTurnRunner.taskRunService.FailTaskRun(taskRun.TaskRunID, reason)
	if failError != nil {
		taskRun.Status = task.TaskStatusFailed
		taskRun.FailureReason = firstNonEmptyString(reason, failError.Error())
		failedTaskRun = taskRun
	}
	failureNotice, noticeStatus := (FailureNoticeGenerator{LanguageModel: agentTurnRunner.recoveryLanguageModel}).Generate(ctx, FailureReport{
		Phase:              "launch",
		StepName:           stepName,
		StopReason:         reason,
		SafeFailureSummary: reason,
		RawError:           reason,
		OriginalRequest:    request.Prompt,
		ResponseLanguage:   request.ResponseLanguage,
		DiagnosticEventID:  diagnosticEventID(request, taskRun.TaskRunID, "launch"),
	})
	agentTurnRunner.appendEvent(taskRun.TaskRunID, "agent.failure_reply", marshalEventBody(noticeStatus))
	failedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, failedTaskRun, failureNotice.SendableMessage())
	return AgentTurnResult{TaskRun: failedTaskRun, UserNotice: failedTaskRun.Result, FailureNotice: failureNotice, ToolNames: toolNamesForEvent(request.ToolSet)}
}

func (agentTurnRunner *AgentTurnRunner) handleToolCallAction(ctx context.Context, taskRunID string, stepID string, iteration int, request AgentTurnRequest, requirements []toolUseRequirement, state *agentTaskState, actionDocument turnActionDocument, successfulToolCalls map[string]turnObservation, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	if outcome := agentTurnRunner.rejectMalformedToolCall(taskRunID, stepID, request, state, actionDocument, stopForNoProgress); outcome.WasHandled {
		return outcome
	}
	if duplicateObservation, isDuplicate := repeatedSuccessfulCompletionCandidate(state, actionDocument, successfulToolCalls); isDuplicate {
		finalizationRequirements, canFinalize := duplicateSuccessFinalizationRequirements(request.ToolSet, requirements, state.Observations, actionDocument)
		if canFinalize {
			if result, isFinalized := agentTurnRunner.finalizeSatisfiedTurn(ctx, taskRunID, request, finalizationRequirements, state.Observations, state.QualityCriteria, state.ExecutionState, duplicateObservation.Tool); isFinalized {
				return toolCallActionOutcome{Result: result, ShouldReturn: true, WasHandled: true}
			}
		}
	}
	if outcome := agentTurnRunner.rejectRepeatedToolCall(taskRunID, stepID, state, actionDocument, successfulToolCalls, stopForNoProgress); outcome.WasHandled {
		return outcome
	}
	recoveryStep, outcome := agentTurnRunner.prepareRecoveryAttempt(ctx, taskRunID, stepID, request, state, actionDocument, stopForNoProgress)
	if outcome.WasHandled {
		return outcome
	}
	if outcome := agentTurnRunner.rejectUnavailableToolCall(taskRunID, stepID, request, state, actionDocument, stopForNoProgress); outcome.WasHandled {
		return outcome
	}
	if !request.IsApprovalContinuation && toolCallRequiresRuntimeApproval(request.ToolSet, actionDocument) {
		return agentTurnRunner.requestHeldCallApproval(ctx, taskRunID, stepID, request, state, actionDocument)
	}
	state.ToolCallCount++
	if state.ToolCallCount > maxToolCallCountWithRecovery(agentTurnRunner.options, state.Observations) {
		result, shouldContinue, errorValue := agentTurnRunner.finalizeEscalateOrStopForLimit(ctx, taskRunID, request, "max_tool_calls", requirements, state.Observations, state.Attachments, state.QualityCriteria, state.ExecutionState, iteration, state.ToolCallCount)
		if errorValue != nil || !shouldContinue {
			agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusBlocked, "limit stop", "max_tool_calls")
			return toolCallActionOutcome{Result: result, ShouldReturn: true, WasHandled: true}
		}
	}
	state.Observations = agentTurnRunner.sendCheckpointMessage(ctx, taskRunID, request, actionDocument, state.Observations)
	observationID := nextObservationID(len(state.Observations) + 1)
	observation := agentTurnRunner.invokeTool(ctx, request.ToolSet, taskRunID, observationID, actionDocument.ToolName, actionDocument.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, actionDocument.Message)
	observation = agentTurnRunner.resolveCalendarDuplicate(ctx, taskRunID, observationID, request, actionDocument, observation)
	if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRunID, state.Attachments); isCancelled {
		return toolCallActionOutcome{Result: cancelledResult, ShouldReturn: true, WasHandled: true}
	}
	if isApprovalRequiredObservation(observation) {
		return agentTurnRunner.requestHeldCallApproval(ctx, taskRunID, stepID, request, state, actionDocument)
	}
	agentTurnRunner.recordToolObservation(taskRunID, state, actionDocument, successfulToolCalls, observation, recoveryStep)
	if pausedResult, isPaused := agentTurnRunner.pausedTaskResult(taskRunID, observation, state.Attachments); isPaused {
		agentTurnRunner.saveStep(taskRunID, stepID, pausedResult.TaskRun.Status, "continue "+actionDocument.ToolName, observation.ContentText())
		return toolCallActionOutcome{Result: pausedResult, ShouldReturn: true, WasHandled: true}
	}
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "continue "+actionDocument.ToolName, observation.ContentText())
	if !observation.Failed() && isInspectionProgressTool(observation.Tool) && hasPendingObservedSuggestedNextTool(state.Observations) {
		result, shouldStop := stopForNoProgress(stepID)
		return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	return toolCallActionOutcome{WasHandled: true}
}

func (agentTurnRunner *AgentTurnRunner) applyPendingSteeringEvents(taskRunID string, observations []turnObservation, appliedEventIDs map[string]bool) []turnObservation {
	for _, taskEvent := range agentTurnRunner.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name != "task.steer.requested" || appliedEventIDs[taskEvent.TaskEventID] {
			continue
		}
		var document struct {
			MessageID   string `json:"messageID"`
			Instruction string `json:"instruction"`
			Reason      string `json:"reason"`
		}
		if json.Unmarshal([]byte(taskEvent.Body), &document) != nil {
			continue
		}
		instruction := strings.TrimSpace(document.Instruction)
		if instruction == "" {
			continue
		}
		observation := newContentObservation(nextObservationID(len(observations)+1), "steer", "", marshalEventBody(map[string]string{
			"instruction": instruction,
			"reason":      strings.TrimSpace(document.Reason),
			"messageID":   strings.TrimSpace(document.MessageID),
		}))
		observation.Summary = "User steering instruction: " + instruction
		observations = append(observations, observation)
		appliedEventIDs[taskEvent.TaskEventID] = true
		agentTurnRunner.appendEvent(taskRunID, "task.steer.applied", marshalEventBody(map[string]string{
			"sourceEventID": taskEvent.TaskEventID,
			"observationID": observation.ObservationID,
			"messageID":     strings.TrimSpace(document.MessageID),
		}))
	}
	return observations
}

func appliedSteerEventIDsFromTaskEvents(taskEvents []task.TaskEvent) map[string]bool {
	eventIDs := map[string]bool{}
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != "task.steer.applied" {
			continue
		}
		var document struct {
			SourceEventID string `json:"sourceEventID"`
		}
		if json.Unmarshal([]byte(taskEvent.Body), &document) == nil && strings.TrimSpace(document.SourceEventID) != "" {
			eventIDs[strings.TrimSpace(document.SourceEventID)] = true
		}
	}
	return eventIDs
}

func (agentTurnRunner *AgentTurnRunner) taskRunForRequest(request AgentTurnRequest) task.TaskRun {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := agentTurnRunner.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun
		}
	}
	return agentTurnRunner.taskRunService.CreateTaskRunWithOrigin(request.RequesterPersonID, task.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}

func (agentTurnRunner *AgentTurnRunner) appendTaskSourceEvent(taskRunID string, sourceReference string) {
	trimmedSourceReference := strings.TrimSpace(sourceReference)
	if trimmedSourceReference == "" {
		return
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.task_source", marshalEventBody(map[string]string{
		"sourceReference": trimmedSourceReference,
	}))
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

func (agentTurnRunner *AgentTurnRunner) sendCheckpointMessage(ctx context.Context, taskRunID string, request AgentTurnRequest, actionDocument turnActionDocument, observations []turnObservation) []turnObservation {
	message := strings.TrimSpace(actionDocument.Message)
	if message == "" || agentTurnRunner == nil {
		return observations
	}
	if taskLevelWantsSingleFinalReply(request.TaskLevel) {
		agentTurnRunner.appendEvent(taskRunID, "agent.checkpoint.skipped", marshalEventBody(map[string]any{
			"toolName": actionDocument.ToolName,
			"reason":   "task_level_xlow",
		}))
		return observations
	}
	if !checkpointMessageAllowed(message, observations) {
		agentTurnRunner.appendEvent(taskRunID, "agent.checkpoint.skipped", marshalEventBody(map[string]any{
			"toolName": actionDocument.ToolName,
			"reason":   "rate_limited_or_duplicate",
		}))
		return observations
	}
	observation := newContentObservation(nextObservationID(len(observations)+1), "checkpoint", "", marshalEventBody(map[string]any{
		"message":  message,
		"toolName": actionDocument.ToolName,
	}))
	observation.Summary = message
	if request.CheckpointSender != nil {
		errorValue := request.CheckpointSender(ctx, AgentCheckpoint{
			TaskRunID: taskRunID,
			Message:   message,
			ToolName:  strings.TrimSpace(actionDocument.ToolName),
		})
		if errorValue != nil {
			observation.Output.Content = marshalEventBody(map[string]any{
				"message":  message,
				"toolName": actionDocument.ToolName,
				"status":   "failed",
				"error":    errorValue.Error(),
			})
			agentTurnRunner.appendEvent(taskRunID, "agent.checkpoint.failed", marshalEventBody(map[string]any{
				"toolName": actionDocument.ToolName,
				"error":    errorValue.Error(),
			}))
			return append(observations, observation)
		}
		observation.Output.Content = marshalEventBody(map[string]any{
			"message":  message,
			"toolName": actionDocument.ToolName,
			"status":   "sent",
		})
		agentTurnRunner.appendEvent(taskRunID, "agent.checkpoint.sent", marshalEventBody(map[string]any{
			"toolName": actionDocument.ToolName,
			"message":  message,
		}))
		return append(observations, observation)
	}
	observation.Output.Content = marshalEventBody(map[string]any{
		"message":  message,
		"toolName": actionDocument.ToolName,
		"status":   "skipped",
		"reason":   "missing_sender",
	})
	agentTurnRunner.appendEvent(taskRunID, "agent.checkpoint.skipped", marshalEventBody(map[string]any{
		"toolName": actionDocument.ToolName,
		"reason":   "missing_sender",
	}))
	return append(observations, observation)
}

func checkpointMessageAllowed(message string, observations []turnObservation) bool {
	normalizedMessage := normalizeCheckpointMessage(message)
	count := 0
	for _, observation := range observations {
		if observation.Action != "checkpoint" {
			continue
		}
		count++
		if normalizeCheckpointMessage(checkpointObservationMessage(observation)) == normalizedMessage {
			return false
		}
	}
	return count < 3
}

func normalizeCheckpointMessage(message string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(message))), " ")
}

func checkpointObservationMessage(observation turnObservation) string {
	var document struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(observation.ContentText()), &document) == nil {
		return document.Message
	}
	return observation.Summary
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

func finishActionMessage(actionDocument turnActionDocument) string {
	return firstNonEmptyString(replyPartsText(actionDocument.ReplyParts), actionDocument.Message, actionDocument.Reply)
}

func replyPartsText(parts []AgentPart) string {
	textParts := []string{}
	for _, part := range parts {
		if strings.TrimSpace(part.Type) != AgentPartTypeText || strings.TrimSpace(part.Text) == "" {
			continue
		}
		textParts = append(textParts, strings.TrimSpace(part.Text))
	}
	return strings.TrimSpace(strings.Join(textParts, "\n\n"))
}

func approvalObservationUserFacingMessage(observation turnObservation) string {
	var document struct {
		UserFacingMessage string `json:"userFacingMessage"`
		Message           string `json:"message"`
		Question          string `json:"question"`
	}
	if json.Unmarshal([]byte(observation.ContentText()), &document) != nil {
		return ""
	}
	return firstNonEmptyString(document.UserFacingMessage, document.Message, document.Question)
}

func (agentTurnRunner *AgentTurnRunner) nextAction(ctx context.Context, taskRunID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, executionState ExecutionState, contextSummary TaskContextSummary, allowQualityCriteria bool) (turnActionDocument, error) {
	state := agentTaskState{
		Request:         request,
		Options:         agentTurnRunner.options,
		Observations:    append([]turnObservation{}, observations...),
		ExecutionState:  executionState,
		ContextSummary:  contextSummary,
		QualityCriteria: qualityCriteriaForActionRequest(allowQualityCriteria),
		Requirements:    append([]toolUseRequirement{}, requirements...),
	}
	state.Observations = agentTurnRunner.promptVisibleObservationsForAction(ctx, taskRunID, state)
	actionDocument, errorValue := DecideAgentAction(ctx, agentTurnRunner.languageModel, state)
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	return actionDocument, nil
}

func (agentTurnRunner *AgentTurnRunner) requestForStep(_ context.Context, request AgentTurnRequest, state agentTaskState) AgentTurnRequest {
	plannedRequest := requestWithStepWorkingSetTools(request, state.Observations)
	filteredToolSet, exposureEvent := toolSetForAgentTurnWithExposure(
		plannedRequest.ToolSet,
		instructionBundleFromTurnRequest(plannedRequest),
		agentRequestFromTurnRequest(plannedRequest),
		ExecutionPlan{},
		false,
		plannedRequest.OutcomeContract,
		ToolExposureEvent{SelectionSource: "deterministic_palette"},
		state.Observations,
	)
	iterationRequest := plannedRequest
	iterationRequest.ToolSet = filteredToolSet
	iterationRequest.ToolExposure = exposureEvent
	iterationRequest.StepBudgetContext = agentTurnRunner.stepBudgetContext(state)
	return iterationRequest
}

func (agentTurnRunner *AgentTurnRunner) stepBudgetContext(state agentTaskState) string {
	maxToolCallCount := maxToolCallCountWithRecovery(agentTurnRunner.options, state.Observations)
	remainingToolCallCount := maxToolCallCount - state.ToolCallCount
	if remainingToolCallCount < 0 {
		remainingToolCallCount = 0
	}
	maxIterationCount := agentTurnRunner.options.MaxIterationCount
	remainingIterationCount := maxIterationCount - state.IterationCount
	if remainingIterationCount < 0 {
		remainingIterationCount = 0
	}
	return strings.Join([]string{
		"Step budget:",
		fmt.Sprintf("Tool calls: %d/%d used, %d remaining.", state.ToolCallCount, maxToolCallCount, remainingToolCallCount),
		fmt.Sprintf("Steps: %d/%d used, %d remaining.", state.IterationCount, maxIterationCount, remainingIterationCount),
		"Use the shortest path to the expected result. Avoid extra inspection when the next edit, build, publish, file delivery, or final action is already clear.",
		"Keep at least two tool calls for delivery when the requested link or file has not been delivered yet.",
	}, "\n")
}

func requestWithStepWorkingSetTools(request AgentTurnRequest, observations []turnObservation) AgentTurnRequest {
	request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, pendingFileDeliveryToolNames(request, observations)...)
	request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, workflowToolNamesForTurnRequest(request)...)
	request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, observedSuggestedNextToolNames(observations)...)
	return request
}

func (agentTurnRunner *AgentTurnRunner) applyInlineToolRequest(taskRunID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) AgentTurnRequest {
	requestArguments := requestToolsArguments{
		ToolNames:  append([]string{}, actionDocument.RequestTools...),
		SkillNames: append([]string{}, actionDocument.RequestSkills...),
	}
	if len(appendUniqueStrings(requestArguments.ToolNames)) == 0 && len(appendUniqueStrings(requestArguments.SkillNames)) == 0 {
		return request
	}
	nextRequest, selectionResult := applyToolRequest(request, requestArguments)
	state.Request = nextRequest
	agentTurnRunner.appendEvent(taskRunID, "agent.tool_palette.applied", marshalEventBody(map[string]any{
		"request": requestArguments,
		"result":  selectionResult,
		"source":  "continue_inline",
	}))
	return nextRequest
}

func pendingFileDeliveryToolNames(request AgentTurnRequest, observations []turnObservation) []string {
	if !expectedResultRequiresFileAttachment(request.OutcomeContract) || hasSuccessfulArtifactDeliveryObservation(observations) {
		return nil
	}
	return availableFileDeliveryToolNames(request)
}

func availableFileDeliveryToolNames(request AgentTurnRequest) []string {
	toolNames := []string{TerminalRunToolName, FileDeliverToolName, SkillSearchToolName}
	if request.ToolSet == nil {
		return toolNames
	}
	return registeredToolNamesOnly(request.ToolSet, toolNames)
}

func hasSuccessfulArtifactDeliveryObservation(observations []turnObservation) bool {
	for _, observation := range observations {
		if !observation.Failed() && IsArtifactDeliveryTool(observation.Tool) {
			return true
		}
	}
	return false
}

func selectedSkillFileDeliveryToolNames(request AgentTurnRequest) []string {
	selectedSkillNames := selectedSkillNameSet(request.SkillDecisions)
	toolNames := []string{}
	for _, skillInstruction := range request.AvailableSkills {
		if !selectedSkillNames[skillInstruction.Name] {
			continue
		}
		if !skillSupportsFileDelivery(skillInstruction) {
			continue
		}
		toolNames = appendUniqueStrings(toolNames, SkillToolNames(skillInstruction)...)
		toolNames = appendUniqueStrings(toolNames, skillInstruction.Completion.RequiredEvidenceTools...)
	}
	return toolNames
}

func hasSuccessfulToolObservation(observations []turnObservation, toolName string) bool {
	for _, observation := range observations {
		if strings.TrimSpace(observation.Tool) == toolName && observation.Failure == nil {
			return true
		}
	}
	return false
}

func instructionBundleFromTurnRequest(request AgentTurnRequest) InstructionBundle {
	return InstructionBundle{
		Prompt:         request.InstructionPrompt,
		Skills:         append([]SkillInstruction{}, request.AvailableSkills...),
		Sources:        append([]InstructionSource{}, request.InstructionSources...),
		SkillDecisions: append([]SkillSelectionDecision{}, request.SkillDecisions...),
		RetrievalMode:  request.SkillRetrievalMode,
		IndexStatus:    request.SkillIndexStatus,
		CandidateCount: request.SkillCandidateCount,
		SkillQueries:   append([]string{}, request.SkillQueries...),
	}
}

func agentRequestFromTurnRequest(request AgentTurnRequest) AgentRequest {
	return AgentRequest{
		RequesterPersonID:      request.RequesterPersonID,
		RequesterName:          request.RequesterName,
		RequesterCallingName:   request.RequesterCallingName,
		RequesterHandle:        request.RequesterHandle,
		RequesterCircles:       append([]string{}, request.RequesterCircles...),
		IsApprovalContinuation: request.IsApprovalContinuation,
		ExistingTaskRunID:      request.ExistingTaskRunID,
		ProfileName:            request.ProfileName,
		ConversationID:         request.ConversationID,
		ConversationType:       request.ConversationType,
		Prompt:                 request.Prompt,
		ResponseLanguage:       request.ResponseLanguage,
		VisibleContext:         request.VisibleContext,
		MemoryFacts:            append([]memory.MemoryFact{}, request.MemoryFacts...),
		ToolSet:                request.ToolSet,
		PinnedToolNames:        append([]string{}, request.PinnedToolNames...),
		PinnedSkillNames:       append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:      request.WorkspaceRootPath,
		ActivePaths:            append([]string{}, request.ActivePaths...),
		InstructionPrompt:      request.InstructionPrompt,
		ActiveGoal:             request.ActiveGoal,
		TaskShape:              request.TaskShape,
		TurnStartedAt:          request.TurnStartedAt,
		CheckpointSender:       request.CheckpointSender,
	}
}

func (agentTurnRunner *AgentTurnRunner) buildTurnMessages(request AgentTurnRequest, observations []turnObservation, executionState ExecutionState) []llm.Message {
	if len(request.ArtifactManifest) == 0 {
		request.ArtifactManifest = buildConversationArtifactManifest(request, agentTurnRunner.taskRunService, agentTurnRunner.taskArtifactService)
	}
	return (PromptAssembler{}).BuildTurnMessages(
		request,
		observations,
		buildAgentSystemInstruction(request),
		buildAgentToolDescription(request.ToolSet),
		executionState,
	)
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

const maxStallRecoveryDirectivesPerEpisode = 4

func (agentTurnRunner *AgentTurnRunner) continueStalledRecoveryIfAllowed(taskRunID string, state *agentTaskState, tracker *actionProgressTracker, allowance recoveryAllowance) bool {
	if !allowance.CanRecover {
		return false
	}
	failureDebt, hasFailureDebt := activeFailureDebt(state.Observations)
	if !hasFailureDebt {
		return false
	}
	if !stalledOnRedundantInspection(state.Observations) {
		return false
	}
	if tracker.stallRecoveryDirectiveCount >= maxStallRecoveryDirectivesPerEpisode {
		return false
	}
	directive := stalledRecoveryDirectiveObservation(nextObservationID(len(state.Observations)+1), failureDebt)
	state.Observations = append(state.Observations, directive)
	agentTurnRunner.appendEvent(taskRunID, "agent.stall_recovery_directive", marshalEventBody(directive))
	tracker.noteStallRecoveryDirective(state.Observations)
	return true
}

func (agentTurnRunner *AgentTurnRunner) steerStalledTurnTowardNextTool(taskRunID string, state *agentTaskState, tracker *actionProgressTracker) bool {
	if tracker.stallRecoveryDirectiveCount >= maxStallRecoveryDirectivesPerEpisode {
		return false
	}
	suggestion, isFound := latestObservedSuggestedNextTool(state.Observations)
	if !isFound {
		return false
	}
	if state.Request.ToolSet != nil && !state.Request.ToolSet.IsAllowed(suggestion.ToolName) {
		return false
	}
	directive := suggestedNextToolDirectiveObservation(nextObservationID(len(state.Observations)+1), suggestion)
	state.Observations = append(state.Observations, directive)
	agentTurnRunner.appendEvent(taskRunID, "agent.suggested_next_tool_directive", marshalEventBody(directive))
	tracker.noteStallRecoveryDirective(state.Observations)
	return true
}

func (agentTurnRunner *AgentTurnRunner) steerStalledTurnTowardExit(taskRunID string, state *agentTaskState, tracker *actionProgressTracker) bool {
	if tracker.stallRecoveryDirectiveCount >= maxStallRecoveryDirectivesPerEpisode {
		return false
	}
	directive := stalledExitDirectiveObservation(nextObservationID(len(state.Observations)+1), state.Observations)
	state.Observations = append(state.Observations, directive)
	agentTurnRunner.appendEvent(taskRunID, "agent.stall_exit_directive", marshalEventBody(directive))
	tracker.noteStallRecoveryDirective(state.Observations)
	return true
}

func suggestedNextToolDirectiveObservation(observationID string, suggestion observedSuggestedNextTool) turnObservation {
	message := suggestion.Reason + " Call " + suggestion.ToolName + " now before repeating inspection, asking the user, or finishing."
	observation := newContentObservation(observationID, "policy", "", marshalEventBody(map[string]string{
		"directive":           message,
		"suggestedTool":       suggestion.ToolName,
		"sourceTool":          suggestion.SourceTool,
		"sourceObservationID": suggestion.ObservationID,
	}))
	observation.Summary = message
	return observation
}

func stalledExitDirectiveObservation(observationID string, observations []turnObservation) turnObservation {
	failedTool := ""
	if failureDebt, hasFailureDebt := activeFailureDebt(observations); hasFailureDebt {
		failedTool = strings.TrimSpace(failureDebt.LatestFailure.Tool)
	}
	message := "You are repeating actions without making progress. Stop retrying the same thing and stop re-emitting a finish that keeps getting rejected. Take one of two exits now: either take a genuinely different action that changes workspace, tool, or evidence state; or, if you cannot obtain what you need because a tool keeps failing or the required evidence is unavailable, end immediately with fail and failureResolution=failure_report, giving the user a short honest explanation of what you could not do. Do not loop and do not ask the user how to proceed."
	missingOperationName := latestMissingRequiredEvidenceOperationName(observations)
	if missingOperationName != "" {
		message = "You have not yet called capability.invoke with operation=\"" + missingOperationName + "\". Call it now with the appropriate input before attempting to finish again. If it is genuinely not needed for this request, end with fail and failureResolution=failure_report, explaining why in the user reply. Do not re-emit finish again without this evidence."
	}
	observation := newContentObservation(observationID, "policy", "", marshalEventBody(map[string]string{
		"directive":                message,
		"failedTool":               failedTool,
		"missingEvidenceOperation": missingOperationName,
	}))
	observation.Summary = message
	return observation
}

func latestMissingRequiredEvidenceOperationName(observations []turnObservation) string {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Action != "evidence_missing" || observation.RecoveryPacket == nil {
			continue
		}
		if len(observation.RecoveryPacket.AllowedTools) > 0 {
			return strings.TrimSpace(observation.RecoveryPacket.AllowedTools[0])
		}
	}
	return ""
}

func stalledOnRedundantInspection(observations []turnObservation) bool {
	if len(observations) == 0 {
		return false
	}
	lastObservation := observations[len(observations)-1]
	if lastObservation.Action != "policy" || lastObservation.Tool != "file.read" {
		return false
	}
	document := map[string]any{}
	if json.Unmarshal(lastObservation.Output.Data, &document) != nil {
		return false
	}
	return stringValue(document["cacheStatus"]) == "hit"
}

func stalledRecoveryDirectiveObservation(observationID string, failureDebt FailureDebt) turnObservation {
	failedTool := strings.TrimSpace(failureDebt.LatestFailure.Tool)
	message := "You are repeating actions without progress while " + failedTool + " is still failing. You already have the information you need. Make one concrete fix now by editing the offending file with file.edit, then re-run " + failedTool + ". Do not read the same content again and do not ask the user how to proceed."
	observation := newContentObservation(observationID, "policy", "", marshalEventBody(map[string]string{
		"directive":           message,
		"failedTool":          failedTool,
		"failedObservationID": failureDebt.LatestFailure.ObservationID,
	}))
	observation.Summary = message
	return observation
}

func (agentTurnRunner *AgentTurnRunner) shouldPauseForStalledRecovery(taskRunID string, observations []turnObservation) bool {
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return false
	}
	if failureClassForObservation(failureDebt.LatestFailure) != failureClassUserInput {
		return false
	}
	for _, taskEvent := range agentTurnRunner.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == "agent.no_progress_loop_paused" {
			return false
		}
	}
	return true
}

func (agentTurnRunner *AgentTurnRunner) pauseTurnForStall(taskRunID string, stepID string, request AgentTurnRequest, reason string, progressEvaluation actionProgressEvaluation, allowance recoveryAllowance, state agentTaskState) (AgentTurnResult, bool) {
	notice, replyStatus, hasReply := agentTurnRunner.generateStallPauseNotice(taskRunID, request, reason, state.Observations, state.Attachments, state.ExecutionState)
	agentTurnRunner.appendEvent(taskRunID, "agent.stall_pause_reply", marshalEventBody(replyStatus))
	if !hasReply {
		return AgentTurnResult{}, false
	}
	pausedTaskRun, errorValue := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingUserInput, reason)
	if errorValue != nil {
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.no_progress_loop_paused", marshalEventBody(map[string]any{
		"reason":             reason,
		"progressEvaluation": progressEvaluation,
		"recoveryAllowance":  allowance,
	}))
	agentTurnRunner.appendEvent(taskRunID, "agent.goal.waiting_user_input", marshalEventBody(stalledWaitingGoal(taskRunID, request)))
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusWaitingUserInput, "no_progress_loop_paused", reason)
	reply := notice.SendableMessage()
	pausedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, pausedTaskRun, reply)
	return AgentTurnResult{TaskRun: pausedTaskRun, UserNotice: reply, FailureNotice: notice, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, true
}

func (agentTurnRunner *AgentTurnRunner) blockTurnForStall(taskRunID string, stepID string, request AgentTurnRequest, reason string, progressEvaluation actionProgressEvaluation, allowance recoveryAllowance, state agentTaskState) (AgentTurnResult, bool) {
	notice, replyStatus, hasReply := agentTurnRunner.generateStallPauseNotice(taskRunID, request, reason, state.Observations, state.Attachments, state.ExecutionState)
	agentTurnRunner.appendEvent(taskRunID, "agent.stall_blocked_reply", marshalEventBody(replyStatus))
	blockedTaskRun, errorValue := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusBlocked, reason)
	if errorValue != nil {
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.no_progress_loop_stopped", marshalEventBody(map[string]any{
		"reason":             reason,
		"progressEvaluation": progressEvaluation,
		"recoveryAllowance":  allowance,
	}))
	agentTurnRunner.appendEvent(taskRunID, "agent.goal.blocked", marshalEventBody(blockedGoal(taskRunID, request, reason)))
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusBlocked, "no_progress_loop_stopped", reason)
	if !hasReply {
		agentTurnRunner.appendUnavailableReplyEvents(taskRunID, "stall", reason, replyStatus)
		fallbackReply := deterministicFailureFallbackReply(request.ResponseLanguage)
		blockedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, blockedTaskRun, fallbackReply)
		return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: fallbackReply, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, true
	}
	reply := notice.SendableMessage()
	blockedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, blockedTaskRun, reply)
	return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: reply, FailureNotice: notice, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, true
}

func stalledWaitingGoal(taskRunID string, request AgentTurnRequest) ActiveGoal {
	waitingGoal := request.ActiveGoal
	waitingGoal.GoalID = firstNonEmptyString(waitingGoal.GoalID, taskRunID)
	waitingGoal.TaskRunID = firstNonEmptyString(waitingGoal.TaskRunID, taskRunID)
	waitingGoal.OriginalInstruction = firstNonEmptyString(waitingGoal.OriginalInstruction, request.Prompt)
	waitingGoal.Status = ActiveGoalStatusWaitingUserInput
	return waitingGoal
}

func blockedGoal(taskRunID string, request AgentTurnRequest, reason string) ActiveGoal {
	blockedGoal := request.ActiveGoal
	blockedGoal.GoalID = firstNonEmptyString(blockedGoal.GoalID, taskRunID)
	blockedGoal.TaskRunID = firstNonEmptyString(blockedGoal.TaskRunID, taskRunID)
	blockedGoal.OriginalInstruction = firstNonEmptyString(blockedGoal.OriginalInstruction, request.Prompt)
	blockedGoal.CurrentObjective = firstNonEmptyString(blockedGoal.CurrentObjective, reason)
	blockedGoal.Status = ActiveGoalStatusBlocked
	return blockedGoal
}

// A run that already produced the required completion evidence has met its goal;
// a later transient error, empty finish, or exhausted recovery must not erase that
// success. Finalize the satisfied turn before declaring failure, so a delivered
// artifact never ends as a failed task.
func (agentTurnRunner *AgentTurnRunner) finalizeIfSatisfiedOrFail(ctx context.Context, taskRunID string, request AgentTurnRequest, reason string, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion, executionState ExecutionState) (AgentTurnResult, error) {
	finalization := agentTurnRunner.finalizeLimitIfPossible(ctx, taskRunID, request, requirements, observations, attachments, criteria, executionState)
	if finalization.IsCompleted {
		return finalization.Result, nil
	}
	return agentTurnRunner.failTurn(taskRunID, request, reason, finalization.Observations, finalization.Attachments, executionState)
}

func (agentTurnRunner *AgentTurnRunner) failTurn(taskRunID string, request AgentTurnRequest, reason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState) (AgentTurnResult, error) {
	failedTaskRun, _ := agentTurnRunner.taskRunService.FailTaskRun(taskRunID, reason)
	failureNotice, replyStatus, hasReply := agentTurnRunner.generateFailureNotice(taskRunID, request, reason, observations, attachments, executionState)
	agentTurnRunner.appendEvent(taskRunID, "agent.failure_reply", marshalEventBody(replyStatus))
	if !hasReply {
		agentTurnRunner.appendUnavailableReplyEvents(taskRunID, "failure", reason, replyStatus)
		fallbackReply := deterministicFailureFallbackReply(request.ResponseLanguage)
		failedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, failedTaskRun, fallbackReply)
		return AgentTurnResult{TaskRun: failedTaskRun, UserNotice: fallbackReply, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
	}
	reply := failureNotice.SendableMessage()
	failedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, failedTaskRun, reply)
	return AgentTurnResult{TaskRun: failedTaskRun, UserNotice: reply, FailureNotice: failureNotice, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
}

func deterministicFailureFallbackReply(responseLanguage string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(responseLanguage)), "en") {
		return "Sorry — a temporary system error interrupted this task before it could finish. Please try again in a moment."
	}
	return "죄송합니다. 일시적인 시스템 오류로 요청을 끝까지 처리하지 못했습니다. 잠시 후 다시 시도해 주세요."
}

type limitPressureWarning struct {
	Level       string
	Observation turnObservation
	EventBody   map[string]any
}

func (agentTurnRunner *AgentTurnRunner) nextLimitPressureWarning(usedIterationCount int, usedToolCallCount int, elapsed time.Duration, observationIndex int, sentWarnings map[string]bool) *limitPressureWarning {
	if sentWarnings["finalize"] {
		return nil
	}
	if agentTurnRunner.options.MaxIterationCount < 10 && agentTurnRunner.options.MaxToolCallCount < 5 {
		return nil
	}
	level := agentTurnRunner.limitPressureLevel(usedIterationCount, usedToolCallCount, elapsed)
	if level == "" || sentWarnings[level] {
		return nil
	}
	maxToolCallCount := maxToolCallCountWithRecovery(agentTurnRunner.options, nil)
	maxElapsed := time.Duration(agentTurnRunner.options.MaxElapsedSecond) * time.Second
	message := limitPressureMessage(level, usedToolCallCount, maxToolCallCount, usedIterationCount, agentTurnRunner.options.MaxIterationCount, elapsed, maxElapsed)
	return &limitPressureWarning{
		Level:       level,
		Observation: newContentObservation(nextObservationID(observationIndex), "limit_pressure", "", message),
		EventBody: map[string]any{
			"level":              level,
			"taskLevel":          agentTurnRunner.options.TaskLevel,
			"usedIterationCount": usedIterationCount,
			"usedToolCallCount":  usedToolCallCount,
			"maxIterationCount":  agentTurnRunner.options.MaxIterationCount,
			"maxToolCallCount":   maxToolCallCount,
			"elapsedSeconds":     int(elapsed.Seconds()),
			"maxElapsedSeconds":  agentTurnRunner.options.MaxElapsedSecond,
		},
	}
}

func (agentTurnRunner *AgentTurnRunner) limitPressureLevel(usedIterationCount int, usedToolCallCount int, elapsed time.Duration) string {
	maxElapsed := time.Duration(agentTurnRunner.options.MaxElapsedSecond) * time.Second
	if limitUsageReached(usedIterationCount, agentTurnRunner.options.MaxIterationCount, 90) || limitUsageReached(usedToolCallCount, agentTurnRunner.options.MaxToolCallCount, 90) || elapsedUsageReached(elapsed, maxElapsed, 90) {
		return "finalize"
	}
	if limitUsageReached(usedIterationCount, agentTurnRunner.options.MaxIterationCount, 75) || limitUsageReached(usedToolCallCount, agentTurnRunner.options.MaxToolCallCount, 75) || elapsedUsageReached(elapsed, maxElapsed, 75) {
		return "consolidate"
	}
	if limitUsageReached(usedIterationCount, agentTurnRunner.options.MaxIterationCount, 50) || limitUsageReached(usedToolCallCount, agentTurnRunner.options.MaxToolCallCount, 50) || elapsedUsageReached(elapsed, maxElapsed, 50) {
		return "budget"
	}
	return ""
}

func elapsedUsageReached(elapsed time.Duration, maxElapsed time.Duration, thresholdPercent int) bool {
	if maxElapsed <= 0 || elapsed <= 0 {
		return false
	}
	return elapsed*100 >= maxElapsed*time.Duration(thresholdPercent)
}

func roundedSeconds(duration time.Duration) string {
	return duration.Round(time.Second).String()
}

func limitUsageReached(usedCount int, maxCount int, thresholdPercent int) bool {
	if maxCount <= 0 || usedCount <= 0 {
		return false
	}
	return usedCount*100 >= maxCount*thresholdPercent
}

func limitPressureMessage(level string, usedToolCallCount int, maxToolCallCount int, usedIterationCount int, maxIterationCount int, elapsed time.Duration, maxElapsed time.Duration) string {
	budgetLine := fmt.Sprintf("Budget status: %d/%d tool calls used and %d/%d steps used.", usedToolCallCount, maxToolCallCount, usedIterationCount, maxIterationCount)
	if maxElapsed > 0 {
		budgetLine += fmt.Sprintf(" Time: %s/%s elapsed.", roundedSeconds(elapsed), roundedSeconds(maxElapsed))
	}
	if level == "finalize" {
		return budgetLine + " The run is very close to its limit. Use only the shortest delivery path: build/render if still needed, then publish or deliver files, then final. Do not inspect more unless delivery is impossible without it. If a quality gate has not passed but a usable build exists, deliver the best build now with an honest note about its state instead of failing with nothing; offer a further improvement round in the final reply only when your recent attempts were still improving and you can name the concrete next fix, and otherwise say plainly that you have reached your limit with the current approach. If there is no deliverable to build, register or finish with whatever concrete result you already have now (for example task.add for clearly identified items, or finish) instead of continuing to search."
	}
	if level == "consolidate" {
		return budgetLine + " Consolidate completed work, reuse existing observations, and prefer direct edit/build/publish or file delivery over additional inspection."
	}
	return budgetLine + " Spend tool calls deliberately. Keep enough budget for final delivery and avoid exploratory reads unless they directly enable the next action."
}

type limitFinalizationResult struct {
	Result       AgentTurnResult
	IsCompleted  bool
	Observations []turnObservation
	Attachments  []FileAttachment
}

func (agentTurnRunner *AgentTurnRunner) finalizeOrStopForLimit(ctx context.Context, taskRunID string, request AgentTurnRequest, reason string, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion, executionState ExecutionState, usedIterationCount int, usedToolCallCount int) (AgentTurnResult, error) {
	finalization := agentTurnRunner.finalizeLimitIfPossible(ctx, taskRunID, request, requirements, observations, attachments, criteria, executionState)
	if finalization.IsCompleted {
		return finalization.Result, nil
	}
	return agentTurnRunner.stopForLimit(taskRunID, request, reason, finalization.Observations, finalization.Attachments, executionState, usedIterationCount, usedToolCallCount)
}

func (agentTurnRunner *AgentTurnRunner) finalizeLimitIfPossible(ctx context.Context, taskRunID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion, executionState ExecutionState) limitFinalizationResult {
	if ctx.Err() == nil {
		transition := agentTurnRunner.applyCompletionState(ctx, taskRunID, taskRunID+":completion", request, requirements, observations, attachments, criteria, "")
		if transition.IsCompleted {
			return limitFinalizationResult{Result: transition.Result, IsCompleted: true, Observations: observations, Attachments: attachments}
		}
		if transition.DidTransition {
			transition = agentTurnRunner.applyCompletionState(ctx, taskRunID, taskRunID+":completion", request, requirements, transition.Observations, transition.Attachments, criteria, "")
			if transition.IsCompleted {
				return limitFinalizationResult{Result: transition.Result, IsCompleted: true, Observations: transition.Observations, Attachments: transition.Attachments}
			}
			observations = transition.Observations
			attachments = transition.Attachments
		}
		if completionRequirementsHaveEvidence(requirements, observations) {
			if result, isFinalized := agentTurnRunner.finalizeSatisfiedTurn(ctx, taskRunID, request, requirements, observations, criteria, executionState, ""); isFinalized {
				return limitFinalizationResult{Result: result, IsCompleted: true, Observations: observations, Attachments: attachments}
			}
		}
	}
	return limitFinalizationResult{Observations: observations, Attachments: attachments}
}

func (agentTurnRunner *AgentTurnRunner) finalizeEscalateOrStopForLimit(ctx context.Context, taskRunID string, request AgentTurnRequest, reason string, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion, executionState ExecutionState, usedIterationCount int, usedToolCallCount int) (AgentTurnResult, bool, error) {
	finalization := agentTurnRunner.finalizeLimitIfPossible(ctx, taskRunID, request, requirements, observations, attachments, criteria, executionState)
	if finalization.IsCompleted {
		return finalization.Result, false, nil
	}
	observations = finalization.Observations
	attachments = finalization.Attachments
	qualifyingEvents := qualifyingDurableProgressEventsSinceTierStart(agentTurnRunner.taskRunService.ListTaskEvent(taskRunID), observations)
	if agentTurnRunner.options.TaskLevel == TaskLevelMax {
		agentTurnRunner.appendLimitCheckpoint(taskRunID, qualifyingEvents)
		result, errorValue := agentTurnRunner.stopForLimit(taskRunID, request, reason, observations, attachments, executionState, usedIterationCount, usedToolCallCount)
		return result, false, errorValue
	}
	if len(qualifyingEvents) < 2 || agentTurnRunner.budgetEscalationCount(taskRunID) >= maxBudgetEscalationCount {
		result, errorValue := agentTurnRunner.stopForLimit(taskRunID, request, reason, observations, attachments, executionState, usedIterationCount, usedToolCallCount)
		return result, false, errorValue
	}
	agentTurnRunner.escalateBudgetTier(taskRunID, qualifyingEvents, usedIterationCount, usedToolCallCount)
	return AgentTurnResult{}, true, nil
}

const maxBudgetEscalationCount = 2

func (agentTurnRunner *AgentTurnRunner) budgetEscalationCount(taskRunID string) int {
	count := 0
	for _, taskEvent := range agentTurnRunner.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == "agent.budget_escalated" {
			count++
		}
	}
	return count
}

func (agentTurnRunner *AgentTurnRunner) escalateBudgetTier(taskRunID string, qualifyingEvents []qualifyingProgressEvent, usedIterationCount int, usedToolCallCount int) {
	previousTaskLevel := TaskLevelProfileForLevel(agentTurnRunner.options.TaskLevel).TaskLevel
	newTaskLevel, canEscalate := nextTaskLevel(previousTaskLevel)
	if !canEscalate {
		return
	}
	taskLevelProfile := TaskLevelProfileForLevel(newTaskLevel)
	agentTurnRunner.options.TaskLevel = taskLevelProfile.TaskLevel
	agentTurnRunner.options.MaxIterationCount = taskLevelProfile.MaxIterationCount
	agentTurnRunner.options.MaxToolCallCount = taskLevelProfile.MaxToolCallCount
	agentTurnRunner.options.MaxElapsedSecond = int(taskLevelProfile.Duration.Seconds())
	agentTurnRunner.appendEvent(taskRunID, "agent.budget_escalated", marshalEventBody(budgetEscalatedEventBody{
		PreviousTaskLevel:  previousTaskLevel,
		NewTaskLevel:       taskLevelProfile.TaskLevel,
		UsedIterationCount: usedIterationCount,
		UsedToolCallCount:  usedToolCallCount,
		QualifyingEventIDs: qualifyingProgressEventIDs(qualifyingEvents),
	}))
}

func (agentTurnRunner *AgentTurnRunner) appendLimitCheckpoint(taskRunID string, qualifyingEvents []qualifyingProgressEvent) {
	agentTurnRunner.appendEvent(taskRunID, "agent.limit_checkpoint", marshalEventBody(map[string]any{
		"qualifyingProgressEvents": qualifyingEvents,
		"qualifyingEventIDs":       qualifyingProgressEventIDs(qualifyingEvents),
		"note":                     "work was preserved and this task run can be continued",
	}))
}

func (agentTurnRunner *AgentTurnRunner) currentEffortElapsed(turnStartedAt time.Time) bool {
	if turnStartedAt.IsZero() || agentTurnRunner.options.MaxElapsedSecond <= 0 {
		return false
	}
	return time.Since(turnStartedAt) >= time.Duration(agentTurnRunner.options.MaxElapsedSecond)*time.Second
}

func (agentTurnRunner *AgentTurnRunner) currentEffortContext(parentContext context.Context, effortStartedAt time.Time) (context.Context, context.CancelFunc) {
	if effortStartedAt.IsZero() || agentTurnRunner.options.MaxElapsedSecond <= 0 {
		return context.WithCancel(parentContext)
	}
	deadline := effortStartedAt.Add(time.Duration(agentTurnRunner.options.MaxElapsedSecond) * time.Second)
	return context.WithDeadline(parentContext, deadline)
}

func (agentTurnRunner *AgentTurnRunner) turnElapsed(turnStartedAt time.Time) time.Duration {
	if turnStartedAt.IsZero() {
		return 0
	}
	return time.Since(turnStartedAt)
}

func (agentTurnRunner *AgentTurnRunner) finalizeSatisfiedTurn(ctx context.Context, taskRunID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, criteria []qualityCriterion, executionState ExecutionState, requiredToolName string) (AgentTurnResult, bool) {
	actionDocument, errorValue := agentTurnRunner.finalizerAction(ctx, request, observations, executionState)
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_failed", marshalEventBody(map[string]string{"error": errorValue.Error()}))
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_action", marshalEventBody(actionDocument))
	if strings.TrimSpace(actionDocument.Action) != "finish" {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_rejected", marshalEventBody(map[string]string{"reason": "finalizer did not return finish"}))
		return AgentTurnResult{}, false
	}
	if !completionEvidenceIncludesSuccessfulTool(observations, actionDocument.CompletionEvidence, requiredToolName) {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_rejected", marshalEventBody(map[string]string{"reason": "finalizer omitted successful evidence for the repeated tool"}))
		return AgentTurnResult{}, false
	}
	completionGateResult := agentTurnRunner.validateCompletionGateForRequestWithExpectedResults(ctx, taskRunID, request, requirements, observations, nil, criteria, actionDocument)
	if !completionGateResult.IsSatisfied {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_rejected", marshalEventBody(map[string]string{"reason": completionGateResult.Message}))
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendValidityReview(taskRunID, "limit_finalizer", completionGateResult.ValidityState)
	agentTurnRunner.appendQualityReview(taskRunID, criteria, actionDocument.QualityReview, observations)
	reply := finishActionMessage(actionDocument)
	if reply == "" {
		agentTurnRunner.appendEvent(taskRunID, "agent.finalizer_rejected", marshalEventBody(map[string]string{"reason": "empty finish message"}))
		return AgentTurnResult{}, false
	}
	reply = agentTurnRunner.prepareFinishMessageForPlatform(request, reply, completionGateResult.Attachments)
	completedTaskRun, _ := agentTurnRunner.taskRunService.CompleteTaskRun(taskRunID, reply)
	return AgentTurnResult{TaskRun: completedTaskRun, FinishMessage: reply, Attachments: completionGateResult.Attachments, RecoveryActions: recoveryActionsFromObservations(observations)}, true
}

func completionEvidenceIncludesSuccessfulTool(observations []turnObservation, references []completionEvidenceReference, requiredToolName string) bool {
	trimmedToolName := strings.TrimSpace(requiredToolName)
	if trimmedToolName == "" {
		return true
	}
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if isFound && ToolNamesMatch(observation.Tool, trimmedToolName) {
			return true
		}
	}
	return false
}

func (agentTurnRunner *AgentTurnRunner) finalizerAction(ctx context.Context, request AgentTurnRequest, observations []turnObservation, executionState ExecutionState) (turnActionDocument, error) {
	messages := agentTurnRunner.buildTurnMessages(request, observations, executionState)
	messages = append(messages, llm.Message{
		Role:    "system",
		Content: "The required evidence is already available. Do not call tools. Use finish with goalSatisfied=true and cite successful completionEvidence. If the evidence does not actually satisfy the user's request, return a concise fail reply that accurately says what is missing.",
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
	return ParseAgentActionResponse(structuredResponse)
}

func (agentTurnRunner *AgentTurnRunner) runTerminalNoToolsStep(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, reason string) AgentTurnResult {
	rejectionReason := ""
	for attempt := 1; attempt <= 3; attempt++ {
		actionDocument, actionError := agentTurnRunner.terminalNoToolsAction(ctx, request, state.Observations, state.ExecutionState, rejectionReason)
		if actionError != nil {
			rejectionReason = "terminal no-tools action was invalid: " + actionError.Error()
			agentTurnRunner.recordTerminalNoToolsRejection(taskRunID, stepID, state, rejectionReason)
			continue
		}
		if !executionStateIsEmpty(actionDocument.ExecutionStateUpdate) {
			state.ExecutionState = normalizeExecutionState(actionDocument.ExecutionStateUpdate)
			agentTurnRunner.appendEvent(taskRunID, "agent.execution_state", marshalEventBody(state.ExecutionState))
		}
		agentTurnRunner.appendEvent(taskRunID, "agent.terminal_no_tools_action", marshalEventBody(actionDocument))
		result, isComplete, validationMessage := agentTurnRunner.applyTerminalNoToolsAction(ctx, taskRunID, stepID, request, state, actionDocument)
		if isComplete {
			return result
		}
		rejectionReason = validationMessage
		agentTurnRunner.recordTerminalNoToolsRejection(taskRunID, stepID, state, rejectionReason)
	}
	progressEvaluation := actionProgressEvaluation{Reason: "terminal no-tools action did not produce a valid finish or fail"}
	allowance := recoveryAllowance{CanRecover: false, Reason: "tool recovery budget exhausted"}
	result, _ := agentTurnRunner.blockTurnForStall(taskRunID, stepID, request, reason, progressEvaluation, allowance, *state)
	return result
}

func (agentTurnRunner *AgentTurnRunner) terminalNoToolsAction(ctx context.Context, request AgentTurnRequest, observations []turnObservation, executionState ExecutionState, rejectionReason string) (turnActionDocument, error) {
	messages := agentTurnRunner.buildTurnMessages(request, observations, executionState)
	messages = append(messages, llm.Message{
		Role:    "system",
		Content: terminalNoToolsInstruction(observations, agentTurnRunner.options.RecoveryBudget, rejectionReason),
	})
	structuredResponse, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_agent_terminal_no_tools_action",
			Document:           terminalNoToolsActionSchema(),
			IsStrictlyEnforced: true,
		},
		GenerationOptions: agentTurnRunner.options.GenerationOptions,
	})
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	return ParseAgentActionResponse(structuredResponse)
}

func terminalNoToolsInstruction(observations []turnObservation, budget RecoveryBudget, rejectionReason string) string {
	facts := buildFailureReportFacts(observations, budget)
	parts := []string{
		"Recovery tool budget is exhausted. Do not call tools and do not select tools.",
		"Return exactly one terminal action.",
		"Use finish only when you can answer from current context with failureResolution=no_tool_fallback.",
		"Use fail only when completion is blocked, with failureResolution=failure_report and usedFailureFacts copied from FailureReportFacts.",
		"FailureReportFacts:\n" + marshalEventBody(facts),
	}
	if strings.TrimSpace(rejectionReason) != "" {
		parts = append(parts, "Previous terminal action was rejected: "+strings.TrimSpace(rejectionReason))
	}
	return strings.Join(parts, "\n")
}

func (agentTurnRunner *AgentTurnRunner) applyTerminalNoToolsAction(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) (AgentTurnResult, bool, string) {
	switch strings.TrimSpace(actionDocument.Action) {
	case "finish":
		return agentTurnRunner.completeTerminalNoToolsFinish(ctx, taskRunID, stepID, request, state, actionDocument)
	case "fail":
		return agentTurnRunner.failTerminalNoToolsFailure(taskRunID, stepID, request, state, actionDocument)
	default:
		return AgentTurnResult{}, false, "terminal no-tools action must be finish or fail"
	}
}

func (agentTurnRunner *AgentTurnRunner) completeTerminalNoToolsFinish(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) (AgentTurnResult, bool, string) {
	completionGateResult := agentTurnRunner.validateCompletionGateForRequestWithExpectedResults(ctx, taskRunID, request, state.Requirements, state.Observations, state.Attachments, state.QualityCriteria, actionDocument)
	agentTurnRunner.appendValidityReview(taskRunID, "terminal_no_tools_finish", completionGateResult.ValidityState)
	if !completionGateResult.IsSatisfied {
		return AgentTurnResult{}, false, completionGateResult.Message
	}
	agentTurnRunner.appendQualityReview(taskRunID, state.QualityCriteria, actionDocument.QualityReview, state.Observations)
	reply := finishActionMessage(actionDocument)
	if strings.TrimSpace(reply) == "" {
		return AgentTurnResult{}, false, "finish message is empty"
	}
	reply = agentTurnRunner.prepareFinishMessageForPlatform(request, reply, completionGateResult.Attachments)
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "terminal_no_tools_finish", reply)
	completedTaskRun, errorValue := agentTurnRunner.taskRunService.CompleteTaskRun(taskRunID, reply)
	if errorValue != nil {
		return agentTurnRunner.cancelledTaskResultOrCurrent(taskRunID, state.Attachments), true, ""
	}
	return AgentTurnResult{TaskRun: completedTaskRun, FinishMessage: reply, Attachments: completionGateResult.Attachments, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, true, ""
}

func (agentTurnRunner *AgentTurnRunner) failTerminalNoToolsFailure(taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) (AgentTurnResult, bool, string) {
	facts := buildFailureReportFacts(state.Observations, agentTurnRunner.options.RecoveryBudget)
	failureReportResult := validateFailureReportAction(actionDocument, facts)
	if !failureReportResult.IsSatisfied {
		return AgentTurnResult{}, false, failureReportResult.Message
	}
	reason := strings.TrimSpace(firstNonEmptyString(actionDocument.Reason, "agent reported failure"))
	notice, failureReport, validationMessage := failureNoticeFromTerminalAction(request, taskRunID, reason, state.Observations, state.Attachments, state.ExecutionState)
	if validationMessage != "" {
		return AgentTurnResult{}, false, validationMessage
	}
	failedTaskRun, _ := agentTurnRunner.taskRunService.FailTaskRun(taskRunID, reason)
	agentTurnRunner.appendEvent(taskRunID, "agent.failure_report_facts_used", marshalEventBody(actionDocument.UsedFailureFacts))
	agentTurnRunner.appendEvent(taskRunID, "agent.failure_report", marshalEventBody(failureReportEventBody("terminal_no_tools", failureReport, FailureNoticeGenerationStatus{Source: notice.Source})))
	agentTurnRunner.appendEvent(taskRunID, "agent.failure_reply", marshalEventBody(FailureNoticeGenerationStatus{Source: notice.Source}))
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusFailed, "terminal_no_tools_fail", reason)
	reply := notice.SendableMessage()
	failedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, failedTaskRun, reply)
	return AgentTurnResult{TaskRun: failedTaskRun, UserNotice: reply, FailureNotice: notice, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, true, ""
}

func failureNoticeFromTerminalAction(request AgentTurnRequest, taskRunID string, reason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState) (FailureNotice, FailureReport, string) {
	decision := recoveryDecision{
		WhatFailed:      latestFailedOperation(observations),
		WhatWasKnown:    buildLimitObservationSummary(observations),
		NextAction:      strings.TrimSpace(reason),
		UserReplyIntent: strings.TrimSpace(reason),
	}
	failureReport := buildFailureReport(request, taskRunID, "terminal_no_tools", reason, observations, attachments, executionState, decision)
	notice := buildFailureNotice(reason, "terminal_no_tools", failureReport)
	if notice.IsSendable {
		return notice, failureReport, ""
	}
	return FailureNotice{}, failureReport, "fail.reason must be a safe user-facing explanation"
}

func (agentTurnRunner *AgentTurnRunner) recordTerminalNoToolsRejection(taskRunID string, stepID string, state *agentTaskState, reason string) {
	observation := completionGateObservation(len(state.Observations)+1, completionGateResult{Message: strings.TrimSpace(reason)})
	state.Observations = append(state.Observations, observation)
	agentTurnRunner.appendEvent(taskRunID, "agent.terminal_no_tools_rejected", marshalEventBody(observation))
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "terminal_no_tools_rejected", observation.ContentText())
}

func (agentTurnRunner *AgentTurnRunner) stopForLimit(taskRunID string, request AgentTurnRequest, reason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState, usedIterationCount int, usedToolCallCount int) (AgentTurnResult, error) {
	body := map[string]any{
		"taskLevel":          agentTurnRunner.options.TaskLevel,
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
	failureNotice, replyStatus, hasReply := agentTurnRunner.generateLimitReachedNotice(taskRunID, request, reason, observations, nil, executionState)
	agentTurnRunner.appendEvent(taskRunID, "agent.limit_reply", marshalEventBody(replyStatus))
	if !hasReply {
		agentTurnRunner.appendUnavailableReplyEvents(taskRunID, "limit", reason, replyStatus)
		fallbackReply := deterministicFailureFallbackReply(request.ResponseLanguage)
		blockedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, blockedTaskRun, fallbackReply)
		return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: fallbackReply, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
	}
	reply := failureNotice.SendableMessage()
	blockedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, blockedTaskRun, reply)
	return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: reply, FailureNotice: failureNotice, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
}

func persistTaskRunResult(taskRunService *task.TaskRunService, taskRun task.TaskRun, result string) task.TaskRun {
	persistedTaskRun, errorValue := taskRunService.RecordTaskRunResult(taskRun.TaskRunID, result)
	if errorValue != nil {
		taskRun.Result = result
		return taskRun
	}
	return persistedTaskRun
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
