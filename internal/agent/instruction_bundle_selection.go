package agent

import (
	"context"
	"strings"
)

func promoteIntakeDecisionForSelectedSkills(decision IntakeDecision, instructionBundle InstructionBundle, defaultEffortLevel EffortLevel) IntakeDecision {
	if !canPromoteIntakeDecisionForSelectedSkills(decision) || !selectedSkillsNeedBoundedExecution(instructionBundle, decision.Classification) {
		return decision
	}
	decision.Classification = IntakeClassificationBoundedTask
	if decision.TaskShape == "" || decision.TaskShape == TaskShapeImmediateReply || decision.TaskShape == TaskShapeApprovalGatedTask || decision.UsedDeterministicFallback {
		decision.TaskShape = taskShapeForSelectedSkills(instructionBundle)
	}
	decision.EffortLevel = LargerEffortLevel(decision.EffortLevel, defaultEffortLevel)
	decision.Reason = "selected skill requires bounded completion evidence"
	decision.UserFacingReply = ""
	return decision
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
		case "terminal.run", "file.write", "file.edit", "file.patch", "file.promote", "file.attach":
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
	dominantSkillName := dominantArtifactSkillName(request, candidateByName)
	candidateInstructions := visibleCandidateSkillInstructions(candidateSkillInstructions(instructionBundle.Skills, retrievalResult.SelectedCandidates), candidateByName, request.RequesterCircles)
	for _, skillInstruction := range candidateInstructions {
		skillCandidate, isFound := candidateByName[skillInstruction.Name]
		if !isFound {
			continue
		}
		skillDecision := skillDecisionForCandidate(skillInstruction, skillCandidate, normalizedAgentProfileName(request.ProfileName))
		if skillDecision.Status == "selected" {
			availabilityDecision := skillAvailabilityDecision(skillInstruction, request, normalizedAgentProfileName(request.ProfileName))
			if availabilityDecision.Status == "skipped" && availabilityDecision.Reason != "no_trigger_matched" {
				skillDecision = availabilityDecision
				skillDecision.Score = skillCandidate.Score
			}
		}
		if skillDecision.Status == "selected" && shouldSkipDominatedArtifactSkill(skillInstruction.Name, skillCandidate, dominantSkillName) {
			skillDecision = skippedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "dominated_by_"+dominantSkillName, nil)
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

func dominantArtifactSkillName(request AgentRequest, candidateByName map[string]SkillCandidate) string {
	if !requestLooksLikeArtifactSkillRequest(request) {
		return ""
	}
	siteCandidate := candidateByName["site-prototype"]
	slidesCandidate := candidateByName["simple-slides"]
	isSiteQualified := artifactSkillCandidateQualifies(siteCandidate)
	isSlidesQualified := artifactSkillCandidateQualifies(slidesCandidate)
	if isSiteQualified && expectedResultIncludesType(request.ActiveGoal.OutcomeContract, "link") {
		return "site-prototype"
	}
	if isSiteQualified && (!isSlidesQualified || siteCandidate.Score > slidesCandidate.Score) {
		return "site-prototype"
	}
	if isSlidesQualified && (!isSiteQualified || slidesCandidate.Score > siteCandidate.Score) {
		return "simple-slides"
	}
	return ""
}

func requestLooksLikeArtifactSkillRequest(request AgentRequest) bool {
	if requestPromptMatchesWorkflowKind(request, WorkKindFlowTask) && !requestHasWorkKind(request, WorkKindSitePrototype) && !requestHasWorkKind(request, WorkKindSlidesArtifact) && !requestHasWorkKind(request, WorkKindFileDelivery) {
		return false
	}
	if requestHasWorkKind(request, WorkKindSitePrototype) || requestHasWorkKind(request, WorkKindSlidesArtifact) || requestHasWorkKind(request, WorkKindFileDelivery) {
		return true
	}
	if expectedResultIncludesType(request.ActiveGoal.OutcomeContract, ExpectedResultTypeFile) || expectedResultIncludesType(request.ActiveGoal.OutcomeContract, ExpectedResultTypeLink) {
		return true
	}
	if activeGoalRequiresToolPrefix(request.ActiveGoal, "site.app.") || activeGoalRequiresTool(request.ActiveGoal, "file.attach") {
		return true
	}
	text := strings.ToLower(strings.Join(nonEmptyStrings([]string{
		request.Prompt,
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

func shouldSkipDominatedArtifactSkill(skillName string, skillCandidate SkillCandidate, dominantSkillName string) bool {
	if dominantSkillName == "" || skillName == dominantSkillName {
		return false
	}
	return skillCandidate.Reason != "direct_skill_name"
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
