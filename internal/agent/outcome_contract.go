package agent

import (
	"strings"

	"blueclaw/internal/task"
)

const siteRequirementNormalizationEventName = "agent.site_requirement_normalized"

type siteRequirementNormalizationReport struct {
	Source                       string     `json:"source,omitempty"`
	Reason                       string     `json:"reason,omitempty"`
	SiteEvidenceQuote            string     `json:"siteEvidenceQuote,omitempty"`
	DroppedWorkKinds             []string   `json:"droppedWorkKinds,omitempty"`
	DroppedExpectedResultIDs     []string   `json:"droppedExpectedResultIDs,omitempty"`
	DroppedRequiredEvidenceTools []string   `json:"droppedRequiredEvidenceTools,omitempty"`
	DroppedRequiredEvidenceAnyOf [][]string `json:"droppedRequiredEvidenceAnyOf,omitempty"`
	DroppedSelectedEvidenceHints []string   `json:"droppedSelectedEvidenceHints,omitempty"`
}

func (report siteRequirementNormalizationReport) HasDrops() bool {
	return len(report.DroppedWorkKinds) > 0 ||
		len(report.DroppedExpectedResultIDs) > 0 ||
		len(report.DroppedRequiredEvidenceTools) > 0 ||
		len(report.DroppedRequiredEvidenceAnyOf) > 0 ||
		len(report.DroppedSelectedEvidenceHints) > 0
}

func normalizeTurnDecisionSiteRequirement(request AgentRequest, decision TurnDecision) (TurnDecision, siteRequirementNormalizationReport) {
	intakeDecision := decision.IntakeDecision()
	intakeDecision, report := normalizeIntakeDecisionSiteRequirement(request.Prompt, intakeDecision, "intake")
	decision.ExpectedResults = intakeDecision.ExpectedResults
	decision.SiteRequestEvidence = intakeDecision.SiteRequestEvidence
	decision.WorkKinds = intakeDecision.WorkKinds
	return decision, report
}

func normalizeIntakeDecisionSiteRequirement(currentUserMessage string, decision IntakeDecision, source string) (IntakeDecision, siteRequirementNormalizationReport) {
	decision.SiteRequestEvidence = strings.TrimSpace(decision.SiteRequestEvidence)
	if !intakeDecisionRequiresSiteEvidence(decision) {
		decision.SiteRequestEvidence = ""
		return decision, siteRequirementNormalizationReport{}
	}
	if siteEvidenceQuoteMatchesMessage(decision.SiteRequestEvidence, currentUserMessage) {
		return decision, siteRequirementNormalizationReport{}
	}
	report := siteRequirementNormalizationReport{
		Source:            source,
		Reason:            "site evidence quote does not verify against current user message",
		SiteEvidenceQuote: decision.SiteRequestEvidence,
	}
	decision.WorkKinds, report.DroppedWorkKinds = removeSiteWorkKinds(decision.WorkKinds)
	decision.ExpectedResults, report.DroppedExpectedResultIDs = removeSiteExpectedResults(decision.ExpectedResults)
	decision.SiteRequestEvidence = ""
	return decision, report
}

func intakeDecisionRequiresSiteEvidence(decision IntakeDecision) bool {
	return workKindsContain(decision.WorkKinds, WorkKindSitePrototype) || expectedResultsIncludeSiteRequirement(decision.ExpectedResults)
}

func normalizeActiveGoalSiteRequirement(activeGoal ActiveGoal, currentUserMessage string) (ActiveGoal, siteRequirementNormalizationReport) {
	if !activeGoalRequiresSiteEvidence(activeGoal) {
		return activeGoal, siteRequirementNormalizationReport{}
	}
	if siteEvidenceQuoteMatchesMessage(activeGoal.OutcomeContract.SiteEvidenceQuote, activeGoal.OriginalInstruction) {
		return activeGoal, siteRequirementNormalizationReport{}
	}
	if siteEvidenceQuoteMatchesMessage(activeGoal.OutcomeContract.SiteEvidenceQuote, currentUserMessage) {
		return activeGoal, siteRequirementNormalizationReport{}
	}
	report := siteRequirementNormalizationReport{
		Source:            "active_goal",
		Reason:            "stored site evidence quote does not verify against original instruction or current user message",
		SiteEvidenceQuote: activeGoal.OutcomeContract.SiteEvidenceQuote,
	}
	activeGoal.WorkKinds, report.DroppedWorkKinds = removeSiteWorkKinds(activeGoal.WorkKinds)
	activeGoal.OutcomeContract, report = stripOutcomeContractSiteRequirements(activeGoal.OutcomeContract, report)
	return activeGoal, report
}

func activeGoalRequiresSiteEvidence(activeGoal ActiveGoal) bool {
	return workKindsContain(activeGoal.WorkKinds, WorkKindSitePrototype) || outcomeContractHasSiteRequirement(activeGoal.OutcomeContract)
}

func outcomeContractHasSiteRequirement(contract OutcomeContract) bool {
	return contractMentionsToolPrefix(contract, "site.app.") || expectedResultsIncludeSiteRequirement(contract.ExpectedResults)
}

