package intake

import (
	"context"
	"errors"
	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/model"
)

func mustNormalizeTurn(t *testing.T, router TurnRouter, decision agentcontract.TurnDecision, request agentcontract.AgentRequest) agentcontract.TurnDecision {
	t.Helper()
	normalizedDecision, errorValue := router.normalizeDecision(decision, request)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return normalizedDecision
}

func mustPlanIntake(t *testing.T, planner TaskIntakePlanner, request agentcontract.AgentRequest) agentcontract.IntakeDecision {
	t.Helper()
	decision, errorValue := planner.Plan(context.Background(), request)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return decision
}

func mustPlanTurn(t *testing.T, router TurnRouter, request agentcontract.AgentRequest) agentcontract.TurnDecision {
	t.Helper()
	decision, errorValue := router.Plan(context.Background(), request)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return decision
}

func TestTurnRouterReturnsDisabledError(t *testing.T) {
	_, errorValue := NewTurnRouter(nil, agentcontract.IntakeOptions{}).Plan(context.Background(), agentcontract.AgentRequest{Prompt: "hello"})
	if !errors.Is(errorValue, ErrTurnRouterDisabled) {
		t.Fatalf("expected disabled error, got %v", errorValue)
	}
}

func TestTurnRouterPropagatesLanguageModelError(t *testing.T) {
	_, errorValue := NewTurnRouter(failingLanguageModel{}, agentcontract.IntakeOptions{IsEnabled: true}).Plan(context.Background(), agentcontract.AgentRequest{Prompt: "hello"})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "model failed") {
		t.Fatalf("expected typed router failure, got %v", errorValue)
	}
}

func TestTurnRouterCorrectsAuthoritativeStructuredOutputOnce(t *testing.T) {
	correctionError := turnRouterStructuredCorrectionError{
		message: "raw provider response must not enter the correction prompt",
		correction: model.StructuredOutputCorrection{
			Code: "structured_output_invalid",
			Diagnostic: model.StructuredOutputDiagnostic{
				Category: model.StructuredOutputDiagnosticSchemaValidation,
				ValidationIssues: []model.StructuredOutputValidationIssue{
					{FieldPath: "/expectedResults/0/start", Code: model.StructuredOutputValidationAdditionalProperty},
					{FieldPath: "/expectedResults/0/end", Code: model.StructuredOutputValidationAdditionalProperty},
					{FieldPath: "/expectedResults/0/userFacingReply", Code: model.StructuredOutputValidationAdditionalProperty},
				},
			},
		},
	}
	languageModel := &turnRouterCorrectionLanguageModel{
		errorsByCall: map[int]error{0: correctionError},
		contents: []string{
			"",
			`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":2,"initialToolNames":[],"reason":"create the requested file","userFacingReply":"","responseLanguage":"ko","priorTaskReference":"none"}`,
		},
	}
	router := NewTurnRouter(languageModel, agentcontract.IntakeOptions{IsEnabled: true})
	responseContext := context.WithValue(context.Background(), turnRouterCorrectionContextKey{}, "same-context")

	decision, errorValue := router.Plan(responseContext, agentcontract.AgentRequest{Prompt: "JSON 파일을 만들어줘"})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if decision.Route != agentcontract.TurnRouteStartTask || decision.TaskShape != agentcontract.TaskShapeMaintenanceTask {
		t.Fatalf("expected corrected router decision, got %+v", decision)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected exactly one correction, got %d calls", len(languageModel.requests))
	}
	firstRequest := languageModel.requests[0]
	correctionRequest := languageModel.requests[1]
	if firstRequest.StructuredOutputSchema != correctionRequest.StructuredOutputSchema {
		t.Fatal("expected correction to preserve the router schema")
	}
	if firstRequest.GenerationOptions.MaxTokens == nil || correctionRequest.GenerationOptions.MaxTokens == nil ||
		*firstRequest.GenerationOptions.MaxTokens != *correctionRequest.GenerationOptions.MaxTokens {
		t.Fatal("expected correction to preserve generation options")
	}
	if languageModel.contexts[0] != languageModel.contexts[1] {
		t.Fatal("expected correction to use the same response context")
	}
	if len(correctionRequest.Messages) != len(firstRequest.Messages)+1 {
		t.Fatal("expected one typed correction instruction")
	}
	correctionInstruction := correctionRequest.Messages[len(correctionRequest.Messages)-1].Content
	for _, expectedDiagnostic := range []string{
		"/expectedResults/0/start (additional_property)",
		"/expectedResults/0/end (additional_property)",
		"/expectedResults/0/userFacingReply (additional_property)",
	} {
		if !strings.Contains(correctionInstruction, expectedDiagnostic) {
			t.Fatalf("expected typed diagnostic %q, got %s", expectedDiagnostic, correctionInstruction)
		}
	}
	if strings.Contains(correctionInstruction, correctionError.message) {
		t.Fatal("expected correction to exclude the raw provider error")
	}
}

func TestTurnRouterBoundsStructuredOutputCorrection(t *testing.T) {
	firstError := newTurnRouterCorrectionError("first invalid response")
	finalError := newTurnRouterCorrectionError("second invalid response")
	languageModel := &turnRouterCorrectionLanguageModel{
		errorsByCall: map[int]error{0: firstError, 1: finalError},
		contents: []string{
			"",
			"",
			`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":2}`,
		},
	}

	_, errorValue := NewTurnRouter(languageModel, agentcontract.IntakeOptions{IsEnabled: true}).Plan(context.Background(), agentcontract.AgentRequest{Prompt: "파일을 만들어줘"})

	if errorValue == nil || !strings.Contains(errorValue.Error(), finalError.message) {
		t.Fatalf("expected final correction error, got %v", errorValue)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected exactly one correction attempt, got %d calls", len(languageModel.requests))
	}
}

func TestTurnRouterDoesNotRetryNonCorrectableStructuredOutput(t *testing.T) {
	nonCorrectableError := turnRouterStructuredCorrectionError{
		message: "serialization failed",
		correction: model.StructuredOutputCorrection{
			Code: "structured_output_invalid",
			Diagnostic: model.StructuredOutputDiagnostic{
				Category: model.StructuredOutputDiagnosticSerialization,
			},
		},
	}
	languageModel := &turnRouterCorrectionLanguageModel{errorsByCall: map[int]error{0: nonCorrectableError}}

	_, errorValue := NewTurnRouter(languageModel, agentcontract.IntakeOptions{IsEnabled: true}).Plan(context.Background(), agentcontract.AgentRequest{Prompt: "hello"})

	if errorValue == nil || !strings.Contains(errorValue.Error(), nonCorrectableError.message) {
		t.Fatalf("expected non-correctable error, got %v", errorValue)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected no correction, got %d calls", len(languageModel.requests))
	}
}

func TestTurnRouterDoesNotRetryCancellationOrDeadline(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			correctionError := newTurnRouterCorrectionError("generation stopped")
			correctionError.cause = cause
			languageModel := &turnRouterCorrectionLanguageModel{errorsByCall: map[int]error{0: correctionError}}

			_, errorValue := NewTurnRouter(languageModel, agentcontract.IntakeOptions{IsEnabled: true}).Plan(context.Background(), agentcontract.AgentRequest{Prompt: "hello"})

			if !errors.Is(errorValue, cause) {
				t.Fatalf("expected %v, got %v", cause, errorValue)
			}
			if len(languageModel.requests) != 1 {
				t.Fatalf("expected no correction, got %d calls", len(languageModel.requests))
			}
		})
	}
}

func TestTurnRouterPreservesUnsupportedArtifactDecision(t *testing.T) {
	decision := mustNormalizeTurn(t, NewTurnRouter(nil, agentcontract.IntakeOptions{}), agentcontract.TurnDecision{
		Route:                  agentcontract.TurnRouteGiveUp,
		Classification:         agentcontract.IntakeClassificationUnsupported,
		TaskShape:              agentcontract.TaskShapeImmediateReply,
		TaskLevel:              agentcontract.TaskLevelLow,
		EstimatedMinutes:       1,
		RequestedOutputFormats: []string{"pdf"},
		Reason:                 "unsupported",
		UserFacingReply:        "지원하지 않습니다.",
		PriorTaskReference:     agentcontract.PriorTaskReferenceNone,
	}, agentcontract.AgentRequest{Prompt: "PDF 만들어줘", AllowGiveUp: true, ToolSet: newTestToolSet([]string{"terminal_run", "file_deliver"})})

	if decision.Classification != agentcontract.IntakeClassificationUnsupported || decision.Route != agentcontract.TurnRouteGiveUp {
		t.Fatalf("expected router decision to remain authoritative, got %+v", decision)
	}
}

