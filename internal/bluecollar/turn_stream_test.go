package bluecollar

import (
	"context"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/turnstream"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

func continueWithMessageDocument(operationName string, message string) string {
	return `{"action":"continue","toolName":"` + operationName + `","toolInput":{},"message":"` + message + `"}`
}

func collectTurnEvents(turnStream *turnstream.Stream) []turnstream.Event {
	collected := []turnstream.Event{}
	for turnEvent := range turnStream.Events {
		collected = append(collected, turnEvent)
	}
	return collected
}

func TestStreamTurnEmitsOrderedProgressBeforeTheResult(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		continueWithMessageDocument("alpha", "first reply"),
		finishMessageDocument("last reply"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "alpha"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("alpha result"), nil
	})

	turnStream := turnstream.New(services.runner, services.taskRunService).StreamTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		CheckpointSender:  func(context.Context, AgentCheckpoint) error { return nil },
	})
	collected := collectTurnEvents(turnStream)

	replyIndex := indexOfTurnEventKind(collected, turnstream.EventReply)
	toolIndex := indexOfTurnEventKind(collected, turnstream.EventTool)
	if replyIndex < 0 || toolIndex < 0 {
		t.Fatalf("expected reply and tool events, got %v", collected)
	}
	if replyIndex >= toolIndex {
		t.Fatalf("expected reply before tool, got reply=%d tool=%d", replyIndex, toolIndex)
	}
	if collected[replyIndex].Message != "first reply" {
		t.Fatalf("expected reply message, got %q", collected[replyIndex].Message)
	}
	if collected[toolIndex].ToolName != "alpha" {
		t.Fatalf("expected tool name alpha, got %q", collected[toolIndex].ToolName)
	}
	turnResult, errorValue := turnStream.Result()
	if errorValue != nil {
		t.Fatalf("expected the turn to finish: %v", errorValue)
	}
	if turnResult.FinishMessage != "last reply" {
		t.Fatalf("expected the finished turn to carry its message, got %q", turnResult.FinishMessage)
	}
}

func TestAFloodOfProgressNeverCostsTheTurnItsResult(t *testing.T) {
	contents := []string{}
	for index := 0; index < 64*2; index++ {
		contents = append(contents, continueWithMessageDocument("alpha", "reply"+string(rune('a'+index%26))))
	}
	contents = append(contents, finishMessageDocument("survived the flood"))
	services := newTurnRunnerTestServices(&sequenceLanguageModel{contents: contents}, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})

	turnStream := turnstream.New(services.runner, services.taskRunService).StreamTurn(context.Background(), turnRequestWithTool(services))
	turnResult, errorValue := turnStream.Result()
	if errorValue != nil {
		t.Fatalf("expected the turn to finish: %v", errorValue)
	}
	if turnResult.TaskRun.TaskRunID == "" {
		t.Fatal("expected the finished turn to come back even when its progress overflowed, because an empty result reads exactly like a clean success")
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
	collectTurnEvents(turnstream.New(streamServices.runner, streamServices.taskRunService).StreamTurn(context.Background(), turnRequestWithTool(streamServices)))
	streamNames := taskEventNames(streamServices.taskEventService.ListTaskEvent(onlyTaskRunID(streamServices.taskRunService)))

	if !equalStringSlices(runTurnNames, streamNames) {
		t.Fatalf("observer changed persisted events:\n run-turn: %v\n stream:   %v", runTurnNames, streamNames)
	}
}

func TestStreamTurnAbandonedConsumerDoesNotPanic(t *testing.T) {
	contents := []string{}
	for index := 0; index < 64*2; index++ {
		contents = append(contents, continueWithMessageDocument("alpha", "reply"+string(rune('a'+index%26))))
	}
	contents = append(contents, finishMessageDocument("end"))
	languageModel := &sequenceLanguageModel{contents: contents}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "alpha"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("alpha result"), nil
	})

	turnStream := turnstream.New(services.runner, services.taskRunService).StreamTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		CheckpointSender:  func(context.Context, AgentCheckpoint) error { return nil },
	})
	for range turnStream.Events {
		break
	}
	for range turnStream.Events {
	}
	turnStream.Result()
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

func toolRegistryWithAlpha() *toolcontract.ToolSet {
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "alpha"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("alpha result"), nil
	})
	return toolRegistry
}

func indexOfTurnEventKind(turnEvents []turnstream.Event, kind turnstream.EventKind) int {
	for index, turnEvent := range turnEvents {
		if turnEvent.Kind == kind {
			return index
		}
	}
	return -1
}

func taskEventNames(taskEvents []taskstate.TaskEvent) []string {
	names := make([]string, len(taskEvents))
	for index, taskEvent := range taskEvents {
		names[index] = taskEvent.Name
	}
	return names
}

func onlyTaskRunID(taskRunService *taskstate.TaskRunService) string {
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
