package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func planUpdateSuccessObservation(observationID string, planDocument string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        "continue",
		Tool:          PlanUpdateToolName,
		Output:        ToolOutput{Content: planDocument, Data: json.RawMessage(planDocument)},
	}
}

func hasTaskEvent(services turnRunnerTestServices, taskRunID string, eventName string) bool {
	for _, taskEvent := range services.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == eventName {
			return true
		}
	}
	return false
}

func TestTaskLevelRequiresPlan(t *testing.T) {
	for taskLevel, expected := range map[TaskLevel]bool{
		TaskLevelXLow:   false,
		TaskLevelLow:    false,
		TaskLevelMedium: true,
		TaskLevelHigh:   true,
		TaskLevelXHigh:  true,
		TaskLevelMax:    true,
		TaskLevel(""):   false,
	} {
		if taskLevelRequiresPlan(taskLevel) != expected {
			t.Fatalf("expected taskLevelRequiresPlan(%q) to be %v", taskLevel, expected)
		}
	}
}

func TestApplyPlanUpdateObservationMergesExecutionStateAndAppendsEvents(t *testing.T) {
	services := newTurnRunnerTestServices(&completionJudgeStubLanguageModel{}, TurnOptions{})
	state := &agentTaskState{ExecutionState: ExecutionState{Goal: "previous goal"}}
	observation := planUpdateSuccessObservation("obs-001", `{"goal":"ship the report","steps":[{"title":"gather data","status":"done"},{"title":"write summary","status":"in_progress"}]}`)

	services.runner.applyPlanUpdateObservation("task-plan-1", state, observation)

	if state.ExecutionState.Goal != "ship the report" {
		t.Fatalf("expected merged goal, got %q", state.ExecutionState.Goal)
	}
	if len(state.ExecutionState.Steps) != 2 || state.ExecutionState.Steps[1].Status != "in_progress" {
		t.Fatalf("expected merged steps, got %+v", state.ExecutionState.Steps)
	}
	if !hasTaskEvent(services, "task-plan-1", "agent.plan.updated") {
		t.Fatal("expected agent.plan.updated event")
	}
	if !hasTaskEvent(services, "task-plan-1", "agent.execution_state") {
		t.Fatal("expected agent.execution_state event so the plan survives restore")
	}
}

func TestApplyPlanUpdateObservationKeepsGoalWhenUpdateOmitsIt(t *testing.T) {
	services := newTurnRunnerTestServices(&completionJudgeStubLanguageModel{}, TurnOptions{})
	state := &agentTaskState{ExecutionState: ExecutionState{Goal: "previous goal"}}
	observation := planUpdateSuccessObservation("obs-001", `{"steps":[{"title":"only step","status":"pending"}]}`)

	services.runner.applyPlanUpdateObservation("task-plan-2", state, observation)

	if state.ExecutionState.Goal != "previous goal" {
		t.Fatalf("expected preserved goal, got %q", state.ExecutionState.Goal)
	}
	if len(state.ExecutionState.Steps) != 1 || state.ExecutionState.Steps[0].Title != "only step" {
		t.Fatalf("expected replaced steps, got %+v", state.ExecutionState.Steps)
	}
}

func TestApplyPlanUpdateObservationIgnoresFailedAndForeignObservations(t *testing.T) {
	services := newTurnRunnerTestServices(&completionJudgeStubLanguageModel{}, TurnOptions{})
	state := &agentTaskState{}
	failedObservation := planUpdateSuccessObservation("obs-001", `{"steps":[{"title":"x","status":"pending"}]}`)
	failedObservation.Failure = &ToolFailure{Kind: FailureUnknown}
	foreignObservation := successfulSideEffectObservation("obs-002", "task.add", `{}`, "created")

	services.runner.applyPlanUpdateObservation("task-plan-3", state, failedObservation)
	services.runner.applyPlanUpdateObservation("task-plan-3", state, foreignObservation)

	if len(state.ExecutionState.Steps) != 0 {
		t.Fatalf("expected no merge, got %+v", state.ExecutionState.Steps)
	}
	if hasTaskEvent(services, "task-plan-3", "agent.plan.updated") {
		t.Fatal("expected no agent.plan.updated event")
	}
}

