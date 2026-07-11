package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

type AgentKernel struct {
	planCompiler            PlanCompiler
	subagentDispatcher      SubagentDispatcher
	taskRunService          *task.TaskRunService
	taskStepService         *task.TaskStepService
	taskArtifactService     *task.TaskArtifactService
	languageModel           llm.LanguageModelProvider
	maxTaskLanguageModel    llm.LanguageModelProvider
	xHighTaskLanguageModel  llm.LanguageModelProvider
	highTaskLanguageModel   llm.LanguageModelProvider
	mediumTaskLanguageModel llm.LanguageModelProvider
	xLowTaskLanguageModel   llm.LanguageModelProvider
	codingTaskLanguageModel llm.LanguageModelProvider
	intakeLanguageModel     llm.LanguageModelProvider
	turnOptions             TurnOptions
	intakeOptions           IntakeOptions
	instructionPrompt       string
	instructionSources      []InstructionSource
	instructionLoader       func() InstructionBundle
	skillRetriever          SkillRetriever
	companyProvider         func() CompanyContext
}

func NewAgentKernel(taskRunService *task.TaskRunService, taskStepService *task.TaskStepService) *AgentKernel {
	return &AgentKernel{
		planCompiler:        PlanCompiler{},
		subagentDispatcher:  SubagentDispatcher{},
		taskRunService:      taskRunService,
		taskStepService:     taskStepService,
		taskArtifactService: task.NewTaskArtifactService(),
	}
}

func (agentKernel *AgentKernel) UseLanguageModelProvider(languageModel llm.LanguageModelProvider) {
	agentKernel.languageModel = languageModel
}

func (agentKernel *AgentKernel) UseTaskTierLanguageModels(maxTaskLanguageModel llm.LanguageModelProvider, xHighTaskLanguageModel llm.LanguageModelProvider, highTaskLanguageModel llm.LanguageModelProvider, mediumTaskLanguageModel llm.LanguageModelProvider, xLowTaskLanguageModel llm.LanguageModelProvider, codingTaskLanguageModel llm.LanguageModelProvider) {
	agentKernel.maxTaskLanguageModel = maxTaskLanguageModel
	agentKernel.xHighTaskLanguageModel = xHighTaskLanguageModel
	agentKernel.highTaskLanguageModel = highTaskLanguageModel
	agentKernel.mediumTaskLanguageModel = mediumTaskLanguageModel
	agentKernel.xLowTaskLanguageModel = xLowTaskLanguageModel
	agentKernel.codingTaskLanguageModel = codingTaskLanguageModel
}

func (agentKernel *AgentKernel) UseTaskArtifactService(taskArtifactService *task.TaskArtifactService) {
	if taskArtifactService != nil {
		agentKernel.taskArtifactService = taskArtifactService
	}
}

func (agentKernel *AgentKernel) UseTurnOptions(turnOptions TurnOptions) {
	agentKernel.turnOptions = normalizeTurnOptions(turnOptions)
}

