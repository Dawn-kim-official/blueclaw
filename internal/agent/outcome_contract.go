package agent

import (
	"strings"

	"blueclaw/internal/task"
)

func expectedResultsIncludeSiteRequirement(results []ExpectedResult) bool {
	for _, result := range results {
		if expectedResultIsSiteRequirement(result) {
			return true
		}
	}
	return false
}

func expectedResultIsSiteRequirement(result ExpectedResult) bool {
	return strings.TrimSpace(result.ID) == "site-public-link"
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
	for _, toolName := range appendUniqueStrings(requiredEvidenceTools, intakeDecision.InitialToolNames...) {
		if isSendEvidenceTool(request.ToolSet, toolName) {
			return true
		}
	}
	return false
}

func requestIsNonDestructiveSitePrototypePublish(request AgentRequest, requiredEvidenceTools []string) bool {
	if !hasAllTools(request.ToolSet, []string{"site.create", "site.publish"}) {
		return false
	}
	if !requiredEvidenceContains(requiredEvidenceTools, "site.publish") && !hasTool(request.ToolSet, "site.publish") {
		return false
	}
	if !contractRequiresToolNamespace(request.ToolSet, request.ActiveGoal.OutcomeContract, "site") &&
		!requiredEvidenceIncludesNamespace(request.ToolSet, requiredEvidenceTools, "site") {
		return false
	}
	return true
}

func requestLooksLikeSitePrototypeWork(request AgentRequest) bool {
	return contractRequiresToolNamespace(request.ToolSet, request.ActiveGoal.OutcomeContract, "site")
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

func requestNeedsDerivedSideEffectEvidenceGroup(toolSet *ToolSet, intakeDecision IntakeDecision, contract OutcomeContract) bool {
	switch intakeDecision.TaskShape {
	case TaskShapeMaintenanceTask, TaskShapeScheduledTask, TaskShapeApprovalGatedTask:
	default:
		return false
	}
	return !requiredEvidenceIncludesSideEffect(toolSet, contract.RequiredEvidenceTools)
}

func toolSetForOutcomeReference(toolSet *ToolSet, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) *ToolSet {
	if toolSet == nil {
		return nil
	}
	allowedToolNames := []string{}
	for _, toolName := range toolSet.ListToolNames() {
		if shouldExposeToolForOutcome(toolSet, toolName, request, executionPlan, hasExecutionPlan, outcomeContract) {
			allowedToolNames = append(allowedToolNames, toolName)
		}
	}
	return toolSet.WithAllowedToolNames(allowedToolNames)
}

func shouldExposeToolForOutcome(toolSet *ToolSet, toolName string, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if stringSliceContains(request.PinnedToolNames, trimmedToolName) {
		return true
	}
	if activeGoalRequiresTool(request.ActiveGoal, trimmedToolName) {
		return true
	}
	if toolIsInNamespace(toolSet, trimmedToolName, "site") {
		return outcomeAllowsSiteTools(toolSet, executionPlan, hasExecutionPlan, outcomeContract)
	}
	if isSendEvidenceTool(toolSet, trimmedToolName) {
		return outcomeAllowsExternalSendTools(toolSet, executionPlan, hasExecutionPlan, outcomeContract)
	}
	return true
}

func outcomeAllowsSiteTools(toolSet *ToolSet, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
	return contractRequiresToolNamespace(toolSet, outcomeContract, "site") || hasExecutionPlan && executionPlan.PublicDeploy
}

func outcomeAllowsExternalSendTools(toolSet *ToolSet, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
	return contractRequiresSendTool(toolSet, outcomeContract) ||
		(hasExecutionPlan && (executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend))
}

func outcomeAllowsVisualArtifactReview(request AgentRequest, outcomeContract OutcomeContract) bool {
	artifactRequirement := strings.TrimSpace(outcomeContract.ArtifactRequirement)
	return (artifactRequirement != "" && artifactRequirement != ArtifactRequirementNone) ||
		expectedResultIncludesType(outcomeContract, ExpectedResultTypeFile) ||
		expectedResultIncludesType(outcomeContract, ExpectedResultTypeLink) ||
		contractRequiresToolNamespace(request.ToolSet, request.ActiveGoal.OutcomeContract, "site") ||
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
	return appendUniqueStrings(instructionBundle.RequiredEvidenceTools)
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
		if isSendEvidenceTool(request.ToolSet, trimmedToolName) && !requestLooksLikeExternalSendContinuation(request, contract) {
			continue
		}
		if isSendEvidenceTool(request.ToolSet, trimmedToolName) {
			toolNames = appendUniqueStrings(toolNames, trimmedToolName)
		}
	}
	return toolNames
}