func stripOutcomeContractSiteRequirements(contract OutcomeContract, report siteRequirementNormalizationReport) (OutcomeContract, siteRequirementNormalizationReport) {
	contract.RequiredEvidenceTools, report.DroppedRequiredEvidenceTools = removeToolNamePrefix(contract.RequiredEvidenceTools, "site.app.")
	contract.RequiredEvidenceAnyOf, report.DroppedRequiredEvidenceAnyOf = removeToolNamePrefixGroups(contract.RequiredEvidenceAnyOf, "site.app.")
	contract.SelectedEvidenceHints, report.DroppedSelectedEvidenceHints = removeToolNamePrefix(contract.SelectedEvidenceHints, "site.app.")
	contract.ExpectedResults, report.DroppedExpectedResultIDs = removeSiteExpectedResults(contract.ExpectedResults)
	contract.SiteEvidenceQuote = ""
	return normalizeOutcomeContract(contract), report
}

func removeSiteWorkKinds(workKinds []string) ([]string, []string) {
	filteredWorkKinds := []string{}
	droppedWorkKinds := []string{}
	for _, workKind := range workKinds {
		if strings.TrimSpace(workKind) == WorkKindSitePrototype {
			droppedWorkKinds = appendUniqueStrings(droppedWorkKinds, WorkKindSitePrototype)
			continue
		}
		filteredWorkKinds = appendUniqueStrings(filteredWorkKinds, workKind)
	}
	return filteredWorkKinds, droppedWorkKinds
}

func removeSiteExpectedResults(results []ExpectedResult) ([]ExpectedResult, []string) {
	filteredResults := []ExpectedResult{}
	droppedResultIDs := []string{}
	for _, result := range results {
		if expectedResultIsSiteRequirement(result) {
			droppedResultIDs = appendUniqueStrings(droppedResultIDs, firstNonEmptyString(result.ID, result.Description))
			continue
		}
		filteredResults = append(filteredResults, result)
	}
	return normalizeExpectedResults(filteredResults), droppedResultIDs
}

func expectedResultsIncludeSiteRequirement(results []ExpectedResult) bool {
	for _, result := range results {
		if expectedResultIsSiteRequirement(result) {
			return true
		}
	}
	return false
}

func expectedResultIsSiteRequirement(result ExpectedResult) bool {
	if strings.TrimSpace(result.Type) == ExpectedResultTypeLink {
		return true
	}
	text := strings.ToLower(strings.Join(append([]string{result.ID, result.Description}, result.AcceptanceHints...), " "))
	for _, fragment := range []string{"site", "website", "web app", "webpage", "public url", "웹사이트", "홈페이지", "웹 앱", "웹앱"} {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

func removeToolNamePrefix(toolNames []string, prefix string) ([]string, []string) {
	filteredToolNames := []string{}
	droppedToolNames := []string{}
	for _, toolName := range toolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if strings.HasPrefix(trimmedToolName, prefix) {
			droppedToolNames = appendUniqueStrings(droppedToolNames, trimmedToolName)
			continue
		}
		filteredToolNames = appendUniqueStrings(filteredToolNames, trimmedToolName)
	}
	return filteredToolNames, droppedToolNames
}

func removeToolNamePrefixGroups(groups [][]string, prefix string) ([][]string, [][]string) {
	filteredGroups := [][]string{}
	droppedGroups := [][]string{}
	for _, group := range groups {
		filteredGroup, droppedGroup := removeToolNamePrefix(group, prefix)
		if len(droppedGroup) > 0 {
			droppedGroups = append(droppedGroups, droppedGroup)
		}
		if len(filteredGroup) > 0 {
			filteredGroups = append(filteredGroups, filteredGroup)
		}
	}
	return filteredGroups, droppedGroups
}

func siteEvidenceQuoteMatchesMessage(siteEvidenceQuote string, userMessage string) bool {
	normalizedQuote := trimSiteEvidencePunctuation(normalizeSiteEvidenceText(siteEvidenceQuote))
	normalizedMessage := normalizeSiteEvidenceText(userMessage)
	if normalizedQuote == "" || normalizedMessage == "" {
		return false
	}
	if strings.Contains(normalizedMessage, normalizedQuote) {
		return true
	}
	return strings.Contains(trimSiteEvidencePunctuation(normalizedMessage), normalizedQuote)
}

func normalizeSiteEvidenceText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func trimSiteEvidencePunctuation(value string) string {
	return strings.Trim(value, " \t\n\r\"'`.,!?;:()[]{}<>“”‘’…。！？、，")
}

func shouldBuildExecutionPlanForConfirmation(request AgentRequest, intakeDecision IntakeDecision, requiredEvidenceTools []string) bool {
	if intakeDecision.Classification != IntakeClassificationBoundedTask {
		return false
	}
	if requestIsNonDestructiveSitePrototypePublish(request, requiredEvidenceTools) {
		return false
	}
	if hasTool(request.ToolSet, "site.app.publish") && requestHasWorkKind(request, WorkKindDestructiveAction) {
		return true
	}
	if intakeDecision.TaskShape == TaskShapeApprovalGatedTask {
		return true
	}
	for _, toolName := range requiredEvidenceTools {
		if confirmationRiskyEvidenceTool(toolName) {
			return true
		}
	}
	return requestHasWorkKind(request, WorkKindDestructiveAction) ||
		requestHasWorkKind(request, WorkKindPaidService) ||
		requestHasWorkKind(request, WorkKindExternalSend)
}

func confirmationRiskyEvidenceTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "platform.message.send", "mail.message.send", "google.gmail.send", "slack.message.send":
		return true
	default:
		return false
	}
}

