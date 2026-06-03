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
		"Runtime:",
		"Current date: 2026-05-12",
		"Current weekday: Tuesday",
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

func TestPromptAssemblerIncludesTemporalContextInDirectReplies(t *testing.T) {
	messages := (PromptAssembler{}).BuildReplyMessages("오늘 무슨 요일이야?", VisibleContext{}, "", "")
	body := joinMessageContent(messages)

	if !strings.Contains(body, "Runtime:") || !strings.Contains(body, "Current date:") || !strings.Contains(body, "Current weekday:") {
		t.Fatalf("expected direct reply temporal context, got %s", body)
	}
}

func TestPromptAssemblerIncludesStepBudgetContext(t *testing.T) {
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt:            "사이트 만들어줘",
		TurnStartedAt:     time.Date(2026, 5, 12, 8, 32, 27, 0, time.UTC),
		StepBudgetContext: "Step budget:\nTool calls: 10/24 used, 14 remaining.",
	}, nil, "base", "")
	body := joinMessageContent(messages)

	if !strings.Contains(body, "Step budget:") || !strings.Contains(body, "Tool calls: 10/24 used") {
		t.Fatalf("expected step budget context, got %s", body)
	}
}

func TestPromptAssemblerIncludesInputImagePart(t *testing.T) {
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "이거 뭔지 알아?",
		InputParts: []AgentPart{{
			Type: AgentPartTypeImage,
			Image: &AgentImagePart{
				MimeType:   "image/png",
				DataBase64: "aW1hZ2U=",
				Filename:   "mascot.png",
			},
			File: &AgentFilePart{
				Path:        "/workspace/private/people/person-1/inbox/mattermost/post-1/mascot.png",
				Filename:    "mascot.png",
				ContentType: "image/png",
			},
		}},
	}, nil, "base", "")

	if !messagesContainImagePart(messages) {
		t.Fatalf("expected input image part in LLM messages, got %+v", messages)
	}
	body := joinMessageContent(messages)
	if !strings.Contains(body, "Attached file:") || !strings.Contains(body, "mascot.png") {
		t.Fatalf("expected image file context text, got %s", body)
	}
}

func TestPromptAssemblerIncludesInputFileMarkdownPreview(t *testing.T) {
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "요약해줘",
		InputParts: []AgentPart{{
			Type: AgentPartTypeFile,
			File: &AgentFilePart{
				Path:             "/workspace/private/people/person-1/inbox/mattermost/post-1/report.pdf",
				Filename:         "report.pdf",
				ContentType:      "application/pdf",
				MarkdownPreview:  "# 보고서\n\n핵심 내용",
				ConversionStatus: "converted",
			},
		}},
	}, nil, "base", "")

	body := joinMessageContent(messages)
	for _, expected := range []string{"report.pdf", "Markdown preview:", "# 보고서", "핵심 내용"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected file markdown preview %q, got %s", expected, body)
		}
	}
}

func TestPromptAssemblerOmitsRawBrowserSnapshotOutput(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "browser.snapshot",
		Output:        ToolOutput{Content: `{"url":"https://example.com","title":"Example","snapshotText":"VISIBLE START ` + strings.Repeat("raw-page-text ", 500) + ` SECRET_COOKIE_VALUE","interactiveRefs":["@e1","@e2"],"profilePath":"/Users/me/Profile","cdpURL":"ws://localhost:9222"}`},
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

func TestPromptAssemblerIncludesRawToolResultSummary(t *testing.T) {
	fetchResult := `{"results":[{"url":"https://example.com","content":"Yeomyeonggeori provides AI automation and blockchain solutions."}]}`
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "web.fetch",
		Output:        ToolOutput{Content: fetchResult},
		Summary:       fetchResult,
	}}

	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "make a deck",
	}, observations, "base", "")
	body := joinMessageContent(messages)

	if !strings.Contains(body, "Tool result context") || !strings.Contains(body, "Yeomyeonggeori provides AI automation") {
		t.Fatalf("expected raw fetch summary in tool result context, got %s", body)
	}
}

func TestPromptAssemblerIncludesCheckpointMessages(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "checkpoint",
		Output:        ToolOutput{Content: `{"message":"사이트 스캐폴드를 만들고 있습니다.","status":"sent"}`},
		Summary:       "사이트 스캐폴드를 만들고 있습니다.",
	}}

	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "개인 홈페이지 만들어줘",
	}, observations, buildAgentSystemInstruction(AgentTurnRequest{}), "")
	body := joinMessageContent(messages)

	if !strings.Contains(body, "checkpointMessages") || !strings.Contains(body, "사이트 스캐폴드를 만들고 있습니다.") {
		t.Fatalf("expected checkpoint messages in progress context, got %s", body)
	}
	if !strings.Contains(body, "repeat-back plan") || !strings.Contains(body, "meaningful state change") {
		t.Fatalf("expected checkpoint policy instruction, got %s", body)
	}
}

