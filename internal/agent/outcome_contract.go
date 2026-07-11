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
	DroppedExpectedResultIDs     []string   `json:"droppedExpectedResultIDs,omitempty"`
	DroppedRequiredEvidenceTools []string   `json:"droppedRequiredEvidenceTools,omitempty"`
	DroppedRequiredEvidenceAnyOf [][]string `json:"droppedRequiredEvidenceAnyOf,omitempty"`
	DroppedSelectedEvidenceHints []string   `json:"droppedSelectedEvidenceHints,omitempty"`
}

func (report siteRequirementNormalizationReport) HasDrops() bool {
	return len(report.DroppedExpectedResultIDs) > 0 ||
		len(report.DroppedRequiredEvidenceTools) > 0 ||
		len(report.DroppedRequiredEvidenceAnyOf) > 0 ||
		len(report.DroppedSelectedEvidenceHints) > 0
}

func normalizeTurnDecisionSiteRequirement(request AgentRequest, decision TurnDecision) (TurnDecision, siteRequirementNormalizationReport) {
	intakeDecision := decision.IntakeDecision()
	intakeDecision, report := normalizeIntakeDecisionSiteRequirement(request.Prompt, intakeDecision, "intake")
	decision.ExpectedResults = intakeDecision.ExpectedResults
	decision.SiteRequestEvidence = intakeDecision.SiteRequestEvidence
	if report.HasDrops() {
		decision.InitialToolNames, _ = removeToolNamePrefix(decision.InitialToolNames, "site.")
	}
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
	decision.ExpectedResults, report.DroppedExpectedResultIDs = removeSiteExpectedResults(decision.ExpectedResults)
	decision.SiteRequestEvidence = ""
	return decision, report
}

func intakeDecisionRequiresSiteEvidence(decision IntakeDecision) bool {
	return strings.TrimSpace(decision.SiteRequestEvidence) != "" ||
		requiredEvidenceHasPrefix(decision.RequiredEvidenceTools, "site.") ||
		expectedResultsIncludeSiteRequirement(decision.ExpectedResults)
}

func normalizeActiveGoalSiteRequirement(activeGoal ActiveGoal, currentUserMessage string, isContinuation bool) (ActiveGoal, siteRequirementNormalizationReport) {
	if !activeGoalRequiresSiteEvidence(activeGoal) {
		return activeGoal, siteRequirementNormalizationReport{}
	}
	if isContinuation {
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
	activeGoal.OutcomeContract, report = stripOutcomeContractSiteRequirements(activeGoal.OutcomeContract, report)
	return activeGoal, report
}

func activeGoalRequiresSiteEvidence(activeGoal ActiveGoal) bool {
	return outcomeContractHasSiteRequirement(activeGoal.OutcomeContract)
}

func outcomeContractHasSiteRequirement(contract OutcomeContract) bool {
	return contractMentionsToolPrefix(contract, "site.") || expectedResultsIncludeSiteRequirement(contract.ExpectedResults)
}

func stripOutcomeContractSiteRequirements(contract OutcomeContract, report siteRequirementNormalizationReport) (OutcomeContract, siteRequirementNormalizationReport) {
	contract.RequiredEvidenceTools, report.DroppedRequiredEvidenceTools = removeToolNamePrefix(contract.RequiredEvidenceTools, "site.")
	contract.RequiredEvidenceAnyOf, report.DroppedRequiredEvidenceAnyOf = removeToolNamePrefixGroups(contract.RequiredEvidenceAnyOf, "site.")
	contract.SelectedEvidenceHints, report.DroppedSelectedEvidenceHints = removeToolNamePrefix(contract.SelectedEvidenceHints, "site.")
	contract.ExpectedResults, report.DroppedExpectedResultIDs = removeSiteExpectedResults(contract.ExpectedResults)
	contract.SiteEvidenceQuote = ""
	return normalizeOutcomeContract(contract), report
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
	return strings.TrimSpace(result.Type) == ExpectedResultTypeLink
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
	if intakeDecision.TaskShape == TaskShapeApprovalGatedTask {
		return true
	}
	for _, toolName := range requiredEvidenceTools {
		if confirmationRiskyEvidenceTool(toolName) {
			return true
		}
	}
	return false
}

func confirmationRiskyEvidenceTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "message.send", "mail.message.send", "google.gmail.send", "slack.message.send":
		return true
	default:
		return false
	}
}