func requestIsNonDestructiveSitePrototypePublish(request AgentRequest, requiredEvidenceTools []string) bool {
	if !hasAllTools(request.ToolSet, []string{"site.app.create", "site.app.publish"}) {
		return false
	}
	if !requiredEvidenceContains(requiredEvidenceTools, "site.app.publish") && !hasTool(request.ToolSet, "site.app.publish") {
		return false
	}
	if !requestLooksLikeSitePrototypeWork(request) {
		return false
	}
	if requestHasWorkKind(request, WorkKindDestructiveAction) || requestHasWorkKind(request, WorkKindPaidService) {
		return false
	}
	return true
}

func requestLooksLikeSitePrototypeWork(request AgentRequest) bool {
	return requestHasWorkKind(request, WorkKindSitePrototype) || activeGoalRequiresToolPrefix(request.ActiveGoal, "site.app.")
}

func requestLooksLikeCalendarWork(request AgentRequest) bool {
	return requestHasWorkKind(request, WorkKindCalendar)
}

func requestLooksLikeSlidesArtifactWork(request AgentRequest) bool {
	return requestHasWorkKind(request, WorkKindSlidesArtifact)
}

func requestHasWorkKind(request AgentRequest, workKind string) bool {
	return workKindsContain(request.WorkKinds, workKind)
}

func toolSetForSelectedSkills(toolSet *ToolSet, instructionBundle InstructionBundle) *ToolSet {
	if toolSet == nil {
		return nil
	}
	return toolSet.WithAllowedToolNames(toolNamesForSelectedSkills(instructionBundle))
}

func turnToolSelectionIsConstrained(instructionBundle InstructionBundle, outcomeContract OutcomeContract) bool {
	for _, skillDecision := range instructionBundle.SkillDecisions {
		if skillDecision.Status == "selected" {
			return true
		}
	}
	return len(outcomeContractRequiredToolNames(outcomeContract)) > 0 ||
		len(outcomeContract.RequiredAttachmentSuffixes) > 0 ||
		strings.TrimSpace(outcomeContract.ArtifactRequirement) == ArtifactRequirementRequired ||
		strings.TrimSpace(outcomeContract.ArtifactRequirement) == ArtifactRequirementPreferred
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
	if stringSliceContains(request.PinnedToolNames, trimmedToolName) {
		return true
	}
	if activeGoalRequiresTool(request.ActiveGoal, trimmedToolName) {
		return true
	}
	if strings.HasPrefix(trimmedToolName, "site.app.") {
		return outcomeAllowsSiteTools(request, executionPlan, hasExecutionPlan, outcomeContract)
	}
	if isSendEvidenceTool(trimmedToolName) {
		return outcomeAllowsExternalSendTools(request, executionPlan, hasExecutionPlan, outcomeContract)
	}
	return true
}

func outcomeAllowsSiteTools(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
	return contractRequiresToolPrefix(outcomeContract, "site.app.") || (hasExecutionPlan && executionPlan.PublicDeploy) || requestLooksLikeSitePrototypeWork(request)
}

func selectedSkillToolShouldExpose(toolName string, selectedSkillToolNames map[string]bool, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if !selectedSkillToolNames[trimmedToolName] {
		return false
	}
	if strings.HasPrefix(trimmedToolName, "site.app.") {
		return outcomeAllowsSiteTools(request, executionPlan, hasExecutionPlan, outcomeContract)
	}
	if trimmedToolName == "artifact.review" {
		return outcomeAllowsVisualArtifactReview(request, outcomeContract)
	}
	if isSendEvidenceTool(trimmedToolName) {
		if activeGoalRequiresTool(request.ActiveGoal, trimmedToolName) {
			return true
		}
		return outcomeAllowsExternalSendTools(request, executionPlan, hasExecutionPlan, outcomeContract)
	}
	return true
}

func outcomeAllowsExternalSendTools(_ AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
	return contractRequiresSendTool(outcomeContract) ||
		(hasExecutionPlan && (executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend))
}

func outcomeAllowsVisualArtifactReview(request AgentRequest, outcomeContract OutcomeContract) bool {
	artifactRequirement := strings.TrimSpace(outcomeContract.ArtifactRequirement)
	return (artifactRequirement != "" && artifactRequirement != ArtifactRequirementNone) ||
		expectedResultIncludesType(outcomeContract, ExpectedResultTypeFile) ||
		expectedResultIncludesType(outcomeContract, ExpectedResultTypeLink) ||
		requestLooksLikeSitePrototypeWork(request) ||
		requestLooksLikeSlidesArtifactWork(request)
}

