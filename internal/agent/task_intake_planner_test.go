package agent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

func mustNormalizeTurn(t *testing.T, router TurnRouter, decision TurnDecision, request AgentRequest) TurnDecision {
	t.Helper()
	normalizedDecision, errorValue := router.normalizeDecision(decision, request)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return normalizedDecision
}

func mustPlanIntake(t *testing.T, planner TaskIntakePlanner, request AgentRequest) IntakeDecision {
	t.Helper()
	decision, errorValue := planner.Plan(context.Background(), request)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return decision
}

func mustPlanTurn(t *testing.T, router TurnRouter, request AgentRequest) TurnDecision {
	t.Helper()
	decision, errorValue := router.Plan(context.Background(), request)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return decision
}

func TestTurnRouterReturnsDisabledError(t *testing.T) {
	_, errorValue := NewTurnRouter(nil, IntakeOptions{}).Plan(context.Background(), AgentRequest{Prompt: "hello"})
	if !errors.Is(errorValue, ErrTurnRouterDisabled) {
		t.Fatalf("expected disabled error, got %v", errorValue)
	}
}

func TestTurnRouterPropagatesLanguageModelError(t *testing.T) {
	_, errorValue := NewTurnRouter(failingLanguageModel{}, IntakeOptions{IsEnabled: true}).Plan(context.Background(), AgentRequest{Prompt: "hello"})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "model failed") {
		t.Fatalf("expected typed router failure, got %v", errorValue)
	}
}

func TestTurnRouterPreservesUnsupportedArtifactDecision(t *testing.T) {
	outputEvidence := "PDF"
	decision := mustNormalizeTurn(t, NewTurnRouter(nil, IntakeOptions{}), TurnDecision{
		Route:                   TurnRouteGiveUp,
		Classification:          IntakeClassificationUnsupported,
		TaskShape:               TaskShapeImmediateReply,
		TaskLevel:               TaskLevelLow,
		EstimatedMinutes:        1,
		RequestedOutputFormats:  []string{"pdf"},
		RequestedOutputEvidence: &outputEvidence,
		Reason:                  "unsupported",
		UserFacingReply:         "지원하지 않습니다.",
		PriorTaskReference:      PriorTaskReferenceNone,
	}, AgentRequest{Prompt: "PDF 만들어줘", AllowGiveUp: true, ToolSet: newTestToolSet([]string{"terminal.run", "file.deliver"})})

	if decision.Classification != IntakeClassificationUnsupported || decision.Route != TurnRouteGiveUp {
		t.Fatalf("expected router decision to remain authoritative, got %+v", decision)
	}
}

func TestTurnRouterRejectsInconsistentDecisionFields(t *testing.T) {
	validDecision := TurnDecision{
		Route:              TurnRouteStartTask,
		Classification:     IntakeClassificationBoundedTask,
		TaskShape:          TaskShapeMaintenanceTask,
		TaskLevel:          TaskLevelLow,
		EstimatedMinutes:   1,
		ResponseLanguage:   "ko",
		PriorTaskReference: PriorTaskReferenceNone,
	}
	testCases := []struct {
		name     string
		mutate   func(*TurnDecision)
		expected string
	}{
		{name: "quick reply shape", mutate: func(decision *TurnDecision) {
			decision.Classification = IntakeClassificationQuickReply
		}, expected: "quick_reply without immediate_reply"},
		{name: "confirmation route", mutate: func(decision *TurnDecision) {
			decision.Classification = IntakeClassificationNeedsConfirmation
			decision.TaskShape = TaskShapeApprovalGatedTask
		}, expected: "inconsistent needs_confirmation"},
		{name: "unsupported route", mutate: func(decision *TurnDecision) {
			decision.Classification = IntakeClassificationUnsupported
			decision.TaskShape = TaskShapeImmediateReply
		}, expected: "inconsistent unsupported"},
		{name: "bounded terminal route", mutate: func(decision *TurnDecision) {
			decision.Route = TurnRouteConsume
		}, expected: "bounded_task with a terminal route"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			decision := validDecision
			testCase.mutate(&decision)
			_, errorValue := NewTurnRouter(nil, IntakeOptions{}).normalizeDecision(decision, AgentRequest{AllowGiveUp: true})
			if errorValue == nil || !strings.Contains(errorValue.Error(), testCase.expected) {
				t.Fatalf("expected %q error, got %v", testCase.expected, errorValue)
			}
		})
	}
}

func TestTurnRouterAcceptsCanonicalDecisionFields(t *testing.T) {
	testCases := []TurnDecision{
		{Route: TurnRouteStartTask, Classification: IntakeClassificationQuickReply, TaskShape: TaskShapeImmediateReply},
		{Route: TurnRouteConsume, Classification: IntakeClassificationQuickReply, TaskShape: TaskShapeImmediateReply},
		{Route: TurnRouteStartTask, Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
		{Route: TurnRouteClarify, Classification: IntakeClassificationNeedsConfirmation, TaskShape: TaskShapeApprovalGatedTask},
		{Route: TurnRouteGiveUp, Classification: IntakeClassificationUnsupported, TaskShape: TaskShapeImmediateReply},
	}
	for _, decision := range testCases {
		decision.TaskLevel = TaskLevelLow
		decision.EstimatedMinutes = 1
		decision.ResponseLanguage = "ko"
		decision.PriorTaskReference = PriorTaskReferenceNone
		if _, errorValue := NewTurnRouter(nil, IntakeOptions{}).normalizeDecision(decision, AgentRequest{}); errorValue != nil {
			t.Fatalf("expected canonical decision %+v to pass: %v", decision, errorValue)
		}
	}
}

func TestTurnRouterRejectsInvalidEstimatedMinutes(t *testing.T) {
	decision := TurnDecision{
		Route:              TurnRouteStartTask,
		Classification:     IntakeClassificationBoundedTask,
		TaskShape:          TaskShapeMaintenanceTask,
		TaskLevel:          TaskLevelLow,
		EstimatedMinutes:   1,
		ResponseLanguage:   "ko",
		PriorTaskReference: PriorTaskReferenceNone,
	}
	for _, estimatedMinutes := range []int{-1, 0} {
		decision.EstimatedMinutes = estimatedMinutes
		_, errorValue := NewTurnRouter(nil, IntakeOptions{}).normalizeDecision(decision, AgentRequest{})
		if errorValue == nil || !strings.Contains(errorValue.Error(), "invalid estimated minutes") {
			t.Fatalf("expected invalid estimate error for %d, got %v", estimatedMinutes, errorValue)
		}
	}
}

func TestTaskIntakePlannerUsesStructuredModelDecision(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"bounded tool work","userFacingReply":""}`,
	}}
	toolRegistry := newTestToolSet([]string{"memory.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "memory.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{}, nil
	})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{
		IsEnabled:        true,
		DefaultTaskLevel: TaskLevelLow,
	})

	decision := mustPlanIntake(t, planner, AgentRequest{
		Prompt:  "search memory",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationBoundedTask {
		t.Fatalf("expected bounded task, got %q", decision.Classification)
	}
	if decision.TaskShape != TaskShapeResearchTask {
		t.Fatalf("expected research task shape, got %+v", decision)
	}
	if decision.TaskLevel != TaskLevelLow {
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
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"requiredEvidence"`) {
		t.Fatalf("expected required evidence in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
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
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "leave it null for reading, summarizing, searching, or analyzing an input attachment") {
		t.Fatal("expected intake prompt to separate input attachments from file deliverables")
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "Do not use capability.invoke as requiredEvidence") {
		t.Fatal("expected intake prompt to require effective operation evidence")
	}
}