func outcomeContractForRequest(request AgentRequest, intakeDecision IntakeDecision, instructionBundle InstructionBundle, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredAttachmentSuffixes []string) OutcomeContract {
	requiredAttachmentSuffixes = attachmentSuffixesForOutcomeContract(requiredAttachmentSuffixes)
	if OutcomeContractHasRequirements(request.ActiveGoal.OutcomeContract) {
		contract := request.ActiveGoal.OutcomeContract
		selectedEvidenceHints := selectedEvidenceHintTools(instructionBundle)
		contract.SelectedEvidenceHints = appendUniqueStrings(contract.SelectedEvidenceHints, selectedEvidenceHints...)
		contract.SelectedEvidenceHints = filterStaleOutcomeHints(request, executionPlan, hasExecutionPlan, contract, contract.SelectedEvidenceHints)
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, selectedEvidenceToolsForRequestContinuation(request, contract, selectedEvidenceHints)...)
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, requiredSendEvidenceToolsForContract(request.ToolSet, contract)...)
		contract.RequiredEffects = appendOutcomeEffects(contract.RequiredEffects, requiredWorkflowEffectRequirementsForRequest(request)...)
		if strings.TrimSpace(contract.ArtifactRequirement) == "" || contract.ArtifactRequirement == ArtifactRequirementNone {
			contract.ArtifactRequirement = artifactRequirementForOutcomeContract(intakeDecision, contract)
		}
		return sanitizeOutcomeContractForRequest(request, executionPlan, hasExecutionPlan, contract)
	}
	contract := OutcomeContract{
		SelectedEvidenceHints:      appendUniqueStrings(outcomeContractToolNames(request.ActiveGoal.OutcomeContract), selectedEvidenceHintTools(instructionBundle)...),
		RequiredAttachmentSuffixes: append([]string{}, requiredAttachmentSuffixes...),
	}
	contract.SelectedEvidenceHints = appendUniqueStrings(contract.SelectedEvidenceHints, workingSetEvidenceGroup(request.ToolSet, intakeDecision.InitialToolNames)...)
	contract.RequiredEvidenceTools = outcomeEvidenceTools(request, intakeDecision, executionPlan, hasExecutionPlan, contract.SelectedEvidenceHints, requiredAttachmentSuffixes)
	contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, requiredSendEvidenceToolsForContract(request.ToolSet, contract)...)
	if requestNeedsDerivedSideEffectEvidenceGroup(request.ToolSet, intakeDecision, contract) {
		evidenceGroup := workingSetEvidenceGroup(request.ToolSet, selectedEvidenceHintTools(instructionBundle))
		if len(evidenceGroup) > 0 {
			contract.RequiredEvidenceAnyOf = append(contract.RequiredEvidenceAnyOf, evidenceGroup)
		}
	}
	contract.RequiredEffects = appendOutcomeEffects(contract.RequiredEffects, requiredWorkflowEffectRequirementsForRequest(request)...)
	contract.SelectedEvidenceHints = filterStaleOutcomeHints(request, executionPlan, hasExecutionPlan, contract, contract.SelectedEvidenceHints)
	if len(requiredAttachmentSuffixes) > 0 {
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, FileDeliverToolName)
	}
	contract.ExpectedResults = expectedResultsForRequest(intakeDecision, executionPlan, hasExecutionPlan, requiredAttachmentSuffixes)
	contract.ArtifactRequirement = artifactRequirementForOutcomeContract(intakeDecision, contract)
	contract.Source = outcomeContractSource(hasExecutionPlan, requiredAttachmentSuffixes)
	return sanitizeOutcomeContractForRequest(request, executionPlan, hasExecutionPlan, contract)
}

func filterStaleOutcomeHints(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, contract OutcomeContract, toolNames []string) []string {
	filteredToolNames := []string{}
	for _, toolName := range toolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" {
			continue
		}
		if toolIsInNamespace(request.ToolSet, trimmedToolName, "site") && !outcomeAllowsSiteTools(request.ToolSet, executionPlan, hasExecutionPlan, contract) {
			continue
		}
		if trimmedToolName == "artifact.review" && !outcomeAllowsVisualArtifactReview(request, contract) {
			continue
		}
		filteredToolNames = appendUniqueStrings(filteredToolNames, trimmedToolName)
	}
	return filteredToolNames
}