func toolNamesForAgentTurn(instructionBundle InstructionBundle, outcomeContract OutcomeContract, request AgentRequest) []string {
	toolNames := append([]string{}, universalAgentToolNames()...)
	selectedSkillName := selectedSkillNames(instructionBundle.SkillDecisions)
	pinnedSkillName := stringSet(request.PinnedSkillNames)
	for _, skillInstruction := range instructionBundle.Skills {
		if !selectedSkillName[skillInstruction.Name] && !pinnedSkillName[skillInstruction.Name] {
			continue
		}
		toolNames = appendUniqueStrings(toolNames, SkillToolNames(skillInstruction)...)
	}
	toolNames = appendUniqueStrings(toolNames, request.PinnedToolNames...)
	toolNames = appendUniqueStrings(toolNames, outcomeContractRequiredToolNames(outcomeContract)...)
	return toolNames
}

func selectedAndPinnedSkillToolNameSet(instructionBundle InstructionBundle, pinnedSkillNames []string) map[string]bool {
	toolNameByName := map[string]bool{}
	selectedSkillName := selectedSkillNames(instructionBundle.SkillDecisions)
	pinnedSkillName := stringSet(pinnedSkillNames)
	for _, skillInstruction := range instructionBundle.Skills {
		if !selectedSkillName[skillInstruction.Name] && !pinnedSkillName[skillInstruction.Name] {
			continue
		}
		for _, toolName := range SkillToolNames(skillInstruction) {
			trimmedToolName := strings.TrimSpace(toolName)
			if trimmedToolName != "" {
				toolNameByName[trimmedToolName] = true
			}
		}
	}
	return toolNameByName
}

func toolNamesForSelectedSkills(instructionBundle InstructionBundle) []string {
	toolNames := append([]string{}, universalAgentToolNames()...)
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

func pinnedSkillToolNames(instructionBundle InstructionBundle, skillNames []string) []string {
	pinnedSkillName := stringSet(skillNames)
	toolNames := []string{}
	for _, skillInstruction := range instructionBundle.Skills {
		if !pinnedSkillName[skillInstruction.Name] {
			continue
		}
		toolNames = appendUniqueStrings(toolNames, SkillToolNames(skillInstruction)...)
	}
	return toolNames
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			result[trimmedValue] = true
		}
	}
	return result
}

func universalAgentToolNames() []string {
	toolNames := append([]string{}, coreAgentToolNames()...)
	toolNames = appendUniqueStrings(toolNames, DefaultSkillToolNames()...)
	toolNames = appendUniqueStrings(toolNames, genericBuiltInToolNames()...)
	return toolNames
}

