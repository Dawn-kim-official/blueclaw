package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

func TestBuildAgentActionRequestPreservesNativeToolCallingWireShape(t *testing.T) {
	seed := int64(77)
	temperature := 0.4
	toolSet := NewToolSet([]string{TerminalRunToolName, "site.publish"})
	toolSet.RegisterTool(ToolDefinition{
		Name:        TerminalRunToolName,
		Description: "Run a command.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("ran"), nil
	})
	toolSet.RegisterTool(ToolDefinition{
		Name:        "site.publish",
		Description: "Publish a site.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"required":["siteID"],"additionalProperties":false}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("published"), nil
	})
	state := agentTaskState{
		Request: AgentTurnRequest{
			RequesterPersonID: "person-1",
			ConversationID:    "conversation-1",
			Prompt:            "publish it",
			VisibleContext: VisibleContext{Messages: []VisibleContextMessage{{
				Speaker: "Lee",
				Text:    "Please publish the site.",
			}}},
			ToolSet: toolSet,
		},
		Options: TurnOptions{GenerationOptions: llm.GenerationOptions{
			Seed:        &seed,
			Temperature: &temperature,
		}},
	}

	request := BuildAgentActionRequest(state)

	if request.StructuredOutputSchema.Name != "blueclaw_agent_turn_action" {
		t.Fatalf("expected agent action schema name, got %q", request.StructuredOutputSchema.Name)
	}
	if request.GenerationOptions.Seed == nil || *request.GenerationOptions.Seed != seed {
		t.Fatalf("expected seed to be preserved, got %+v", request.GenerationOptions)
	}
	if request.GenerationOptions.Temperature == nil || *request.GenerationOptions.Temperature != temperature {
		t.Fatalf("expected temperature to be preserved, got %+v", request.GenerationOptions)
	}
	var schemaDocument struct {
		OneOf []map[string]any `json:"oneOf"`
	}
	if errorValue := json.Unmarshal([]byte(request.StructuredOutputSchema.Document), &schemaDocument); errorValue != nil {
		t.Fatalf("expected action schema JSON: %v", errorValue)
	}
	if len(schemaDocument.OneOf) == 0 {
		t.Fatal("expected action schema oneOf variants")
	}
	for _, variant := range schemaDocument.OneOf {
		properties := mapFromAny(variant["properties"])
		actionValues := stringSliceFromAny(mapFromAny(properties["action"])["enum"])
		if len(actionValues) != 1 {
			t.Fatalf("expected one action discriminator per variant, got %+v", actionValues)
		}
		if actionValues[0] != "continue" {
			continue
		}
		toolNameValues := stringSliceFromAny(mapFromAny(properties["toolName"])["enum"])
		if len(toolNameValues) != 1 {
			t.Fatalf("expected one toolName discriminator per continue variant, got %+v", toolNameValues)
		}
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"action":{"enum":["continue"]`) {
		t.Fatalf("expected continue action variant, got %s", request.StructuredOutputSchema.Document)
	}
	if strings.Contains(request.StructuredOutputSchema.Document, `"call_tool"`) || strings.Contains(request.StructuredOutputSchema.Document, `"final_reply"`) || strings.Contains(request.StructuredOutputSchema.Document, `"finalReply"`) {
		t.Fatalf("expected model-facing schema to omit legacy action aliases, got %s", request.StructuredOutputSchema.Document)
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolName":{"enum":["terminal.run"]`) {
		t.Fatalf("expected kernel toolName enum to be preserved, got %s", request.StructuredOutputSchema.Document)
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolName":{"enum":["site.publish"]`) {
		t.Fatalf("expected selected domain operation to remain in the model-facing schema, got %s", request.StructuredOutputSchema.Document)
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolInput"`) {
		t.Fatalf("expected toolInput to be preserved, got %s", request.StructuredOutputSchema.Document)
	}
	if strings.Contains(request.StructuredOutputSchema.Document, `"nextStepPlan"`) {
		t.Fatalf("expected continue action to omit nextStepPlan, got %s", request.StructuredOutputSchema.Document)
	}
	if strings.Contains(request.StructuredOutputSchema.Document, `"requestTools"`) {
		t.Fatalf("expected continue action to omit requestTools, got %s", request.StructuredOutputSchema.Document)
	}
	finishVariant := actionSchemaVariant(t, request.StructuredOutputSchema.Document, "finish")
	requiredFields := stringSliceFromAny(finishVariant["required"])
	for _, fieldName := range []string{"message", "completionEvidenceIDs", "qualityReview", "executionStateUpdate"} {
		if !containsString(requiredFields, fieldName) {
			t.Fatalf("expected finish schema to require %s, got %+v", fieldName, requiredFields)
		}
	}
	finishProperties := mapFromAny(finishVariant["properties"])
	qualityReviewItems := mapFromAny(mapFromAny(finishProperties["qualityReview"])["items"])
	if qualityReviewItems["additionalProperties"] != false {
		t.Fatalf("expected quality review items to reject undeclared fields, got %+v", qualityReviewItems)
	}
	qualityReviewProperties := mapFromAny(qualityReviewItems["properties"])
	if _, isPresent := qualityReviewProperties["evidenceIDs"]; !isPresent {
		t.Fatalf("expected quality review items to expose evidenceIDs, got %+v", qualityReviewProperties)
	}
	if _, isPresent := qualityReviewProperties["evidence"]; isPresent {
		t.Fatalf("expected quality review items to omit legacy evidence, got %+v", qualityReviewProperties)
	}
	if strings.Contains(request.StructuredOutputSchema.Document, `"finishMessage"`) {
		t.Fatalf("expected model-facing schema to omit legacy finishMessage, got %s", request.StructuredOutputSchema.Document)
	}
	if strings.Contains(request.StructuredOutputSchema.Document, `"action":{"enum":["tool.request"]`) {
		t.Fatalf("expected tool.request action to stay hidden, got %s", request.StructuredOutputSchema.Document)
	}
	if strings.Contains(request.StructuredOutputSchema.Document, "require_capabilities") {
		t.Fatalf("expected model-facing schema to omit require_capabilities, got %s", request.StructuredOutputSchema.Document)
	}
	if !messagesContain(request.Messages, "Recent visible conversation context") {
		t.Fatalf("expected visible context in model messages, got %+v", request.Messages)
	}
}

func TestBuildAgentActionRequestPreservesTypedInteractionTool(t *testing.T) {
	toolSet := NewToolSet([]string{AskInputToolName})
	toolSet.RegisterTool(ToolDefinition{
		Name:        AskInputToolName,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}},"required":["question"]}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("waiting"), nil
	})

	request := BuildAgentActionRequest(agentTaskState{Request: AgentTurnRequest{ToolSet: toolSet}})

	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolName":{"enum":["ask.input"]`) {
		t.Fatalf("expected typed ask.input exposure to remain in the action schema, got %s", request.StructuredOutputSchema.Document)
	}
}

