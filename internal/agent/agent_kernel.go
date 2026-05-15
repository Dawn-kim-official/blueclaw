package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"blueclaw/internal/llm"
	"blueclaw/internal/memory"
	"blueclaw/internal/task"
)

type AgentKernel struct {
	planCompiler        PlanCompiler
	subagentDispatcher  SubagentDispatcher
	taskRunService      *task.TaskRunService
	taskStepService     *task.TaskStepService
	taskArtifactService *task.TaskArtifactService
	languageModel       llm.LanguageModelProvider
	intakeLanguageModel llm.LanguageModelProvider
	turnOptions         TurnOptions
	intakeOptions       IntakeOptions
	instructionPrompt   string
	instructionSources  []InstructionSource
	instructionLoader   func() InstructionBundle
	skillRetriever      SkillRetriever
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

func (agentKernel *AgentKernel) GenerateReply(responseContext context.Context, prompt string) (string, error) {
	return agentKernel.GenerateReplyWithMemory(responseContext, prompt, nil)
}

type ApprovalReplyDecision struct {
	IsApproval bool   `json:"isApproval"`
	Reason     string `json:"reason"`
}

func (agentKernel *AgentKernel) ClassifyApprovalReply(responseContext context.Context, pendingPrompt string, approvalQuestion string, reply string) (ApprovalReplyDecision, error) {
	decision, errorValue := agentKernel.ClassifyConfirmationReply(responseContext, pendingPrompt, approvalQuestion, reply)
	if errorValue != nil {
		return ApprovalReplyDecision{}, errorValue
	}
	return ApprovalReplyDecision{
		IsApproval: decision.Decision == "approved",
		Reason:     decision.Reason,
	}, nil
}

func (agentKernel *AgentKernel) GenerateReplyWithMemory(responseContext context.Context, prompt string, memoryFacts []memory.MemoryFact) (string, error) {
	return agentKernel.GenerateReplyWithContext(responseContext, prompt, VisibleContext{}, memoryFacts)
}

func (agentKernel *AgentKernel) GenerateReplyWithContext(responseContext context.Context, prompt string, visibleContext VisibleContext, memoryFacts []memory.MemoryFact) (string, error) {
	if agentKernel.languageModel == nil {
		return "", errors.New("language model provider is not configured")
	}
	instructionBundle := agentKernel.currentInstructionBundle()

	structuredResponse, errorValue := agentKernel.languageModel.GenerateStructuredResponse(
		responseContext,
		llm.StructuredResponseRequest{
			Messages: buildReplyMessagesWithInstructions(prompt, visibleContext, memoryFacts, instructionBundle.Prompt),
			StructuredOutputSchema: llm.StructuredOutputSchema{
				Name:               "blueclaw_reply",
				Document:           `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"],"additionalProperties":false}`,
				IsStrictlyEnforced: true,
			},
		},
	)
	if errorValue != nil {
		return "", errorValue
	}

	var replyDocument struct {
		Reply string `json:"reply"`
	}
	errorValue = json.Unmarshal([]byte(structuredResponse.Content), &replyDocument)
	if errorValue != nil {
		return "", errorValue
	}

	reply := strings.TrimSpace(replyDocument.Reply)
	if reply == "" {
		return "", errors.New("language model reply is empty")
	}

	return reply, nil
}

func (agentKernel *AgentKernel) RunTurn(responseContext context.Context, request AgentTurnRequest) (AgentTurnResult, error) {
	return agentKernel.RunAgentRequest(responseContext, AgentRequest{
		RequesterPersonID:      request.RequesterPersonID,
		RequesterName:          request.RequesterName,
		RequesterCallingName:   request.RequesterCallingName,
		RequesterHandle:        request.RequesterHandle,
		RequesterCircles:       append([]string{}, request.RequesterCircles...),
		IsApprovalContinuation: request.IsApprovalContinuation,
		ExistingTaskRunID:      request.ExistingTaskRunID,
		ProfileName:            request.ProfileName,
		ConversationID:         request.ConversationID,
		Prompt:                 request.Prompt,
		ResponseLanguage:       request.ResponseLanguage,
		VisibleContext:         request.VisibleContext,
		MemoryFacts:            request.MemoryFacts,
		ToolSet:                request.ToolSet,
		WorkspaceRootPath:      request.WorkspaceRootPath,
		ActivePaths:            request.ActivePaths,
		ActiveGoal:             request.ActiveGoal,
	})
}

func (agentKernel *AgentKernel) RunAgentRequest(responseContext context.Context, request AgentRequest) (AgentTurnResult, error) {
	request.ResponseLanguage = ResolveResponseLanguage(request.ResponseLanguage, request.VisibleContext.ResponseLanguage)
	instructionBundle := agentKernel.currentInstructionBundle()
	instructionBundle = selectInstructionBundleForRequestWithRetrieverAndRouter(
		responseContext,
		instructionBundle,
		request,
		agentKernel.skillRetriever,
		NewSkillSearchQueryRouter(agentKernel.intakeLanguageModel),
	)
	turnToolSet := request.ToolSet
	intakeRequest := request
	intakeRequest.ToolSet = turnToolSet
	intakePlanner := NewTaskIntakePlanner(agentKernel.intakeLanguageModel, agentKernel.intakeOptions)
	intakeDecision := intakePlanner.Plan(responseContext, intakeRequest)
	intakeDecision = promoteIntakeDecisionForSelectedSkills(intakeDecision, instructionBundle, agentKernel.intakeOptions.DefaultEffortLevel)
	intakeDecision = (TaskRecoveryPlanner{}).Plan(intakeRequest, intakeDecision)
	request.ResponseLanguage = ResolveResponseLanguage(intakeDecision.ResponseLanguage, request.ResponseLanguage)
	if intakeDecision.Classification == IntakeClassificationNeedsConfirmation {
		return agentKernel.completeIntakeOnlyRequest(intakeRequest, intakeDecision, task.TaskStatusWaitingUserInput)
	}
	if intakeDecision.Classification == IntakeClassificationUnsupported {
		return agentKernel.completeIntakeOnlyRequest(intakeRequest, intakeDecision, task.TaskStatusBlocked)
	}

	requiredAttachmentSuffixes := attachmentSuffixesForRequestedOutputFormats(intakeDecision.RequestedOutputFormats)
	evidenceHints := selectedEvidenceHintTools(instructionBundle)
	confirmationEvidenceHints := confirmationEvidenceHintsForRequest(request, intakeDecision, evidenceHints)
	confirmationResult, isBlocked, executionPlan, hasExecutionPlan, errorValue := agentKernel.applyConfirmationGate(responseContext, request, intakeDecision, confirmationEvidenceHints)
	if isBlocked || errorValue != nil {
		return confirmationResult, errorValue
	}
	outcomeContract := outcomeContractForRequest(request, intakeDecision, instructionBundle, executionPlan, hasExecutionPlan, requiredAttachmentSuffixes)
	requiredEvidenceTools := outcomeContract.RequiredEvidenceTools
	requiredAttachmentSuffixes = outcomeContract.RequiredAttachmentSuffixes
	turnToolSet = toolSetForOutcomeReference(turnToolSet, request, executionPlan, hasExecutionPlan, outcomeContract)

	turnRequest := AgentTurnRequest{
		RequesterPersonID:          request.RequesterPersonID,
		RequesterName:              request.RequesterName,
		RequesterCallingName:       request.RequesterCallingName,
		RequesterHandle:            request.RequesterHandle,
		RequesterCircles:           append([]string{}, request.RequesterCircles...),
		IsApprovalContinuation:     request.IsApprovalContinuation,
		ExistingTaskRunID:          request.ExistingTaskRunID,
		ProfileName:                normalizedAgentProfileName(request.ProfileName),
		ConversationID:             request.ConversationID,
		Prompt:                     request.Prompt,
		ResponseLanguage:           request.ResponseLanguage,
		VisibleContext:             request.VisibleContext,
		MemoryFacts:                request.MemoryFacts,
		ToolSet:                    turnToolSet,
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
	}
	turnOptions := agentKernel.turnOptionsForIntakeDecision(intakeDecision)

	agentTurnRunner := NewAgentTurnRunner(
		agentKernel.taskRunService,
		agentKernel.taskStepService,
		agentKernel.taskArtifactService,
		agentKernel.languageModel,
		turnOptions,
	)
	result, errorValue := agentTurnRunner.RunTurn(responseContext, turnRequest)
	result.ToolNames = toolNamesForEvent(turnRequest.ToolSet)
	if result.TaskRun.TaskRunID != "" {
		agentKernel.AppendTaskEvent(result.TaskRun.TaskRunID, "agent.intake", marshalEventBody(intakeDecision))
		agentKernel.appendGoalLifecycleEvent(result.TaskRun, turnRequest.ActiveGoal)
	}
	return result, errorValue
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

	taskRun := agentKernel.taskRunService.CreateTaskRun(request.RequesterPersonID, request.ConversationID, request.Prompt)
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
		agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.goal.created", marshalEventBody(activeGoalFromExecutionPlan(taskRun.TaskRunID, executionPlan, ActiveGoalStatusWaitingUserInput, evidenceHints, nil)))
		agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.goal.waiting_user_input", marshalEventBody(activeGoalFromExecutionPlan(taskRun.TaskRunID, executionPlan, ActiveGoalStatusWaitingUserInput, evidenceHints, nil)))
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
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.goal.created", marshalEventBody(activeGoalFromExecutionPlan(taskRun.TaskRunID, executionPlan, ActiveGoalStatusWaitingApproval, evidenceHints, nil)))
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.goal.waiting_approval", marshalEventBody(activeGoalFromExecutionPlan(taskRun.TaskRunID, executionPlan, ActiveGoalStatusWaitingApproval, evidenceHints, nil)))
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

func shouldBuildExecutionPlanForConfirmation(request AgentRequest, intakeDecision IntakeDecision, requiredEvidenceTools []string) bool {
	if intakeDecision.Classification != IntakeClassificationBoundedTask {
		return false
	}
	if intakeDecision.TaskShape == TaskShapeApprovalGatedTask {
		return true
	}
	for _, toolName := range requiredEvidenceTools {
		if confirmationRiskyEvidenceTool(toolName) {
			return true
		}
	}
	return promptLooksLikeConfirmationCandidate(request.Prompt)
}

func confirmationRiskyEvidenceTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "platform.dm.send", "mail.message.send", "google.gmail.send", "slack.message.send", "site.app.publish":
		return true
	default:
		return false
	}
}

