package bluecollar

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"strings"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/harnessdriver"
	"github.com/Dawn-kim-official/blueclaw/model"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

type AgentKernel struct {
	planCompiler            PlanCompiler
	taskRunService          taskstate.TaskRunStore
	taskStepService         taskstate.TaskStepStore
	taskArtifactService     taskstate.TaskArtifactStore
	languageModel           model.LanguageModelProvider
	maxTaskLanguageModel    model.LanguageModelProvider
	xHighTaskLanguageModel  model.LanguageModelProvider
	highTaskLanguageModel   model.LanguageModelProvider
	mediumTaskLanguageModel model.LanguageModelProvider
	lowTaskLanguageModel    model.LanguageModelProvider
	xLowTaskLanguageModel   model.LanguageModelProvider
	intakeLanguageModel     model.LanguageModelProvider
	turnOptions             TurnOptions
	intakeOptions           IntakeOptions
	instructionPrompt       string
	instructionSources      []InstructionSource
	instructionLoader       func() InstructionBundle
	skillRetriever          SkillRetriever
	companyProvider         func() CompanyContext
}

func NewAgentKernel(taskRunService taskstate.TaskRunStore, taskStepService taskstate.TaskStepStore) *AgentKernel {
	return &AgentKernel{
		planCompiler:        PlanCompiler{},
		taskRunService:      taskRunService,
		taskStepService:     taskStepService,
		taskArtifactService: taskstate.NewTaskArtifactService(),
	}
}

func (agentKernel *AgentKernel) UseLanguageModelProvider(languageModel model.LanguageModelProvider) {
	agentKernel.languageModel = languageModel
}

func (agentKernel *AgentKernel) UseTaskTierLanguageModels(taskTierLanguageModels harnessdriver.TaskTierLanguageModels) {
	agentKernel.maxTaskLanguageModel = taskTierLanguageModels.Max
	agentKernel.xHighTaskLanguageModel = taskTierLanguageModels.XHigh
	agentKernel.highTaskLanguageModel = taskTierLanguageModels.High
	agentKernel.mediumTaskLanguageModel = taskTierLanguageModels.Medium
	agentKernel.lowTaskLanguageModel = taskTierLanguageModels.Low
	agentKernel.xLowTaskLanguageModel = taskTierLanguageModels.XLow
}

func (agentKernel *AgentKernel) UseTaskArtifactService(taskArtifactService taskstate.TaskArtifactStore) {
	if taskArtifactService != nil {
		agentKernel.taskArtifactService = taskArtifactService
	}
}

func (agentKernel *AgentKernel) UseTurnOptions(turnOptions TurnOptions) {
	agentKernel.turnOptions = normalizeTurnOptions(turnOptions)
}

func (agentKernel *AgentKernel) UseIntakeLanguageModelProvider(languageModel model.LanguageModelProvider) {
	agentKernel.intakeLanguageModel = languageModel
}

func (agentKernel *AgentKernel) UseIntakeOptions(intakeOptions IntakeOptions) {
	agentKernel.intakeOptions = normalizeIntakeOptions(intakeOptions)
}

func (agentKernel *AgentKernel) UseInstructionPrompt(instructionPrompt string) {
	agentKernel.instructionPrompt = strings.TrimSpace(instructionPrompt)
}

func (agentKernel *AgentKernel) UseInstructionBundle(instructionBundle InstructionBundle) {
	agentKernel.instructionPrompt = strings.TrimSpace(instructionBundle.Prompt)
	agentKernel.instructionSources = append([]InstructionSource{}, instructionBundle.Sources...)
}

func (agentKernel *AgentKernel) UseInstructionBundleLoader(instructionLoader func() InstructionBundle) {
	agentKernel.instructionLoader = instructionLoader
	if instructionLoader != nil {
		agentKernel.UseInstructionBundle(instructionLoader())
	}
}

func (agentKernel *AgentKernel) UseSkillRetriever(skillRetriever SkillRetriever) {
	agentKernel.skillRetriever = skillRetriever
}

func (agentKernel *AgentKernel) UseCompanyProvider(companyProvider func() CompanyContext) {
	agentKernel.companyProvider = companyProvider
}

func (agentKernel *AgentKernel) companyContext() CompanyContext {
	if agentKernel.companyProvider == nil {
		return CompanyContext{}
	}
	return agentKernel.companyProvider()
}

func (agentKernel *AgentKernel) RefreshSkillIndex(ctx context.Context, instructionBundle InstructionBundle) {
	if agentKernel.skillRetriever == nil {
		return
	}
	agentKernel.skillRetriever.Refresh(ctx, instructionBundle.Skills)
}

func (agentKernel *AgentKernel) RunTurn(responseContext context.Context, request AgentTurnRequest) (AgentTurnResult, error) {
	return agentKernel.RunAgentRequest(responseContext, AgentRequest{
		RequesterPersonID:          request.RequesterPersonID,
		RequesterName:              request.RequesterName,
		RequesterCallingName:       request.RequesterCallingName,
		RequesterHandle:            request.RequesterHandle,
		RequesterCircles:           append([]string{}, request.RequesterCircles...),
		SourceReference:            request.SourceReference,
		IsApprovalContinuation:     request.IsApprovalContinuation,
		IsRuntimeRestartResume:     request.IsRuntimeRestartResume,
		ExistingTaskRunID:          request.ExistingTaskRunID,
		OriginReplyTargetID:        request.OriginReplyTargetID,
		OriginIsThread:             request.OriginIsThread,
		ProfileName:                request.ProfileName,
		ConversationID:             request.ConversationID,
		Prompt:                     request.Prompt,
		InputParts:                 append([]AgentPart{}, request.InputParts...),
		ResponseLanguage:           request.ResponseLanguage,
		VisibleContext:             request.VisibleContext,
		MemoryFacts:                request.MemoryFacts,
		ToolSet:                    request.ToolSet,
		PinnedToolNames:            append([]string{}, request.PinnedToolNames...),
		PinnedSkillNames:           append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:          request.WorkspaceRootPath,
		ActivePaths:                request.ActivePaths,
		ActiveGoal:                 request.ActiveGoal,
		PriorTask:                  request.PriorTask,
		ScheduledRun:               request.ScheduledRun,
		PrecomputedTurnDecision:    request.PrecomputedTurnDecision,
		IsPrecomputedDecisionExact: request.IsPrecomputedDecisionExact,
		SkipSkillSelection:         request.SkipSkillSelection,
		AmbientDuty:                request.AmbientDuty,
		TaskLevel:                  request.TaskLevel,
		TurnStartedAt:              request.TurnStartedAt,
		CheckpointSender:           request.CheckpointSender,
	})
}

