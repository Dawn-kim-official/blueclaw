package bluecollarharness

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/turnstream"
	"github.com/Dawn-kim-official/blueclaw/model"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"github.com/Dawn-kim-official/bluecollar"
)

const streamTestCompletionJudgeSchemaName = "blueclaw_completion_judge"

type scriptedTurnLanguageModel struct {
	contents     []string
	requestCount int
}

func (languageModel *scriptedTurnLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *scriptedTurnLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == streamTestCompletionJudgeSchemaName {
		document, _ := json.Marshal(map[string]any{
			"satisfied":   true,
			"missingWork": []string{},
			"reason":      "scripted test default",
		})
		return model.StructuredResponse{Content: string(document)}, nil
	}
	index := languageModel.requestCount
	languageModel.requestCount++
	if index >= len(languageModel.contents) {
		index = len(languageModel.contents) - 1
	}
	return model.StructuredResponse{Content: languageModel.contents[index]}, nil
}

func continueWithMessageDocument(toolName string, message string) string {
	return `{"action":"continue","toolName":"` + toolName + `","toolInput":{},"message":"` + message + `"}`
}

func finishMessageDocument(reply string) string {
	quotedReply := strconv.Quote(reply)
	return `{"action":"finish","message":` + quotedReply + `,"completionSummary":` + quotedReply + `,"replyParts":[{"type":"text","text":` + quotedReply + `}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"qualityReview":[]}`
}

func exhaustedRecoveryBudgetForTest() agentcontract.RecoveryBudget {
	return agentcontract.RecoveryBudget{CorrectedRetry: -1, AlternateRoute: -1, AdjacentTool: -1, NoToolFallback: -1}
}

type streamTestServices struct {
	runner           agentcontract.Harness
	taskRunService   *taskstate.TaskRunService
	taskEventService *taskstate.TaskEventService
}

func newStreamTestServices(languageModel model.LanguageModelProvider) streamTestServices {
	taskEventService := taskstate.NewTaskEventService()
	taskStepService := taskstate.NewTaskStepService()
	taskArtifactService := taskstate.NewTaskArtifactService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	turnOptions := agentcontract.TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()}
	return streamTestServices{
		runner:           bluecollar.NewAgentTurnRunner(taskRunService, taskStepService, taskArtifactService, languageModel, turnOptions),
		taskRunService:   taskRunService,
		taskEventService: taskEventService,
	}
}

func toolRegistryWithAlpha(t *testing.T) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"alpha"})
	toolSet.AllowTestReplacement()
	definition := toolcontract.ToolDefinition{
		ID:                "test:alpha",
		Name:              "alpha",
		Visibility:        toolcontract.ToolVisibilityModel,
		InputSchema:       json.RawMessage(`{"type":"object","properties":{}}`),
		InputIntentSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		OutputSchema:      json.RawMessage(`{"type":"object","properties":{}}`),
		ResultContract: &toolcontract.ToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		},
		SideEffectClass: toolcontract.ToolSideEffectStateChange,
	}
	registerErrorValue := toolSet.RegisterTool(definition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("alpha result", json.RawMessage(`{}`)), nil
	})
	if registerErrorValue != nil {
		t.Fatalf("expected the alpha tool to register: %v", registerErrorValue)
	}
	return toolSet
}

func streamTestTurnRequest(t *testing.T) agentcontract.AgentTurnRequest {
	t.Helper()
	toolSet := toolRegistryWithAlpha(t)
	return agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolSet,
		PinnedToolNames:   toolSet.ListToolNames(),
		CheckpointSender:  func(context.Context, agentcontract.AgentCheckpoint) error { return nil },
	}
}

func collectTurnEvents(turnStream *turnstream.Stream) []turnstream.Event {
	collected := []turnstream.Event{}
	for turnEvent := range turnStream.Events {
		collected = append(collected, turnEvent)
	}
	return collected
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

func TestStreamTurnEmitsOrderedProgressBeforeTheResult(t *testing.T) {
	languageModel := &scriptedTurnLanguageModel{contents: []string{
		continueWithMessageDocument("alpha", "first reply"),
		finishMessageDocument("last reply"),
	}}
	services := newStreamTestServices(languageModel)

	turnStream := turnstream.New(services.runner, services.taskRunService).StreamTurn(context.Background(), streamTestTurnRequest(t))
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
	services := newStreamTestServices(&scriptedTurnLanguageModel{contents: contents})

	turnStream := turnstream.New(services.runner, services.taskRunService).StreamTurn(context.Background(), streamTestTurnRequest(t))
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

	runTurnServices := newStreamTestServices(&scriptedTurnLanguageModel{contents: append([]string{}, script...)})
	runTurnServices.runner.RunTurn(context.Background(), streamTestTurnRequest(t))
	runTurnNames := taskEventNames(runTurnServices.taskEventService.ListTaskEvent(onlyTaskRunID(runTurnServices.taskRunService)))

	streamServices := newStreamTestServices(&scriptedTurnLanguageModel{contents: append([]string{}, script...)})
	collectTurnEvents(turnstream.New(streamServices.runner, streamServices.taskRunService).StreamTurn(context.Background(), streamTestTurnRequest(t)))
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
	services := newStreamTestServices(&scriptedTurnLanguageModel{contents: contents})

	turnStream := turnstream.New(services.runner, services.taskRunService).StreamTurn(context.Background(), streamTestTurnRequest(t))
	for range turnStream.Events {
		break
	}
	for range turnStream.Events {
	}
	turnStream.Result()
}