func nudgeTestRequest(taskLevel TaskLevel) AgentTurnRequest {
	return AgentTurnRequest{
		TaskLevel: taskLevel,
		ToolSet: newTestToolSetWithDefinitions([]ToolDefinition{
			testToolDescriptor("task.add"),
			testToolDescriptor("task.list"),
			testToolDescriptor(PlanUpdateToolName),
		}),
	}
}

func TestNudgePlanFiresOnceForStateChangingToolWithoutPlan(t *testing.T) {
	services := newTurnRunnerTestServices(&completionJudgeStubLanguageModel{}, TurnOptions{})
	request := nudgeTestRequest(TaskLevelMedium)
	state := &agentTaskState{}
	actionDocument := turnActionDocument{Action: "continue", ToolName: "task.add", ToolInput: json.RawMessage(`{}`)}

	services.runner.notePlanMissingBeforeStateChange("task-nudge-1", request, state, actionDocument)

	if !state.DidNudgePlan {
		t.Fatal("expected DidNudgePlan to be set")
	}
	if len(state.Observations) != 1 || !strings.Contains(state.Observations[0].ContentText(), "plan.update") {
		t.Fatalf("expected a plan.update policy observation, got %+v", state.Observations)
	}
	if !hasTaskEvent(services, "task-nudge-1", "agent.plan.nudged") {
		t.Fatal("expected agent.plan.nudged event")
	}

	services.runner.notePlanMissingBeforeStateChange("task-nudge-1", request, state, actionDocument)
	if len(state.Observations) != 1 {
		t.Fatal("expected the nudge to fire at most once per task")
	}
}

func TestNudgePlanDoesNotFireForReadTools(t *testing.T) {
	services := newTurnRunnerTestServices(&completionJudgeStubLanguageModel{}, TurnOptions{})
	state := &agentTaskState{}
	actionDocument := turnActionDocument{Action: "continue", ToolName: "task.list", ToolInput: json.RawMessage(`{}`)}

	services.runner.notePlanMissingBeforeStateChange("task-nudge-2", nudgeTestRequest(TaskLevelMedium), state, actionDocument)

	if state.DidNudgePlan || len(state.Observations) != 0 {
		t.Fatalf("expected reads to stay free, got %+v", state.Observations)
	}
}

func TestNudgePlanDoesNotFireBelowMediumLevel(t *testing.T) {
	services := newTurnRunnerTestServices(&completionJudgeStubLanguageModel{}, TurnOptions{})
	actionDocument := turnActionDocument{Action: "continue", ToolName: "task.add", ToolInput: json.RawMessage(`{}`)}
	for _, taskLevel := range []TaskLevel{TaskLevelXLow, TaskLevelLow, ""} {
		state := &agentTaskState{}
		services.runner.notePlanMissingBeforeStateChange("task-nudge-3", nudgeTestRequest(taskLevel), state, actionDocument)
		if state.DidNudgePlan || len(state.Observations) != 0 {
			t.Fatalf("expected no nudge for level %q", taskLevel)
		}
	}
}

func TestNudgePlanDoesNotFireOnceStepsExist(t *testing.T) {
	services := newTurnRunnerTestServices(&completionJudgeStubLanguageModel{}, TurnOptions{})
	state := &agentTaskState{ExecutionState: ExecutionState{Steps: []PlanStep{{Title: "step", Status: "pending"}}}}
	actionDocument := turnActionDocument{Action: "continue", ToolName: "task.add", ToolInput: json.RawMessage(`{}`)}

	services.runner.notePlanMissingBeforeStateChange("task-nudge-4", nudgeTestRequest(TaskLevelHigh), state, actionDocument)

	if state.DidNudgePlan || len(state.Observations) != 0 {
		t.Fatalf("expected no nudge once a plan exists, got %+v", state.Observations)
	}
}