func (agentKernel *AgentKernel) CompleteLaunchFailure(responseContext context.Context, request AgentTurnRequest, phase string, stepName string, errorValue error) AgentTurnResult {
	taskRun, createError := agentKernel.taskRunForLaunchFailure(request)
	reason := firstNonEmptyString(errorString(errorValue), errorString(createError))
	if createError != nil {
		reason = strings.TrimSpace(reason + "; task_run_create=" + createError.Error())
	}
	failedTaskRun, failError := agentKernel.taskRunService.FailTaskRun(taskRun.TaskRunID, reason)
	if failError != nil {
		taskRun.Status = taskstate.TaskStatusFailed
		taskRun.FailureReason = firstNonEmptyString(reason, failError.Error())
		failedTaskRun = taskRun
	}
	launchFailureReport := FailureReport{
		Phase:              phase,
		StepName:           stepName,
		StopReason:         reason,
		SafeFailureSummary: reason,
		RawError:           reason,
		OriginalRequest:    request.Prompt,
		ResponseLanguage:   request.ResponseLanguage,
		DiagnosticEventID:  diagnosticEventID(request, taskRun.TaskRunID, phase),
	}
	failureNotice, noticeStatus := (FailureNoticeGenerator{LanguageModel: agentKernel.languageModel}).Generate(responseContext, launchFailureReport)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.failure_reply", marshalEventBody(noticeStatus))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.failure_report", marshalEventBody(failureReportEventBody(phase, launchFailureReport, noticeStatus)))
	failedTaskRun = persistTaskRunResult(agentKernel.taskRunService, failedTaskRun, failureNotice.SendableMessage())
	return AgentTurnResult{TaskRun: failedTaskRun, UserNotice: failedTaskRun.Result, FailureNotice: failureNotice, ToolNames: toolNamesForEvent(request.ToolSet)}
}

func (agentKernel *AgentKernel) taskRunForLaunchFailure(request AgentTurnRequest) (taskstate.TaskRun, error) {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := agentKernel.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun, nil
		}
	}
	return agentKernel.taskRunService.CreateTaskRunWithOriginAndError(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}