func promptLooksLikeConfirmationCandidate(prompt string) bool {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	if normalizedPrompt == "" {
		return false
	}
	return containsAny(normalizedPrompt, []string{
		"dm", "direct message", "email", "mail", "delete", "remove", "cancel", "deploy", "publish", "permission", "invite", "every minute", "every hour",
		"전송", "삭제", "취소", "배포", "공개", "권한", "초대", "분마다", "시간마다", "1분마다", "반복",
	})
}

func promptLooksLikeExternalSend(prompt string) bool {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	return containsAny(normalizedPrompt, []string{
		"dm", "direct message", "email", "mail", "send", "message",
		"보내", "전송", "메일", "디엠", "dm으로", "메시지", "전달", "알려줘",
	})
}

func promptLooksLikeSitePrototypeRequest(prompt string) bool {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	return containsAny(normalizedPrompt, []string{
		"website", "web app", "prototype", "landing page", "publish", "deploy",
		"웹사이트", "웹 앱", "프로토타입", "랜딩", "배포", "공개", "사이트 만들어", "사이트를 만들어",
	})
}

func promptLooksLikeCalendarRequest(prompt string) bool {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	return containsAny(normalizedPrompt, []string{
		"calendar", "event", "일정", "캘린더", "달력", "회의", "약속", "휴가",
	})
}

func toolSetForSelectedSkills(toolSet *ToolSet, instructionBundle InstructionBundle) *ToolSet {
	if toolSet == nil {
		return nil
	}
	return toolSet.WithAllowedToolNames(toolNamesForSelectedSkills(instructionBundle))
}

func toolSetForOutcomeReference(toolSet *ToolSet, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) *ToolSet {
	if toolSet == nil {
		return nil
	}
	allowedToolNames := []string{}
	for _, toolName := range toolSet.ListToolNames() {
		if shouldExposeToolForOutcome(toolName, request, executionPlan, hasExecutionPlan, outcomeContract) {
			allowedToolNames = append(allowedToolNames, toolName)
		}
	}
	return toolSet.WithAllowedToolNames(allowedToolNames)
}

