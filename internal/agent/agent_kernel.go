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

func (agentKernel *AgentKernel) HandleInboundMessage(requesterPersonID string, originConversationID string, prompt string) (task.TaskRun, error) {
	return agentKernel.RunTask(requesterPersonID, originConversationID, prompt)
}

func (agentKernel *AgentKernel) AppendTaskEvent(taskRunID string, name string, body string) {
	agentKernel.taskRunService.AppendTaskEvent(taskRunID, name, body)
}

func (agentKernel *AgentKernel) GenerateReply(responseContext context.Context, prompt string) (string, error) {
	return agentKernel.GenerateReplyWithMemory(responseContext, prompt, nil)
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
		RequesterPersonID:    request.RequesterPersonID,
		RequesterName:        request.RequesterName,
		RequesterCallingName: request.RequesterCallingName,
		RequesterHandle:      request.RequesterHandle,
		ProfileName:          request.ProfileName,
		ConversationID:       request.ConversationID,
		Prompt:               request.Prompt,
		VisibleContext:       request.VisibleContext,
		MemoryFacts:          request.MemoryFacts,
		ToolRegistry:         request.ToolRegistry,
	})
}

func (agentKernel *AgentKernel) RunAgentRequest(responseContext context.Context, request AgentRequest) (AgentTurnResult, error) {
	instructionBundle := agentKernel.currentInstructionBundle()
	instructionBundle = selectInstructionBundleForRequest(instructionBundle, request)
	intakePlanner := NewTaskIntakePlanner(agentKernel.intakeLanguageModel, agentKernel.intakeOptions)
	intakeDecision := intakePlanner.Plan(responseContext, request)
	intakeDecision = promoteIntakeDecisionForSelectedSkills(intakeDecision, instructionBundle, agentKernel.intakeOptions.DefaultBudgetClass)
	if intakeDecision.Classification == IntakeClassificationNeedsConfirmation {
		return agentKernel.completeIntakeOnlyRequest(request, intakeDecision, task.TaskStatusWaitingUserInput)
	}
	if intakeDecision.Classification == IntakeClassificationUnsupported {
		return agentKernel.completeIntakeOnlyRequest(request, intakeDecision, task.TaskStatusBlocked)
	}

	turnRequest := AgentTurnRequest{
		RequesterPersonID:     request.RequesterPersonID,
		RequesterName:         request.RequesterName,
		RequesterCallingName:  request.RequesterCallingName,
		RequesterHandle:       request.RequesterHandle,
		ProfileName:           normalizedAgentProfileName(request.ProfileName),
		ConversationID:        request.ConversationID,
		Prompt:                request.Prompt,
		VisibleContext:        request.VisibleContext,
		MemoryFacts:           request.MemoryFacts,
		ToolRegistry:          request.ToolRegistry,
		InstructionPrompt:     instructionBundle.Prompt,
		InstructionSources:    append([]InstructionSource{}, instructionBundle.Sources...),
		SkillDecisions:        append([]SkillSelectionDecision{}, instructionBundle.SkillDecisions...),
		RequiredEvidenceTools: selectedRequiredEvidenceTools(instructionBundle),
	}
	turnOptions := agentKernel.turnOptionsForIntakeDecision(intakeDecision)
	if intakeDecision.Classification == IntakeClassificationQuickReply {
		turnRequest.ToolRegistry = nil
		turnOptions.MaxIterations = 1
	}

	agentTurnRunner := NewAgentTurnRunner(
		agentKernel.taskRunService,
		agentKernel.taskStepService,
		agentKernel.taskArtifactService,
		agentKernel.languageModel,
		turnOptions,
	)
	result, errorValue := agentTurnRunner.RunTurn(responseContext, turnRequest)
	if result.TaskRun.TaskRunID != "" {
		agentKernel.AppendTaskEvent(result.TaskRun.TaskRunID, "agent.intake", marshalEventBody(intakeDecision))
	}
	return result, errorValue
}

func selectedRequiredEvidenceTools(instructionBundle InstructionBundle) []string {
	selectedSkillName := map[string]bool{}
	for _, skillDecision := range instructionBundle.SkillDecisions {
		if skillDecision.Status == "selected" {
			selectedSkillName[skillDecision.Name] = true
		}
	}
	requiredEvidenceTools := []string{}
	seenToolName := map[string]bool{}
	for _, skillInstruction := range instructionBundle.Skills {
		if !selectedSkillName[skillInstruction.Name] {
			continue
		}
		for _, toolName := range skillInstruction.Completion.RequiredEvidenceTools {
			trimmedToolName := strings.TrimSpace(toolName)
			if trimmedToolName == "" || seenToolName[trimmedToolName] {
				continue
			}
			seenToolName[trimmedToolName] = true
			requiredEvidenceTools = append(requiredEvidenceTools, trimmedToolName)
		}
	}
	return requiredEvidenceTools
}

func promoteIntakeDecisionForSelectedSkills(decision IntakeDecision, instructionBundle InstructionBundle, defaultBudgetClass BudgetClass) IntakeDecision {
	if decision.Classification != IntakeClassificationQuickReply || !hasSelectedSkillWithRequiredTools(instructionBundle) {
		return decision
	}
	decision.Classification = IntakeClassificationBoundedTask
	if decision.TaskShape == "" || decision.TaskShape == TaskShapeImmediateReply {
		decision.TaskShape = TaskShapeResearchTask
	}
	decision.BudgetClass = LargerBudgetClass(decision.BudgetClass, defaultBudgetClass)
	decision.Reason = "selected skill requires bounded tool execution"
	decision.UserFacingReply = ""
	return decision
}

