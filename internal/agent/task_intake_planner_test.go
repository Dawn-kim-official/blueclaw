package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

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
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected one intake model call, got %d", len(languageModel.requests))
	}
	if languageModel.requests[0].StructuredOutputSchema.Name != "blueclaw_task_intake_effort" {
		t.Fatalf("expected intake schema, got %q", languageModel.requests[0].StructuredOutputSchema.Name)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"taskShape"`) {
		t.Fatalf("expected task shape in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, `"requestedOutputFormats"`) {
		t.Fatalf("expected requested output formats in intake schema, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), `requestedOutputFormats should be ["html"], not ["html","pptx"]`) {
		t.Fatal("expected intake prompt to disambiguate html presentation requests from pptx file requests")
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
}

func TestTaskIntakePlannerFallsBackDeterministically(t *testing.T) {
	planner := NewTaskIntakePlanner(failingLanguageModel{}, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{Prompt: "please analyze the whole repo"})

	if decision.Classification != IntakeClassificationNeedsConfirmation {
		t.Fatalf("expected confirmation fallback, got %q", decision.Classification)
	}
	if decision.TaskShape != TaskShapeApprovalGatedTask {
		t.Fatalf("expected approval-gated fallback shape, got %+v", decision)
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

func TestTaskIntakePlannerPromotesBrowserFollowUpDespiteQuickModelDecision(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"reason":"looks conversational","userFacingReply":""}`,
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
		t.Fatalf("expected browser follow-up to stay bounded, got %+v", decision)
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "구글 클라우드 콘솔") {
		t.Fatal("expected intake planner to receive visible context")
	}
}

func TestTaskIntakePlannerTreatsLocalArtifactConfirmationAsBoundedTask(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"needs_confirmation","taskShape":"approval_gated_task","effortLevel":"deep","requestedOutputFormats":["pdf"],"reason":"asks for generated files","userFacingReply":"승인하시겠습니까?"}`,
	}}
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.attach"})
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
		return ToolResult{Content: "scheduled"}, nil
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
		`{"action":"call_tool","toolName":"schedule.create","toolInput":{"prompt":"죄송합니다.","executionMode":"message","kind":"interval","intervalSecond":60,"maxRunCount":10,"timeZone":"Asia/Seoul"}}`,
		finalReplyDocument("1분 간격으로 10번 보내도록 예약했습니다."),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, ""))
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{{
			Name:         "scheduled-task",
			Description:  "Create scheduled, recurring, and finite repeated messages.",
			WhenToUse:    "Use for reminders, interval messages, 1분에 한 번씩, 10번, finite repeated message, and repeat N times requests.",
			Prompt:       "Use schedule.create with executionMode message for exact repeated messages, intervalSecond, and maxRunCount.",
			AllowedTools: []string{"schedule.create"},
			Source:       InstructionSource{Path: "skills/scheduled-task/SKILL.md", SkillName: "scheduled-task"},
		}}}
	})
	toolRegistry := newTestToolSet([]string{"schedule.create"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "schedule.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "scheduled"}, nil
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
	if result.FinalReply != "1분 간격으로 10번 보내도록 예약했습니다." {
		t.Fatalf("expected schedule final reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", "bounded_task") {
		t.Fatal("expected selected scheduled skill to promote intake")
	}
}

func TestTaskIntakePlannerTreatsSupportedSitePrototypeConfirmationAsBoundedTask(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"needs_confirmation","taskShape":"approval_gated_task","effortLevel":"deep","requestedOutputFormats":null,"reason":"publishing needs approval","userFacingReply":"승인해주시겠어요?"}`,
	}}
	toolRegistry := newTestToolSet([]string{"site.app.create", "site.app.publish"})
	for _, toolName := range toolRegistry.ListToolNames() {
		currentToolName := toolName
		toolRegistry.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolResult{Content: "ok"}, nil
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

func TestTaskIntakePlannerPromotesDeepAndExtendedRequests(t *testing.T) {
	planner := NewTaskIntakePlanner(failingLanguageModel{}, IntakeOptions{IsEnabled: true})
	toolRegistry := newTestToolSet([]string{"terminal.run"})

	deepDecision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "꼼꼼히 디버그하고 검증해줘",
		ToolSet: toolRegistry,
	})
	if deepDecision.EffortLevel != EffortLevelDeep {
		t.Fatalf("expected deep effort, got %+v", deepDecision)
	}

	extendedDecision := planner.Plan(context.Background(), AgentRequest{
		Prompt:  "backup restore migration workflow를 처리해줘",
		ToolSet: toolRegistry,
	})
	if extendedDecision.EffortLevel != EffortLevelExtended {
		t.Fatalf("expected extended effort, got %+v", extendedDecision)
	}
}