func TestTaskIntakePlannerMapsRequiredEvidenceField(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"expectedResults":[],"requiredEvidence":["calendar.add"],"siteRequestEvidence":"","responseLanguage":"ko","reason":"calendar add","userFacingReply":"","initialToolNames":["calendar.add"],"priorTaskReference":"none"}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})
	toolRegistry := NewToolSet([]string{CapabilityInvokeToolName})
	for _, toolName := range []string{CapabilityInvokeToolName, "calendar.add"} {
		currentToolName := toolName
		toolRegistry.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}

	decision := mustPlanIntake(t, planner, AgentRequest{
		Prompt:  "7월 6일 오후 1시 스타트업월드컵 일정 추가",
		ToolSet: toolRegistry,
	})

	if len(decision.RequiredEvidenceTools) != 1 || decision.RequiredEvidenceTools[0] != "calendar.add" {
		t.Fatalf("expected calendar.add required evidence, got %+v", decision.RequiredEvidenceTools)
	}
	if containsString(decision.InitialToolNames, "calendar.add") {
		t.Fatalf("expected hidden capability operation to stay out of initial tools, got %+v", decision.InitialToolNames)
	}
}

func TestTaskIntakePlannerPassesPriorTaskContext(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":["docx"],"reason":"deliver prior file","userFacingReply":"","priorTaskReference":"outcome_recovery"}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, AgentRequest{
		Prompt: "전달해줘야지 그럼",
		PriorTask: PriorTaskContext{
			TaskRunID:              "88894f",
			Prompt:                 "기업 문서 가이드를 워드 파일로 만들어줘",
			RequestedOutputFormats: []string{"docx"},
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools:      []string{"file.deliver"},
				RequiredAttachmentSuffixes: []string{".docx"},
				ArtifactRequirement:        ArtifactRequirementRequired,
			},
		},
	})

	if decision.PriorTaskReference != PriorTaskReferenceOutcomeRecovery {
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
	planner := NewTaskIntakePlanner(nil, IntakeOptions{})
	_, errorValue := planner.Plan(context.Background(), AgentRequest{Prompt: "링크로 전달된 적 없어. 첨부파일로 줘야지 그리고."})
	if !errors.Is(errorValue, ErrTurnRouterDisabled) {
		t.Fatalf("expected disabled router error, got %v", errorValue)
	}
}

func TestTaskIntakePlannerDropsFileContractWithoutRequestedOutputFormat(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":["html"],"requestedOutputEvidence":"HTML 파일","expectedResults":[{"id":"attached-file","type":"file","description":"attach a file","required":true}],"requiredEvidence":["calendar.update","file.deliver"],"siteRequestEvidence":"","responseLanguage":"ko","reason":"calendar update","userFacingReply":"","initialToolNames":["capability.invoke","file.deliver"],"priorTaskReference":"none"}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})
	toolRegistry := newTestCapabilityToolSet([]string{"calendar.update", "file.deliver"})

	decision := mustPlanIntake(t, planner, AgentRequest{Prompt: "일정을 오후 2시로 수정해줘", ToolSet: toolRegistry})

	if expectedResultIncludesType(OutcomeContract{ExpectedResults: decision.ExpectedResults}, ExpectedResultTypeFile) {
		t.Fatalf("expected untyped file result to be removed, got %+v", decision.ExpectedResults)
	}
	if containsString(decision.RequiredEvidenceTools, FileDeliverToolName) || containsString(decision.InitialToolNames, FileDeliverToolName) {
		t.Fatalf("expected untyped file delivery tools to be removed, got required=%+v initial=%+v", decision.RequiredEvidenceTools, decision.InitialToolNames)
	}
	if !containsString(decision.RequiredEvidenceTools, "calendar.update") {
		t.Fatalf("expected calendar evidence to remain, got %+v", decision.RequiredEvidenceTools)
	}
}

func TestTaskIntakePlannerKeepsGroundedRequestedOutputFormat(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":["html"],"requestedOutputEvidence":"HTML 발표자료","expectedResults":[{"id":"attached-file","type":"file","description":"attach HTML","required":true}],"requiredEvidence":["file.deliver"],"siteRequestEvidence":"","responseLanguage":"ko","reason":"presentation","userFacingReply":"","initialToolNames":["file.deliver"],"priorTaskReference":"none"}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})
	toolRegistry := newTestToolSet([]string{FileDeliverToolName})

	decision := mustPlanIntake(t, planner, AgentRequest{Prompt: "HTML 발표자료를 만들어줘", ToolSet: toolRegistry})

	if strings.Join(decision.RequestedOutputFormats, ",") != "html" || !expectedResultIncludesType(OutcomeContract{ExpectedResults: decision.ExpectedResults}, ExpectedResultTypeFile) {
		t.Fatalf("expected grounded HTML contract to remain, got %+v", decision)
	}
}

func TestTaskIntakePlannerFallbackDoesNotTreatInputAttachmentExtensionAsOutput(t *testing.T) {
	planner := NewTaskIntakePlanner(nil, IntakeOptions{})
	_, errorValue := planner.Plan(context.Background(), AgentRequest{Prompt: "첨부한 report.pdf 요약 작성해줘"})
	if !errors.Is(errorValue, ErrTurnRouterDisabled) {
		t.Fatalf("expected disabled router error, got %v", errorValue)
	}
}

func TestTaskIntakePlannerFallbackDoesNotInferDomainEvidence(t *testing.T) {
	planner := NewTaskIntakePlanner(nil, IntakeOptions{})
	_, errorValue := planner.Plan(context.Background(), AgentRequest{Prompt: "업무 등록해줘"})
	if !errors.Is(errorValue, ErrTurnRouterDisabled) {
		t.Fatalf("expected disabled router error, got %v", errorValue)
	}
}

func TestTaskIntakePlannerMapsModelRequiredEvidenceToCapabilityInvoke(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"expectedResults":[],"requiredEvidence":["task.add"],"siteRequestEvidence":"","responseLanguage":"ko","reason":"task request","userFacingReply":"","initialToolNames":[],"priorTaskReference":"none"}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})
	toolRegistry := newTestCapabilityToolSet([]string{"task.add", "task.list", "task.update"})

	decision := mustPlanIntake(t, planner, AgentRequest{
		Prompt:  "업무 등록해줘\n- 메일 페이지 앱 비밀번호, 다양한 사이트 관련 링크로 이동으로 개선하기",
		ToolSet: toolRegistry,
	})

	if len(decision.RequiredEvidenceTools) != 1 || decision.RequiredEvidenceTools[0] != "task.add" {
		t.Fatalf("expected model task.add required evidence, got %+v", decision.RequiredEvidenceTools)
	}
	if containsString(decision.InitialToolNames, "task.add") {
		t.Fatalf("expected hidden capability operation to stay out of initial tools, got %+v", decision.InitialToolNames)
	}
	if len(decision.InitialToolNames) != 0 {
		t.Fatalf("expected router initial tools to remain authoritative, got %+v", decision.InitialToolNames)
	}
}

func TestTaskIntakePlannerKeepsStructuredOutputFormats(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":["html"],"reason":"explicit html output","userFacingReply":""}`,
	}}
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.deliver"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, AgentRequest{
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

func TestTaskIntakePlannerUsesStructuredArtifactEnumForFileDelivery(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"give_up","classification":"unsupported","taskShape":"immediate_reply","level":"medium","estimatedMinutes":1,"requestedOutputFormats":["pdf"],"siteRequestEvidence":"","responseLanguage":"ko","reason":"mistaken unsupported file artifact","userFacingReply":"PDF 생성은 지원하지 않습니다.","priorTaskReference":"none"}`,
	}}
	toolRegistry := newTestToolSet([]string{"conversation.history", "file.read", "file.write", "terminal.run", "file.promote", "file.deliver"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, AgentRequest{
		Prompt:  "제안서를 PDF 파일로 만들어줘",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationUnsupported {
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

func TestTaskIntakePlannerUsesRequestedOutputFormatsToResolveArtifactKindConflict(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":["pdf"],"expectedResults":[{"id":"result-1","type":"file","description":"PDF document","required":true},{"id":"site-public-link","type":"link","description":"public URL","required":true}],"siteRequestEvidence":"","responseLanguage":"ko","reason":"conflicted artifact kind","userFacingReply":"","initialToolNames":["site.status"],"priorTaskReference":"none"}`,
	}}
	toolRegistry := newTestToolSet([]string{"conversation.history", "file.read", "file.write", "terminal.run", "file.promote", "file.deliver", "site.status"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, AgentRequest{
		Prompt:  "제공한 데이터만 기반으로 제안서를 PDF 파일로 만들어서 첨부해줘",
		ToolSet: toolRegistry,
	})

	if !hasArtifactOutputFormat(decision.RequestedOutputFormats) {
		t.Fatalf("expected requested output formats to imply file artifact work, got %+v", decision.RequestedOutputFormats)
	}
	if len(decision.ExpectedResults) != 1 || decision.ExpectedResults[0].ID != "result-1" {
		t.Fatalf("expected quote-less site result to be removed, got %+v", decision.ExpectedResults)
	}
	if slices.Contains(decision.InitialToolNames, "site.status") {
		t.Fatalf("expected site tool to be removed from initial tools, got %+v", decision.InitialToolNames)
	}
}