func coreAgentToolNames() []string {
	return []string{"skill.search", "tool.describe", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember"}
}

func genericBuiltInToolNames() []string {
	return []string{
		"math.calculate",
		"web.search",
		"web.fetch",
		"terminal.run",
		"terminal.session",
		"browser_handoff.openURL",
		"file.preview",
		"file.read",
		"file.write",
		"file.edit",
		"file.patch",
		"file.promote",
		"file.attach",
		"calendar.event.add",
		"calendar.event.delete",
		"skill.add",
		"skill.remove",
		"schedule.create",
		"schedule.update",
		"schedule.cancel",
	}
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

func selectedEvidenceToolsForContinuation(contract OutcomeContract, selectedEvidenceHints []string) []string {
	activeGoalHintByName := stringSet(contract.SelectedEvidenceHints)
	toolNames := []string{}
	for _, toolName := range selectedEvidenceHints {
		trimmedToolName := strings.TrimSpace(toolName)
		if activeGoalHintByName[trimmedToolName] {
			toolNames = appendUniqueStrings(toolNames, trimmedToolName)
		}
	}
	return toolNames
}

func selectedEvidenceToolsForRequestContinuation(request AgentRequest, contract OutcomeContract, selectedEvidenceHints []string) []string {
	requiredToolByName := stringSet(outcomeContractRequiredToolNames(contract))
	toolNames := []string{}
	for _, toolName := range selectedEvidenceHints {
		trimmedToolName := strings.TrimSpace(toolName)
		if requiredToolByName[trimmedToolName] {
			toolNames = appendUniqueStrings(toolNames, trimmedToolName)
			continue
		}
		if isSendEvidenceTool(trimmedToolName) && !requestLooksLikeExternalSendContinuation(request, contract) {
			continue
		}
		if isSendEvidenceTool(trimmedToolName) {
			toolNames = appendUniqueStrings(toolNames, trimmedToolName)
		}
	}
	return toolNames
}

func outcomeContractForRequest(request AgentRequest, intakeDecision IntakeDecision, instructionBundle InstructionBundle, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredAttachmentSuffixes []string) OutcomeContract {
	request.ActiveGoal, _ = normalizeActiveGoalSiteRequirement(request.ActiveGoal, request.Prompt)
	request = normalizeRequestSiteWorkKinds(request, intakeDecision)
	requiredAttachmentSuffixes = attachmentSuffixesForOutcomeContract(request, executionPlan, hasExecutionPlan, requiredAttachmentSuffixes)
	if activeGoalOutcomeContractHasRequirements(request.ActiveGoal.OutcomeContract) {
		contract := request.ActiveGoal.OutcomeContract
		selectedEvidenceHints := selectedEvidenceHintTools(instructionBundle)
		contract.SelectedEvidenceHints = appendUniqueStrings(contract.SelectedEvidenceHints, selectedEvidenceHints...)
		contract.SelectedEvidenceHints = filterStaleOutcomeHints(request, executionPlan, hasExecutionPlan, contract, contract.SelectedEvidenceHints)
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, selectedEvidenceToolsForRequestContinuation(request, contract, selectedEvidenceHints)...)
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, requiredSendEvidenceToolsForRequest(request, contract, contract.SelectedEvidenceHints)...)
		contract.ExpectedResults = appendExpectedResults(contract.ExpectedResults, legacyExpectedResultsForContract(request, intakeDecision, executionPlan, hasExecutionPlan, contract)...)
		if strings.TrimSpace(contract.ArtifactRequirement) == "" || contract.ArtifactRequirement == ArtifactRequirementNone {
			contract.ArtifactRequirement = artifactRequirementForOutcomeContract(intakeDecision, contract)
		}
		return sanitizeOutcomeContractForRequest(request, executionPlan, hasExecutionPlan, contract)
	}
	contract := OutcomeContract{
		SelectedEvidenceHints:      appendUniqueStrings(outcomeContractToolNames(request.ActiveGoal.OutcomeContract), selectedEvidenceHintTools(instructionBundle)...),
		RequiredAttachmentSuffixes: append([]string{}, requiredAttachmentSuffixes...),
	}
	contract.RequiredEvidenceTools = outcomeEvidenceTools(request, intakeDecision, executionPlan, hasExecutionPlan, contract.SelectedEvidenceHints, requiredAttachmentSuffixes)
	contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, requiredSendEvidenceToolsForRequest(request, contract, contract.SelectedEvidenceHints)...)
	contract.SelectedEvidenceHints = filterStaleOutcomeHints(request, executionPlan, hasExecutionPlan, contract, contract.SelectedEvidenceHints)
	if len(requiredAttachmentSuffixes) > 0 {
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, "file.attach")
	}
	contract.ExpectedResults = expectedResultsForRequest(request, intakeDecision, executionPlan, hasExecutionPlan, contract.RequiredEvidenceTools, requiredAttachmentSuffixes)
	contract.ArtifactRequirement = artifactRequirementForOutcomeContract(intakeDecision, contract)
	contract.SiteEvidenceQuote = siteEvidenceQuoteForOutcomeContract(request, intakeDecision, executionPlan, hasExecutionPlan, contract)
	contract.Source = outcomeContractSource(hasExecutionPlan, requiredAttachmentSuffixes)
	return sanitizeOutcomeContractForRequest(request, executionPlan, hasExecutionPlan, contract)
}

func normalizeRequestSiteWorkKinds(request AgentRequest, intakeDecision IntakeDecision) AgentRequest {
	if !workKindsContain(request.WorkKinds, WorkKindSitePrototype) {
		return request
	}
	if siteEvidenceQuoteMatchesMessage(intakeDecision.SiteRequestEvidence, request.Prompt) {
		return request
	}
	if siteEvidenceQuoteMatchesMessage(request.ActiveGoal.OutcomeContract.SiteEvidenceQuote, request.ActiveGoal.OriginalInstruction) {
		return request
	}
	if siteEvidenceQuoteMatchesMessage(request.ActiveGoal.OutcomeContract.SiteEvidenceQuote, request.Prompt) {
		return request
	}
	request.WorkKinds, _ = removeSiteWorkKinds(request.WorkKinds)
	return request
}

func siteEvidenceQuoteForOutcomeContract(request AgentRequest, intakeDecision IntakeDecision, executionPlan ExecutionPlan, hasExecutionPlan bool, contract OutcomeContract) string {
	if !requestExpectsPublicSiteResult(request, executionPlan, hasExecutionPlan, outcomeContractRequiredToolNames(contract)) {
		return ""
	}
	siteEvidenceQuote := strings.TrimSpace(intakeDecision.SiteRequestEvidence)
	if siteEvidenceQuoteMatchesMessage(siteEvidenceQuote, request.Prompt) {
		return siteEvidenceQuote
	}
	return ""
}

func filterStaleOutcomeHints(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, contract OutcomeContract, toolNames []string) []string {
	filteredToolNames := []string{}
	for _, toolName := range toolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" {
			continue
		}
		if strings.HasPrefix(trimmedToolName, "site.app.") && !outcomeAllowsSiteTools(request, executionPlan, hasExecutionPlan, contract) {
			continue
		}
		if trimmedToolName == "artifact.review" && !outcomeAllowsVisualArtifactReview(request, contract) {
			continue
		}
		filteredToolNames = appendUniqueStrings(filteredToolNames, trimmedToolName)
	}
	return filteredToolNames
}

