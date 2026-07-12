package agent

import (
	"context"
	"strings"
)

func promoteIntakeDecisionForSelectedSkills(decision IntakeDecision, instructionBundle InstructionBundle, toolSet *ToolSet, options IntakeOptions) IntakeDecision {
	decision = applySkillTaskLevelFloor(decision, instructionBundle, options.SkillTaskLevelFloor)
	defaultTaskLevel := options.DefaultTaskLevel
	if !canPromoteIntakeDecisionForSelectedSkills(decision) || !selectedSkillsNeedBoundedExecution(instructionBundle, toolSet, decision.Classification) {
		return decision
	}
	decision.Classification = IntakeClassificationBoundedTask
	if decision.TaskShape == "" || decision.TaskShape == TaskShapeImmediateReply || decision.TaskShape == TaskShapeApprovalGatedTask || decision.UsedDeterministicFallback {
		decision.TaskShape = taskShapeForSelectedSkills(instructionBundle)
	}
	decision.TaskLevel = LargerTaskLevel(decision.TaskLevel, defaultTaskLevel)
	decision.Reason = "selected skill requires bounded completion evidence"
	decision.UserFacingReply = ""
	// The promotion itself came from a selected skill's completion contract, not from the
	// intake model's own judgment, so that contract's evidence requirements have to become
	// hard requirements here directly. Leaving them as advisory hints would let the finish
	// gate accept a completion with no independent corroborating signal (e.g. a requested
	// attachment suffix) to promote the hint on its own, defeating the whole promotion.
	for _, skillInstruction := range selectedSkillInstructionList(instructionBundle) {
		if !selectedSkillRequiresAllowedCompletionEvidence(skillInstruction, toolSet) {
			continue
		}
		decision.RequiredEvidenceTools = appendUniqueStrings(decision.RequiredEvidenceTools, skillInstruction.Completion.RequiredEvidenceTools...)
		decision.RequestedOutputFormats = appendUniqueStrings(decision.RequestedOutputFormats, attachmentSuffixFormats(skillInstruction.Completion.RequiredAttachmentSuffixes)...)
	}
	return decision
}

var taskLevelFloorBySelectedSkillName = map[string]TaskLevel{
	"presentation": TaskLevelXHigh,
	"website":      TaskLevelXHigh,
}

func applySkillTaskLevelFloor(decision IntakeDecision, instructionBundle InstructionBundle, defaultSkillFloor TaskLevel) IntakeDecision {
	selectedSkillInstructions := selectedSkillInstructionList(instructionBundle)
	selectedSkillNames := make([]string, 0, len(selectedSkillInstructions))
	for _, skillInstruction := range selectedSkillInstructions {
		selectedSkillNames = append(selectedSkillNames, skillInstruction.Name)
	}
	decision.TaskLevel = LargerTaskLevel(decision.TaskLevel, taskLevelFloorForSelectedSkillNames(selectedSkillNames))
	for _, skillInstruction := range selectedSkillInstructions {
		if !selectedSkillRequiresCompletionEvidence(skillInstruction) {
			continue
		}
		if defaultSkillFloor == "" {
			continue
		}
		decision.TaskLevel = LargerTaskLevel(decision.TaskLevel, defaultSkillFloor)
	}
	return decision
}

func taskLevelFloorForSelectedSkillNames(skillNames []string) TaskLevel {
	taskLevelFloor := TaskLevel("")
	for _, skillName := range skillNames {
		programmedFloor := taskLevelFloorBySelectedSkillName[strings.ToLower(strings.TrimSpace(skillName))]
		taskLevelFloor = LargerTaskLevel(taskLevelFloor, programmedFloor)
	}
	return taskLevelFloor
}

func attachmentSuffixFormats(suffixes []string) []string {
	formats := []string{}
	for _, suffix := range suffixes {
		formats = append(formats, strings.TrimPrefix(strings.ToLower(strings.TrimSpace(suffix)), "."))
	}
	return normalizeRequestedOutputFormats(formats)
}

func canPromoteIntakeDecisionForSelectedSkills(decision IntakeDecision) bool {
	if decision.UsedDeterministicFallback {
		return true
	}
	switch decision.Classification {
	case IntakeClassificationQuickReply, IntakeClassificationNeedsConfirmation, IntakeClassificationUnsupported:
		return true
	default:
		return false
	}
}

func taskShapeForSelectedSkills(instructionBundle InstructionBundle) TaskShape {
	for _, skillInstruction := range selectedSkillInstructionList(instructionBundle) {
		if skillSupportsToolPrefix(skillInstruction, "schedule.") {
			return TaskShapeScheduledTask
		}
	}
	return TaskShapeResearchTask
}

