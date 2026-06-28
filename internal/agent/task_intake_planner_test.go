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

func TestTaskIntakePlannerUsesStructuredModelDecision(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"research_task","effortLevel":"standard","requestedOutputFormats":null,"reason":"bounded tool work","userFacingReply":""}`,
	}}
	toolRegistry := newTestToolSet([]string{"memory.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "memory.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{}, nil
	})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{
		IsEnabled:          true,
		DefaultEffortLevel: EffortLevelStandard,
	})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "search memory",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationBoundedTask {
		t.Fatalf("expected bounded task, got %q", decision.Classification)
	}
	if decision.TaskShape != TaskShapeResearchTask {
		t.Fatalf("expected research task shape, got %+v", decision)
	}
	if decision.EffortLevel != EffortLevelStandard {
		t.Fatalf("expected selected effort level, got %+v", decision)
	}
	if decision.TaskComplexity != TaskComplexityNormal {
		t.Fatalf("expected default task complexity, got %+v", decision)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected one intake model call, got %d", len(languageModel.requests))
	}
	if languageModel.requests[0].StructuredOutputSchema.Name != "blueclaw_turn_router" {
		t.Fatalf("expected turn router schema, got %q", languageModel.requests[0].StructuredOutputSchema.Name)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"taskShape"`) {
		t.Fatalf("expected task shape in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"taskComplexity"`) {
		t.Fatalf("expected task complexity in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"requestedOutputFormats"`) {
		t.Fatalf("expected requested output formats in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"outputKind"`) {
		t.Fatalf("expected output kind in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"enum":["file","photo","site"]`) {
		t.Fatalf("expected generic output kind enum in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"priorTaskReference"`) {
		t.Fatalf("expected prior task reference in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, WorkKindFlowTask) {
		t.Fatalf("expected flow task work kind in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
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
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "set it to null for reading, summarizing, searching, or analyzing an input attachment") {
		t.Fatal("expected intake prompt to separate input attachments from file deliverables")
	}
}

func TestTaskIntakePlannerPassesPriorTaskContext(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","effortLevel":"standard","requestedOutputFormats":["docx"],"reason":"deliver prior file","userFacingReply":"","priorTaskReference":"outcome_recovery"}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt: "전달해줘야지 그럼",
		PriorTask: PriorTaskContext{
			TaskRunID:              "88894f",
			Prompt:                 "기업 문서 가이드를 워드 파일로 만들어줘",
			RequestedOutputFormats: []string{"docx"},
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools:      []string{"file.attach"},
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

func TestTaskIntakePlannerFallbackRecoversPriorDocxDelivery(t *testing.T) {
	planner := NewTaskIntakePlanner(nil, IntakeOptions{})
	toolRegistry := newTestToolSet([]string{"conversation.history", "file.read", "file.write", "terminal.run", "file.promote", "file.attach"})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "링크로 전달된 적 없어. 첨부파일로 줘야지 그리고.",
		ToolSet: toolRegistry,
		PriorTask: PriorTaskContext{
			TaskRunID:              "88894f",
			Status:                 string(task.TaskStatusCompleted),
			Prompt:                 "기업 문서 가이드를 워드 파일로 만들어줘",
			Result:                 "요청하신 작업이 이미 성공적으로 완료되었습니다.",
			RequestedOutputFormats: []string{"docx"},
			OutcomeContract: OutcomeContract{
				RequiredAttachmentSuffixes: []string{".docx"},
			},
		},
	})

	if decision.PriorTaskReference != PriorTaskReferenceOutcomeRecovery {
		t.Fatalf("expected deterministic fallback to recover prior outcome, got %+v", decision)
	}
	if strings.Join(decision.RequestedOutputFormats, ",") != "docx" {
		t.Fatalf("expected docx output format, got %+v", decision.RequestedOutputFormats)
	}
	if !decision.HasWorkKind(WorkKindFileDelivery) {
		t.Fatalf("expected file delivery work kind, got %+v", decision.WorkKinds)
	}
	if !slices.Contains(decision.InitialToolNames, "file.attach") {
		t.Fatalf("expected file.attach to be prepared, got %+v", decision.InitialToolNames)
	}
}

func TestTaskIntakePlannerFallbackDoesNotTreatInputAttachmentExtensionAsOutput(t *testing.T) {
	planner := NewTaskIntakePlanner(nil, IntakeOptions{})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt: "첨부한 report.pdf 요약 작성해줘",
	})

	if len(decision.RequestedOutputFormats) != 0 {
		t.Fatalf("expected no output format for input attachment work, got %+v", decision.RequestedOutputFormats)
	}
	if decision.HasWorkKind(WorkKindFileDelivery) {
		t.Fatalf("expected no file delivery work kind for input attachment work, got %+v", decision.WorkKinds)
	}
}

func TestTaskIntakePlannerFallbackDoesNotInferFlowTaskWorkKind(t *testing.T) {
	planner := NewTaskIntakePlanner(nil, IntakeOptions{})
	toolRegistry := newTestToolSet([]string{"flow.task.add", "flow.task.list", "flow.task.update"})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "업무 등록해줘\n- 메일 페이지 앱 비밀번호, 다양한 사이트 관련 링크로 이동으로 개선하기",
		ToolSet: toolRegistry,
	})

	if decision.HasWorkKind(WorkKindFlowTask) {
		t.Fatalf("expected fallback not to infer flow task work kind, got %+v", decision.WorkKinds)
	}
	if len(decision.InitialToolNames) != 0 {
		t.Fatalf("expected fallback not to pin flow tools, got %+v", decision.InitialToolNames)
	}
}

func TestTaskIntakePlannerMapsModelFlowTaskWorkKindToInitialTools(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"reason":"flow task request","userFacingReply":"","workKinds":["flow_task"]}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})
	toolRegistry := newTestToolSet([]string{"flow.task.add", "flow.task.list", "flow.task.update"})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "업무 등록해줘\n- 메일 페이지 앱 비밀번호, 다양한 사이트 관련 링크로 이동으로 개선하기",
		ToolSet: toolRegistry,
	})

	if !decision.HasWorkKind(WorkKindFlowTask) {
		t.Fatalf("expected model flow task work kind, got %+v", decision.WorkKinds)
	}
	for _, toolName := range []string{"flow.task.add", "flow.task.list", "flow.task.update"} {
		if !containsString(decision.InitialToolNames, toolName) {
			t.Fatalf("expected initial flow tool %s, got %+v", toolName, decision.InitialToolNames)
		}
	}
}