func (agentKernel *AgentKernel) RunAgentRequest(responseContext context.Context, request AgentRequest) (AgentTurnResult, error) {
	requestReceivedAt := time.Now()
	routerCallLedger := &turnRouterCallLedger{}
	request.ActiveGoal = normalizePersistedActiveGoal(request.ActiveGoal)
	if request.TurnStartedAt.IsZero() {
		request.TurnStartedAt = requestReceivedAt.Add(-2 * time.Second)
	}
	request.ResponseLanguage = ResolveResponseLanguage(request.ResponseLanguage, request.VisibleContext.ResponseLanguage)
	if strings.TrimSpace(request.ActiveGoal.RestoreError) != "" {
		return agentKernel.CompleteLaunchFailure(responseContext, launchFailureRequest(request), "restore_state", "active_goal", errors.New(request.ActiveGoal.RestoreError)), nil
	}
	baseInstructionBundle := agentKernel.currentInstructionBundle()
	instructionBundle := baseInstructionBundle
	turnToolSet := request.ToolSet
	intakeRequest := request
	intakeRequest.ToolSet = turnToolSet
	turnDecision, errorValue := routedTurnDecision(intakeRequest)
	if errorValue != nil {
		result := agentKernel.completeTurnRouterFailure(responseContext, intakeRequest, errorValue, routerCallLedger.records)
		return result, nil
	}
	intakeDecision := turnDecision.IntakeDecision()
	intakeDecision = promoteArtifactTaskLevelForRequest(intakeRequest, intakeDecision)
	turnOptions := agentKernel.turnOptionsForIntakeDecision(intakeDecision)
	taskBudget := newTurnBudgetContext(responseContext, request.TurnStartedAt, request.IsRuntimeRestartResume, requestReceivedAt, turnOptions)
	defer taskBudget.cancel()
	taskContext := taskBudget.workContext
	request.TurnStartedAt = taskBudget.turnStartedAt
	if result, didExpire := agentKernel.completeIntakeIfElapsed(taskBudget, intakeRequest, intakeDecision, turnDecision.Route, routerCallLedger.records); didExpire {
		return result, nil
	}
	request.ResponseLanguage = ResolveResponseLanguage(intakeDecision.ResponseLanguage, request.ResponseLanguage)
	manualPinnedToolNames := append([]string{}, request.PinnedToolNames...)
	request = restorePersistedToolSelection(request)
	persistedPinnedToolNames := append([]string{}, request.PinnedToolNames...)
	intakeRequest = request
	if turnDecision.Route == TurnRouteStartTask && !request.IsApprovalContinuation {
		if strings.TrimSpace(request.ExistingTaskRunID) == strings.TrimSpace(request.ActiveGoal.TaskRunID) {
			request.ExistingTaskRunID = ""
			request.IsRuntimeRestartResume = false
			intakeRequest.ExistingTaskRunID = ""
			intakeRequest.IsRuntimeRestartResume = false
		}
		request.ActiveGoal = ActiveGoal{}
		intakeRequest.ActiveGoal = ActiveGoal{}
		request, intakeDecision = applyPriorTaskOutcomeRecovery(request, intakeDecision)
		intakeDecision.InitialToolNames = registeredToolNamesOnly(turnToolSet, intakeDecision.InitialToolNames)
		intakeRequest.ActiveGoal = request.ActiveGoal
		intakeDecision = agentKernel.restoreEscalatedTaskLevelForContinuation(intakeRequest, intakeDecision)
	}
	lifecycleMode := taskLifecycleModeForRequest(turnDecision, request)
	if lifecycleMode == taskLifecycleSemanticRevision {
		request.ExistingTaskRunID = ""
		request.IsRuntimeRestartResume = false
		request.ActiveGoal = ActiveGoal{}
		intakeRequest.ExistingTaskRunID = ""
		intakeRequest.IsRuntimeRestartResume = false
		intakeRequest.ActiveGoal = ActiveGoal{}
	}
	startsNewSemanticRun := lifecycleMode == taskLifecycleFresh || lifecycleMode == taskLifecycleSemanticRevision
	request.PinnedToolNames = appendUniqueStrings(append([]string{}, request.PinnedToolNames...), intakeDecision.InitialToolNames...)
	intakeRequest.PinnedToolNames = request.PinnedToolNames
	if turnDecision.Route == TurnRouteConsume {
		result, errorValue := agentKernel.completeConsumedRequest(intakeRequest, turnDecision, routerCallLedger.records)
		return result, errorValue
	}
	if !request.SkipSkillSelection {
		instructionBundle, intakeDecision = agentKernel.selectInstructionBundleForResolvedRequest(taskContext, baseInstructionBundle, request, intakeDecision)
	}
	if instructionBundle.ContractSkillArbitrationFailed {
		agentKernel.taskRunService.AppendTaskEvent(request.ExistingTaskRunID, "agent.contract_arbitration_degraded", marshalEventBody(map[string]string{
			"reason": "contract skill arbitration failed; continuing with score-selected skills",
		}))
	}
	if result, didExpire := agentKernel.completeIntakeIfElapsed(taskBudget, intakeRequest, intakeDecision, turnDecision.Route, routerCallLedger.records); didExpire {
		return result, nil
	}
	request.PinnedToolNames = pinnedToolNamesForResolvedRequest(
		manualPinnedToolNames,
		persistedPinnedToolNames,
		intakeDecision.InitialToolNames,
		instructionBundle.RequiredEvidenceTools,
		startsNewSemanticRun,
	)
	intakeRequest.PinnedToolNames = request.PinnedToolNames
	request.PinnedSkillNames = appendUniqueStrings(request.PinnedSkillNames, selectedSkillNameList(instructionBundle.SkillDecisions)...)
	intakeRequest.PinnedSkillNames = request.PinnedSkillNames
	if intakeDecision.Classification == IntakeClassificationNeedsConfirmation {
		result, errorValue := agentKernel.completeIntakeOnlyRequest(taskContext, intakeRequest, intakeDecision, taskstate.TaskStatusWaitingUserInput, routerCallLedger.records)
		result.TurnRoute = turnDecision.Route
		return result, errorValue
	}
	if intakeDecision.Classification == IntakeClassificationUnsupported {
		result, errorValue := agentKernel.completeIntakeOnlyRequest(taskContext, intakeRequest, intakeDecision, taskstate.TaskStatusBlocked, routerCallLedger.records)
		result.TurnRoute = turnDecision.Route
		return result, errorValue
	}

	requiredNextToolNames := requiredNextToolNamesForResolvedRequest(request.ActiveGoal, instructionBundle.RequiredNextTools, intakeDecision.InitialToolNames)
	request.ActiveGoal.RequiredNextTools = requiredNextToolNames
	intakeRequest.ActiveGoal = request.ActiveGoal
	requiredAttachmentSuffixes := attachmentSuffixesForRequestedOutputFormats(intakeDecision.RequestedOutputFormats)
	evidenceHints := selectedEvidenceHintTools(instructionBundle)
	confirmationEvidenceHints := confirmationEvidenceHintsForRequest(request, intakeDecision, evidenceHints)
	confirmationPlan, errorValue := agentKernel.planConfirmationGate(taskContext, request, intakeDecision, confirmationEvidenceHints)
	if errorValue != nil {
		if result, didExpire := agentKernel.completeIntakeIfElapsed(taskBudget, intakeRequest, intakeDecision, turnDecision.Route, routerCallLedger.records); didExpire {
			return result, nil
		}
		return AgentTurnResult{}, errorValue
	}
	if confirmationPlan.DegradedError != nil {
		agentKernel.taskRunService.AppendTaskEvent(strings.TrimSpace(request.ExistingTaskRunID), "agent.confirmation_plan_degraded", marshalEventBody(map[string]string{
			"reason": "execution plan generation failed twice; continuing with the runtime tool approval gate: " + confirmationPlan.DegradedError.Error(),
		}))
	}
	if confirmationPlan.Decision.RequiresClarification {
		confirmationResult, pauseError := agentKernel.pauseForClarification(taskContext, request, intakeDecision, confirmationPlan, OutcomeContract{}, confirmationEvidenceHints, selectedSkillNameList(instructionBundle.SkillDecisions))
		if pauseError != nil && taskBudget.didWorkExpire() {
			intakeRequest.ExistingTaskRunID = confirmationResult.TaskRun.TaskRunID
			confirmationResult = agentKernel.completeIntakeElapsed(taskBudget, intakeRequest, intakeDecision, routerCallLedger.records)
			pauseError = nil
		}
		confirmationResult.TurnRoute = turnDecision.Route
		return confirmationResult, pauseError
	}
	executionPlan := confirmationPlan.ExecutionPlan
	hasExecutionPlan := confirmationPlan.HasExecutionPlan
	outcomeContract := outcomeContractForRequest(request, intakeDecision, instructionBundle, executionPlan, hasExecutionPlan, requiredAttachmentSuffixes)
	outcomeContract = dischargeResolvedInputContract(request, turnDecision, outcomeContract)
	if result, didExpire := agentKernel.completeIntakeIfElapsed(taskBudget, intakeRequest, intakeDecision, turnDecision.Route, routerCallLedger.records); didExpire {
		return result, nil
	}
	requiredNextToolNames = requiredNextToolNamesForResolvedRequest(request.ActiveGoal, instructionBundle.RequiredNextTools, intakeDecision.InitialToolNames)
	request.ActiveGoal.RequiredNextTools = requiredNextToolNames
	requiredEvidenceTools := outcomeContract.RequiredEvidenceTools
	requiredAttachmentSuffixes = outcomeContract.RequiredAttachmentSuffixes
	request.PinnedToolNames = pinnedToolNamesForResolvedRequest(
		manualPinnedToolNames,
		persistedPinnedToolNames,
		intakeDecision.InitialToolNames,
		requiredEvidenceTools,
		startsNewSemanticRun,
	)
	intakeRequest.PinnedToolNames = request.PinnedToolNames

	contractToolWorkingSet := ContractToolWorkingSet{
		RequiredNextTools:     requiredNextToolNames,
		RequiredEvidenceTools: append([]string{}, instructionBundle.RequiredEvidenceTools...),
	}
	turnRequest := AgentTurnRequest{
		RequesterPersonID:          request.RequesterPersonID,
		Company:                    agentKernel.companyContext(),
		RequesterName:              request.RequesterName,
		RequesterCallingName:       request.RequesterCallingName,
		RequesterHandle:            request.RequesterHandle,
		RequesterCircles:           append([]string{}, request.RequesterCircles...),
		SourceReference:            request.SourceReference,
		IsApprovalContinuation:     request.IsApprovalContinuation,
		IsRuntimeRestartResume:     request.IsRuntimeRestartResume,
		ExistingTaskRunID:          request.ExistingTaskRunID,
		OriginReplyTargetID:        request.OriginReplyTargetID,
		OriginIsThread:             request.OriginIsThread,
		ProfileName:                normalizedAgentProfileName(request.ProfileName),
		ConversationID:             request.ConversationID,
		Prompt:                     request.Prompt,
		InputParts:                 append([]AgentPart{}, request.InputParts...),
		ResponseLanguage:           request.ResponseLanguage,
		VisibleContext:             request.VisibleContext,
		MemoryFacts:                request.MemoryFacts,
		ToolSet:                    turnToolSet,
		AvailableSkills:            append([]SkillInstruction{}, instructionBundle.Skills...),
		PinnedToolNames:            append([]string{}, request.PinnedToolNames...),
		PinnedSkillNames:           append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:          request.WorkspaceRootPath,
		InstructionPrompt:          instructionBundle.Prompt,
		InstructionSources:         append([]InstructionSource{}, instructionBundle.Sources...),
		SkillDecisions:             append([]SkillSelectionDecision{}, instructionBundle.SkillDecisions...),
		SkillRetrievalMode:         instructionBundle.RetrievalMode,
		SkillIndexStatus:           instructionBundle.IndexStatus,
		SkillCandidateCount:        instructionBundle.CandidateCount,
		SkillQueries:               append([]string{}, instructionBundle.SkillQueries...),
		ContractToolWorkingSet:     contractToolWorkingSet,
		RequiredEvidenceTools:      requiredEvidenceTools,
		RequiredAttachmentSuffixes: requiredAttachmentSuffixes,
		OutcomeContract:            outcomeContract,
		ActiveGoal:                 activeGoalForTurn(request, outcomeContract, executionPlan, hasExecutionPlan),
		PriorTask:                  request.PriorTask,
		ScheduledRun:               request.ScheduledRun,
		AmbientDuty:                request.AmbientDuty,
		TaskShape:                  intakeDecision.TaskShape,
		TaskLevel:                  intakeDecision.TaskLevel,
		EstimatedMinutes:           intakeDecision.EstimatedMinutes,
		TurnStartedAt:              request.TurnStartedAt,
		EffortStartedAt:            request.TurnStartedAt,
		TurnAnchorClamped:          taskBudget.didClampAnchor,
		OriginalTurnStartedAt:      taskBudget.originalTurnStartedAt,
		CheckpointSender:           request.CheckpointSender,
	}
	agentTurnRunner := NewAgentTurnRunnerWithRecoveryModel(
		agentKernel.taskRunService,
		agentKernel.taskStepService,
		agentKernel.taskArtifactService,
		agentKernel.taskLanguageModelForLevel(intakeDecision.TaskLevel),
		agentKernel.languageModel,
		turnOptions,
	)
	agentTurnRunner.UseTaskLevelLanguageModelResolver(agentKernel.taskLanguageModelForLevel)
	result, errorValue := agentTurnRunner.RunTurn(taskBudget.callerContext(), turnRequest)
	result.TurnRoute = turnDecision.Route
	result.ToolNames = toolNamesForEvent(turnRequest.ToolSet)
	if result.TaskRun.TaskRunID != "" {
		agentKernel.appendTurnRouterCallRecords(result.TaskRun.TaskRunID, routerCallLedger.records)
		agentKernel.taskRunService.AppendTaskEvent(result.TaskRun.TaskRunID, "agent.intake", marshalEventBody(intakeDecision))
		agentKernel.appendGoalLifecycleEvent(result.TaskRun, turnRequest.ActiveGoal)
	}
	return result, errorValue
}