func TestTurnRouterRejectsInconsistentDecisionFields(t *testing.T) {
	validDecision := agentcontract.TurnDecision{
		Route:              agentcontract.TurnRouteStartTask,
		Classification:     agentcontract.IntakeClassificationBoundedTask,
		TaskShape:          agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:          agentcontract.TaskLevelLow,
		EstimatedMinutes:   1,
		ResponseLanguage:   "ko",
		PriorTaskReference: agentcontract.PriorTaskReferenceNone,
	}
	validDecision.Route = agentcontract.TurnRouteConsume
	_, errorValue := NewTurnRouter(nil, agentcontract.IntakeOptions{}).normalizeDecision(validDecision, agentcontract.AgentRequest{AllowGiveUp: true})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "bounded_task with a terminal route") {
		t.Fatalf("expected bounded terminal route error, got %v", errorValue)
	}
}

func TestTurnRouterCanonicalizesClassificationControlFields(t *testing.T) {
	testCases := []struct {
		classification agentcontract.IntakeClassification
		expectedRoute  agentcontract.TurnRoute
		expectedShape  agentcontract.TaskShape
	}{
		{classification: agentcontract.IntakeClassificationQuickReply, expectedRoute: agentcontract.TurnRouteConsume, expectedShape: agentcontract.TaskShapeImmediateReply},
		{classification: agentcontract.IntakeClassificationNeedsConfirmation, expectedRoute: agentcontract.TurnRouteClarify, expectedShape: agentcontract.TaskShapeApprovalGatedTask},
		{classification: agentcontract.IntakeClassificationUnsupported, expectedRoute: agentcontract.TurnRouteGiveUp, expectedShape: agentcontract.TaskShapeImmediateReply},
	}
	for _, testCase := range testCases {
		decision, errorValue := NewTurnRouter(nil, agentcontract.IntakeOptions{}).normalizeDecision(agentcontract.TurnDecision{
			Route:              agentcontract.TurnRouteConsume,
			Classification:     testCase.classification,
			TaskShape:          agentcontract.TaskShapeMaintenanceTask,
			TaskLevel:          agentcontract.TaskLevelLow,
			EstimatedMinutes:   1,
			ResponseLanguage:   "ko",
			PriorTaskReference: agentcontract.PriorTaskReferenceNone,
		}, agentcontract.AgentRequest{AllowGiveUp: true})
		if errorValue != nil {
			t.Fatalf("expected canonical decision for %s: %v", testCase.classification, errorValue)
		}
		if decision.Route != testCase.expectedRoute || decision.TaskShape != testCase.expectedShape {
			t.Fatalf("unexpected canonical decision for %s: %+v", testCase.classification, decision)
		}
	}
}

func TestTurnRouterRepairsBoundedImmediateReplyShape(t *testing.T) {
	decision := mustNormalizeTurn(t, NewTurnRouter(nil, agentcontract.IntakeOptions{}), agentcontract.TurnDecision{
		Route: agentcontract.TurnRouteStartTask, Classification: agentcontract.IntakeClassificationBoundedTask, TaskShape: agentcontract.TaskShapeImmediateReply,
		TaskLevel: agentcontract.TaskLevelLow, EstimatedMinutes: 1, ResponseLanguage: "ko",
	}, agentcontract.AgentRequest{})
	if decision.TaskShape != agentcontract.TaskShapeMaintenanceTask {
		t.Fatalf("expected maintenance task repair, got %+v", decision)
	}
}

func TestTurnRouterAcceptsCanonicalDecisionFields(t *testing.T) {
	testCases := []agentcontract.TurnDecision{
		{Route: agentcontract.TurnRouteStartTask, Classification: agentcontract.IntakeClassificationQuickReply, TaskShape: agentcontract.TaskShapeImmediateReply},
		{Route: agentcontract.TurnRouteConsume, Classification: agentcontract.IntakeClassificationQuickReply, TaskShape: agentcontract.TaskShapeImmediateReply},
		{Route: agentcontract.TurnRouteStartTask, Classification: agentcontract.IntakeClassificationBoundedTask, TaskShape: agentcontract.TaskShapeMaintenanceTask},
		{Route: agentcontract.TurnRouteClarify, Classification: agentcontract.IntakeClassificationNeedsConfirmation, TaskShape: agentcontract.TaskShapeApprovalGatedTask},
		{Route: agentcontract.TurnRouteGiveUp, Classification: agentcontract.IntakeClassificationUnsupported, TaskShape: agentcontract.TaskShapeImmediateReply},
	}
	for _, decision := range testCases {
		decision.TaskLevel = agentcontract.TaskLevelLow
		decision.EstimatedMinutes = 1
		decision.ResponseLanguage = "ko"
		decision.PriorTaskReference = agentcontract.PriorTaskReferenceNone
		if _, errorValue := NewTurnRouter(nil, agentcontract.IntakeOptions{}).normalizeDecision(decision, agentcontract.AgentRequest{}); errorValue != nil {
			t.Fatalf("expected canonical decision %+v to pass: %v", decision, errorValue)
		}
	}
}

func TestTurnRouterRejectsInvalidEstimatedMinutes(t *testing.T) {
	decision := agentcontract.TurnDecision{
		Route:              agentcontract.TurnRouteStartTask,
		Classification:     agentcontract.IntakeClassificationBoundedTask,
		TaskShape:          agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:          agentcontract.TaskLevelLow,
		EstimatedMinutes:   1,
		ResponseLanguage:   "ko",
		PriorTaskReference: agentcontract.PriorTaskReferenceNone,
	}
	for _, estimatedMinutes := range []int{-1, 0} {
		decision.EstimatedMinutes = estimatedMinutes
		_, errorValue := NewTurnRouter(nil, agentcontract.IntakeOptions{}).normalizeDecision(decision, agentcontract.AgentRequest{})
		if errorValue == nil || !strings.Contains(errorValue.Error(), "invalid estimated minutes") {
			t.Fatalf("expected invalid estimate error for %d, got %v", estimatedMinutes, errorValue)
		}
	}
}

func TestWithRestoredIntakeStateOverlaysOnlyControlFields(t *testing.T) {
	approvalSignal := agentcontract.ApprovalSignalApprove
	control := agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteContinueTask,
		Approval:         &approvalSignal,
		Classification:   agentcontract.IntakeClassificationBoundedTask,
		TaskShape:        agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:        agentcontract.TaskLevelLow,
		ResponseLanguage: "ko",
		Reason:           "interactive_confirm",
	}
	restored := control.WithRestoredIntakeState(agentcontract.IntakeDecision{
		Classification:   agentcontract.IntakeClassificationBoundedTask,
		TaskShape:        agentcontract.TaskShapeApprovalGatedTask,
		TaskLevel:        agentcontract.TaskLevelMedium,
		EstimatedMinutes: 7,
		ResponseLanguage: "en",
		Reason:           "original intake",
	})
	if restored.EstimatedMinutes != 7 || restored.TaskLevel != agentcontract.TaskLevelMedium || restored.TaskShape != agentcontract.TaskShapeApprovalGatedTask {
		t.Fatalf("expected persisted intake state restored, got %+v", restored)
	}
	if restored.Route != agentcontract.TurnRouteContinueTask || restored.Approval == nil || *restored.Approval != agentcontract.ApprovalSignalApprove {
		t.Fatalf("expected control route and approval preserved, got %+v", restored)
	}
	if restored.ResponseLanguage != "ko" || restored.Reason != "interactive_confirm" {
		t.Fatalf("expected control language and reason preserved, got %+v", restored)
	}

	unrestored := control.WithRestoredIntakeState(agentcontract.IntakeDecision{})
	if unrestored.EstimatedMinutes != 0 || unrestored.TaskLevel != agentcontract.TaskLevelLow {
		t.Fatalf("expected missing intake to leave control decision unchanged, got %+v", unrestored)
	}
}