func selectedSkillsNeedBoundedExecution(instructionBundle InstructionBundle, toolSet *ToolSet, classification IntakeClassification) bool {
	for _, skillInstruction := range selectedSkillInstructionList(instructionBundle) {
		if classification == IntakeClassificationQuickReply {
			if selectedSkillRequiresAllowedCompletionEvidence(skillInstruction, toolSet) {
				return true
			}
			continue
		}
		if selectedSkillRequiresAllowedCompletionEvidence(skillInstruction, toolSet) {
			return true
		}
		if artifactSkillCanRecoverIntakeRefusal(classification, SkillToolNames(skillInstruction)) {
			return true
		}
	}
	return false
}

func selectedSkillInstructionList(instructionBundle InstructionBundle) []SkillInstruction {
	selectedSkillNames := selectedSkillNameSet(instructionBundle.SkillDecisions)
	skillInstructions := []SkillInstruction{}
	for _, skillInstruction := range instructionBundle.Skills {
		if selectedSkillNames[skillInstruction.Name] {
			skillInstructions = append(skillInstructions, skillInstruction)
		}
	}
	return skillInstructions
}

func selectedSkillRequiresCompletionEvidence(skillInstruction SkillInstruction) bool {
	return len(skillInstruction.Completion.RequiredEvidenceTools) > 0 ||
		len(skillInstruction.Completion.RequiredAttachmentSuffixes) > 0
}

func selectedSkillRequiresAllowedCompletionEvidence(skillInstruction SkillInstruction, toolSet *ToolSet) bool {
	if !selectedSkillRequiresCompletionEvidence(skillInstruction) {
		return false
	}
	for _, toolName := range skillInstruction.Completion.RequiredEvidenceTools {
		if toolSet == nil || !toolSet.IsAllowed(toolName) {
			return false
		}
	}
	return true
}

func artifactSkillCanRecoverIntakeRefusal(classification IntakeClassification, allowedTools []string) bool {
	if classification != IntakeClassificationUnsupported && classification != IntakeClassificationNeedsConfirmation {
		return false
	}
	for _, toolName := range allowedTools {
		switch strings.TrimSpace(toolName) {
		case "terminal.run", "file.write", "file.edit", FileDeliverToolName:
			return true
		}
	}
	return false
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
	dominantSkill := dominantArtifactSkill(request, candidateInstructions, candidateByName)
	contractArbitration, hasContractArbitration := skillSearchQueryRouter.ArbitrateContractSkills(ctx, request, candidateInstructions, candidateByName)
	contractSelectedSkillNames := stringSet(contractArbitration.SelectedSkillNames)
	for _, skillInstruction := range candidateInstructions {
		skillCandidate, isFound := candidateByName[skillInstruction.Name]
		if !isFound {
			continue
		}
		skillDecision := skillDecisionForCandidate(skillInstruction, skillCandidate, normalizedAgentProfileName(request.ProfileName))
		if hasContractArbitration {
			skillDecision = skillDecisionForArbitratedCandidate(skillInstruction, skillCandidate, contractSelectedSkillNames, normalizedAgentProfileName(request.ProfileName))
		}
		if skillDecision.Status == "selected" {
			availabilityDecision := skillAvailabilityDecision(skillInstruction, request, normalizedAgentProfileName(request.ProfileName))
			if availabilityDecision.Status == "skipped" && availabilityDecision.Reason != "no_trigger_matched" {
				skillDecision = availabilityDecision
				skillDecision.Score = skillCandidate.Score
			}
		}
		if !hasContractArbitration && skillDecision.Status == "selected" && shouldSkipDominatedArtifactSkill(skillInstruction, skillCandidate, dominantSkill, request) {
			skillDecision = skippedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "dominated_by_"+dominantSkill.Name, nil)
			skillDecision.Score = skillCandidate.Score
		}
		if !hasStructuredQueries && !hasContractArbitration && skillDecision.Status == "selected" && shouldSkipArtifactSkillForNonArtifactRequest(skillInstruction, skillCandidate, request) {
			skillDecision = skippedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "outside_artifact_request", nil)
			skillDecision.Score = skillCandidate.Score
		}
		if !hasContractArbitration && skillDecision.Status == "selected" && shouldSkipArtifactSkillOutsideContract(skillInstruction, skillCandidate, request) {
			skillDecision = skippedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "outside_artifact_contract", nil)
			skillDecision.Score = skillCandidate.Score
		}
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