func requestStartsFreshTask(turnDecision TurnDecision, request AgentRequest) bool {
	return turnDecision.Route == TurnRouteStartTask &&
		!request.IsApprovalContinuation &&
		!request.IsRuntimeRestartResume &&
		strings.TrimSpace(request.ExistingTaskRunID) == ""
}

func launchFailureRequest(request AgentRequest) AgentTurnRequest {
	return AgentTurnRequest{
		RequesterPersonID:   request.RequesterPersonID,
		SourceReference:     request.SourceReference,
		ExistingTaskRunID:   request.ExistingTaskRunID,
		OriginReplyTargetID: request.OriginReplyTargetID,
		OriginIsThread:      request.OriginIsThread,
		ConversationID:      request.ConversationID,
		Prompt:              request.Prompt,
		ResponseLanguage:    request.ResponseLanguage,
		ToolSet:             request.ToolSet,
	}
}

type taskLifecycleMode string

const (
	taskLifecycleFresh            taskLifecycleMode = "fresh"
	taskLifecycleApprovalResume   taskLifecycleMode = "approval_resume"
	taskLifecycleRuntimeResume    taskLifecycleMode = "runtime_resume"
	taskLifecycleSemanticRevision taskLifecycleMode = "semantic_revision"
	taskLifecycleContinuation     taskLifecycleMode = "continuation"
)

func taskLifecycleModeForRequest(turnDecision TurnDecision, request AgentRequest) taskLifecycleMode {
	if request.IsApprovalContinuation {
		return taskLifecycleApprovalResume
	}
	if request.IsRuntimeRestartResume {
		return taskLifecycleRuntimeResume
	}
	if turnDecision.Route == TurnRouteReviseTask {
		return taskLifecycleSemanticRevision
	}
	if requestStartsFreshTask(turnDecision, request) {
		return taskLifecycleFresh
	}
	return taskLifecycleContinuation
}

func pinnedToolNamesForResolvedRequest(
	manualToolNames []string,
	persistedToolNames []string,
	routerToolNames []string,
	requiredEvidenceTools []string,
	isFreshTask bool,
) []string {
	preservedToolNames := persistedToolNames
	selectedToolNames := routerToolNames
	if isFreshTask {
		preservedToolNames = manualToolNames
		selectedToolNames = appendUniqueStrings(routerToolNames, requiredEvidenceTools...)
	}
	return appendUniqueStrings(append([]string{}, preservedToolNames...), selectedToolNames...)
}

func requiredNextToolNamesForResolvedRequest(activeGoal ActiveGoal, arbitratedToolNames []string, routerToolNames []string) []string {
	if len(activeGoal.RequiredNextTools) > 0 {
		return appendUniqueStrings(activeGoal.RequiredNextTools)
	}
	if len(arbitratedToolNames) > 0 {
		return appendUniqueStrings(arbitratedToolNames)
	}
	return appendUniqueStrings(routerToolNames)
}

func (agentKernel *AgentKernel) completeTurnRouterFailure(responseContext context.Context, request AgentRequest, errorValue error, routerCallRecords []llmCallRecord) AgentTurnResult {
	result := agentKernel.CompleteLaunchFailure(responseContext, AgentTurnRequest{
		RequesterPersonID:   request.RequesterPersonID,
		SourceReference:     request.SourceReference,
		ExistingTaskRunID:   request.ExistingTaskRunID,
		OriginReplyTargetID: request.OriginReplyTargetID,
		OriginIsThread:      request.OriginIsThread,
		ConversationID:      request.ConversationID,
		Prompt:              request.Prompt,
		ResponseLanguage:    request.ResponseLanguage,
		ToolSet:             request.ToolSet,
	}, "routing", "turn_router", errorValue)
	agentKernel.appendTurnRouterCallRecords(result.TaskRun.TaskRunID, routerCallRecords)
	return result
}

