package approvalgate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type immediateDecision struct {
	decision  mcpserver.ApprovalDecision
	isDecided bool
}

func (source immediateDecision) AwaitDecision(context.Context, string) (mcpserver.ApprovalDecision, bool) {
	return source.decision, source.isDecided
}

func gateFixture(t *testing.T) (*Gate, *task.TaskRunService, task.TaskRun) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "내일 회의 지워줘")
	return New(taskRunService), taskRunService, taskRun
}

func approvalRequestFixture(taskRunID string) mcpserver.ApprovalRequest {
	return mcpserver.ApprovalRequest{
		RequesterPersonID: "person-1",
		TaskRunID:         taskRunID,
		ToolName:          "calendar_delete",
		ToolInput:         json.RawMessage(`{"eventID":"event-1"}`),
		ApprovalScope:     "calendar",
		HarnessSession:    mcpserver.HarnessSession{HarnessName: "claude-code", SessionID: "session-uuid", IsResumable: true},
	}
}

func heldCallEventBody(t *testing.T, taskRunService *task.TaskRunService, taskRunID string) string {
	t.Helper()
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == "approval.pending_call" {
			return taskEvent.Body
		}
	}
	t.Fatal("expected a held call to be recorded")
	return ""
}

func TestAHeldCallIsRecordedWithTheConversationItWasHeldIn(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("expected the gate to answer: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("expected the call to be held, got %+v", outcome)
	}

	body := heldCallEventBody(t, taskRunService, taskRun.TaskRunID)
	for _, expectedFragment := range []string{"calendar_delete", "event-1", "calendar", "claude-code", "session-uuid"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("expected the held call to carry %q so it can be resumed, got %s", expectedFragment, body)
		}
	}
	pausedTaskRun, _ := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if pausedTaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected the task run to wait for the requester, got %q", pausedTaskRun.Status)
	}
}

func TestAnApprovalThatArrivesQuicklyFinishesTheCallTheAgentIsWaitingOn(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseInlineWait(immediateDecision{decision: mcpserver.ApprovalDecisionApproved, isDecided: true}, time.Second)

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("expected the gate to answer: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("expected a quick approval to let the call proceed, got %+v", outcome)
	}
	runningTaskRun, _ := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if runningTaskRun.Status == task.TaskStatusWaitingApproval {
		t.Fatal("expected a call answered inline never to park the task run")
	}
	if heldCallEventBody(t, taskRunService, taskRun.TaskRunID) == "" {
		t.Fatal("expected the held call to be recorded even when answered inline, so the ledger reads the same either way")
	}
}

func TestAnApprovalThatDoesNotArriveInTimeFallsBackToTheDurableHold(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseInlineWait(immediateDecision{isDecided: false}, 10*time.Millisecond)

	outcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("expected an unanswered call to fall back to the durable hold, got %+v", outcome)
	}
	pausedTaskRun, _ := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if pausedTaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected the task run to be parked for later, got %q", pausedTaskRun.Status)
	}
}

func TestACallWithNoTaskRunToAnswerOnIsHeldRatherThanRun(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	gate := New(taskRunService)

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(""))
	if errorValue != nil {
		t.Fatalf("expected the gate to answer: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("expected a call the requester cannot be asked about to be held, got %+v", outcome)
	}
}

func recordDecision(taskRunService *task.TaskRunService, taskRunID string, decision string) {
	taskRunService.AppendTaskEvent(taskRunID, "approval.decided", `{"decision":"`+decision+`"}`)
}

func TestTheSameCallRunsOnceTheRequesterHasApprovedIt(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	heldOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if heldOutcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("expected the first call to be held, got %+v", heldOutcome)
	}

	recordDecision(taskRunService, taskRun.TaskRunID, "confirm")

	approvedOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if approvedOutcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("expected the approved call to run when the agent reissues it, got %+v", approvedOutcome)
	}
}

func TestAnApprovalIsSpentOnTheCallItAnsweredAndNotTheNextOne(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	recordDecision(taskRunService, taskRun.TaskRunID, "confirm")
	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	repeatedOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if repeatedOutcome.Decision == mcpserver.ApprovalDecisionApproved {
		t.Fatal("expected one approval to authorise one call, so a second identical call is asked about again")
	}
}

func TestADeclinedCallComesBackRejectedRatherThanHeldForever(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	recordDecision(taskRunService, taskRun.TaskRunID, "cancel")

	declinedOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if declinedOutcome.Decision != mcpserver.ApprovalDecisionRejected {
		t.Fatalf("expected a declined call to be told so, got %+v", declinedOutcome)
	}
}