func TestTaskIntakePlannerKeepsStructuredOutputFormats(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"research_task","effortLevel":"standard","requestedOutputFormats":["html"],"reason":"explicit html output","userFacingReply":""}`,
	}}
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.attach"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "html만 주면 돼",
		ToolSet: toolRegistry,
	})

	if strings.Join(decision.RequestedOutputFormats, ",") != "html" {
		t.Fatalf("expected structured html output format, got %+v", decision.RequestedOutputFormats)
	}
	if decision.OutputKind != OutputKindFile {
		t.Fatalf("expected requested output formats to imply file output kind, got %+v", decision.OutputKind)
	}
}

func TestTaskIntakePlannerUsesStructuredArtifactEnumForFileDelivery(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"unsupported","taskShape":"immediate_reply","taskComplexity":"simple","effortLevel":"deep","outputKind":"file","requestedOutputFormats":["pdf"],"siteRequestEvidence":"","responseLanguage":"ko","reason":"mistaken unsupported file artifact","userFacingReply":"PDF 생성은 지원하지 않습니다.","workKinds":[],"priorTaskReference":"none"}`,
	}}
	toolRegistry := newTestToolSet([]string{"conversation.history", "file.read", "file.write", "terminal.run", "file.promote", "file.attach"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "제안서를 PDF 파일로 만들어줘",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationBoundedTask {
		t.Fatalf("expected structured file artifact enum to recover bounded task, got %+v", decision)
	}
	if decision.OutputKind != OutputKindFile {
		t.Fatalf("expected file artifact kind, got %+v", decision)
	}
	if strings.Join(decision.RequestedOutputFormats, ",") != "pdf" {
		t.Fatalf("expected pdf output format, got %+v", decision.RequestedOutputFormats)
	}
	if !decision.HasWorkKind(WorkKindFileDelivery) {
		t.Fatalf("expected file delivery work kind, got %+v", decision.WorkKinds)
	}
	if !slices.Contains(decision.InitialToolNames, "file.attach") {
		t.Fatalf("expected file delivery tools from enum result, got %+v", decision.InitialToolNames)
	}
	if decision.UserFacingReply != "" {
		t.Fatalf("expected unsupported reply to be cleared, got %q", decision.UserFacingReply)
	}
}

func TestTaskIntakePlannerUsesRequestedOutputFormatsToResolveArtifactKindConflict(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","taskComplexity":"normal","effortLevel":"deep","requestedOutputFormats":["pdf"],"expectedResults":[{"id":"result-1","type":"file","description":"PDF document","required":true},{"id":"site-public-link","type":"link","description":"public URL","required":true}],"siteRequestEvidence":"","responseLanguage":"ko","reason":"conflicted artifact kind","userFacingReply":"","workKinds":["file_delivery","site_prototype"],"initialToolNames":["site.app.status"],"priorTaskReference":"none"}`,
	}}
	toolRegistry := newTestToolSet([]string{"conversation.history", "file.read", "file.write", "terminal.run", "file.promote", "file.attach", "site.app.status"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "제공한 데이터만 기반으로 제안서를 PDF 파일로 만들어서 첨부해줘",
		ToolSet: toolRegistry,
	})

	if decision.OutputKind != OutputKindFile {
		t.Fatalf("expected requested output formats to imply file output kind, got %+v", decision.OutputKind)
	}
	if !decision.HasWorkKind(WorkKindFileDelivery) {
		t.Fatalf("expected file delivery work kind, got %+v", decision.WorkKinds)
	}
	if decision.HasWorkKind(WorkKindSitePrototype) {
		t.Fatalf("expected quote-less site work kind to be removed, got %+v", decision.WorkKinds)
	}
	if len(decision.ExpectedResults) != 1 || decision.ExpectedResults[0].ID != "result-1" {
		t.Fatalf("expected quote-less site result to be removed, got %+v", decision.ExpectedResults)
	}
	if slices.Contains(decision.InitialToolNames, "site.app.status") {
		t.Fatalf("expected site tool to be removed from initial tools, got %+v", decision.InitialToolNames)
	}
}