func (agentKernel *AgentKernel) selectInstructionBundleForResolvedRequest(ctx context.Context, baseInstructionBundle InstructionBundle, request AgentRequest, intakeDecision IntakeDecision) (InstructionBundle, IntakeDecision) {
	selectionRequest := request
	selectionContract := selectionRequest.ActiveGoal.OutcomeContract
	selectionContract.RequiredAttachmentSuffixes = appendUniqueStrings(selectionContract.RequiredAttachmentSuffixes, attachmentSuffixesForRequestedOutputFormats(intakeDecision.RequestedOutputFormats)...)
	selectionContract.ExpectedResults = appendExpectedResults(selectionContract.ExpectedResults, intakeDecision.ExpectedResults...)
	if len(selectionContract.RequiredAttachmentSuffixes) > 0 {
		selectionContract.RequiredEvidenceTools = appendUniqueStrings(selectionContract.RequiredEvidenceTools, toolcontract.FileDeliverToolName)
		selectionContract.ArtifactRequirement = ArtifactRequirementRequired
	}
	selectionRequest.ActiveGoal.OutcomeContract = normalizeOutcomeContract(selectionContract)
	instructionBundle := selectInstructionBundleForRequestWithRetrieverAndRouter(
		ctx,
		baseInstructionBundle,
		selectionRequest,
		agentKernel.skillRetriever,
		NewSkillSearchQueryRouter(agentKernel.classificationLanguageModel()),
	)
	instructionBundle = instructionBundleWithPinnedSkills(instructionBundle, selectionRequest)
	instructionBundle = instructionBundleWithToolOwningSkills(instructionBundle, selectionRequest, intakeDecision.InitialToolNames)
	return instructionBundle, intakeDecision
}

func (agentKernel *AgentKernel) completeConsumedRequest(request AgentRequest, decision TurnDecision, routerCallRecords []llmCallRecord) (AgentTurnResult, error) {
	taskRun := agentKernel.createTaskRunForRequest(request)
	reactionEmojiName := NormalizeReactionEmojiName(decision.ReactionEmojiName)
	agentKernel.appendTurnRouterCallRecords(taskRun.TaskRunID, routerCallRecords)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.intake", marshalEventBody(decision.IntakeDecision()))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.consumed", marshalEventBody(map[string]string{
		"route":             string(decision.Route),
		"reason":            strings.TrimSpace(decision.Reason),
		"reactionEmojiName": reactionEmojiName,
	}))
	completedTaskRun, errorValue := agentKernel.taskRunService.CompleteTaskRun(taskRun.TaskRunID, "consumed")
	if errorValue != nil {
		return AgentTurnResult{}, errorValue
	}
	return AgentTurnResult{TaskRun: completedTaskRun, TurnRoute: TurnRouteConsume, ReactionEmojiName: reactionEmojiName, FinishMessage: strings.TrimSpace(decision.UserFacingReply), ReplySuppressed: true, ToolNames: toolNamesForEvent(request.ToolSet)}, nil
}

type confirmationGatePlan struct {
	ExecutionPlan    ExecutionPlan
	Decision         ConfirmationPolicyDecision
	HasExecutionPlan bool
	DegradedError    error
}

func (agentKernel *AgentKernel) planConfirmationGate(responseContext context.Context, request AgentRequest, intakeDecision IntakeDecision, evidenceHints []string) (confirmationGatePlan, error) {
	if request.IsApprovalContinuation || request.IsRuntimeRestartResume {
		return confirmationGatePlan{}, nil
	}
	if !shouldBuildExecutionPlanForConfirmation(request, intakeDecision, evidenceHints) {
		return confirmationGatePlan{}, nil
	}
	executionPlan, errorValue := agentKernel.BuildExecutionPlan(responseContext, request, evidenceHints)
	if errorValue != nil {
		executionPlan, errorValue = agentKernel.BuildExecutionPlan(responseContext, request, evidenceHints)
	}
	if errorValue != nil {
		return confirmationGatePlan{DegradedError: errorValue}, nil
	}
	decision := EvaluateConfirmationPolicy(executionPlan)
	return confirmationGatePlan{ExecutionPlan: executionPlan, Decision: decision, HasExecutionPlan: true}, nil
}

func (agentKernel *AgentKernel) pauseForClarification(responseContext context.Context, request AgentRequest, intakeDecision IntakeDecision, plan confirmationGatePlan, outcomeContract OutcomeContract, evidenceHints []string, selectedSkills []string) (AgentTurnResult, error) {
	executionPlan := plan.ExecutionPlan
	decision := plan.Decision
	taskRun := agentKernel.createTaskRunForRequest(request)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.intake", marshalEventBody(intakeDecision))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "confirmation.plan_created", marshalEventBody(executionPlan))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "confirmation.policy_decision", marshalEventBody(decision))
	reply, errorValue := agentKernel.GenerateClarificationMessage(responseContext, request, executionPlan, decision)
	if errorValue != nil {
		return AgentTurnResult{TaskRun: taskRun}, errorValue
	}
	waitingTaskRun, errorValue := agentKernel.taskRunService.PauseTaskRun(taskRun.TaskRunID, taskstate.TaskStatusWaitingUserInput, reply)
	if errorValue != nil {
		return AgentTurnResult{}, errorValue
	}
	waitingGoal := activeGoalFromExecutionPlan(taskRun.TaskRunID, executionPlan, ActiveGoalStatusWaitingUserInput, request.ToolSet, evidenceHints, nil)
	waitingGoal.RequiredNextTools = appendUniqueStrings(request.ActiveGoal.RequiredNextTools)
	waitingGoal.OutcomeContract = normalizeOutcomeContract(outcomeContract)
	waitingGoal.SelectedToolNames = appendUniqueStrings(nil, request.PinnedToolNames...)
	waitingGoal.SelectedSkillNames = appendUniqueStrings(nil, selectedSkills...)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.goal.created", marshalEventBody(waitingGoal))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.goal.waiting_user_input", marshalEventBody(waitingGoal))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "confirmation.clarification_requested", reply)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "ask.requested", marshalEventBody(map[string]string{
		"kind":             "ask_input",
		"question":         reply,
		"message":          reply,
		"responseLanguage": request.ResponseLanguage,
	}))
	return AgentTurnResult{TaskRun: waitingTaskRun, UserNotice: reply, ToolNames: toolNamesForEvent(request.ToolSet)}, nil
}

