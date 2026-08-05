package scheduler

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestShouldNotifyUserOnlyWhenNothingWasDelivered(t *testing.T) {
	sweeper := StaleTaskSweeper{Notifier: stubStaleTaskNotifier{}}
	cases := []struct {
		name         string
		taskRun      task.TaskRun
		shouldNotify bool
	}{
		{"blocked with delivered result", task.TaskRun{Status: task.TaskStatusBlocked, Result: "already told the user"}, false},
		{"blocked with no delivery", task.TaskRun{Status: task.TaskStatusBlocked}, true},
		{"waiting approval", task.TaskRun{Status: task.TaskStatusWaitingApproval, Result: "question was asked"}, true},
		{"waiting user input", task.TaskRun{Status: task.TaskStatusWaitingUserInput}, true},
	}
	for _, testCase := range cases {
		if sweeper.shouldNotifyUser(testCase.taskRun) != testCase.shouldNotify {
			t.Fatalf("%s: shouldNotifyUser = %v, want %v", testCase.name, !testCase.shouldNotify, testCase.shouldNotify)
		}
	}
}

func TestShouldNotifyUserRequiresNotifier(t *testing.T) {
	sweeper := StaleTaskSweeper{}
	if sweeper.shouldNotifyUser(task.TaskRun{Status: task.TaskStatusWaitingApproval}) {
		t.Fatal("a sweeper without a notifier must expire quietly")
	}
}

func TestStaleTaskUserFacingReasonNamesTheWait(t *testing.T) {
	if !strings.Contains(staleTaskUserFacingReason("waiting_expired"), "expired without an answer") {
		t.Fatal("waiting expiry must explain the unanswered wait")
	}
	if !strings.Contains(staleTaskUserFacingReason("blocked_expired"), "closed") {
		t.Fatal("blocked expiry must state the task was closed")
	}
}

type stubStaleTaskNotifier struct{}

func (stubStaleTaskNotifier) FailUnresumedInterruptedTaskRun(_ context.Context, _ task.TaskRun, _ string) bool {
	return true
}
