package cliharness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

func writeFakeCodexScript(t *testing.T) string {
	t.Helper()
	scriptPath := filepath.Join(t.TempDir(), "fake-codex")
	scriptBody := "#!/bin/sh\n" +
		"echo '{\"type\":\"thread.started\",\"thread_id\":\"codex-real-thread-id\"}'\n" +
		"echo '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"'\"$*\"'\"}}'\n"
	if errorValue := os.WriteFile(scriptPath, []byte(scriptBody), 0o755); errorValue != nil {
		t.Fatalf("expected the fake codex script to write: %v", errorValue)
	}
	return scriptPath
}

func TestCodexNeverResumesASessionIDItNeverMinted(t *testing.T) {
	harness, publisher := sessionTestHarness(t, CodexAgentCommand(writeFakeCodexScript(t)))

	request := agentcontract.AgentTurnRequest{RequesterPersonID: "person-1", ExistingTaskRunID: "task-run-1", Prompt: "첫 턴"}
	firstOutput := runEchoingTurn(t, harness, request)
	secondOutput := runEchoingTurn(t, harness, request)

	published := publisher.published()
	if len(published) != 2 {
		t.Fatalf("expected two published harness sessions, got %+v", published)
	}
	if published[0].IsResumable {
		t.Fatalf("expected the first codex turn to admit it has no session to resume yet, got %+v", published[0])
	}
	if !published[1].IsResumable || published[1].SessionID != "codex-real-thread-id" {
		t.Fatalf("expected the second turn to resume the thread id codex actually minted, got %+v", published[1])
	}
	if strings.Contains(firstOutput, "resume") {
		t.Fatalf("expected the first codex turn to never claim a resume argument, got %q", firstOutput)
	}
	if !strings.Contains(secondOutput, "resume codex-real-thread-id") {
		t.Fatalf("expected the second codex turn to resume the session codex minted, got %q", secondOutput)
	}
}

func TestASecondTurnOfTheSameTaskRunResumesRatherThanReclaimingTheSessionID(t *testing.T) {
	harness, _ := sessionTestHarness(t, AgentCommand{
		Path:        "/bin/echo",
		HarnessName: "test-harness",
		SessionArguments: func(sessionID string, isResuming bool) []string {
			if isResuming {
				return []string{"--resume", sessionID}
			}
			return []string{"--session-id", sessionID}
		},
	})

	request := agentcontract.AgentTurnRequest{RequesterPersonID: "person-1", ExistingTaskRunID: "task-run-1", Prompt: "첫 턴"}
	firstOutput := runEchoingTurn(t, harness, request)
	secondOutput := runEchoingTurn(t, harness, request)

	if !strings.Contains(firstOutput, "--session-id") {
		t.Fatalf("expected the first turn of a task run to claim a fresh session id, got %q", firstOutput)
	}
	if strings.Contains(secondOutput, "--session-id") || !strings.Contains(secondOutput, "--resume") {
		t.Fatalf("expected an ordinary second turn of the same task run to resume rather than reclaim the session id, got %q", secondOutput)
	}
}

func TestClaudeCodeHeadlessRunPreAllowsItsOwnMCPServer(t *testing.T) {
	harness, _ := sessionTestHarness(t, ClaudeCodeAgentCommand("/bin/echo"))

	output := runEchoingTurn(t, harness, agentcontract.AgentTurnRequest{RequesterPersonID: "person-1", ExistingTaskRunID: "task-run-1", Prompt: "hi"})

	if !strings.Contains(output, "--allowedTools mcp__"+toolCatalogServerName) {
		t.Fatalf("expected the headless claude code run to pre-allow mcp__%s tools, got %q", toolCatalogServerName, output)
	}
}

func TestAFinishedTurnIsRecordedSoItCanBeSeenAndAudited(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	harness, _ := sessionTestHarness(t, AgentCommand{
		Path:            "/bin/sh",
		HarnessName:     "test",
		PromptArguments: []string{"-c", "echo done"},
	})
	harness.taskRunStore = taskRunService

	turnResult, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "회의록 만들어줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           emptyToolSet(t),
	})

	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	if turnResult.TaskRun.TaskRunID == "" {
		t.Fatal("expected a finished turn to carry the run it was recorded as, because a turn with no run never appears in the task list and cannot be audited")
	}
	if len(taskRunService.ListTaskRun()) != 1 {
		t.Fatalf("expected exactly one recorded run, got %d", len(taskRunService.ListTaskRun()))
	}
}