func requestIsNonDestructiveSitePrototypePublish(request AgentRequest, requiredEvidenceTools []string) bool {
	if !hasAllTools(request.ToolSet, []string{"site.create", "site.publish"}) {
		return false
	}
	if !requiredEvidenceContains(requiredEvidenceTools, "site.publish") && !hasTool(request.ToolSet, "site.publish") {
		return false
	}
	if !requestLooksLikeSitePrototypeWork(request) {
		return false
	}
	return true
}

func requestLooksLikeSitePrototypeWork(request AgentRequest) bool {
	return activeGoalRequiresToolPrefix(request.ActiveGoal, "site.") || activeGoalMentionsToolPrefix(request.ActiveGoal, "site.")
}

func requestLooksLikeCalendarWork(request AgentRequest) bool {
	return activeGoalRequiresToolPrefix(request.ActiveGoal, "calendar.") || activeGoalMentionsToolPrefix(request.ActiveGoal, "calendar.")
}

func requestLooksLikeFlowTaskWork(request AgentRequest) bool {
	return activeGoalRequiresToolPrefix(request.ActiveGoal, "task.") || activeGoalMentionsToolPrefix(request.ActiveGoal, "task.")
}

func requestLooksLikeSlidesArtifactWork(request AgentRequest) bool {
	return outcomeContractMentionsAttachmentSuffix(request.ActiveGoal.OutcomeContract, ".pptx") ||
		outcomeContractMentionsAttachmentSuffix(request.ActiveGoal.OutcomeContract, ".ppt")
}

func intakeDecisionRequestsVisualDeliverable(intakeDecision IntakeDecision) bool {
	for _, format := range intakeDecision.RequestedOutputFormats {
		switch strings.ToLower(strings.TrimSpace(format)) {
		case "pptx", "ppt", "html":
			return true
		}
	}
	return false
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
	if strings.HasPrefix(trimmedToolName, "site.") {
		return outcomeAllowsSiteTools(request, executionPlan, hasExecutionPlan, outcomeContract)
	}
	if isSendEvidenceTool(trimmedToolName) {
		return outcomeAllowsExternalSendTools(request, executionPlan, hasExecutionPlan, outcomeContract)
	}
	return true
}

func outcomeAllowsSiteTools(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
	return contractRequiresToolPrefix(outcomeContract, "site.") || (hasExecutionPlan && executionPlan.PublicDeploy) || requestLooksLikeSitePrototypeWork(request)
}