func TestTaskIntakePlannerDropsQuoteLessHallucinatedSiteWorkKind(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"scheduled_task","taskComplexity":"simple","effortLevel":"standard","requestedOutputFormats":null,"expectedResults":[{"id":"site-public-link","type":"link","description":"public URL","required":true}],"siteRequestEvidence":"","responseLanguage":"ko","reason":"calendar work","userFacingReply":"","workKinds":["calendar","site_prototype"]}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{Prompt: "19일 오후 6시 어반브랜딩 미팅 추가 위치는 코엑스"})

	if decision.HasWorkKind(WorkKindSitePrototype) {
		t.Fatalf("expected hallucinated site work kind to be dropped, got %+v", decision.WorkKinds)
	}
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
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","taskComplexity":"complex","effortLevel":"standard","requestedOutputFormats":null,"expectedResults":[{"id":"site-public-link","type":"link","description":"public URL","required":true}],"siteRequestEvidence":"웹사이트 만들어서 배포","responseLanguage":"ko","reason":"site work","userFacingReply":"","workKinds":["site_prototype"]}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{Prompt: "포트폴리오 웹사이트 만들어서 배포해줘"})

	if !decision.HasWorkKind(WorkKindSitePrototype) {
		t.Fatalf("expected verified site work kind to remain, got %+v", decision.WorkKinds)
	}
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
		`{"route":"start_task","classification":"bounded_task","taskShape":"scheduled_task","taskComplexity":"simple","effortLevel":"standard","requestedOutputFormats":null,"expectedResults":[{"id":"site-public-link","type":"link","description":"public URL","required":true}],"siteRequestEvidence":"","responseLanguage":"ko","reason":"calendar work","userFacingReply":"","workKinds":["calendar","site_prototype"]}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("일정을 추가했습니다."),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "19일 오후 6시 어반브랜딩 미팅 추가 위치는 코엑스",
	})
	if errorValue != nil {
		t.Fatalf("expected normalized intake to run: %v", errorValue)
	}

	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, siteRequirementNormalizationEventName, WorkKindSitePrototype) {
		t.Fatalf("expected site normalization event, got %+v", taskEvents)
	}
	if taskEventsContain(taskEvents, "agent.intake", WorkKindSitePrototype) {
		t.Fatalf("expected agent intake event not to include site work kind, got %+v", taskEvents)
	}
}

func TestTurnRouterSchemaUsesContextDependentPendingFields(t *testing.T) {
	noPendingSchema := turnRouterSchema(AgentRequest{})
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
	decision := router.normalizeDecision(TurnDecision{
		Route:                 TurnRouteClarify,
		Classification:        IntakeClassificationQuickReply,
		TaskShape:             TaskShapeImmediateReply,
		EffortLevel:           EffortLevelQuick,
		ResponseLanguage:      "ko",
		Reason:                "needs finite choice",
		ClarificationQuestion: " 어느 방식으로 진행할까요? ",
		ClarificationOptions: []ClarificationOption{
			{Key: "A", Label: "A안", Value: "first"},
			{Key: "A", Label: "duplicate"},
			{Label: "B안", Value: "second"},
			{Key: "C", Label: ""},
		},
	}, router.deterministicDecision(AgentRequest{}), AgentRequest{})

	if decision.Classification != IntakeClassificationNeedsConfirmation {
		t.Fatalf("expected clarify route to require confirmation, got %+v", decision)
	}
	if decision.UserFacingReply != "어느 방식으로 진행할까요?" {
		t.Fatalf("expected clarification question as reply, got %q", decision.UserFacingReply)
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
	nullDecision := router.normalizeDecision(TurnDecision{
		Route:            TurnRouteConsume,
		Classification:   IntakeClassificationQuickReply,
		TaskShape:        TaskShapeImmediateReply,
		EffortLevel:      EffortLevelQuick,
		ResponseLanguage: "ko",
		Reason:           "ack",
	}, router.deterministicDecision(AgentRequest{}), AgentRequest{})
	validDecision := router.normalizeDecision(TurnDecision{
		Route:             TurnRouteConsume,
		Classification:    IntakeClassificationQuickReply,
		TaskShape:         TaskShapeImmediateReply,
		EffortLevel:       EffortLevelQuick,
		ResponseLanguage:  "ko",
		Reason:            "ack",
		ReactionEmojiName: ":TADA:",
	}, router.deterministicDecision(AgentRequest{}), AgentRequest{})
	invalidDecision := router.normalizeDecision(TurnDecision{
		Route:             TurnRouteConsume,
		Classification:    IntakeClassificationQuickReply,
		TaskShape:         TaskShapeImmediateReply,
		EffortLevel:       EffortLevelQuick,
		ResponseLanguage:  "ko",
		Reason:            "ack",
		ReactionEmojiName: "unknown_custom_emoji",
	}, router.deterministicDecision(AgentRequest{}), AgentRequest{})

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

func TestTurnRouterPromotesTaskfulConsumeToTaskRoute(t *testing.T) {
	router := NewTurnRouter(nil, IntakeOptions{IsEnabled: false})
	toolSet := newTestToolSet([]string{"flow.task.add", "flow.task.list", "flow.task.update"})
	decision := router.normalizeDecision(TurnDecision{
		Route:            TurnRouteConsume,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeResearchTask,
		TaskComplexity:   TaskComplexitySimple,
		EffortLevel:      EffortLevelStandard,
		ResponseLanguage: "ko",
		Reason:           "사용자가 명시적으로 업무 등록을 요청함",
		WorkKinds:        []string{WorkKindFlowTask},
		InitialToolNames: []string{"flow.task.add", "flow.task.list", "flow.task.update"},
	}, router.deterministicDecision(AgentRequest{ToolSet: toolSet}), AgentRequest{
		Prompt:  "업무 등록해줘.\n\n- 메일 페이지 앱 비밀번호 개선",
		ToolSet: toolSet,
	})

	if decision.Route != TurnRouteStartTask {
		t.Fatalf("expected taskful consume to become start_task, got %+v", decision)
	}
	if !workKindsContain(decision.WorkKinds, WorkKindFlowTask) {
		t.Fatalf("expected flow task work kind to remain, got %+v", decision.WorkKinds)
	}
	if !containsString(decision.InitialToolNames, "flow.task.add") {
		t.Fatalf("expected initial flow task tool to remain, got %+v", decision.InitialToolNames)
	}
}

func TestTurnRouterNormalizesConditionalFields(t *testing.T) {
	router := NewTurnRouter(nil, IntakeOptions{IsEnabled: false})
	decision := router.normalizeDecision(TurnDecision{
		Route:                  TurnRouteGiveUp,
		Classification:         IntakeClassificationBoundedTask,
		TaskShape:              TaskShapeMaintenanceTask,
		EffortLevel:            EffortLevelStandard,
		RequestedOutputFormats: []string{"pptx"},
		ResponseLanguage:       "ko",
		Choices:                []string{"B", "A", "A"},
	}, router.deterministicDecision(AgentRequest{}), AgentRequest{
		PendingConfirmation: PendingConfirmationContext{TaskRunID: "task-1"},
		PendingChoice: PendingChoiceContext{
			TaskRunID:     "task-2",
			SelectionMode: "multiple",
			Options: []ChoiceReplyOption{
				{Key: "A", Label: "Option A"},
			},
		},
	})

	if decision.Approval == nil || *decision.Approval != ApprovalSignalUnclear {
		t.Fatalf("expected missing pending approval to normalize to unclear, got %+v", decision.Approval)
	}
	if decision.Route != TurnRouteAnswerMeta {
		t.Fatalf("expected disallowed give_up to normalize to answer_meta, got %+v", decision.Route)
	}
	if strings.Join(decision.Choices, ",") != "A" {
		t.Fatalf("expected choices to be whitelisted and deduplicated, got %+v", decision.Choices)
	}
}

func TestTurnRouterNormalizesChoiceNumberToOptionKey(t *testing.T) {
	router := NewTurnRouter(nil, IntakeOptions{IsEnabled: false})
	decision := router.normalizeDecision(TurnDecision{
		Route:                  TurnRouteContinueTask,
		Classification:         IntakeClassificationBoundedTask,
		TaskShape:              TaskShapeMaintenanceTask,
		EffortLevel:            EffortLevelStandard,
		RequestedOutputFormats: nil,
		ResponseLanguage:       "ko",
		Choices:                []string{"2"},
	}, router.deterministicDecision(AgentRequest{}), AgentRequest{
		PendingChoice: PendingChoiceContext{
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
	decision := router.normalizeDecision(TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeMaintenanceTask,
		EffortLevel:      EffortLevelStandard,
		ResponseLanguage: "ko",
		Reason:           "approval",
		Approval:         &approval,
	}, router.deterministicDecision(AgentRequest{}), AgentRequest{
		PendingConfirmation: PendingConfirmationContext{TaskRunID: "task-1"},
	})

	if decision.Route != TurnRouteContinueTask {
		t.Fatalf("expected approval to force continuation, got %+v", decision)
	}
}

func TestTaskRecoveryPlannerPromotesArtifactRetryDespitePriorFailureRefusal(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.promote", "file.attach"})
	decision := (TaskRecoveryPlanner{}).Plan(AgentRequest{
		Prompt:  "다시 해봐 이제 될 거야",
		ToolSet: toolRegistry,
	}, IntakeDecision{
		Classification:         IntakeClassificationUnsupported,
		TaskShape:              TaskShapeImmediateReply,
		EffortLevel:            EffortLevelStandard,
		RequestedOutputFormats: []string{"pptx"},
		ResponseLanguage:       "ko",
		Reason:                 "previous permission failure",
		UserFacingReply:        "PPTX 파일 생성은 불가능합니다.",
	})

	if decision.Classification != IntakeClassificationBoundedTask {
		t.Fatalf("expected artifact retry to become bounded, got %+v", decision)
	}
	if decision.UserFacingReply != "" {
		t.Fatalf("expected intake refusal reply to be cleared, got %q", decision.UserFacingReply)
	}
	if strings.Join(decision.RequestedOutputFormats, ",") != "pptx" {
		t.Fatalf("expected pptx format to be preserved, got %+v", decision.RequestedOutputFormats)
	}
}

func TestTaskIntakePlannerFallsBackDeterministically(t *testing.T) {
	planner := NewTaskIntakePlanner(failingLanguageModel{}, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{Prompt: "please analyze the whole repo"})

	if decision.Classification != IntakeClassificationBoundedTask {
		t.Fatalf("expected uniform bounded fallback, got %q", decision.Classification)
	}
	if decision.TaskShape != TaskShapeResearchTask {
		t.Fatalf("expected research fallback shape, got %+v", decision)
	}
	if !decision.UsedDeterministicFallback {
		t.Fatal("expected deterministic fallback marker")
	}
}

func TestTaskIntakePlannerFallbackDefaultsUncertainWorkToStandard(t *testing.T) {
	planner := NewTaskIntakePlanner(failingLanguageModel{}, IntakeOptions{IsEnabled: true})
	toolRegistry := newTestToolSet([]string{"memory.search"})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "please search memory",
		ToolSet: toolRegistry,
	})

	if decision.EffortLevel != EffortLevelStandard {
		t.Fatalf("expected standard effort fallback, got %+v", decision)
	}
}

func TestTaskIntakePlannerClampsBrowserControlEffort(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"browser_handoff_task","effortLevel":"quick","requestedOutputFormats":null,"reason":"browser control","userFacingReply":""}`,
	}}
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.screenshot"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "open google and take a screenshot",
		ToolSet: toolRegistry,
	})

	if decision.EffortLevel != EffortLevelDeep {
		t.Fatalf("expected browser control effort clamp, got %+v", decision)
	}
}