func TestPromptAssemblerIncludesTurnDateContext(t *testing.T) {
	turnStartedAt := time.Date(2026, time.May, 8, 18, 1, 10, 0, time.UTC)
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt:        "내일부터 화요일까지 휴가 등록해줘",
		TurnStartedAt: turnStartedAt,
	}, nil, "base", "")
	body := joinMessageContent(messages)

	if !strings.Contains(body, "Runtime:") || !strings.Contains(body, "Current date: 2026-05-09") || !strings.Contains(body, "Current weekday: Saturday") || !strings.Contains(body, "Time zone: Asia/Seoul") {
		t.Fatalf("expected turn date context, got %s", body)
	}
}

func TestPromptAssemblerIncludesWritableWorkspaceContext(t *testing.T) {
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt:               "pptx 만들어줘",
		RequesterPersonID:    "person-1",
		TurnStartedAt:        time.Date(2026, time.May, 8, 18, 1, 10, 0, time.UTC),
		WorkspaceRootPath:    "/workspace",
		WorkspaceDefaultPath: "/workspace/private/people/person-1",
		ResponseLanguage:     "ko",
	}, nil, "base", "")
	body := joinMessageContent(messages)

	for _, expected := range []string{
		"Terminal commands run as the requester POSIX identity.",
		"Use virtual workspace paths in tool inputs",
		"Use home/<path> for requester-private durable source work.",
		"Use tmp/<artifact-slug> for draft artifact work.",
		"Use artifacts/<artifact-slug> for accepted final files.",
		"The requester has a private POSIX home directory",
		"Circle-shared files live under /workspace/circles/<circleID>",
		"/workspace/.blueclaw is service-owned runtime state",
		"ls -ld .",
		"stat -c '%A %U %G %n' .",
		"test -w .",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected workspace context %q, got %s", expected, body)
		}
	}
	if strings.Contains(body, "/workspace/private/people/") {
		t.Fatalf("workspace context must not expose concrete private paths, got %s", body)
	}
}

func TestPromptAssemblerDoesNotExposeAttachmentDevicePath(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "browser.screenshot",
		Output:        ToolOutput{Content: `{"devicePath":"/tmp/internkim-companion-files/screen.png","filename":"screen.png","contentType":"image/png"}`},
		Summary:       "Screenshot captured. Use the imageRefs for visual inspection.",
		ImageRefs: []ToolResultImageRef{{
			ObservationID:   "obs-001",
			AttachmentIndex: 0,
			MimeType:        "image/png",
			Filename:        "screen.png",
		}},
		Attachments: []FileAttachment{{
			DevicePath:    "/tmp/internkim-companion-files/screen.png",
			Filename:      "screen.png",
			ContentType:   "image/png",
			SizeBytes:     123,
			ContentBase64: "aW1hZ2U=",
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
	if len(messages) == 0 || !messagesContainImagePart(messages) {
		t.Fatalf("expected image part to be attached to LLM messages, got %+v", messages)
	}
	if !messagesContainUserImagePart(messages) {
		t.Fatalf("expected tool result image part to be sent as a user message, got %+v", messages)
	}
}

func TestPromptAssemblerCompressesLongObservationHistory(t *testing.T) {
	observations := []turnObservation{}
	for index := 0; index < 20; index++ {
		observations = append(observations, turnObservation{
			ObservationID: "obs-" + strings.Repeat("0", 2) + string(rune('a'+index)),
			Action:        "continue",
			Tool:          "browser.snapshot",
			Output:        ToolOutput{Content: `{"snapshotText":"` + strings.Repeat("very-long-page-output ", 200) + `","interactiveRefs":["@old"]}`},
		})
	}
	observations = append(observations, turnObservation{
		ObservationID: "obs-latest",
		Action:        "continue",
		Tool:          "browser.snapshot",
		Output:        ToolOutput{Content: `{"snapshotText":"latest form","interactiveRefs":["@latestSearch","@latestButton"]}`},
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
		for _, messagePart := range message.Parts {
			if messagePart.Type == "text" {
				parts = append(parts, messagePart.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func messagesContainImagePart(messages []llm.Message) bool {
	for _, message := range messages {
		for _, part := range message.Parts {
			if part.Type == "image" && part.MimeType == "image/png" && part.DataBase64 == "aW1hZ2U=" {
				return true
			}
		}
	}
	return false
}

func messagesContainUserImagePart(messages []llm.Message) bool {
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		for _, part := range message.Parts {
			if part.Type == "image" && part.MimeType == "image/png" && part.DataBase64 == "aW1hZ2U=" {
				return true
			}
		}
	}
	return false
}