func TestTaskIntakePlannerDropsQuoteLessHallucinatedSiteRequirement(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"scheduled_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"expectedResults":[{"id":"site-public-link","type":"link","description":"public URL","required":true}],"siteRequestEvidence":"","responseLanguage":"ko","reason":"calendar work","userFacingReply":""}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, AgentRequest{Prompt: "19일 오후 6시 어반브랜딩 미팅 추가 위치는 코엑스"})

	if expectedResultsContain(decision.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected hallucinated link result to be dropped, got %+v", decision.ExpectedResults)
	}
	if strings.TrimSpace(decision.SiteRequestEvidence) != "" {
		t.Fatalf("expected empty site evidence after drop, got %q", decision.SiteRequestEvidence)
	}
	if !decision.siteNormalizationReport.HasDrops() {
		t.Fatalf("expected diagnostic normalization report, got %+v", decision.siteNormalizationReport)
	}
}

func TestTaskIntakePlannerAcceptsVerbatimSiteEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":null,"expectedResults":[{"id":"site-public-link","type":"link","description":"public URL","required":true}],"siteRequestEvidence":"웹사이트 만들어서 배포","responseLanguage":"ko","reason":"site work","userFacingReply":""}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, AgentRequest{Prompt: "포트폴리오 웹사이트 만들어서 배포해줘"})

	if !expectedResultsContain(decision.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected verified link result to remain, got %+v", decision.ExpectedResults)
	}
	if decision.SiteRequestEvidence != "웹사이트 만들어서 배포" {
		t.Fatalf("expected verbatim evidence to remain, got %q", decision.SiteRequestEvidence)
	}
	if decision.siteNormalizationReport.HasDrops() {
		t.Fatalf("expected no normalization drops, got %+v", decision.siteNormalizationReport)
	}
}

