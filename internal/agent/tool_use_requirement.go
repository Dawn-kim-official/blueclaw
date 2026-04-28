package agent

import (
	"strings"
)

type toolUseRequirement struct {
	ToolPrefix string
	Reason     string
}

func deriveToolUseRequirements(request AgentTurnRequest) []toolUseRequirement {
	requirements := []toolUseRequirement{}
	if requestRequiresBrowserEvidence(request) {
		requirements = append(requirements, toolUseRequirement{
			ToolPrefix: "browser.",
			Reason:     "the request asks for browser state or browser control",
		})
	}
	return requirements
}

func missingToolUseRequirement(requirements []toolUseRequirement, observations []turnObservation) (turnObservation, bool) {
	for _, requirement := range requirements {
		if !hasObservedToolPrefix(observations, requirement.ToolPrefix) {
			toolScope := strings.TrimSuffix(requirement.ToolPrefix, ".")
			return turnObservation{
				Action:  "policy",
				Tool:    toolScope,
				Content: "Required tool scope has not been attempted yet: " + toolScope + ". Reason: " + requirement.Reason + ". Call an available " + toolScope + " tool before final_reply.",
				IsError: true,
			}, true
		}
	}
	return turnObservation{}, false
}

func hasObservedToolPrefix(observations []turnObservation, toolPrefix string) bool {
	for _, observation := range observations {
		if strings.HasPrefix(strings.TrimSpace(observation.Tool), toolPrefix) {
			return true
		}
	}
	return false
}

func requestRequiresBrowserEvidence(request AgentTurnRequest) bool {
	if !hasToolPrefix(request.ToolRegistry, "browser.") {
		return false
	}
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	return looksLikeBrowserControlSequence(prompt) || containsAny(prompt, []string{"google", "구글", "browser", "브라우저", "search", "검색", "screenshot", "스크린샷"})
}