func shouldExposeToolForOutcome(toolName string, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if strings.HasPrefix(trimmedToolName, "site.app.") {
		return outcomeAllowsSiteTools(request, executionPlan, hasExecutionPlan, outcomeContract)
	}
	if isSendEvidenceTool(trimmedToolName) {
		return outcomeAllowsExternalSendTools(request, executionPlan, hasExecutionPlan, outcomeContract)
	}
	return true
}

func outcomeAllowsSiteTools(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
	return contractRequiresToolPrefix(outcomeContract, "site.app.") || (hasExecutionPlan && executionPlan.PublicDeploy) || promptLooksLikeSitePrototypeRequest(request.Prompt)
}

func outcomeAllowsExternalSendTools(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
	return contractRequiresSendTool(outcomeContract) || (hasExecutionPlan && (executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend)) || promptLooksLikeExternalSend(request.Prompt)
}

func toolNamesForSelectedSkills(instructionBundle InstructionBundle) []string {
	toolNames := append([]string{}, coreAgentToolNames()...)
	toolNames = appendUniqueStrings(toolNames, DefaultSkillToolNames()...)
	selectedSkillName := selectedSkillNames(instructionBundle.SkillDecisions)
	for _, skillInstruction := range instructionBundle.Skills {
		if !selectedSkillName[skillInstruction.Name] {
			continue
		}
		toolNames = appendUniqueStrings(toolNames, SkillToolNames(skillInstruction)...)
	}
	return toolNames
}

func selectedSkillNames(skillDecisions []SkillSelectionDecision) map[string]bool {
	selectedSkillName := map[string]bool{}
	for _, skillDecision := range skillDecisions {
		if skillDecision.Status == "selected" {
			selectedSkillName[skillDecision.Name] = true
		}
	}
	return selectedSkillName
}

func coreAgentToolNames() []string {
	return []string{"conversation.history", "memory.search", "ask.confirm", "ask.choice", "ask.input", "skill.search"}
}

func selectedEvidenceHintTools(instructionBundle InstructionBundle) []string {
	toolNames := []string{}
	selectedSkillNames := selectedSkillNameSet(instructionBundle.SkillDecisions)
	for _, skillInstruction := range instructionBundle.Skills {
		if !selectedSkillNames[skillInstruction.Name] {
			continue
		}
		toolNames = append(toolNames, skillInstruction.Completion.RequiredEvidenceTools...)
	}
	return appendUniqueStrings(toolNames)
}

func confirmationEvidenceHintsForRequest(request AgentRequest, intakeDecision IntakeDecision, evidenceHints []string) []string {
	toolNames := []string{}
	for _, toolName := range evidenceHints {
		if evidenceHintMatchesOutcome(toolName, request, intakeDecision, ExecutionPlan{}, false, nil) {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
	}
	return toolNames
}

func selectedSkillNameSet(skillDecisions []SkillSelectionDecision) map[string]bool {
	selectedSkillNames := map[string]bool{}
	for _, skillDecision := range skillDecisions {
		if skillDecision.Status == "selected" {
			selectedSkillNames[skillDecision.Name] = true
		}
	}
	return selectedSkillNames
}

func selectedRequiredAttachmentSuffixes(_ InstructionBundle, _ string) []string {
	return nil
}

func outcomeContractForRequest(request AgentRequest, intakeDecision IntakeDecision, instructionBundle InstructionBundle, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredAttachmentSuffixes []string) OutcomeContract {
	if activeGoalOutcomeContractHasRequirements(request.ActiveGoal.OutcomeContract) {
		contract := request.ActiveGoal.OutcomeContract
		contract.SelectedEvidenceHints = appendUniqueStrings(contract.SelectedEvidenceHints, selectedEvidenceHintTools(instructionBundle)...)
		if strings.TrimSpace(contract.ArtifactRequirement) == "" || contract.ArtifactRequirement == ArtifactRequirementNone {
			contract.ArtifactRequirement = artifactRequirementForOutcomeContract(intakeDecision, contract)
		}
		return normalizeOutcomeContract(contract)
	}
	contract := OutcomeContract{
		SelectedEvidenceHints:      selectedEvidenceHintTools(instructionBundle),
		RequiredAttachmentSuffixes: append([]string{}, requiredAttachmentSuffixes...),
	}
	contract.RequiredEvidenceTools = outcomeEvidenceTools(request, intakeDecision, executionPlan, hasExecutionPlan, contract.SelectedEvidenceHints, requiredAttachmentSuffixes)
	if len(requiredAttachmentSuffixes) > 0 {
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, "file.attach")
	}
	contract.ArtifactRequirement = artifactRequirementForOutcomeContract(intakeDecision, contract)
	contract.Source = outcomeContractSource(hasExecutionPlan, requiredAttachmentSuffixes)
	return normalizeOutcomeContract(contract)
}

func activeGoalOutcomeContractHasRequirements(contract OutcomeContract) bool {
	return len(contract.RequiredEvidenceTools) > 0 || len(contract.RequiredEvidenceAnyOf) > 0 || len(contract.RequiredAttachmentSuffixes) > 0 || strings.TrimSpace(contract.ArtifactRequirement) != ""
}

func artifactRequirementForOutcomeContract(intakeDecision IntakeDecision, contract OutcomeContract) string {
	if len(contract.RequiredAttachmentSuffixes) > 0 || stringSliceContains(contract.RequiredEvidenceTools, "file.attach") || evidenceAnyOfContainsTool(contract.RequiredEvidenceAnyOf, "file.attach") {
		return ArtifactRequirementRequired
	}
	for _, outputFormat := range intakeDecision.RequestedOutputFormats {
		if isArtifactOutputFormat(outputFormat) {
			return ArtifactRequirementPreferred
		}
	}
	return ArtifactRequirementNone
}

func evidenceAnyOfContainsTool(groups [][]string, toolName string) bool {
	for _, group := range groups {
		if stringSliceContains(group, toolName) {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func isArtifactOutputFormat(value string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "."))) {
	case "pdf", "ppt", "pptx", "doc", "docx", "xls", "xlsx", "csv", "tsv", "html", "zip", "png", "jpg", "jpeg":
		return true
	default:
		return false
	}
}

