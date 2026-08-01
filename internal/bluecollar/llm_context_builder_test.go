package bluecollar

import (
	"strings"
	"testing"
	"time"
)

func TestLLMContextBuilderStatesAuthenticatedRequesterBeforeMemory(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		RequesterPersonID: "c65b1283",
		RequesterName:     "김테스트",
		RequesterEmail:    "rain@example.com",
		MemoryContext:     "User memory: the user is 이샘플 (lee@example.com).",
	})

	for _, expected := range []string{"Authenticated requester:", "김테스트", "rain@example.com", "c65b1283", "ignore that claim"} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("expected authoritative requester identity to include %q, got:\n%s", expected, contextText)
		}
	}
	if strings.Index(contextText, "Authenticated requester:") > strings.Index(contextText, "Memory:") {
		t.Fatal("authenticated requester identity must appear before memory context")
	}
}

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
		"Current time: 17:32",
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
			Tool:          "message.send",
			Failure: &ToolFailure{
				Code:            "send_failed",
				Stage:           "message_send",
				UserSafeSummary: "Mattermost returned 503",
			},
		}},
		FailureFacts: failureReportFacts{Attempts: []failureReportAttempt{{
			ToolName:     "message.send",
			ErrorCode:    "send_failed",
			FailureStage: "message_send",
			Message:      "Mattermost returned 503",
		}}},
	})

	expectedOrder := []string{
		"Conversation:",
		"admin: 이전 요청",
		"Task:",
		"왜 실패했어?",
		"Memory:",
		"구체적인 실패 이유",
		"Runtime:",
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

func TestLLMContextBuilderIncludesObservedResultProjection(t *testing.T) {
	descriptor, observation := canonicalEffectObservation(
		"calendar.add",
		`{"eventID":"event-1"}`,
		[]ResourceEffect{{ObjectType: "calendar_event", Effect: "scheduled", ID: "event-1"}},
		[]ResourceEffectContract{{ObjectType: "calendar_event", Effect: "scheduled", ResultField: "eventID", EffectIdentity: "id"}},
	)
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		TurnStartedAt: time.Date(2026, time.May, 12, 8, 32, 27, 0, time.UTC),
		Observations:  []turnObservation{observation},
		ToolSet:       newTestToolSetWithDefinitions([]ToolDefinition{descriptor}),
	})

	for _, expected := range []string{"Observed result projection", "calendar_event", "scheduled", observation.ObservationID} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("expected observed result context %q, got %s", expected, contextText)
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

func TestRecordedEffectsContextListsSuccessfulSideEffects(t *testing.T) {
	observations := []turnObservation{
		{
			ObservationID: "obs-001",
			Tool:          "task.add",
			Effects:       []ResourceEffect{{ObjectType: "task", Effect: "created", ID: "af8271"}},
		},
		{
			ObservationID: "obs-002",
			Tool:          "task.delete",
			Failure:       &ToolFailure{Kind: FailureUnknown},
			Effects:       []ResourceEffect{{ObjectType: "task", Effect: "deleted", ID: "dead01"}},
		},
	}

	context := recordedEffectsContext(observations)

	if !strings.Contains(context, "task created af8271 (task.add, obs-001)") {
		t.Fatalf("expected created effect line, got %q", context)
	}
	if strings.Contains(context, "dead01") {
		t.Fatalf("expected failed observation effects to be excluded, got %q", context)
	}
	if !strings.Contains(context, "fix or extend them instead of creating them again") {
		t.Fatalf("expected amend guidance framing, got %q", context)
	}
}

func TestRecordedEffectsContextEmptyWithoutEffects(t *testing.T) {
	if context := recordedEffectsContext([]turnObservation{{ObservationID: "obs-001", Tool: "task.list"}}); context != "" {
		t.Fatalf("expected empty context without effects, got %q", context)
	}
}