func TestTaskIntakePlannerRespectsModelDecisionForShortFollowUp(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"browser_handoff_task","effortLevel":"standard","requestedOutputFormats":null,"reason":"continues visible browser work","userFacingReply":"","workKinds":["user_browser","browser_session"]}`,
	}}
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.snapshot"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{
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
	if !decision.HasWorkKind(WorkKindUserBrowser) || !decision.HasWorkKind(WorkKindBrowserSession) {
		t.Fatalf("expected browser work kinds to be preserved, got %+v", decision.WorkKinds)
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "구글 클라우드 콘솔") {
		t.Fatal("expected intake planner to receive visible context")
	}
}

func TestTaskIntakePlannerTreatsLocalArtifactConfirmationAsBoundedTask(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"needs_confirmation","taskShape":"approval_gated_task","effortLevel":"deep","requestedOutputFormats":["pdf"],"reason":"asks for generated files","userFacingReply":"승인하시겠습니까?","workKinds":["slides_artifact","file_delivery"]}`,
	}}
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.promote", "file.attach"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "너 뭐 할 수 있는지 피피티 만들어서 pdf로 보내줘",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationBoundedTask {
		t.Fatalf("expected local artifact request to be bounded, got %+v", decision)
	}
	if decision.TaskShape == TaskShapeApprovalGatedTask {
		t.Fatalf("expected non-approval task shape, got %+v", decision)
	}
	if decision.UserFacingReply != "" {
		t.Fatalf("expected confirmation reply to be cleared, got %q", decision.UserFacingReply)
	}
}