func outcomeEvidenceTools(request AgentRequest, intakeDecision IntakeDecision, executionPlan ExecutionPlan, hasExecutionPlan bool, evidenceHints []string, requiredAttachmentSuffixes []string) []string {
	toolNames := []string{}
	for _, toolName := range evidenceHints {
		if evidenceHintMatchesOutcome(toolName, request, intakeDecision, executionPlan, hasExecutionPlan, requiredAttachmentSuffixes) {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
	}
	return toolNames
}

func evidenceHintMatchesOutcome(toolName string, request AgentRequest, intakeDecision IntakeDecision, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredAttachmentSuffixes []string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return false
	}
	if isSendEvidenceTool(trimmedToolName) {
		return (hasExecutionPlan && (executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend)) || promptLooksLikeExternalSend(request.Prompt)
	}
	if trimmedToolName == "file.attach" {
		return len(requiredAttachmentSuffixes) > 0
	}
	if strings.HasPrefix(trimmedToolName, "site.app.") {
		return (hasExecutionPlan && executionPlan.PublicDeploy) || promptLooksLikeSitePrototypeRequest(request.Prompt)
	}
	if strings.HasPrefix(trimmedToolName, "schedule.") {
		return intakeDecision.TaskShape == TaskShapeScheduledTask
	}
	if strings.HasPrefix(trimmedToolName, "calendar.") {
		return promptLooksLikeCalendarRequest(request.Prompt)
	}
	return false
}

func isSendEvidenceTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "platform.dm.send", "mail.message.send", "google.gmail.send", "slack.message.send":
		return true
	default:
		return false
	}
}

func contractRequiresSendTool(contract OutcomeContract) bool {
	for _, toolName := range outcomeContractRequiredToolNames(contract) {
		if isSendEvidenceTool(toolName) {
			return true
		}
	}
	return false
}

func contractRequiresToolPrefix(contract OutcomeContract, prefix string) bool {
	for _, toolName := range outcomeContractRequiredToolNames(contract) {
		if strings.HasPrefix(strings.TrimSpace(toolName), prefix) {
			return true
		}
	}
	return false
}

func outcomeContractRequiredToolNames(contract OutcomeContract) []string {
	toolNames := append([]string{}, contract.RequiredEvidenceTools...)
	for _, toolNameGroup := range contract.RequiredEvidenceAnyOf {
		toolNames = append(toolNames, toolNameGroup...)
	}
	return toolNames
}

func outcomeContractSource(hasExecutionPlan bool, requiredAttachmentSuffixes []string) string {
	sources := []string{}
	if hasExecutionPlan {
		sources = append(sources, "execution_plan")
	}
	if len(requiredAttachmentSuffixes) > 0 {
		sources = append(sources, "requested_output")
	}
	if len(sources) == 0 {
		return "explicit_request"
	}
	return strings.Join(sources, "+")
}

func activeGoalForTurn(request AgentRequest, outcomeContract OutcomeContract, executionPlan ExecutionPlan, hasExecutionPlan bool) ActiveGoal {
	activeGoal := request.ActiveGoal
	activeGoal.OutcomeContract = normalizeOutcomeContract(outcomeContract)
	if strings.TrimSpace(activeGoal.OriginalInstruction) == "" {
		activeGoal.OriginalInstruction = strings.TrimSpace(request.Prompt)
	}
	if hasExecutionPlan {
		activeGoal.OriginalInstruction = firstNonEmptyString(executionPlan.OriginalInstruction, activeGoal.OriginalInstruction)
		activeGoal.CurrentObjective = firstNonEmptyString(executionPlan.Summary, activeGoal.CurrentObjective)
		activeGoal.MissingInformation = append([]string{}, executionPlan.MissingInformation...)
	}
	if activeGoal.Status == "" {
		activeGoal.Status = ActiveGoalStatusActive
	}
	return activeGoal
}

func activeGoalFromExecutionPlan(taskRunID string, executionPlan ExecutionPlan, status ActiveGoalStatus, evidenceHints []string, requiredAttachmentSuffixes []string) ActiveGoal {
	outcomeContract := normalizeOutcomeContract(OutcomeContract{
		RequiredEvidenceTools:      executionPlanEvidenceTools(executionPlan, evidenceHints),
		RequiredAttachmentSuffixes: append([]string{}, requiredAttachmentSuffixes...),
		SelectedEvidenceHints:      append([]string{}, evidenceHints...),
		Source:                     "execution_plan",
	})
	return ActiveGoal{
		GoalID:              strings.TrimSpace(taskRunID),
		TaskRunID:           strings.TrimSpace(taskRunID),
		OriginalInstruction: strings.TrimSpace(executionPlan.OriginalInstruction),
		CurrentObjective:    strings.TrimSpace(executionPlan.Summary),
		MissingInformation:  append([]string{}, executionPlan.MissingInformation...),
		OutcomeContract:     outcomeContract,
		Status:              status,
	}
}

func activeGoalFromIntakeOnly(taskRunID string, request AgentRequest, intakeDecision IntakeDecision, status task.TaskStatus) ActiveGoal {
	return ActiveGoal{
		GoalID:              strings.TrimSpace(taskRunID),
		TaskRunID:           strings.TrimSpace(taskRunID),
		OriginalInstruction: strings.TrimSpace(request.Prompt),
		CurrentObjective:    strings.TrimSpace(intakeDecision.Reason),
		Status:              activeGoalStatusForTaskStatus(status),
	}
}

func activeGoalStatusForTaskStatus(status task.TaskStatus) ActiveGoalStatus {
	switch status {
	case task.TaskStatusWaitingUserInput:
		return ActiveGoalStatusWaitingUserInput
	case task.TaskStatusWaitingApproval:
		return ActiveGoalStatusWaitingApproval
	case task.TaskStatusCompleted:
		return ActiveGoalStatusCompleted
	case task.TaskStatusBlocked, task.TaskStatusFailed, task.TaskStatusCancelled:
		return ActiveGoalStatusBlocked
	default:
		return ActiveGoalStatusActive
	}
}

func activeGoalEventNameForTaskStatus(status task.TaskStatus) string {
	switch status {
	case task.TaskStatusWaitingUserInput:
		return "agent.goal.waiting_user_input"
	case task.TaskStatusWaitingApproval:
		return "agent.goal.waiting_approval"
	case task.TaskStatusCompleted:
		return "agent.goal.completed"
	case task.TaskStatusBlocked, task.TaskStatusFailed, task.TaskStatusCancelled:
		return "agent.goal.blocked"
	default:
		return "agent.goal.updated"
	}
}