func TestTaskIntakePlannerUsesStructuredModelDecision(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"bounded tool work","userFacingReply":""}`,
	}}
	toolRegistry := newTestToolSet([]string{"memory_search"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "memory_search"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{}, nil
	})
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{
		IsEnabled:        true,
		DefaultTaskLevel: agentcontract.TaskLevelLow,
	})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "search memory",
		ToolSet: toolRegistry,
	})

	if decision.Classification != agentcontract.IntakeClassificationBoundedTask {
		t.Fatalf("expected bounded task, got %q", decision.Classification)
	}
	if decision.TaskShape != agentcontract.TaskShapeResearchTask {
		t.Fatalf("expected research task shape, got %+v", decision)
	}
	if decision.TaskLevel != agentcontract.TaskLevelLow {
		t.Fatalf("expected selected task level, got %+v", decision)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected one intake model call, got %d", len(languageModel.requests))
	}
	if languageModel.requests[0].StructuredOutputSchema.Name != "blueclaw_turn_router" {
		t.Fatalf("expected turn router schema, got %q", languageModel.requests[0].StructuredOutputSchema.Name)
	}
	if languageModel.requests[0].GenerationOptions.MaxTokens == nil || *languageModel.requests[0].GenerationOptions.MaxTokens != turnRouterMaxTokens {
		t.Fatalf("expected bounded turn router output, got %+v", languageModel.requests[0].GenerationOptions)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"taskShape"`) {
		t.Fatalf("expected task shape in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"level"`) {
		t.Fatalf("expected task level in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"requestedOutputFormats"`) {
		t.Fatalf("expected requested output formats in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"json"`) {
		t.Fatalf("expected JSON in requested output formats, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"requiredEvidence"`) {
		t.Fatalf("expected no completion-evidence field in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"enum":["low","medium","high"]`) {
		t.Fatalf("expected task level enum in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"priorTaskReference"`) {
		t.Fatalf("expected prior task reference in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	deprecatedFieldName := `"work` + `Kinds"`
	if strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, deprecatedFieldName) {
		t.Fatalf("expected no deprecated routing field in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), `requestedOutputFormats should be ["html"], not ["html","pptx"]`) {
		t.Fatal("expected intake prompt to disambiguate html presentation requests from pptx file requests")
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "Prefer consume with reactionEmojiName for lightweight acknowledgement") {
		t.Fatal("expected intake prompt to prefer reactions over text emoji")
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "only mentions the assistant") {
		t.Fatal("expected intake prompt to guide bare assistant mentions")
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "Do not ignore jokes") {
		t.Fatal("expected intake prompt to guide playful addressed remarks")
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "Use maintenance_task or approval_gated_task only for work that changes state") {
		t.Fatal("expected intake prompt to distinguish mutations from reads and tool-free replies")
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "leave it null for reading, summarizing, searching, or analyzing an input attachment") {
		t.Fatal("expected intake prompt to separate input attachments from file deliverables")
	}
	if strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "requiredEvidence") {
		t.Fatal("expected intake prompt not to ask the router to predict completion evidence")
	}
}

func TestIntakeToolDescriptionsCoverReachableTools(t *testing.T) {
	toolRegistry := toolcontract.NewToolSet([]string{"file_deliver"})
	for _, toolDefinition := range []toolcontract.ToolDefinition{
		{Name: "calendar_add", Description: "Create a calendar event with a long operation description."},
		{Name: "file_deliver", Description: "Deliver a file to the requester."},
	} {
		definition := toolDefinition
		registerTestTool(toolRegistry, definition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}

	descriptions := intakeToolDescriptions(toolRegistry)

	if !strings.Contains(descriptions, "- file_deliver: Deliver a file to the requester.") {
		t.Fatalf("expected the callable tool description, got %q", descriptions)
	}
	if strings.Contains(descriptions, "Registered requiredEvidence names") {
		t.Fatalf("expected no completion-evidence name listing, got %q", descriptions)
	}
	if !strings.Contains(descriptions, "- calendar_add: Create a calendar event with a long operation description.") {
		t.Fatalf("expected reachable capability operations to carry descriptions at intake, got %q", descriptions)
	}
}

func TestTurnRouterBuildMessagesKeepsStablePrefixClockInvariantAndOrdersVolatileLast(t *testing.T) {
	toolRegistry := toolcontract.NewToolSet([]string{"calendar_list"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "calendar_list", Description: "List calendar events."}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("ok"), nil
	})
	turnRouter := NewTurnRouter(nil, agentcontract.IntakeOptions{IsEnabled: true})

	baseRequest := agentcontract.AgentRequest{
		Prompt:  "이번 주 회의 일정 알려줘",
		ToolSet: toolRegistry,
	}
	earlyRequest := baseRequest
	earlyRequest.TurnStartedAt = time.Date(2026, 5, 12, 8, 32, 27, 0, time.UTC)
	lateRequest := baseRequest
	lateRequest.TurnStartedAt = time.Date(2026, 5, 12, 21, 5, 59, 0, time.UTC)

	earlyMessages := turnRouter.buildMessages(earlyRequest)
	lateMessages := turnRouter.buildMessages(lateRequest)

	if len(earlyMessages) != len(lateMessages) {
		t.Fatalf("expected the same message count across different wall-clock times, got %d and %d", len(earlyMessages), len(lateMessages))
	}

	temporalIndex := -1
	for index, message := range earlyMessages {
		if strings.Contains(message.Content, "Runtime temporal context:") {
			temporalIndex = index
			break
		}
	}
	if temporalIndex < 0 {
		t.Fatalf("expected a temporal context message, got %+v", earlyMessages)
	}
	if temporalIndex != len(earlyMessages)-2 {
		t.Fatalf("expected the temporal context message to be the last system message before the user message, got index %d of %d messages", temporalIndex, len(earlyMessages))
	}
	if earlyMessages[len(earlyMessages)-1].Role != "user" {
		t.Fatalf("expected the final message to be the user message, got %+v", earlyMessages[len(earlyMessages)-1])
	}

	for index := 0; index < temporalIndex; index++ {
		if earlyMessages[index].Content != lateMessages[index].Content {
			t.Fatalf("expected stable prefix message %d to be clock invariant, got %q vs %q", index, earlyMessages[index].Content, lateMessages[index].Content)
		}
	}

	toolDescriptionIndex := -1
	for index, message := range earlyMessages {
		if strings.Contains(message.Content, "Available tools:") {
			toolDescriptionIndex = index
			break
		}
	}
	if toolDescriptionIndex < 0 || toolDescriptionIndex >= temporalIndex {
		t.Fatalf("expected tool descriptions before the volatile temporal context, got tool description index %d and temporal index %d", toolDescriptionIndex, temporalIndex)
	}
}

func TestTaskIntakePlannerExplainsTaskRecordSemantics(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"expectedResults":[],"responseLanguage":"ko","reason":"add the requested task record","userFacingReply":"","initialToolNames":["task_add"],"priorTaskReference":"none"}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "금요일까지 결산 자료 누락 항목 확인 업무를 추가해줘",
		ToolSet: newTestCapabilityToolSet([]string{"task_add"}),
	})

	if decision.Classification != agentcontract.IntakeClassificationBoundedTask {
		t.Fatalf("expected bounded task record management, got %+v", decision)
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), taskRecordRoutingInstruction) {
		t.Fatal("expected task record routing instruction")
	}
}

func TestTurnRouterNormalizesSideEffectEvidenceIntoExecutableWork(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"answer_question","classification":"quick_reply","taskShape":"immediate_reply","level":"low","estimatedMinutes":1,"requestedOutputFormats":[],"expectedResults":[],"responseLanguage":"ko","reason":"add the task","userFacingReply":"","initialToolNames":["task_add"],"priorTaskReference":"none"}`,
	}}
	turnRouter := NewTurnRouter(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{
		Prompt:  "분기 결산 확인 업무를 추가해줘",
		ToolSet: newTestCapabilityToolSet([]string{"task_add", "file_read"}),
	})

	if errorValue != nil {
		t.Fatalf("expected normalized executable decision: %v", errorValue)
	}
	if decision.Route != agentcontract.TurnRouteStartTask || decision.Classification != agentcontract.IntakeClassificationBoundedTask || decision.TaskShape != agentcontract.TaskShapeMaintenanceTask {
		t.Fatalf("expected a predicted side-effect tool to define executable work, got %+v", decision)
	}
}

func TestTurnRouterDropsHTMLFormatWhenSiteToolsAreSuggested(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"medium","estimatedMinutes":20,"requestedOutputFormats":["html"],"expectedResults":[],"responseLanguage":"ko","reason":"create the site","userFacingReply":"","initialToolNames":["site_serve","file_edit"],"priorTaskReference":"none"}`,
	}}
	turnRouter := NewTurnRouter(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{
		Prompt:  "상담 안내 웹사이트를 초안으로 만들어줘",
		ToolSet: newTestCapabilityToolSet([]string{"site_serve", "file_edit"}),
	})

	if errorValue != nil {
		t.Fatalf("expected normalized site decision: %v", errorValue)
	}
	if len(decision.RequestedOutputFormats) != 0 {
		t.Fatalf("expected the site deliverable to drop the html file format, got %+v", decision.RequestedOutputFormats)
	}
}

