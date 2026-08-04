package connectors

import (
	"context"
	"testing"

	"github.com/Dawn-kim-official/bluecollar/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

func TestUserSteerTaskProfileSourceReferenceResolvesRealPlatform(t *testing.T) {
	profile := userSteerTaskProfile("mattermost", "task-1")
	if platformFromSourceReference(profile.sourceReference) != "mattermost" {
		t.Fatalf("steer source reference must resolve the real platform, got %q", profile.sourceReference)
	}
}

func TestPlatformFromSourceReferenceRejectsResumePrefixes(t *testing.T) {
	for _, sourceReference := range []string{"auto_resume:task-1", "user_steer:task-1", "steer:task-1"} {
		if platform := platformFromSourceReference(sourceReference); platform != "" {
			t.Fatalf("non-adapter resume prefix must not resolve as a platform, %q gave %q", sourceReference, platform)
		}
	}
	if platformFromSourceReference("mattermost:thread:abc") != "mattermost" {
		t.Fatal("a real platform prefix must still resolve")
	}
}

func TestResumePausedTaskForSteerWithoutLaunchContextSendsNoticeWithoutOrphan(t *testing.T) {
	connectorRuntime, _, harness := newStubbedTestConnectorRuntime(t)
	harness.Reply = "저장된 컨텍스트에서 이 작업을 재개할 수 없습니다."
	pausedTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "사이트 만들어")
	event := testInboundEvent("message-steer-resume")
	sendReply := func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		return "dispatch-1", nil
	}

	result, errorValue := connectorRuntime.resumePausedTaskForSteer(context.Background(), "test", event, ReplyTarget{}, pausedTaskRun, "이어서 해", agentcontract.TurnDecision{}, sendReply)

	if errorValue != nil {
		t.Fatalf("missing launch context must be handled with a notice, got error %v", errorValue)
	}
	if !result.isHandled {
		t.Fatal("missing launch context must be handled, not silently dropped")
	}
	if connectorTaskEventsContain(connectorRuntime, pausedTaskRun.TaskRunID, "task.steer.requested", "") {
		t.Fatal("must not write an orphan task.steer.requested when resume is unavailable")
	}
	if !connectorTaskEventsContain(connectorRuntime, pausedTaskRun.TaskRunID, "task.steer.resume_unavailable", "") {
		t.Fatal("expected a task.steer.resume_unavailable diagnostic event")
	}
}

func TestInterruptedTaskTurnDecisionInheritsHighestRecordedEffort(t *testing.T) {
	taskEvents := []task.TaskEvent{
		{Name: "agent.intake", Body: `{"effortLevel":"deep","taskComplexity":"complex"}`},
		{Name: "agent.intake", Body: `{"effortLevel":"standard","taskComplexity":"normal","reason":"runtime_restart_auto_resume"}`},
		{Name: "agent.budget_escalated", Body: `{"newEffortLevel":"extended"}`},
	}
	decision := interruptedTaskTurnDecision(taskEvents, "ko")
	if decision.TaskLevel != agentcontract.TaskLevelHigh {
		t.Fatalf("resumed task must inherit the highest recorded task level, got %q", decision.TaskLevel)
	}
}

func TestInterruptedTaskTurnDecisionDefaultsToStandardEffort(t *testing.T) {
	decision := interruptedTaskTurnDecision([]task.TaskEvent{{Name: "agent.intake", Body: "not-json"}}, "ko")
	if decision.TaskLevel != agentcontract.TaskLevelLow {
		t.Fatalf("resumed task without recorded task level must default to low, got %q", decision.TaskLevel)
	}
}
