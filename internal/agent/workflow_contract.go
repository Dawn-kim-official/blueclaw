package agent

func workflowToolNamesForTurnRequest(request AgentTurnRequest) []string {
	toolNames := []string{}
	for _, toolName := range request.RequiredEvidenceTools {
		toolNames = appendUniqueStrings(toolNames, callableToolNameForRequiredEvidence(request.ToolSet, toolName))
	}
	return registeredToolNamesOnly(request.ToolSet, toolNames)
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