func TestTurnRouterKeepsReadOnlyEvidenceAsQuickReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"answer_question","classification":"quick_reply","taskShape":"immediate_reply","level":"low","estimatedMinutes":1,"requestedOutputFormats":[],"expectedResults":[],"responseLanguage":"ko","reason":"list tasks","userFacingReply":"","initialToolNames":["task_list"],"priorTaskReference":"none"}`,
	}}
	turnRouter := NewTurnRouter(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{
		Prompt:  "내 업무 목록 보여줘",
		ToolSet: newTestCapabilityToolSet([]string{"task_list"}),
	})

	if errorValue != nil {
		t.Fatalf("expected read-only decision: %v", errorValue)
	}
	if decision.Route != agentcontract.TurnRouteAnswerQuestion || decision.Classification != agentcontract.IntakeClassificationQuickReply || decision.TaskShape != agentcontract.TaskShapeImmediateReply {
		t.Fatalf("expected read-only evidence to preserve quick reply, got %+v", decision)
	}
}

func TestTurnRouterIgnoresUnregisteredSideEffectSuffix(t *testing.T) {
	decision := mustNormalizeTurn(t, TurnRouter{}, agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteAnswerQuestion,
		Classification:   agentcontract.IntakeClassificationQuickReply,
		TaskShape:        agentcontract.TaskShapeImmediateReply,
		TaskLevel:        agentcontract.TaskLevelLow,
		EstimatedMinutes: 1,
		InitialToolNames: []string{"fake.delete"},
	}, agentcontract.AgentRequest{ToolSet: newTestCapabilityToolSet([]string{"task_list"})})

	if decision.Route != agentcontract.TurnRouteAnswerQuestion || decision.Classification != agentcontract.IntakeClassificationQuickReply {
		t.Fatalf("expected unregistered predicted tool to remain non-executable, got %+v", decision)
	}
}

func TestTaskIntakePlannerReviewsExecutableTaskClarification(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"clarify","classification":"needs_confirmation","taskShape":"approval_gated_task","level":"low","estimatedMinutes":10,"requestedOutputFormats":[],"expectedResults":[{"id":"result-1","type":"message","description":"작업 추가 완료 메시지","required":true}],"responseLanguage":"ko","reason":"작업 목록을 먼저 확인해야 합니다.","userFacingReply":"혹시 이미 작업 목록에 있는 작업인가요?","initialToolNames":["file_write"],"priorTaskReference":"none","clarificationQuestion":"이미 등록된 작업인가요?"}`,
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":10,"requestedOutputFormats":[],"expectedResults":[{"id":"result-1","type":"message","description":"작업 추가 완료 메시지","required":true}],"responseLanguage":"ko","reason":"업무 기록을 바로 추가할 수 있습니다.","userFacingReply":"","initialToolNames":["task_add"],"priorTaskReference":"none"}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "이번 주 금요일까지 고객지원 분기 결산 누락 항목 확인 업무를 추가해줘",
		ToolSet: newTestCapabilityToolSet([]string{"task_add", "task_list", "file_write"}),
	})

	if decision.Classification != agentcontract.IntakeClassificationBoundedTask {
		t.Fatalf("expected reviewed task to be executable, got %+v", decision)
	}
	if len(languageModel.requests) != 2 || !strings.Contains(joinMessageContent(languageModel.requests[1].Messages), clarificationReviewInstruction) {
		t.Fatalf("expected one LLM clarification review, got %d calls", len(languageModel.requests))
	}
}

func TestTaskIntakePlannerPreservesClarificationWhenReviewFails(t *testing.T) {
	languageModel := &clarificationReviewFailureLanguageModel{content: `{"route":"clarify","classification":"needs_confirmation","taskShape":"approval_gated_task","level":"low","estimatedMinutes":2,"requestedOutputFormats":[],"expectedResults":[],"responseLanguage":"ko","reason":"수정할 업무가 여러 개일 수 있습니다.","userFacingReply":"어떤 업무를 수정할까요?","initialToolNames":["task_update"],"priorTaskReference":"none","clarificationQuestion":"어떤 업무를 수정할까요?"}`}
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "업무를 수정해줘",
		ToolSet: newTestCapabilityToolSet([]string{"task_update"}),
	})

	if decision.Classification != agentcontract.IntakeClassificationNeedsConfirmation || decision.ClarificationQuestion != "어떤 업무를 수정할까요?" {
		t.Fatalf("expected valid clarification fallback, got %+v", decision)
	}
	if languageModel.callCount != 2 {
		t.Fatalf("expected one failed review, got %d calls", languageModel.callCount)
	}
}

func TestTaskIntakePlannerPassesPriorTaskContext(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":["docx"],"reason":"deliver prior file","userFacingReply":"","priorTaskReference":"outcome_recovery"}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt: "전달해줘야지 그럼",
		PriorTask: agentcontract.PriorTaskContext{
			TaskRunID:              "88894f",
			Prompt:                 "기업 문서 가이드를 워드 파일로 만들어줘",
			RequestedOutputFormats: []string{"docx"},
			OutcomeContract: agentcontract.OutcomeContract{
				RequiredEvidenceTools:      []string{"file_deliver"},
				RequiredAttachmentSuffixes: []string{".docx"},
				ArtifactRequirement:        agentcontract.ArtifactRequirementRequired,
			},
		},
	})

	if decision.PriorTaskReference != agentcontract.PriorTaskReferenceOutcomeRecovery {
		t.Fatalf("expected prior task outcome recovery, got %+v", decision)
	}
	messageContent := joinedMessageContent(languageModel.requests[0].Messages)
	if !strings.Contains(messageContent, "Prior task context") || !strings.Contains(messageContent, "88894f") {
		t.Fatalf("expected prior task context in router messages, got %s", messageContent)
	}
	if !strings.Contains(messageContent, "not permission to finish from old text") {
		t.Fatalf("expected prior task context to forbid stale finish reuse, got %s", messageContent)
	}
}

func TestTaskIntakePlannerFallbackDoesNotInferPriorTaskIntent(t *testing.T) {
	planner := NewTaskIntakePlanner(nil, agentcontract.IntakeOptions{})
	_, errorValue := planner.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "링크로 전달된 적 없어. 첨부파일로 줘야지 그리고."})
	if !errors.Is(errorValue, ErrTurnRouterDisabled) {
		t.Fatalf("expected disabled router error, got %v", errorValue)
	}
}

func TestTaskIntakePlannerKeepsTypedFileContract(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":["html"],"expectedResults":[{"id":"attached-file","type":"file","description":"attach a file","required":true}],"responseLanguage":"ko","reason":"calendar update","userFacingReply":"","initialToolNames":["calendar_update","file_deliver"],"priorTaskReference":"none"}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})
	toolRegistry := newTestCapabilityToolSet([]string{"calendar_update", "file_deliver"})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{Prompt: "일정을 오후 2시로 수정해줘", ToolSet: toolRegistry})

	if !expectedResultIncludesType(agentcontract.OutcomeContract{ExpectedResults: decision.ExpectedResults}, agentcontract.ExpectedResultTypeFile) {
		t.Fatalf("expected typed file result to remain, got %+v", decision.ExpectedResults)
	}
	if !containsString(decision.InitialToolNames, toolcontract.FileDeliverToolName) {
		t.Fatalf("expected typed file delivery tool to remain in initial tools, got initial=%+v", decision.InitialToolNames)
	}
}

func TestTaskIntakePlannerKeepsGroundedRequestedOutputFormat(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":["html"],"expectedResults":[{"id":"attached-file","type":"file","description":"attach HTML","required":true}],"responseLanguage":"ko","reason":"presentation","userFacingReply":"","initialToolNames":["file_deliver"],"priorTaskReference":"none"}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})
	toolRegistry := newTestToolSet([]string{toolcontract.FileDeliverToolName})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{Prompt: "HTML 발표자료를 만들어줘", ToolSet: toolRegistry})

	if strings.Join(decision.RequestedOutputFormats, ",") != "html" || !expectedResultIncludesType(agentcontract.OutcomeContract{ExpectedResults: decision.ExpectedResults}, agentcontract.ExpectedResultTypeFile) {
		t.Fatalf("expected grounded HTML contract to remain, got %+v", decision)
	}
}