func TestCapabilityInvokeActionSchemaPreservesWrapperRequiredFields(t *testing.T) {
	schemaDocument := buildActionSchemaFromToolDefinitions([]ToolDefinition{{
		Name:        CapabilityInvokeToolName,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"operation":{"type":"string","enum":["calendar.add"]},"input":{"type":"object"}},"required":["operation","input"]}`),
	}}, false, nil, false, false)

	continueVariant := actionSchemaVariant(t, schemaDocument, "continue")
	properties := mapFromAny(continueVariant["properties"])
	toolInput := mapFromAny(properties["toolInput"])
	requiredFields := stringSliceFromAny(toolInput["required"])
	if !containsString(requiredFields, "operation") || !containsString(requiredFields, "input") {
		t.Fatalf("expected capability.invoke wrapper fields to stay required, got %+v in %s", requiredFields, schemaDocument)
	}
	toolInputProperties := mapFromAny(toolInput["properties"])
	operationProperty := mapFromAny(toolInputProperties["operation"])
	if !containsString(stringSliceFromAny(operationProperty["enum"]), "calendar.add") {
		t.Fatalf("expected operation enum to be preserved, got %+v", operationProperty)
	}
}

func TestActionSchemaPreservesRequiredFieldsOnArrayOfNestedObjects(t *testing.T) {
	schemaDocument := buildActionSchemaFromToolDefinitions([]ToolDefinition{{
		Name:        "calendar.add",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}}},"required":["items"]}`),
	}}, false, nil, false, false)

	continueVariant := actionSchemaVariant(t, schemaDocument, "continue")
	properties := mapFromAny(continueVariant["properties"])
	toolInput := mapFromAny(properties["toolInput"])
	topLevelRequired := stringSliceFromAny(toolInput["required"])
	if !containsString(topLevelRequired, "items") {
		t.Fatalf("expected top-level required to include items, got %+v in %s", topLevelRequired, schemaDocument)
	}
	toolInputProperties := mapFromAny(toolInput["properties"])
	itemsProperty := mapFromAny(toolInputProperties["items"])
	arrayItemSchema := mapFromAny(itemsProperty["items"])
	nestedRequired := stringSliceFromAny(arrayItemSchema["required"])
	if !containsString(nestedRequired, "name") {
		t.Fatalf("expected required to be preserved two levels deep on array item objects, got %+v in %s", nestedRequired, schemaDocument)
	}
}