func TestTaskIntakePlannerDoesNotOverrideScheduleRefusalWithoutSelectedSkill(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"unsupported","taskShape":"scheduled_task","effortLevel":"deep","requestedOutputFormats":null,"reason":"background loops are unsupported","userFacingReply":"지원하지 않습니다."}`,
	}}
	toolRegistry := newTestToolSet([]string{"schedule.create"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "schedule.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("scheduled"), nil
	})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{
		IsEnabled:          true,
		DefaultEffortLevel: EffortLevelStandard,
	})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "1분마다 \"1분 지났습니다\"라고 보내줘",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationUnsupported || decision.TaskShape != TaskShapeScheduledTask {
		t.Fatalf("expected raw intake refusal to remain unsupported without selected skill, got %+v", decision)
	}
	if decision.UserFacingReply == "" {
		t.Fatal("expected unsupported reply to remain")
	}
}

func TestAgentKernelPromotesSelectedScheduledSkillOverIntakeRefusal(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"unsupported","taskShape":"scheduled_task","effortLevel":"deep","requestedOutputFormats":null,"reason":"background loops are unsupported","userFacingReply":"지원하지 않습니다."}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"schedule.create","toolInput":{"taskInstruction":"현재 대화에 \"죄송합니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}}`,
		finishMessageDocument("1분 간격으로 10번 보내도록 예약했습니다."),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, ""))
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{{
			Name:         "scheduled-task",
			Description:  "Create scheduled, recurring, and finite repeated messages.",
			WhenToUse:    "Use for reminders, interval messages, 1분에 한 번씩, 10번, finite repeated message, and repeat N times requests.",
			Prompt:       "Use schedule.create with taskInstruction for the run-time work, intervalSecond, repeatPolicy, and maxRunCount.",
			AllowedTools: []string{"schedule.create"},
			Source:       InstructionSource{Path: "skills/scheduled-task/SKILL.md", SkillName: "scheduled-task"},
		}}}
	})
	toolRegistry := newTestToolSet([]string{"schedule.create"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "schedule.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("scheduled"), nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            `1분에 한 번씩 나한테 "죄송합니다" 10번 해봐`,
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected promoted scheduled task: %v", errorValue)
	}
	if result.FinishMessage != "1분 간격으로 10번 보내도록 예약했습니다." {
		t.Fatalf("expected schedule final reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", "bounded_task") {
		t.Fatal("expected selected scheduled skill to promote intake")
	}
}

func TestAgentKernelPromotesSelectedArtifactSkillOverIntakeRefusal(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"unsupported","taskShape":"immediate_reply","effortLevel":"deep","requestedOutputFormats":["pptx"],"initialToolNames":["file.attach"],"reason":"previous permission failure","userFacingReply":"Gamma나 Canva를 사용하세요."}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"artifacts/deck/deck.pptx"}}`,
		finishMessageWithEvidence("deck.pptx 파일을 첨부했습니다.", "obs-001", "file.attach", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, ""))
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{{
			Name:         "simple-slides",
			Description:  "Create presentation decks, 피피티, 파워포인트, 발표자료, and PPTX files.",
			WhenToUse:    "Use for 피피티 and PPTX requests.",
			Prompt:       "Create and attach PPTX files.",
			TriggerHints: []string{"피피티", "pptx"},
			AllowedTools: []string{"terminal.run", "file.write", "file.attach"},
			Source:       InstructionSource{Path: "skills/simple-slides/SKILL.md", SkillName: "simple-slides"},
		}}}
	})
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.promote", "file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
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
		t.Fatalf("expected selected artifact skill to run despite intake refusal: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected pptx attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", "selected skill requires bounded completion evidence") {
		t.Fatal("expected intake refusal to be promoted by selected artifact skill")
	}
}

func TestAgentKernelRetriesArtifactFromOutputFormatWithoutSelectedSkill(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"unsupported","taskShape":"immediate_reply","effortLevel":"standard","requestedOutputFormats":["pptx"],"initialToolNames":["file.attach"],"responseLanguage":"ko","reason":"previous permission failure","userFacingReply":"PPTX 파일 생성은 불가능합니다."}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"artifacts/deck/deck.pptx"}}`,
		finishMessageWithEvidence("deck.pptx 파일을 첨부했습니다.", "obs-001", "file.attach", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.promote", "file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
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
		t.Fatalf("expected artifact retry to run despite intake refusal: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, events)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected pptx attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", "artifact retry recovery route") {
		t.Fatal("expected intake retry promotion event")
	}
}

