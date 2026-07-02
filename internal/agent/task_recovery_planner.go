package agent

type TaskRecoveryPlanner struct{}

func (TaskRecoveryPlanner) Plan(request AgentRequest, decision IntakeDecision) IntakeDecision {
	if shouldRetryUnsupportedLocalArtifact(request, decision) {
		decision.Classification = IntakeClassificationBoundedTask
		decision.Reason = "artifact retry recovery route selected from available terminal and file delivery capabilities"
		decision.UserFacingReply = ""
	}
	return decision
}

func shouldRetryUnsupportedLocalArtifact(request AgentRequest, decision IntakeDecision) bool {
	if decision.Classification != IntakeClassificationUnsupported {
		return false
	}
	if !hasAllTools(request.ToolSet, []string{TerminalRunToolName, FileDeliverToolName}) {
		return false
	}
	return normalizeOutputKind(decision.OutputKind) == OutputKindFile || hasArtifactOutputFormat(decision.RequestedOutputFormats)
}

func hasArtifactOutputFormat(formats []string) bool {
	for _, format := range normalizeRequestedOutputFormats(formats) {
		switch format {
		case "html", "pptx", "pdf", "txt", "docx", "xlsx", "csv":
			return true
		}
	}
	return false
}
