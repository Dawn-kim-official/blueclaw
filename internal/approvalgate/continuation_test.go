package approvalgate

import (
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

func taskRunWithHeldCall(t *testing.T) (*task.TaskRunService, string) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "내일 회의 지워줘")
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "approval.pending_call", `{"toolName":"calendar_delete","confirmation":"지울까요?"}`)
	return taskRunService, taskRun.TaskRunID
}

func TestAResumedTurnIsToldTheApprovalArrivedAndWhichCallStillNeedsToRun(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	taskRunService.AppendTaskEvent(taskRunID, "approval.decided", `{"decision":"confirm"}`)

	continuationNote := ApprovalContinuationNote(taskRunService.ListTaskEvent(taskRunID))
	if !strings.Contains(continuationNote, "calendar_delete") {
		t.Fatalf("expected the resumed turn to learn which call was approved, got %q", continuationNote)
	}
}

func TestATurnIsNotToldToRerunACallThatAlreadyRan(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	taskRunService.AppendTaskEvent(taskRunID, "approval.decided", `{"decision":"confirm"}`)
	taskRunService.AppendTaskEvent(taskRunID, "approval.executed", `{"toolName":"calendar_delete"}`)

	if continuationNote := ApprovalContinuationNote(taskRunService.ListTaskEvent(taskRunID)); continuationNote != "" {
		t.Fatalf("expected no instruction to repeat a call that already ran, got %q", continuationNote)
	}
}

func TestADeclinedCallIsReportedAsDeclinedRatherThanLeftSilent(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	taskRunService.AppendTaskEvent(taskRunID, "approval.decided", `{"decision":"cancel"}`)

	continuationNote := ApprovalContinuationNote(taskRunService.ListTaskEvent(taskRunID))
	if !strings.Contains(continuationNote, "declined") {
		t.Fatalf("expected the resumed turn to learn the requester said no, got %q", continuationNote)
	}
}

func TestATurnWithNothingPendingCarriesNoInstruction(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "안녕")

	if continuationNote := ApprovalContinuationNote(taskRunService.ListTaskEvent(taskRun.TaskRunID)); continuationNote != "" {
		t.Fatalf("expected an ordinary turn to be left alone, got %q", continuationNote)
	}
}