func TestBuildAgentActionRequestGenerationOptionsDoNotChangeSchema(t *testing.T) {
	seed := int64(88)
	temperature := 0.5
	toolSet := NewToolSet([]string{"browser.open"})
	toolSet.RegisterTool(ToolDefinition{Name: "browser.open"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("opened"), nil
	})
	state := agentTaskState{
		Request: AgentTurnRequest{
			Prompt:  "open browser",
			ToolSet: toolSet,
		},
	}
	seededState := state
	seededState.Options.GenerationOptions = llm.GenerationOptions{Seed: &seed, Temperature: &temperature}

	request := BuildAgentActionRequest(state)
	seededRequest := BuildAgentActionRequest(seededState)

	if request.StructuredOutputSchema.Document != seededRequest.StructuredOutputSchema.Document {
		t.Fatalf("expected generation options not to change schema document\nwithout=%s\nwith=%s", request.StructuredOutputSchema.Document, seededRequest.StructuredOutputSchema.Document)
	}
	if request.StructuredOutputSchema.Name != seededRequest.StructuredOutputSchema.Name {
		t.Fatalf("expected generation options not to change schema name")
	}
}

func TestRestoreAgentTaskStateRestoresTaskContextSummary(t *testing.T) {
	events := []task.TaskEvent{{
		Name: taskContextSummaryEventName,
		Body: marshalEventBody(TaskContextSummary{
			ObservationID:                 "context-summary-001",
			CompactedThroughObservationID: "obs-007",
			Goal:                          "finish the site",
			CompletedSteps:                []string{"created the app"},
			Artifacts:                     []string{"/workspace/site/index.html"},
			NextPlan:                      []string{"run verification"},
		}),
	}}

	state, errorValue := restoreAgentTaskState(AgentTurnRequest{Prompt: "continue"}, TurnOptions{}, task.TaskRun{
		TaskRunID: "task-1",
		Status:    task.TaskStatusRunning,
	}, events)

	if errorValue != nil {
		t.Fatalf("expected restore to succeed: %v", errorValue)
	}
	if state.ContextSummary.CompactedThroughObservationID != "obs-007" {
		t.Fatalf("expected context summary to be restored, got %+v", state.ContextSummary)
	}
	if len(state.ContextSummary.Artifacts) != 1 || state.ContextSummary.Artifacts[0] != "/workspace/site/index.html" {
		t.Fatalf("expected artifact path to be preserved, got %+v", state.ContextSummary.Artifacts)
	}
}

