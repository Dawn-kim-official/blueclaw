package agent

import (
	"strings"
)

type toolUseRequirement struct {
	ToolPrefix         string
	ToolName           string
	Reason             string
	RequiresAttachment bool
}

func deriveToolUseRequirements(request AgentTurnRequest) []toolUseRequirement {
	requirements := []toolUseRequirement{}
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

func requestRequiresBrowserEvidence(request AgentTurnRequest) bool {
	if !hasToolPrefix(request.ToolRegistry, "browser.") {
		return false
	}
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	return looksLikeBrowserControlSequence(prompt) || containsAny(prompt, []string{"google", "구글", "browser", "브라우저", "search", "검색", "screenshot", "스크린샷"})
}

func requestRequiresBrowserScreenshot(request AgentTurnRequest) bool {
	if !hasToolPrefix(request.ToolRegistry, "browser.screenshot") {
		return false
	}
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	return containsAny(prompt, []string{"screenshot", "스크린샷"})
}