func (agentKernel *AgentKernel) UseIntakeLanguageModelProvider(languageModel llm.LanguageModelProvider) {
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

func (agentKernel *AgentKernel) HandleInboundMessage(requesterPersonID string, originConversationID string, prompt string) (task.TaskRun, error) {
	return agentKernel.RunTask(requesterPersonID, originConversationID, prompt)
}

func (agentKernel *AgentKernel) AppendTaskEvent(taskRunID string, name string, body string) {
	agentKernel.taskRunService.AppendTaskEvent(taskRunID, name, body)
}

func (agentKernel *AgentKernel) ListTaskRunByPersonID(personID string) []task.TaskRun {
	return agentKernel.taskRunService.ListTaskRunByPersonID(personID)
}

func (agentKernel *AgentKernel) FindTaskRun(taskRunID string) (task.TaskRun, bool) {
	return agentKernel.taskRunService.FindTaskRun(taskRunID)
}

func (agentKernel *AgentKernel) ListTaskEvent(taskRunID string) []task.TaskEvent {
	return agentKernel.taskRunService.ListTaskEvent(taskRunID)
}

func (agentKernel *AgentKernel) CompleteTask(taskRunID string, result string) (task.TaskRun, error) {
	return agentKernel.taskRunService.CompleteTaskRun(taskRunID, result)
}

func (agentKernel *AgentKernel) CancelTask(taskRunID string, requesterPersonID string, reason string) (task.TaskRun, error) {
	return agentKernel.taskRunService.CancelTaskRunWithReason(taskRunID, requesterPersonID, reason)
}

func (agentKernel *AgentKernel) CancelActiveTasks(request task.TaskRunCancelRequest) []task.TaskRun {
	return agentKernel.taskRunService.CancelActiveTaskRuns(request)
}

func (agentKernel *AgentKernel) IsTaskRunActuallyRunning(taskRun task.TaskRun) bool {
	return agentKernel.taskRunService.IsTaskRunActuallyRunning(taskRun)
}

func (agentKernel *AgentKernel) InterruptInactiveTaskRun(taskRunID string, reason string) (task.TaskRun, bool) {
	return agentKernel.taskRunService.InterruptInactiveTaskRun(taskRunID, reason)
}

func (agentKernel *AgentKernel) RunTurn(responseContext context.Context, request AgentTurnRequest) (AgentTurnResult, error) {
	return agentKernel.RunAgentRequest(responseContext, AgentRequest{
		RequesterPersonID:       request.RequesterPersonID,
		RequesterName:           request.RequesterName,
		RequesterCallingName:    request.RequesterCallingName,
		RequesterHandle:         request.RequesterHandle,
		RequesterCircles:        append([]string{}, request.RequesterCircles...),
		SourceReference:         request.SourceReference,
		IsApprovalContinuation:  request.IsApprovalContinuation,
		IsRuntimeRestartResume:  request.IsRuntimeRestartResume,
		ExistingTaskRunID:       request.ExistingTaskRunID,
		OriginReplyTargetID:     request.OriginReplyTargetID,
		OriginIsThread:          request.OriginIsThread,
		ProfileName:             request.ProfileName,
		ConversationID:          request.ConversationID,
		Prompt:                  request.Prompt,
		InputParts:              append([]AgentPart{}, request.InputParts...),
		ResponseLanguage:        request.ResponseLanguage,
		VisibleContext:          request.VisibleContext,
		MemoryFacts:             request.MemoryFacts,
		ToolSet:                 request.ToolSet,
		PinnedToolNames:         append([]string{}, request.PinnedToolNames...),
		PinnedSkillNames:        append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:       request.WorkspaceRootPath,
		ActivePaths:             request.ActivePaths,
		ActiveGoal:              request.ActiveGoal,
		PriorTask:               request.PriorTask,
		ScheduledRun:            request.ScheduledRun,
		PrecomputedTurnDecision: request.PrecomputedTurnDecision,
		AmbientDuty:             request.AmbientDuty,
		TaskLevel:               request.TaskLevel,
		TurnStartedAt:           request.TurnStartedAt,
		CheckpointSender:        request.CheckpointSender,
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
		taskRun.Status = task.TaskStatusFailed
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
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.failure_reply", marshalEventBody(noticeStatus))
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.failure_report", marshalEventBody(failureReportEventBody(phase, launchFailureReport, noticeStatus)))
	failedTaskRun = persistTaskRunResult(agentKernel.taskRunService, failedTaskRun, failureNotice.SendableMessage())
	return AgentTurnResult{TaskRun: failedTaskRun, UserNotice: failedTaskRun.Result, FailureNotice: failureNotice, ToolNames: toolNamesForEvent(request.ToolSet)}
}

func (agentKernel *AgentKernel) taskRunForLaunchFailure(request AgentTurnRequest) (task.TaskRun, error) {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := agentKernel.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun, nil
		}
	}
	return agentKernel.taskRunService.CreateTaskRunWithOriginAndError(request.RequesterPersonID, task.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}

func (agentKernel *AgentKernel) RouteTurn(responseContext context.Context, request AgentRequest) TurnDecision {
	return NewTurnRouter(agentKernel.classificationLanguageModel(), agentKernel.intakeOptions).Plan(responseContext, request)
}

func (agentKernel *AgentKernel) RunAgentRequest(responseContext context.Context, request AgentRequest) (AgentTurnResult, error) {
	if request.TurnStartedAt.IsZero() {
		request.TurnStartedAt = time.Now().Add(-2 * time.Second)
	}
	request.ResponseLanguage = ResolveResponseLanguage(request.ResponseLanguage, request.VisibleContext.ResponseLanguage)
	siteNormalizationReports := []siteRequirementNormalizationReport{}
	var activeGoalSiteReport siteRequirementNormalizationReport
	request.ActiveGoal, activeGoalSiteReport = normalizeActiveGoalSiteRequirement(request.ActiveGoal, request.Prompt, request.IsApprovalContinuation || request.IsRuntimeRestartResume)
	siteNormalizationReports = appendSiteRequirementNormalizationReport(siteNormalizationReports, activeGoalSiteReport)
	baseInstructionBundle := agentKernel.currentInstructionBundle()
	instructionBundle := baseInstructionBundle
	instructionBundle = selectInstructionBundleForRequestWithRetrieverAndRouter(
		responseContext,
		instructionBundle,
		request,
		agentKernel.skillRetriever,
		NewSkillSearchQueryRouter(agentKernel.classificationLanguageModel()),
	)
	instructionBundle = instructionBundleWithPinnedSkills(instructionBundle, request)
	turnToolSet := toolSetForAgentTurn(request.ToolSet, instructionBundle)
	intakeRequest := request
	intakeRequest.ToolSet = turnToolSet
	turnRouter := NewTurnRouter(agentKernel.classificationLanguageModel(), agentKernel.intakeOptions)
	turnDecision := turnRouter.Plan(responseContext, intakeRequest)
	intakeDecision := turnDecision.IntakeDecision()
	intakeDecision = promoteIntakeDecisionForSelectedSkills(intakeDecision, instructionBundle, agentKernel.intakeOptions)
	intakeDecision = (TaskRecoveryPlanner{}).Plan(intakeRequest, intakeDecision)
	intakeDecision = promoteArtifactTaskLevel(intakeRequest, intakeDecision)
	siteNormalizationReports = appendSiteRequirementNormalizationReport(siteNormalizationReports, intakeDecision.siteNormalizationReport)
	request.ResponseLanguage = ResolveResponseLanguage(intakeDecision.ResponseLanguage, request.ResponseLanguage)
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
	request.PinnedToolNames = appendUniqueStrings(append([]string{}, request.PinnedToolNames...), intakeDecision.InitialToolNames...)
	intakeRequest.PinnedToolNames = request.PinnedToolNames
	instructionBundle, intakeDecision = agentKernel.selectInstructionBundleForResolvedRequest(responseContext, baseInstructionBundle, request, intakeDecision)
	turnToolSet = toolSetForAgentTurn(request.ToolSet, instructionBundle)
	intakeDecision = promoteIntakeDecisionForSelectedSkills(intakeDecision, instructionBundle, agentKernel.intakeOptions)
	if turnDecision.Route == TurnRouteConsume && intakeDecision.Classification == IntakeClassificationBoundedTask {
		turnDecision.Route = TurnRouteStartTask
	}
	if turnDecision.Route == TurnRouteConsume {
		result, errorValue := agentKernel.completeConsumedRequest(intakeRequest, turnDecision)
		agentKernel.appendSiteRequirementNormalizationReports(result.TaskRun.TaskRunID, siteNormalizationReports)
		return result, errorValue
	}
	if intakeDecision.Classification == IntakeClassificationNeedsConfirmation && len(intakeDecision.ClarificationOptions) >= 2 {
		result, errorValue := agentKernel.completeIntakeOnlyRequest(responseContext, intakeRequest, intakeDecision, task.TaskStatusWaitingUserInput)
		result.TurnRoute = turnDecision.Route
		agentKernel.appendSiteRequirementNormalizationReports(result.TaskRun.TaskRunID, siteNormalizationReports)
		return result, errorValue
	}
	if intakeDecision.Classification == IntakeClassificationNeedsConfirmation {
		intakeDecision.Classification = IntakeClassificationBoundedTask
		if intakeDecision.TaskShape == "" || intakeDecision.TaskShape == TaskShapeImmediateReply || intakeDecision.TaskShape == TaskShapeApprovalGatedTask {
			intakeDecision.TaskShape = TaskShapeMaintenanceTask
		}
	}
	if intakeDecision.Classification == IntakeClassificationUnsupported {
		result, errorValue := agentKernel.completeIntakeOnlyRequest(responseContext, intakeRequest, intakeDecision, task.TaskStatusBlocked)
		result.TurnRoute = turnDecision.Route
		agentKernel.appendSiteRequirementNormalizationReports(result.TaskRun.TaskRunID, siteNormalizationReports)
		return result, errorValue
	}

	requiredAttachmentSuffixes := attachmentSuffixesForRequestedOutputFormats(intakeDecision.RequestedOutputFormats)
	evidenceHints := selectedEvidenceHintTools(instructionBundle)
	confirmationEvidenceHints := confirmationEvidenceHintsForRequest(request, intakeDecision, evidenceHints)
	confirmationResult, isBlocked, executionPlan, hasExecutionPlan, errorValue := agentKernel.applyConfirmationGate(responseContext, request, intakeDecision, confirmationEvidenceHints)
	if isBlocked || errorValue != nil {
		confirmationResult.TurnRoute = turnDecision.Route
		return confirmationResult, errorValue
	}
	outcomeContract := outcomeContractForRequest(request, intakeDecision, instructionBundle, executionPlan, hasExecutionPlan, requiredAttachmentSuffixes)
	isDeterministicResume := request.IsApprovalContinuation || request.IsRuntimeRestartResume
	evidenceValidationReport := validateRequiredEvidenceTools(turnToolSet, outcomeContract.RequiredEvidenceTools)
	prunedEvidenceReport := requiredEvidenceValidationReport{}
	if evidenceValidationReport.HasInvalidEvidence() {
		outcomeContract.RequiredEvidenceTools = requiredEvidenceToolsWithout(outcomeContract.RequiredEvidenceTools, evidenceValidationReport.InvalidEvidence)
		prunedEvidenceReport = evidenceValidationReport
		prunedEvidenceReport.Reason = "invalid required evidence pruned; the task keeps executing and real permission is enforced at execution"
	}
	var requiredEvidenceReask requiredEvidenceReaskReport
	if missingEvidenceReport := missingRequiredEvidenceReport(intakeDecision, outcomeContract, turnToolSet); !isDeterministicResume && strings.TrimSpace(missingEvidenceReport.Reason) != "" {
		intakeDecision, outcomeContract, requiredEvidenceReask = agentKernel.reaskMissingRequiredEvidenceOnce(responseContext, request, intakeRequest, intakeDecision, outcomeContract, instructionBundle, executionPlan, hasExecutionPlan, requiredAttachmentSuffixes, turnToolSet)
	}
	requiredEvidenceTools := outcomeContract.RequiredEvidenceTools
	requiredAttachmentSuffixes = outcomeContract.RequiredAttachmentSuffixes

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
		RequiredEvidenceTools:      requiredEvidenceTools,
		RequiredAttachmentSuffixes: requiredAttachmentSuffixes,
		OutcomeContract:            outcomeContract,
		ActiveGoal:                 activeGoalForTurn(request, outcomeContract, executionPlan, hasExecutionPlan),
		PriorTask:                  request.PriorTask,
		ScheduledRun:               request.ScheduledRun,
		QualityAcceptanceGuidance:  selectedQualityAcceptanceGuidance(instructionBundle),
		AmbientDuty:                request.AmbientDuty,
		TaskShape:                  intakeDecision.TaskShape,
		TaskLevel:                  intakeDecision.TaskLevel,
		EstimatedMinutes:           intakeDecision.EstimatedMinutes,
		LaunchNotice:               intakeDecision.LaunchNotice,
		TurnStartedAt:              request.TurnStartedAt,
		CheckpointSender:           request.CheckpointSender,
	}
	turnOptions := agentKernel.turnOptionsForIntakeDecision(intakeDecision)

	agentTurnRunner := NewAgentTurnRunnerWithRecoveryModel(
		agentKernel.taskRunService,
		agentKernel.taskStepService,
		agentKernel.taskArtifactService,
		agentKernel.taskLanguageModelForLevel(intakeDecision.TaskLevel),
		agentKernel.languageModel,
		turnOptions,
	)
	agentTurnRunner.UseTaskLanguageModelResolver(agentKernel.taskLanguageModelForLevel)
	result, errorValue := agentTurnRunner.RunTurn(responseContext, turnRequest)
	result.TurnRoute = turnDecision.Route
	result.ToolNames = toolNamesForEvent(turnRequest.ToolSet)
	if result.TaskRun.TaskRunID != "" {
		agentKernel.AppendTaskEvent(result.TaskRun.TaskRunID, "agent.intake", marshalEventBody(intakeDecision))
		if prunedEvidenceReport.HasInvalidEvidence() {
			agentKernel.AppendTaskEvent(result.TaskRun.TaskRunID, requiredEvidenceInvalidEventName, marshalEventBody(prunedEvidenceReport))
		}
		if requiredEvidenceReask.WasAttempted {
			agentKernel.AppendTaskEvent(result.TaskRun.TaskRunID, requiredEvidenceReaskEventName, marshalEventBody(requiredEvidenceReask))
		}
		agentKernel.appendSiteRequirementNormalizationReports(result.TaskRun.TaskRunID, siteNormalizationReports)
		agentKernel.appendGoalLifecycleEvent(result.TaskRun, turnRequest.ActiveGoal)
	}
	return result, errorValue
}

func (agentKernel *AgentKernel) reaskMissingRequiredEvidenceOnce(responseContext context.Context, request AgentRequest, intakeRequest AgentRequest, intakeDecision IntakeDecision, outcomeContract OutcomeContract, instructionBundle InstructionBundle, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredAttachmentSuffixes []string, turnToolSet *ToolSet) (IntakeDecision, OutcomeContract, requiredEvidenceReaskReport) {
	turnRouter := NewTurnRouter(agentKernel.classificationLanguageModel(), agentKernel.intakeOptions)
	reaskDecision, errorValue := turnRouter.ReaskRequiredEvidence(responseContext, intakeRequest)
	if errorValue != nil {
		return intakeDecision, outcomeContract, requiredEvidenceReaskReport{WasAttempted: true, Reason: errorValue.Error()}
	}
	reaskIntakeDecision := reaskDecision.IntakeDecision()
	evidenceValidationReport := validateRequiredEvidenceTools(turnToolSet, reaskIntakeDecision.RequiredEvidenceTools)
	if len(reaskIntakeDecision.RequiredEvidenceTools) == 0 || evidenceValidationReport.HasInvalidEvidence() {
		return intakeDecision, outcomeContract, requiredEvidenceReaskReport{WasAttempted: true, Reason: "re-ask still returned no valid required evidence"}
	}
	intakeDecision.RequiredEvidenceTools = appendUniqueStrings(intakeDecision.RequiredEvidenceTools, reaskIntakeDecision.RequiredEvidenceTools...)
	rebuiltOutcomeContract := outcomeContractForRequest(request, intakeDecision, instructionBundle, executionPlan, hasExecutionPlan, requiredAttachmentSuffixes)
	return intakeDecision, rebuiltOutcomeContract, requiredEvidenceReaskReport{
		WasAttempted:       true,
		DidRecoverEvidence: true,
		RecoveredEvidence:  reaskIntakeDecision.RequiredEvidenceTools,
	}
}

func (agentKernel *AgentKernel) selectInstructionBundleForResolvedRequest(ctx context.Context, baseInstructionBundle InstructionBundle, request AgentRequest, intakeDecision IntakeDecision) (InstructionBundle, IntakeDecision) {
	selectionRequest := request
	selectionRequest.ActiveGoal.OutcomeContract.RequiredAttachmentSuffixes = appendUniqueStrings(
		selectionRequest.ActiveGoal.OutcomeContract.RequiredAttachmentSuffixes,
		attachmentSuffixesForRequestedOutputFormats(intakeDecision.RequestedOutputFormats)...,
	)
	if len(selectionRequest.ActiveGoal.OutcomeContract.RequiredAttachmentSuffixes) > 0 {
		selectionRequest.ActiveGoal.OutcomeContract.RequiredEvidenceTools = appendUniqueStrings(selectionRequest.ActiveGoal.OutcomeContract.RequiredEvidenceTools, FileDeliverToolName)
	}
	requiredCapabilityEvidence := appendUniqueStrings(
		intakeDecision.RequiredEvidenceTools,
		selectionRequest.ActiveGoal.OutcomeContract.RequiredEvidenceTools...,
	)
	coveringSkillNames, undocumentedOperationNames := requiredEvidenceSkillCoverage(request.ToolSet, baseInstructionBundle.Skills, requiredCapabilityEvidence)
	selectionRequest.PinnedSkillNames = appendUniqueStrings(selectionRequest.PinnedSkillNames, coveringSkillNames...)
	intakeDecision.RequiredEvidenceTools = requiredEvidenceToolsWithout(intakeDecision.RequiredEvidenceTools, undocumentedOperationNames)
	selectionRequest.ActiveGoal.OutcomeContract.RequiredEvidenceTools = requiredEvidenceToolsWithout(selectionRequest.ActiveGoal.OutcomeContract.RequiredEvidenceTools, undocumentedOperationNames)
	instructionBundle := selectInstructionBundleForRequestWithRetrieverAndRouter(
		ctx,
		baseInstructionBundle,
		selectionRequest,
		agentKernel.skillRetriever,
		NewSkillSearchQueryRouter(agentKernel.classificationLanguageModel()),
	)
	return instructionBundleWithPinnedSkills(instructionBundle, selectionRequest), intakeDecision
}

func requiredEvidenceSkillCoverage(toolSet *ToolSet, skillInstructions []SkillInstruction, requiredEvidenceTools []string) ([]string, []string) {
	coveringSkillNames := []string{}
	undocumentedOperationNames := []string{}
	for _, toolName := range requiredEvidenceTools {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" {
			continue
		}
		toolKind, isValid := requiredEvidenceToolKind(toolSet, trimmedToolName)
		if !isValid || toolKind == requiredEvidenceToolKindNativeTool {
			continue
		}
		skillNames := skillNamesAllowingOperation(skillInstructions, trimmedToolName)
		if len(skillNames) == 0 {
			undocumentedOperationNames = append(undocumentedOperationNames, trimmedToolName)
			continue
		}
		coveringSkillNames = appendUniqueStrings(coveringSkillNames, skillNames...)
	}
	return coveringSkillNames, undocumentedOperationNames
}