func attachmentSuffixesForOutcomeContract(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredAttachmentSuffixes []string) []string {
	if len(requiredAttachmentSuffixes) == 0 {
		return nil
	}
	if !requestExpectsSiteLinkResult(request, executionPlan, hasExecutionPlan) {
		return append([]string{}, requiredAttachmentSuffixes...)
	}
	if !onlyHTMLAttachmentSuffixes(requiredAttachmentSuffixes) {
		return append([]string{}, requiredAttachmentSuffixes...)
	}
	if requestHasWorkKind(request, WorkKindFileDelivery) {
		return append([]string{}, requiredAttachmentSuffixes...)
	}
	return nil
}

func requestExpectsSiteLinkResult(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool) bool {
	return requestLooksLikeSitePrototypeWork(request) || hasExecutionPlan && executionPlan.PublicDeploy
}

func onlyHTMLAttachmentSuffixes(requiredAttachmentSuffixes []string) bool {
	for _, suffix := range requiredAttachmentSuffixes {
		if strings.ToLower(strings.TrimSpace(suffix)) != ".html" {
			return false
		}
	}
	return len(requiredAttachmentSuffixes) > 0
}

func sanitizeOutcomeContractForRequest(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, contract OutcomeContract) OutcomeContract {
	contract = normalizeOutcomeContract(contract)
	contract = normalizeOutcomeContractSiteRequirementForRequest(request, contract)
	if requestExpectsSiteLinkResult(request, executionPlan, hasExecutionPlan) && !requestHasWorkKind(request, WorkKindFileDelivery) {
		contract = removeImplicitHTMLFileContract(contract)
	}
	if outcomeContractRequiresPublicLinkOnly(contract) {
		contract.ArtifactRequirement = ArtifactRequirementNone
	}
	if outcomeContractRequiresPlatformMessageMaintenance(contract) {
		contract = removePlatformMessageSendContract(contract)
	}
	return normalizeOutcomeContract(contract)
}

func normalizeOutcomeContractSiteRequirementForRequest(request AgentRequest, contract OutcomeContract) OutcomeContract {
	if !outcomeContractHasSiteRequirement(contract) {
		return contract
	}
	activeGoal := ActiveGoal{
		OriginalInstruction: request.ActiveGoal.OriginalInstruction,
		OutcomeContract:     contract,
	}
	if strings.TrimSpace(activeGoal.OriginalInstruction) == "" {
		activeGoal.OriginalInstruction = request.Prompt
	}
	activeGoal, _ = normalizeActiveGoalSiteRequirement(activeGoal, request.Prompt)
	return activeGoal.OutcomeContract
}

func outcomeContractRequiresPlatformMessageMaintenance(contract OutcomeContract) bool {
	for _, toolName := range outcomeContractToolNames(contract) {
		if isPlatformMessageMaintenanceTool(toolName) {
			return true
		}
	}
	return false
}

func removePlatformMessageSendContract(contract OutcomeContract) OutcomeContract {
	contract.RequiredEvidenceTools = removeToolName(contract.RequiredEvidenceTools, "platform.message.send")
	contract.SelectedEvidenceHints = removeToolName(contract.SelectedEvidenceHints, "platform.message.send")
	contract.RequiredEvidenceAnyOf = removeToolNameGroups(contract.RequiredEvidenceAnyOf, "platform.message.send")
	return contract
}

func removeImplicitHTMLFileContract(contract OutcomeContract) OutcomeContract {
	if !onlyHTMLAttachmentSuffixes(contract.RequiredAttachmentSuffixes) {
		return contract
	}
	contract.RequiredAttachmentSuffixes = nil
	contract.RequiredEvidenceTools = removeToolName(contract.RequiredEvidenceTools, "file.attach")
	contract.RequiredEvidenceAnyOf = removeToolNameGroups(contract.RequiredEvidenceAnyOf, "file.attach")
	contract.ExpectedResults = removeExpectedResultsByType(contract.ExpectedResults, ExpectedResultTypeFile)
	return contract
}

func removeToolName(toolNames []string, removedToolName string) []string {
	values := []string{}
	for _, toolName := range toolNames {
		if strings.TrimSpace(toolName) != removedToolName {
			values = appendUniqueStrings(values, toolName)
		}
	}
	return values
}

func removeToolNameGroups(groups [][]string, removedToolName string) [][]string {
	filteredGroups := [][]string{}
	for _, group := range groups {
		filteredGroup := removeToolName(group, removedToolName)
		if len(filteredGroup) > 0 {
			filteredGroups = append(filteredGroups, filteredGroup)
		}
	}
	return filteredGroups
}

func removeExpectedResultsByType(results []ExpectedResult, removedType string) []ExpectedResult {
	filteredResults := []ExpectedResult{}
	for _, result := range results {
		if result.Type != removedType {
			filteredResults = append(filteredResults, result)
		}
	}
	return filteredResults
}