func outcomeAllowsExternalSendTools(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
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

func outcomeContractMentionsAttachmentSuffix(contract OutcomeContract, suffix string) bool {
	normalizedSuffix := strings.ToLower(strings.TrimSpace(suffix))
	for _, candidateSuffix := range contract.RequiredAttachmentSuffixes {
		if strings.ToLower(strings.TrimSpace(candidateSuffix)) == normalizedSuffix {
			return true
		}
	}
	for _, result := range contract.ExpectedResults {
		for _, hint := range result.AcceptanceHints {
			if strings.ToLower(strings.TrimSpace(hint)) == normalizedSuffix {
				return true
			}
		}
	}
	return false
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
	return KernelToolNames()
}

func coreAgentToolNames() []string {
	return KernelToolNames()
}

func genericBuiltInToolNames() []string {
	return KernelToolNames()
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
	request.ActiveGoal, _ = normalizeActiveGoalSiteRequirement(request.ActiveGoal, request.Prompt, request.IsApprovalContinuation || request.IsRuntimeRestartResume)
	requestExplicitlyAsksForFileBeyondSite := requestMentionsFileBeyondSiteEvidence(request, intakeDecision)
	requiredAttachmentSuffixes = attachmentSuffixesForOutcomeContract(request, executionPlan, hasExecutionPlan, requiredAttachmentSuffixes, requestExplicitlyAsksForFileBeyondSite)
	if OutcomeContractHasRequirements(request.ActiveGoal.OutcomeContract) {
		contract := request.ActiveGoal.OutcomeContract
		selectedEvidenceHints := selectedEvidenceHintTools(instructionBundle)
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, intakeDecision.RequiredEvidenceTools...)
		contract.SelectedEvidenceHints = appendUniqueStrings(contract.SelectedEvidenceHints, selectedEvidenceHints...)
		contract.SelectedEvidenceHints = filterStaleOutcomeHints(request, executionPlan, hasExecutionPlan, contract, contract.SelectedEvidenceHints)
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, selectedEvidenceToolsForRequestContinuation(request, contract, selectedEvidenceHints)...)
		if len(intakeDecision.RequiredEvidenceTools) == 0 {
			contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, requiredSendEvidenceToolsForContract(contract)...)
		}
		contract.RequiredEffects = appendOutcomeEffects(contract.RequiredEffects, requiredWorkflowEffectRequirementsForRequest(request)...)
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
	if len(intakeDecision.RequiredEvidenceTools) > 0 {
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, intakeDecision.RequiredEvidenceTools...)
	} else {
		contract.RequiredEvidenceTools = outcomeEvidenceTools(request, intakeDecision, executionPlan, hasExecutionPlan, contract.SelectedEvidenceHints, requiredAttachmentSuffixes)
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, requiredSendEvidenceToolsForContract(contract)...)
	}
	contract.RequiredEffects = appendOutcomeEffects(contract.RequiredEffects, requiredWorkflowEffectRequirementsForRequest(request)...)
	contract.SelectedEvidenceHints = filterStaleOutcomeHints(request, executionPlan, hasExecutionPlan, contract, contract.SelectedEvidenceHints)
	if len(requiredAttachmentSuffixes) > 0 {
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, FileDeliverToolName)
	}
	contract.ExpectedResults = expectedResultsForRequest(request, intakeDecision, executionPlan, hasExecutionPlan, contract.RequiredEvidenceTools, requiredAttachmentSuffixes)
	contract.ArtifactRequirement = artifactRequirementForOutcomeContract(intakeDecision, contract)
	contract.SiteEvidenceQuote = siteEvidenceQuoteForOutcomeContract(request, intakeDecision, executionPlan, hasExecutionPlan, contract)
	contract.Source = outcomeContractSource(hasExecutionPlan, requiredAttachmentSuffixes)
	return sanitizeOutcomeContractForRequest(request, executionPlan, hasExecutionPlan, contract)
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
		if strings.HasPrefix(trimmedToolName, "site.") && !outcomeAllowsSiteTools(request, executionPlan, hasExecutionPlan, contract) {
			continue
		}
		if trimmedToolName == "artifact.review" && !outcomeAllowsVisualArtifactReview(request, contract) {
			continue
		}
		filteredToolNames = appendUniqueStrings(filteredToolNames, trimmedToolName)
	}
	return filteredToolNames
}

func attachmentSuffixesForOutcomeContract(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredAttachmentSuffixes []string, requestExplicitlyAsksForFileBeyondSite bool) []string {
	if len(requiredAttachmentSuffixes) == 0 {
		return nil
	}
	if !requestExpectsSiteLinkResult(request, executionPlan, hasExecutionPlan) {
		return append([]string{}, requiredAttachmentSuffixes...)
	}
	if activeGoalRequiresTool(request.ActiveGoal, FileDeliverToolName) ||
		expectedResultIncludesType(request.ActiveGoal.OutcomeContract, ExpectedResultTypeFile) ||
		requestExplicitlyAsksForFileBeyondSite {
		return append([]string{}, requiredAttachmentSuffixes...)
	}
	return nil
}

