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
			Tool:          "platform.dm.send",
			Failure: &ToolFailure{
				Code:            "send_failed",
				Stage:           "message_send",
				UserSafeSummary: "Mattermost returned 503",
			},
		}},
		FailureFacts: failureReportFacts{Attempts: []failureReportAttempt{{
			ToolName:     "platform.dm.send",
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