func executionPlanEvidenceTools(executionPlan ExecutionPlan, evidenceHints []string) []string {
	toolNames := []string{}
	for _, toolName := range evidenceHints {
		if isSendEvidenceTool(toolName) && (executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend) {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
		if strings.HasPrefix(strings.TrimSpace(toolName), "site.app.") && executionPlan.PublicDeploy {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
	}
	return toolNames
}

func attachmentSuffixesForRequestedOutputFormats(formats []string) []string {
	suffixes := []string{}
	for _, format := range normalizeRequestedOutputFormats(formats) {
		switch format {
		case "html":
			suffixes = append(suffixes, ".html")
		case "pptx":
			suffixes = append(suffixes, ".pptx")
		case "pdf":
			suffixes = append(suffixes, ".pdf")
		case "txt":
			suffixes = append(suffixes, ".txt")
		case "docx":
			suffixes = append(suffixes, ".docx")
		case "xlsx":
			suffixes = append(suffixes, ".xlsx")
		case "csv":
			suffixes = append(suffixes, ".csv")
		}
	}
	return suffixes
}

func appendUniqueStrings(values []string, candidates ...string) []string {
	nextValues := append([]string{}, values...)
	seenValue := map[string]bool{}
	for _, value := range nextValues {
		seenValue[value] = true
	}
	for _, candidate := range candidates {
		trimmedCandidate := strings.TrimSpace(candidate)
		if trimmedCandidate == "" || seenValue[trimmedCandidate] {
			continue
		}
		seenValue[trimmedCandidate] = true
		nextValues = append(nextValues, trimmedCandidate)
	}
	return nextValues
}

func selectedQualityAcceptanceGuidance(instructionBundle InstructionBundle) []string {
	selectedSkillName := map[string]bool{}
	for _, skillDecision := range instructionBundle.SkillDecisions {
		if skillDecision.Status == "selected" {
			selectedSkillName[skillDecision.Name] = true
		}
	}
	guidance := []string{}
	seenGuidance := map[string]bool{}
	for _, skillInstruction := range instructionBundle.Skills {
		if !selectedSkillName[skillInstruction.Name] {
			continue
		}
		guidance = appendUniqueQualityGuidance(guidance, seenGuidance, skillInstruction.Quality.AcceptanceGuidance)
		guidance = appendUniqueQualityGuidance(guidance, seenGuidance, skillInstruction.Quality.Rubric)
	}
	return guidance
}

func appendUniqueQualityGuidance(guidance []string, seenGuidance map[string]bool, values []string) []string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" || seenGuidance[trimmedValue] {
			continue
		}
		seenGuidance[trimmedValue] = true
		guidance = append(guidance, trimmedValue)
	}
	return guidance
}

func promoteIntakeDecisionForSelectedSkills(decision IntakeDecision, instructionBundle InstructionBundle, defaultEffortLevel EffortLevel) IntakeDecision {
	if !canPromoteIntakeDecisionForSelectedSkills(decision) || !selectedSkillsNeedBoundedExecution(instructionBundle, decision.Classification) {
		return decision
	}
	decision.Classification = IntakeClassificationBoundedTask
	if decision.TaskShape == "" || decision.TaskShape == TaskShapeImmediateReply || decision.TaskShape == TaskShapeApprovalGatedTask {
		decision.TaskShape = taskShapeForSelectedSkills(instructionBundle)
	}
	decision.EffortLevel = LargerEffortLevel(decision.EffortLevel, defaultEffortLevel)
	decision.Reason = "selected skill requires bounded completion evidence"
	decision.UserFacingReply = ""
	return decision
}

func canPromoteIntakeDecisionForSelectedSkills(decision IntakeDecision) bool {
	switch decision.Classification {
	case IntakeClassificationQuickReply, IntakeClassificationNeedsConfirmation, IntakeClassificationUnsupported:
		return true
	default:
		return false
	}
}

func taskShapeForSelectedSkills(instructionBundle InstructionBundle) TaskShape {
	selectedSkillNames := selectedSkillNameSet(instructionBundle.SkillDecisions)
	if selectedSkillNames["scheduled-task"] {
		return TaskShapeScheduledTask
	}
	return TaskShapeResearchTask
}

func selectedSkillsNeedBoundedExecution(instructionBundle InstructionBundle, classification IntakeClassification) bool {
	allowedToolCountBySkillName := map[string]int{}
	allowedToolsBySkillName := map[string][]string{}
	for _, skillInstruction := range instructionBundle.Skills {
		allowedToolCountBySkillName[skillInstruction.Name] = len(SkillToolNames(skillInstruction))
		allowedToolsBySkillName[skillInstruction.Name] = SkillToolNames(skillInstruction)
	}
	for _, skillDecision := range instructionBundle.SkillDecisions {
		if skillDecision.Status == "selected" && skillDecision.Name == "scheduled-task" && allowedToolCountBySkillName[skillDecision.Name] > 0 {
			return true
		}
		if skillDecision.Status == "selected" && artifactSkillCanRecoverIntakeRefusal(classification, allowedToolsBySkillName[skillDecision.Name]) {
			return true
		}
	}
	return false
}

func artifactSkillCanRecoverIntakeRefusal(classification IntakeClassification, allowedTools []string) bool {
	if classification != IntakeClassificationUnsupported && classification != IntakeClassificationNeedsConfirmation {
		return false
	}
	for _, toolName := range allowedTools {
		switch strings.TrimSpace(toolName) {
		case "terminal.run", "file.write", "file.promote", "file.attach":
			return true
		}
	}
	return false
}

type VisibleContext struct {
	Messages         []VisibleContextMessage
	HasMoreBefore    bool
	HistoryCursor    string
	ResponseLanguage string
}

type VisibleContextMessage struct {
	Speaker            string
	SpeakerCallingName string
	SpeakerHandle      string
	Text               string
}

func (agentKernel *AgentKernel) buildReplyMessages(prompt string, visibleContext VisibleContext, memoryFacts []memory.MemoryFact) []llm.Message {
	return buildReplyMessagesWithInstructions(prompt, visibleContext, memoryFacts, agentKernel.currentInstructionBundle().Prompt)
}