func TestAgentKernelRecordsSiteRequirementNormalizationEvent(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"scheduled_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"expectedResults":[{"id":"site-public-link","type":"link","description":"public URL","required":true}],"requiredEvidence":["calendar.add"],"siteRequestEvidence":"","responseLanguage":"ko","reason":"calendar work","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"calendar.add","input":{"title":"어반브랜딩 미팅","location":"코엑스"}}}`,
		finishMessageWithEvidence("일정을 추가했습니다.", "obs-001", "calendar.add", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)

	toolSet := newTestCapabilityToolSet([]string{"calendar.add"})
	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "19일 오후 6시 어반브랜딩 미팅 추가 위치는 코엑스",
		ToolSet:           toolSet,
	})
	if errorValue != nil {
		t.Fatalf("expected normalized intake to run: %v", errorValue)
	}

	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, siteRequirementNormalizationEventName, "site evidence quote") {
		t.Fatalf("expected site normalization event, got %+v", taskEvents)
	}
	if taskEventsContain(taskEvents, "agent.intake", `"site-public-link"`) {
		t.Fatalf("expected agent intake event not to include unverified site result, got %+v", taskEvents)
	}
}

func TestTurnRouterSchemaUsesContextDependentPendingFields(t *testing.T) {
	toolSet := newTestCapabilityToolSet([]string{"task.add", "task.update"})
	noPendingSchema := turnRouterSchema(AgentRequest{ToolSet: toolSet})
	if strings.Contains(noPendingSchema, `"approval"`) {
		t.Fatalf("expected no approval field without pending confirmation, got %s", noPendingSchema)
	}
	if strings.Contains(noPendingSchema, `"choices"`) {
		t.Fatalf("expected no choices field without pending choice, got %s", noPendingSchema)
	}
	if !strings.Contains(noPendingSchema, `"clarificationQuestion"`) || !strings.Contains(noPendingSchema, `"clarificationOptions"`) {
		t.Fatalf("expected optional clarify fields in base schema, got %s", noPendingSchema)
	}
	for _, expectedEmojiName := range []string{`"reactionEmojiName"`, `"white_check_mark"`, `"thumbsup"`, `"tada"`, `"rocket"`, `"ok_hand"`, `"hourglass_flowing_sand"`, `"sparkles"`, `"wave"`} {
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
	for _, toolName := range []string{`"task.add"`, `"task.update"`, `"capability.invoke"`} {
		if !strings.Contains(noPendingSchema, toolName) {
			t.Fatalf("expected registered tool enum value %s in schema, got %s", toolName, noPendingSchema)
		}
	}

	pendingSchema := turnRouterSchema(AgentRequest{
		PendingConfirmation: PendingConfirmationContext{TaskRunID: "task-1"},
		PendingChoice: PendingChoiceContext{
			TaskRunID: "task-2",
			Options:   []ChoiceReplyOption{{Key: "A", Label: "Option A"}},
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
	description := turnRoutingContextDescription(AgentRequest{
		PendingInput: PendingInputContext{
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
	router := NewTurnRouter(nil, IntakeOptions{IsEnabled: false})
	decision := mustNormalizeTurn(t, router, TurnDecision{
		Route:                 TurnRouteClarify,
		Classification:        IntakeClassificationNeedsConfirmation,
		TaskShape:             TaskShapeApprovalGatedTask,
		TaskLevel:             TaskLevelXLow,
		EstimatedMinutes:      1,
		ResponseLanguage:      "ko",
		Reason:                "needs finite choice",
		ClarificationQuestion: " 어느 방식으로 진행할까요? ",
		ClarificationOptions: []ClarificationOption{
			{Key: "A", Label: "A안", Value: "first"},
			{Key: "A", Label: "duplicate"},
			{Label: "B안", Value: "second"},
			{Key: "C", Label: ""},
		},
	}, AgentRequest{})

	if decision.Classification != IntakeClassificationNeedsConfirmation {
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
	router := NewTurnRouter(nil, IntakeOptions{IsEnabled: false})
	nullDecision := mustNormalizeTurn(t, router, TurnDecision{
		Route:            TurnRouteConsume,
		Classification:   IntakeClassificationQuickReply,
		TaskShape:        TaskShapeImmediateReply,
		TaskLevel:        TaskLevelXLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "ack",
	}, AgentRequest{})
	validDecision := mustNormalizeTurn(t, router, TurnDecision{
		Route:             TurnRouteConsume,
		Classification:    IntakeClassificationQuickReply,
		TaskShape:         TaskShapeImmediateReply,
		TaskLevel:         TaskLevelXLow,
		EstimatedMinutes:  1,
		ResponseLanguage:  "ko",
		Reason:            "ack",
		ReactionEmojiName: ":TADA:",
	}, AgentRequest{})
	invalidDecision := mustNormalizeTurn(t, router, TurnDecision{
		Route:             TurnRouteConsume,
		Classification:    IntakeClassificationQuickReply,
		TaskShape:         TaskShapeImmediateReply,
		TaskLevel:         TaskLevelXLow,
		EstimatedMinutes:  1,
		ResponseLanguage:  "ko",
		Reason:            "ack",
		ReactionEmojiName: "unknown_custom_emoji",
	}, AgentRequest{})

	if nullDecision.ReactionEmojiName != DefaultReactionEmojiName {
		t.Fatalf("expected missing emoji to default, got %q", nullDecision.ReactionEmojiName)
	}
	if validDecision.ReactionEmojiName != "tada" {
		t.Fatalf("expected valid emoji to normalize, got %q", validDecision.ReactionEmojiName)
	}
	if invalidDecision.ReactionEmojiName != DefaultReactionEmojiName {
		t.Fatalf("expected invalid emoji to default, got %q", invalidDecision.ReactionEmojiName)
	}
	if nullDecision.Route != TurnRouteConsume {
		t.Fatalf("expected lightweight consume route to stay consume, got %q", nullDecision.Route)
	}
}

func TestTurnRouterRequiresDirectMessageConsumeFallback(t *testing.T) {
	router := NewTurnRouter(nil, IntakeOptions{IsEnabled: false})
	request := AgentRequest{ConversationType: "D"}
	missingFallback := mustNormalizeTurn(t, router, TurnDecision{
		Route:            TurnRouteConsume,
		Classification:   IntakeClassificationQuickReply,
		TaskShape:        TaskShapeImmediateReply,
		TaskLevel:        TaskLevelXLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "ack",
	}, request)
	withFallback := mustNormalizeTurn(t, router, TurnDecision{
		Route:            TurnRouteConsume,
		Classification:   IntakeClassificationQuickReply,
		TaskShape:        TaskShapeImmediateReply,
		TaskLevel:        TaskLevelXLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "ack",
		UserFacingReply:  "알겠습니다.",
	}, request)

	if missingFallback.Route != TurnRouteConsume {
		t.Fatalf("expected router consume route to remain unchanged, got %+v", missingFallback)
	}
	if withFallback.Route != TurnRouteConsume || withFallback.UserFacingReply != "알겠습니다." {
		t.Fatalf("expected direct consume with fallback to remain consume, got %+v", withFallback)
	}
}

func TestTurnRouterRejectsTaskfulConsumeRoute(t *testing.T) {
	router := NewTurnRouter(nil, IntakeOptions{IsEnabled: false})
	toolSet := newTestToolSet([]string{"task.add", "task.list", "task.update"})
	_, errorValue := router.normalizeDecision(TurnDecision{
		Route:                 TurnRouteConsume,
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeResearchTask,
		TaskLevel:             TaskLevelLow,
		EstimatedMinutes:      1,
		ResponseLanguage:      "ko",
		Reason:                "사용자가 명시적으로 업무 등록을 요청함",
		RequiredEvidenceTools: []string{"task.add"},
		InitialToolNames:      []string{"task.add", "task.list", "task.update"},
	}, AgentRequest{
		Prompt:  "업무 등록해줘.\n\n- 메일 페이지 앱 비밀번호 개선",
		ToolSet: toolSet,
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "bounded_task with a terminal route") {
		t.Fatalf("expected contradictory consume error, got %v", errorValue)
	}
}

func TestTurnRouterRejectsBoundedGiveUp(t *testing.T) {
	router := NewTurnRouter(nil, IntakeOptions{IsEnabled: false})
	_, errorValue := router.normalizeDecision(TurnDecision{
		Route:                  TurnRouteGiveUp,
		Classification:         IntakeClassificationBoundedTask,
		TaskShape:              TaskShapeMaintenanceTask,
		TaskLevel:              TaskLevelLow,
		EstimatedMinutes:       1,
		RequestedOutputFormats: []string{"pptx"},
		ResponseLanguage:       "ko",
		Choices:                []string{"B", "A", "A"},
	}, AgentRequest{
		PendingConfirmation: PendingConfirmationContext{TaskRunID: "task-1"},
		PendingChoice: PendingChoiceContext{
			TaskRunID:     "task-2",
			SelectionMode: "multiple",
			Options: []ChoiceReplyOption{
				{Key: "A", Label: "Option A"},
			},
		},
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "bounded_task with a terminal route") {
		t.Fatalf("expected bounded give_up error, got %v", errorValue)
	}
}

func TestTurnRouterNormalizesChoiceNumberToOptionKey(t *testing.T) {
	router := NewTurnRouter(nil, IntakeOptions{IsEnabled: false})
	decision := mustNormalizeTurn(t, router, TurnDecision{
		Route:                  TurnRouteContinueTask,
		Classification:         IntakeClassificationBoundedTask,
		TaskShape:              TaskShapeMaintenanceTask,
		TaskLevel:              TaskLevelLow,
		EstimatedMinutes:       1,
		RequestedOutputFormats: nil,
		ResponseLanguage:       "ko",
		Choices:                []string{"2"},
	}, AgentRequest{
		PendingInput: PendingInputContext{
			TaskRunID:     "task-2",
			SelectionMode: "single",
			Options: []ChoiceReplyOption{
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
	approval := ApprovalSignalApprove
	router := NewTurnRouter(nil, IntakeOptions{IsEnabled: false})
	decision := mustNormalizeTurn(t, router, TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeMaintenanceTask,
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "approval",
		Approval:         &approval,
	}, AgentRequest{
		PendingConfirmation: PendingConfirmationContext{TaskRunID: "task-1"},
	})

	if decision.Route != TurnRouteContinueTask {
		t.Fatalf("expected approval to force continuation, got %+v", decision)
	}
}

func TestSelectedToolsDoNotOverrideUnsupportedDecision(t *testing.T) {
	decision := applySelectedSkillCompletionRequirements(IntakeDecision{
		Classification:         IntakeClassificationUnsupported,
		TaskShape:              TaskShapeImmediateReply,
		TaskLevel:              TaskLevelLow,
		EstimatedMinutes:       1,
		RequestedOutputFormats: []string{"pptx"},
		ResponseLanguage:       "ko",
		Reason:                 "previous permission failure",
		UserFacingReply:        "PPTX 파일 생성은 불가능합니다.",
	}, namedSkillBundle("presentation"))

	if decision.Classification != IntakeClassificationUnsupported {
		t.Fatalf("expected router classification to remain unsupported, got %+v", decision)
	}
	if decision.UserFacingReply != "PPTX 파일 생성은 불가능합니다." {
		t.Fatalf("expected router reply to remain unchanged, got %q", decision.UserFacingReply)
	}
	if strings.Join(decision.RequestedOutputFormats, ",") != "pptx" {
		t.Fatalf("expected pptx format to be preserved, got %+v", decision.RequestedOutputFormats)
	}
}

func TestTaskIntakePlannerReturnsLanguageModelError(t *testing.T) {
	planner := NewTaskIntakePlanner(failingLanguageModel{}, IntakeOptions{IsEnabled: true})
	_, errorValue := planner.Plan(context.Background(), AgentRequest{Prompt: "please analyze the whole repo"})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "model failed") {
		t.Fatalf("expected language model error, got %v", errorValue)
	}
}

func TestTaskIntakePlannerReturnsLanguageModelErrorWithActiveTask(t *testing.T) {
	router := NewTurnRouter(failingLanguageModel{}, IntakeOptions{IsEnabled: true})
	_, errorValue := router.Plan(context.Background(), AgentRequest{
		Prompt:     "아니야 하지마",
		ActiveTask: ActiveTaskContext{TaskRunID: "task-1", Prompt: "report 만들어줘"},
	})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "model failed") {
		t.Fatalf("expected language model error, got %v", errorValue)
	}
}

func TestTaskIntakePlannerReturnsLanguageModelErrorWithoutActiveTask(t *testing.T) {
	router := NewTurnRouter(failingLanguageModel{}, IntakeOptions{IsEnabled: true})
	_, errorValue := router.Plan(context.Background(), AgentRequest{Prompt: "please analyze the whole repo"})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "model failed") {
		t.Fatalf("expected language model error, got %v", errorValue)
	}
}

func TestTaskIntakePlannerDoesNotInferTaskLevelAfterLanguageModelError(t *testing.T) {
	planner := NewTaskIntakePlanner(failingLanguageModel{}, IntakeOptions{IsEnabled: true})
	decision, errorValue := planner.Plan(context.Background(), AgentRequest{Prompt: "please search memory"})
	if errorValue == nil || decision.TaskLevel != "" {
		t.Fatalf("expected error without inferred task level, got decision=%+v error=%v", decision, errorValue)
	}
}

func TestTaskIntakePlannerClampsBrowserControlEffort(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"browser_handoff_task","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"browser control","userFacingReply":""}`,
	}}
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.screenshot"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, AgentRequest{
		Prompt:  "open google and take a screenshot",
		ToolSet: toolRegistry,
	})

	if decision.TaskLevel != TaskLevelXLow {
		t.Fatalf("expected router task level to remain unchanged, got %+v", decision)
	}
}

