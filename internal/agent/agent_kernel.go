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
	highTaskLanguageModel   llm.LanguageModelProvider
	mediumTaskLanguageModel llm.LanguageModelProvider
	intakeLanguageModel     llm.LanguageModelProvider
	turnOptions             TurnOptions
	intakeOptions           IntakeOptions
	instructionPrompt       string
	instructionSources      []InstructionSource
	instructionLoader       func() InstructionBundle
	skillRetriever          SkillRetriever
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

func (agentKernel *AgentKernel) UseTaskTierLanguageModels(highTaskLanguageModel llm.LanguageModelProvider, mediumTaskLanguageModel llm.LanguageModelProvider) {
	agentKernel.highTaskLanguageModel = highTaskLanguageModel
	agentKernel.mediumTaskLanguageModel = mediumTaskLanguageModel
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
		PrecomputedTurnDecision: request.PrecomputedTurnDecision,
		AmbientDuty:             request.AmbientDuty,
		TaskComplexity:          request.TaskComplexity,
		WorkKinds:               append([]string{}, request.WorkKinds...),
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
	failedTaskRun.Result = failureNotice.SendableMessage()
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
	return NewTurnRouter(agentKernel.intakeLanguageModel, agentKernel.intakeOptions).Plan(responseContext, request)
}

func (agentKernel *AgentKernel) RunAgentRequest(responseContext context.Context, request AgentRequest) (AgentTurnResult, error) {
	if request.TurnStartedAt.IsZero() {
		request.TurnStartedAt = time.Now().Add(-2 * time.Second)
	}
	request.ResponseLanguage = ResolveResponseLanguage(request.ResponseLanguage, request.VisibleContext.ResponseLanguage)
	siteNormalizationReports := []siteRequirementNormalizationReport{}
	var activeGoalSiteReport siteRequirementNormalizationReport
	request.ActiveGoal, activeGoalSiteReport = normalizeActiveGoalSiteRequirement(request.ActiveGoal, request.Prompt)
	siteNormalizationReports = appendSiteRequirementNormalizationReport(siteNormalizationReports, activeGoalSiteReport)
	instructionBundle := agentKernel.currentInstructionBundle()
	instructionBundle = selectInstructionBundleForRequestWithRetrieverAndRouter(
		responseContext,
		instructionBundle,
		request,
		agentKernel.skillRetriever,
		NewSkillSearchQueryRouter(agentKernel.intakeLanguageModel),
	)
	instructionBundle = instructionBundleWithPinnedSkills(instructionBundle, request)
	turnToolSet := request.ToolSet
	intakeRequest := request
	intakeRequest.ToolSet = turnToolSet
	turnRouter := NewTurnRouter(agentKernel.intakeLanguageModel, agentKernel.intakeOptions)
	turnDecision := turnRouter.Plan(responseContext, intakeRequest)
	intakeDecision := turnDecision.IntakeDecision()
	intakeDecision = promoteIntakeDecisionForSelectedSkills(intakeDecision, instructionBundle, agentKernel.intakeOptions.DefaultEffortLevel)
	intakeDecision = (TaskRecoveryPlanner{}).Plan(intakeRequest, intakeDecision)
	intakeDecision = promoteSitePrototypeEffort(intakeRequest, intakeDecision)
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
	}
	intakeDecision = agentKernel.restoreEscalatedEffortForContinuation(intakeRequest, intakeDecision)
	request.WorkKinds = appendUniqueStrings(append([]string{}, intakeDecision.WorkKinds...), request.ActiveGoal.WorkKinds...)
	intakeRequest.WorkKinds = request.WorkKinds
	if turnDecision.Route == TurnRouteConsume {
		result, errorValue := agentKernel.completeConsumedRequest(intakeRequest, turnDecision)
		agentKernel.appendSiteRequirementNormalizationReports(result.TaskRun.TaskRunID, siteNormalizationReports)
		return result, errorValue
	}
	if intakeDecision.Classification == IntakeClassificationNeedsConfirmation {
		result, errorValue := agentKernel.completeIntakeOnlyRequest(responseContext, intakeRequest, intakeDecision, task.TaskStatusWaitingUserInput)
		result.TurnRoute = turnDecision.Route
		agentKernel.appendSiteRequirementNormalizationReports(result.TaskRun.TaskRunID, siteNormalizationReports)
		return result, errorValue
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
	requiredEvidenceTools := outcomeContract.RequiredEvidenceTools
	requiredAttachmentSuffixes = outcomeContract.RequiredAttachmentSuffixes

	turnRequest := AgentTurnRequest{
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
		QualityAcceptanceGuidance:  selectedQualityAcceptanceGuidance(instructionBundle),
		AmbientDuty:                request.AmbientDuty,
		TaskComplexity:             intakeDecision.TaskComplexity,
		WorkKinds:                  append([]string{}, request.WorkKinds...),
		TurnStartedAt:              request.TurnStartedAt,
		CheckpointSender:           request.CheckpointSender,
	}
	turnOptions := agentKernel.turnOptionsForIntakeDecision(intakeDecision)

	agentTurnRunner := NewAgentTurnRunnerWithRecoveryModel(
		agentKernel.taskRunService,
		agentKernel.taskStepService,
		agentKernel.taskArtifactService,
		agentKernel.taskLanguageModelForTier(taskModelTier(intakeDecision.TaskComplexity, turnOptions.EffortLevel)),
		agentKernel.languageModel,
		turnOptions,
	)
	result, errorValue := agentTurnRunner.RunTurn(responseContext, turnRequest)
	result.TurnRoute = turnDecision.Route
	result.ToolNames = toolNamesForEvent(turnRequest.ToolSet)
	if result.TaskRun.TaskRunID != "" {
		agentKernel.AppendTaskEvent(result.TaskRun.TaskRunID, "agent.intake", marshalEventBody(intakeDecision))
		agentKernel.appendSiteRequirementNormalizationReports(result.TaskRun.TaskRunID, siteNormalizationReports)
		agentKernel.appendGoalLifecycleEvent(result.TaskRun, turnRequest.ActiveGoal)
	}
	return result, errorValue
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
		waitingGoal.WorkKinds = append([]string{}, request.WorkKinds...)
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
	approvalGoal.WorkKinds = append([]string{}, request.WorkKinds...)
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
	blockedTaskRun.Result = finishMessage
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
	effortProfile := EffortLimitProfileForLevel(intakeDecision.EffortLevel)
	baseOptions.EffortLevel = effortProfile.EffortLevel
	baseOptions.MaxIterationCount = effortProfile.MaxIterationCount
	baseOptions.MaxToolCallCount = effortProfile.MaxToolCallCount
	baseOptions.MaxElapsedSecond = int(effortProfile.Duration.Seconds())
	return baseOptions
}

