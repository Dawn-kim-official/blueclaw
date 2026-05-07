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
		RequesterCircles:     append([]string{}, request.RequesterCircles...),
		ProfileName:          request.ProfileName,
		ConversationID:       request.ConversationID,
		Prompt:               request.Prompt,
		VisibleContext:       request.VisibleContext,
		MemoryFacts:          request.MemoryFacts,
		ToolSet:              request.ToolSet,
		WorkspaceRootPath:    request.WorkspaceRootPath,
		ActivePaths:          request.ActivePaths,
	})
}

func (agentKernel *AgentKernel) RunAgentRequest(responseContext context.Context, request AgentRequest) (AgentTurnResult, error) {
	instructionBundle := agentKernel.currentInstructionBundle()
	instructionBundle = selectInstructionBundleForRequestWithRetriever(responseContext, instructionBundle, request, agentKernel.skillRetriever)
	turnToolSet := toolSetForSelectedSkills(request.ToolSet, instructionBundle)
	intakeRequest := request
	intakeRequest.ToolSet = turnToolSet
	intakePlanner := NewTaskIntakePlanner(agentKernel.intakeLanguageModel, agentKernel.intakeOptions)
	intakeDecision := intakePlanner.Plan(responseContext, intakeRequest)
	intakeDecision = promoteIntakeDecisionForSelectedSkills(intakeDecision, instructionBundle, agentKernel.intakeOptions.DefaultEffortLevel)
	if intakeDecision.Classification == IntakeClassificationNeedsConfirmation {
		return agentKernel.completeIntakeOnlyRequest(intakeRequest, intakeDecision, task.TaskStatusWaitingUserInput)
	}
	if intakeDecision.Classification == IntakeClassificationUnsupported {
		return agentKernel.completeIntakeOnlyRequest(intakeRequest, intakeDecision, task.TaskStatusBlocked)
	}

	requiredAttachmentSuffixes := attachmentSuffixesForRequestedOutputFormats(intakeDecision.RequestedOutputFormats)
	requiredEvidenceTools := selectedRequiredEvidenceTools(instructionBundle)
	if len(requiredAttachmentSuffixes) > 0 {
		requiredEvidenceTools = appendUniqueStrings(requiredEvidenceTools, "file.attach")
	}

	turnRequest := AgentTurnRequest{
		RequesterPersonID:          request.RequesterPersonID,
		RequesterName:              request.RequesterName,
		RequesterCallingName:       request.RequesterCallingName,
		RequesterHandle:            request.RequesterHandle,
		RequesterCircles:           append([]string{}, request.RequesterCircles...),
		ProfileName:                normalizedAgentProfileName(request.ProfileName),
		ConversationID:             request.ConversationID,
		Prompt:                     request.Prompt,
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
		RequiredEvidenceTools:      requiredEvidenceTools,
		RequiredAttachmentSuffixes: requiredAttachmentSuffixes,
		QualityAcceptanceGuidance:  selectedQualityAcceptanceGuidance(instructionBundle),
	}
	turnOptions := agentKernel.turnOptionsForIntakeDecision(intakeDecision)
	if intakeDecision.Classification == IntakeClassificationQuickReply {
		turnRequest.ToolSet = nil
		turnOptions.MaxIterationCount = 1
	}

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
	}
	return result, errorValue
}

func toolSetForSelectedSkills(toolSet *ToolSet, instructionBundle InstructionBundle) *ToolSet {
	if toolSet == nil {
		return nil
	}
	return toolSet.WithAllowedToolNames(toolNamesForSelectedSkills(instructionBundle))
}

func toolNamesForSelectedSkills(instructionBundle InstructionBundle) []string {
	toolNames := append([]string{}, coreAgentToolNames()...)
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
	return []string{"conversation.history", "memory.search", "approval.request"}
}