func TestRunTurnMergesPlanUpdateObservationIntoExecutionState(t *testing.T) {
	languageModel := &sequenceLanguageModel{modelTier: "low", contents: []string{
		`{"action":"continue","toolName":"plan.update","toolInput":{"goal":"answer the question","steps":[{"title":"look up the fact","status":"in_progress"}]}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{PlanUpdateToolName})
	planResultContract := &ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)}
	registerTestTool(toolRegistry, ToolDefinition{Name: PlanUpdateToolName, SideEffectClass: ToolSideEffectNone, ResultContract: planResultContract}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var input planUpdateDocument
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return ToolResult{}, errorValue
		}
		input.Goal, input.Steps = NormalizePlan(input.Goal, input.Steps)
		document := marshalEventBody(input)
		return ToolSuccessData(document, json.RawMessage(document)), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "done" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if !hasTaskEvent(services, result.TaskRun.TaskRunID, "agent.plan.updated") {
		t.Fatal("expected agent.plan.updated event from the continue flow")
	}
	planStateFromEvents := executionStateFromTaskEvents(services.taskRunService.ListTaskEvent(result.TaskRun.TaskRunID))
	if planStateFromEvents.Goal != "answer the question" || len(planStateFromEvents.Steps) != 1 {
		t.Fatalf("expected the merged plan persisted for restore, got %+v", planStateFromEvents)
	}
}

func TestRunTurnExecutesTheStateChangingCallTheNudgeAnnotates(t *testing.T) {
	languageModel := &sequenceLanguageModel{modelTier: "low", contents: []string{
		`{"action":"continue","toolName":"task.add","toolInput":{}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolSet := newTestToolSet([]string{"task.add", PlanUpdateToolName})
	wasToolInvoked := false
	registerTestTool(toolSet, testToolDescriptor("task.add"), func(_ context.Context, _ ToolInvocation) (ToolResult, error) {
		wasToolInvoked = true
		return ToolSuccessData(`{"taskID":"task-1"}`, json.RawMessage(`{"taskID":"task-1"}`)), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		TaskLevel:         TaskLevelMedium,
		PinnedToolNames:   []string{"task.add"},
		ToolSet:           toolSet,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if !hasTaskEvent(services, result.TaskRun.TaskRunID, "agent.plan.nudged") {
		eventNames := []string{}
		for _, event := range services.taskRunService.ListTaskEvent(result.TaskRun.TaskRunID) {
			eventNames = append(eventNames, event.Name)
		}
		t.Fatalf("expected the plan nudge to fire for the first state-changing call, events: %v invoked: %v finish: %q", eventNames, wasToolInvoked, result.FinishMessage)
	}
	if !wasToolInvoked {
		t.Fatal("expected the nudged state-changing call to execute anyway")
	}
}

func TestCompletionJudgeMessagesIncludePlanChecklistHint(t *testing.T) {
	observations := []turnObservation{
		planUpdateSuccessObservation("obs-001", `{"goal":"ship","steps":[{"title":"build the deck","status":"done"}]}`),
	}

	messages := completionJudgeMessages(AgentTurnRequest{Prompt: "make a deck"}, observations, turnActionDocument{})
	joined := joinedMessageContent(messages)

	if !strings.Contains(joined, "checklist hint") || !strings.Contains(joined, "build the deck") {
		t.Fatalf("expected plan checklist hint in judge prompt, got %s", joined)
	}

	messagesWithoutPlan := completionJudgeMessages(AgentTurnRequest{Prompt: "make a deck"}, nil, turnActionDocument{})
	if strings.Contains(joinedMessageContent(messagesWithoutPlan), "checklist hint") {
		t.Fatal("expected no plan hint without a plan.update observation")
	}
}

func TestNudgePlanSkipsWhenPlanToolIsUnavailable(t *testing.T) {
	services := newTurnRunnerTestServices(&completionJudgeStubLanguageModel{}, TurnOptions{})
	request := AgentTurnRequest{
		TaskLevel: TaskLevelMedium,
		ToolSet: newTestToolSetWithDefinitions([]ToolDefinition{
			testToolDescriptor("task.add"),
		}),
	}
	state := &agentTaskState{}
	actionDocument := turnActionDocument{Action: "continue", ToolName: "task.add", ToolInput: json.RawMessage(`{}`)}

	services.runner.notePlanMissingBeforeStateChange("task-nudge-2", request, state, actionDocument)

	if state.DidNudgePlan || len(state.Observations) != 0 {
		t.Fatalf("expected no nudge without a reachable plan tool, got %+v", state.Observations)
	}
}