type modelTier string

const (
	modelTierLow    modelTier = "low"
	modelTierMedium modelTier = "medium"
	modelTierHigh   modelTier = "high"
)

func taskModelTier(taskComplexity TaskComplexity, effortLevel EffortLevel) modelTier {
	if effortLevel == EffortLevelQuick {
		return modelTierLow
	}
	if effortLevel == EffortLevelDeep || effortLevel == EffortLevelExtended {
		return modelTierHigh
	}
	if taskComplexity == TaskComplexityComplex {
		return modelTierMedium
	}
	return modelTierLow
}

func (agentKernel *AgentKernel) taskLanguageModelForTier(tier modelTier) llm.LanguageModelProvider {
	switch tier {
	case modelTierHigh:
		if agentKernel.highTaskLanguageModel != nil {
			return agentKernel.highTaskLanguageModel
		}
	case modelTierMedium:
		if agentKernel.mediumTaskLanguageModel != nil {
			return agentKernel.mediumTaskLanguageModel
		}
	}
	return agentKernel.languageModel
}

func (agentKernel *AgentKernel) restoreEscalatedEffortForContinuation(request AgentRequest, intakeDecision IntakeDecision) IntakeDecision {
	taskRunID := strings.TrimSpace(request.ExistingTaskRunID)
	if taskRunID == "" {
		return intakeDecision
	}
	restoredEffortLevel := highestEscalatedEffortLevel(agentKernel.taskRunService.ListTaskEvent(taskRunID))
	if restoredEffortLevel == "" {
		return intakeDecision
	}
	intakeDecision.EffortLevel = LargerEffortLevel(intakeDecision.EffortLevel, restoredEffortLevel)
	return intakeDecision
}

func promoteSitePrototypeEffort(request AgentRequest, intakeDecision IntakeDecision) IntakeDecision {
	if !intakeDecisionHasSitePrototypeEvidence(request, intakeDecision) {
		return intakeDecision
	}
	intakeDecision.EffortLevel = LargerEffortLevel(intakeDecision.EffortLevel, EffortLevelDeep)
	return intakeDecision
}

func intakeDecisionHasSitePrototypeEvidence(request AgentRequest, intakeDecision IntakeDecision) bool {
	if intakeDecision.HasWorkKind(WorkKindSitePrototype) {
		return true
	}
	if strings.TrimSpace(intakeDecision.SiteRequestEvidence) != "" {
		return true
	}
	return activeGoalRequiresToolPrefix(request.ActiveGoal, "site.app.")
}

type budgetEscalatedEventBody struct {
	PreviousEffortLevel EffortLevel `json:"previousEffortLevel,omitempty"`
	NewEffortLevel      EffortLevel `json:"newEffortLevel"`
	UsedIterationCount  int         `json:"usedIterationCount,omitempty"`
	UsedToolCallCount   int         `json:"usedToolCallCount,omitempty"`
	QualifyingEventIDs  []string    `json:"qualifyingEventIDs,omitempty"`
}

func highestEscalatedEffortLevel(taskEvents []task.TaskEvent) EffortLevel {
	highestEffortLevel := EffortLevel("")
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != "agent.budget_escalated" {
			continue
		}
		var eventBody budgetEscalatedEventBody
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &eventBody); errorValue != nil {
			continue
		}
		normalizedEffortLevel := NormalizeEffortLevel(string(eventBody.NewEffortLevel))
		if normalizedEffortLevel == "" {
			continue
		}
		highestEffortLevel = LargerEffortLevel(highestEffortLevel, normalizedEffortLevel)
	}
	return highestEffortLevel
}
