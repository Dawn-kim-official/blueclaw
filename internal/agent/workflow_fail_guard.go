package agent

import "strings"

func recoverableWorkflowFailResult(request AgentTurnRequest, observations []turnObservation) (completionGateResult, bool) {
	if _, hasFailureDebt := activeFailureDebt(observations); hasFailureDebt {
		return completionGateResult{}, false
	}
	nextTools := recoverableWorkflowNextTools(request, observations)
	if len(nextTools) == 0 {
		return completionGateResult{}, false
	}
	message := "The Task expected result is not complete yet after successful workflow progress. Continue with the next delivery step instead of failing: " + strings.Join(nextTools, ", ")
	return completionGateResult{
		Message:            message,
		SuggestedNextTools: nextTools,
	}, true
}

func recoverableWorkflowNextTools(request AgentTurnRequest, observations []turnObservation) []string {
	if !turnRequestLooksLikeSitePrototypeWork(request) {
		return recoverableFileDeliveryNextTools(request, observations)
	}
	if !sitePublishIsRequired(request) {
		return nil
	}
	sourceChangeIndex := latestSuccessfulToolIndex(observations, []string{"file.write", "file.edit"})
	if sourceChangeIndex < 0 {
		return nil
	}
	publishIndex := latestSuccessfulToolIndexAfter(observations, []string{"site.serve"}, sourceChangeIndex)
	if publishIndex < 0 && request.ToolSet != nil && request.ToolSet.IsRegistered("site.serve") {
		return []string{"site.serve"}
	}
	return nil
}

func recoverableFileDeliveryNextTools(request AgentTurnRequest, observations []turnObservation) []string {
	if !turnRequestLooksLikeFileDeliveryWork(request) {
		return nil
	}
	if latestSuccessfulToolIndex(observations, []string{FileDeliverToolName}) >= 0 {
		return nil
	}
	if latestSuccessfulToolIndex(observations, []string{"file.write", "file.edit", "terminal.run"}) < 0 {
		return nil
	}
	return availableWorkflowTools(request.ToolSet, []string{"terminal.run", FileDeliverToolName})
}

func turnRequestLooksLikeSitePrototypeWork(request AgentTurnRequest) bool {
	return contractRequiresToolNamespace(request.ToolSet, request.ActiveGoal.OutcomeContract, "site") ||
		contractRequiresToolNamespace(request.ToolSet, request.OutcomeContract, "site") ||
		requiredEvidenceIncludesNamespace(request.ToolSet, request.RequiredEvidenceTools, "site")
}

func sitePublishIsRequired(request AgentTurnRequest) bool {
	return requiredEvidenceIncludesAnySideEffectClass(request.ToolSet, request.RequiredEvidenceTools, ToolSideEffectExternalPublish, ToolSideEffectSitePublish) ||
		requiredEvidenceIncludesAnySideEffectClass(request.ToolSet, request.OutcomeContract.RequiredEvidenceTools, ToolSideEffectExternalPublish, ToolSideEffectSitePublish) ||
		expectedResultsIncludeSiteRequirement(request.OutcomeContract.ExpectedResults)
}

func turnRequestLooksLikeFileDeliveryWork(request AgentTurnRequest) bool {
	return len(request.RequiredAttachmentSuffixes) > 0 ||
		len(request.OutcomeContract.RequiredAttachmentSuffixes) > 0 ||
		requiredEvidenceContains(request.RequiredEvidenceTools, FileDeliverToolName) ||
		requiredEvidenceContains(request.OutcomeContract.RequiredEvidenceTools, FileDeliverToolName)
}

func availableWorkflowTools(toolSet *ToolSet, toolNames []string) []string {
	tools := []string{}
	for _, toolName := range toolNames {
		if toolAvailableForAction(toolSet, toolName) {
			tools = append(tools, toolName)
		}
	}
	return tools
}

func latestSuccessfulToolIndex(observations []turnObservation, toolNames []string) int {
	return latestSuccessfulToolIndexAfter(observations, toolNames, -1)
}

func latestSuccessfulToolIndexAfter(observations []turnObservation, toolNames []string, afterIndex int) int {
	toolNameSet := stringSet(toolNames)
	latestIndex := -1
	for index, observation := range observations {
		if index <= afterIndex || observation.Action != "continue" || observation.Failed() {
			continue
		}
		if toolNameSet[strings.TrimSpace(observation.Tool)] {
			latestIndex = index
		}
	}
	return latestIndex
}