func selectedRequiredEvidenceTools(instructionBundle InstructionBundle) []string {
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
	if decision.Classification != IntakeClassificationQuickReply || !hasSelectedSkillWithAllowedTools(instructionBundle) {
		return decision
	}
	decision.Classification = IntakeClassificationBoundedTask
	if decision.TaskShape == "" || decision.TaskShape == TaskShapeImmediateReply {
		decision.TaskShape = TaskShapeResearchTask
	}
	decision.EffortLevel = LargerEffortLevel(decision.EffortLevel, defaultEffortLevel)
	decision.Reason = "selected skill requires bounded tool execution"
	decision.UserFacingReply = ""
	return decision
}

func hasSelectedSkillWithAllowedTools(instructionBundle InstructionBundle) bool {
	allowedToolCountBySkillName := map[string]int{}
	for _, skillInstruction := range instructionBundle.Skills {
		allowedToolCountBySkillName[skillInstruction.Name] = len(SkillToolNames(skillInstruction))
	}
	for _, skillDecision := range instructionBundle.SkillDecisions {
		if skillDecision.Status == "selected" && allowedToolCountBySkillName[skillDecision.Name] > 0 {
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
	return selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, request, nil)
}

func selectInstructionBundleForRequestWithRetriever(ctx context.Context, instructionBundle InstructionBundle, request AgentRequest, skillRetriever SkillRetriever) InstructionBundle {
	prompts := []string{strings.TrimSpace(instructionBundle.Prompt)}
	sources := append([]InstructionSource{}, instructionBundle.Sources...)
	skillSelector := SkillSelector{}
	selectionRequest := requestForSkillSelection(request)
	skillDecisions := []SkillSelectionDecision{}
	selectedSkillInstructions := []SkillInstruction{}
	retrievalResult := retrieveSkillCandidates(ctx, request, instructionBundle.Skills, skillRetriever)
	candidateByName := skillCandidateByName(retrievalResult.SelectedCandidates)
	candidateInstructions := visibleCandidateSkillInstructions(candidateSkillInstructions(instructionBundle.Skills, retrievalResult.SelectedCandidates), candidateByName, request.RequesterCircles)
	candidateInstructions = appendSkillInstructions(candidateInstructions, promptTriggeredSkillInstructions(instructionBundle.Skills, selectionRequest, candidateByName, request.RequesterCircles)...)
	for _, skillInstruction := range candidateInstructions {
		skillDecision := skillSelector.Evaluate(skillInstruction, selectionRequest, normalizedAgentProfileName(request.ProfileName))
		if skillCandidate, isFound := candidateByName[skillInstruction.Name]; isFound {
			skillDecision = skillDecisionForCandidate(skillInstruction, skillDecision, skillCandidate, normalizedAgentProfileName(request.ProfileName))
		}
		if skillDecision.Status == "selected" && len(selectedSkillInstructions) >= maxSelectedSkillInstructionCount {
			skillDecision = skippedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "selected_skill_limit_reached", nil)
			skillDecision.Score = candidateByName[skillInstruction.Name].Score
		}
		skillDecisions = append(skillDecisions, skillDecision)
		if skillDecision.Status != "selected" {
			continue
		}
		selectedSkillInstructions = append(selectedSkillInstructions, skillInstruction)
		sources = append(sources, skillInstruction.Source)
	}
	skillDecisions = append(skillDecisions, blockedSkillSelectionDecisions(instructionBundle.Skills, skillDecisions, selectionRequest, normalizedAgentProfileName(request.ProfileName))...)
	prompts = append(prompts, buildCompactSkillIndexPrompt(candidateInstructions))
	prompts = append(prompts, buildSelectedSkillInstructionPrompt(selectedSkillInstructions))
	return InstructionBundle{
		Prompt:         strings.Join(nonEmptyStrings(prompts), "\n\n"),
		Sources:        sources,
		Skills:         append([]SkillInstruction{}, instructionBundle.Skills...),
		SkillDecisions: skillDecisions,
		RetrievalMode:  retrievalResult.RetrievalMode,
		IndexStatus:    retrievalResult.IndexStatus,
		CandidateCount: len(candidateInstructions),
	}
}

