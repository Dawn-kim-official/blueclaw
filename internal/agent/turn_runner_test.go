package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

func TestAgentTurnRunnerCallsToolsUntilFinalReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"alpha","toolInput":{"value":"one"}}`,
		`{"action":"call_tool","toolName":"beta","toolInput":{"value":"two"}}`,
		finalReplyDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"alpha", "beta"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "alpha result"}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "beta"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "beta result"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinalReply != "done" {
		t.Fatalf("expected final reply, got %q", result.FinalReply)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if len(services.taskStepService.ListTaskStep(result.TaskRun.TaskRunID)) != 3 {
		t.Fatalf("expected three task steps, got %d", len(services.taskStepService.ListTaskStep(result.TaskRun.TaskRunID)))
	}
	if len(languageModel.requests) != 3 {
		t.Fatalf("expected three model calls, got %d", len(languageModel.requests))
	}
}

func TestAgentTurnRunnerRejectsAttachmentClaimWithoutAttachmentEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finalReplyDocument("첨부된 파일들을 확인해 주세요."),
		`{"action":"fail","reason":"attachment evidence missing"}`,
	}, textResponses: []string{
		"첨부 파일을 만들거나 보냈다고 확인할 근거가 없어 여기서 멈췄어요. 파일이 필요하면 다시 시도해 주세요.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "파일 만들어서 보내줘",
	})
	if errorValue != nil {
		t.Fatalf("expected turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task after unsupported attachment claim, got %s", result.TaskRun.Status)
	}
	if !strings.Contains(result.FinalReply, "근거가 없어") {
		t.Fatalf("expected generated failure reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "claims attached files") {
		t.Fatal("expected completion gate to reject attachment claim without evidence")
	}
}

func TestAgentTurnRunnerGeneratesFailureReplyAfterStructuredModelFailure(t *testing.T) {
	languageModel := &structuredFailureTextRecoveryLanguageModel{
		reply:      "지금은 요청을 이어갈 모델 호출이 실패해서 작업을 끝내지 못했어요. 다시 시도하면 현재 제한이 풀렸는지 확인해 볼게요.",
		errorValue: errors.New("structured model failed"),
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "서울 날씨 알려줘",
	})
	if errorValue != nil {
		t.Fatalf("expected generated failure result, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if result.FinalReply != languageModel.reply {
		t.Fatalf("expected generated failure reply, got %q", result.FinalReply)
	}
	if len(languageModel.textPrompts) != 1 {
		t.Fatalf("expected one recovery text prompt, got %d", len(languageModel.textPrompts))
	}
	if !strings.Contains(languageModel.textPrompts[0], "structured model failed") {
		t.Fatalf("expected recovery prompt to include failure reason, got %q", languageModel.textPrompts[0])
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "generated") {
		t.Fatal("expected generated failure reply event")
	}
}

func TestAgentTurnRunnerUsesDynamicReplyWhenAllModelCallsFail(t *testing.T) {
	languageModel := failingRecoveryLanguageModel{errorValue: errors.New("model unavailable")}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "서울 날씨 알려줘",
	})
	if errorValue != nil {
		t.Fatalf("expected dynamic failure result, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if result.FinalReply == "" {
		t.Fatal("expected dynamic failure reply")
	}
	if strings.Contains(result.FinalReply, "I am having trouble reaching the language model") || strings.Contains(result.FinalReply, "model configuration") {
		t.Fatalf("expected non-static dynamic reply, got %q", result.FinalReply)
	}
	if !strings.Contains(result.FinalReply, "모델 호출") {
		t.Fatalf("expected natural model failure reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "dynamic") {
		t.Fatal("expected dynamic failure reply event")
	}
}

func TestAgentTurnRunnerUsesNaturalCaptchaFailureReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.snapshot","toolInput":{}}`,
		`{"action":"fail","reason":"blocked_by_captcha"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"browser.snapshot"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.snapshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "blocked_by_captcha: bot-detection wall", IsError: true}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		RequesterCallingName:  "샘플",
		ConversationID:        "conversation-1",
		Prompt:                "내일 서울 날씨 검색해줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"browser.snapshot"},
	})
	if errorValue != nil {
		t.Fatalf("expected dynamic captcha result, got error: %v", errorValue)
	}
	if !strings.Contains(result.FinalReply, "샘플 님") || !strings.Contains(result.FinalReply, "자동화 접근을 막아서") {
		t.Fatalf("expected natural captcha reply, got %q", result.FinalReply)
	}
	if strings.Contains(result.FinalReply, "처리할 수 없습니다") || strings.Contains(result.FinalReply, "오류가 발생했습니다") {
		t.Fatalf("expected non-mechanical captcha reply, got %q", result.FinalReply)
	}
}

func TestAgentTurnRunnerRejectsHtmlClaimBackedByMarkdownAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"file.attach","toolInput":{"path":"DESIGN.md"}}`,
		finalReplyWithEvidence("HTML 파일을 전달해 드립니다.", "obs-001", "file.attach", 0),
		`{"action":"fail","reason":"html attachment missing"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Content: "file attached",
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/DESIGN.md",
				Filename:   "DESIGN.md",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "html만 주면 돼",
		ToolSet:                    toolRegistry,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".html"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task after mismatched attachment claim, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", ".html") {
		t.Fatal("expected completion gate to reject missing html attachment")
	}
}

func TestAgentTurnRunnerAcceptsHtmlRequestWithHtmlAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"file.attach","toolInput":{"path":"deck.html"}}`,
		finalReplyWithEvidence("HTML 파일을 전달해 드립니다.", "obs-001", "file.attach", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Content: "file attached",
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/deck.html",
				Filename:   "deck.html",
				SizeBytes:  12,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "html만 주면 돼",
		ToolSet:                    toolRegistry,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".html"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.html" {
		t.Fatalf("expected html attachment, got %+v", result.Attachments)
	}
}