func TestAgentKernelRecoversPriorTaskAttachmentContract(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","taskComplexity":"normal","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"latest message asks to deliver prior file outcome","userFacingReply":"","workKinds":[],"initialToolNames":[],"priorTaskReference":"outcome_recovery"}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("기존 작업이 이미 완료되어 파일이 준비되었습니다."),
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"artifacts/company-guide/company-guide.docx"}}`,
		finishMessageWithEvidence("company-guide.docx 파일을 첨부했습니다.", "obs-002", "file.attach", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
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
				RequiredEvidenceTools:      []string{"file.attach"},
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
			WorkKinds:              []string{WorkKindFileDelivery},
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
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","taskComplexity":"normal","effortLevel":"standard","requestedOutputFormats":["docx"],"responseLanguage":"ko","reason":"latest message asks for the prior Word file as an attachment","userFacingReply":"","workKinds":["file_delivery"],"initialToolNames":["file.attach"],"priorTaskReference":"outcome_recovery"}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("기존 작업이 이미 완료되어 파일이 준비되었습니다."),
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"artifacts/company-guide/company-guide.docx"}}`,
		finishMessageWithEvidence("company-guide.docx 파일을 첨부했습니다.", "obs-002", "file.attach", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"conversation.history", "file.read", "file.write", "terminal.run", "file.promote", "file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
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
	if taskEventsContain(events, "agent.intake", `"usedDeterministicFallback":true`) {
		t.Fatal("expected normal intake path, not deterministic fallback")
	}
}

func TestTaskIntakePlannerTreatsSupportedSitePrototypeConfirmationAsBoundedTask(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"needs_confirmation","taskShape":"approval_gated_task","effortLevel":"deep","requestedOutputFormats":null,"siteRequestEvidence":"웹사이트 하나 만들어서 배포","reason":"publishing needs approval","userFacingReply":"승인해주시겠어요?","workKinds":["site_prototype"]}`,
	}}
	toolRegistry := newTestToolSet([]string{"site.app.create", "site.app.publish"})
	for _, toolName := range toolRegistry.ListToolNames() {
		currentToolName := toolName
		toolRegistry.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{
		IsEnabled:          true,
		DefaultEffortLevel: EffortLevelStandard,
	})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "웹사이트 하나 만들어서 배포해봐",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationBoundedTask {
		t.Fatalf("expected supported site prototype request to be bounded, got %+v", decision)
	}
	if decision.TaskShape == TaskShapeApprovalGatedTask {
		t.Fatalf("expected non-approval task shape, got %+v", decision)
	}
	if decision.UserFacingReply != "" {
		t.Fatalf("expected confirmation reply to be cleared, got %q", decision.UserFacingReply)
	}
}

func TestTaskIntakePlannerIncludesTemporalContext(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"research_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"website request","userFacingReply":""}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	_ = planner.Plan(context.Background(), AgentRequest{
		Prompt:        "김인턴 구조 웹사이트 만들어줘",
		TurnStartedAt: time.Date(2026, time.May, 17, 1, 2, 3, 0, time.UTC),
		ToolSet:       newTestToolSet([]string{"site.app.create", "site.app.publish"}),
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
		`{"classification":"needs_confirmation","taskShape":"approval_gated_task","effortLevel":"deep","requestedOutputFormats":null,"reason":"destructive","userFacingReply":"승인하시겠습니까?"}`,
	}}
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.attach"})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "전체 자료를 삭제하고 새 피피티 만들어줘",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationNeedsConfirmation {
		t.Fatalf("expected destructive request to stay approval gated, got %+v", decision)
	}
}

func TestTaskIntakePlannerKeepsSyntheticConnectorVerificationQuick(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"research_task","effortLevel":"deep","requestedOutputFormats":null,"responseLanguage":"en","reason":"contains verify","userFacingReply":""}`,
	}}
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{IsEnabled: true})
	toolRegistry := newTestToolSet([]string{"web.search", "mail.message.search"})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "verify invited 1778564495",
		ToolSet: toolRegistry,
	})

	if decision.Classification != IntakeClassificationQuickReply {
		t.Fatalf("expected quick connector probe, got %+v", decision)
	}
	if decision.EffortLevel != EffortLevelQuick {
		t.Fatalf("expected quick effort, got %+v", decision)
	}
}