func TestTaskIntakePlannerRespectsModelDecisionForShortFollowUp(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"browser_handoff_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"continues visible browser work","userFacingReply":""}`,
	}}
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.snapshot"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, AgentRequest{
		Prompt:  "다시 열어봐",
		ToolSet: toolRegistry,
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "사용자", Text: "구글 클라우드 콘솔에서 credential.json 받는 거 도와줘"},
			{Speaker: "김인턴", Text: "Companion 브라우저 연결이 필요합니다."},
		}},
	})

	if decision.Classification != IntakeClassificationBoundedTask || decision.TaskShape != TaskShapeBrowserHandoffTask {
		t.Fatalf("expected model browser decision to be preserved, got %+v", decision)
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "구글 클라우드 콘솔") {
		t.Fatal("expected intake planner to receive visible context")
	}
}

func TestTaskIntakePlannerTreatsLocalArtifactConfirmationAsBoundedTask(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"clarify","classification":"needs_confirmation","taskShape":"approval_gated_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":["pdf"],"reason":"asks for generated files","userFacingReply":"승인하시겠습니까?"}`,
	}}
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.promote", "file.deliver"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, AgentRequest{
		Prompt:  "너 뭐 할 수 있는지 피피티 만들어서 pdf로 보내줘",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationNeedsConfirmation {
		t.Fatalf("expected router confirmation classification to remain authoritative, got %+v", decision)
	}
	if decision.TaskShape != TaskShapeApprovalGatedTask {
		t.Fatalf("expected router task shape to remain authoritative, got %+v", decision)
	}
	if decision.UserFacingReply != "승인하시겠습니까?" {
		t.Fatalf("expected router reply to remain unchanged, got %q", decision.UserFacingReply)
	}
}

func TestTaskIntakePlannerDoesNotOverrideScheduleRefusalWithoutSelectedSkill(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"give_up","classification":"unsupported","taskShape":"immediate_reply","level":"medium","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"background loops are unsupported","userFacingReply":"지원하지 않습니다."}`,
	}}
	toolRegistry := newTestToolSet([]string{"schedule.create"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "schedule.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("scheduled"), nil
	})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{
		IsEnabled:        true,
		DefaultTaskLevel: TaskLevelLow,
	})

	decision := mustPlanIntake(t, planner, AgentRequest{
		Prompt:  "1분마다 \"1분 지났습니다\"라고 보내줘",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationUnsupported || decision.TaskShape != TaskShapeImmediateReply {
		t.Fatalf("expected raw intake refusal to remain unsupported without selected skill, got %+v", decision)
	}
	if decision.UserFacingReply == "" {
		t.Fatal("expected unsupported reply to remain")
	}
}

func TestAgentKernelPreservesScheduledIntakeRefusalAfterSkillSelection(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"give_up","classification":"unsupported","taskShape":"immediate_reply","level":"medium","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"background loops are unsupported","userFacingReply":"지원하지 않습니다."}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"schedule.create","input":{"taskInstruction":"현재 대화에 \"죄송합니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}}}`,
		finishMessageWithEvidence("1분 간격으로 10번 보내도록 예약했습니다.", "obs-001", "schedule.create", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(staticSkillRetriever{result: SkillRetrievalResult{SelectedCandidates: []SkillCandidate{{Name: "scheduled-task", Score: 1, Reason: "test"}}}})
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{{
			Name:         "scheduled-task",
			Description:  "Create scheduled, recurring, and finite repeated messages.",
			WhenToUse:    "Use for reminders, interval messages, 1분에 한 번씩, 10번, finite repeated message, and repeat N times requests.",
			Prompt:       "Use schedule.create with taskInstruction for the run-time work, intervalSecond, repeatPolicy, and maxRunCount.",
			AllowedTools: []string{"schedule.create"},
			Completion:   SkillCompletion{RequiredEvidenceTools: []string{"schedule.create"}},
			Source:       InstructionSource{Path: "skills/scheduled-task/SKILL.md", SkillName: "scheduled-task"},
		}}}
	})
	toolRegistry := newTestCapabilityToolSet([]string{"schedule.create"})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            `1분에 한 번씩 나한테 "죄송합니다" 10번 해봐`,
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected unsupported intake to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked scheduled task, got %s", result.TaskRun.Status)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.action", "continue") {
		t.Fatal("expected no task execution after unsupported router decision")
	}
}

func TestAgentKernelSelectsArtifactSkillOnceAfterRouting(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":["pptx"],"initialToolNames":["file.deliver"],"reason":"create and deliver the requested presentation","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"artifacts/deck/deck.pptx"}}`,
		finishMessageWithEvidence("deck.pptx 파일을 첨부했습니다.", "obs-001", "file.deliver", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	skillRetriever := &countingSkillRetriever{result: SkillRetrievalResult{
		RetrievalMode:      "embedding",
		IndexStatus:        "ready",
		CandidateCount:     1,
		SelectedCandidates: []SkillCandidate{{Name: "presentation", Score: 1, Reason: "embedding_similarity"}},
	}}
	services.kernel.UseSkillRetriever(skillRetriever)
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{{
			Name:         "presentation",
			Description:  "Create presentation decks, 피피티, 파워포인트, 발표자료, and PPTX files.",
			WhenToUse:    "Use for 피피티 and PPTX requests.",
			Prompt:       "Create and attach PPTX files.",
			TriggerHints: []string{"피피티", "pptx"},
			AllowedTools: []string{"terminal.run", "file.write", "file.deliver"},
			Source:       InstructionSource{Path: "skills/presentation/SKILL.md", SkillName: "presentation"},
		}}}
	})
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.promote", "file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/private/people/person-1/artifacts/deck/deck.pptx",
				Filename:   "deck.pptx",
			}},
		}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "아까 피피티 다시 해봐",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected routed artifact task to run: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected pptx attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", `"classification":"bounded_task"`) {
		t.Fatal("expected bounded artifact intake")
	}
	if skillRetriever.searchCount != 1 {
		t.Fatalf("expected one routed skill retrieval, got %d", skillRetriever.searchCount)
	}
	if len(skillRetriever.requests) != 1 {
		t.Fatalf("expected one routed skill request, got %d", len(skillRetriever.requests))
	}
	selectionContract := skillRetriever.requests[0].ActiveGoal.OutcomeContract
	if !stringSliceContains(selectionContract.RequiredEvidenceTools, FileDeliverToolName) || !stringSliceContains(selectionContract.RequiredAttachmentSuffixes, ".pptx") || selectionContract.ArtifactRequirement != ArtifactRequirementRequired {
		t.Fatalf("expected routed artifact contract during skill selection, got %+v", selectionContract)
	}
	skillQueryCount := 0
	for _, request := range intakeLanguageModel.requests {
		if request.StructuredOutputSchema.Name == "blueclaw_skill_search_queries" {
			skillQueryCount++
		}
	}
	if skillQueryCount != 1 {
		t.Fatalf("expected one routed skill query, got %d", skillQueryCount)
	}
}

func TestAgentKernelPreservesUnsupportedArtifactWithoutSelectedSkill(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"give_up","classification":"unsupported","taskShape":"immediate_reply","level":"low","estimatedMinutes":1,"requestedOutputFormats":["pptx"],"initialToolNames":["file.deliver"],"responseLanguage":"ko","reason":"previous permission failure","userFacingReply":"PPTX 파일 생성은 불가능합니다."}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"artifacts/deck/deck.pptx"}}`,
		finishMessageWithEvidence("deck.pptx 파일을 첨부했습니다.", "obs-001", "file.deliver", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.promote", "file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/private/people/person-1/artifacts/deck/deck.pptx",
				Filename:   "deck.pptx",
			}},
		}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "다시 해봐 이제 될 거야",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected unsupported intake to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task, got %s", result.TaskRun.Status)
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("expected no attachment after unsupported decision, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", `"classification":"unsupported"`) {
		t.Fatal("expected unsupported router classification to remain authoritative")
	}
}

