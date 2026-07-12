package e2e

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"blueclaw/internal/task"
)

func gateNamedEvent(name string) task.TaskEvent {
	return task.TaskEvent{Name: name}
}

func gateCheckpointEvent(message string) task.TaskEvent {
	return task.TaskEvent{Name: "agent.checkpoint.sent", Body: fmt.Sprintf(`{"toolName":"alpha","message":%q}`, message)}
}

func TestAssertEventSubsequence(t *testing.T) {
	events := []task.TaskEvent{gateNamedEvent("a"), gateNamedEvent("b"), gateNamedEvent("c")}
	if errorValue := assertEventSubsequence(events, []string{"a", "c"}); errorValue != nil {
		t.Fatalf("expected subsequence a,c to match: %v", errorValue)
	}
	if assertEventSubsequence(events, []string{"c", "a"}) == nil {
		t.Fatal("expected order violation c,a to fail")
	}
	if assertEventSubsequence(events, []string{"a", "z"}) == nil {
		t.Fatal("expected missing event z to fail")
	}
	if errorValue := assertEventSubsequence(events, nil); errorValue != nil {
		t.Fatalf("expected empty expectation to pass: %v", errorValue)
	}
}

func TestAssertTurnResultGateFields(t *testing.T) {
	passingResult := VirtualTurnResult{
		TaskStatus:    task.TaskStatusCompleted,
		FinishMessage: "최종 답변",
		Events: []task.TaskEvent{
			gateNamedEvent("tool.alpha.requested"),
			gateCheckpointEvent("작업 진행 중입니다"),
			gateNamedEvent("tool.alpha.result"),
		},
	}
	passingTurn := VirtualTurn{
		ExpectedSequence:          []string{"tool.alpha.requested", "tool.alpha.result"},
		ExpectedCheckpointReplies: []string{"진행 중"},
		ForbiddenEvents:           []string{"agent.no_progress_loop_stopped"},
		ExpectedTaskStatus:        task.TaskStatusCompleted,
	}
	if errorValue := assertTurnResult("", passingTurn, passingResult); errorValue != nil {
		t.Fatalf("expected gate to pass: %v", errorValue)
	}

	failureCases := map[string]VirtualTurn{
		"checkpoint fragment missing": {ExpectedCheckpointReplies: []string{"존재하지 않는 문구"}},
		"sequence order violated":     {ExpectedSequence: []string{"tool.alpha.result", "tool.alpha.requested"}},
		"task status mismatch":        {ExpectedTaskStatus: task.TaskStatusBlocked},
	}
	for name, failingTurn := range failureCases {
		if assertTurnResult("", failingTurn, passingResult) == nil {
			t.Fatalf("expected gate to fail for %q", name)
		}
	}

	stalledResult := passingResult
	stalledResult.Events = append([]task.TaskEvent{gateNamedEvent("agent.no_progress_loop_stopped")}, passingResult.Events...)
	if assertTurnResult("", VirtualTurn{ForbiddenEvents: []string{"agent.no_progress_loop_stopped"}}, stalledResult) == nil {
		t.Fatal("expected forbidden no-progress event to fail the gate")
	}
}

func TestStreamProgressObserverFormatsReplyAndTool(t *testing.T) {
	buffer := &bytes.Buffer{}
	observe := streamProgressObserver(buffer)
	observe(task.RawTurnEvent{Name: "agent.checkpoint.sent", Body: `{"toolName":"alpha","message":"진행 중"}`})
	observe(task.RawTurnEvent{Name: "tool.web.search.requested", Body: "{}"})
	observe(task.RawTurnEvent{Name: "tool.web.search.result", Body: "{}"})
	output := buffer.String()
	if !strings.Contains(output, "reply: 진행 중") {
		t.Fatalf("expected reply line, got %q", output)
	}
	if !strings.Contains(output, "tool: web.search") {
		t.Fatalf("expected tool line, got %q", output)
	}
	if strings.Count(output, "\n") != 2 {
		t.Fatalf("expected 2 progress lines (tool result ignored), got %q", output)
	}
}

func TestCheckpointReplyMalformedBodyDoesNotPanic(t *testing.T) {
	events := []task.TaskEvent{{Name: "agent.checkpoint.sent", Body: "{not json"}}
	if checkpointRepliesContain(events, "anything") {
		t.Fatal("expected malformed checkpoint body to yield no replies")
	}
}