func TestAgentTurnRunnerInjectsInstructionPrompt(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finalReplyDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})

	_, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		InstructionPrompt: "Use agent-browser for web automation.",
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if !messagesContain(languageModel.requests[0].Messages, "Use agent-browser for web automation.") {
		t.Fatal("expected instruction prompt to be injected")
	}
}

func TestAgentTurnRunnerRecordsDeniedToolAsObservation(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"forbidden","toolInput":{}}`,
		finalReplyDocument("recovered"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"allowed"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "forbidden"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "should not run"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover: %v", errorValue)
	}
	if result.FinalReply != "recovered" {
		t.Fatalf("expected recovered reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.forbidden.result", "not allowed") {
		t.Fatal("expected denied tool event")
	}
}

func TestAgentTurnRunnerRecordsToolRequestedEvent(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"alpha","toolInput":{"value":"one"}}`,
		finalReplyDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"alpha"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "alpha result"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.alpha.requested", `"value":"one"`) {
		t.Fatal("expected requested tool event with input summary")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.alpha.result", "alpha result") {
		t.Fatal("expected result tool event")
	}
}

func TestAgentTurnRunnerRequiresToolEvidenceBeforeFinalReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finalReplyDocument("browser tool is unavailable"),
		`{"action":"call_tool","toolName":"memory.search","toolInput":{}}`,
		finalReplyDocument("still no screenshot"),
		`{"action":"call_tool","toolName":"browser.screenshot","toolInput":{}}`,
		finalReplyWithEvidence("observed", "obs-004", "browser.screenshot", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"browser.screenshot", "memory.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "memory.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `[]`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.screenshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Content: `{"devicePath":"/tmp/internkim-companion-files/screenshot.png"}`,
			Attachments: []FileAttachment{{
				DevicePath: "/tmp/internkim-companion-files/screenshot.png",
				Filename:   "screenshot.png",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글 서치바에 hello world라고 치고 스크린샷",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected browser tool requirement to recover: %v", errorValue)
	}
	if result.FinalReply != "observed" {
		t.Fatalf("expected final reply after tool use, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "browser.screenshot") {
		t.Fatal("expected completion requirement event")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.memory.search.result", "[]") {
		t.Fatal("expected memory search observation before screenshot")
	}
	if len(result.Attachments) != 1 || result.Attachments[0].DevicePath != "/tmp/internkim-companion-files/screenshot.png" {
		t.Fatalf("expected screenshot attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.browser.screenshot.result", "/tmp/internkim-companion-files/screenshot.png") {
		t.Fatal("expected browser screenshot observation")
	}
}

func TestAgentTurnRunnerRequiresSelectedSkillEvidenceBeforeFinalReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finalReplyDocument("PPT 못 만들어요"),
		`{"action":"call_tool","toolName":"file.attach","toolInput":{"path":"deck.pptx"}}`,
		finalReplyWithEvidence("PPTX를 첨부했습니다.", "obs-002", "file.attach", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Content: "file attached",
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/deck.pptx",
				Filename:   "deck.pptx",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "피피티 만들어줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"file.attach"},
	})
	if errorValue != nil {
		t.Fatalf("expected required evidence to recover: %v", errorValue)
	}
	if !strings.Contains(result.FinalReply, "deck.pptx") {
		t.Fatalf("expected artifact-aware reply, got %q", result.FinalReply)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected pptx attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "file.attach") {
		t.Fatal("expected completion required event for selected skill evidence")
	}
}

func TestAgentTurnRunnerDoesNotRequireNonAttachmentToolInCompletionEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"file.write","toolInput":{"path":"/workspace/.blueclaw/tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"call_tool","toolName":"file.attach","toolInput":{"path":"deck.html"}}`,
		finalReplyWithEvidence("HTML 파일을 첨부했습니다: deck.html", "obs-002", "file.attach", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"file.write", "file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"path":"/workspace/.blueclaw/tmp/deck/presentation.md","sizeBytes":6}`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Content: "file attached",
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/deck.html",
				Filename:   "deck.html",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "html 만들어줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"file.write", "file.attach"},
	})
	if errorValue != nil {
		t.Fatalf("expected required evidence to recover: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.html" {
		t.Fatalf("expected html attachment, got %+v", result.Attachments)
	}
}

func TestAgentTurnRunnerRequiresAttachmentSuffixEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"file.attach","toolInput":{"path":"DESIGN.md"}}`,
		finalReplyWithEvidence("첨부했습니다.", "obs-001", "file.attach", 0),
		`{"action":"call_tool","toolName":"file.attach","toolInput":{"path":"deck.pptx"}}`,
		finalReplyWithEvidence("PPTX를 첨부했습니다.", "obs-003", "file.attach", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return ToolResult{}, errorValue
		}
		return ToolResult{
			Content: "file attached",
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/" + request.Path,
				Filename:   request.Path,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected required suffix evidence to recover: %v", errorValue)
	}
	if !strings.Contains(result.FinalReply, "deck.pptx") {
		t.Fatalf("expected artifact-aware reply, got %q", result.FinalReply)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected pptx attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", ".pptx") {
		t.Fatal("expected completion required event for missing attachment suffix")
	}
}

func TestAgentTurnRunnerAcceptsReadableFileAttachObservation(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, ".blueclaw", "tmp", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "presentation.md"), "Hermes Agent 장단점 분석")
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "deck.html"), "<html><body>Hermes Agent 장단점 분석</body></html>")
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"file.attach","toolInput":{"path":"/workspace/.blueclaw/tmp/deck/deck.html"}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Content: "file attached",
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/.blueclaw/tmp/deck/deck.html",
				Filename:   "deck.html",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "html 만들어줘",
		WorkspaceRootPath:     workspaceRootPath,
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"file.attach"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed turn without runner error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("expected readable attachment to be delivered, got %+v", result.Attachments)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.artifact_attach_rejected", "deck intent manifest is missing") {
		t.Fatal("did not expect intent manifest rejection event")
	}
}

func TestAgentTurnRunnerAutoAttachesRequiredWorkspaceArtifacts(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, ".blueclaw", "tmp", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))
	writeValidPDFTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pdf"))

	languageModel := &sequenceLanguageModel{contents: []string{finalReplyDocument("unused")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var request struct {
			Paths []string `json:"paths"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return ToolResult{}, errorValue
		}
		attachments := []FileAttachment{}
		for _, path := range request.Paths {
			attachments = append(attachments, FileAttachment{
				DevicePath: path,
				Filename:   filepath.Base(path),
			})
		}
		return ToolResult{Content: "file attached", Attachments: attachments}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx", ".pdf"},
	})
	if errorValue != nil {
		t.Fatalf("expected auto attachment evidence to succeed: %v", errorValue)
	}
	if !strings.Contains(result.FinalReply, "deck.pptx") || !strings.Contains(result.FinalReply, "deck.pdf") {
		t.Fatalf("expected artifact-aware final reply, got %q", result.FinalReply)
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected two attachments, got %+v", result.Attachments)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.completion_state_transition", "attach_existing_artifacts") {
		t.Fatal("expected completion state attachment transition")
	}
	if !taskEventsContain(taskEvents, "tool.file.attach.requested", "deck.pptx") {
		t.Fatal("expected automatic file.attach request")
	}
	if len(languageModel.requests) != 0 {
		t.Fatalf("expected completion state to avoid model calls, got %d", len(languageModel.requests))
	}
}

