package agent

import (
	"strings"
)

type toolUseRequirement struct {
	ToolPrefix         string
	ToolName           string
	Reason             string
	RequiresAttachment bool
	AttachmentSuffixes []string
}

func deriveToolUseRequirements(request AgentTurnRequest) []toolUseRequirement {
	requirements := evidenceToolRequirements(request)
	if directMessageEvidenceRequired(request) {
		return requirements
	}
	if requestRequiresBrowserScreenshot(request) {
		requirements = append(requirements, toolUseRequirement{
			ToolName:           "browser.screenshot",
			Reason:             "the request asks for a screenshot",
			RequiresAttachment: true,
		})
		return requirements
	}
	if requestRequiresBrowserEvidence(request) {
		requirements = append(requirements, toolUseRequirement{
			ToolPrefix: "browser.",
			Reason:     "the request asks for browser state or browser control",
		})
	}
	return requirements
}

func directMessageEvidenceRequired(request AgentTurnRequest) bool {
	if requiredEvidenceContains(request.RequiredEvidenceTools, "platform.dm.send") {
		return true
	}
	return selectedSkillNameSet(request.SkillDecisions)["direct-message"]
}

func evidenceToolRequirements(request AgentTurnRequest) []toolUseRequirement {
	requirements := []toolUseRequirement{}
	seenToolName := map[string]bool{}
	for _, toolName := range request.RequiredEvidenceTools {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" || seenToolName[trimmedToolName] {
			continue
		}
		seenToolName[trimmedToolName] = true
		requirements = append(requirements, toolUseRequirement{
			ToolName:           trimmedToolName,
			Reason:             "selected skill requires completion evidence",
			RequiresAttachment: strings.HasSuffix(trimmedToolName, ".attach"),
			AttachmentSuffixes: attachmentSuffixesForEvidenceTool(trimmedToolName, request.RequiredAttachmentSuffixes),
		})
	}
	return requirements
}

func attachmentSuffixesForEvidenceTool(toolName string, suffixes []string) []string {
	if !strings.HasSuffix(toolName, ".attach") {
		return nil
	}
	trimmedSuffixes := []string{}
	seenSuffix := map[string]bool{}
	for _, suffix := range suffixes {
		trimmedSuffix := strings.TrimSpace(suffix)
		if trimmedSuffix == "" || seenSuffix[trimmedSuffix] {
			continue
		}
		seenSuffix[trimmedSuffix] = true
		trimmedSuffixes = append(trimmedSuffixes, trimmedSuffix)
	}
	return trimmedSuffixes
}

func requestRequiresBrowserEvidence(request AgentTurnRequest) bool {
	if !hasToolPrefix(request.ToolSet, "browser.") {
		return false
	}
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	if looksLikeBrowserFollowUp(prompt) && visibleContextMentionsBrowserWork(request.VisibleContext) {
		return true
	}
	if looksLikeBrowserControlSequence(prompt) {
		return true
	}
	if containsAny(prompt, []string{"browser", "브라우저", "search", "검색"}) {
		return true
	}
	return containsAny(prompt, []string{"google", "구글"}) && !mentionsGoogleWorkspaceAvoidance(prompt)
}

func requestOnlyOpensBrowser(request AgentTurnRequest) bool {
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	if prompt == "" {
		return false
	}
	if containsAny(prompt, []string{"검색", "search", "find", "찾", "스크린샷", "screenshot", "click", "클릭", "입력", "type", "go to", "이동", "페이지", "observe", "read", "확인"}) {
		return false
	}
	return containsAny(prompt, []string{"브라우저 열", "브라우저 켜", "open browser", "open the browser"})
}

func looksLikeBrowserFollowUp(prompt string) bool {
	if prompt == "" {
		return false
	}
	return containsAny(prompt, []string{
		"다시 해", "다시 열", "다시 시도", "계속해", "진행해", "이제 연결", "연결했",
		"try again", "open it again", "do it again", "continue", "go ahead", "connected now",
	})
}

func visibleContextMentionsBrowserWork(visibleContext VisibleContext) bool {
	for _, message := range visibleContext.Messages {
		text := strings.ToLower(strings.TrimSpace(message.Text))
		if text == "" {
			continue
		}
		if containsAny(text, []string{
			"browser", "브라우저", "companion", "컴패니언", "login", "로그인",
			"credential", "credentials", "자격 증명", "인증 정보",
			"google cloud console", "cloud console", "구글 클라우드 콘솔",
		}) {
			return true
		}
	}
	return false
}

func mentionsGoogleWorkspaceAvoidance(prompt string) bool {
	if !containsAny(prompt, []string{"google workspace", "구글 워크스페이스", "gws"}) {
		return false
	}
	return containsAny(prompt, []string{"don't use", "do not use", "without", "not use", "쓰지 말", "사용하지 말", "빼고", "없이"})
}

func requestRequiresBrowserScreenshot(request AgentTurnRequest) bool {
	if !hasToolPrefix(request.ToolSet, "browser.screenshot") {
		return false
	}
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	return containsAny(prompt, []string{"screenshot", "스크린샷"})
}