func hasSelectedSkillWithRequiredTools(instructionBundle InstructionBundle) bool {
	requiredToolCountBySkillName := map[string]int{}
	for _, skillInstruction := range instructionBundle.Skills {
		requiredToolCountBySkillName[skillInstruction.Name] = len(skillInstruction.RequiredTools)
	}
	for _, skillDecision := range instructionBundle.SkillDecisions {
		if skillDecision.Status == "selected" && requiredToolCountBySkillName[skillDecision.Name] > 0 {
			return true
		}
	}
	return false
}

type VisibleContext struct {
	Messages      []VisibleContextMessage
	HasMoreBefore bool
	HistoryCursor string
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
	prompts := []string{strings.TrimSpace(instructionBundle.Prompt)}
	sources := append([]InstructionSource{}, instructionBundle.Sources...)
	skillSelector := SkillSelector{}
	skillDecisions := []SkillSelectionDecision{}
	selectedSkillInstructions := []SkillInstruction{}
	for _, skillInstruction := range instructionBundle.Skills {
		skillDecision := skillSelector.Evaluate(skillInstruction, request, normalizedAgentProfileName(request.ProfileName))
		skillDecisions = append(skillDecisions, skillDecision)
		if skillDecision.Status != "selected" {
			continue
		}
		selectedSkillInstructions = append(selectedSkillInstructions, skillInstruction)
		sources = append(sources, skillInstruction.Source)
	}
	prompts = append(prompts, buildCompactSkillIndexPrompt(instructionBundle.Skills, skillDecisions))
	prompts = append(prompts, buildSelectedSkillInstructionPrompt(selectedSkillInstructions))
	return InstructionBundle{
		Prompt:         strings.Join(nonEmptyStrings(prompts), "\n\n"),
		Sources:        sources,
		Skills:         append([]SkillInstruction{}, instructionBundle.Skills...),
		SkillDecisions: skillDecisions,
	}
}

func normalizedAgentProfileName(profileName string) string {
	trimmedProfileName := strings.TrimSpace(profileName)
	if trimmedProfileName == "" {
		return "default"
	}
	return trimmedProfileName
}

func buildCompactSkillIndexPrompt(skillInstructions []SkillInstruction, skillDecisions []SkillSelectionDecision) string {
	eligibleSkillInstructions := eligibleSkillInstructions(skillInstructions, skillDecisions)
	if len(eligibleSkillInstructions) == 0 {
		return ""
	}
	lines := []string{"Available skill index. Full instructions are loaded only for selected skills:"}
	for _, skillInstruction := range eligibleSkillInstructions {
		lines = append(lines, "- "+compactSkillIndexLine(skillInstruction))
	}
	return strings.Join(lines, "\n")
}

func eligibleSkillInstructions(skillInstructions []SkillInstruction, skillDecisions []SkillSelectionDecision) []SkillInstruction {
	decisionByName := map[string]SkillSelectionDecision{}
	for _, skillDecision := range skillDecisions {
		decisionByName[skillDecision.Name] = skillDecision
	}
	eligibleSkills := []SkillInstruction{}
	for _, skillInstruction := range skillInstructions {
		skillDecision, isFound := decisionByName[skillInstruction.Name]
		if !isFound || skillDecision.Status == "selected" || skillDecision.Reason == "no_trigger_matched" {
			eligibleSkills = append(eligibleSkills, skillInstruction)
		}
	}
	return eligibleSkills
}

func compactSkillIndexLine(skillInstruction SkillInstruction) string {
	parts := []string{skillInstruction.Name}
	if strings.TrimSpace(skillInstruction.Category) != "" {
		parts = append(parts, "category="+strings.TrimSpace(skillInstruction.Category))
	}
	if strings.TrimSpace(skillInstruction.Description) != "" {
		parts = append(parts, "description="+strings.TrimSpace(skillInstruction.Description))
	}
	if len(skillInstruction.Tags) > 0 {
		parts = append(parts, "tags="+strings.Join(skillInstruction.Tags, ", "))
	}
	if len(skillInstruction.TriggerHints) > 0 {
		parts = append(parts, "triggerHints="+strings.Join(skillInstruction.TriggerHints, ", "))
	}
	if len(skillInstruction.RequiredTools) > 0 {
		parts = append(parts, "requiredTools="+strings.Join(skillInstruction.RequiredTools, ", "))
	}
	return strings.Join(parts, "; ")
}

func buildSelectedSkillInstructionPrompt(skillInstructions []SkillInstruction) string {
	if len(skillInstructions) == 0 {
		return ""
	}
	parts := []string{"Selected skill instructions:"}
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
		finalReply = defaultUserFacingReply(intakeDecision.Classification)
	}
	if finalReply == "" {
		finalReply = "I cannot complete that within the current execution boundary."
	}
	blockedTaskRun, errorValue := agentKernel.taskRunService.PauseTaskRun(taskRun.TaskRunID, status, intakeDecision.Reason)
	if errorValue != nil {
		return AgentTurnResult{}, errorValue
	}
	blockedTaskRun.Result = finalReply
	return AgentTurnResult{TaskRun: blockedTaskRun, FinalReply: finalReply}, nil
}

func (agentKernel *AgentKernel) turnOptionsForIntakeDecision(intakeDecision IntakeDecision) TurnOptions {
	baseOptions := normalizeTurnOptions(agentKernel.turnOptions)
	budgetProfile := BudgetProfileForClass(intakeDecision.BudgetClass)
	baseOptions.BudgetClass = budgetProfile.BudgetClass
	baseOptions.MaxIterations = budgetProfile.MaxIterations
	baseOptions.MaxToolCalls = budgetProfile.MaxToolCalls
	baseOptions.WallClockSecond = int(budgetProfile.Duration.Seconds())
	return baseOptions
}