func TestTaskIntakePlannerFallbackDoesNotTreatInputAttachmentExtensionAsOutput(t *testing.T) {
	planner := NewTaskIntakePlanner(nil, agentcontract.IntakeOptions{})
	_, errorValue := planner.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "첨부한 report.pdf 요약 작성해줘"})
	if !errors.Is(errorValue, ErrTurnRouterDisabled) {
		t.Fatalf("expected disabled router error, got %v", errorValue)
	}
}

func TestTaskIntakePlannerFallbackDoesNotInferDomainEvidence(t *testing.T) {
	planner := NewTaskIntakePlanner(nil, agentcontract.IntakeOptions{})
	_, errorValue := planner.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "업무 등록해줘"})
	if !errors.Is(errorValue, ErrTurnRouterDisabled) {
		t.Fatalf("expected disabled router error, got %v", errorValue)
	}
}

func TestTaskIntakePlannerKeepsStructuredOutputFormats(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":["html"],"reason":"explicit html output","userFacingReply":""}`,
	}}
	toolRegistry := newTestToolSet([]string{"terminal_run", "file_deliver"})
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "html만 주면 돼",
		ToolSet: toolRegistry,
	})

	if strings.Join(decision.RequestedOutputFormats, ",") != "html" {
		t.Fatalf("expected structured html output format, got %+v", decision.RequestedOutputFormats)
	}
	if !hasArtifactOutputFormat(decision.RequestedOutputFormats) {
		t.Fatalf("expected requested output formats to imply file artifact work, got %+v", decision.RequestedOutputFormats)
	}
}

func TestTaskIntakePlannerKeepsJSONOutputFormat(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":["json"],"expectedResults":[{"id":"memo","type":"file","description":"JSON memo","required":true}],"responseLanguage":"ko","reason":"create and deliver a JSON memo","userFacingReply":"","initialToolNames":["file_write","file_deliver"],"priorTaskReference":"none"}`,
	}}
	toolRegistry := newTestToolSet([]string{"file_write", "file_deliver"})
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "고객지원 FAQ 개편 작업용 JSON 메모를 만들어줘",
		ToolSet: toolRegistry,
	})

	if !slices.Equal(decision.RequestedOutputFormats, []string{"json"}) {
		t.Fatalf("expected JSON output format, got %+v", decision.RequestedOutputFormats)
	}
	if !hasArtifactOutputFormat(decision.RequestedOutputFormats) {
		t.Fatalf("expected JSON to be an artifact output format, got %+v", decision.RequestedOutputFormats)
	}
	if !slices.Equal(attachmentSuffixesForRequestedOutputFormats(decision.RequestedOutputFormats), []string{".json"}) {
		t.Fatalf("expected JSON attachment suffix, got %+v", attachmentSuffixesForRequestedOutputFormats(decision.RequestedOutputFormats))
	}
}

func TestTaskIntakePlannerUsesStructuredArtifactEnumForFileDelivery(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"give_up","classification":"unsupported","taskShape":"immediate_reply","level":"medium","estimatedMinutes":1,"requestedOutputFormats":["pdf"],"responseLanguage":"ko","reason":"mistaken unsupported file artifact","userFacingReply":"PDF 생성은 지원하지 않습니다.","priorTaskReference":"none"}`,
	}}
	toolRegistry := newTestToolSet([]string{"conversation_history", "file_read", "file_write", "terminal_run", "file.promote", "file_deliver"})
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "제안서를 PDF 파일로 만들어줘",
		ToolSet: toolRegistry,
	})

	if decision.Classification != agentcontract.IntakeClassificationUnsupported {
		t.Fatalf("expected unsupported router classification to remain authoritative, got %+v", decision)
	}
	if !hasArtifactOutputFormat(decision.RequestedOutputFormats) {
		t.Fatalf("expected file artifact work, got %+v", decision)
	}
	if strings.Join(decision.RequestedOutputFormats, ",") != "pdf" {
		t.Fatalf("expected pdf output format, got %+v", decision.RequestedOutputFormats)
	}
	if len(decision.InitialToolNames) != 0 {
		t.Fatalf("expected no inferred file delivery tools, got %+v", decision.InitialToolNames)
	}
	if decision.UserFacingReply != "PDF 생성은 지원하지 않습니다." {
		t.Fatalf("expected router reply to remain unchanged, got %q", decision.UserFacingReply)
	}
}

func TestTaskIntakePlannerPreservesTypedOutputFormatsAndExpectedResults(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":["pdf"],"expectedResults":[{"id":"result-1","type":"file","description":"PDF document","required":true},{"id":"site-public-link","type":"link","description":"public URL","required":true}],"responseLanguage":"ko","reason":"conflicted artifact kind","userFacingReply":"","initialToolNames":["site_list"],"priorTaskReference":"none"}`,
	}}
	toolRegistry := newTestToolSet([]string{"conversation_history", "file_read", "file_write", "terminal_run", "file.promote", "file_deliver", "site_list"})
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "제공한 데이터만 기반으로 제안서를 PDF 파일로 만들어서 첨부해줘",
		ToolSet: toolRegistry,
	})

	if !hasArtifactOutputFormat(decision.RequestedOutputFormats) {
		t.Fatalf("expected requested output formats to imply file artifact work, got %+v", decision.RequestedOutputFormats)
	}
	if len(decision.ExpectedResults) != 2 || !expectedResultsContain(decision.ExpectedResults, agentcontract.ExpectedResultTypeFile, "PDF document") || !expectedResultsContain(decision.ExpectedResults, agentcontract.ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected typed expected results to remain, got %+v", decision.ExpectedResults)
	}
	if !slices.Contains(decision.InitialToolNames, "site_list") {
		t.Fatalf("expected typed initial site tool to remain, got %+v", decision.InitialToolNames)
	}
}

func TestTurnRouterSchemaUsesContextDependentPendingFields(t *testing.T) {
	toolSet := newTestCapabilityToolSet([]string{"task_add", "task_update"})
	noPendingSchema := turnRouterSchema(agentcontract.AgentRequest{ToolSet: toolSet})
	if strings.Contains(noPendingSchema, `"approval"`) {
		t.Fatalf("expected no approval field without pending confirmation, got %s", noPendingSchema)
	}
	if strings.Contains(noPendingSchema, `"choices"`) {
		t.Fatalf("expected no choices field without pending choice, got %s", noPendingSchema)
	}
	if !strings.Contains(noPendingSchema, `"clarificationQuestion"`) || !strings.Contains(noPendingSchema, `"clarificationOptions"`) {
		t.Fatalf("expected optional clarify fields in base schema, got %s", noPendingSchema)
	}
	for _, expectedEmojiName := range []string{`"reactionEmojiName"`, `"white_check_mark"`, `"+1"`, `"tada"`, `"rocket"`, `"ok_hand"`, `"hourglass_flowing_sand"`, `"sparkles"`, `"wave"`} {
		if !strings.Contains(noPendingSchema, expectedEmojiName) {
			t.Fatalf("expected reaction emoji enum value %s in schema, got %s", expectedEmojiName, noPendingSchema)
		}
	}
	if strings.Contains(noPendingSchema, `"uniqueItems"`) {
		t.Fatalf("expected provider-portable array schemas, got %s", noPendingSchema)
	}
	if strings.Count(noPendingSchema, `"maxItems"`) < 4 {
		t.Fatalf("expected bounded router arrays, got %s", noPendingSchema)
	}
	for _, toolName := range []string{`"task_add"`, `"task_update"`} {
		if !strings.Contains(noPendingSchema, toolName) {
			t.Fatalf("expected registered tool enum value %s in schema, got %s", toolName, noPendingSchema)
		}
	}

	pendingSchema := turnRouterSchema(agentcontract.AgentRequest{
		PendingConfirmation: agentcontract.PendingConfirmationContext{TaskRunID: "task-1"},
		PendingChoice: agentcontract.PendingChoiceContext{
			TaskRunID: "task-2",
			Options:   []agentcontract.ChoiceReplyOption{{Key: "A", Label: "Option A"}},
		},
		AllowGiveUp: true,
	})
	for _, expected := range []string{`"approval"`, `"choices"`, `"give_up"`, `"A"`, `"1"`} {
		if !strings.Contains(pendingSchema, expected) {
			t.Fatalf("expected %s in pending schema, got %s", expected, pendingSchema)
		}
	}
	if strings.Contains(pendingSchema, `"uniqueItems"`) {
		t.Fatalf("expected provider-portable pending schemas, got %s", pendingSchema)
	}
}