func skillNamesAllowingOperation(skillInstructions []SkillInstruction, operationName string) []string {
	skillNames := []string{}
	for _, skillInstruction := range skillInstructions {
		if skillAllowsOperation(skillInstruction, operationName) {
			skillNames = appendUniqueStrings(skillNames, skillInstruction.Name)
		}
	}
	return skillNames
}

func skillAllowsOperation(skillInstruction SkillInstruction, operationName string) bool {
	return skillHasToolName(skillInstruction, operationName)
}

func (agentKernel *AgentKernel) completeConsumedRequest(request AgentRequest, decision TurnDecision) (AgentTurnResult, error) {
	taskRun := agentKernel.createTaskRunForRequest(request)
	reactionEmojiName := NormalizeReactionEmojiName(decision.ReactionEmojiName)
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.intake", marshalEventBody(decision.IntakeDecision()))
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.consumed", marshalEventBody(map[string]string{
		"route":             string(decision.Route),
		"reason":            strings.TrimSpace(decision.Reason),
		"reactionEmojiName": reactionEmojiName,
	}))
	completedTaskRun, errorValue := agentKernel.taskRunService.CompleteTaskRun(taskRun.TaskRunID, "consumed")
	if errorValue != nil {
		return AgentTurnResult{}, errorValue
	}
	return AgentTurnResult{TaskRun: completedTaskRun, TurnRoute: TurnRouteConsume, ReactionEmojiName: reactionEmojiName, ReplySuppressed: true, ToolNames: toolNamesForEvent(request.ToolSet)}, nil
}