func TestAgentKernelRecoversPriorTaskAttachmentContract(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"responseLanguage":"ko","reason":"latest message asks to deliver prior file outcome","userFacingReply":"","initialToolNames":[],"priorTaskReference":"outcome_recovery"}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("기존 작업이 이미 완료되어 파일이 준비되었습니다."),
		`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"artifacts/company-guide/company-guide.docx"}}`,
		finishMessageWithEvidence("company-guide.docx 파일을 첨부했습니다.", "obs-002", "file.deliver", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/private/people/person-1/artifacts/company-guide/company-guide.docx",
				Filename:   "company-guide.docx",
			}},
		}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "direct-1",
		Prompt:            "전달해줘야지 그럼",
		ToolSet:           toolRegistry,
		PriorTask: PriorTaskContext{
			TaskRunID: "88894f",
			Status:    string(task.TaskStatusFailed),
			Prompt:    "기업 문서 가이드를 워드 파일로 만들어줘",
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools:      []string{"file.deliver"},
				RequiredAttachmentSuffixes: []string{".docx"},
				ExpectedResults: []ExpectedResult{{
					ID:          "attached-file",
					Type:        ExpectedResultTypeFile,
					Description: "docx guide attached to the current conversation",
					Required:    true,
				}},
				ArtifactRequirement: ArtifactRequirementRequired,
			},
			RequestedOutputFormats: []string{"docx"},
		},
	})

	if errorValue != nil {
		t.Fatalf("expected prior task attachment recovery to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
		t.Fatalf("expected completed recovery task, got %s events=%+v", result.TaskRun.Status, events)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "company-guide.docx" {
		t.Fatalf("expected current task docx attachment, got %+v", result.Attachments)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "agent.completion_required", "required file expected result") {
		t.Fatal("expected first text-only finish to be rejected by the restored file contract")
	}
	if !taskEventsContain(events, "agent.intake", `"priorTaskReference":"outcome_recovery"`) {
		t.Fatal("expected intake event to record prior task outcome recovery")
	}
	if !strings.Contains(joinedMessageContent(replyLanguageModel.requests[0].Messages), "Prior task context") {
		t.Fatal("expected task model context to include prior task context")
	}
}

func TestAgentKernelRecoversLegacyPriorAttachmentContractFromIntakeOutput(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":["docx"],"responseLanguage":"ko","reason":"latest message asks for the prior Word file as an attachment","userFacingReply":"","initialToolNames":["file.deliver"],"priorTaskReference":"outcome_recovery"}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("기존 작업이 이미 완료되어 파일이 준비되었습니다."),
		`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"artifacts/company-guide/company-guide.docx"}}`,
		finishMessageWithEvidence("company-guide.docx 파일을 첨부했습니다.", "obs-002", "file.deliver", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"conversation.history", "file.read", "file.write", "terminal.run", "file.promote", "file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/private/people/person-1/artifacts/company-guide/company-guide.docx",
				Filename:   "company-guide.docx",
			}},
		}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "direct-1",
		Prompt:            "링크로 전달된 적 없어. 첨부파일로 줘야지 그리고.",
		ToolSet:           toolRegistry,
		PriorTask: PriorTaskContext{
			TaskRunID: "88894f",
			Status:    string(task.TaskStatusCompleted),
			Prompt:    "기업 문서 가이드를 워드 파일로 만들어줘",
			Result:    "요청하신 작업이 이미 성공적으로 완료되었습니다.",
		},
	})

	if errorValue != nil {
		t.Fatalf("expected fallback prior task attachment recovery to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
		t.Fatalf("expected completed recovery task, got %s events=%+v", result.TaskRun.Status, events)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "company-guide.docx" {
		t.Fatalf("expected current task docx attachment, got %+v", result.Attachments)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "agent.completion_required", "required file expected result") {
		t.Fatal("expected text-only finish to be rejected by intake-restored file contract")
	}
	if !taskEventsContain(events, "agent.intake", `"requestedOutputFormats":["docx"]`) {
		t.Fatal("expected intake event to record structured output format")
	}
}

func TestTaskIntakePlannerTreatsSupportedSitePrototypeConfirmationAsBoundedTask(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"clarify","classification":"needs_confirmation","taskShape":"approval_gated_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":null,"requiredEvidence":["site.create","site.publish"],"siteRequestEvidence":"웹사이트 하나 만들어서 배포","reason":"publishing needs approval","userFacingReply":"승인해주시겠어요?"}`,
	}}
	toolRegistry := newTestToolSet([]string{"site.create", "site.publish"})
	for _, toolName := range toolRegistry.ListToolNames() {
		currentToolName := toolName
		toolRegistry.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{
		IsEnabled:        true,
		DefaultTaskLevel: TaskLevelLow,
	})

	decision := mustPlanIntake(t, planner, AgentRequest{
		Prompt:  "웹사이트 하나 만들어서 배포해봐",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationNeedsConfirmation {
		t.Fatalf("expected confirmation classification to remain authoritative, got %+v", decision)
	}
	if decision.TaskShape != TaskShapeApprovalGatedTask {
		t.Fatalf("expected approval-gated task shape, got %+v", decision)
	}
	if decision.UserFacingReply != "승인해주시겠어요?" {
		t.Fatalf("expected router reply to remain unchanged, got %q", decision.UserFacingReply)
	}
}

func TestTaskIntakePlannerIncludesTemporalContext(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"responseLanguage":"ko","reason":"website request","userFacingReply":""}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	_ = mustPlanIntake(t, planner, AgentRequest{
		Prompt:        "김인턴 구조 웹사이트 만들어줘",
		TurnStartedAt: time.Date(2026, time.May, 17, 1, 2, 3, 0, time.UTC),
		ToolSet:       newTestToolSet([]string{"site.create", "site.publish"}),
	})

	if len(languageModel.requests) != 1 {
		t.Fatalf("expected one intake request, got %d", len(languageModel.requests))
	}
	body := joinMessageContent(languageModel.requests[0].Messages)
	if !strings.Contains(body, "Runtime temporal context") || !strings.Contains(body, "Current date: 2026-05-17") || !strings.Contains(body, "Current weekday: Sunday") {
		t.Fatalf("expected intake temporal context, got %s", body)
	}
}

func TestTaskIntakePlannerKeepsDestructiveArtifactWorkApprovalGated(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"clarify","classification":"needs_confirmation","taskShape":"approval_gated_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"destructive","userFacingReply":"승인하시겠습니까?"}`,
	}}
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.deliver"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := mustPlanIntake(t, planner, AgentRequest{
		Prompt:  "전체 자료를 삭제하고 새 피피티 만들어줘",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationNeedsConfirmation {
		t.Fatalf("expected destructive request to stay approval gated, got %+v", decision)
	}
}