func TestTurnRoutingContextTreatsDelegatedPendingInputAsAnswer(t *testing.T) {
	description := turnRoutingContextDescription(agentcontract.AgentRequest{
		PendingInput: agentcontract.PendingInputContext{
			TaskRunID: "task-1",
			Question:  "제목과 섹션을 어떻게 구성할까요?",
		},
	})

	if !strings.Contains(description, "delegate the missing choice back to the assistant") {
		t.Fatalf("expected pending input delegation guidance, got %q", description)
	}
	if !strings.Contains(description, "do not ask the same question again") {
		t.Fatalf("expected repeated ask guidance, got %q", description)
	}
}

func TestTurnRouterNormalizesClarificationFields(t *testing.T) {
	router := NewTurnRouter(nil, agentcontract.IntakeOptions{IsEnabled: false})
	decision := mustNormalizeTurn(t, router, agentcontract.TurnDecision{
		Route:                 agentcontract.TurnRouteClarify,
		Classification:        agentcontract.IntakeClassificationNeedsConfirmation,
		TaskShape:             agentcontract.TaskShapeApprovalGatedTask,
		TaskLevel:             agentcontract.TaskLevelXLow,
		EstimatedMinutes:      1,
		ResponseLanguage:      "ko",
		Reason:                "needs finite choice",
		ClarificationQuestion: " 어느 방식으로 진행할까요? ",
		ClarificationOptions: []agentcontract.ClarificationOption{
			{Key: "A", Label: "A안", Value: "first"},
			{Key: "A", Label: "duplicate"},
			{Label: "B안", Value: "second"},
			{Key: "C", Label: ""},
		},
	}, agentcontract.AgentRequest{})

	if decision.Classification != agentcontract.IntakeClassificationNeedsConfirmation {
		t.Fatalf("expected router classification to remain unchanged, got %+v", decision)
	}
	if decision.UserFacingReply != "" {
		t.Fatalf("expected router reply to remain unchanged, got %q", decision.UserFacingReply)
	}
	if len(decision.ClarificationOptions) != 2 {
		t.Fatalf("expected two valid unique options, got %+v", decision.ClarificationOptions)
	}
	if decision.ClarificationOptions[0].Key != "A" || decision.ClarificationOptions[1].Key == "" {
		t.Fatalf("unexpected normalized options: %+v", decision.ClarificationOptions)
	}
}

func TestTurnRouterNormalizesReactionEmojiNameToEnum(t *testing.T) {
	router := NewTurnRouter(nil, agentcontract.IntakeOptions{IsEnabled: false})
	nullDecision := mustNormalizeTurn(t, router, agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteConsume,
		Classification:   agentcontract.IntakeClassificationQuickReply,
		TaskShape:        agentcontract.TaskShapeImmediateReply,
		TaskLevel:        agentcontract.TaskLevelXLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "ack",
	}, agentcontract.AgentRequest{})
	validDecision := mustNormalizeTurn(t, router, agentcontract.TurnDecision{
		Route:             agentcontract.TurnRouteConsume,
		Classification:    agentcontract.IntakeClassificationQuickReply,
		TaskShape:         agentcontract.TaskShapeImmediateReply,
		TaskLevel:         agentcontract.TaskLevelXLow,
		EstimatedMinutes:  1,
		ResponseLanguage:  "ko",
		Reason:            "ack",
		ReactionEmojiName: ":TADA:",
	}, agentcontract.AgentRequest{})
	invalidDecision := mustNormalizeTurn(t, router, agentcontract.TurnDecision{
		Route:             agentcontract.TurnRouteConsume,
		Classification:    agentcontract.IntakeClassificationQuickReply,
		TaskShape:         agentcontract.TaskShapeImmediateReply,
		TaskLevel:         agentcontract.TaskLevelXLow,
		EstimatedMinutes:  1,
		ResponseLanguage:  "ko",
		Reason:            "ack",
		ReactionEmojiName: "unknown_custom_emoji",
	}, agentcontract.AgentRequest{})

	if nullDecision.ReactionEmojiName != agentcontract.DefaultReactionEmojiName {
		t.Fatalf("expected missing emoji to default, got %q", nullDecision.ReactionEmojiName)
	}
	if validDecision.ReactionEmojiName != "tada" {
		t.Fatalf("expected valid emoji to normalize, got %q", validDecision.ReactionEmojiName)
	}
	if invalidDecision.ReactionEmojiName != agentcontract.DefaultReactionEmojiName {
		t.Fatalf("expected invalid emoji to default, got %q", invalidDecision.ReactionEmojiName)
	}
	if nullDecision.Route != agentcontract.TurnRouteConsume {
		t.Fatalf("expected lightweight consume route to stay consume, got %q", nullDecision.Route)
	}
}

func TestTurnRouterRequiresDirectMessageConsumeFallback(t *testing.T) {
	router := NewTurnRouter(nil, agentcontract.IntakeOptions{IsEnabled: false})
	request := agentcontract.AgentRequest{ConversationType: "D"}
	missingFallback := mustNormalizeTurn(t, router, agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteConsume,
		Classification:   agentcontract.IntakeClassificationQuickReply,
		TaskShape:        agentcontract.TaskShapeImmediateReply,
		TaskLevel:        agentcontract.TaskLevelXLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "ack",
	}, request)
	withFallback := mustNormalizeTurn(t, router, agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteConsume,
		Classification:   agentcontract.IntakeClassificationQuickReply,
		TaskShape:        agentcontract.TaskShapeImmediateReply,
		TaskLevel:        agentcontract.TaskLevelXLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "ack",
		UserFacingReply:  "알겠습니다.",
	}, request)

	if missingFallback.Route != agentcontract.TurnRouteConsume {
		t.Fatalf("expected router consume route to remain unchanged, got %+v", missingFallback)
	}
	if withFallback.Route != agentcontract.TurnRouteConsume || withFallback.UserFacingReply != "알겠습니다." {
		t.Fatalf("expected direct consume with fallback to remain consume, got %+v", withFallback)
	}
}

func TestTurnRouterRejectsTaskfulConsumeRoute(t *testing.T) {
	router := NewTurnRouter(nil, agentcontract.IntakeOptions{IsEnabled: false})
	toolSet := newTestToolSet([]string{"task_add", "task_list", "task_update"})
	_, errorValue := router.normalizeDecision(agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteConsume,
		Classification:   agentcontract.IntakeClassificationBoundedTask,
		TaskShape:        agentcontract.TaskShapeResearchTask,
		TaskLevel:        agentcontract.TaskLevelLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "사용자가 명시적으로 업무 등록을 요청함",
		InitialToolNames: []string{"task_add", "task_list", "task_update"},
	}, agentcontract.AgentRequest{
		Prompt:  "업무 등록해줘.\n\n- 메일 페이지 앱 비밀번호 개선",
		ToolSet: toolSet,
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "bounded_task with a terminal route") {
		t.Fatalf("expected contradictory consume error, got %v", errorValue)
	}
}

