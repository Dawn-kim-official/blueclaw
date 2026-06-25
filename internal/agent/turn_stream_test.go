package agent

import (
	"context"
	"testing"

	"blueclaw/internal/task"
)

func continueWithMessageDocument(toolName string, message string) string {
	return `{"action":"continue","toolName":"` + toolName + `","toolInput":{},"message":"` + message + `"}`
}

func collectTurnEvents(events <-chan TurnEvent) []TurnEvent {
	collected := []TurnEvent{}
	for turnEvent := range events {
		collected = append(collected, turnEvent)
	}
	return collected
}

func TestStreamTurnEmitsOrderedEventsEndingWithFinal(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		continueWithMessageDocument("alpha", "첫 번째 답변"),
		finishMessageDocument("마지막 답변"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"alpha"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("alpha result"), nil
	})

	events := services.runner.StreamTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		CheckpointSender:  func(context.Context, AgentCheckpoint) error { return nil },
	})
	collected := collectTurnEvents(events)

	replyIndex := indexOfTurnEventKind(collected, TurnEventReply)
	toolIndex := indexOfTurnEventKind(collected, TurnEventTool)
	finalIndex := indexOfTurnEventKind(collected, TurnEventFinal)
	if replyIndex < 0 || toolIndex < 0 || finalIndex < 0 {
		t.Fatalf("expected reply, tool, and final events, got %v", collected)
	}
	if !(replyIndex < toolIndex && toolIndex < finalIndex) {
		t.Fatalf("expected reply before tool before final, got reply=%d tool=%d final=%d", replyIndex, toolIndex, finalIndex)
	}
	if finalIndex != len(collected)-1 {
		t.Fatalf("expected final event to be last, got index %d of %d", finalIndex, len(collected))
	}
	if collected[replyIndex].Message != "첫 번째 답변" {
		t.Fatalf("expected reply message, got %q", collected[replyIndex].Message)
	}
	if collected[toolIndex].ToolName != "alpha" {
		t.Fatalf("expected tool name alpha, got %q", collected[toolIndex].ToolName)
	}
	if collected[finalIndex].Result.FinishMessage != "마지막 답변" {
		t.Fatalf("expected final finish message, got %q", collected[finalIndex].Result.FinishMessage)
	}
}

func TestStreamTurnPersistsSameEventsAsRunTurn(t *testing.T) {
	script := []string{
		continueWithMessageDocument("alpha", "진행 중"),
		finishMessageDocument("완료"),
	}

	runTurnServices := newTurnRunnerTestServices(&sequenceLanguageModel{contents: append([]string{}, script...)}, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	runTurnServices.runner.RunTurn(context.Background(), turnRequestWithTool(runTurnServices))
	runTurnNames := taskEventNames(runTurnServices.taskEventService.ListTaskEvent(onlyTaskRunID(runTurnServices.taskRunService)))

	streamServices := newTurnRunnerTestServices(&sequenceLanguageModel{contents: append([]string{}, script...)}, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	collectTurnEvents(streamServices.runner.StreamTurn(context.Background(), turnRequestWithTool(streamServices)))
	streamNames := taskEventNames(streamServices.taskEventService.ListTaskEvent(onlyTaskRunID(streamServices.taskRunService)))

	if !equalStringSlices(runTurnNames, streamNames) {
		t.Fatalf("observer changed persisted events:\n run-turn: %v\n stream:   %v", runTurnNames, streamNames)
	}
}

func TestStreamTurnAbandonedConsumerDoesNotPanic(t *testing.T) {
	contents := []string{}
	for index := 0; index < streamTurnEventBuffer*2; index++ {
		contents = append(contents, continueWithMessageDocument("alpha", "답변"+string(rune('a'+index%26))))
	}
	contents = append(contents, finishMessageDocument("끝"))
	languageModel := &sequenceLanguageModel{contents: contents}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"alpha"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("alpha result"), nil
	})

	events := services.runner.StreamTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		CheckpointSender:  func(context.Context, AgentCheckpoint) error { return nil },
	})
	for range events {
		break
	}
	for range events {
	}
}

func turnRequestWithTool(services turnRunnerTestServices) AgentTurnRequest {
	return AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistryWithAlpha(),
		CheckpointSender:  func(context.Context, AgentCheckpoint) error { return nil },
	}
}

func toolRegistryWithAlpha() *ToolSet {
	toolRegistry := newTestToolSet([]string{"alpha"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("alpha result"), nil
	})
	return toolRegistry
}

func indexOfTurnEventKind(turnEvents []TurnEvent, kind TurnEventKind) int {
	for index, turnEvent := range turnEvents {
		if turnEvent.Kind == kind {
			return index
		}
	}
	return -1
}

func taskEventNames(taskEvents []task.TaskEvent) []string {
	names := make([]string, len(taskEvents))
	for index, taskEvent := range taskEvents {
		names[index] = taskEvent.Name
	}
	return names
}

func onlyTaskRunID(taskRunService *task.TaskRunService) string {
	taskRuns := taskRunService.ListTaskRun()
	if len(taskRuns) != 1 {
		return ""
	}
	return taskRuns[0].TaskRunID
}

func equalStringSlices(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