func TestParseAgentActionResponseUsesReplyPartsForFinishMessage(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"finish","message":"summary","replyParts":[{"type":"text","text":"done"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":[],"qualityReview":[]}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "finish" || finishActionMessage(action) != "done" {
		t.Fatalf("expected replyParts to provide finish message, got %+v", action)
	}
}

func TestParseAgentActionResponseNormalizesUntypedFinishReplyParts(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"finish","message":"직접 전달합니다.","replyParts":[{"type":"","text":"대기 중 입니다. 차후 첨부로 전달해드리겠습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":[],"qualityReview":[]}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if finishActionMessage(action) != "대기 중 입니다. 차후 첨부로 전달해드리겠습니다." {
		t.Fatalf("expected untyped replyParts text to stay visible, got %+v", action)
	}
}

func TestParseAgentActionResponseCoercesStringCompletionEvidenceIDs(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"finish","message":"완료했습니다.","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":"obs-005, obs-008","qualityReview":[]}`})
	if errorValue != nil {
		t.Fatalf("expected string completionEvidenceIDs to parse: %v", errorValue)
	}
	if len(action.CompletionEvidenceIDs) != 2 || action.CompletionEvidenceIDs[0] != "obs-005" || action.CompletionEvidenceIDs[1] != "obs-008" {
		t.Fatalf("expected coerced evidence IDs, got %+v", action.CompletionEvidenceIDs)
	}
}

func TestParseAgentActionResponseNormalizesNestedFinishBlock(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"executionStateUpdate":{"goal":"answer user"},"finish":{"message":"done","replyParts":[{"type":"text","text":"done"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"],"qualityReview":[{"id":"complete","passed":true,"evidenceIDs":["obs-001"]}]}}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "finish" || finishActionMessage(action) != "done" {
		t.Fatalf("expected nested finish block to normalize, got %+v", action)
	}
	if action.GoalSatisfied == nil || !*action.GoalSatisfied {
		t.Fatalf("expected goalSatisfied to be parsed, got %+v", action.GoalSatisfied)
	}
	if action.ExecutionStateUpdate.Goal != "answer user" {
		t.Fatalf("expected top-level execution state to be preserved, got %+v", action.ExecutionStateUpdate)
	}
	if len(action.CompletionEvidence) != 1 || action.CompletionEvidence[0].ObservationID != "obs-001" {
		t.Fatalf("expected nested completion evidence to expand, got %+v", action.CompletionEvidence)
	}
}

func TestParseAgentActionResponseNormalizesStringGoalSatisfied(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"finish","message":"done","goalStatus":"satisfied","goalSatisfied":"true","completionEvidenceIDs":[],"qualityReview":[]}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.GoalSatisfied == nil || !*action.GoalSatisfied {
		t.Fatalf("expected string boolean to normalize, got %+v", action.GoalSatisfied)
	}
}

func TestParseAgentActionResponseRejectsAmbiguousNestedActionBlocks(t *testing.T) {
	_, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"finish":{"message":"done"},"continue":{"toolName":"browser.open","toolInput":{}}}`})
	if errorValue == nil {
		t.Fatal("expected ambiguous action blocks to be rejected")
	}
}

func TestParseAgentActionResponseExpandsShallowEvidenceIDs(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"finish","message":"done","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"],"qualityReview":[{"id":"done","passed":true,"evidenceIDs":["obs-001"]}]}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if len(action.CompletionEvidence) != 1 || action.CompletionEvidence[0].ObservationID != "obs-001" {
		t.Fatalf("expected completion evidence IDs to expand, got %+v", action.CompletionEvidence)
	}
	if len(action.QualityReview) != 1 || len(action.QualityReview[0].Evidence) != 1 || action.QualityReview[0].Evidence[0].ObservationID != "obs-001" {
		t.Fatalf("expected quality review evidence IDs to expand, got %+v", action.QualityReview)
	}
}

func TestParseAgentActionResponseParsesToolCall(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"continue","toolName":"browser.open","toolInput":{"url":"https://example.com"}}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != "browser.open" {
		t.Fatalf("expected tool call action, got %+v", action)
	}
	if string(action.ToolInput) != `{"url":"https://example.com"}` {
		t.Fatalf("expected tool input to be preserved, got %s", string(action.ToolInput))
	}
}

func TestParseAgentActionResponseNormalizesContinueToolCall(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"continue","toolName":"browser.open","message":"opening it","toolInput":{"url":"https://example.com"}}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != "browser.open" || action.Message != "opening it" {
		t.Fatalf("expected continue action to normalize, got %+v", action)
	}
	if string(action.ToolInput) != `{"url":"https://example.com"}` {
		t.Fatalf("expected tool input to be preserved, got %s", string(action.ToolInput))
	}
}

func TestParseAgentActionResponseRejectsMalformedJSON(t *testing.T) {
	_, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":`})
	if errorValue == nil {
		t.Fatal("expected malformed JSON error")
	}
}

func TestApplyToolResultAppendsObservationDeterministically(t *testing.T) {
	state := agentTaskState{}
	result := ToolResult{
		Output: ToolOutput{Content: "attached"},
		Attachments: []FileAttachment{{
			DevicePath:  "/tmp/file.html",
			Filename:    "file.html",
			ContentType: "text/html",
		}},
	}

	nextState := applyToolResult(state, ToolInvocation{ToolName: "file.deliver", Input: json.RawMessage(`{"path":"file.html"}`)}, result)

	if len(nextState.Observations) != 1 {
		t.Fatalf("expected one observation, got %+v", nextState.Observations)
	}
	observation := nextState.Observations[0]
	if observation.ObservationID != "obs-001" || observation.Tool != "file.deliver" || observation.ContentText() != "attached" {
		t.Fatalf("unexpected observation: %+v", observation)
	}
	if len(nextState.Attachments) != 1 || nextState.Attachments[0].Filename != "file.html" {
		t.Fatalf("expected attachment to be appended, got %+v", nextState.Attachments)
	}
}

