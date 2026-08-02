package bluecollar

import (
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/taskstate"
)

// A tool that names an approval family is approved once for the task. A tool that
// names none - a send, a delete - is decided call by call, forever.

func TestApprovedScopeCoversTheRestOfTheTask(t *testing.T) {
	approvedScopes := taskApprovedScopes([]taskstate.TaskEvent{
		{Name: "approval.executed", Body: `{"toolName":"browser_open"}`},
		{Name: "approval.scope_granted", Body: `{"scope":"browser"}`},
	})

	if !approvedScopes["browser"] {
		t.Fatal("expected an approved browser family to cover the rest of the task")
	}
	if approvedScopes["file"] {
		t.Fatal("expected an approval to cover only the family it was given for")
	}
}

func TestNoScopeIsApprovedWithoutAGrantEvent(t *testing.T) {
	approvedScopes := taskApprovedScopes([]taskstate.TaskEvent{
		{Name: "approval.executed", Body: `{"toolName":"message_send"}`},
	})

	if len(approvedScopes) != 0 {
		t.Fatalf("expected an executed call alone to grant nothing, got %v", approvedScopes)
	}
}

func TestScopelessToolNeverBecomesSessionApproved(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions(nil)

	if scope := approvalScopeForTool(toolSet, "message_send"); scope != "" {
		t.Fatalf("expected a tool outside the set to claim no family, got %q", scope)
	}
}