func buildReplyMessages(prompt string, visibleContext VisibleContext, memoryFacts []memory.MemoryFact) []llm.Message {
	return buildReplyMessagesWithInstructions(prompt, visibleContext, memoryFacts, "")
}

func buildReplyMessagesWithInstructions(prompt string, visibleContext VisibleContext, memoryFacts []memory.MemoryFact, instructionPrompt string) []llm.Message {
	return (PromptAssembler{}).BuildReplyMessages(prompt, visibleContext, buildMemoryContext(memoryFacts), instructionPrompt)
}

func (agentKernel *AgentKernel) currentInstructionBundle() InstructionBundle {
	if agentKernel.instructionLoader != nil {
		return agentKernel.instructionLoader()
	}
	return InstructionBundle{
		Prompt:  agentKernel.instructionPrompt,
		Sources: append([]InstructionSource{}, agentKernel.instructionSources...),
	}
}

func selectInstructionBundleForRequest(instructionBundle InstructionBundle, request AgentRequest) InstructionBundle {
	return selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, request, nil)
}

func selectInstructionBundleForRequestWithRetriever(ctx context.Context, instructionBundle InstructionBundle, request AgentRequest, skillRetriever SkillRetriever) InstructionBundle {
	return selectInstructionBundleForRequestWithRetrieverAndRouter(ctx, instructionBundle, request, skillRetriever, SkillSearchQueryRouter{})
}

func selectInstructionBundleForRequestWithRetrieverAndRouter(ctx context.Context, instructionBundle InstructionBundle, request AgentRequest, skillRetriever SkillRetriever, skillSearchQueryRouter SkillSearchQueryRouter) InstructionBundle {
	prompts := []string{strings.TrimSpace(instructionBundle.Prompt)}
	sources := append([]InstructionSource{}, instructionBundle.Sources...)
	skillDecisions := []SkillSelectionDecision{}
	defaultSkillInstructions := DefaultSkillInstructions()
	selectedSkillInstructions := []SkillInstruction{}
	querySet, hasStructuredQueries := skillSearchQueryRouter.Build(ctx, request)
	retrievalResult := retrieveSkillCandidates(ctx, request, instructionBundle.Skills, skillRetriever, querySet, hasStructuredQueries)
	candidateByName := skillCandidateByName(retrievalResult.SelectedCandidates)
	candidateInstructions := visibleCandidateSkillInstructions(candidateSkillInstructions(instructionBundle.Skills, retrievalResult.SelectedCandidates), candidateByName, request.RequesterCircles)
	for _, skillInstruction := range candidateInstructions {
		skillCandidate, isFound := candidateByName[skillInstruction.Name]
		if !isFound {
			continue
		}
		skillDecision := skillDecisionForCandidate(skillInstruction, skillCandidate, normalizedAgentProfileName(request.ProfileName))
		if skillDecision.Status == "selected" && len(selectedSkillInstructions) >= maxSelectedSkillInstructionCount {
			skillDecision = skippedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "selected_skill_limit_reached", nil)
			skillDecision.Score = skillCandidate.Score
		}
		skillDecisions = append(skillDecisions, skillDecision)
		if skillDecision.Status != "selected" {
			continue
		}
		selectedSkillInstructions = append(selectedSkillInstructions, skillInstruction)
		sources = append(sources, skillInstruction.Source)
	}
	for _, skillInstruction := range alwaysSelectedSkillInstructions(instructionBundle.Skills, request, normalizedAgentProfileName(request.ProfileName), skillDecisions) {
		skillDecisions = append(skillDecisions, selectedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "always_selected"))
		selectedSkillInstructions = append(selectedSkillInstructions, skillInstruction)
		sources = append(sources, skillInstruction.Source)
	}
	skillDecisions = append(skillDecisions, blockedSkillSelectionDecisions(instructionBundle.Skills, skillDecisions, request, normalizedAgentProfileName(request.ProfileName))...)
	prompts = append(prompts, buildCompactSkillIndexPrompt(candidateInstructions))
	prompts = append(prompts, buildSelectedSkillInstructionPrompt(defaultSkillInstructions))
	prompts = append(prompts, buildSelectedSkillInstructionPrompt(selectedSkillInstructions))
	return InstructionBundle{
		Prompt:         strings.Join(nonEmptyStrings(prompts), "\n\n"),
		Sources:        sources,
		Skills:         appendSkillInstructions(instructionBundle.Skills, defaultSkillInstructions...),
		SkillDecisions: skillDecisions,
		RetrievalMode:  retrievalResult.RetrievalMode,
		IndexStatus:    retrievalResult.IndexStatus,
		CandidateCount: len(candidateInstructions),
		SkillQueries:   append([]string{}, retrievalResult.QueryDescriptions...),
	}
}

func alwaysSelectedSkillInstructions(skillInstructions []SkillInstruction, request AgentRequest, profileName string, existingSkillDecisions []SkillSelectionDecision) []SkillInstruction {
	existingDecisionByName := map[string]bool{}
	for _, skillDecision := range existingSkillDecisions {
		existingDecisionByName[skillDecision.Name] = true
	}
	alwaysSelectedSkills := []SkillInstruction{}
	for _, skillInstruction := range skillInstructions {
		if skillInstruction.Name != "ask" || existingDecisionByName[skillInstruction.Name] {
			continue
		}
		if skillAvailabilityDecision(skillInstruction, request, profileName).Status == "selected" {
			alwaysSelectedSkills = append(alwaysSelectedSkills, skillInstruction)
		}
	}
	return alwaysSelectedSkills
}

func appendSkillInstructions(left []SkillInstruction, right ...SkillInstruction) []SkillInstruction {
	seenSkillNames := map[string]bool{}
	result := []SkillInstruction{}
	for _, skillInstruction := range left {
		if strings.TrimSpace(skillInstruction.Name) == "" || seenSkillNames[skillInstruction.Name] {
			continue
		}
		seenSkillNames[skillInstruction.Name] = true
		result = append(result, skillInstruction)
	}
	for _, skillInstruction := range right {
		if strings.TrimSpace(skillInstruction.Name) == "" || seenSkillNames[skillInstruction.Name] {
			continue
		}
		seenSkillNames[skillInstruction.Name] = true
		result = append(result, skillInstruction)
	}
	return result
}