func instructionBundleWithPinnedSkills(instructionBundle InstructionBundle, request AgentRequest) InstructionBundle {
	pinnedSkillNames := stringSet(request.PinnedSkillNames)
	if len(pinnedSkillNames) == 0 {
		return instructionBundle
	}
	selectedSkillName := selectedSkillNames(instructionBundle.SkillDecisions)
	pinnedSkillInstructions := []SkillInstruction{}
	for _, skillInstruction := range instructionBundle.Skills {
		if !pinnedSkillNames[skillInstruction.Name] || selectedSkillName[skillInstruction.Name] {
			continue
		}
		pinnedSkillInstructions = append(pinnedSkillInstructions, skillInstruction)
		instructionBundle.SkillDecisions = append(instructionBundle.SkillDecisions, selectedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "manual_require"))
		instructionBundle.Sources = append(instructionBundle.Sources, skillInstruction.Source)
	}
	if len(pinnedSkillInstructions) == 0 {
		return instructionBundle
	}
	instructionBundle.Prompt = strings.Join(nonEmptyStrings([]string{
		instructionBundle.Prompt,
		buildSelectedSkillInstructionPrompt(pinnedSkillInstructions),
	}), "\n\n")
	return instructionBundle
}

func dominantArtifactSkill(request AgentRequest, skillInstructions []SkillInstruction, candidateByName map[string]SkillCandidate) SkillInstruction {
	contracts := artifactContractRequirementsForRequest(request)
	if len(contracts) == 0 {
		return SkillInstruction{}
	}
	dominantSkill := SkillInstruction{}
	dominantCandidate := SkillCandidate{}
	for _, skillInstruction := range skillInstructions {
		skillCandidate := candidateByName[skillInstruction.Name]
		if !artifactSkillCandidateQualifies(skillCandidate) || !skillMatchesAnyArtifactContract(skillInstruction, contracts) {
			continue
		}
		if dominantSkill.Name == "" || skillCandidate.Score > dominantCandidate.Score {
			dominantSkill = skillInstruction
			dominantCandidate = skillCandidate
		}
	}
	return dominantSkill
}

func requestLooksLikeArtifactSkillRequest(request AgentRequest) bool {
	if len(artifactContractRequirementsForRequest(request)) > 0 {
		return true
	}
	if expectedResultIncludesType(request.ActiveGoal.OutcomeContract, ExpectedResultTypeFile) || expectedResultIncludesType(request.ActiveGoal.OutcomeContract, ExpectedResultTypeLink) {
		return true
	}
	if activeGoalRequiresToolPrefix(request.ActiveGoal, "site.") || activeGoalRequiresTool(request.ActiveGoal, FileDeliverToolName) {
		return true
	}
	text := strings.ToLower(strings.Join(nonEmptyStrings([]string{
		skillSelectionPrompt(request),
		request.ActiveGoal.OriginalInstruction,
		request.ActiveGoal.CurrentObjective,
	}), "\n"))
	return containsAny(text, []string{
		"피피티", "발표자료", "프레젠테이션", "슬라이드", "ppt", "pptx", "deck", "slides",
		"웹사이트", "웹 앱", "웹앱", "홈페이지", "사이트", "프로토타입", "website", "web app", "webpage",
		"파일", "첨부", "문서", "보고서", "pdf", "docx", "xlsx", "csv", "html",
	})
}

func artifactSkillCandidateQualifies(skillCandidate SkillCandidate) bool {
	return skillCandidate.Name != "" && skillCandidate.Score >= minimumSelectionScoreForCandidate(skillCandidate)
}

func shouldSkipDominatedArtifactSkill(skillInstruction SkillInstruction, skillCandidate SkillCandidate, dominantSkill SkillInstruction, request AgentRequest) bool {
	if dominantSkill.Name == "" || skillInstruction.Name == dominantSkill.Name {
		return false
	}
	if !artifactSkillsOverlapContract(skillInstruction, dominantSkill, artifactContractRequirementsForRequest(request)) {
		return false
	}
	return skillCandidate.Reason != "direct_skill_name"
}

func artifactSkillsOverlapContract(leftSkill SkillInstruction, rightSkill SkillInstruction, contracts []artifactContractRequirement) bool {
	for _, contract := range contracts {
		if skillMatchesArtifactContract(leftSkill, contract) && skillMatchesArtifactContract(rightSkill, contract) {
			return true
		}
	}
	return false
}

func shouldSkipArtifactSkillOutsideContract(skillInstruction SkillInstruction, skillCandidate SkillCandidate, request AgentRequest) bool {
	if skillCandidate.Reason == "direct_skill_name" || strings.TrimSpace(skillCandidate.Name) == "" {
		return false
	}
	contracts := artifactContractRequirementsForRequest(request)
	if len(contracts) == 0 {
		return false
	}
	return !skillMatchesAnyArtifactContract(skillInstruction, contracts)
}