// requestMentionsFileBeyondSiteEvidence reports whether the user's message has content
// beyond the quoted site request itself, meaning the requested output format (e.g. "html")
// most likely names a separate file attachment the user explicitly asked for rather than
// just describing the site's own tech format.
func requestMentionsFileBeyondSiteEvidence(request AgentRequest, intakeDecision IntakeDecision) bool {
	normalizedQuote := trimSiteEvidencePunctuation(normalizeSiteEvidenceText(intakeDecision.SiteRequestEvidence))
	if normalizedQuote == "" {
		return false
	}
	normalizedPrompt := trimSiteEvidencePunctuation(normalizeSiteEvidenceText(request.Prompt))
	return normalizedPrompt != normalizedQuote
}

func requestExpectsSiteLinkResult(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool) bool {
	return requestLooksLikeSitePrototypeWork(request) || hasExecutionPlan && executionPlan.PublicDeploy
}

func sanitizeOutcomeContractForRequest(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, contract OutcomeContract) OutcomeContract {
	contract = normalizeOutcomeContract(contract)
	contract = normalizeOutcomeContractSiteRequirementForRequest(request, contract)
	if requestExpectsSiteLinkResult(request, executionPlan, hasExecutionPlan) && !outcomeContractExpectsFileResult(contract) {
		contract = removeImplicitSiteFileContract(contract)
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
	if requiredEvidenceContains(outcomeContractRequiredToolNames(contract), "site.delete") {
		return contract
	}
	activeGoal := ActiveGoal{
		OriginalInstruction: request.ActiveGoal.OriginalInstruction,
		OutcomeContract:     contract,
	}
	if strings.TrimSpace(activeGoal.OriginalInstruction) == "" {
		activeGoal.OriginalInstruction = request.Prompt
	}
	activeGoal, _ = normalizeActiveGoalSiteRequirement(activeGoal, request.Prompt, request.IsApprovalContinuation || request.IsRuntimeRestartResume)
	return activeGoal.OutcomeContract
}

func outcomeContractExpectsFileResult(contract OutcomeContract) bool {
	return len(contract.RequiredAttachmentSuffixes) > 0 ||
		evidenceToolsContainArtifactDelivery(contract.RequiredEvidenceTools) ||
		evidenceAnyOfContainsArtifactDelivery(contract.RequiredEvidenceAnyOf) ||
		expectedResultIncludesType(contract, ExpectedResultTypeFile)
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
	contract.RequiredEvidenceTools = removeToolName(contract.RequiredEvidenceTools, "message.send")
	contract.SelectedEvidenceHints = removeToolName(contract.SelectedEvidenceHints, "message.send")
	contract.RequiredEvidenceAnyOf = removeToolNameGroups(contract.RequiredEvidenceAnyOf, "message.send")
	return contract
}

func removeImplicitSiteFileContract(contract OutcomeContract) OutcomeContract {
	contract.RequiredAttachmentSuffixes = nil
	contract.RequiredEvidenceTools = removeToolName(contract.RequiredEvidenceTools, FileDeliverToolName)
	contract.RequiredEvidenceTools = removeToolName(contract.RequiredEvidenceTools, FileAttachToolName)
	contract.RequiredEvidenceAnyOf = removeToolNameGroups(contract.RequiredEvidenceAnyOf, FileDeliverToolName)
	contract.RequiredEvidenceAnyOf = removeToolNameGroups(contract.RequiredEvidenceAnyOf, FileAttachToolName)
	contract.ExpectedResults = removeExpectedResultsByType(contract.ExpectedResults, ExpectedResultTypeFile)
	return contract
}

func removeToolName(toolNames []string, removedToolName string) []string {
	values := []string{}
	for _, toolName := range toolNames {
		if !ToolNamesMatch(toolName, removedToolName) {
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

func OutcomeContractHasRequirements(contract OutcomeContract) bool {
	artifactRequirement := strings.TrimSpace(contract.ArtifactRequirement)
	return len(contract.ExpectedResults) > 0 ||
		len(contract.RequiredEvidenceTools) > 0 ||
		len(contract.RequiredEvidenceAnyOf) > 0 ||
		len(contract.RequiredAttachmentSuffixes) > 0 ||
		len(contract.RequiredEffects) > 0 ||
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
	if requiredEvidenceContains(requiredEvidenceTools, "site.delete") {
		return false
	}
	if requiredEvidenceContains(requiredEvidenceTools, "site.publish") {
		return true
	}
	if hasExecutionPlan && executionPlan.PublicDeploy {
		return true
	}
	return requestLooksLikeSitePrototypeWork(request)
}

func requestExpectsFileResult(requiredEvidenceTools []string, requiredAttachmentSuffixes []string) bool {
	if len(requiredAttachmentSuffixes) > 0 {
		return true
	}
	for _, toolName := range requiredEvidenceTools {
		if IsArtifactDeliveryTool(toolName) {
			return true
		}
	}
	return false
}

func appendExpectedResults(results []ExpectedResult, additionalResults ...ExpectedResult) []ExpectedResult {
	nextResults := append([]ExpectedResult{}, results...)
	nextResults = append(nextResults, additionalResults...)
	return normalizeExpectedResults(nextResults)
}

func artifactRequirementForOutcomeContract(intakeDecision IntakeDecision, contract OutcomeContract) string {
	if len(contract.RequiredAttachmentSuffixes) > 0 || evidenceToolsContainArtifactDelivery(contract.RequiredEvidenceTools) || evidenceAnyOfContainsArtifactDelivery(contract.RequiredEvidenceAnyOf) {
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
		for _, candidateToolName := range group {
			if ToolNamesMatch(candidateToolName, toolName) {
				return true
			}
		}
	}
	return false
}

func evidenceToolsContainArtifactDelivery(toolNames []string) bool {
	for _, toolName := range toolNames {
		if IsArtifactDeliveryTool(toolName) {
			return true
		}
	}
	return false
}

func evidenceAnyOfContainsArtifactDelivery(groups [][]string) bool {
	for _, group := range groups {
		if evidenceToolsContainArtifactDelivery(group) {
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

func requiredSendEvidenceToolsForContract(contract OutcomeContract) []string {
	if contractRequiresSendTool(contract) {
		return sendEvidenceToolsFromValues(outcomeContractRequiredToolNames(contract))
	}
	return nil
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

func availableSendEvidenceToolNames(toolSet *ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	toolNames := []string{}
	for _, toolName := range toolSet.ListToolNames() {
		if isSendEvidenceTool(toolName) {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
	}
	return toolNames
}

func singleAvailableSendEvidenceTool(toolSet *ToolSet) []string {
	toolNames := availableSendEvidenceToolNames(toolSet)
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
			activeGoalMentionsToolPrefix(request.ActiveGoal, "message.")
	}
	if activeGoalRequiresTool(request.ActiveGoal, trimmedToolName) {
		return true
	}
	if IsArtifactDeliveryTool(trimmedToolName) {
		return len(requiredAttachmentSuffixes) > 0
	}
	if strings.HasPrefix(trimmedToolName, "site.") {
		return (hasExecutionPlan && executionPlan.PublicDeploy) || requestLooksLikeSitePrototypeWork(request)
	}
	if strings.HasPrefix(trimmedToolName, "schedule.") {
		return intakeDecision.TaskShape == TaskShapeScheduledTask
	}
	return false
}

func isSendEvidenceTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "message.send", "mail.message.send", "google.gmail.send", "slack.message.send":
		return true
	default:
		return false
	}
}

func isPlatformMessageMaintenanceTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "message.context", "message.search", "message.update", "message.delete":
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
		if ToolNamesMatch(activeToolName, normalizedToolName) {
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
		if ToolNamesMatch(activeToolName, normalizedToolName) {
			return true
		}
	}
	return false
}

func requestLooksLikeExternalSendContinuation(request AgentRequest, contract OutcomeContract) bool {
	return contractRequiresSendTool(contract) ||
		activeGoalRequiresTool(request.ActiveGoal, "message.send") ||
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
		if strings.HasPrefix(strings.TrimSpace(toolName), "site.") && executionPlan.PublicDeploy {
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
