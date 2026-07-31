package agentruntime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/agent"
)

type recordingConversationHistoryProvider struct {
	historyCursor  string
	limit          int
	visibleContext agent.VisibleContext
}

func (provider *recordingConversationHistoryProvider) FetchHistory(_ context.Context, historyCursor string, limit int) (agent.VisibleContext, error) {
	provider.historyCursor = historyCursor
	provider.limit = limit
	return provider.visibleContext, nil
}

func TestConversationHistoryUsesTrustedCursorAndCanonicalProjection(t *testing.T) {
	historyProvider := &recordingConversationHistoryProvider{
		visibleContext: agent.VisibleContext{
			Messages: []agent.VisibleContextMessage{{
				Speaker:            "Requester",
				SpeakerCallingName: "Lee",
				SpeakerHandle:      "lee",
				Text:               "check the previous file again",
				SentAt:             time.Date(2026, 7, 19, 9, 30, 0, 0, time.UTC),
				Materials: []agent.VisibleContextMaterial{{
					FileHint:          "quarterly-report",
					MaterialID:        "internal-material-id",
					Platform:          "mattermost",
					MessageID:         "internal-message-id",
					Filename:          "quarterly-report.docx",
					ContentType:       "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
					SizeBytes:         2048,
					Path:              "/workspace/private/quarterly-report.docx",
					IsAvailable:       true,
					MarkdownPreview:   "Quarterly report",
					ConversionStatus:  "complete",
					ConversionMessage: "Converted",
				}},
			}},
			HasMoreBefore: true,
			HistoryCursor: "next-cursor",
		},
	}
	toolSet := conversationHistoryToolSet(historyProvider, "trusted-cursor")

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: agent.ConversationHistoryToolName,
		Input:    json.RawMessage(`{}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected conversation history success, got %+v", result)
	}
	if historyProvider.historyCursor != "trusted-cursor" || historyProvider.limit != 20 {
		t.Fatalf("expected trusted cursor and default limit, got cursor=%q limit=%d", historyProvider.historyCursor, historyProvider.limit)
	}
	if len(result.Effects) != 0 {
		t.Fatalf("expected read-only result without effects, got %+v", result.Effects)
	}

	var document map[string]any
	if errorValue := json.Unmarshal(result.Output.Data, &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	assertExactKeys(t, document, "messages", "hasMoreBefore", "historyCursor")
	messages := document["messages"].([]any)
	message := messages[0].(map[string]any)
	assertExactKeys(t, message, "speaker", "speakerCallingName", "speakerHandle", "text", "sentAt", "materials")
	materials := message["materials"].([]any)
	material := materials[0].(map[string]any)
	assertExactKeys(t, material, "fileHint", "filename", "contentType", "sizeBytes", "isAvailable", "markdownPreview", "conversionStatus", "conversionMessage")
}

func TestConversationHistoryNormalizesNilArrays(t *testing.T) {
	historyProvider := &recordingConversationHistoryProvider{
		visibleContext: agent.VisibleContext{HistoryCursor: "next-cursor"},
	}
	toolSet := conversationHistoryToolSet(historyProvider, "trusted-cursor")

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: agent.ConversationHistoryToolName,
		Input:    json.RawMessage(`{}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected empty history success, got %+v", result)
	}
	var document struct {
		Messages []any `json:"messages"`
	}
	if errorValue := json.Unmarshal(result.Output.Data, &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	if document.Messages == nil || len(document.Messages) != 0 {
		t.Fatalf("expected normalized empty messages, got %#v", document.Messages)
	}
}

func TestConversationHistoryRejectsNonCanonicalInput(t *testing.T) {
	testCases := []json.RawMessage{
		json.RawMessage(`{"direction":"before"}`),
		json.RawMessage(`{"historyCursor":"   "}`),
		json.RawMessage(`{"limit":0}`),
		json.RawMessage(`{"limit":51}`),
		json.RawMessage(`{"limit":1.5}`),
	}
	for _, input := range testCases {
		t.Run(string(input), func(t *testing.T) {
			historyProvider := &recordingConversationHistoryProvider{}
			toolSet := conversationHistoryToolSet(historyProvider, "trusted-cursor")

			result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
				ToolName: agent.ConversationHistoryToolName,
				Input:    input,
			})

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !result.Failed() || result.FailureStage() != "tool_input_schema" {
				t.Fatalf("expected strict input rejection, got %+v", result)
			}
			if historyProvider.historyCursor != "" {
				t.Fatalf("expected rejected input not to reach history provider, got %q", historyProvider.historyCursor)
			}
		})
	}
}

func TestConversationHistoryAcceptsExplicitCursorAndLimit(t *testing.T) {
	historyProvider := &recordingConversationHistoryProvider{
		visibleContext: agent.VisibleContext{HistoryCursor: "next-cursor"},
	}
	toolSet := conversationHistoryToolSet(historyProvider, "trusted-cursor")

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: agent.ConversationHistoryToolName,
		Input:    json.RawMessage(`{"historyCursor":"explicit-cursor","limit":50}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected explicit pagination input success, got %+v", result)
	}
	if historyProvider.historyCursor != "explicit-cursor" || historyProvider.limit != 50 {
		t.Fatalf("expected explicit cursor and limit, got cursor=%q limit=%d", historyProvider.historyCursor, historyProvider.limit)
	}
}

func TestConversationHistoryResultContractRejectsMalformedOutput(t *testing.T) {
	handlerToolSet := agent.NewToolSet(nil)
	handlerToolSet.RegisterTool(agent.ToolDefinition{
		Name:        agent.ConversationHistoryToolName,
		Description: "Fetch earlier visible messages.",
		InputSchema: conversationHistoryInputSchema,
	}, func(context.Context, agent.ToolInvocation) (agent.ToolResult, error) {
		document := json.RawMessage(`{"messages":null,"hasMoreBefore":false,"historyCursor":"cursor"}`)
		return agent.ToolSuccessData(string(document), document), nil
	})
	toolSet := agent.NewToolSet([]string{agent.ConversationHistoryToolName})
	if errorValue := toolSet.RegisterProvider(context.Background(), kernelToolProvider{handlerToolSet: handlerToolSet}); errorValue != nil {
		t.Fatal(errorValue)
	}

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: agent.ConversationHistoryToolName,
		Input:    json.RawMessage(`{}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "tool_result_contract" {
		t.Fatalf("expected malformed history result rejection, got %+v", result)
	}
}

func conversationHistoryToolSet(historyProvider HistoryProvider, historyCursor string) *agent.ToolSet {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{agent.ConversationHistoryToolName})
	return toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:     "default",
		HistoryCursor:   historyCursor,
		HistoryProvider: historyProvider,
	})
}

func assertExactKeys(t *testing.T, document map[string]any, expectedKeys ...string) {
	t.Helper()
	if len(document) != len(expectedKeys) {
		t.Fatalf("expected keys %v, got %v", expectedKeys, document)
	}
	for _, key := range expectedKeys {
		if _, isFound := document[key]; !isFound {
			t.Fatalf("expected key %q in %v", key, document)
		}
	}
}