func TestAdvanceAgentTaskReturnsModelCallEffectByDefault(t *testing.T) {
	state := buildInitialAgentTaskState(AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "hello",
	}, TurnOptions{}, "task-1")

	transition := advanceAgentTask(state)

	if transition.Effect.Kind != agentEffectCallModel {
		t.Fatalf("expected model call effect, got %+v", transition.Effect)
	}
	if transition.Effect.ModelCall == nil {
		t.Fatal("expected model call request")
	}
}

func TestAdvanceAgentTaskReturnsAttachExistingArtifactEffect(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactPath := filepath.Join(workspaceRootPath, "report.html")
	if errorValue := os.WriteFile(artifactPath, []byte("<html></html>"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolSet := NewToolSet([]string{FileDeliverToolName})
	toolSet.RegisterTool(ToolDefinition{Name: FileDeliverToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("delivered"), nil
	})
	state := agentTaskState{
		Request: AgentTurnRequest{
			Prompt:                     "HTML 파일 만들어줘",
			ToolSet:                    toolSet,
			WorkspaceRootPath:          workspaceRootPath,
			RequiredEvidenceTools:      []string{FileDeliverToolName},
			RequiredAttachmentSuffixes: []string{".html"},
			TurnStartedAt:              time.Now().Add(-time.Second),
		},
		Requirements: []toolUseRequirement{{
			ToolName:           FileDeliverToolName,
			RequiresAttachment: true,
			AttachmentSuffixes: []string{".html"},
		}},
	}

	transition := advanceAgentTask(state)

	if transition.Effect.Kind != agentEffectContinue {
		t.Fatalf("expected file delivery effect, got %+v", transition.Effect)
	}
	if transition.Effect.ToolCall == nil || transition.Effect.ToolCall.ToolName != FileDeliverToolName {
		t.Fatalf("expected file.deliver tool call, got %+v", transition.Effect.ToolCall)
	}
	if !strings.Contains(string(transition.Effect.ToolCall.Input), artifactPath) {
		t.Fatalf("expected artifact path in tool input, got %s", string(transition.Effect.ToolCall.Input))
	}
}

func TestAdvanceAgentTaskReturnsFinishMessageEffectForSatisfiedBrowserOpen(t *testing.T) {
	state := agentTaskState{
		Request: AgentTurnRequest{Prompt: "open browser", TaskShape: TaskShapeBrowserHandoffTask, TaskLevel: TaskLevelXLow, ToolSet: newTestCapabilityToolSet([]string{"browser.open"})},
		Requirements: []toolUseRequirement{{
			ToolName: "browser.open",
		}},
		Observations: []turnObservation{{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "browser.open",
			Output:        ToolOutput{Content: "opened"},
		}},
	}

	transition := advanceAgentTask(state)

	if transition.Effect.Kind != agentEffectFinish {
		t.Fatalf("expected final reply effect, got %+v", transition.Effect)
	}
	if transition.Effect.Finish == nil {
		t.Fatalf("expected completion finish effect, got %+v", transition.Effect.Finish)
	}
}

func TestRestoreAgentTaskStateRestoresToolProgressOnly(t *testing.T) {
	events := []task.TaskEvent{{
		Name: "tool.browser.open.result",
		Body: `{"observationID":"obs-001","action":"continue","tool":"browser.open","content":"opened","isError":false}`,
	}}

	state, errorValue := restoreAgentTaskState(AgentTurnRequest{Prompt: "continue"}, TurnOptions{}, task.TaskRun{
		TaskRunID: "task-1",
		Status:    task.TaskStatusWaitingUserInput,
	}, events)

	if errorValue != nil {
		t.Fatalf("expected restored state: %v", errorValue)
	}
	if state.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected restored status, got %s", state.Status)
	}
	if len(state.Observations) != 1 || state.Observations[0].Tool != "browser.open" {
		t.Fatalf("expected restored observation, got %+v", state.Observations)
	}
}

func TestBlockedResumeRestoresPriorObservations(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "build site")
	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	taskRunService.AppendTaskEvent(runningTaskRun.TaskRunID, "tool.file.write.result", `{"observationID":"obs-001","action":"continue","tool":"file.write","content":"wrote app","isError":false}`)
	blockedTaskRun, errorValue := taskRunService.PauseTaskRun(runningTaskRun.TaskRunID, task.TaskStatusBlocked, "max_iterations")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	resumedTaskRun, errorValue := taskRunService.AdvanceTaskRun(blockedTaskRun.TaskRunID, "assistant")
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	state, errorValue := agentTaskStateForTurn(AgentTurnRequest{
		Prompt:                 "continue",
		ExistingTaskRunID:      resumedTaskRun.TaskRunID,
		IsRuntimeRestartResume: true,
	}, TurnOptions{}, resumedTaskRun, taskRunService.ListTaskEvent(resumedTaskRun.TaskRunID))

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(state.Observations) != 1 || state.Observations[0].Tool != "file.write" {
		t.Fatalf("expected prior file.write observation to be restored, got %+v", state.Observations)
	}
	if state.ToolCallCount != 1 {
		t.Fatalf("expected restored tool call count, got %d", state.ToolCallCount)
	}
}

