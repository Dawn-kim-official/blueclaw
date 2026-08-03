package turnstream

import (
	"context"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

type ledgerWritingHarness struct {
	taskRunStore taskstate.TaskRunStore
	writeEvents  func(taskRunID string)
	finishResult agentcontract.AgentTurnResult
}

func (harness ledgerWritingHarness) RunTurn(_ context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if harness.writeEvents != nil {
		harness.writeEvents(request.ExistingTaskRunID)
	}
	taskRun, _ := harness.taskRunStore.FindTaskRun(request.ExistingTaskRunID)
	result := harness.finishResult
	result.TaskRun = taskRun
	return result, nil
}

func streamerFixture(t *testing.T, writeEvents func(taskstate.TaskRunStore, string)) *Streamer {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	return New(ledgerWritingHarness{
		taskRunStore: taskRunService,
		writeEvents: func(taskRunID string) {
			writeEvents(taskRunService, taskRunID)
		},
		finishResult: agentcontract.AgentTurnResult{FinishMessage: "done"},
	}, taskRunService)
}

func collectEvents(stream *Stream) []Event {
	collected := []Event{}
	for event := range stream.Events {
		collected = append(collected, event)
	}
	return collected
}

func TestAnyHarnessThatWritesTheLedgerIsStreamedWithoutImplementingAStreamingPort(t *testing.T) {
	streamer := streamerFixture(t, func(taskRunStore taskstate.TaskRunStore, taskRunID string) {
		taskRunStore.AppendTaskEvent(taskRunID, "agent.checkpoint.sent", `{"message":"작업 시작합니다"}`)
		taskRunStore.AppendTaskEvent(taskRunID, "tool.file_read.result", `{"tool":"file_read"}`)
		taskRunStore.AppendTaskEvent(taskRunID, "approval.pending_call", `{"toolName":"calendar_delete","confirmation":"지울까요?"}`)
	})

	stream := streamer.StreamTurn(context.Background(), agentcontract.AgentTurnRequest{RequesterPersonID: "person-1", Prompt: "해줘"})
	collected := collectEvents(stream)

	if len(collected) != 3 {
		t.Fatalf("expected the ledger to be the stream, got %v", collected)
	}
	if collected[0].Kind != EventReply || collected[0].Message != "작업 시작합니다" {
		t.Fatalf("expected a checkpoint to reach the client as a reply, got %+v", collected[0])
	}
	if collected[1].Kind != EventTool || collected[1].ToolName != "file_read" {
		t.Fatalf("expected a tool result to name its tool, got %+v", collected[1])
	}
	if collected[2].Kind != EventApproval || collected[2].ToolName != "calendar_delete" {
		t.Fatalf("expected a held call to reach the client, got %+v", collected[2])
	}
}

func TestTheTurnResultSurvivesAConsumerThatNeverReadsProgress(t *testing.T) {
	streamer := streamerFixture(t, func(taskRunStore taskstate.TaskRunStore, taskRunID string) {
		for index := 0; index < eventBuffer*3; index++ {
			taskRunStore.AppendTaskEvent(taskRunID, "agent.checkpoint.sent", `{"message":"진행중"}`)
		}
	})

	stream := streamer.StreamTurn(context.Background(), agentcontract.AgentTurnRequest{RequesterPersonID: "person-1", Prompt: "해줘"})
	turnResult, errorValue := stream.Result()
	if errorValue != nil {
		t.Fatalf("expected the turn to finish: %v", errorValue)
	}
	if turnResult.FinishMessage != "done" {
		t.Fatalf("expected the result to survive an overflowing progress feed, got %q", turnResult.FinishMessage)
	}
}

func TestATurnJoinsTheTaskRunItWasGivenRatherThanStartingANewOne(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	existingTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "이어서")
	streamer := New(ledgerWritingHarness{taskRunStore: taskRunService}, taskRunService)

	stream := streamer.StreamTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		ExistingTaskRunID: existingTaskRun.TaskRunID,
		Prompt:            "이어서",
	})
	turnResult, _ := stream.Result()

	if turnResult.TaskRun.TaskRunID != existingTaskRun.TaskRunID {
		t.Fatalf("expected the turn to continue the run it was given, got %q", turnResult.TaskRun.TaskRunID)
	}
	if len(taskRunService.ListTaskRun()) != 1 {
		t.Fatalf("expected no second task run to appear, got %d", len(taskRunService.ListTaskRun()))
	}
}
