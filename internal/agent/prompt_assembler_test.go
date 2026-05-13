package agent

import (
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
)

func TestPromptAssemblerIncludesTemporalContext(t *testing.T) {
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt:        "내일 오후 6시 회식 추가해줘",
		TurnStartedAt: time.Date(2026, 5, 12, 8, 32, 27, 0, time.UTC),
	}, nil, "base", "")
	body := joinMessageContent(messages)

	for _, expected := range []string{
		"Runtime temporal context:",
		"Current date: 2026-05-12",
		"Current time: 2026-05-12T17:32:27+09:00",
		"Time zone: Asia/Seoul",
		"Resolve relative dates",
		"내일",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected temporal context %q, got %s", expected, body)
		}
	}
}

func TestPromptAssemblerOmitsRawBrowserSnapshotOutput(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "call_tool",
		Tool:          "browser.snapshot",
		Content:       `{"url":"https://example.com","title":"Example","snapshotText":"VISIBLE START ` + strings.Repeat("raw-page-text ", 500) + ` SECRET_COOKIE_VALUE","interactiveRefs":["@e1","@e2"],"profilePath":"/Users/me/Profile","cdpURL":"ws://localhost:9222"}`,
	}}

	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "summarize the page",
	}, observations, "base", "")
	body := joinMessageContent(messages)

	if strings.Contains(body, "SECRET_COOKIE_VALUE") || strings.Contains(body, "/Users/me/Profile") || strings.Contains(body, "ws://localhost:9222") {
		t.Fatalf("expected unsafe raw browser output to be omitted, got %s", body)
	}
	if !strings.Contains(body, "Progress ledger") || !strings.Contains(body, "obs-001") || !strings.Contains(body, "@e1") {
		t.Fatalf("expected compact progress with observation and refs, got %s", body)
	}
}

func TestPromptAssemblerIncludesTurnDateContext(t *testing.T) {
	turnStartedAt := time.Date(2026, time.May, 8, 18, 1, 10, 0, time.UTC)
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt:        "내일부터 화요일까지 휴가 등록해줘",
		TurnStartedAt: turnStartedAt,
	}, nil, "base", "")
	body := joinMessageContent(messages)

	if !strings.Contains(body, "Runtime context") || !strings.Contains(body, "Current turn date: 2026-05-09") || !strings.Contains(body, "Default calendar timezone: Asia/Seoul") {
		t.Fatalf("expected turn date context, got %s", body)
	}
}

func TestPromptAssemblerDoesNotExposeAttachmentDevicePath(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "call_tool",
		Tool:          "browser.screenshot",
		Content:       `{"devicePath":"/tmp/internkim-companion-files/screen.png","filename":"screen.png","contentType":"image/png"}`,
		Attachments: []FileAttachment{{
			DevicePath:  "/tmp/internkim-companion-files/screen.png",
			Filename:    "screen.png",
			ContentType: "image/png",
			SizeBytes:   123,
		}},
	}}

	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "send the screenshot",
	}, observations, "base", "")
	body := joinMessageContent(messages)

	if strings.Contains(body, "/tmp/internkim-companion-files/screen.png") || strings.Contains(body, "devicePath") {
		t.Fatalf("expected device path to stay out of prompt, got %s", body)
	}
	if !strings.Contains(body, `"attachmentIndex":0`) || !strings.Contains(body, `"filename":"screen.png"`) {
		t.Fatalf("expected attachment evidence reference, got %s", body)
	}
}

func TestPromptAssemblerCompressesLongObservationHistory(t *testing.T) {
	observations := []turnObservation{}
	for index := 0; index < 20; index++ {
		observations = append(observations, turnObservation{
			ObservationID: "obs-" + strings.Repeat("0", 2) + string(rune('a'+index)),
			Action:        "call_tool",
			Tool:          "browser.snapshot",
			Content:       `{"snapshotText":"` + strings.Repeat("very-long-page-output ", 200) + `","interactiveRefs":["@old"]}`,
		})
	}
	observations = append(observations, turnObservation{
		ObservationID: "obs-latest",
		Action:        "call_tool",
		Tool:          "browser.snapshot",
		Content:       `{"snapshotText":"latest form","interactiveRefs":["@latestSearch","@latestButton"]}`,
	})

	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "continue",
	}, observations, "base", "")
	body := joinMessageContent(messages)

	if len(body) > 16000 {
		t.Fatalf("expected compact prompt, got %d bytes", len(body))
	}
	if strings.Contains(body, strings.Repeat("very-long-page-output ", 50)) {
		t.Fatalf("expected long raw output to be compressed, got %s", body)
	}
	if !strings.Contains(body, "@latestSearch") {
		t.Fatalf("expected latest interactive refs to remain, got %s", body)
	}
}

func joinMessageContent(messages []llm.Message) string {
	parts := []string{}
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}