func TestAgentTurnRunnerCompletesAfterRequiredArtifactsExist(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, ".blueclaw", "tmp", "deck")
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"terminal.run","toolInput":{"command":"build deck"}}`,
		`{"action":"call_tool","toolName":"terminal.run","toolInput":{"command":"build another deck"}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.attach"})
	terminalCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
			return ToolResult{}, errorValue
		}
		writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))
		writeValidPDFTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pdf"))
		return ToolResult{Content: `{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var request struct {
			Paths []string `json:"paths"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return ToolResult{}, errorValue
		}
		attachments := []FileAttachment{}
		for _, path := range request.Paths {
			attachments = append(attachments, FileAttachment{DevicePath: path, Filename: filepath.Base(path)})
		}
		return ToolResult{Content: "file attached", Attachments: attachments}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		WorkspaceRootPath:          workspaceRootPath,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx", ".pdf"},
	})
	if errorValue != nil {
		t.Fatalf("expected auto artifact completion: %v", errorValue)
	}
	if terminalCallCount != 1 {
		t.Fatalf("expected one build command before auto completion, got %d", terminalCallCount)
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected two attachments, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_state_finalized", "attachmentCount") {
		t.Fatal("expected completion state finalization event")
	}
}

func TestAgentTurnRunnerDoesNotRepeatFailedAutomaticAttachment(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, ".blueclaw", "tmp", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))

	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"fail","reason":"attachment unavailable"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	attachmentCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		attachmentCallCount++
		return ToolResult{Content: "attachment unavailable", IsError: true}, nil
	})

	_, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected failed turn to return result without runner error: %v", errorValue)
	}
	if attachmentCallCount != 1 {
		t.Fatalf("expected one automatic attachment attempt, got %d", attachmentCallCount)
	}
}