func TestDecodeLegacyObservationNormalizesMemorySearchFailureCode(t *testing.T) {
	observation, errorValue := decodeTurnObservation([]byte(`{"observationID":"obs-001","action":"continue","tool":"memory.search","content":"memory failed","isError":true,"errorCode":"memory_search_unavailable","failureStage":"graphiti_search","message":"memory failed"}`))
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if !observation.Failed() || observation.FailureCode() != FailureCodes.Unavailable.String() {
		t.Fatalf("expected canonical memory search failure, got %+v", observation)
	}
}

func TestUserResumeClearsInheritedFailureDebt(t *testing.T) {
	observations := []turnObservation{
		{ObservationID: "obs-001", Action: "continue", Tool: "site.create", Output: ToolOutput{Content: `{"siteID":"site-1"}`}},
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          "site.publish",
			Failure:       &ToolFailure{Code: FailureCodes.OperationFailed.String()},
			ToolInputKey:  "site.publish\x00{\"siteID\":\"site-1\"}",
		},
	}
	if _, hasDebt := activeFailureDebt(observations); !hasDebt {
		t.Fatal("setup expected active failure debt before resume")
	}

	userResume := AgentTurnRequest{IsRuntimeRestartResume: true, IsApprovalContinuation: false}
	if !userResumeClearsInheritedFailureDebt(userResume, observations) {
		t.Fatal("expected user-driven resume to clear inherited failure debt")
	}
	approvalResume := AgentTurnRequest{IsRuntimeRestartResume: true, IsApprovalContinuation: true}
	if userResumeClearsInheritedFailureDebt(approvalResume, observations) {
		t.Fatal("expected approval continuation to retain failure debt")
	}
	autoStart := AgentTurnRequest{IsRuntimeRestartResume: false}
	if userResumeClearsInheritedFailureDebt(autoStart, observations) {
		t.Fatal("expected non-resume turn to be unaffected")
	}

	cleared := observationsWithoutFailures(observations)
	if _, hasDebt := activeFailureDebt(cleared); hasDebt {
		t.Fatal("expected failure debt cleared after dropping failed observations")
	}
	if len(cleared) != 1 || cleared[0].ObservationID != "obs-001" {
		t.Fatalf("expected successful observation retained, got %+v", cleared)
	}
}

func TestProducedSourcePathsRecoversSourceFilesFromDurableResults(t *testing.T) {
	events := []task.TaskEvent{
		toolResultTestEvent("tool.file.write.result", "obs-1", "file.write", `{"path":"tmp/deck/slides.html","sizeBytes":20}`, false),
		toolResultTestEvent("tool.file.edit.result", "obs-2", "file.edit", `{"editedFiles":["tmp/deck/slides.html","tmp/deck/DESIGN.md"]}`, false),
		toolResultTestEvent("tool.file.write.result", "obs-3", "file.write", `{"path":"tmp/deck/notes.md"}`, true),
	}
	paths := producedSourcePaths(events)
	if len(paths) != 2 || paths[0] != "tmp/deck/slides.html" || paths[1] != "tmp/deck/DESIGN.md" {
		t.Fatalf("expected deduped non-failed source paths, got %+v", paths)
	}
}