func activeGoalOutcomeContractHasRequirements(contract OutcomeContract) bool {
	artifactRequirement := strings.TrimSpace(contract.ArtifactRequirement)
	return len(contract.ExpectedResults) > 0 ||
		len(contract.RequiredEvidenceTools) > 0 ||
		len(contract.RequiredEvidenceAnyOf) > 0 ||
		len(contract.RequiredAttachmentSuffixes) > 0 ||
		(artifactRequirement != "" && artifactRequirement != ArtifactRequirementNone)
}

func expectedResultsForRequest(request AgentRequest, intakeDecision IntakeDecision, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredEvidenceTools []string, requiredAttachmentSuffixes []string) []ExpectedResult {
	results := append([]ExpectedResult{}, intakeDecision.ExpectedResults...)
	if requestExpectsPublicSiteResult(request, executionPlan, hasExecutionPlan, requiredEvidenceTools) {
		results = append(results, ExpectedResult{
			ID:          "site-public-link",
			Type:        ExpectedResultTypeLink,
			Description: "사용자가 열 수 있는 public URL의 웹사이트 프로젝트 한 개",
			Required:    true,
			AcceptanceHints: []string{
				"URL must be visible in a successful tool result or final response.",
				"Updates should keep the same site project when the task is a revision.",
			},
		})
	}
	if requestExpectsFileResult(requiredEvidenceTools, requiredAttachmentSuffixes) {
		results = append(results, ExpectedResult{
			ID:              "attached-file",
			Type:            ExpectedResultTypeFile,
			Description:     "요청한 형식의 파일 한 개 이상이 사용자에게 첨부됨",
			Required:        true,
			AcceptanceHints: appendUniqueStrings(requiredAttachmentSuffixes),
		})
	}
	if len(results) == 0 {
		return nil
	}
	results = append(results, ExpectedResult{
		ID:          "final-message",
		Type:        ExpectedResultTypeMessage,
		Description: "사용자에게 현재 Task 결과를 설명하는 최종 답변",
		Required:    true,
	})
	return normalizeExpectedResults(results)
}

func legacyExpectedResultsForContract(request AgentRequest, intakeDecision IntakeDecision, executionPlan ExecutionPlan, hasExecutionPlan bool, contract OutcomeContract) []ExpectedResult {
	return expectedResultsForRequest(request, intakeDecision, executionPlan, hasExecutionPlan, outcomeContractRequiredToolNames(contract), contract.RequiredAttachmentSuffixes)
}

func requestExpectsPublicSiteResult(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredEvidenceTools []string) bool {
	if requestLooksLikeSitePrototypeWork(request) || hasExecutionPlan && executionPlan.PublicDeploy {
		return true
	}
	for _, toolName := range requiredEvidenceTools {
		if strings.HasPrefix(strings.TrimSpace(toolName), "site.app.") {
			return true
		}
	}
	return false
}

func requestExpectsFileResult(requiredEvidenceTools []string, requiredAttachmentSuffixes []string) bool {
	if len(requiredAttachmentSuffixes) > 0 {
		return true
	}
	return stringSliceContains(requiredEvidenceTools, "file.attach")
}

func appendExpectedResults(results []ExpectedResult, additionalResults ...ExpectedResult) []ExpectedResult {
	nextResults := append([]ExpectedResult{}, results...)
	nextResults = append(nextResults, additionalResults...)
	return normalizeExpectedResults(nextResults)
}

func artifactRequirementForOutcomeContract(intakeDecision IntakeDecision, contract OutcomeContract) string {
	if len(contract.RequiredAttachmentSuffixes) > 0 || stringSliceContains(contract.RequiredEvidenceTools, "file.attach") || evidenceAnyOfContainsTool(contract.RequiredEvidenceAnyOf, "file.attach") {
		return ArtifactRequirementRequired
	}
	if outcomeContractRequiresPublicLinkOnly(contract) {
		return ArtifactRequirementNone
	}
	for _, outputFormat := range intakeDecision.RequestedOutputFormats {
		if isArtifactOutputFormat(outputFormat) {
			return ArtifactRequirementPreferred
		}
	}
	return ArtifactRequirementNone
}