func TestAgentTurnRunnerAttachesReadableImperfectArtifactCandidate(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, ".blueclaw", "tmp", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"), "not a valid pptx")

	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"fail","reason":"artifact invalid"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	attachmentCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		attachmentCallCount++
		return ToolResult{
			Content: "file attached",
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/.blueclaw/tmp/deck/deck.pptx",
				Filename:   "deck.pptx",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected invalid artifact turn to return result without runner error: %v", errorValue)
	}
	if attachmentCallCount == 0 {
		t.Fatal("expected readable imperfect artifact to be attached")
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("expected imperfect artifact attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.validity_review", `"passed":true`) {
		t.Fatal("expected passing basic validity review event")
	}
}

func TestAgentTurnRunnerAutoCompletionKeepsQualityOutOfCorePolicy(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, ".blueclaw", "tmp", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))
	writeValidPDFTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pdf"))

	languageModel := &sequenceLanguageModel{contents: []string{finalReplyDocument("unused")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var request struct {
			Paths []string `json:"paths"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return ToolResult{}, errorValue
		}
		attachments := []FileAttachment{}
		for _, path := range request.Paths {
			attachments = append(attachments, FileAttachment{DevicePath: path, Filename: filepath.Base(path)})
		}
		return ToolResult{Content: "file attached", Attachments: attachments}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx", ".pdf"},
	})
	if errorValue != nil {
		t.Fatalf("expected artifact validity completion without core quality checks: %v", errorValue)
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected attachments, got %+v", result.Attachments)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.quality_review", "marp_build_log_success") {
		t.Fatal("expected no hard-coded slide quality check event")
	}
}

func TestAgentTurnRunnerAuditsSelectedSkillDecisions(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finalReplyDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:  "person-1",
		ConversationID:     "conversation-1",
		Prompt:             "피피티 만들어줘",
		InstructionPrompt:  "Available skill index.\n\nSelected skill instructions:\nGenerate PPTX with Marp.",
		InstructionSources: []InstructionSource{{Path: "skills/simple-slides/SKILL.md", SkillName: "simple-slides", SHA256: "abc"}},
		SkillDecisions: []SkillSelectionDecision{{
			Name:   "simple-slides",
			Status: "selected",
			Reason: "embedding_similarity",
			Source: InstructionSource{Path: "skills/simple-slides/SKILL.md", SkillName: "simple-slides", SHA256: "abc"},
		}},
	})
	if errorValue != nil {
		t.Fatalf("expected turn result: %v", errorValue)
	}
	if result.FinalReply != "done" {
		t.Fatalf("expected final reply, got %q", result.FinalReply)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "simple-slides") {
		t.Fatal("expected selected skill in instructions event")
	}
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "embedding_similarity") {
		t.Fatal("expected selected skill reason in instructions event")
	}
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "skills/simple-slides/SKILL.md") {
		t.Fatal("expected selected skill source in instructions event")
	}
}

func TestAgentTurnRunnerRejectsUnsatisfiedFinalReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"final_reply","goalStatus":"in_progress","goalSatisfied":false,"completionEvidence":[],"finalReply":"done"}`,
		finalReplyDocument("now done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "say hello",
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover: %v", errorValue)
	}
	if result.FinalReply != "now done" {
		t.Fatalf("expected recovered final reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "goalSatisfied=true") {
		t.Fatal("expected goalSatisfied completion gate event")
	}
}

func TestAgentTurnRunnerRejectsCompletionEvidenceFromErrorObservation(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"unstable","toolInput":{}}`,
		finalReplyWithEvidence("done", "obs-001", "unstable", 0),
		`{"action":"fail","reason":"tool failed"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"unstable"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "unstable"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "failed", IsError: true}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to fail safely: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "unknown or failed observation") {
		t.Fatal("expected failed evidence gate event")
	}
}

func TestAgentTurnRunnerTreatsToolFailureAsObservation(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"unstable","toolInput":{}}`,
		finalReplyDocument("handled failure"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"unstable"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "unstable"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{}, errors.New("tool failed")
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover: %v", errorValue)
	}
	if result.FinalReply != "handled failure" {
		t.Fatalf("expected final reply after failure, got %q", result.FinalReply)
	}
}

func TestAgentTurnRunnerRejectsEmptyBrowserPressAfterFill(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.fill","toolInput":{"target":"@e5","text":"hello world"}}`,
		`{"action":"call_tool","toolName":"browser.press","toolInput":{}}`,
		finalReplyWithEvidence("searched", "obs-001", "browser.fill", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	pressCallCount := 0
	toolRegistry := newTestToolSet([]string{"browser.fill", "browser.press"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"ok":true}`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.press"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		pressCallCount++
		return ToolResult{Content: `{"ok":true}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "입력칸에 hello world라고 입력해줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinalReply != "searched" {
		t.Fatalf("expected searched reply, got %q", result.FinalReply)
	}
	if pressCallCount != 0 {
		t.Fatalf("expected malformed press input not to invoke tool, got %d calls", pressCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_malformed", "browser.press") {
		t.Fatal("expected malformed browser press event")
	}
}

func TestAgentTurnRunnerRejectsBrowserFillWithoutRequiredInput(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.snapshot","toolInput":{}}`,
		`{"action":"call_tool","toolName":"browser.fill","toolInput":{}}`,
		finalReplyWithEvidence("filled", "obs-001", "browser.snapshot", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	fillCallCount := 0
	toolRegistry := newTestToolSet([]string{"browser.snapshot", "browser.fill"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.snapshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"snapshotText":"- textbox \"Google 검색\" [ref=e5]"}`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		fillCallCount++
		return ToolResult{Content: `{"ok":true}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "입력칸에 hello world라고 입력해줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinalReply != "filled" {
		t.Fatalf("expected filled reply, got %q", result.FinalReply)
	}
	if fillCallCount != 0 {
		t.Fatalf("expected malformed fill input not to invoke tool, got %d calls", fillCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_malformed", "target/ref/selector, text") {
		t.Fatal("expected malformed browser fill event")
	}
}

func TestAgentTurnRunnerRejectsEmptyGoogleNavigate(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.open","toolInput":{}}`,
		`{"action":"call_tool","toolName":"browser.open","toolInput":{"url":"https://www.google.com"}}`,
		finalReplyWithEvidence("opened", "obs-002", "browser.open", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	navigateCallCount := 0
	toolRegistry := newTestToolSet([]string{"browser.open"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.open"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		navigateCallCount++
		return ToolResult{Content: `{"url":"https://www.google.com"}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글 서치바에 hello world라고 치고 스크린샷",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinalReply != "opened" {
		t.Fatalf("expected opened reply, got %q", result.FinalReply)
	}
	if navigateCallCount != 1 {
		t.Fatalf("expected only valid navigate input to invoke tool, got %d calls", navigateCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_malformed", "url") {
		t.Fatal("expected malformed browser navigate event")
	}
}

func TestAgentTurnRunnerAutoCompletesSimpleBrowserOpen(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.open","toolInput":{"url":"https://www.google.com"}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"browser.open"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.open"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"url":"https://www.google.com/"}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "브라우저 열어줘.",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if !strings.Contains(result.FinalReply, "완료") && !strings.Contains(result.FinalReply, "열") {
		t.Fatalf("expected browser-open completion reply, got %q", result.FinalReply)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected no extra model calls after browser.open, got %d", len(languageModel.requests))
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_state_finalized", "evidenceCount") {
		t.Fatal("expected completion state finalization event")
	}
}

func TestAgentTurnRunnerRejectsBrowserFollowUpReplyWithoutToolEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finalReplyDocument("말로만 답변"),
		`{"action":"call_tool","toolName":"browser.open","toolInput":{"url":"https://console.cloud.google.com/"}}`,
		finalReplyWithEvidence("열었습니다", "obs-002", "browser.open", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"browser.open"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.open"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"url":"https://console.cloud.google.com/"}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "다시 열어봐",
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "사용자", Text: "구글 클라우드 콘솔에서 credential.json 받는 거 도와줘"},
			{Speaker: "김인턴", Text: "Companion 브라우저 연결이 필요합니다."},
		}},
		ToolSet: toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinalReply != "열었습니다" {
		t.Fatalf("expected browser-backed reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "browser.") {
		t.Fatal("expected browser follow-up completion gate to reject tool-free reply")
	}
}

func TestBrowserActionSchemaUsesProviderCompatibleObjectInputs(t *testing.T) {
	runner := NewAgentTurnRunner(nil, nil, nil, nil, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.click", "browser.fill", "browser.select", "browser.wait"})
	for _, toolName := range []string{"browser.open", "browser.click", "browser.fill", "browser.select", "browser.wait"} {
		toolRegistry.RegisterTool(ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolResult{}, nil
		})
	}
	schemaDocument := runner.buildActionSchema(toolRegistry, true, nil)

	if strings.Contains(schemaDocument, "anyOf") {
		t.Fatalf("expected browser action schema to avoid anyOf, got %s", schemaDocument)
	}
	if strings.Contains(schemaDocument, `"toolInput":{"oneOf"`) {
		t.Fatalf("expected browser tool inputs to avoid oneOf unions, got %s", schemaDocument)
	}
	if strings.Contains(schemaDocument, `{"type":"string","minLength":1}`) {
		t.Fatalf("expected browser tool inputs to avoid string shortcut branches, got %s", schemaDocument)
	}
	for _, fragment := range []string{
		`"toolName":{"enum":["browser.open"],"type":"string"}`,
		`"required":["url"]`,
		`"required":["text"]`,
		`"required":["value"]`,
		`"properties":{"milliseconds":{"type":"number"},"ref":{"type":"string"},"selector":{"type":"string"},"target":{"type":"string"}}`,
	} {
		if !strings.Contains(schemaDocument, fragment) {
			t.Fatalf("expected action schema to include %q, got %s", fragment, schemaDocument)
		}
	}
}

func TestAgentTurnRunnerRemovesQualityCriteriaActionAfterCriteriaAreSet(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"set_quality_criteria","qualityCriteria":[{"id":"done-once","description":"criteria are declared","required":true}],"goalStatus":"in_progress","goalSatisfied":false}`,
		`{"action":"call_tool","toolName":"alpha","toolInput":{}}`,
		`{"action":"final_reply","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"alpha"}],"qualityReview":[{"id":"done-once","passed":true,"evidence":[{"observationID":"obs-002","toolName":"alpha"}]}],"finalReply":"done"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"alpha"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "alpha result"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:         "person-1",
		ConversationID:            "conversation-1",
		Prompt:                    "make an artifact",
		ToolSet:                   toolRegistry,
		QualityAcceptanceGuidance: []string{"declare criteria first"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinalReply != "done" {
		t.Fatalf("expected final reply, got %q", result.FinalReply)
	}
	if len(languageModel.requests) < 2 {
		t.Fatalf("expected at least two model requests, got %d", len(languageModel.requests))
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, "set_quality_criteria") {
		t.Fatalf("expected initial schema to allow quality criteria, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if strings.Contains(languageModel.requests[1].StructuredOutputSchema.Document, "set_quality_criteria") {
		t.Fatalf("expected next schema to remove quality criteria, got %s", languageModel.requests[1].StructuredOutputSchema.Document)
	}
}

func TestAgentTurnRunnerStopsRepeatedMalformedToolInputByLimit(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.fill","toolInput":{}}`,
		`{"action":"call_tool","toolName":"browser.fill","toolInput":{}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	fillCallCount := 0
	toolRegistry := newTestToolSet([]string{"browser.fill"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		fillCallCount++
		return ToolResult{Content: `{"ok":true}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "fill the search box",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if result.FinalReply == "" {
		t.Fatal("expected dynamic limit reply")
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task, got %s", result.TaskRun.Status)
	}
	if !strings.Contains(result.FinalReply, "could not finish") && !strings.Contains(result.FinalReply, "try again") {
		t.Fatalf("expected natural dynamic limit reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_reply", "dynamic") {
		t.Fatal("expected dynamic limit reply event")
	}
	if fillCallCount != 0 {
		t.Fatalf("expected malformed fill input not to invoke tool, got %d calls", fillCallCount)
	}
}

func TestAgentTurnRunnerDoesNotChargeMalformedInputToToolEffort(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.fill","toolInput":{}}`,
		`{"action":"call_tool","toolName":"alpha","toolInput":{}}`,
		`{"action":"call_tool","toolName":"beta","toolInput":{}}`,
		finalReplyDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 2})
	toolRegistry := newTestToolSet([]string{"browser.fill", "alpha", "beta"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"ok":true}`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "alpha result"}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "beta"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "beta result"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinalReply != "done" {
		t.Fatalf("expected final reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_malformed", "browser.fill") {
		t.Fatal("expected malformed tool event")
	}
}

func TestAgentTurnRunnerRejectsRepeatedSuccessfulToolCall(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"terminal.run","toolInput":{"command":"marp --version"}}`,
		`{"action":"call_tool","toolName":"terminal.run","toolInput":{"command":"marp --version"}}`,
		finalReplyDocument("명령 실행은 완료됐습니다.\n\n@marp-team/marp-cli v4.3.1"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolResult{Content: `{"exitCode":0,"stdout":"@marp-team/marp-cli v4.3.1\n","stderr":"","timedOut":false}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "marp 버전 확인해줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected duplicate completion: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected duplicate tool call not to execute, got %d calls", toolCallCount)
	}
	if !strings.Contains(result.FinalReply, "@marp-team/marp-cli v4.3.1") {
		t.Fatalf("expected final reply from successful observation, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.duplicate_tool_call_rejected", "obs-001") {
		t.Fatal("expected duplicate rejection event")
	}
}

func TestAgentTurnRunnerRejectsUnnecessarySitePublishApproval(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"approval.request","toolInput":{"message":"배포는 외부 영향이 있는 작업이므로 확인이 필요합니다."}}`,
		`{"action":"call_tool","toolName":"site.app.publish","toolInput":{"siteID":"site-1","message":"Publish prototype"}}`,
		finalReplyWithEvidence("배포했습니다.", "obs-002", "site.app.publish", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5, MaxToolCallCount: 4})
	publishCallCount := 0
	toolRegistry := newTestToolSet([]string{"approval.request", "terminal.run", "site.app.create", "site.app.publish"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.publish"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		publishCallCount++
		return ToolResult{Content: `{"siteID":"site-1","status":"published","publishedURL":"https://demo.example"}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "웹사이트 만들어서 배포해",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"site.app.publish"},
		WorkspaceRootPath:     t.TempDir(),
	})
	if errorValue != nil {
		t.Fatalf("expected site publish to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if publishCallCount != 1 {
		t.Fatalf("expected site.app.publish to run once, got %d", publishCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "approval.requested", "") {
		t.Fatal("unexpected waiting approval request")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.approval_request_rejected", "site.app.publish") {
		t.Fatal("expected unnecessary approval rejection event")
	}
}

func TestAgentTurnRunnerFinalizesOneShotEvidenceToolAfterSuccess(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"calendar.event.add","toolInput":{"title":"휴가","startISO":"2026-05-10T00:00:00+09:00","endISO":"2026-05-13T00:00:00+09:00","timeZone":"Asia/Seoul","isAllDay":true}}`,
		`{"action":"call_tool","toolName":"calendar.event.add","toolInput":{"title":"휴가","startISO":"2026-05-11T00:00:00+09:00","endISO":"2026-05-14T00:00:00+09:00","timeZone":"Asia/Seoul","isAllDay":true}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestToolSet([]string{"calendar.event.add"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "calendar.event.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolResult{Content: `{"id":"event-1","title":"휴가","startISO":"2026-05-10T00:00:00+09:00","endISO":"2026-05-13T00:00:00+09:00","timeZone":"Asia/Seoul","isAllDay":true}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "내일부터 화요일까지 휴가 등록해줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"calendar.event.add"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed calendar turn: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected one calendar write, got %d", toolCallCount)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected no second model action after evidence success, got %d requests", len(languageModel.requests))
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_state_finalized", "calendar.event.add") {
		t.Fatal("expected completion state finalization with calendar evidence")
	}
}

func TestAgentTurnRunnerDoesNotBlockTerminalRerunForMissingFile(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"call_tool","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"call_tool","toolName":"file.write","toolInput":{"path":"/workspace/.blueclaw/tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"call_tool","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		finalReplyDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 6})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		if terminalCallCount == 1 {
			return ToolResult{Content: `{"exitCode":1,"stdout":"","stderr":"Error: presentation.md not found. Create presentation.md or set SRC=yourfile.md\n","timedOut":false}`, IsError: true}, nil
		}
		return ToolResult{Content: `{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return ToolResult{Content: errorValue.Error(), IsError: true}, nil
		}
		return ToolResult{Content: `{"path":"` + input.Path + `","sizeBytes":5}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "build deck",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 2 {
		t.Fatalf("expected terminal rerun to remain available, got %d terminal calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "presentation.md") {
		t.Fatal("did not expect terminal precondition block event")
	}
}

func TestAgentTurnRunnerDoesNotBlockTerminalRerunForMissingDesignFile(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"call_tool","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"call_tool","toolName":"file.write","toolInput":{"path":"/workspace/.blueclaw/tmp/deck/DESIGN.md","content":"colors: blue"}}`,
		`{"action":"call_tool","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		finalReplyDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 6})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		if terminalCallCount == 1 {
			return ToolResult{Content: `{"exitCode":1,"stdout":"","stderr":"DESIGN.md is missing colors:\n","timedOut":false}`, IsError: true}, nil
		}
		return ToolResult{Content: `{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return ToolResult{Content: errorValue.Error(), IsError: true}, nil
		}
		return ToolResult{Content: `{"path":"` + input.Path + `","sizeBytes":12}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "build deck",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 2 {
		t.Fatalf("expected terminal rerun to remain available, got %d terminal calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "DESIGN.md") {
		t.Fatal("did not expect DESIGN.md precondition block event")
	}
}

func TestAgentTurnRunnerDoesNotBlockTerminalBeforeRequiredFileWrite(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"call_tool","toolName":"file.write","toolInput":{"path":"/workspace/.blueclaw/tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"call_tool","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"final_reply","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"file.write"}],"finalReply":"done"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5, MaxToolCallCount: 5})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		return ToolResult{Content: `{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return ToolResult{Content: errorValue.Error(), IsError: true}, nil
		}
		return ToolResult{Content: `{"path":"` + input.Path + `","sizeBytes":5}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "build deck",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"file.write"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 1 {
		t.Fatalf("expected terminal call to remain available, got %d calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "first required workspace file") {
		t.Fatal("did not expect required file.write precondition block event")
	}
}

func TestAgentTurnRunnerUsesContextualLimitReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"call_tool","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{"검색은 시작했지만 결과 정리는 아직 남았습니다. 지금 확인된 내용은 다시 이어서 처리할 수 있게 저장했습니다."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "again"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글에서 검색해줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if strings.Contains(result.FinalReply, "예산") || strings.Contains(result.FinalReply, "budget") {
		t.Fatalf("expected reply without budget wording, got %q", result.FinalReply)
	}
	if !strings.Contains(result.FinalReply, "남았습니다") {
		t.Fatalf("expected contextual limit reply, got %q", result.FinalReply)
	}
}

func TestAgentTurnRunnerRegeneratesLimitReplyWhenItClaimsAttachments(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"call_tool","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{
			"요청하신 HTML 파일을 생성해 첨부했습니다.",
			"작업은 시작했지만 HTML 파일을 완성하기 전에 실행 한계에 걸렸습니다. 지금까지의 작업 상태는 저장되어 다시 이어서 시도할 수 있습니다.",
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Content: "started",
			Attachments: []FileAttachment{{
				Filename:   "deck.html",
				DevicePath: "/tmp/deck.html",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "html 파일 만들어줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if strings.Contains(result.FinalReply, "첨부") {
		t.Fatalf("expected generated reply without attachment claim, got %q", result.FinalReply)
	}
	if !strings.Contains(result.FinalReply, "저장") {
		t.Fatalf("expected regenerated contextual reply, got %q", result.FinalReply)
	}
	if len(languageModel.textPrompts) != 2 {
		t.Fatalf("expected repair generation prompt, got %d prompts", len(languageModel.textPrompts))
	}
	if strings.Contains(languageModel.textPrompts[0], "deck.html") {
		t.Fatalf("expected blocked limit reply prompt to omit undeliverable attachments, got %s", languageModel.textPrompts[0])
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("expected blocked task to deliver no attachments, got %+v", result.Attachments)
	}
}

func TestAgentTurnRunnerRegeneratesLimitReplyWhenItMentionsUnattachedFilename(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"call_tool","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{
			"아래 파일을 확인해 주세요.\n[Hermes_Agent_Slide_Part1.html]",
			"작업은 시작했지만 HTML 파일을 완성하기 전에 중단되었습니다. 다시 시도할 수 있게 상태를 저장했습니다.",
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "started"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "html 파일 만들어줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if strings.Contains(result.FinalReply, "Hermes_Agent_Slide_Part1.html") {
		t.Fatalf("expected generated reply without unattached filename, got %q", result.FinalReply)
	}
	if len(languageModel.textPrompts) != 2 {
		t.Fatalf("expected repair generation prompt, got %d prompts", len(languageModel.textPrompts))
	}
}

func TestAgentTurnRunnerUsesDynamicLimitReplyWhenFinalizationLeaksDiagnostics(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"call_tool","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{
			"I used 10 minutes and 7 iterations before the budget stopped.",
			"I still used 10 minutes and 7 iterations before the budget stopped.",
			"I still used 10 minutes and 7 iterations before the budget stopped.",
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "again"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if result.FinalReply == "" {
		t.Fatal("expected dynamic fallback reply")
	}
	if strings.Contains(result.FinalReply, "budget") || strings.Contains(result.FinalReply, "iterations") || strings.Contains(result.FinalReply, "minutes") {
		t.Fatalf("expected natural dynamic limit reply without diagnostics, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_reply", "dynamic") {
		t.Fatal("expected dynamic limit reply event")
	}
}

func TestAgentTurnRunnerAddsModelFacingLimitPressureWarnings(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"unknown"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 10, MaxToolCallCount: 8})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_pressure", "consolidate") {
		t.Fatal("expected consolidate pressure warning event")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_pressure", "finalize") {
		t.Fatal("expected finalize pressure warning event")
	}
	if !structuredRequestsContain(languageModel.requests, "Consolidate completed work") {
		t.Fatal("expected consolidate warning in model-facing messages")
	}
	if !structuredRequestsContain(languageModel.requests, "Do not start new tool work") {
		t.Fatal("expected finalize warning in model-facing messages")
	}
}

func TestAgentTurnRunnerFinalizesSatisfiedGoalAtIterationEffort(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.screenshot","toolInput":{}}`,
		`{"action":"call_tool","toolName":"browser.screenshot","toolInput":{}}`,
		finalReplyWithEvidence("캡처했습니다.", "obs-002", "browser.screenshot", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 2})
	toolRegistry := newTestToolSet([]string{"browser.screenshot"})
	screenshotIndex := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.screenshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		screenshotIndex++
		filename := fmt.Sprintf("browser-screenshot-%d.png", screenshotIndex)
		return ToolResult{
			Content: `{"devicePath":"/tmp/internkim-companion-files/` + filename + `"}`,
			Attachments: []FileAttachment{{
				DevicePath:  "/tmp/internkim-companion-files/" + filename,
				Filename:    filename,
				ContentType: "image/png",
				SizeBytes:   10,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "스크린샷 줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected attachment completion, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "browser-screenshot-2.png" {
		t.Fatalf("expected latest screenshot attachment, got %+v", result.Attachments)
	}
	if result.FinalReply != "캡처했습니다." {
		t.Fatalf("expected finalizer reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.finalizer_action", "obs-002") {
		t.Fatal("expected finalizer action with completion evidence")
	}
}

func TestAgentTurnRunnerDoesNotDeliverAttachmentsWhenFinalizerFails(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.screenshot","toolInput":{}}`,
		`{"action":"fail","reason":"not complete"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"browser.screenshot"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.screenshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Content: `{"devicePath":"/tmp/internkim-companion-files/browser-screenshot.png"}`,
			Attachments: []FileAttachment{{
				DevicePath:  "/tmp/internkim-companion-files/browser-screenshot.png",
				Filename:    "browser-screenshot.png",
				ContentType: "image/png",
				SizeBytes:   10,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "스크린샷 줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected effort result, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task, got %s", result.TaskRun.Status)
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("expected no secret attachment delivery, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.finalizer_rejected", "finalizer did not return final_reply") {
		t.Fatal("expected finalizer rejection event")
	}
}

func TestAgentTurnRunnerDoesNotCompleteEffortStopFromUnrequestedAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"file.pick","toolInput":{}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"file.pick"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.pick"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Content: `{"devicePath":"/tmp/internkim-companion-files/report.txt"}`,
			Attachments: []FileAttachment{{
				DevicePath:  "/tmp/internkim-companion-files/report.txt",
				Filename:    "report.txt",
				ContentType: "text/plain",
				SizeBytes:   10,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do some work",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected effort result, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task, got %s", result.TaskRun.Status)
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("expected no delivery attachments, got %+v", result.Attachments)
	}
}

func TestAgentTurnRunnerStoresLargeToolResultAsArtifact(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"large","toolInput":{}}`,
		finalReplyDocument("summarized"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{ToolResultMaxBytes: 8})
	toolRegistry := newTestToolSet([]string{"large"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "large"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: strings.Repeat("x", 32)}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if len(services.taskArtifactService.ListTaskArtifact(result.TaskRun.TaskRunID)) != 1 {
		t.Fatalf("expected one task artifact, got %d", len(services.taskArtifactService.ListTaskArtifact(result.TaskRun.TaskRunID)))
	}
}

func TestAgentTurnRunnerFailsWhenMaximumIterationsAreExceeded(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"call_tool","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{"작업을 시작했지만 완료 전에 멈췄습니다. 다시 시도하면 이어서 처리할 수 있어요."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "again"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected fallback result, got error: %v", errorValue)
	}
	if result.FinalReply != "작업을 시작했지만 완료 전에 멈췄습니다. 다시 시도하면 이어서 처리할 수 있어요." {
		t.Fatalf("expected generated limit reply, got %q", result.FinalReply)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task run, got %s", result.TaskRun.Status)
	}
}

func TestAgentTurnRunnerStopsWhenToolEffortIsExceeded(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"call_tool","toolName":"loop","toolInput":{}}`,
			`{"action":"call_tool","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{"도구 호출이 더 진행되기 전에 멈췄습니다. 확인된 내용까지만 바탕으로 다시 이어갈 수 있어요."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3, MaxToolCallCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "again"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if result.FinalReply != "도구 호출이 더 진행되기 전에 멈췄습니다. 확인된 내용까지만 바탕으로 다시 이어갈 수 있어요." {
		t.Fatalf("expected generated limit reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_stop", "max_tool_calls") {
		t.Fatal("expected limit stop event")
	}
}

type turnRunnerTestServices struct {
	runner              *AgentTurnRunner
	taskEventService    *task.TaskEventService
	taskStepService     *task.TaskStepService
	taskArtifactService *task.TaskArtifactService
}

func newTurnRunnerTestServices(languageModel llm.LanguageModelProvider, options TurnOptions) turnRunnerTestServices {
	taskEventService := task.NewTaskEventService()
	taskStepService := task.NewTaskStepService()
	taskArtifactService := task.NewTaskArtifactService()
	taskRunService := task.NewTaskRunService(taskEventService)
	return turnRunnerTestServices{
		runner:              NewAgentTurnRunner(taskRunService, taskStepService, taskArtifactService, languageModel, options),
		taskEventService:    taskEventService,
		taskStepService:     taskStepService,
		taskArtifactService: taskArtifactService,
	}
}

type sequenceLanguageModel struct {
	contents      []string
	textResponses []string
	requests      []llm.StructuredResponseRequest
	textPrompts   []string
}

func (languageModel *sequenceLanguageModel) GenerateResponse(_ context.Context, prompt string) (string, error) {
	languageModel.textPrompts = append(languageModel.textPrompts, prompt)
	index := len(languageModel.textPrompts) - 1
	if index >= len(languageModel.textResponses) {
		return "", nil
	}
	return languageModel.textResponses[index], nil
}

func (languageModel *sequenceLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	index := len(languageModel.requests) - 1
	if index >= len(languageModel.contents) {
		index = len(languageModel.contents) - 1
	}
	return llm.StructuredResponse{Content: languageModel.contents[index]}, nil
}

type structuredFailureTextRecoveryLanguageModel struct {
	reply       string
	errorValue  error
	textPrompts []string
}

func (languageModel *structuredFailureTextRecoveryLanguageModel) GenerateResponse(_ context.Context, prompt string) (string, error) {
	languageModel.textPrompts = append(languageModel.textPrompts, prompt)
	return languageModel.reply, nil
}

func (languageModel *structuredFailureTextRecoveryLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, languageModel.errorValue
}

type failingRecoveryLanguageModel struct {
	errorValue error
}

func (languageModel failingRecoveryLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", languageModel.errorValue
}

func (languageModel failingRecoveryLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, languageModel.errorValue
}

func taskEventsContain(taskEvents []task.TaskEvent, name string, bodyFragment string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name && strings.Contains(taskEvent.Body, bodyFragment) {
			return true
		}
	}
	return false
}

func messagesContain(messages []llm.Message, fragment string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, fragment) {
			return true
		}
	}
	return false
}

func structuredRequestsContain(requests []llm.StructuredResponseRequest, fragment string) bool {
	for _, request := range requests {
		if messagesContain(request.Messages, fragment) {
			return true
		}
	}
	return false
}

func writeAgentTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if errorValue := os.WriteFile(path, []byte(content), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func finalReplyDocument(reply string) string {
	return `{"action":"final_reply","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"finalReply":` + strconv.Quote(reply) + `}`
}

func finalReplyWithEvidence(reply string, observationID string, toolName string, attachmentIndex int) string {
	return `{"action":"final_reply","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":` + strconv.Quote(observationID) + `,"toolName":` + strconv.Quote(toolName) + `,"attachmentIndex":` + strconv.Itoa(attachmentIndex) + `}],"finalReply":` + strconv.Quote(reply) + `}`
}