func (agentKernel *AgentKernel) applyConfirmationGate(responseContext context.Context, request AgentRequest, intakeDecision IntakeDecision, evidenceHints []string) (AgentTurnResult, bool, ExecutionPlan, bool, error) {
	if request.IsApprovalContinuation || strings.TrimSpace(request.ExistingTaskRunID) != "" {
		return AgentTurnResult{}, false, ExecutionPlan{}, false, nil
	}
	if !shouldBuildExecutionPlanForConfirmation(request, intakeDecision, evidenceHints) {
		return AgentTurnResult{}, false, ExecutionPlan{}, false, nil
	}
	executionPlan, errorValue := agentKernel.BuildExecutionPlan(responseContext, request, evidenceHints)
	if errorValue != nil {
		return AgentTurnResult{}, false, ExecutionPlan{}, false, errorValue
	}
	decision := EvaluateConfirmationPolicy(executionPlan)
	if !decision.RequiresConfirmation && !decision.RequiresClarification {
		return AgentTurnResult{}, false, executionPlan, true, nil
	}

	taskRun := agentKernel.createTaskRunForRequest(request)
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "confirmation.plan_created", marshalEventBody(executionPlan))
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "confirmation.policy_decision", marshalEventBody(decision))

	if decision.RequiresClarification {
		reply, errorValue := agentKernel.GenerateClarificationMessage(responseContext, request, executionPlan, decision)
		if errorValue != nil {
			return AgentTurnResult{}, false, ExecutionPlan{}, false, errorValue
		}
		waitingTaskRun, errorValue := agentKernel.taskRunService.PauseTaskRun(taskRun.TaskRunID, task.TaskStatusWaitingUserInput, reply)
		if errorValue != nil {
			return AgentTurnResult{}, false, ExecutionPlan{}, false, errorValue
		}
		waitingGoal := activeGoalFromExecutionPlan(taskRun.TaskRunID, executionPlan, ActiveGoalStatusWaitingUserInput, evidenceHints, nil)
		agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.goal.created", marshalEventBody(waitingGoal))
		agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.goal.waiting_user_input", marshalEventBody(waitingGoal))
		agentKernel.AppendTaskEvent(taskRun.TaskRunID, "confirmation.clarification_requested", reply)
		agentKernel.AppendTaskEvent(taskRun.TaskRunID, "ask.requested", marshalEventBody(map[string]string{
			"kind":             "input",
			"question":         reply,
			"message":          reply,
			"responseLanguage": request.ResponseLanguage,
		}))
		return AgentTurnResult{TaskRun: waitingTaskRun, UserNotice: reply, ToolNames: toolNamesForEvent(request.ToolSet)}, true, ExecutionPlan{}, false, nil
	}

	reply, errorValue := agentKernel.GenerateConfirmationMessage(responseContext, request, executionPlan, decision)
	if errorValue != nil {
		return AgentTurnResult{}, false, ExecutionPlan{}, false, errorValue
	}
	waitingTaskRun, errorValue := agentKernel.taskRunService.PauseTaskRun(taskRun.TaskRunID, task.TaskStatusWaitingApproval, reply)
	if errorValue != nil {
		return AgentTurnResult{}, false, ExecutionPlan{}, false, errorValue
	}
	approvalGoal := activeGoalFromExecutionPlan(taskRun.TaskRunID, executionPlan, ActiveGoalStatusWaitingApproval, evidenceHints, nil)
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.goal.created", marshalEventBody(approvalGoal))
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.goal.waiting_approval", marshalEventBody(approvalGoal))
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "confirmation.requested", marshalEventBody(map[string]string{
		"message":                 reply,
		"reason":                  decision.Reason,
		"responseLanguage":        request.ResponseLanguage,
		"continuationInstruction": executionPlan.ContinuationInstruction,
	}))
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "ask.requested", marshalEventBody(map[string]string{
		"kind":             "confirm",
		"message":          reply,
		"responseLanguage": request.ResponseLanguage,
	}))
	return AgentTurnResult{TaskRun: waitingTaskRun, UserNotice: reply, ToolNames: toolNamesForEvent(request.ToolSet)}, true, ExecutionPlan{}, false, nil
}