func (agentKernel *AgentKernel) RunTask(requesterPersonID string, originConversationID string, prompt string) (taskstate.TaskRun, error) {
	return agentKernel.RunTaskWithOrigin(requesterPersonID, taskstate.TaskRunOrigin{ConversationID: originConversationID}, prompt)
}

func (agentKernel *AgentKernel) RunTaskWithOrigin(requesterPersonID string, origin taskstate.TaskRunOrigin, prompt string) (taskstate.TaskRun, error) {
	taskRun := agentKernel.taskRunService.CreateTaskRunWithOrigin(requesterPersonID, origin, prompt)
	taskPlan, errorValue := agentKernel.planCompiler.CompilePlan(prompt)
	if errorValue != nil {
		return taskstate.TaskRun{}, errorValue
	}

	for _, taskPlanStep := range taskPlan.TaskSteps {
		agentKernel.taskStepService.AddTaskStep(taskstate.TaskStep{
			TaskStepID:               taskRun.TaskRunID + ":" + taskPlanStep.Name,
			TaskRunID:                taskRun.TaskRunID,
			AssignedAgentProfileName: taskPlanStep.AssignedAgentProfileName,
			Instruction:              taskPlanStep.Instruction,
			Status:                   taskstate.TaskStatusPlanned,
		})
	}

	return agentKernel.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "planner")
}

func (agentKernel *AgentKernel) ResumeTask(taskRunID string) (taskstate.TaskRun, error) {
	return agentKernel.taskRunService.ResumeTaskRun(taskRunID)
}

func (agentKernel *AgentKernel) completeIntakeOnlyRequest(responseContext context.Context, request AgentRequest, intakeDecision IntakeDecision, status taskstate.TaskStatus, routerCallRecords []llmCallRecord) (AgentTurnResult, error) {
	taskRun := agentKernel.createTaskRunForRequest(request)
	agentKernel.appendTurnRouterCallRecords(taskRun.TaskRunID, routerCallRecords)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.intake", marshalEventBody(intakeDecision))
	finishMessage := strings.TrimSpace(intakeDecision.UserFacingReply)
	if finishMessage == "" {
		finishMessage = (FailureNoticeGenerator{LanguageModel: agentKernel.languageModel}).GenerateIntakeNotice(responseContext, IntakeReport{
			Classification:    intakeDecision.Classification,
			Reason:            intakeDecision.Reason,
			OriginalRequest:   request.Prompt,
			ResponseLanguage:  request.ResponseLanguage,
			DiagnosticEventID: taskRun.TaskRunID + ":task_intake",
		}).SendableMessage()
	}
	if intakeDecision.Classification == IntakeClassificationNeedsConfirmation && len(intakeDecision.ClarificationOptions) >= 2 {
		finishMessage = firstNonEmptyString(strings.TrimSpace(intakeDecision.ClarificationQuestion), finishMessage)
	}
	blockedTaskRun, errorValue := agentKernel.taskRunService.PauseTaskRun(taskRun.TaskRunID, status, intakeDecision.Reason)
	if errorValue != nil {
		return AgentTurnResult{}, errorValue
	}
	if status == taskstate.TaskStatusWaitingUserInput && intakeDecision.Classification == IntakeClassificationNeedsConfirmation && len(intakeDecision.ClarificationOptions) >= 2 {
		agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "ask.requested", marshalEventBody(map[string]any{
			"kind":                 "ask_input",
			"question":             finishMessage,
			"message":              finishMessage,
			"options":              intakeDecision.ClarificationOptions,
			"recommendedOptionKey": intakeDecision.ClarificationOptions[0].Key,
			"selectionMode":        "single",
			"responseLanguage":     request.ResponseLanguage,
		}))
	}
	agentKernel.appendGoalLifecycleEvent(blockedTaskRun, activeGoalFromIntakeOnly(taskRun.TaskRunID, request, intakeDecision, status))
	blockedTaskRun = persistTaskRunResult(agentKernel.taskRunService, blockedTaskRun, finishMessage)
	return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: finishMessage, ToolNames: toolNamesForEvent(request.ToolSet)}, nil
}

func (agentKernel *AgentKernel) createTaskRunForRequest(request AgentRequest) taskstate.TaskRun {
	return agentKernel.taskRunService.CreateTaskRunWithOrigin(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}

func (agentKernel *AgentKernel) appendTurnRouterCallRecords(taskRunID string, records []llmCallRecord) {
	for _, record := range records {
		agentKernel.taskRunService.AppendTaskEvent(taskRunID, "llm.call", marshalEventBody(record))
	}
}

func (agentKernel *AgentKernel) appendGoalLifecycleEvent(taskRun taskstate.TaskRun, activeGoal ActiveGoal) {
	if strings.TrimSpace(taskRun.TaskRunID) == "" {
		return
	}
	activeGoal.GoalID = firstNonEmptyString(activeGoal.GoalID, taskRun.TaskRunID)
	activeGoal.TaskRunID = firstNonEmptyString(activeGoal.TaskRunID, taskRun.TaskRunID)
	activeGoal.Status = activeGoalStatusForTaskStatus(taskRun.Status)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, activeGoalEventNameForTaskStatus(taskRun.Status), marshalEventBody(activeGoal))
}

type turnBudgetContext struct {
	parentContext         context.Context
	totalContext          context.Context
	workContext           context.Context
	cancelTotal           context.CancelFunc
	cancelWork            context.CancelFunc
	turnOptions           TurnOptions
	turnStartedAt         time.Time
	workDeadline          time.Time
	didClampAnchor        bool
	originalTurnStartedAt time.Time
}

const nonResumeAnchorStaleAllowance = 2 * time.Minute

func clampedTurnStartedAt(turnStartedAt time.Time, isRuntimeRestartResume bool, referenceNow time.Time) (resolvedTurnStartedAt time.Time, didClampAnchor bool, originalTurnStartedAt time.Time) {
	if isRuntimeRestartResume || turnStartedAt.IsZero() {
		return turnStartedAt, false, turnStartedAt
	}
	if referenceNow.Sub(turnStartedAt) <= nonResumeAnchorStaleAllowance {
		return turnStartedAt, false, turnStartedAt
	}
	return referenceNow, true, turnStartedAt
}