func TestTurnRouterRejectsBoundedGiveUp(t *testing.T) {
	router := NewTurnRouter(nil, agentcontract.IntakeOptions{IsEnabled: false})
	_, errorValue := router.normalizeDecision(agentcontract.TurnDecision{
		Route:                  agentcontract.TurnRouteGiveUp,
		Classification:         agentcontract.IntakeClassificationBoundedTask,
		TaskShape:              agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:              agentcontract.TaskLevelLow,
		EstimatedMinutes:       1,
		RequestedOutputFormats: []string{"pptx"},
		ResponseLanguage:       "ko",
		Choices:                []string{"B", "A", "A"},
	}, agentcontract.AgentRequest{
		PendingConfirmation: agentcontract.PendingConfirmationContext{TaskRunID: "task-1"},
		PendingChoice: agentcontract.PendingChoiceContext{
			TaskRunID:     "task-2",
			SelectionMode: "multiple",
			Options: []agentcontract.ChoiceReplyOption{
				{Key: "A", Label: "Option A"},
			},
		},
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "bounded_task with a terminal route") {
		t.Fatalf("expected bounded give_up error, got %v", errorValue)
	}
}

func TestTurnRouterNormalizesChoiceNumberToOptionKey(t *testing.T) {
	router := NewTurnRouter(nil, agentcontract.IntakeOptions{IsEnabled: false})
	decision := mustNormalizeTurn(t, router, agentcontract.TurnDecision{
		Route:                  agentcontract.TurnRouteContinueTask,
		Classification:         agentcontract.IntakeClassificationBoundedTask,
		TaskShape:              agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:              agentcontract.TaskLevelLow,
		EstimatedMinutes:       1,
		RequestedOutputFormats: nil,
		ResponseLanguage:       "ko",
		Choices:                []string{"2"},
	}, agentcontract.AgentRequest{
		PendingInput: agentcontract.PendingInputContext{
			TaskRunID:     "task-2",
			SelectionMode: "single",
			Options: []agentcontract.ChoiceReplyOption{
				{Key: "1", Label: "첫 번째"},
				{Key: "2", Label: "두 번째"},
			},
		},
	})

	if strings.Join(decision.Choices, ",") != "2" {
		t.Fatalf("expected numbered choice to resolve to key 2, got %+v", decision.Choices)
	}
}

func TestTurnRouterApproveForcesContinuation(t *testing.T) {
	approval := agentcontract.ApprovalSignalApprove
	router := NewTurnRouter(nil, agentcontract.IntakeOptions{IsEnabled: false})
	decision := mustNormalizeTurn(t, router, agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteStartTask,
		Classification:   agentcontract.IntakeClassificationBoundedTask,
		TaskShape:        agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:        agentcontract.TaskLevelLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "approval",
		Approval:         &approval,
	}, agentcontract.AgentRequest{
		PendingConfirmation: agentcontract.PendingConfirmationContext{TaskRunID: "task-1"},
	})

	if decision.Route != agentcontract.TurnRouteContinueTask {
		t.Fatalf("expected approval to force continuation, got %+v", decision)
	}
}

func TestTaskIntakePlannerReturnsLanguageModelError(t *testing.T) {
	planner := NewTaskIntakePlanner(failingLanguageModel{}, agentcontract.IntakeOptions{IsEnabled: true})
	_, errorValue := planner.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "please analyze the whole repo"})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "model failed") {
		t.Fatalf("expected language model error, got %v", errorValue)
	}
}

func TestTaskIntakePlannerReturnsLanguageModelErrorWithActiveTask(t *testing.T) {
	router := NewTurnRouter(failingLanguageModel{}, agentcontract.IntakeOptions{IsEnabled: true})
	_, errorValue := router.Plan(context.Background(), agentcontract.AgentRequest{
		Prompt:     "아니야 하지마",
		ActiveTask: agentcontract.ActiveTaskContext{TaskRunID: "task-1", Prompt: "report 만들어줘"},
	})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "model failed") {
		t.Fatalf("expected language model error, got %v", errorValue)
	}
}

func TestTaskIntakePlannerReturnsLanguageModelErrorWithoutActiveTask(t *testing.T) {
	router := NewTurnRouter(failingLanguageModel{}, agentcontract.IntakeOptions{IsEnabled: true})
	_, errorValue := router.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "please analyze the whole repo"})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "model failed") {
		t.Fatalf("expected language model error, got %v", errorValue)
	}
}

func TestTaskIntakePlannerDoesNotInferTaskLevelAfterLanguageModelError(t *testing.T) {
	planner := NewTaskIntakePlanner(failingLanguageModel{}, agentcontract.IntakeOptions{IsEnabled: true})
	decision, errorValue := planner.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "please search memory"})
	if errorValue == nil || decision.TaskLevel != "" {
		t.Fatalf("expected error without inferred task level, got decision=%+v error=%v", decision, errorValue)
	}
}

func TestTaskIntakePlannerClampsBrowserControlEffort(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"browser_handoff_task","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"browser control","userFacingReply":""}`,
	}}
	toolRegistry := newTestToolSet([]string{"browser_open", "browser_screenshot"})
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "open google and take a screenshot",
		ToolSet: toolRegistry,
	})

	if decision.TaskLevel != agentcontract.TaskLevelXLow {
		t.Fatalf("expected router task level to remain unchanged, got %+v", decision)
	}
}

func TestTaskIntakePlannerRespectsModelDecisionForShortFollowUp(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"browser_handoff_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"continues visible browser work","userFacingReply":""}`,
	}}
	toolRegistry := newTestToolSet([]string{"browser_open", "browser_snapshot"})
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "다시 열어봐",
		ToolSet: toolRegistry,
		VisibleContext: agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{
			{Speaker: "사용자", Text: "구글 클라우드 콘솔에서 credential.json 받는 거 도와줘"},
			{Speaker: "김인턴", Text: "Companion 브라우저 연결이 필요합니다."},
		}},
	})

	if decision.Classification != agentcontract.IntakeClassificationBoundedTask || decision.TaskShape != agentcontract.TaskShapeBrowserHandoffTask {
		t.Fatalf("expected model browser decision to be preserved, got %+v", decision)
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "구글 클라우드 콘솔") {
		t.Fatal("expected intake planner to receive visible context")
	}
}

func TestTaskIntakePlannerTreatsLocalArtifactConfirmationAsBoundedTask(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"clarify","classification":"needs_confirmation","taskShape":"approval_gated_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":["pdf"],"reason":"asks for generated files","userFacingReply":"승인하시겠습니까?"}`,
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"medium","estimatedMinutes":10,"requestedOutputFormats":["pdf"],"responseLanguage":"ko","reason":"request is executable","userFacingReply":"","initialToolNames":["file_write"]}`,
	}}
	toolRegistry := newTestToolSet([]string{"terminal_run", "file_write", "file.promote", "file_deliver"})
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "너 뭐 할 수 있는지 피피티 만들어서 pdf로 보내줘",
		ToolSet: toolRegistry,
	})

	if decision.Classification != agentcontract.IntakeClassificationBoundedTask {
		t.Fatalf("expected executable artifact work, got %+v", decision)
	}
	if decision.TaskShape != agentcontract.TaskShapeMaintenanceTask {
		t.Fatalf("expected executable task shape, got %+v", decision)
	}
	if len(languageModel.requests) != 2 || !strings.Contains(joinMessageContent(languageModel.requests[1].Messages), clarificationReviewInstruction) {
		t.Fatalf("expected one clarification review, got %d calls", len(languageModel.requests))
	}
}

func TestTaskIntakePlannerDoesNotOverrideScheduleRefusalWithoutSelectedSkill(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"give_up","classification":"unsupported","taskShape":"immediate_reply","level":"medium","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"background loops are unsupported","userFacingReply":"지원하지 않습니다."}`,
	}}
	toolRegistry := newTestToolSet([]string{"schedule_create"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "schedule_create"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("scheduled"), nil
	})
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{
		IsEnabled:        true,
		DefaultTaskLevel: agentcontract.TaskLevelLow,
	})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "1분마다 \"1분 지났습니다\"라고 보내줘",
		ToolSet: toolRegistry,
	})

	if decision.Classification != agentcontract.IntakeClassificationUnsupported || decision.TaskShape != agentcontract.TaskShapeImmediateReply {
		t.Fatalf("expected raw intake refusal to remain unsupported without selected skill, got %+v", decision)
	}
	if decision.UserFacingReply == "" {
		t.Fatal("expected unsupported reply to remain")
	}
}