func shouldSkipArtifactSkillForNonArtifactRequest(skillInstruction SkillInstruction, skillCandidate SkillCandidate, request AgentRequest) bool {
	if skillCandidate.Reason == "direct_skill_name" || strings.TrimSpace(skillCandidate.Name) == "" {
		return false
	}
	if requestLooksLikeArtifactSkillRequest(request) {
		return false
	}
	return skillSupportsSiteArtifact(skillInstruction) || skillSupportsFileDelivery(skillInstruction)
}

func alwaysSelectedSkillInstructions(skillInstructions []SkillInstruction, request AgentRequest, profileName string, existingSkillDecisions []SkillSelectionDecision) []SkillInstruction {
	existingDecisionByName := map[string]bool{}
	for _, skillDecision := range existingSkillDecisions {
		existingDecisionByName[skillDecision.Name] = true
	}
	alwaysSelectedSkills := []SkillInstruction{}
	for _, skillInstruction := range skillInstructions {
		if !isAlwaysSelectedSupportSkill(skillInstruction) || existingDecisionByName[skillInstruction.Name] {
			continue
		}
		if skillAvailabilityDecision(skillInstruction, request, profileName).Status == "selected" {
			alwaysSelectedSkills = append(alwaysSelectedSkills, skillInstruction)
		}
	}
	return alwaysSelectedSkills
}

func isAlwaysSelectedSupportSkill(skillInstruction SkillInstruction) bool {
	if !skillSupportsToolPrefix(skillInstruction, "ask.") {
		return false
	}
	text := skillContractSearchText(skillInstruction)
	return strings.Contains(text, "always available") ||
		skillTextContainsAny(text, []string{"confirmation", "confirm", "bounded choice", "free-form input", "ask the user", "사용자", "확인", "선택", "입력"})
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

func VisibleSkillInstructionsForRequester(skillInstructions []SkillInstruction, requesterCircles []string) []SkillInstruction {
	visibleSkillInstructions := []SkillInstruction{}
	for _, skillInstruction := range skillInstructions {
		if skillHiddenFromRequester(skillInstruction, requesterCircles) {
			continue
		}
		visibleSkillInstructions = append(visibleSkillInstructions, skillInstruction)
	}
	return visibleSkillInstructions
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
		querySet = normalizeSkillSearchQuerySet(augmentSkillSearchQuerySetForArtifactContract(querySet, request))
		if len(querySet.Queries) == 0 {
			return SkillRetrievalResult{RetrievalMode: "structured_query", IndexStatus: "empty_query"}
		}
	}
	var retrievalResult SkillRetrievalResult
	if skillRetriever != nil {
		if hasStructuredQueries {
			retrievalResult = skillRetriever.Search(ctx, request, skillInstructions, querySet, maxSkillIndexCandidateCount)
		} else {
			retrievalResult = skillRetriever.Retrieve(ctx, request, skillInstructions, maxSkillIndexCandidateCount)
		}
		return augmentSkillRetrievalResultForArtifactContract(request, skillInstructions, retrievalResult, maxSkillIndexCandidateCount)
	}
	if hasStructuredQueries {
		retrievalResult = retrieveSkillsWithBM25(request, skillInstructions, skillSearchQueryText(querySet), maxSkillIndexCandidateCount, "embedding_unconfigured")
		return augmentSkillRetrievalResultForArtifactContract(request, skillInstructions, retrievalResult, maxSkillIndexCandidateCount)
	}
	retrievalResult = retrieveSkillsWithBM25(request, skillInstructions, skillSelectionPrompt(request), maxSkillIndexCandidateCount, "embedding_unconfigured")
	return augmentSkillRetrievalResultForArtifactContract(request, skillInstructions, retrievalResult, maxSkillIndexCandidateCount)
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

func skillDecisionForArbitratedCandidate(skillInstruction SkillInstruction, skillCandidate SkillCandidate, selectedSkillNames map[string]bool, profileName string) SkillSelectionDecision {
	if selectedSkillNames[skillInstruction.Name] {
		skillDecision := selectedSkillDecision(skillInstruction, profileName, "contract_arbitration")
		skillDecision.Score = skillCandidate.Score
		return skillDecision
	}
	skillDecision := skippedSkillDecision(skillInstruction, profileName, "not_selected_by_contract_arbitration", nil)
	skillDecision.Score = skillCandidate.Score
	return skillDecision
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
	return len([]rune(strings.TrimSpace(prompt))) <= 20
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
		"Multiple skills may be selected at once, but only use the ones this specific request actually needs. Mentioning a topic (e.g. email, calendar, browsing) is not the same as being asked to act on it — ignore skills whose subject matter is not the actual task.",
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
