package agent

func workflowToolNamesForTurnRequest(request AgentTurnRequest) []string {
	toolNames := []string{}
	for _, toolName := range request.RequiredEvidenceTools {
		registeredToolName, isRegistered := requiredEvidenceRegisteredToolName(request.ToolSet, toolName)
		if !isRegistered || !requiredEvidenceToolCanBeSatisfied(request.ToolSet, registeredToolName) || !request.ToolSet.CanExpose(registeredToolName) {
			continue
		}
		toolNames = appendUniqueStrings(toolNames, registeredToolName)
	}
	return toolNames
}

func requiredWorkflowEffectRequirementsForRequest(AgentRequest) []OutcomeEffect {
	return nil
}

func workflowEvidenceHintMatchesRequest(string, AgentRequest) bool {
	return false
}

func appendOutcomeEffects(effects []OutcomeEffect, candidates ...OutcomeEffect) []OutcomeEffect {
	return normalizeOutcomeEffects(append(append([]OutcomeEffect{}, effects...), candidates...))
}
