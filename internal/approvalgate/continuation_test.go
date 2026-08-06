package approvalgate

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func taskRunWithHeldCall(t *testing.T) (*task.TaskRunService, string) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "내일 회의 지워줘")
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "approval.pending_call", `{"toolName":"calendar_delete","toolInput":{"eventHint":"내일 회의"},"confirmation":"지울까요?"}`)
	return taskRunService, taskRun.TaskRunID
}

func TestAnApprovedCallIsHandedBackWithTheInputItWasApprovedWith(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	taskRunService.AppendTaskEvent(taskRunID, "approval.decided", `{"decision":"confirm"}`)

	approvedCall, isApproved := ApprovedPendingCall(taskRunService.ListTaskEvent(taskRunID))
	if !isApproved || approvedCall.ToolName != "calendar_delete" {
		t.Fatalf("expected the approved call to be found, got %+v", approvedCall)
	}
	if !strings.Contains(string(approvedCall.ToolInput), "내일 회의") {
		t.Fatalf("expected the input the requester approved, got %q", approvedCall.ToolInput)
	}
}

func TestACallThatAlreadyRanIsNotHandedBackAgain(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	taskRunService.AppendTaskEvent(taskRunID, "approval.decided", `{"decision":"confirm"}`)
	taskRunService.AppendTaskEvent(taskRunID, "approval.executed", `{"toolName":"calendar_delete"}`)

	if _, isApproved := ApprovedPendingCall(taskRunService.ListTaskEvent(taskRunID)); isApproved {
		t.Fatal("expected a call that already ran to stay carried out")
	}
}

func TestANewHeldCallDoesNotInheritTheDecisionMadeAboutTheLastOne(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	taskRunService.AppendTaskEvent(taskRunID, "approval.decided", `{"decision":"confirm"}`)
	taskRunService.AppendTaskEvent(taskRunID, "approval.executed", `{"toolName":"calendar_delete"}`)
	taskRunService.AppendTaskEvent(taskRunID, "approval.pending_call", `{"toolName":"message_send","toolInput":{"message":"보냅니다"},"confirmation":"보낼까요?"}`)

	if _, isApproved := ApprovedPendingCall(taskRunService.ListTaskEvent(taskRunID)); isApproved {
		t.Fatal("expected a freshly held call to wait for its own decision")
	}
}

func TestADeclinedCallIsReportedAsDeclinedRatherThanLeftSilent(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	taskRunService.AppendTaskEvent(taskRunID, "approval.decided", `{"decision":"cancel"}`)

	declinedCallNote := DeclinedCallNote(taskRunService.ListTaskEvent(taskRunID))
	if !strings.Contains(declinedCallNote, "declined") {
		t.Fatalf("expected the resumed turn to learn the requester said no, got %q", declinedCallNote)
	}
	if _, isApproved := ApprovedPendingCall(taskRunService.ListTaskEvent(taskRunID)); isApproved {
		t.Fatal("expected a declined call to stay uncarried")
	}
}

func TestATurnWithNothingPendingCarriesNoInstruction(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "안녕")

	if declinedCallNote := DeclinedCallNote(taskRunService.ListTaskEvent(taskRun.TaskRunID)); declinedCallNote != "" {
		t.Fatalf("expected an ordinary turn to be left alone, got %q", declinedCallNote)
	}
}