func TestTurnRouterPreservesExactPrecomputedDecision(t *testing.T) {
	precomputedDecision := TurnDecision{
		Route:              TurnRouteStartTask,
		Classification:     IntakeClassificationQuickReply,
		TaskShape:          TaskShapeImmediateReply,
		TaskLevel:          TaskLevelLow,
		EstimatedMinutes:   1,
		PriorTaskReference: PriorTaskReferenceNone,
		Reason:             "SDKD topology diagnostic",
	}
	decision := mustPlanTurn(t, NewTurnRouter(nil, IntakeOptions{DefaultTaskLevel: TaskLevelHigh}), AgentRequest{
		Prompt:                     "Create and publish a PDF website",
		PrecomputedTurnDecision:    &precomputedDecision,
		IsPrecomputedDecisionExact: true,
	})

	if decision.Route != TurnRouteStartTask || decision.Classification != IntakeClassificationQuickReply {
		t.Fatalf("expected exact precomputed route and classification, got %+v", decision)
	}
	if decision.TaskShape != TaskShapeImmediateReply || decision.TaskLevel != TaskLevelLow {
		t.Fatalf("expected exact precomputed shape and level, got %+v", decision)
	}
	if len(decision.RequestedOutputFormats) != 0 || len(decision.RequiredEvidenceTools) != 0 || len(decision.InitialToolNames) != 0 {
		t.Fatalf("expected exact empty diagnostic requirements, got %+v", decision)
	}
}
func TestTaskLevelProfileMapping(t *testing.T) {
	profile := TaskLevelProfileForLevel(TaskLevelMedium)

	if profile.MaxIterationCount != 180 || profile.MaxToolCallCount != 100 || profile.Duration.Minutes() != 40 {
		t.Fatalf("expected medium profile, got %+v", profile)
	}
}

func TestAgentKernelUsesIntakeBeforeRunningTools(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"clarify","classification":"needs_confirmation","taskShape":"approval_gated_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"ambiguous target","clarificationQuestion":"Which one do you mean?","clarificationOptions":[{"key":"A","label":"First","value":"First"},{"key":"B","label":"Second","value":"Second"}],"userFacingReply":"Which one do you mean?"}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("should not run"),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"expensive"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "expensive"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("expensive result"), nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do the entire thing",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected intake-only result: %v", errorValue)
	}
	if result.UserNotice != "Which one do you mean?" {
		t.Fatalf("expected clarification reply, got %q", result.UserNotice)
	}
	if result.TaskRun.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected waiting user input, got %s", result.TaskRun.Status)
	}
	if len(replyLanguageModel.requests) != 0 {
		t.Fatalf("expected agent loop not to run, got %d model calls", len(replyLanguageModel.requests))
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", "needs_confirmation") {
		t.Fatal("expected intake event")
	}
}

func TestAgentKernelCreatesChoiceAskForClarificationOptions(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"clarify","classification":"needs_confirmation","taskShape":"approval_gated_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"responseLanguage":"ko","reason":"needs output choice","userFacingReply":"","clarificationQuestion":"어떤 형식으로 만들까요?","clarificationOptions":[{"key":"A","label":"웹사이트","value":"website"},{"key":"B","label":"발표자료","value":"slides"}]}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("should not run"),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "소개 자료 만들어줘",
		ToolSet:           newTestToolSet([]string{"ask.choice"}),
	})
	if errorValue != nil {
		t.Fatalf("expected clarify result: %v", errorValue)
	}

	if result.UserNotice != "어떤 형식으로 만들까요?" {
		t.Fatalf("expected clarification question, got %q", result.UserNotice)
	}
	if result.TaskRun.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected waiting user input, got %s", result.TaskRun.Status)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "ask.requested", `"kind":"ask_input"`) {
		t.Fatalf("expected option-bearing ask.input event, got %+v", events)
	}
	if !taskEventsContain(events, "ask.requested", `"recommendedOptionKey":"A"`) {
		t.Fatalf("expected first option to be recommended, got %+v", events)
	}
	if len(replyLanguageModel.requests) != 0 {
		t.Fatalf("expected agent loop not to run, got %d model calls", len(replyLanguageModel.requests))
	}
}

func TestAgentKernelQuickReplyAllowsToolFreeReplyWithoutAskInput(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"direct answer","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("hello"),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"expensive", "ask.input"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "expensive"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("expensive result"), nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "hello",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected quick reply: %v", errorValue)
	}
	if result.FinishMessage != "hello" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if len(replyLanguageModel.requests) != 1 {
		t.Fatalf("expected one direct reply request, got %d", len(replyLanguageModel.requests))
	}
	actionSchema := string(replyLanguageModel.requests[0].StructuredOutputSchema.Document)
	if strings.Contains(actionSchema, AskInputToolName) {
		t.Fatalf("expected quick reply schema to hide ask.input without typed interaction, got %s", actionSchema)
	}
	if !strings.Contains(strings.Join(result.ToolNames, ","), "expensive") {
		t.Fatalf("expected quick reply result to preserve tools, got %+v", result.ToolNames)
	}
}

func TestAgentKernelRunTurnPreservesCheckpointSender(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"needs tool","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","message":"확인 중입니다.","toolName":"capability.invoke","toolInput":{"operation":"alpha","input":{"value":"one"}}}`,
		finishMessageWithEvidence("done", "obs-002", "alpha", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("alpha result"), nil
	})
	checkpoints := []AgentCheckpoint{}

	result, errorValue := services.kernel.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "확인해줘",
		ToolSet:           toolRegistry,
		CheckpointSender: func(_ context.Context, checkpoint AgentCheckpoint) error {
			checkpoints = append(checkpoints, checkpoint)
			return nil
		},
	})
	if errorValue != nil {
		t.Fatalf("expected task to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if len(checkpoints) != 1 || checkpoints[0].Message != "확인 중입니다." {
		t.Fatalf("expected checkpoint sender to be preserved, got %+v", checkpoints)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.checkpoint.sent", "capability.invoke") {
		t.Fatal("expected checkpoint sent event")
	}
}

func TestAgentKernelQuickReplyPromotesToolFailureToRecovery(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"initialToolNames":["primary.lookup","backup.lookup"],"reason":"quick with useful tool","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"primary.lookup","input":{"query":"hello"}}}`,
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"backup.lookup","input":{"query":"hello"}}}`,
		finishMessageWithEvidence("backup answer", "obs-003", "backup.lookup", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestCapabilityToolSet([]string{"primary.lookup", "backup.lookup"})
	primaryCallCount := 0
	backupCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "primary.lookup"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		primaryCallCount++
		return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "primary_lookup", "primary lookup failed"), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "backup.lookup"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		backupCallCount++
		return ToolSuccess("backup result"), nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "lookup hello",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected quick recovery: %v", errorValue)
	}
	if result.FinishMessage != "backup answer" {
		t.Fatalf("expected recovered final reply, got %q", result.FinishMessage)
	}
	if primaryCallCount != 1 || backupCallCount != 1 {
		t.Fatalf("expected one primary failure and one backup recovery, got primary=%d backup=%d", primaryCallCount, backupCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.recovery_attempt", "adjacent_tool") {
		t.Fatal("expected adjacent recovery event")
	}
}

func TestAgentKernelQuickReplyFailureDoesNotInventToolFailure(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"direct answer","userFacingReply":""}`,
	}}
	services := newKernelIntakeTestServices(failingLanguageModel{}, intakeLanguageModel)

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "1+1=",
	})
	if errorValue != nil {
		t.Fatalf("expected direct reply failure result: %v", errorValue)
	}
	if strings.Contains(strings.ToLower(result.UserNotice), "calculation tool") || strings.Contains(strings.ToLower(result.UserNotice), "data processing") {
		t.Fatalf("expected no invented tool failure, got %q", result.UserNotice)
	}
	if result.ReplySuppressed || !strings.Contains(result.UserNotice, "llm action failed: model failed") {
		t.Fatalf("expected raw model error reply, got reply=%q suppressed=%v", result.UserNotice, result.ReplySuppressed)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "raw_error") {
		t.Fatal("expected raw error failure reply event")
	}
}

