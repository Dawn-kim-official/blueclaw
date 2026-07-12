package agent

import (
	"context"
	"strings"
	"testing"
)

func TestTaskIntakePromptPinsEstimateAndLaunchNoticeGuidance(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"research_task","level":"low","requestedOutputFormats":null,"reason":"bounded tool work","userFacingReply":""}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{
		IsEnabled:        true,
		DefaultTaskLevel: TaskLevelLow,
	})

	planner.Plan(context.Background(), AgentRequest{Prompt: "draft a short memo"})

	if len(languageModel.requests) != 1 {
		t.Fatalf("expected one intake model call, got %d", len(languageModel.requests))
	}
	prompt := joinedMessageContent(languageModel.requests[0].Messages)

	t.Run("careful human professional estimate anchor", func(t *testing.T) {
		if !strings.Contains(prompt, "careful human professional") {
			t.Fatal("dropping 'careful human professional' removes the estimate-realism anchor for estimatedMinutes; without it the model reverts to a rushed-minimum guess instead of how long a careful professional would actually take, which is the root cause behind unrealistically short launch notices")
		}
	})

	t.Run("do not lowball directive", func(t *testing.T) {
		if !strings.Contains(prompt, "Do not lowball") {
			t.Fatal("dropping 'Do not lowball' removes the explicit anti-lowball instruction on estimatedMinutes; the model drifts back toward minimizing its own time estimate, reintroducing the '5분 정도' launch notices incident of 2026-07-12")
		}
	})

	t.Run("launch notice must not leak internal budget", func(t *testing.T) {
		if !strings.Contains(prompt, "do not state any specific time estimate, minute count, or internal budget in launchNotice") {
			t.Fatal("dropping this clause lets the model leak its internal minute estimate straight into the user-facing launchNotice text, which is the exact '5분 정도' launch notice regression of 2026-07-12 that this sentence was added to prevent")
		}
	})

	t.Run("often 15 or more calibration for artifact work", func(t *testing.T) {
		if !strings.Contains(prompt, "often 15 or more") {
			t.Fatal("dropping 'often 15 or more' removes the calibration anchor for design, document, deck, and site work; without it estimatedMinutes for artifact-heavy tasks drifts back down to unrealistically short durations and undersized work budgets")
		}
	})
}

func TestWorkspaceContextStatesConversationDefaultDirectory(t *testing.T) {
	circleDescription := buildWorkspaceContextDescription(AgentTurnRequest{WorkspaceDefaultPath: "/workspace/circles/staff"})
	if !strings.Contains(circleDescription, "This conversation's default directory is /workspace/circles/staff") {
		t.Fatalf("workspace context must state the concrete conversation default directory so the agent knows where it is working (2026-07-12 IR deck incident: relative writes landed outside the conversation directory unnoticed); got %q", circleDescription)
	}
	privateDescription := buildWorkspaceContextDescription(AgentTurnRequest{WorkspaceDefaultPath: "/workspace/private/people/person-1"})
	if !strings.Contains(privateDescription, "This conversation's default directory is ~") {
		t.Fatalf("workspace context must state the private default directory as ~; got %q", privateDescription)
	}
	if strings.Contains(privateDescription, "/workspace/private/people/") {
		t.Fatalf("workspace context must not expose concrete private paths; got %q", privateDescription)
	}
}
