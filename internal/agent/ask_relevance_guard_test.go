package agent

import (
	"context"
	"testing"

	"blueclaw/internal/task"
)

func TestAgentTurnRunnerRejectsOffTopicAskInputAfterToolFailureRecovery(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"input":"continue"}}`,
		`{"action":"continue","toolName":"ask.input","toolInput":{"question":"What is Gemma"}}`,
		finishMessageDocument("등록했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 3, MaxIterationCount: 6, MaxToolCallCount: 6})
	askInputCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "ask.input"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolFailureResult(FailureInvalidInput, FailureCodes.InvalidInput, "terminal_run", "executableName is required"), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "ask.input"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		askInputCallCount++
		return ToolSuccess(`{"status":"waiting_user_input"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "광장 채널이랑 잡담 채널에서 내가 언급한 업무를 태스크로 등록해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		ActiveGoal:        ActiveGoal{OriginalInstruction: "광장 채널이랑 잡담 채널에서 내가 언급한 업무를 태스크로 등록해줘"},
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to recover past the off-topic ask.input: %v", errorValue)
	}
	if askInputCallCount != 0 {
		t.Fatalf("expected the off-topic ask.input never to actually invoke the tool, got %d calls", askInputCallCount)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected the task to finish instead of pausing on an unrelated question, got %s", result.TaskRun.Status)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "agent.unrelated_ask_rejected", "What is Gemma") {
		t.Fatal("expected an unrelated_ask_rejected event for the off-topic question")
	}
	if taskEventsContain(events, "agent.goal.waiting_user_input", "") {
		t.Fatal("did not expect the task to record a waiting_user_input goal event for the rejected question")
	}
}

func TestAgentTurnRunnerAllowsRelevantAskInputToPauseNormally(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"ask.input","toolInput":{"question":"What is the correct channel, 광장 or 잡담?"}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	askInputCallCount := 0
	toolRegistry := newTestToolSet([]string{"ask.input"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "ask.input"}, func(ctx context.Context, invocation ToolInvocation) (ToolResult, error) {
		askInputCallCount++
		services.taskRunService.PauseTaskRun(TaskRunIDFromContext(ctx), task.TaskStatusWaitingUserInput, "What is the correct channel, 광장 or 잡담?")
		return ToolSuccess(`{"status":"waiting_user_input"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "광장 채널이랑 잡담 채널에서 내가 언급한 업무를 태스크로 등록해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		ActiveGoal:        ActiveGoal{OriginalInstruction: "광장 채널이랑 잡담 채널에서 내가 언급한 업무를 태스크로 등록해줘"},
	})
	if errorValue != nil {
		t.Fatalf("expected the relevant ask.input to run without error: %v", errorValue)
	}
	if askInputCallCount != 1 {
		t.Fatalf("expected the relevant ask.input to actually invoke the tool once, got %d calls", askInputCallCount)
	}
	if result.TaskRun.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected the relevant ask.input to pause the task normally, got %s", result.TaskRun.Status)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(events, "agent.unrelated_ask_rejected", "") {
		t.Fatal("did not expect a disambiguation question that shares goal vocabulary to be rejected as unrelated")
	}
}

func TestLooksLikeOffTopicMetaQuestion(t *testing.T) {
	cases := []struct {
		question string
		want     bool
	}{
		{"What is Gemma", true},
		{"무엇을 도와드릴까요", true},
		{"Did you mean the 광장 channel or the 잡담 channel?", false},
		{"이 메시지를 보낼까요?", false},
	}
	for _, testCase := range cases {
		if got := looksLikeOffTopicMetaQuestion(testCase.question); got != testCase.want {
			t.Fatalf("looksLikeOffTopicMetaQuestion(%q) = %v, want %v", testCase.question, got, testCase.want)
		}
	}
}

func TestSharesSignificantWordDetectsGoalOverlap(t *testing.T) {
	goalCorpus := "광장 채널이랑 잡담 채널에서 내가 언급한 업무를 태스크로 등록해줘"
	if sharesSignificantWord("What is Gemma", goalCorpus) {
		t.Fatal("expected an unrelated question to share no significant words with the goal")
	}
	if !sharesSignificantWord("What is the correct channel, 광장 or 잡담?", goalCorpus) {
		t.Fatal("expected a disambiguation question naming goal vocabulary to share a significant word")
	}
}