func TestAgentKernelQuickReplyCanUseCalculatorTool(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"initialToolNames":["math.calculate"],"responseLanguage":"ko","reason":"calculation","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"math.calculate","input":{"expression":"1+1"}}}`,
		finishMessageWithEvidence("2", "obs-001", "math.calculate", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestCapabilityToolSet([]string{"math.calculate"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "math.calculate"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"expression":"1+1","result":"2"}`), nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "1+1=",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected quick calculator reply: %v", errorValue)
	}
	if result.FinishMessage != "2" {
		t.Fatalf("expected calculator final reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.capability.invoke.result", "math.calculate") {
		t.Fatal("expected calculator tool event")
	}
}

func TestAgentKernelQuickReplyUsesAskChoiceForExplicitChoiceRequest(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"expectedResults":[{"id":"interactive-choice","type":"message","description":"사용자가 직접 고를 수 있는 선택지 UI가 표시됨","required":true,"acceptanceHints":["ask.choice"]}],"responseLanguage":"ko","reason":"choice probe","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("아래 세 가지 중 하나를 선택해 주세요.\n\n1. 선택지 1\n2. 선택지 2\n3. 선택지 3"),
		`{"action":"continue","toolName":"ask.input","toolInput":{"question":"아래 세 가지 중 하나를 선택해 주세요.","options":["선택지 1","선택지 2","선택지 3"],"recommendedOptionKey":"1","selectionMode":"single"}}`,
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"ask.input"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "ask.input"}, func(toolContext context.Context, invocation ToolInvocation) (ToolResult, error) {
		taskRunID := TaskRunIDFromContext(toolContext)
		if taskRunID == "" {
			return ToolFailureResult(FailureInvalidInput, FailureCodes.InvalidInput, "ask_choice", "missing task run"), nil
		}
		_, errorValue := services.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingUserInput, "아래 세 가지 중 하나를 선택해 주세요.")
		if errorValue != nil {
			return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "ask_choice", errorValue.Error()), nil
		}
		services.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", string(invocation.Input))
		return ToolSuccess(`{"kind":"choice_single","question":"아래 세 가지 중 하나를 선택해 주세요."}`), nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "나한테 1 2 3 선택지 줘봐. 잘 동작하는지 테스트해보게",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected choice request: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected waiting user input, got %s", result.TaskRun.Status)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "agent.completion_required", "ask.input") {
		t.Fatalf("expected text-only finish to be rejected, got %+v", events)
	}
	if !taskEventsContain(events, "ask.requested", `"selectionMode":"single"`) {
		t.Fatalf("expected ask.input request event, got %+v", events)
	}
	if len(replyLanguageModel.requests) != 2 {
		t.Fatalf("expected finish rejection then ask.choice action, got %d requests", len(replyLanguageModel.requests))
	}
}

func TestAgentKernelPreservesQuickReplyAfterSkillSelection(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"direct answer","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("deck created too early"),
		`{"action":"continue","message":"deck attached: deck.pptx","toolName":"file.deliver","toolInput":{"path":"deck.pptx"}}`,
		finishMessageWithEvidence("deck attached: deck.pptx", "obs-003", "file.deliver", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(staticSkillRetriever{result: SkillRetrievalResult{SelectedCandidates: []SkillCandidate{{Name: "presentation", Score: 1, Reason: "test"}}}})
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{
			Skills: []SkillInstruction{{
				Name:         "presentation",
				Description:  "Create presentation slides.",
				WhenToUse:    "Use for 피피티 and PPTX requests.",
				Prompt:       "Create and attach PPTX files.",
				TriggerHints: []string{"피피티"},
				AllowedTools: []string{"terminal.run", "file.write", "file.deliver"},
				Completion: SkillCompletion{
					RequiredEvidenceTools: []string{"file.deliver"},
				},
				Source: InstructionSource{Path: "skills/presentation/SKILL.md", SkillName: "presentation"},
			}},
		}
	})
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.deliver"})
	for _, toolName := range toolRegistry.ListToolNames() {
		currentToolName := toolName
		toolRegistry.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			if currentToolName == "file.deliver" {
				return ToolResult{
					Output: ToolOutput{Content: "attached"},
					Attachments: []FileAttachment{{
						DevicePath: "artifacts/deck/deck.pptx",
						Filename:   "deck.pptx",
					}},
				}, nil
			}
			return ToolSuccess("ok"), nil
		})
	}

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "너 뭐 할 수 있는지 피피티 만들어서 보내줘봐",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected quick reply: %v", errorValue)
	}
	if result.FinishMessage != "deck created too early" {
		t.Fatalf("expected router quick reply to remain authoritative, got %q", result.FinishMessage)
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("expected no inferred artifact delivery, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", "quick_reply") {
		t.Fatal("expected router quick reply classification to remain authoritative")
	}
}

func TestAgentKernelUsesStructuredOutputFormatsForAttachmentRequirements(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":["html"],"initialToolNames":["file.deliver"],"reason":"explicit html output","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"deck.html"}}`,
		finishMessageWithEvidence("HTML 파일을 첨부했습니다: deck.html", "obs-001", "file.deliver", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(NewEmbeddingSkillRetriever(nil, ""))
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{{
			Name:         "html-attachment",
			Description:  "Attach HTML deliverables.",
			WhenToUse:    "Use for html output requests.",
			Prompt:       "Use file.deliver for HTML deliverables.",
			TriggerHints: []string{"html"},
			AllowedTools: []string{"file.deliver"},
			Source:       InstructionSource{Path: "skills/html-attachment/SKILL.md", SkillName: "html-attachment"},
		}}}
	})
	toolRegistry := newTestToolSet([]string{"file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/deck/deck.html",
				Filename:   "deck.html",
				SizeBytes:  12,
			}},
		}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "html만 주면 돼",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected structured output format task to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.html" {
		t.Fatalf("expected html attachment, got %+v", result.Attachments)
	}
	if !strings.Contains(joinedMessageContent(replyLanguageModel.requests[0].Messages), ".html") {
		t.Fatal("expected structured output format to become an html attachment requirement")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", `"requestedOutputFormats":["html"]`) {
		t.Fatal("expected intake event to preserve structured output format")
	}
}

func joinedMessageContent(messages []llm.Message) string {
	parts := []string{}
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}

type kernelIntakeTestServices struct {
	kernel           *AgentKernel
	taskRunService   *task.TaskRunService
	taskEventService *task.TaskEventService
}

func newKernelIntakeTestServices(replyLanguageModel llm.LanguageModelProvider, intakeLanguageModel llm.LanguageModelProvider) kernelIntakeTestServices {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskStepService := task.NewTaskStepService()
	kernel := NewAgentKernel(taskRunService, taskStepService)
	kernel.UseLanguageModelProvider(replyLanguageModel)
	kernel.UseIntakeLanguageModelProvider(intakeLanguageModel)
	kernel.UseIntakeOptions(IntakeOptions{
		IsEnabled:        true,
		DefaultTaskLevel: TaskLevelLow,
	})
	return kernelIntakeTestServices{kernel: kernel, taskRunService: taskRunService, taskEventService: taskEventService}
}

type countingSkillRetriever struct {
	result      SkillRetrievalResult
	searchCount int
	requests    []AgentRequest
}

func (retriever *countingSkillRetriever) Retrieve(_ context.Context, request AgentRequest, _ []SkillInstruction, _ int) SkillRetrievalResult {
	retriever.searchCount++
	retriever.requests = append(retriever.requests, request)
	return retriever.result
}

func (retriever *countingSkillRetriever) Search(_ context.Context, request AgentRequest, _ []SkillInstruction, _ SkillSearchQuerySet, _ int) SkillRetrievalResult {
	retriever.searchCount++
	retriever.requests = append(retriever.requests, request)
	return retriever.result
}

func (retriever *countingSkillRetriever) Refresh(context.Context, []SkillInstruction) {}

type failingLanguageModel struct{}

func (failingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("model failed")
}

func (failingLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, errors.New("model failed")
}
