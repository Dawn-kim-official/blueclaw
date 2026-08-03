package bluecollar

func requiredWorkflowEffectRequirementsForRequest(AgentRequest) []OutcomeEffect {
	return nil
}

func workflowEvidenceHintMatchesRequest(string, AgentRequest) bool {
	return false
}

func appendOutcomeEffects(effects []OutcomeEffect, candidates ...OutcomeEffect) []OutcomeEffect {
	return normalizeOutcomeEffects(append(append([]OutcomeEffect{}, effects...), candidates...))
}