func newTurnBudgetContext(parentContext context.Context, turnStartedAt time.Time, isRuntimeRestartResume bool, referenceNow time.Time, turnOptions TurnOptions) turnBudgetContext {
	resolvedTurnStartedAt, didClampAnchor, originalTurnStartedAt := clampedTurnStartedAt(turnStartedAt, isRuntimeRestartResume, referenceNow)
	if resolvedTurnStartedAt.IsZero() || turnOptions.MaxElapsedSecond <= 0 {
		totalContext, cancelTotal := context.WithCancel(parentContext)
		workContext, cancelWork := context.WithCancel(totalContext)
		return turnBudgetContext{
			parentContext:         parentContext,
			totalContext:          totalContext,
			workContext:           workContext,
			cancelTotal:           cancelTotal,
			cancelWork:            cancelWork,
			turnOptions:           turnOptions,
			turnStartedAt:         resolvedTurnStartedAt,
			didClampAnchor:        didClampAnchor,
			originalTurnStartedAt: originalTurnStartedAt,
		}
	}
	totalDuration := time.Duration(turnOptions.MaxElapsedSecond) * time.Second
	workDeadline := resolvedTurnStartedAt.Add(workDurationWithinTotal(totalDuration))
	totalContext, cancelTotal := context.WithDeadline(parentContext, resolvedTurnStartedAt.Add(totalDuration))
	workContext, cancelWork := context.WithDeadline(totalContext, workDeadline)
	return turnBudgetContext{
		parentContext:         parentContext,
		totalContext:          totalContext,
		workContext:           workContext,
		cancelTotal:           cancelTotal,
		cancelWork:            cancelWork,
		turnOptions:           turnOptions,
		turnStartedAt:         resolvedTurnStartedAt,
		workDeadline:          workDeadline,
		didClampAnchor:        didClampAnchor,
		originalTurnStartedAt: originalTurnStartedAt,
	}
}

func (turnBudget turnBudgetContext) cancel() {
	turnBudget.cancelWork()
	turnBudget.cancelTotal()
}

func (turnBudget turnBudgetContext) callerContext() context.Context {
	return turnBudget.parentContext
}

func (turnBudget turnBudgetContext) didWorkExpire() bool {
	return turnBudget.parentContext.Err() == nil && errors.Is(turnBudget.workContext.Err(), context.DeadlineExceeded)
}

func (agentKernel *AgentKernel) completeIntakeIfElapsed(turnBudget turnBudgetContext, request AgentRequest, intakeDecision IntakeDecision, turnRoute TurnRoute, routerCallRecords []llmCallRecord) (AgentTurnResult, bool) {
	if !turnBudget.didWorkExpire() {
		return AgentTurnResult{}, false
	}
	result := agentKernel.completeIntakeElapsed(turnBudget, request, intakeDecision, routerCallRecords)
	result.TurnRoute = turnRoute
	return result, true
}

func (agentKernel *AgentKernel) completeIntakeElapsed(turnBudget turnBudgetContext, request AgentRequest, intakeDecision IntakeDecision, routerCallRecords []llmCallRecord) AgentTurnResult {
	taskRun := agentKernel.taskRunForIntakeLimit(request)
	agentKernel.appendTurnRouterCallRecords(taskRun.TaskRunID, routerCallRecords)
	if intakeDecision.TaskLevel != "" {
		agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.intake", marshalEventBody(intakeDecision))
	}
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.limit_stop", marshalEventBody(intakeLimitEventBody(turnBudget)))
	if turnBudget.didClampAnchor {
		agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.turn_anchor_clamped", marshalEventBody(turnAnchorClampedEventBody(turnBudget)))
	}
	blockedTaskRun, errorValue := agentKernel.taskRunService.PauseTaskRun(taskRun.TaskRunID, taskstate.TaskStatusBlocked, "max_elapsed")
	if errorValue != nil {
		taskRun.Status = taskstate.TaskStatusBlocked
		taskRun.FailureReason = "max_elapsed"
		blockedTaskRun = taskRun
	}
	failureNotice, noticeStatus := agentKernel.generateIntakeElapsedNotice(turnBudget.totalContext, request, taskRun.TaskRunID)
	replyStatus := limitReplyStatus{Source: noticeStatus.Source, Reason: noticeStatus.Reason, TextRecoveryError: noticeStatus.TextRecoveryError}
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.limit_reply", marshalEventBody(replyStatus))
	blockedTaskRun = persistTaskRunResult(agentKernel.taskRunService, blockedTaskRun, failureNotice.SendableMessage())
	agentKernel.appendGoalLifecycleEvent(blockedTaskRun, activeGoalFromIntakeOnly(taskRun.TaskRunID, request, intakeDecision, taskstate.TaskStatusBlocked))
	return AgentTurnResult{
		TaskRun:       blockedTaskRun,
		UserNotice:    failureNotice.SendableMessage(),
		FailureNotice: failureNotice,
		ToolNames:     toolNamesForEvent(request.ToolSet),
	}
}

func (agentKernel *AgentKernel) generateIntakeElapsedNotice(responseContext context.Context, request AgentRequest, taskRunID string) (FailureNotice, FailureNoticeGenerationStatus) {
	report := FailureReport{
		Phase:              "limit",
		StopReason:         "max_elapsed",
		SafeFailureSummary: elapsedLimitRawErrorSummary,
		RawError:           elapsedLimitRawErrorSummary,
		OriginalRequest:    request.Prompt,
		ResponseLanguage:   request.ResponseLanguage,
		DiagnosticEventID:  taskRunID + ":intake_limit",
	}
	return (FailureNoticeGenerator{LanguageModel: agentKernel.languageModel}).Generate(responseContext, report)
}

func (agentKernel *AgentKernel) taskRunForIntakeLimit(request AgentRequest) taskstate.TaskRun {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := agentKernel.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun
		}
	}
	return agentKernel.createTaskRunForRequest(request)
}

func intakeLimitEventBody(turnBudget turnBudgetContext) map[string]any {
	turnOptions := turnBudget.turnOptions
	body := map[string]any{
		"phase":              "intake",
		"taskLevel":          turnOptions.TaskLevel,
		"maxIterationCount":  turnOptions.MaxIterationCount,
		"maxElapsedSecond":   turnOptions.MaxElapsedSecond,
		"maxToolCallCount":   turnOptions.MaxToolCallCount,
		"usedIterationCount": 0,
		"usedToolCallCount":  0,
		"limitStopReason":    "max_elapsed",
		"anchorClamped":      turnBudget.didClampAnchor,
		"nowUnixMs":          time.Now().UnixMilli(),
	}
	if !turnBudget.turnStartedAt.IsZero() {
		body["turnStartedAtUnixMs"] = turnBudget.turnStartedAt.UnixMilli()
	}
	if !turnBudget.workDeadline.IsZero() {
		body["workDeadlineUnixMs"] = turnBudget.workDeadline.UnixMilli()
	}
	if turnBudget.didClampAnchor {
		body["originalTurnStartedAtUnixMs"] = turnBudget.originalTurnStartedAt.UnixMilli()
	}
	return body
}