func promptTriggeredSkillInstructions(skillInstructions []SkillInstruction, request AgentRequest, candidateByName map[string]SkillCandidate, requesterCircles []string) []SkillInstruction {
	skillSelector := SkillSelector{}
	triggeredSkillInstructions := []SkillInstruction{}
	for _, skillInstruction := range skillInstructions {
		if _, isCandidate := candidateByName[skillInstruction.Name]; isCandidate {
			continue
		}
		if skillHiddenFromRequester(skillInstruction, requesterCircles) {
			continue
		}
		if !skillProfileAllows(skillInstruction, normalizedAgentProfileName(request.ProfileName)) {
			continue
		}
		if len(missingAllowedTools(skillInstruction, request)) > 0 {
			continue
		}
		if !skillPathsAllow(skillInstruction, request) {
			continue
		}
		if skillSelector.hasPromptTriggerHint(skillInstruction, request.Prompt) || skillSelector.hasPromptKeyword(skillInstruction, request.Prompt) {
			triggeredSkillInstructions = append(triggeredSkillInstructions, skillInstruction)
		}
	}
	return triggeredSkillInstructions
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
	skillSelector := SkillSelector{}
	blockedDecisions := []SkillSelectionDecision{}
	for _, skillInstruction := range skillInstructions {
		if existingDecisionByName[skillInstruction.Name] {
			continue
		}
		if skillHiddenFromRequester(skillInstruction, request.RequesterCircles) {
			continue
		}
		skillDecision := skillSelector.Evaluate(skillInstruction, request, profileName)
		if skillDecision.Status == "skipped" && skillDecision.Reason != "no_trigger_matched" {
			blockedDecisions = append(blockedDecisions, skillDecision)
		}
	}
	return blockedDecisions
}

func retrieveSkillCandidates(ctx context.Context, request AgentRequest, skillInstructions []SkillInstruction, skillRetriever SkillRetriever) SkillRetrievalResult {
	if skillRetriever != nil {
		return skillRetriever.Retrieve(ctx, request, skillInstructions, maxSkillIndexCandidateCount)
	}
	return retrieveSkillsWithBM25(request, skillInstructions, maxSkillIndexCandidateCount, "embedding_unconfigured")
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

func skillDecisionForCandidate(skillInstruction SkillInstruction, skillDecision SkillSelectionDecision, skillCandidate SkillCandidate, profileName string) SkillSelectionDecision {
	if skillDecision.Status == "selected" || skillCandidate.Reason == "direct_skill_name" || skillCandidate.Score >= minimumSelectionScoreForCandidate(skillCandidate) {
		return SkillSelectionDecision{
			Name:        skillInstruction.Name,
			Status:      "selected",
			Reason:      skillCandidate.Reason,
			ProfileName: profileName,
			Score:       skillCandidate.Score,
			Source:      skillInstruction.Source,
		}
	}
	skillDecision.Score = skillCandidate.Score
	if skillDecision.Reason == "no_trigger_matched" {
		skillDecision.Reason = "candidate_below_selection_threshold"
	}
	return skillDecision
}

func minimumSelectionScoreForCandidate(skillCandidate SkillCandidate) float64 {
	if skillCandidate.Reason == "bm25_fallback" {
		return minimumBM25SelectionScore
	}
	return minimumEmbeddingSelectionScore
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
	lines := []string{"Available skill index. Full instructions are loaded only for selected skills:"}
	for _, skillInstruction := range skillInstructions {
		lines = append(lines, "- "+compactSkillIndexLine(skillInstruction))
	}
	return strings.Join(lines, "\n")
}

func compactSkillIndexLine(skillInstruction SkillInstruction) string {
	parts := []string{skillInstruction.Name}
	if text := skillListText(skillInstruction); strings.TrimSpace(text) != "" {
		parts = append(parts, strings.TrimSpace(text))
	}
	return strings.Join(parts, ": ")
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
	return AgentTurnResult{TaskRun: blockedTaskRun, FinalReply: finalReply, ToolNames: toolNamesForEvent(request.ToolSet)}, nil
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