func TestTaskIntakePlannerTreatsSupportedSitePrototypeConfirmationAsBoundedTask(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"clarify","classification":"needs_confirmation","taskShape":"approval_gated_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"publishing needs approval","userFacingReply":"승인해주시겠어요?"}`,
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"medium","estimatedMinutes":15,"requestedOutputFormats":null,"responseLanguage":"ko","reason":"request is executable","userFacingReply":"","initialToolNames":["site_serve"]}`,
	}}
	toolRegistry := newTestToolSet([]string{"site_serve", "site_serve"})
	for _, toolName := range toolRegistry.ListToolNames() {
		currentToolName := toolName
		registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: currentToolName}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{
		IsEnabled:        true,
		DefaultTaskLevel: agentcontract.TaskLevelLow,
	})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "웹사이트 하나 만들어서 배포해봐",
		ToolSet: toolRegistry,
	})

	if decision.Classification != agentcontract.IntakeClassificationBoundedTask {
		t.Fatalf("expected executable site task, got %+v", decision)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected one clarification review, got %d calls", len(languageModel.requests))
	}
}

func TestTaskIntakePlannerIncludesTemporalContext(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"responseLanguage":"ko","reason":"website request","userFacingReply":""}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	_ = mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:        "김인턴 구조 웹사이트 만들어줘",
		TurnStartedAt: time.Date(2026, time.May, 17, 1, 2, 3, 0, time.UTC),
		ToolSet:       newTestToolSet([]string{"site_serve", "site_serve"}),
	})

	if len(languageModel.requests) != 1 {
		t.Fatalf("expected one intake request, got %d", len(languageModel.requests))
	}
	body := joinMessageContent(languageModel.requests[0].Messages)
	if !strings.Contains(body, "Runtime temporal context") || !strings.Contains(body, "Current date: 2026-05-17") || !strings.Contains(body, "Current weekday: Sunday") {
		t.Fatalf("expected intake temporal context, got %s", body)
	}
}

func TestTaskIntakePlannerRoutesDestructiveArtifactWorkBeforeApprovalGate(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"clarify","classification":"needs_confirmation","taskShape":"approval_gated_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"destructive","userFacingReply":"승인하시겠습니까?"}`,
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"medium","estimatedMinutes":10,"requestedOutputFormats":null,"responseLanguage":"ko","reason":"confirmation is handled after routing","userFacingReply":"","initialToolNames":["file_write"]}`,
	}}
	toolRegistry := newTestToolSet([]string{"terminal_run", "file_write", "file_deliver"})
	planner := NewTaskIntakePlanner(languageModel, agentcontract.IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, agentcontract.AgentRequest{
		Prompt:  "전체 자료를 삭제하고 새 피피티 만들어줘",
		ToolSet: toolRegistry,
	})

	if decision.Classification != agentcontract.IntakeClassificationBoundedTask {
		t.Fatalf("expected destructive work to reach the confirmation gate, got %+v", decision)
	}
}

func TestTurnRouterPreservesExactPrecomputedDecision(t *testing.T) {
	precomputedDecision := agentcontract.TurnDecision{
		Route:              agentcontract.TurnRouteStartTask,
		Classification:     agentcontract.IntakeClassificationQuickReply,
		TaskShape:          agentcontract.TaskShapeImmediateReply,
		TaskLevel:          agentcontract.TaskLevelLow,
		EstimatedMinutes:   1,
		PriorTaskReference: agentcontract.PriorTaskReferenceNone,
		Reason:             "LLMD topology diagnostic",
	}
	decision := mustPlanTurn(t, NewTurnRouter(nil, agentcontract.IntakeOptions{DefaultTaskLevel: agentcontract.TaskLevelHigh}), agentcontract.AgentRequest{
		Prompt:                     "Create and publish a PDF website",
		PrecomputedTurnDecision:    &precomputedDecision,
		IsPrecomputedDecisionExact: true,
	})

	if decision.Route != agentcontract.TurnRouteStartTask || decision.Classification != agentcontract.IntakeClassificationQuickReply {
		t.Fatalf("expected exact precomputed route and classification, got %+v", decision)
	}
	if decision.TaskShape != agentcontract.TaskShapeImmediateReply || decision.TaskLevel != agentcontract.TaskLevelLow {
		t.Fatalf("expected exact precomputed shape and level, got %+v", decision)
	}
	if len(decision.RequestedOutputFormats) != 0 || len(decision.InitialToolNames) != 0 {
		t.Fatalf("expected exact empty diagnostic requirements, got %+v", decision)
	}
}
func joinedMessageContent(messages []model.Message) string {
	parts := []string{}
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}

type countingSkillRetriever struct {
	result      agentcontract.SkillRetrievalResult
	searchCount int
	requests    []agentcontract.AgentRequest
}

func (retriever *countingSkillRetriever) Available(_ agentcontract.AgentRequest, skillInstructions []agentcontract.SkillInstruction) []agentcontract.SkillInstruction {
	return skillInstructions
}

func (retriever *countingSkillRetriever) Retrieve(_ context.Context, request agentcontract.AgentRequest, _ []agentcontract.SkillInstruction, _ int) agentcontract.SkillRetrievalResult {
	retriever.searchCount++
	retriever.requests = append(retriever.requests, request)
	return retriever.result
}

func (retriever *countingSkillRetriever) Search(_ context.Context, request agentcontract.AgentRequest, _ []agentcontract.SkillInstruction, _ agentcontract.SkillSearchQuerySet, _ int) agentcontract.SkillRetrievalResult {
	retriever.searchCount++
	retriever.requests = append(retriever.requests, request)
	return retriever.result
}

func (retriever *countingSkillRetriever) Refresh(context.Context, []agentcontract.SkillInstruction) {}

type failingLanguageModel struct{}

type turnRouterCorrectionContextKey struct{}

type turnRouterStructuredCorrectionError struct {
	message    string
	correction model.StructuredOutputCorrection
	cause      error
}

func newTurnRouterCorrectionError(message string) turnRouterStructuredCorrectionError {
	return turnRouterStructuredCorrectionError{
		message: message,
		correction: model.StructuredOutputCorrection{
			Code: "structured_output_invalid",
			Diagnostic: model.StructuredOutputDiagnostic{
				Category: model.StructuredOutputDiagnosticSchemaValidation,
				ValidationIssues: []model.StructuredOutputValidationIssue{{
					FieldPath: "/expectedResults/0/start",
					Code:      model.StructuredOutputValidationAdditionalProperty,
				}},
			},
		},
	}
}

func (errorValue turnRouterStructuredCorrectionError) Error() string {
	return errorValue.message
}

func (errorValue turnRouterStructuredCorrectionError) Unwrap() error {
	return errorValue.cause
}

func (errorValue turnRouterStructuredCorrectionError) StructuredOutputCorrection() (model.StructuredOutputCorrection, bool) {
	return errorValue.correction, true
}

type turnRouterCorrectionLanguageModel struct {
	contexts     []context.Context
	requests     []model.StructuredResponseRequest
	contents     []string
	errorsByCall map[int]error
}

func (languageModel *turnRouterCorrectionLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *turnRouterCorrectionLanguageModel) GenerateStructuredResponse(responseContext context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.contexts = append(languageModel.contexts, responseContext)
	languageModel.requests = append(languageModel.requests, request)
	callIndex := len(languageModel.requests) - 1
	if errorValue := languageModel.errorsByCall[callIndex]; errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	if callIndex >= len(languageModel.contents) {
		return model.StructuredResponse{}, nil
	}
	return model.StructuredResponse{Content: languageModel.contents[callIndex]}, nil
}

type clarificationReviewFailureLanguageModel struct {
	content   string
	callCount int
}

func (languageModel *clarificationReviewFailureLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("model failed")
}

func (languageModel *clarificationReviewFailureLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.callCount++
	if languageModel.callCount == 1 {
		return model.StructuredResponse{Content: languageModel.content}, nil
	}
	return model.StructuredResponse{}, errors.New("model failed")
}

func (failingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("model failed")
}

func (failingLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{}, errors.New("model failed")
}

func TestNormalizeWebsiteDeliverableKindRoutesSiteNamespace(t *testing.T) {
	decision := normalizeWebsiteDeliverableKind(agentcontract.TurnDecision{
		DeliverableKind:  agentcontract.DeliverableKindWebsite,
		InitialToolNames: []string{"file_write"},
	})
	if !decisionSuggestsSiteTool(decision) {
		t.Fatalf("website deliverable must route the site namespace: %#v", decision.InitialToolNames)
	}
	unchanged := normalizeWebsiteDeliverableKind(agentcontract.TurnDecision{
		DeliverableKind:  agentcontract.DeliverableKindDocument,
		InitialToolNames: []string{"file_write"},
	})
	if decisionSuggestsSiteTool(unchanged) {
		t.Fatalf("non-website deliverable must not gain site tools: %#v", unchanged.InitialToolNames)
	}
	alreadyRouted := normalizeWebsiteDeliverableKind(agentcontract.TurnDecision{
		DeliverableKind:  agentcontract.DeliverableKindWebsite,
		InitialToolNames: []string{"site_list"},
	})
	if len(alreadyRouted.InitialToolNames) != 1 {
		t.Fatalf("existing site routing must stay untouched: %#v", alreadyRouted.InitialToolNames)
	}
}