func TestResultEventFragmentMatchingTargetsToolOutputOnly(t *testing.T) {
	resultEvent := task.TaskEvent{
		Name: "tool.alpha.result",
		Body: `{"observationID":"obs-1","action":"continue","tool":"alpha","output":{"content":"real output token"},"toolInputKey":"alpha\u0000{\"query\":\"input-only-token\"}","durationMs":1}`,
	}
	events := []task.TaskEvent{resultEvent}
	if countEventsWithFragment(events, "tool.alpha.result", "input-only-token") != 0 {
		t.Fatal("result-event fragment matching must not match the canonicalized tool input")
	}
	if countEventsWithFragment(events, "tool.alpha.result", "real output token") != 1 {
		t.Fatal("result-event fragment matching must match the observation output content")
	}
	if eventsContain(events, "tool.alpha.result", "input-only-token") {
		t.Fatal("eventsContain must not match the canonicalized tool input for result events")
	}
	if !eventsContain(events, "tool.alpha.result", "real output token") {
		t.Fatal("eventsContain must match the observation output content for result events")
	}

	failedResultEvent := task.TaskEvent{
		Name: "tool.alpha.result",
		Body: `{"observationID":"obs-2","action":"continue","tool":"alpha","output":{"content":"requires approval"},"failure":{"kind":"policy_blocked","code":"approval_required"},"toolInputKey":"alpha\u0000{}","durationMs":1}`,
	}
	if !eventsContain([]task.TaskEvent{failedResultEvent}, "tool.alpha.result", "approval_required") {
		t.Fatal("result-event fragment matching must still cover the tool failure payload")
	}

	requestedEvent := task.TaskEvent{
		Name: "tool.alpha.requested",
		Body: `{"observationID":"obs-1","toolName":"alpha","input":{"query":"input-only-token"}}`,
	}
	if countEventsWithFragment([]task.TaskEvent{requestedEvent}, "tool.alpha.requested", "input-only-token") != 1 {
		t.Fatal("requested-event fragment matching must keep matching the tool input")
	}
}

func TestLooseModeEnforcesCountExpectations(t *testing.T) {
	turnResult := VirtualTurnResult{
		TaskStatus:    task.TaskStatusCompleted,
		FinishMessage: "최종 답변",
		Events: []task.TaskEvent{
			gateNamedEvent("tool.alpha.requested"),
			{Name: "tool.alpha.result", Body: `{"observationID":"obs-1","action":"continue","tool":"alpha","output":{"content":"alpha output"},"durationMs":1}`},
		},
	}
	passingTurn := VirtualTurn{
		ExpectedToolCallCounts: map[string]int{"alpha": 1},
		ExpectedEventCounts:    []VirtualEventCount{{Name: "tool.alpha.result", BodyFragment: "alpha output", Count: 1}},
	}
	if errorValue := assertLooseTurnResult(passingTurn, turnResult); errorValue != nil {
		t.Fatalf("expected loose mode to accept satisfied counts: %v", errorValue)
	}
	if assertLooseTurnResult(VirtualTurn{ExpectedToolCallCounts: map[string]int{"alpha": 2}}, turnResult) == nil {
		t.Fatal("expected loose mode to enforce expected tool call counts")
	}
	if assertLooseTurnResult(VirtualTurn{ExpectedEventCounts: []VirtualEventCount{{Name: "tool.alpha.result", BodyFragment: "missing", Count: 1}}}, turnResult) == nil {
		t.Fatal("expected loose mode to enforce expected event counts")
	}
}

func TestLooseModeStillEnforcesStructuralFields(t *testing.T) {
	turnResult := VirtualTurnResult{
		TaskStatus:    task.TaskStatusCompleted,
		FinishMessage: "최종 답변",
		Events:        []task.TaskEvent{gateNamedEvent("agent.no_progress_loop_stopped")},
	}
	if errorValue := assertLooseTurnResult(VirtualTurn{}, turnResult); errorValue != nil {
		t.Fatalf("expected loose mode without structural expectations to pass: %v", errorValue)
	}
	if assertLooseTurnResult(VirtualTurn{ForbiddenEvents: []string{"agent.no_progress_loop_stopped"}}, turnResult) == nil {
		t.Fatal("expected loose mode to still enforce forbidden events")
	}
	if assertLooseTurnResult(VirtualTurn{ExpectedSequence: []string{"missing.event"}}, turnResult) == nil {
		t.Fatal("expected loose mode to still enforce expected sequence")
	}
}