func TestEffortLimitProfileMapping(t *testing.T) {
	profile := EffortLimitProfileForLevel(EffortLevelDeep)

	if profile.MaxIterationCount != 180 || profile.MaxToolCallCount != 100 || profile.Duration.Minutes() != 40 {
		t.Fatalf("expected deep profile, got %+v", profile)
	}
}

func TestAgentKernelUsesIntakeBeforeRunningTools(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"needs_confirmation","taskShape":"approval_gated_task","effortLevel":"deep","requestedOutputFormats":null,"reason":"too broad","userFacingReply":"Please narrow this first."}`,
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
	if result.UserNotice != "Please narrow this first." {
		t.Fatalf("expected confirmation reply, got %q", result.UserNotice)
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
		`{"route":"clarify","classification":"needs_confirmation","taskShape":"approval_gated_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"needs output choice","userFacingReply":"","clarificationQuestion":"어떤 형식으로 만들까요?","clarificationOptions":[{"key":"A","label":"웹사이트","value":"website"},{"key":"B","label":"발표자료","value":"slides"}]}`,
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
	if !taskEventsContain(events, "ask.requested", `"kind":"choice_single"`) {
		t.Fatalf("expected choice ask event, got %+v", events)
	}
	if !taskEventsContain(events, "ask.requested", `"recommendedOptionKey":"A"`) {
		t.Fatalf("expected first option to be recommended, got %+v", events)
	}
	if len(replyLanguageModel.requests) != 0 {
		t.Fatalf("expected agent loop not to run, got %d model calls", len(replyLanguageModel.requests))
	}
}

func TestAgentKernelQuickReplyExposesToolsButAllowsToolFreeReply(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"reason":"direct answer","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("hello"),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"expensive"})
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
	requestContent := joinedMessageContent(replyLanguageModel.requests[0].Messages)
	if !strings.Contains(requestContent, "Available tool catalog") {
		t.Fatal("expected quick reply to expose tools")
	}
	if !strings.Contains(strings.Join(result.ToolNames, ","), "expensive") {
		t.Fatalf("expected quick reply result to preserve tools, got %+v", result.ToolNames)
	}
}

func TestAgentKernelRunTurnPreservesCheckpointSender(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"tool_task","effortLevel":"standard","requestedOutputFormats":null,"reason":"needs tool","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","message":"확인 중입니다.","toolName":"alpha","toolInput":{"value":"one"},"nextStepPlan":{"objective":"finish after alpha","expectedTools":[],"doneCriteria":["alpha succeeds"],"risk":"none","workingSetReason":"alpha provides the answer"}}`,
		finishMessageWithEvidence("done", "obs-002", "alpha", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"alpha"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("alpha result"), nil
	})
	checkpoints := []AgentCheckpoint{}

	result, errorValue := services.kernel.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "확인해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"alpha"},
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
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.checkpoint.sent", "alpha") {
		t.Fatal("expected checkpoint sent event")
	}
}

func TestAgentKernelQuickReplyPromotesToolFailureToRecovery(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"initialToolNames":["primary.lookup","backup.lookup"],"reason":"quick with useful tool","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"primary.lookup","toolInput":{"query":"hello"}}`,
		`{"action":"continue","toolName":"backup.lookup","toolInput":{"query":"hello"}}`,
		finishMessageWithEvidence("backup answer", "obs-003", "backup.lookup", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"primary.lookup", "backup.lookup"})
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
		`{"classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"reason":"direct answer","userFacingReply":""}`,
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
		`{"classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"initialToolNames":["math.calculate"],"responseLanguage":"ko","reason":"calculation","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"math.calculate","toolInput":{"expression":"1+1"}}`,
		finishMessageWithEvidence("2", "obs-001", "math.calculate", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"math.calculate"})
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
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.math.calculate.result", "result") {
		t.Fatal("expected calculator tool event")
	}
}

func TestAgentKernelQuickReplyUsesAskChoiceForExplicitChoiceRequest(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"expectedResults":[{"id":"interactive-choice","type":"message","description":"사용자가 직접 고를 수 있는 선택지 UI가 표시됨","required":true,"acceptanceHints":["ask.choice"]}],"responseLanguage":"ko","reason":"choice probe","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("아래 세 가지 중 하나를 선택해 주세요.\n\n1. 선택지 1\n2. 선택지 2\n3. 선택지 3"),
		`{"action":"continue","toolName":"ask.choice","toolInput":{"question":"아래 세 가지 중 하나를 선택해 주세요.","options":["선택지 1","선택지 2","선택지 3"],"recommendedOptionKey":"1","selectionMode":"single"},"nextStepPlan":{"objective":"wait for the user choice","expectedTools":[],"expectedNextResults":["사용자가 선택지를 고름"],"doneCriteria":["ask.choice is displayed"],"risk":"none","workingSetReason":"ask.choice provides the requested options"}}`,
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"ask.choice"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "ask.choice"}, func(toolContext context.Context, invocation ToolInvocation) (ToolResult, error) {
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
	if !taskEventsContain(events, "agent.completion_required", "ask.choice") {
		t.Fatalf("expected text-only finish to be rejected, got %+v", events)
	}
	if !taskEventsContain(events, "ask.requested", `"selectionMode":"single"`) {
		t.Fatalf("expected ask.choice request event, got %+v", events)
	}
	if len(replyLanguageModel.requests) != 2 {
		t.Fatalf("expected finish rejection then ask.choice action, got %d requests", len(replyLanguageModel.requests))
	}
}

