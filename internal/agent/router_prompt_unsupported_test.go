package agent

import (
	"strings"
	"testing"
)

// The intake router used to refuse doable work (e.g. generating a pptx) by
// classifying it unsupported, and the required-evidence guidance told the model
// to name evidence purely to trigger a now-removed wiring-failure hard-block.
// Keep the prompt reserving unsupported for genuinely impossible requests and
// pushing the model to attempt anything plausibly doable.
func TestRouterPromptReservesUnsupportedForImpossibleWork(t *testing.T) {
	systemPrompt := ""
	for _, message := range (TurnRouter{}).buildMessages(AgentRequest{Prompt: "이 파일로 발표자료 만들어줘"}) {
		if message.Role == "system" {
			systemPrompt += message.Content
		}
	}

	if !strings.Contains(systemPrompt, "unsupported ONLY for requests that are genuinely impossible") {
		t.Fatal("router prompt must reserve unsupported for genuinely impossible requests")
	}
	if !strings.Contains(systemPrompt, "lean toward attempting rather than pre-refusing") {
		t.Fatal("router prompt must push the model to attempt doable work rather than pre-refuse")
	}
	if strings.Contains(systemPrompt, "report a tool wiring failure") {
		t.Fatal("router prompt must not instruct naming evidence to trigger a removed wiring-failure block")
	}
}
