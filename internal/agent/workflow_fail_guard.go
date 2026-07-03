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
	sourceChangeIndex := latestSuccessfulToolIndex(observations, []string{"file.write", "file.edit", "file.patch", "site.create"})
	if sourceChangeIndex < 0 {
		return nil
	}
	publishIndex := latestSuccessfulToolIndexAfter(observations, []string{"site.publish"}, sourceChangeIndex)
	// site.publish is a domain operation reached only through capability.invoke
	// now, so its recovery availability means registration, not direct
	// action-schema allow-listing (toolAvailableForAction).
	if publishIndex < 0 && request.ToolSet != nil && request.ToolSet.IsRegistered("site.publish") {
		return []string{"site.publish"}
	}
	return nil
}

func recoverableFileDeliveryNextTools(request AgentTurnRequest, observations []turnObservation) []string {
	if !turnRequestLooksLikeFileDeliveryWork(request) {
		return nil
	}
	if latestSuccessfulToolIndex(observations, []string{FileDeliverToolName, FileAttachToolName}) >= 0 {
		return nil
	}
	if latestSuccessfulToolIndex(observations, []string{"file.write", "file.edit", "file.patch", "terminal.run"}) < 0 {
		return nil
	}
	return availableWorkflowTools(request.ToolSet, []string{"terminal.run", FileDeliverToolName})
}

func turnRequestLooksLikeSitePrototypeWork(request AgentTurnRequest) bool {
	return activeGoalRequiresToolPrefix(request.ActiveGoal, "site.") ||
		contractRequiresToolPrefix(request.OutcomeContract, "site.") ||
		requiredEvidenceContains(request.RequiredEvidenceTools, "site.publish")
}

func sitePublishIsRequired(request AgentTurnRequest) bool {
	return requiredEvidenceContains(request.RequiredEvidenceTools, "site.publish") ||
		requiredEvidenceContains(request.OutcomeContract.RequiredEvidenceTools, "site.publish") ||
		contractRequiresToolPrefix(request.OutcomeContract, "site.") ||
		expectedResultsIncludeSiteRequirement(request.OutcomeContract.ExpectedResults)
}

func turnRequestLooksLikeFileDeliveryWork(request AgentTurnRequest) bool {
	return len(request.RequiredAttachmentSuffixes) > 0 ||
		len(request.OutcomeContract.RequiredAttachmentSuffixes) > 0 ||
		requiredEvidenceContains(request.RequiredEvidenceTools, FileDeliverToolName) ||
		requiredEvidenceContains(request.OutcomeContract.RequiredEvidenceTools, FileDeliverToolName) ||
		requiredEvidenceContains(request.RequiredEvidenceTools, FileAttachToolName) ||
		requiredEvidenceContains(request.OutcomeContract.RequiredEvidenceTools, FileAttachToolName)
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