func visibleCandidateSkillInstructions(skillInstructions []SkillInstruction, candidateByName map[string]SkillCandidate, requesterCircles []string) []SkillInstruction {
	visibleSkillInstructions := []SkillInstruction{}
	for _, skillInstruction := range skillInstructions {
		skillCandidate, isCandidate := candidateByName[skillInstruction.Name]
		isDirectRequest := isCandidate && skillCandidate.Reason == "direct_skill_name"
		if skillHiddenFromRequester(skillInstruction, requesterCircles) && !isDirectRequest {
			continue
		}
		visibleSkillInstructions = append(visibleSkillInstructions, skillInstruction)
	}
	return visibleSkillInstructions
}

func skillHiddenFromRequester(skillInstruction SkillInstruction, requesterCircles []string) bool {
	hiddenCircleByName := map[string]bool{}
	for _, circleID := range skillInstruction.HiddenFromCircles {
		hiddenCircleByName[strings.ToLower(strings.TrimSpace(circleID))] = true
	}
	for _, circleID := range requesterCircles {
		if hiddenCircleByName[strings.ToLower(strings.TrimSpace(circleID))] {
			return true
		}
	}
	return false
}

func blockedSkillSelectionDecisions(skillInstructions []SkillInstruction, existingSkillDecisions []SkillSelectionDecision, request AgentRequest, profileName string) []SkillSelectionDecision {
	existingDecisionByName := map[string]bool{}
	for _, skillDecision := range existingSkillDecisions {
		existingDecisionByName[skillDecision.Name] = true
	}
	blockedDecisions := []SkillSelectionDecision{}
	for _, skillInstruction := range skillInstructions {
		if existingDecisionByName[skillInstruction.Name] {
			continue
		}
		if skillHiddenFromRequester(skillInstruction, request.RequesterCircles) {
			continue
		}
		skillDecision := skillAvailabilityDecision(skillInstruction, request, profileName)
		if skillDecision.Status == "skipped" && skillDecision.Reason != "no_trigger_matched" {
			blockedDecisions = append(blockedDecisions, skillDecision)
		}
	}
	return blockedDecisions
}

func retrieveSkillCandidates(ctx context.Context, request AgentRequest, skillInstructions []SkillInstruction, skillRetriever SkillRetriever, querySet SkillSearchQuerySet, hasStructuredQueries bool) SkillRetrievalResult {
	if hasStructuredQueries {
		querySet = normalizeSkillSearchQuerySet(querySet)
		if len(querySet.Queries) == 0 {
			return SkillRetrievalResult{RetrievalMode: "structured_query", IndexStatus: "empty_query"}
		}
	}
	if skillRetriever != nil {
		if hasStructuredQueries {
			return skillRetriever.Search(ctx, request, skillInstructions, querySet, maxSkillIndexCandidateCount)
		}
		return skillRetriever.Retrieve(ctx, request, skillInstructions, maxSkillIndexCandidateCount)
	}
	if hasStructuredQueries {
		return retrieveSkillsWithBM25(request, skillInstructions, skillSearchQueryText(querySet), maxSkillIndexCandidateCount, "embedding_unconfigured")
	}
	return retrieveSkillsWithBM25(request, skillInstructions, skillSelectionPrompt(request), maxSkillIndexCandidateCount, "embedding_unconfigured")
}

func candidateSkillInstructions(skillInstructions []SkillInstruction, skillCandidates []SkillCandidate) []SkillInstruction {
	skillInstructionByName := skillInstructionByName(skillInstructions)
	candidateInstructions := []SkillInstruction{}
	for _, skillCandidate := range skillCandidates {
		if skillInstruction, isFound := skillInstructionByName[skillCandidate.Name]; isFound {
			candidateInstructions = append(candidateInstructions, skillInstruction)
		}
	}
	return candidateInstructions
}

func skillCandidateByName(skillCandidates []SkillCandidate) map[string]SkillCandidate {
	candidateByName := map[string]SkillCandidate{}
	for _, skillCandidate := range skillCandidates {
		candidateByName[skillCandidate.Name] = skillCandidate
	}
	return candidateByName
}

func skillDecisionForCandidate(skillInstruction SkillInstruction, skillCandidate SkillCandidate, profileName string) SkillSelectionDecision {
	if skillCandidate.Score >= minimumSelectionScoreForCandidate(skillCandidate) {
		return SkillSelectionDecision{
			Name:        skillInstruction.Name,
			Status:      "selected",
			Reason:      skillCandidate.Reason,
			ProfileName: profileName,
			Score:       skillCandidate.Score,
			Source:      skillInstruction.Source,
		}
	}
	return SkillSelectionDecision{
		Name:        skillInstruction.Name,
		Status:      "skipped",
		Reason:      "candidate_below_selection_threshold",
		ProfileName: profileName,
		Score:       skillCandidate.Score,
		Source:      skillInstruction.Source,
	}
}

func minimumSelectionScoreForCandidate(skillCandidate SkillCandidate) float64 {
	if skillCandidate.Reason == "bm25_fallback" {
		return minimumBM25SelectionScore
	}
	return 0
}

func requestForSkillSelection(request AgentRequest) AgentRequest {
	request.Prompt = skillSelectionPrompt(request)
	return request
}

func skillSelectionPrompt(request AgentRequest) string {
	prompt := strings.TrimSpace(request.Prompt)
	if !shouldUseVisibleContextForSkillSelection(prompt) {
		return prompt
	}
	contextLines := []string{}
	for _, message := range request.VisibleContext.Messages {
		text := strings.TrimSpace(message.Text)
		if text != "" {
			contextLines = append(contextLines, text)
		}
	}
	if len(contextLines) == 0 {
		return prompt
	}
	return strings.Join(nonEmptyStrings([]string{strings.Join(contextLines, "\n"), prompt}), "\n")
}

func shouldUseVisibleContextForSkillSelection(prompt string) bool {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	return containsAny(normalizedPrompt, []string{
		"again", "continue", "redo", "same", "that", "previous",
		"계속", "다시", "새로", "아까", "이전", "그거", "그걸", "그 파일", "파일", "첨부", "이어",
	})
}

func normalizedAgentProfileName(profileName string) string {
	trimmedProfileName := strings.TrimSpace(profileName)
	if trimmedProfileName == "" {
		return "default"
	}
	return trimmedProfileName
}