func attachmentSuffixesForOutcomeContract(requiredAttachmentSuffixes []string) []string {
	return append([]string{}, requiredAttachmentSuffixes...)
}

func requestExpectsSiteLinkResult(executionPlan ExecutionPlan, hasExecutionPlan bool, contract OutcomeContract) bool {
	return expectedResultIncludesType(contract, ExpectedResultTypeLink) || hasExecutionPlan && executionPlan.PublicDeploy
}

func sanitizeOutcomeContractForRequest(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, contract OutcomeContract) OutcomeContract {
	contract = normalizeOutcomeContract(contract)
	if outcomeContractExpectsFileResult(contract) {
		contract = removeIntermediateAttachmentEvidence(request.ToolSet, contract)
	}
	if requestExpectsSiteLinkResult(executionPlan, hasExecutionPlan, contract) && !outcomeContractExpectsFileResult(contract) {
		contract = removeImplicitSiteFileContract(contract)
	}
	if outcomeContractRequiresPublicLinkOnly(contract) {
		contract.ArtifactRequirement = ArtifactRequirementNone
	}
	if outcomeContractRequiresPlatformMessageMaintenance(request.ToolSet, contract) {
		contract = removePlatformMessageSendContract(contract)
	}
	if !requestExpectsExternalSend(request, executionPlan, hasExecutionPlan) {
		contract = removeExternalSendContract(request.ToolSet, contract)
	}
	return normalizeOutcomeContract(contract)
}

func removeIntermediateAttachmentEvidence(toolSet *ToolSet, contract OutcomeContract) OutcomeContract {
	requiredEvidenceTools := []string{}
	for _, toolName := range contract.RequiredEvidenceTools {
		if toolProducesIntermediateAttachmentSource(toolSet, toolName) {
			continue
		}
		requiredEvidenceTools = appendUniqueStrings(requiredEvidenceTools, toolName)
	}
	contract.RequiredEvidenceTools = requiredEvidenceTools
	filteredGroups := [][]string{}
	for _, group := range contract.RequiredEvidenceAnyOf {
		filteredGroup := []string{}
		for _, toolName := range group {
			if !toolProducesIntermediateAttachmentSource(toolSet, toolName) {
				filteredGroup = appendUniqueStrings(filteredGroup, toolName)
			}
		}
		if len(filteredGroup) > 0 {
			filteredGroups = append(filteredGroups, filteredGroup)
		}
	}
	contract.RequiredEvidenceAnyOf = filteredGroups
	return contract
}

func toolProducesIntermediateAttachmentSource(toolSet *ToolSet, toolName string) bool {
	toolDefinition, isFound := toolDefinitionForName(toolSet, toolName)
	if !isFound {
		return false
	}
	return toolDefinition.SideEffectClass == ToolSideEffectWorkspaceWrite &&
		toolDefinition.Completion.TargetKind == "file" &&
		!toolResultContractAttachesFile(toolDefinition.ResultContract)
}

func toolResultContractAttachesFile(resultContract *ToolResultContract) bool {
	if resultContract == nil {
		return false
	}
	for _, effect := range resultContract.Effects {
		if effect.ObjectType == "file" && effect.Effect == "attached" {
			return true
		}
	}
	return false
}

func requestExpectsExternalSend(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool) bool {
	_ = request
	if !hasExecutionPlan {
		return true
	}
	return executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend
}

func outcomeContractExpectsFileResult(contract OutcomeContract) bool {
	return len(contract.RequiredAttachmentSuffixes) > 0 ||
		evidenceToolsContainArtifactDelivery(contract.RequiredEvidenceTools) ||
		evidenceAnyOfContainsArtifactDelivery(contract.RequiredEvidenceAnyOf) ||
		expectedResultIncludesType(contract, ExpectedResultTypeFile)
}

