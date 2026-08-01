package bluecollar

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
	for _, message := range (TurnRouter{}).buildMessages(AgentRequest{Prompt: "turn this file into a deck"}) {
		if message.Role == "system" {
			systemPrompt += message.Content
		}
	}

	if !strings.Contains(systemPrompt, "unsupported ONLY for requests that are pointless to even attempt") {
		t.Fatal("router prompt must reserve unsupported for requests pointless to even attempt")
	}
	if !strings.Contains(systemPrompt, "NOT a security or permission gate") {
		t.Fatal("router prompt must state unsupported is not a security/permission gate (POSIX enforces at execution)")
	}
	if !strings.Contains(systemPrompt, "never pre-refuse over permissions, just attempt it") {
		t.Fatal("router prompt must push the model to attempt permission-gated work rather than pre-refuse")
	}
	if strings.Contains(systemPrompt, "report a tool wiring failure") {
		t.Fatal("router prompt must not instruct naming evidence to trigger a removed wiring-failure block")
	}
}