func TestEffortLimitProfileMapping(t *testing.T) {
	profile := EffortLimitProfileForLevel(EffortLevelDeep)

	if profile.MaxIterationCount != 90 || profile.MaxToolCallCount != 32 || profile.Duration.Minutes() != 30 {
		t.Fatalf("expected deep profile, got %+v", profile)
	}
}

func TestAgentKernelUsesIntakeBeforeRunningTools(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"needs_confirmation","taskShape":"approval_gated_task","effortLevel":"deep","requestedOutputFormats":null,"reason":"too broad","userFacingReply":"Please narrow this first."}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finalReplyDocument("should not run"),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"expensive"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "expensive"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "expensive result"}, nil
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
	if result.FinalReply != "Please narrow this first." {
		t.Fatalf("expected confirmation reply, got %q", result.FinalReply)
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

func TestAgentKernelQuickReplyExposesToolsButAllowsToolFreeReply(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"reason":"direct answer","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finalReplyDocument("hello"),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"expensive"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "expensive"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "expensive result"}, nil
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
	if result.FinalReply != "hello" {
		t.Fatalf("expected final reply, got %q", result.FinalReply)
	}
	if len(replyLanguageModel.requests) != 1 {
		t.Fatalf("expected one direct reply request, got %d", len(replyLanguageModel.requests))
	}
	requestContent := joinedMessageContent(replyLanguageModel.requests[0].Messages)
	if !strings.Contains(requestContent, "Available tools") {
		t.Fatal("expected quick reply to expose tools")
	}
	if !strings.Contains(strings.Join(result.ToolNames, ","), "expensive") {
		t.Fatalf("expected quick reply result to preserve tools, got %+v", result.ToolNames)
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
	if strings.Contains(strings.ToLower(result.FinalReply), "calculation tool") || strings.Contains(strings.ToLower(result.FinalReply), "data processing") {
		t.Fatalf("expected no invented tool failure, got %q", result.FinalReply)
	}
	if result.FinalReply != "" || !result.ReplySuppressed {
		t.Fatalf("expected no deterministic fallback reply, got reply=%q suppressed=%v", result.FinalReply, result.ReplySuppressed)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.llm_unavailable", "model failed") {
		t.Fatal("expected LLM unavailable diagnostic event")
	}
}

func TestAgentKernelQuickReplyCanUseCalculatorTool(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calculation","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"math.calculate","toolInput":{"expression":"1+1"}}`,
		finalReplyWithEvidence("2", "obs-001", "math.calculate", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"math.calculate"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "math.calculate"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"expression":"1+1","result":"2"}`}, nil
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
	if result.FinalReply != "2" {
		t.Fatalf("expected calculator final reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.math.calculate.result", "result") {
		t.Fatal("expected calculator tool event")
	}
}

func TestAgentKernelPromotesQuickReplyWhenSelectedSkillNeedsTools(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"reason":"direct answer","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finalReplyDocument("deck created too early"),
		`{"action":"call_tool","toolName":"file.attach","toolInput":{"path":"deck.pptx"}}`,
		finalReplyWithEvidence("deck created", "obs-002", "file.attach", 0),
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
					Content: "attached",
					Attachments: []FileAttachment{{
						DevicePath: "/workspace/deck.pptx",
						Filename:   "deck.pptx",
					}},
				}, nil
			}
			return ToolResult{Content: "ok"}, nil
		})
	}

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "너 뭐 할 수 있는지 피피티 만들어서 보내줘봐",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected promoted bounded task: %v", errorValue)
	}
	if !strings.Contains(result.FinalReply, "deck.pptx") {
		t.Fatalf("expected final reply, got %q", result.FinalReply)
	}
	requestContent := joinedMessageContent(replyLanguageModel.requests[0].Messages)
	if !strings.Contains(requestContent, "Available tools") {
		t.Fatal("expected promoted request to expose tools")
	}
	if !strings.Contains(requestContent, "Create and attach PPTX files.") {
		t.Fatal("expected selected skill instructions")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", "bounded_task") {
		t.Fatal("expected promoted bounded task intake event")
	}
}

func TestAgentKernelUsesStructuredOutputFormatsForAttachmentRequirements(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"research_task","effortLevel":"standard","requestedOutputFormats":["html"],"reason":"explicit html output","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"file.attach","toolInput":{"path":"deck.html"}}`,
		finalReplyWithEvidence("HTML 파일을 첨부했습니다: deck.html", "obs-001", "file.attach", 0),
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
			Content: "file attached",
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/deck.html",
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
	return kernelIntakeTestServices{kernel: kernel, taskEventService: taskEventService}
}

type failingLanguageModel struct{}

func (failingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("model failed")
}

func (failingLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, errors.New("model failed")
}