func buildCompactSkillIndexPrompt(skillInstructions []SkillInstruction) string {
	if len(skillInstructions) == 0 {
		return ""
	}
	lines := []string{"Available skill index. These are capability references, not mandatory workflows:"}
	for _, skillInstruction := range skillInstructions {
		lines = append(lines, "- "+compactSkillIndexLine(skillInstruction))
	}
	return strings.Join(lines, "\n")
}

func compactSkillIndexLine(skillInstruction SkillInstruction) string {
	parts := []string{skillInstruction.Name}
	if text := strings.TrimSpace(skillInstruction.Description); text != "" {
		parts = append(parts, strings.TrimSpace(text))
	}
	return strings.Join(parts, ": ")
}

func buildSelectedSkillInstructionPrompt(skillInstructions []SkillInstruction) string {
	if len(skillInstructions) == 0 {
		return ""
	}
	parts := []string{
		"Available skill references:",
		"These skills/tools are available if they fit the user's current goal. They are not mandatory. Do not change the requested output type to match a skill.",
	}
	for _, skillInstruction := range skillInstructions {
		if strings.TrimSpace(skillInstruction.Prompt) != "" {
			parts = append(parts, strings.TrimSpace(skillInstruction.Prompt))
		}
	}
	return strings.Join(parts, "\n\n")
}

func nonEmptyStrings(values []string) []string {
	result := []string{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}

func buildVisibleContextDescription(visibleContext VisibleContext) string {
	contextLines := []string{}
	for _, message := range visibleContext.Messages {
		speaker := formatSpeakerLabel(message.SpeakerCallingName, message.SpeakerHandle, message.Speaker)
		text := strings.TrimSpace(message.Text)
		if text != "" {
			contextLines = append(contextLines, "- "+speaker+": "+text)
		}
	}

	if len(contextLines) == 0 && !visibleContext.HasMoreBefore {
		return ""
	}

	historyLine := "No earlier visible messages are available."
	if visibleContext.HasMoreBefore {
		historyLine = "There are earlier visible messages not included here. Ask for conversation.history if older context is needed."
	}

	if len(contextLines) == 0 {
		return "Recent visible conversation context:\n" + historyLine
	}

	return "Recent visible conversation context:\n" + strings.Join(contextLines, "\n") + "\n" + historyLine
}

func formatSpeakerLabel(callingName string, handle string, fullName string) string {
	primary := strings.TrimSpace(callingName)
	if primary == "" {
		primary = strings.TrimSpace(fullName)
	}
	if primary == "" {
		return "Someone"
	}
	trimmedHandle := strings.TrimSpace(handle)
	if trimmedHandle == "" {
		return primary
	}
	return primary + " (@" + trimmedHandle + ")"
}

func buildSenderAddressingDescription(request AgentTurnRequest) string {
	callingName := strings.TrimSpace(request.RequesterCallingName)
	fullName := strings.TrimSpace(request.RequesterName)
	handle := strings.TrimSpace(request.RequesterHandle)
	if callingName == "" {
		callingName = fullName
	}
	if callingName == "" && handle == "" {
		return ""
	}

	descriptionLines := []string{"You are speaking with the following user:"}
	if fullName != "" {
		descriptionLines = append(descriptionLines, "- Full name: "+fullName)
	}
	if callingName != "" {
		descriptionLines = append(descriptionLines, "- Calling name: "+callingName)
	}
	if handle != "" {
		descriptionLines = append(descriptionLines, "- Handle: @"+handle)
	}
	descriptionLines = append(descriptionLines,
		"When addressing them in Korean, call them \""+callingName+" 님\".",
		"When addressing them in English, call them \""+callingName+"\".",
		"If multiple participants in this conversation share the same calling name, append \"@handle\" when addressing them to disambiguate.",
	)
	return strings.Join(descriptionLines, "\n")
}

func (agentKernel *AgentKernel) RunTask(requesterPersonID string, originConversationID string, prompt string) (task.TaskRun, error) {
	taskRun := agentKernel.taskRunService.CreateTaskRun(requesterPersonID, originConversationID, prompt)
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

func (agentKernel *AgentKernel) completeIntakeOnlyRequest(request AgentRequest, intakeDecision IntakeDecision, status task.TaskStatus) (AgentTurnResult, error) {
	taskRun := agentKernel.taskRunService.CreateTaskRun(request.RequesterPersonID, request.ConversationID, request.Prompt)
	agentKernel.AppendTaskEvent(taskRun.TaskRunID, "agent.intake", marshalEventBody(intakeDecision))
	finalReply := strings.TrimSpace(intakeDecision.UserFacingReply)
	if finalReply == "" {
		finalReply = defaultUserFacingReplyForLanguage(intakeDecision.Classification, request.ResponseLanguage)
	}
	if finalReply == "" {
		finalReply = defaultExecutionBoundaryReply(request.ResponseLanguage)
	}
	blockedTaskRun, errorValue := agentKernel.taskRunService.PauseTaskRun(taskRun.TaskRunID, status, intakeDecision.Reason)
	if errorValue != nil {
		return AgentTurnResult{}, errorValue
	}
	agentKernel.appendGoalLifecycleEvent(blockedTaskRun, activeGoalFromIntakeOnly(taskRun.TaskRunID, request, intakeDecision, status))
	blockedTaskRun.Result = finalReply
	return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: finalReply, ToolNames: toolNamesForEvent(request.ToolSet)}, nil
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

func defaultUserFacingReplyForLanguage(classification IntakeClassification, responseLanguage string) string {
	if ResolveResponseLanguage(responseLanguage) == ResponseLanguageEnglish {
		return defaultUserFacingReply(classification)
	}
	switch classification {
	case IntakeClassificationNeedsConfirmation:
		return "진행하기 전에 범위를 조금 더 좁혀주세요."
	case IntakeClassificationUnsupported:
		return "현재 실행 범위에서는 안전하게 처리할 수 없습니다."
	default:
		return ""
	}
}

func defaultExecutionBoundaryReply(responseLanguage string) string {
	if ResolveResponseLanguage(responseLanguage) == ResponseLanguageEnglish {
		return "I cannot complete that within the current execution boundary."
	}
	return "현재 실행 범위에서는 요청을 완료할 수 없습니다."
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