func outcomeContractRequiresPlatformMessageMaintenance(toolSet *ToolSet, contract OutcomeContract) bool {
	for _, toolName := range outcomeContractToolNames(contract) {
		if toolIsInNamespace(toolSet, toolName, "message") && !isSendEvidenceTool(toolSet, toolName) {
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

func removeExternalSendContract(toolSet *ToolSet, contract OutcomeContract) OutcomeContract {
	for _, toolName := range outcomeContractToolNames(contract) {
		if !isSendEvidenceTool(toolSet, toolName) {
			continue
		}
		contract.RequiredEvidenceTools = removeToolName(contract.RequiredEvidenceTools, toolName)
		contract.RequiredEvidenceAnyOf = removeToolNameGroups(contract.RequiredEvidenceAnyOf, toolName)
	}
	return contract
}

func removeImplicitSiteFileContract(contract OutcomeContract) OutcomeContract {
	contract.RequiredAttachmentSuffixes = nil
	contract.RequiredEvidenceTools = removeToolName(contract.RequiredEvidenceTools, FileDeliverToolName)
	contract.RequiredEvidenceAnyOf = removeToolNameGroups(contract.RequiredEvidenceAnyOf, FileDeliverToolName)
	contract.ExpectedResults = removeExpectedResultsByType(contract.ExpectedResults, ExpectedResultTypeFile)
	return contract
}

func dischargeResolvedInputContract(request AgentRequest, turnDecision TurnDecision, contract OutcomeContract) OutcomeContract {
	if !resolvesActiveGoalInput(request, turnDecision) {
		return contract
	}
	contract.RequiredEvidenceTools = removeToolName(contract.RequiredEvidenceTools, AskInputToolName)
	contract.RequiredEvidenceAnyOf = removeToolNameGroups(contract.RequiredEvidenceAnyOf, AskInputToolName)
	contract.SelectedEvidenceHints = removeToolName(contract.SelectedEvidenceHints, AskInputToolName)
	contract.ExpectedResults = dischargeExpectedResultTool(contract.ExpectedResults, AskInputToolName)
	return normalizeOutcomeContract(contract)
}

func resolvesActiveGoalInput(request AgentRequest, turnDecision TurnDecision) bool {
	if request.ActiveGoal.Status != ActiveGoalStatusWaitingUserInput {
		return false
	}
	taskRunID := strings.TrimSpace(request.ActiveGoal.TaskRunID)
	if taskRunID == "" || taskRunID != strings.TrimSpace(request.ExistingTaskRunID) {
		return false
	}
	return turnDecision.Route == TurnRouteContinueTask || turnDecision.Route == TurnRouteReviseTask
}

func dischargeExpectedResultTool(results []ExpectedResult, toolName string) []ExpectedResult {
	filteredResults := []ExpectedResult{}
	for _, result := range results {
		if !expectedResultRequiresNamedTool(result, toolName) {
			filteredResults = append(filteredResults, result)
			continue
		}
		result.AcceptanceHints = removeToolName(result.AcceptanceHints, toolName)
		if len(result.AcceptanceHints) == 0 {
			continue
		}
		filteredResults = append(filteredResults, result)
	}
	return filteredResults
}

func expectedResultRequiresNamedTool(result ExpectedResult, toolName string) bool {
	for _, hint := range result.AcceptanceHints {
		if ToolNamesMatch(hint, toolName) {
			return true
		}
	}
	return false
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

func expectedResultsForRequest(intakeDecision IntakeDecision, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredAttachmentSuffixes []string) []ExpectedResult {
	results := append([]ExpectedResult{}, intakeDecision.ExpectedResults...)
	if hasExecutionPlan && executionPlan.PublicDeploy {
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
	if len(requiredAttachmentSuffixes) > 0 {
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

func requiredSendEvidenceToolsForContract(toolSet *ToolSet, contract OutcomeContract) []string {
	if contractRequiresSendTool(toolSet, contract) {
		return sendEvidenceToolsFromValues(toolSet, outcomeContractRequiredToolNames(contract))
	}
	return nil
}

func sendEvidenceToolsFromValues(toolSet *ToolSet, values []string) []string {
	toolNames := []string{}
	for _, value := range values {
		if isSendEvidenceTool(toolSet, value) {
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
		if isSendEvidenceTool(toolSet, toolName) {
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
	if isSendEvidenceTool(request.ToolSet, trimmedToolName) {
		return activeGoalRequiresTool(request.ActiveGoal, trimmedToolName) ||
			(hasExecutionPlan && (executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend))
	}
	if toolIsInNamespace(request.ToolSet, trimmedToolName, "message") {
		return intakeDecision.TaskShape == TaskShapeMaintenanceTask ||
			activeGoalMentionsTool(request.ActiveGoal, trimmedToolName) ||
			contractRequiresToolNamespace(request.ToolSet, request.ActiveGoal.OutcomeContract, "message")
	}
	if activeGoalRequiresTool(request.ActiveGoal, trimmedToolName) {
		return true
	}
	if IsArtifactDeliveryTool(trimmedToolName) {
		return len(requiredAttachmentSuffixes) > 0
	}
	if toolIsInNamespace(request.ToolSet, trimmedToolName, "site") {
		return hasExecutionPlan && executionPlan.PublicDeploy ||
			contractRequiresToolNamespace(request.ToolSet, request.ActiveGoal.OutcomeContract, "site")
	}
	if toolIsInNamespace(request.ToolSet, trimmedToolName, "schedule") {
		return intakeDecision.TaskShape == TaskShapeScheduledTask
	}
	return false
}

func isSendEvidenceTool(toolSet *ToolSet, toolName string) bool {
	toolDefinition, isFound := toolDefinitionForName(toolSet, toolName)
	return isFound && toolDefinition.SideEffectClass == ToolSideEffectExternalSend
}

func toolIsInNamespace(toolSet *ToolSet, toolName string, namespace string) bool {
	toolDefinition, isFound := toolDefinitionForName(toolSet, toolName)
	return isFound && toolDefinition.Namespace == strings.TrimSpace(namespace)
}

func requiredEvidenceIncludesAnySideEffectClass(toolSet *ToolSet, toolNames []string, sideEffectClasses ...string) bool {
	expectedSideEffectClasses := stringSet(sideEffectClasses)
	for _, toolName := range toolNames {
		toolDefinition, isFound := toolDefinitionForName(toolSet, toolName)
		if isFound && expectedSideEffectClasses[ToolDefinitionSideEffectClass(toolDefinition)] {
			return true
		}
	}
	return false
}

func toolDefinitionForName(toolSet *ToolSet, toolName string) (ToolDefinition, bool) {
	if toolSet == nil {
		return ToolDefinition{}, false
	}
	return toolSet.ToolDefinition(strings.TrimSpace(toolName))
}

func contractRequiresToolNamespace(toolSet *ToolSet, contract OutcomeContract, namespace string) bool {
	for _, toolName := range outcomeContractRequiredToolNames(contract) {
		if toolIsInNamespace(toolSet, toolName, namespace) {
			return true
		}
	}
	return false
}

func contractRequiresSendTool(toolSet *ToolSet, contract OutcomeContract) bool {
	for _, toolName := range outcomeContractRequiredToolNames(contract) {
		if isSendEvidenceTool(toolSet, toolName) {
			return true
		}
	}
	return false
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
	return contractRequiresSendTool(request.ToolSet, contract)
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
	activeGoal.SelectedToolNames = appendUniqueStrings(activeGoal.SelectedToolNames, request.PinnedToolNames...)
	activeGoal.SelectedSkillNames = appendUniqueStrings(activeGoal.SelectedSkillNames, request.PinnedSkillNames...)
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

func selectedSkillNameList(skillDecisions []SkillSelectionDecision) []string {
	selectedNames := []string{}
	for _, skillDecision := range skillDecisions {
		if skillDecision.Status == "selected" {
			selectedNames = appendUniqueStrings(selectedNames, skillDecision.Name)
		}
	}
	return selectedNames
}

func activeGoalFromExecutionPlan(taskRunID string, executionPlan ExecutionPlan, status ActiveGoalStatus, toolSet *ToolSet, evidenceHints []string, requiredAttachmentSuffixes []string) ActiveGoal {
	outcomeContract := normalizeOutcomeContract(OutcomeContract{
		RequiredEvidenceTools:      executionPlanEvidenceTools(toolSet, executionPlan, evidenceHints),
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

func executionPlanEvidenceTools(toolSet *ToolSet, executionPlan ExecutionPlan, evidenceHints []string) []string {
	toolNames := []string{}
	for _, toolName := range evidenceHints {
		if isSendEvidenceTool(toolSet, toolName) && (executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend) {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
		if toolIsInNamespace(toolSet, toolName, "site") && executionPlan.PublicDeploy {
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
		case "json":
			suffixes = append(suffixes, ".json")
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

func expectedResultIncludesType(outcomeContract OutcomeContract, resultType string) bool {
	for _, expectedResult := range outcomeContract.ExpectedResults {
		if strings.TrimSpace(expectedResult.Type) == resultType {
			return true
		}
	}
	return false
}
