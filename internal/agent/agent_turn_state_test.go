package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

func TestBuildAgentActionRequestPreservesNativeToolCallingWireShape(t *testing.T) {
	seed := int64(77)
	temperature := 0.4
	toolSet := NewToolSet([]string{"site.app.publish"})
	toolSet.RegisterTool(ToolDefinition{
		Name:        "site.app.publish",
		Description: "Publish a site.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"required":["siteID"],"additionalProperties":false}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("published"), nil
	})
	state := agentTaskState{
		Request: AgentTurnRequest{
			RequesterPersonID: "person-1",
			ConversationID:    "conversation-1",
			Prompt:            "publish it",
			VisibleContext: VisibleContext{Messages: []VisibleContextMessage{{
				Speaker: "Lee",
				Text:    "Please publish the site.",
			}}},
			ToolSet: toolSet,
		},
		Options: TurnOptions{GenerationOptions: llm.GenerationOptions{
			Seed:        &seed,
			Temperature: &temperature,
		}},
	}

	request := BuildAgentActionRequest(state)

	if request.StructuredOutputSchema.Name != "blueclaw_agent_turn_action" {
		t.Fatalf("expected agent action schema name, got %q", request.StructuredOutputSchema.Name)
	}
	if request.GenerationOptions.Seed == nil || *request.GenerationOptions.Seed != seed {
		t.Fatalf("expected seed to be preserved, got %+v", request.GenerationOptions)
	}
	if request.GenerationOptions.Temperature == nil || *request.GenerationOptions.Temperature != temperature {
		t.Fatalf("expected temperature to be preserved, got %+v", request.GenerationOptions)
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"action":{"enum":["call_tool"]`) {
		t.Fatalf("expected call_tool action variant, got %s", request.StructuredOutputSchema.Document)
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolName":{"enum":["site.app.publish"]`) {
		t.Fatalf("expected toolName enum to be preserved, got %s", request.StructuredOutputSchema.Document)
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolInput"`) {
		t.Fatalf("expected toolInput to be preserved, got %s", request.StructuredOutputSchema.Document)
	}
	if !messagesContain(request.Messages, "Recent visible conversation context") {
		t.Fatalf("expected visible context in model messages, got %+v", request.Messages)
	}
}

func TestBuildAgentActionRequestGenerationOptionsDoNotChangeSchema(t *testing.T) {
	seed := int64(88)
	temperature := 0.5
	toolSet := NewToolSet([]string{"browser.open"})
	toolSet.RegisterTool(ToolDefinition{Name: "browser.open"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("opened"), nil
	})
	state := agentTaskState{
		Request: AgentTurnRequest{
			Prompt:  "open browser",
			ToolSet: toolSet,
		},
	}
	seededState := state
	seededState.Options.GenerationOptions = llm.GenerationOptions{Seed: &seed, Temperature: &temperature}

	request := BuildAgentActionRequest(state)
	seededRequest := BuildAgentActionRequest(seededState)

	if request.StructuredOutputSchema.Document != seededRequest.StructuredOutputSchema.Document {
		t.Fatalf("expected generation options not to change schema document\nwithout=%s\nwith=%s", request.StructuredOutputSchema.Document, seededRequest.StructuredOutputSchema.Document)
	}
	if request.StructuredOutputSchema.Name != seededRequest.StructuredOutputSchema.Name {
		t.Fatalf("expected generation options not to change schema name")
	}
}

func TestBuildAgentActionRequestIncludesApprovalUserFacingContract(t *testing.T) {
	toolSet := NewToolSet([]string{"approval.request"})
	toolSet.RegisterTool(ToolDefinition{
		Name:        "approval.request",
		Description: "Ask for approval.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"userFacingMessage":{"type":"string"},"reasonCode":{"type":"string","enum":["external_send","destructive_action","credential_access","paid_action","permission_change","capability_unlock","other_sensitive_action"]},"reasonDetail":{"type":"string"}},"required":["userFacingMessage","reasonCode"],"additionalProperties":false}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("approval requested"), nil
	})
	state := agentTaskState{
		Request: AgentTurnRequest{
			Prompt:           "우경이한테 DM 보내줘",
			ResponseLanguage: ResponseLanguageKorean,
			ToolSet:          toolSet,
		},
	}

	request := BuildAgentActionRequest(state)

	if !messagesContain(request.Messages, "same language as the original user request") {
		t.Fatalf("expected approval language contract in messages, got %+v", request.Messages)
	}
	if !messagesContain(request.Messages, "우경이한테 DM 보내줘") {
		t.Fatalf("expected original user request in messages, got %+v", request.Messages)
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"reasonCode"`) || !strings.Contains(request.StructuredOutputSchema.Document, `"userFacingMessage"`) {
		t.Fatalf("expected approval schema fields, got %s", request.StructuredOutputSchema.Document)
	}
}

func TestParseAgentActionResponseNormalizesLegacyReply(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"reply":"done"}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "final_reply" || action.FinalReply != "done" {
		t.Fatalf("expected legacy reply to normalize, got %+v", action)
	}
}

func TestParseAgentActionResponseParsesToolCall(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"call_tool","toolName":"browser.open","toolInput":{"url":"https://example.com"}}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "call_tool" || action.ToolName != "browser.open" {
		t.Fatalf("expected tool call action, got %+v", action)
	}
	if string(action.ToolInput) != `{"url":"https://example.com"}` {
		t.Fatalf("expected tool input to be preserved, got %s", string(action.ToolInput))
	}
}

func TestParseAgentActionResponseRejectsMalformedJSON(t *testing.T) {
	_, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":`})
	if errorValue == nil {
		t.Fatal("expected malformed JSON error")
	}
}

