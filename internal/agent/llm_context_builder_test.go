package agent

import (
	"strings"
	"testing"
	"time"
)

func TestLLMContextBuilderIncludesRuntimeCalendarContext(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		ResponseLanguage: "ko",
		TurnStartedAt:    time.Date(2026, time.May, 12, 8, 32, 27, 0, time.UTC),
	})

	for _, expected := range []string{
		"Runtime:",
		"Response language: ko",
		"Current date: 2026-05-12",
		"Current weekday: Tuesday",
		"Current time: 2026-05-12T17:32:27+09:00",
		"Time zone: Asia/Seoul",
	} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("expected runtime context %q, got %s", expected, contextText)
		}
	}
}

func TestLLMContextBuilderFlattensConversationMemoryAndFailure(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		ResponseLanguage: "ko",
		UserPrompt:       "왜 실패했어?",
		TurnStartedAt:    time.Date(2026, time.May, 12, 8, 32, 27, 0, time.UTC),
		VisibleContext: VisibleContext{
			Messages: []VisibleContextMessage{
				{Speaker: "admin", Text: "이전 요청"},
			},
		},
		MemoryContext: "사용자는 구체적인 실패 이유를 원한다.",
		Observations: []turnObservation{{
			ObservationID: "obs-1",
			Tool:          "platform.message.send",
			Failure: &ToolFailure{
				Code:            "send_failed",
				Stage:           "message_send",
				UserSafeSummary: "Mattermost returned 503",
			},
		}},
		FailureFacts: failureReportFacts{Attempts: []failureReportAttempt{{
			ToolName:     "platform.message.send",
			ErrorCode:    "send_failed",
			FailureStage: "message_send",
			Message:      "Mattermost returned 503",
		}}},
	})

	expectedOrder := []string{
		"Runtime:",
		"Conversation:",
		"admin: 이전 요청",
		"Task:",
		"왜 실패했어?",
		"Memory:",
		"구체적인 실패 이유",
		"Progress ledger",
		"Failure:",
		"send_failed",
		"message_send",
	}
	assertContextOrder(t, contextText, expectedOrder)
}

func TestLLMContextBuilderOmitsEmptyOptionalSections(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		TurnStartedAt: time.Date(2026, time.May, 12, 8, 32, 27, 0, time.UTC),
	})

	for _, unexpected := range []string{"Conversation:", "Task:", "Memory:", "Progress:", "Failure:", "Attachments:"} {
		if strings.Contains(contextText, unexpected) {
			t.Fatalf("expected optional section %q to be omitted, got %s", unexpected, contextText)
		}
	}
}

func TestLLMContextBuilderIncludesAttachmentCatalog(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		TurnStartedAt: time.Date(2026, time.May, 12, 8, 32, 27, 0, time.UTC),
		VisibleContext: VisibleContext{
			CurrentMaterials: []VisibleContextMaterial{{
				MaterialID:  "mattermost:file-0",
				Filename:    "current.html",
				ContentType: "text/html",
				SizeBytes:   691000,
				Path:        "home/inbox/mattermost/thread-1/post-0/current.html",
				MessageID:   "post-0",
				IsAvailable: true,
			}},
			Messages: []VisibleContextMessage{{
				Speaker: "admin",
				Text:    "이 파일 봐줘",
				Materials: []VisibleContextMaterial{{
					MaterialID:  "mattermost:file-1",
					Filename:    "report.pdf",
					ContentType: "application/pdf",
					Path:        "/workspace/circles/staff/inbox/mattermost/thread-1/post-1/report.pdf",
					MessageID:   "post-1",
					IsAvailable: true,
				}},
			}},
			Materials: []VisibleContextMaterial{{
				MaterialID:  "mattermost:file-2",
				Filename:    "screen.png",
				ContentType: "image/png",
				Path:        "/workspace/circles/staff/inbox/mattermost/thread-1/post-2/screen.png",
				MessageID:   "post-2",
				IsAvailable: true,
			}},
		},
	})

	for _, expected := range []string{
		"Current attachments:",
		"materialID=mattermost:file-0",
		"path=home/inbox/mattermost/thread-1/post-0/current.html",
		"availableTools=file.preview,file.read",
		"admin attached materialID=mattermost:file-1",
		"Previous attachments:",
		"materialID=mattermost:file-2",
		"path=/workspace/circles/staff/inbox/mattermost/thread-1/post-2/screen.png",
	} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("expected attachment catalog %q, got %s", expected, contextText)
		}
	}
	for _, unexpected := range []string{
		"filename=current.html",
		"contentType=text/html",
		"sizeBytes=691000",
		"filename=screen.png",
		"contentType=image/png",
	} {
		if strings.Contains(contextText, unexpected) {
			t.Fatalf("expected normal attachment catalog to omit %q, got %s", unexpected, contextText)
		}
	}
}

func TestLLMContextBuilderIncludesUnavailableAttachmentMetadata(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		TurnStartedAt: time.Date(2026, time.May, 12, 8, 32, 27, 0, time.UTC),
		VisibleContext: VisibleContext{
			CurrentMaterials: []VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Filename:    "archive.bin",
				ContentType: "application/octet-stream",
				SizeBytes:   512,
				MessageID:   "post-1",
				ErrorCode:   "mattermost_download_failed",
			}, {
				MaterialID: "mattermost:file-2",
				MessageID:  "post-2",
			}},
		},
	})

	for _, expected := range []string{
		"Current attachments:",
		"filename=archive.bin",
		"contentType=application/octet-stream",
		"sizeBytes=512",
		"sourceMessageID=post-1",
		"available=false",
		"errorCode=mattermost_download_failed",
		"availableTools=file.preview,file.read",
		"materialID=mattermost:file-2",
		"sourceMessageID=post-2",
	} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("expected unavailable attachment metadata %q, got %s", expected, contextText)
		}
	}
}

func assertContextOrder(t *testing.T, contextText string, fragments []string) {
	t.Helper()
	searchStart := 0
	for _, fragment := range fragments {
		index := strings.Index(contextText[searchStart:], fragment)
		if index < 0 {
			t.Fatalf("expected context fragment %q, got %s", fragment, contextText)
		}
		searchStart += index + len(fragment)
	}
}