func TestAgentKernelDoesNotPromoteQuickReplyOnlyBecauseSelectedSkillHasEvidenceHint(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"reason":"direct answer","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("deck created too early"),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, ""))
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{
			Skills: []SkillInstruction{{
				Name:         "simple-slides",
				Description:  "Create presentation slides.",
				WhenToUse:    "Use for 피피티 and PPTX requests.",
				Prompt:       "Create and attach PPTX files.",
				TriggerHints: []string{"피피티"},
				AllowedTools: []string{"terminal.run", "file.write", "file.attach"},
				Completion: SkillCompletion{
					RequiredEvidenceTools: []string{"file.attach"},
				},
				Source: InstructionSource{Path: "skills/simple-slides/SKILL.md", SkillName: "simple-slides"},
			}},
		}
	})
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.attach"})
	for _, toolName := range toolRegistry.ListToolNames() {
		currentToolName := toolName
		toolRegistry.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			if currentToolName == "file.attach" {
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
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", "quick_reply") {
		t.Fatal("expected quick reply intake event")
	}
}

func TestAgentKernelUsesStructuredOutputFormatsForAttachmentRequirements(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"research_task","effortLevel":"standard","outputKind":"file","requestedOutputFormats":["html"],"initialToolNames":["file.attach"],"reason":"explicit html output","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"deck.html"}}`,
		finishMessageWithEvidence("HTML 파일을 첨부했습니다: deck.html", "obs-001", "file.attach", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, ""))
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{{
			Name:         "html-attachment",
			Description:  "Attach HTML deliverables.",
			WhenToUse:    "Use for html output requests.",
			Prompt:       "Use file.attach for HTML deliverables.",
			TriggerHints: []string{"html"},
			AllowedTools: []string{"file.attach"},
			Source:       InstructionSource{Path: "skills/html-attachment/SKILL.md", SkillName: "html-attachment"},
		}}}
	})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
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

func TestAgentKernelUsesStructuredArtifactEnumForPDFAttachmentSkill(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"unsupported","taskShape":"immediate_reply","taskComplexity":"simple","effortLevel":"deep","outputKind":"site","requestedOutputFormats":["pdf"],"siteRequestEvidence":"","responseLanguage":"ko","reason":"mistaken unsupported file artifact","userFacingReply":"PDF 생성은 지원하지 않습니다.","workKinds":["file_delivery","site_prototype"],"initialToolNames":["site.app.status"],"priorTaskReference":"none"}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"artifacts/brief/quality-brief.pdf"}}`,
		finishMessageWithEvidence("PDF 파일을 첨부했습니다: quality-brief.pdf", "obs-001", "file.attach", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(staticSkillRetriever{result: SkillRetrievalResult{
		RetrievalMode: "embedding",
		IndexStatus:   "ready",
		SelectedCandidates: []SkillCandidate{
			{Name: "site-prototype", Score: 30, Reason: "embedding_similarity"},
			{Name: "pdf", Score: 8, Reason: "embedding_similarity"},
		},
	}})
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{
			{
				Name:         "site-prototype",
				Description:  "Create websites.",
				Prompt:       "Use site tools.",
				AllowedTools: []string{"site.app.create", "site.app.publish"},
				Source:       InstructionSource{Path: "skills/site-prototype/SKILL.md", SkillName: "site-prototype"},
			},
			{
				Name:         "pdf",
				Description:  "Create, verify, and attach PDF documents.",
				WhenToUse:    "Use for PDF deliverables, reports, briefs, and .pdf file artifacts.",
				Prompt:       "Use pdf tools.",
				AllowedTools: []string{"terminal.run", "file.write", "file.attach"},
				Source:       InstructionSource{Path: "skills/pdf/SKILL.md", SkillName: "pdf"},
			},
		}}
	})
	toolRegistry := newTestToolSet([]string{
		"terminal.run",
		"file.write",
		"file.attach",
		"site.app.create",
		"site.app.publish",
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/brief/quality-brief.pdf",
				Filename:   "quality-brief.pdf",
				SizeBytes:  42,
			}},
		}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "제공한 데이터만 기반으로 브리프를 pdf 파일로 만들어서 첨부해줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected structured pdf artifact task to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "quality-brief.pdf" {
		t.Fatalf("expected pdf attachment, got %+v", result.Attachments)
	}
	turnPrompt := joinedMessageContent(replyLanguageModel.requests[0].Messages)
	if !strings.Contains(turnPrompt, "Use pdf tools.") {
		t.Fatalf("expected pdf skill instructions in turn prompt, got %s", turnPrompt)
	}
	if strings.Contains(turnPrompt, "Use site tools.") {
		t.Fatalf("expected site skill to stay out of pdf attachment turn prompt, got %s", turnPrompt)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.intake", `"outputKind":"file"`) {
		t.Fatal("expected intake event to preserve structured artifact enum")
	}
	if !taskEventsContain(taskEvents, "agent.intake", `"requestedOutputFormats":["pdf"]`) {
		t.Fatal("expected intake event to preserve pdf output format")
	}
	if taskEventsContain(taskEvents, "agent.intake", WorkKindSitePrototype) {
		t.Fatal("expected unverified site work kind to be removed from pdf output intake")
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
		IsEnabled:          true,
		DefaultEffortLevel: EffortLevelStandard,
	})
	return kernelIntakeTestServices{kernel: kernel, taskRunService: taskRunService, taskEventService: taskEventService}
}

type failingLanguageModel struct{}

func (failingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("model failed")
}

func (failingLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, errors.New("model failed")
}