func TestApplyToolResultAppendsObservationDeterministically(t *testing.T) {
	state := agentTaskState{}
	result := ToolResult{
		Output: ToolOutput{Content: "attached"},
		Attachments: []FileAttachment{{
			DevicePath:  "/tmp/file.html",
			Filename:    "file.html",
			ContentType: "text/html",
		}},
	}

	nextState := applyToolResult(state, ToolInvocation{ToolName: "file.attach", Input: json.RawMessage(`{"path":"file.html"}`)}, result)

	if len(nextState.Observations) != 1 {
		t.Fatalf("expected one observation, got %+v", nextState.Observations)
	}
	observation := nextState.Observations[0]
	if observation.ObservationID != "obs-001" || observation.Tool != "file.attach" || observation.ContentText() != "attached" {
		t.Fatalf("unexpected observation: %+v", observation)
	}
	if len(nextState.Attachments) != 1 || nextState.Attachments[0].Filename != "file.html" {
		t.Fatalf("expected attachment to be appended, got %+v", nextState.Attachments)
	}
}

func TestAdvanceAgentTaskReturnsModelCallEffectByDefault(t *testing.T) {
	state := buildInitialAgentTaskState(AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "hello",
	}, TurnOptions{}, "task-1")

	transition := advanceAgentTask(state)

	if transition.Effect.Kind != agentEffectCallModel {
		t.Fatalf("expected model call effect, got %+v", transition.Effect)
	}
	if transition.Effect.ModelCall == nil {
		t.Fatal("expected model call request")
	}
}

func TestAdvanceAgentTaskReturnsAttachExistingArtifactEffect(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactPath := filepath.Join(workspaceRootPath, "report.html")
	if errorValue := os.WriteFile(artifactPath, []byte("<html></html>"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolSet := NewToolSet([]string{"file.attach"})
	toolSet.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("attached"), nil
	})
	state := agentTaskState{
		Request: AgentTurnRequest{
			Prompt:                     "HTML 파일 만들어줘",
			ToolSet:                    toolSet,
			WorkspaceRootPath:          workspaceRootPath,
			RequiredEvidenceTools:      []string{"file.attach"},
			RequiredAttachmentSuffixes: []string{".html"},
			TurnStartedAt:              time.Now().Add(-time.Second),
		},
		Requirements: []toolUseRequirement{{
			ToolName:           "file.attach",
			RequiresAttachment: true,
			AttachmentSuffixes: []string{".html"},
		}},
	}

	transition := advanceAgentTask(state)

	if transition.Effect.Kind != agentEffectCallTool {
		t.Fatalf("expected file attach effect, got %+v", transition.Effect)
	}
	if transition.Effect.ToolCall == nil || transition.Effect.ToolCall.ToolName != "file.attach" {
		t.Fatalf("expected file.attach tool call, got %+v", transition.Effect.ToolCall)
	}
	if !strings.Contains(string(transition.Effect.ToolCall.Input), artifactPath) {
		t.Fatalf("expected artifact path in tool input, got %s", string(transition.Effect.ToolCall.Input))
	}
}

func TestAdvanceAgentTaskReturnsFinalReplyEffectForSatisfiedBrowserOpen(t *testing.T) {
	state := agentTaskState{
		Request: AgentTurnRequest{Prompt: "open browser"},
		Requirements: []toolUseRequirement{{
			ToolName: "browser.open",
		}},
		Observations: []turnObservation{{
			ObservationID: "obs-001",
			Action:        "call_tool",
			Tool:          "browser.open",
			Output:        ToolOutput{Content: "opened"},
		}},
	}

	transition := advanceAgentTask(state)

	if transition.Effect.Kind != agentEffectFinalReply {
		t.Fatalf("expected final reply effect, got %+v", transition.Effect)
	}
	if transition.Effect.FinalReply == nil || !strings.Contains(transition.Effect.FinalReply.Reply, "완료") {
		t.Fatalf("expected completion final reply, got %+v", transition.Effect.FinalReply)
	}
}

func TestRestoreAgentTaskStateRestoresToolProgressOnly(t *testing.T) {
	events := []task.TaskEvent{{
		Name: "tool.browser.open.result",
		Body: `{"observationID":"obs-001","action":"call_tool","tool":"browser.open","content":"opened","isError":false}`,
	}}

	state, errorValue := restoreAgentTaskState(AgentTurnRequest{Prompt: "continue"}, TurnOptions{}, task.TaskRun{
		TaskRunID: "task-1",
		Status:    task.TaskStatusWaitingUserInput,
	}, events)

	if errorValue != nil {
		t.Fatalf("expected restored state: %v", errorValue)
	}
	if state.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected restored status, got %s", state.Status)
	}
	if len(state.Observations) != 1 || state.Observations[0].Tool != "browser.open" {
		t.Fatalf("expected restored observation, got %+v", state.Observations)
	}
}

func TestDecodeLegacyObservationNormalizesMemorySearchFailureCode(t *testing.T) {
	observation, errorValue := decodeTurnObservation([]byte(`{"observationID":"obs-001","action":"call_tool","tool":"memory.search","content":"memory failed","isError":true,"errorCode":"memory_search_unavailable","failureStage":"graphiti_search","message":"memory failed"}`))
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if !observation.Failed() || observation.FailureCode() != FailureCodes.Unavailable.String() {
		t.Fatalf("expected canonical memory search failure, got %+v", observation)
	}
}