func turnAnchorClampedEventBody(turnBudget turnBudgetContext) map[string]any {
	return map[string]any{
		"phase":                       "intake",
		"maxElapsedSecond":            turnBudget.turnOptions.MaxElapsedSecond,
		"originalTurnStartedAtUnixMs": turnBudget.originalTurnStartedAt.UnixMilli(),
		"clampedTurnStartedAtUnixMs":  turnBudget.turnStartedAt.UnixMilli(),
		"nowUnixMs":                   time.Now().UnixMilli(),
	}
}

func (agentKernel *AgentKernel) turnOptionsForIntakeDecision(intakeDecision IntakeDecision) TurnOptions {
	baseOptions := normalizeTurnOptions(agentKernel.turnOptions)
	taskLevelProfile := TaskLevelProfileForLevel(intakeDecision.TaskLevel)
	baseOptions.TaskLevel = taskLevelProfile.TaskLevel
	baseOptions.MaxIterationCount = taskLevelProfile.MaxIterationCount
	baseOptions.MaxToolCallCount = taskLevelProfile.MaxToolCallCount
	baseOptions.MaxElapsedSecond = int(taskLevelProfile.Duration.Seconds())
	return baseOptions
}

func artifactTaskLevelFloor(request AgentRequest, intakeDecision IntakeDecision) TaskLevel {
	if requestHasSitePrototypeEvidence(request) {
		return TaskLevelXHigh
	}
	if requestLooksLikeSlidesArtifactWork(request) || intakeDecisionRequestsVisualDeliverable(intakeDecision) {
		return TaskLevelXHigh
	}
	return TaskLevelXLow
}

func promoteArtifactTaskLevel(request AgentRequest, intakeDecision IntakeDecision) IntakeDecision {
	intakeDecision.TaskLevel = LargerTaskLevel(intakeDecision.TaskLevel, artifactTaskLevelFloor(request, intakeDecision))
	return intakeDecision
}

func promoteArtifactTaskLevelForRequest(request AgentRequest, intakeDecision IntakeDecision) IntakeDecision {
	if request.IsPrecomputedDecisionExact {
		return intakeDecision
	}
	return promoteArtifactTaskLevel(request, intakeDecision)
}

func (agentKernel *AgentKernel) taskLanguageModelForLevel(taskLevel TaskLevel) model.LanguageModelProvider {
	switch NormalizeTaskLevel(string(taskLevel)) {
	case TaskLevelMax:
		if agentKernel.maxTaskLanguageModel != nil {
			return agentKernel.maxTaskLanguageModel
		}
	case TaskLevelXHigh:
		if agentKernel.xHighTaskLanguageModel != nil {
			return agentKernel.xHighTaskLanguageModel
		}
	case TaskLevelHigh:
		if agentKernel.highTaskLanguageModel != nil {
			return agentKernel.highTaskLanguageModel
		}
	case TaskLevelMedium:
		if agentKernel.mediumTaskLanguageModel != nil {
			return agentKernel.mediumTaskLanguageModel
		}
	case TaskLevelLow:
		if agentKernel.lowTaskLanguageModel != nil {
			return agentKernel.lowTaskLanguageModel
		}
	case TaskLevelXLow:
		if agentKernel.xLowTaskLanguageModel != nil {
			return agentKernel.xLowTaskLanguageModel
		}
	}
	return agentKernel.languageModel
}

func (agentKernel *AgentKernel) classificationLanguageModel() model.LanguageModelProvider {
	if agentKernel.xLowTaskLanguageModel != nil {
		return agentKernel.xLowTaskLanguageModel
	}
	if agentKernel.intakeLanguageModel != nil {
		return agentKernel.intakeLanguageModel
	}
	return agentKernel.languageModel
}

func (agentKernel *AgentKernel) turnRouterLanguageModel() model.LanguageModelProvider {
	if agentKernel.intakeLanguageModel != nil {
		return agentKernel.intakeLanguageModel
	}
	return agentKernel.classificationLanguageModel()
}

func (agentKernel *AgentKernel) restoreEscalatedTaskLevelForContinuation(request AgentRequest, intakeDecision IntakeDecision) IntakeDecision {
	taskRunID := strings.TrimSpace(request.ExistingTaskRunID)
	if taskRunID == "" {
		return intakeDecision
	}
	restoredTaskLevel := highestEscalatedTaskLevel(agentKernel.taskRunService.ListTaskEvent(taskRunID))
	if restoredTaskLevel == "" {
		return intakeDecision
	}
	intakeDecision.TaskLevel = LargerTaskLevel(intakeDecision.TaskLevel, restoredTaskLevel)
	return intakeDecision
}

func restorePersistedToolSelection(request AgentRequest) AgentRequest {
	request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, request.ActiveGoal.SelectedToolNames...)
	request.PinnedSkillNames = appendUniqueStrings(request.PinnedSkillNames, request.ActiveGoal.SelectedSkillNames...)
	return request
}

func requestHasSitePrototypeEvidence(request AgentRequest) bool {
	return contractRequiresToolNamespace(request.ToolSet, request.ActiveGoal.OutcomeContract, "site")
}

type budgetEscalatedEventBody struct {
	PreviousTaskLevel  TaskLevel `json:"previousTaskLevel,omitempty"`
	NewTaskLevel       TaskLevel `json:"newTaskLevel"`
	UsedIterationCount int       `json:"usedIterationCount,omitempty"`
	UsedToolCallCount  int       `json:"usedToolCallCount,omitempty"`
	QualifyingEventIDs []string  `json:"qualifyingEventIDs,omitempty"`
}

type modelEscalatedEventBody struct {
	PreviousTaskLevel TaskLevel `json:"previousTaskLevel,omitempty"`
	NewTaskLevel      TaskLevel `json:"newTaskLevel"`
}

func highestEscalatedTaskLevel(taskEvents []taskstate.TaskEvent) TaskLevel {
	highestTaskLevel := TaskLevel("")
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != "agent.budget_escalated" {
			continue
		}
		var eventBody budgetEscalatedEventBody
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &eventBody); errorValue != nil {
			continue
		}
		normalizedTaskLevel := NormalizeTaskLevel(string(eventBody.NewTaskLevel))
		if normalizedTaskLevel == "" {
			continue
		}
		highestTaskLevel = LargerTaskLevel(highestTaskLevel, normalizedTaskLevel)
	}
	return highestTaskLevel
}

func routedTurnDecision(request AgentRequest) (TurnDecision, error) {
	if request.PrecomputedTurnDecision == nil {
		return TurnDecision{}, errors.New("turn request carries no routing decision; the host routes before handing a turn to the harness")
	}
	return *request.PrecomputedTurnDecision, nil
}
