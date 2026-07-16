package agent

import (
	"context"
	"strings"
)

func applySelectedSkillCompletionRequirements(decision IntakeDecision, instructionBundle InstructionBundle) IntakeDecision {
	if decision.Classification != IntakeClassificationBoundedTask {
		return decision
	}
	for _, skillInstruction := range selectedSkillInstructionList(instructionBundle) {
		if !selectedSkillRequiresCompletionEvidence(skillInstruction) {
			continue
		}
		decision.RequiredEvidenceTools = appendUniqueStrings(decision.RequiredEvidenceTools, skillInstruction.Completion.RequiredEvidenceTools...)
		decision.RequestedOutputFormats = appendUniqueStrings(decision.RequestedOutputFormats, attachmentSuffixFormats(skillInstruction.Completion.RequiredAttachmentSuffixes)...)
	}
	return decision
}

func attachmentSuffixFormats(suffixes []string) []string {
	formats := []string{}
	for _, suffix := range suffixes {
		formats = append(formats, strings.TrimPrefix(strings.ToLower(strings.TrimSpace(suffix)), "."))
	}
	return normalizeRequestedOutputFormats(formats)
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
		if !hasContractArbitration && skillDecision.Status == "selected" && shouldSkipArtifactSkillForNonArtifactRequest(skillInstruction, skillCandidate, request) {
			skillDecision = skippedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "outside_artifact_request", nil)
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

func shouldSkipArtifactSkillForNonArtifactRequest(skillInstruction SkillInstruction, skillCandidate SkillCandidate, request AgentRequest) bool {
	if skillCandidate.Reason == "direct_skill_name" || strings.TrimSpace(skillCandidate.Name) == "" {
		return false
	}
	if strings.TrimSpace(request.ActiveGoal.OutcomeContract.ArtifactRequirement) != ArtifactRequirementNone {
		return false
	}
	return skillSupportsSiteArtifact(skillInstruction) || skillSupportsFileDelivery(skillInstruction)
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
		querySet = skillRetrievalQuerySet(request, querySet)
	}
	var retrievalResult SkillRetrievalResult
	if skillRetriever != nil {
		if hasStructuredQueries {
			retrievalResult = skillRetriever.Search(ctx, request, skillInstructions, querySet, maxSkillIndexCandidateCount)
		} else {
			retrievalResult = skillRetriever.Retrieve(ctx, request, skillInstructions, maxSkillIndexCandidateCount)
		}
		return retrievalResult
	}
	if hasStructuredQueries {
		retrievalResult = retrieveSkillsWithBM25QuerySet(request, skillInstructions, querySet, maxSkillIndexCandidateCount, "embedding_unconfigured")
		return retrievalResult
	}
	retrievalResult = retrieveSkillsWithBM25(request, skillInstructions, skillSelectionPrompt(request), maxSkillIndexCandidateCount, "embedding_unconfigured")
	return retrievalResult
}

func skillRetrievalQuerySet(request AgentRequest, supplementalQueries SkillSearchQuerySet) SkillSearchQuerySet {
	queries := []SkillSearchQuery{{Description: strings.TrimSpace(request.Prompt)}}
	queries = append(queries, supplementalQueries.Queries...)
	return normalizeSkillSearchQuerySet(SkillSearchQuerySet{Queries: queries})
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

func skillSelectionPrompt(request AgentRequest) string {
	return strings.TrimSpace(request.Prompt)
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