func (agentKernel *AgentKernel) RunTask(requesterPersonID string, originConversationID string, prompt string) (task.TaskRun, error) {
	return agentKernel.RunTaskWithOrigin(requesterPersonID, task.TaskRunOrigin{ConversationID: originConversationID}, prompt)
}

func (agentKernel *AgentKernel) RunTaskWithOrigin(requesterPersonID string, origin task.TaskRunOrigin, prompt string) (task.TaskRun, error) {
	taskRun := agentKernel.taskRunService.CreateTaskRunWithOrigin(requesterPersonID, origin, prompt)
	taskPlan, errorValue := agentKernel.planCompiler.CompilePlan(prompt)
	if errorValue != nil {
		return task.TaskRun{}, errorValue
	}

	for _, taskPlanStep := range taskPlan.TaskSteps {
		agentKernel.taskStepService.AddTaskStep(task.TaskStep{
			TaskStepID:               taskRun.TaskRunID + ":" + taskPlanStep.Name,
			TaskRunID:                taskRun.TaskRunID,
			AssignedAgentProfileName: taskPlanStep.AssignedAgentProfileName,
			Instruction:              taskPlanStep.Instruction,
			Status:                   task.TaskStatusPlanned,
		})
	}

	return agentKernel.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "planner")
}

func (agentKernel *AgentKernel) ResumeTask(taskRunID string) (task.TaskRun, error) {
	return agentKernel.taskRunService.ResumeTaskRun(taskRunID)
}

