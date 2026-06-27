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
		return nil
	}
	if !sitePublishIsRequired(request) {
		return nil
	}
	sourceChangeIndex := latestSuccessfulToolIndex(observations, []string{"file.write", "file.edit", "file.patch", "site.app.create"})
	if sourceChangeIndex < 0 {
		return nil
	}
	buildIndex := latestSuccessfulToolIndexAfter(observations, []string{"site.app.build"}, sourceChangeIndex)
	if buildIndex < 0 && toolAvailableForAction(request.ToolSet, "site.app.build") {
		return []string{"site.app.build"}
	}
	publishIndex := latestSuccessfulToolIndexAfter(observations, []string{"site.app.publish"}, maxInt(sourceChangeIndex, buildIndex))
	if publishIndex < 0 && toolAvailableForAction(request.ToolSet, "site.app.publish") {
		return []string{"site.app.publish"}
	}
	return nil
}

func turnRequestLooksLikeSitePrototypeWork(request AgentTurnRequest) bool {
	return workKindsContain(request.WorkKinds, WorkKindSitePrototype) ||
		activeGoalRequiresToolPrefix(request.ActiveGoal, "site.app.") ||
		contractRequiresToolPrefix(request.OutcomeContract, "site.app.") ||
		requiredEvidenceContains(request.RequiredEvidenceTools, "site.app.publish")
}

func sitePublishIsRequired(request AgentTurnRequest) bool {
	return requiredEvidenceContains(request.RequiredEvidenceTools, "site.app.publish") ||
		requiredEvidenceContains(request.OutcomeContract.RequiredEvidenceTools, "site.app.publish") ||
		contractRequiresToolPrefix(request.OutcomeContract, "site.app.") ||
		expectedResultsIncludeSiteRequirement(request.OutcomeContract.ExpectedResults)
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
