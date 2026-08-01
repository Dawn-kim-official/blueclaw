package bluecollar

import (
	"context"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

func continueWithMessageDocument(operationName string, message string) string {
	return `{"action":"continue","toolName":"` + operationName + `","toolInput":{},"message":"` + message + `"}`
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
		continueWithMessageDocument("alpha", "first reply"),
		finishMessageDocument("last reply"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("alpha result"), nil
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
	if collected[replyIndex].Message != "first reply" {
		t.Fatalf("expected reply message, got %q", collected[replyIndex].Message)
	}
	if collected[toolIndex].ToolName != "alpha" {
		t.Fatalf("expected tool name alpha, got %q", collected[toolIndex].ToolName)
	}
	if collected[finalIndex].Result.FinishMessage != "last reply" {
		t.Fatalf("expected final finish message, got %q", collected[finalIndex].Result.FinishMessage)
	}
}

func TestStreamTurnPersistsSameEventsAsRunTurn(t *testing.T) {
	script := []string{
		continueWithMessageDocument("alpha", "in progress"),
		finishMessageDocument("done"),
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
		contents = append(contents, continueWithMessageDocument("alpha", "reply"+string(rune('a'+index%26))))
	}
	contents = append(contents, finishMessageDocument("end"))
	languageModel := &sequenceLanguageModel{contents: contents}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("alpha result"), nil
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
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("alpha result"), nil
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