func (agentKernel *AgentKernel) completeIntakeOnlyRequest(responseContext context.Context, request AgentRequest, intakeDecision IntakeDecision, status task.TaskStatus) (AgentTurnResult, error) {
	taskRun := agentKernel.createTaskRunForRequest(request)
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.intake", marshalEventBody(intakeDecision))
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
	if status == task.TaskStatusWaitingUserInput && intakeDecision.Classification == IntakeClassificationNeedsConfirmation && len(intakeDecision.ClarificationOptions) >= 2 {
		agentKernel.AppendTaskEvent(taskRun.TaskRunID, "ask.requested", marshalEventBody(map[string]any{
			"kind":                 "choice_single",
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

func (agentKernel *AgentKernel) createTaskRunForRequest(request AgentRequest) task.TaskRun {
	return agentKernel.taskRunService.CreateTaskRunWithOrigin(request.RequesterPersonID, task.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}

func (agentKernel *AgentKernel) appendGoalLifecycleEvent(taskRun task.TaskRun, activeGoal ActiveGoal) {
	if strings.TrimSpace(taskRun.TaskRunID) == "" {
		return
	}
	activeGoal.GoalID = firstNonEmptyString(activeGoal.GoalID, taskRun.TaskRunID)
	activeGoal.TaskRunID = firstNonEmptyString(activeGoal.TaskRunID, taskRun.TaskRunID)
	activeGoal.Status = activeGoalStatusForTaskStatus(taskRun.Status)
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, activeGoalEventNameForTaskStatus(taskRun.Status), marshalEventBody(activeGoal))
}

func appendSiteRequirementNormalizationReport(reports []siteRequirementNormalizationReport, report siteRequirementNormalizationReport) []siteRequirementNormalizationReport {
	if !report.HasDrops() {
		return reports
	}
	return append(reports, report)
}

func (agentKernel *AgentKernel) appendSiteRequirementNormalizationReports(taskRunID string, reports []siteRequirementNormalizationReport) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	for _, report := range reports {
		if report.HasDrops() {
			agentKernel.AppendTaskEvent(taskRunID, siteRequirementNormalizationEventName, marshalEventBody(report))
		}
	}
}

func (agentKernel *AgentKernel) turnOptionsForIntakeDecision(intakeDecision IntakeDecision) TurnOptions {
	baseOptions := normalizeTurnOptions(agentKernel.turnOptions)
	taskLevelProfile := TaskLevelProfileForLevel(intakeDecision.TaskLevel)
	baseOptions.TaskLevel = taskLevelProfile.TaskLevel
	baseOptions.MaxIterationCount = taskLevelProfile.MaxIterationCount
	baseOptions.MaxToolCallCount = taskLevelProfile.MaxToolCallCount
	baseOptions.MaxElapsedSecond = timeBudgetSecondsForIntake(taskLevelProfile, intakeDecision.EstimatedMinutes)
	return baseOptions
}

// timeBudgetSecondsForIntake uses the shorter of the tier's ceiling and 1.5x the
// intake's own minute estimate, so a task the model expects to finish quickly
// gets a tight budget (and earlier budget pressure) even on a high tier, while
// the tier still caps how long anything can run.
func timeBudgetSecondsForIntake(taskLevelProfile TaskLevelProfile, estimatedMinutes int) int {
	tierSeconds := int(taskLevelProfile.Duration.Seconds())
	if estimatedMinutes <= 0 {
		return tierSeconds
	}
	estimateSeconds := estimatedMinutes * 90
	if estimateSeconds < tierSeconds {
		return estimateSeconds
	}
	return tierSeconds
}

func artifactTaskLevelFloor(request AgentRequest, intakeDecision IntakeDecision) TaskLevel {
	if intakeDecisionHasSitePrototypeEvidence(request, intakeDecision) {
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

func (agentKernel *AgentKernel) taskLanguageModelForLevel(taskLevel TaskLevel) llm.LanguageModelProvider {
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
	case TaskLevelXLow:
		if agentKernel.xLowTaskLanguageModel != nil {
			return agentKernel.xLowTaskLanguageModel
		}
	}
	return agentKernel.languageModel
}

func (agentKernel *AgentKernel) classificationLanguageModel() llm.LanguageModelProvider {
	if agentKernel.xLowTaskLanguageModel != nil {
		return agentKernel.xLowTaskLanguageModel
	}
	if agentKernel.intakeLanguageModel != nil {
		return agentKernel.intakeLanguageModel
	}
	return agentKernel.languageModel
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

func intakeDecisionHasSitePrototypeEvidence(request AgentRequest, intakeDecision IntakeDecision) bool {
	if strings.TrimSpace(intakeDecision.SiteRequestEvidence) != "" {
		return true
	}
	if requiredEvidenceHasPrefix(intakeDecision.RequiredEvidenceTools, "site.") {
		return true
	}
	return activeGoalRequiresToolPrefix(request.ActiveGoal, "site.")
}

type budgetEscalatedEventBody struct {
	PreviousTaskLevel  TaskLevel `json:"previousTaskLevel,omitempty"`
	NewTaskLevel       TaskLevel `json:"newTaskLevel"`
	UsedIterationCount int       `json:"usedIterationCount,omitempty"`
	UsedToolCallCount  int       `json:"usedToolCallCount,omitempty"`
	QualifyingEventIDs []string  `json:"qualifyingEventIDs,omitempty"`
}

func highestEscalatedTaskLevel(taskEvents []task.TaskEvent) TaskLevel {
	highestTaskLevel := TaskLevel("")
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != "agent.budget_escalated" && taskEvent.Name != "agent.skill_task_level_escalated" {
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