func outcomeContractRequiresPublicLinkOnly(contract OutcomeContract) bool {
	hasLinkResult := false
	for _, result := range normalizeExpectedResults(contract.ExpectedResults) {
		if !result.Required {
			continue
		}
		switch result.Type {
		case ExpectedResultTypeFile:
			return false
		case ExpectedResultTypeLink:
			hasLinkResult = true
		}
	}
	return hasLinkResult
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

func requiredSendEvidenceToolsForRequest(request AgentRequest, contract OutcomeContract, evidenceHints []string) []string {
	if contractRequiresSendTool(contract) {
		return sendEvidenceToolsFromValues(outcomeContractRequiredToolNames(contract))
	}
	if !requestHasWorkKind(request, WorkKindExternalSend) {
		return nil
	}
	toolNames := sendEvidenceToolsFromValues(evidenceHints)
	if len(toolNames) > 0 {
		return toolNames
	}
	return singleAvailableSendEvidenceTool(request.ToolSet)
}

func sendEvidenceToolsFromValues(values []string) []string {
	toolNames := []string{}
	for _, value := range values {
		if isSendEvidenceTool(value) {
			toolNames = appendUniqueStrings(toolNames, value)
		}
	}
	return toolNames
}

func singleAvailableSendEvidenceTool(toolSet *ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	toolNames := []string{}
	for _, toolName := range toolSet.ListToolNames() {
		if isSendEvidenceTool(toolName) {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
	}
	if len(toolNames) != 1 {
		return nil
	}
	return toolNames
}

func evidenceHintMatchesOutcome(toolName string, request AgentRequest, intakeDecision IntakeDecision, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredAttachmentSuffixes []string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return false
	}
	if isSendEvidenceTool(trimmedToolName) {
		return activeGoalRequiresTool(request.ActiveGoal, trimmedToolName) ||
			(hasExecutionPlan && (executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend))
	}
	if isPlatformMessageMaintenanceTool(trimmedToolName) {
		return intakeDecision.TaskShape == TaskShapeMaintenanceTask ||
			activeGoalMentionsTool(request.ActiveGoal, trimmedToolName) ||
			activeGoalMentionsToolPrefix(request.ActiveGoal, "platform.message.")
	}
	if activeGoalRequiresTool(request.ActiveGoal, trimmedToolName) {
		return true
	}
	if trimmedToolName == "file.attach" {
		return len(requiredAttachmentSuffixes) > 0
	}
	if strings.HasPrefix(trimmedToolName, "site.app.") {
		return (hasExecutionPlan && executionPlan.PublicDeploy) || requestLooksLikeSitePrototypeWork(request)
	}
	if strings.HasPrefix(trimmedToolName, "schedule.") {
		return intakeDecision.TaskShape == TaskShapeScheduledTask
	}
	if strings.HasPrefix(trimmedToolName, "calendar.") {
		return requestLooksLikeCalendarWork(request)
	}
	if strings.HasPrefix(trimmedToolName, "flow.task.") {
		return requestLooksLikeCalendarWork(request)
	}
	return false
}

func isSendEvidenceTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "platform.message.send", "mail.message.send", "google.gmail.send", "slack.message.send":
		return true
	default:
		return false
	}
}

func isPlatformMessageMaintenanceTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "platform.message.context", "platform.message.search", "platform.message.update", "platform.message.delete":
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

func activeGoalMentionsToolPrefix(activeGoal ActiveGoal, prefix string) bool {
	return contractMentionsToolPrefix(activeGoal.OutcomeContract, prefix)
}

func activeGoalRequiresToolPrefix(activeGoal ActiveGoal, prefix string) bool {
	return contractRequiresToolPrefix(activeGoal.OutcomeContract, prefix)
}

func activeGoalMentionsTool(activeGoal ActiveGoal, toolName string) bool {
	normalizedToolName := strings.TrimSpace(toolName)
	if normalizedToolName == "" {
		return false
	}
	for _, activeToolName := range outcomeContractToolNames(activeGoal.OutcomeContract) {
		if strings.TrimSpace(activeToolName) == normalizedToolName {
			return true
		}
	}
	return false
}

func activeGoalRequiresTool(activeGoal ActiveGoal, toolName string) bool {
	normalizedToolName := strings.TrimSpace(toolName)
	if normalizedToolName == "" {
		return false
	}
	for _, activeToolName := range outcomeContractRequiredToolNames(activeGoal.OutcomeContract) {
		if strings.TrimSpace(activeToolName) == normalizedToolName {
			return true
		}
	}
	return false
}

func requestLooksLikeExternalSendContinuation(request AgentRequest, contract OutcomeContract) bool {
	return contractRequiresSendTool(contract) ||
		activeGoalRequiresTool(request.ActiveGoal, "platform.message.send") ||
		activeGoalRequiresTool(request.ActiveGoal, "mail.message.send") ||
		activeGoalRequiresTool(request.ActiveGoal, "google.gmail.send") ||
		activeGoalRequiresTool(request.ActiveGoal, "slack.message.send")
}

func contractMentionsToolPrefix(contract OutcomeContract, prefix string) bool {
	for _, toolName := range outcomeContractToolNames(contract) {
		if strings.HasPrefix(strings.TrimSpace(toolName), prefix) {
			return true
		}
	}
	return false
}

func outcomeContractToolNames(contract OutcomeContract) []string {
	toolNames := outcomeContractRequiredToolNames(contract)
	toolNames = append(toolNames, contract.SelectedEvidenceHints...)
	return toolNames
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
	activeGoal.WorkKinds = appendUniqueStrings(activeGoal.WorkKinds, request.WorkKinds...)
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
		WorkKinds:           append([]string{}, intakeDecision.WorkKinds...),
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

func expectedResultIncludesType(outcomeContract OutcomeContract, resultType string) bool {
	for _, expectedResult := range outcomeContract.ExpectedResults {
		if strings.TrimSpace(expectedResult.Type) == resultType {
			return true
		}
	}
	return false
}
