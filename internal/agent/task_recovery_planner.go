package agent

import "strings"

type TaskRecoveryPlanner struct{}

func (TaskRecoveryPlanner) Plan(request AgentRequest, decision IntakeDecision) IntakeDecision {
	if shouldRetryUnsupportedLocalArtifact(request, decision) {
		decision.Classification = IntakeClassificationBoundedTask
		decision.Reason = "artifact retry recovery route selected from available terminal and file tools"
		decision.UserFacingReply = ""
	}
	return decision
}

func shouldRetryUnsupportedLocalArtifact(request AgentRequest, decision IntakeDecision) bool {
	if decision.Classification != IntakeClassificationUnsupported {
		return false
	}
	if looksLikeDestructiveLocalWork(strings.ToLower(strings.TrimSpace(request.Prompt))) {
		return false
	}
	if !hasAllTools(request.ToolSet, []string{"terminal.run", "file.write", "file.attach"}) {
		return false
	}
	if hasArtifactOutputFormat(decision.RequestedOutputFormats) {
		return true
	}
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	artifactWords := []string{"slide", "slides", "deck", "presentation", "ppt", "pptx", "pdf", "docx", "xlsx", "html", "artifact", "attach", "피피티", "파워포인트", "발표자료", "슬라이드", "문서", "자료", "첨부", "보내"}
	retryWords := []string{"retry", "again", "create", "make", "write", "generate", "export", "다시", "재시도", "만들", "작성", "생성", "줘", "보내"}
	return containsAny(prompt, artifactWords) && containsAny(prompt, retryWords)
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
