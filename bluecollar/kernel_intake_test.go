package bluecollar

import (
	"context"
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/model"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

func TestAgentKernelPreservesScheduledIntakeRefusalAfterSkillSelection(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"give_up","classification":"unsupported","taskShape":"immediate_reply","level":"medium","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"background loops are unsupported","userFacingReply":"지원하지 않습니다."}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"schedule_create","toolInput":{"taskInstruction":"현재 대화에 \"죄송합니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}}`,
		finishMessageWithEvidence("1분 간격으로 10번 보내도록 예약했습니다.", "obs-001", "schedule_create", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(staticSkillRetriever{result: SkillRetrievalResult{SelectedCandidates: []SkillCandidate{{Name: "scheduled-task", Score: 1, Reason: "test"}}}})
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{{
			Name:           "scheduled-task",
			Description:    "Create scheduled, recurring, finite repeated reminders, interval messages, 1분에 한 번씩, and repeat N times.",
			Prompt:         "Use schedule_create with taskInstruction for the run-time work, intervalSecond, repeatPolicy, and maxRunCount.",
			ToolReferences: []string{"schedule_create"},
			Source:         InstructionSource{Path: "skills/scheduled-task/SKILL.md", SkillName: "scheduled-task"},
		}}}
	})
	toolRegistry := newTestCapabilityToolSet([]string{"schedule_create"})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            `1분에 한 번씩 나한테 "죄송합니다" 10번 해봐`,
		ToolSet:           toolRegistry,
	}))
	if errorValue != nil {
		t.Fatalf("expected unsupported intake to complete: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusBlocked {
		t.Fatalf("expected blocked scheduled task, got %s", result.TaskRun.Status)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.action", "continue") {
		t.Fatal("expected no task execution after unsupported router decision")
	}
}

func TestAgentKernelSelectsArtifactSkillOnceAfterRouting(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":["pptx"],"initialToolNames":["file_deliver"],"reason":"create and deliver the requested presentation","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"artifacts/deck/deck.pptx"}}`,
		finishMessageWithEvidence("deck.pptx 파일을 첨부했습니다.", "obs-001", "file_deliver", 0),
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
			Name:           "presentation",
			Description:    "Create presentation decks, 피피티, 파워포인트, 발표자료, and PPTX files.",
			Prompt:         "Create and attach PPTX files.",
			ToolReferences: []string{"terminal_run", "file_write", "file_deliver"},
			Source:         InstructionSource{Path: "skills/presentation/SKILL.md", SkillName: "presentation"},
		}}}
	})
	toolRegistry := newTestToolSet([]string{"terminal_run", "file_write", "file.promote", "file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath: "/workspace/private/people/person-1/artifacts/deck/deck.pptx",
				Filename:   "deck.pptx",
			}},
		}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "아까 피피티 다시 해봐",
		ToolSet:           toolRegistry,
	}))
	if errorValue != nil {
		t.Fatalf("expected routed artifact task to run: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
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
	if !stringSliceContains(selectionContract.RequiredEvidenceTools, toolcontract.FileDeliverToolName) || !stringSliceContains(selectionContract.RequiredAttachmentSuffixes, ".pptx") || selectionContract.ArtifactRequirement != ArtifactRequirementRequired {
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

func TestAgentKernelSelectsSkillForTypedToolContract(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"queries":[{"description":"Create a task."}]}`,
		`{"selectedSkillNames":["internkim-flow"],"rejectedSkillNames":[],"requiredNextToolNames":["task_add"],"expectedEvidence":["task_add"],"unmetPreconditions":[],"reason":"The task contract requires task creation."}`,
	}}
	services := newKernelIntakeTestServices(&sequenceLanguageModel{}, intakeLanguageModel)
	skillRetriever := &countingSkillRetriever{result: SkillRetrievalResult{
		SelectedCandidates: []SkillCandidate{{Name: "internkim-flow", Score: 1}},
	}}
	services.kernel.UseSkillRetriever(skillRetriever)
	toolSet := newTestCapabilityToolSet([]string{"task_add"})
	request := AgentRequest{
		Prompt:  "업무를 추가해줘",
		ToolSet: toolSet,
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			ArtifactRequirement:   ArtifactRequirementNone,
			RequiredEvidenceTools: []string{"task_add"},
		}},
	}
	bundle, _ := services.kernel.selectInstructionBundleForResolvedRequest(context.Background(), InstructionBundle{Skills: []SkillInstruction{{
		Name:           "internkim-flow",
		Description:    "Manage tasks.",
		Prompt:         "Use task.add.",
		ToolReferences: []string{"task_add"},
	}}}, request, IntakeDecision{
		Classification:   IntakeClassificationBoundedTask,
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 2,
	})

	if bundle.RetrievalMode == "tool_contract" || bundle.IndexStatus == "bypassed" {
		t.Fatalf("expected normal skill selection for typed tool contract, got %+v", bundle)
	}
	if skillRetriever.searchCount != 1 || len(intakeLanguageModel.requests) == 0 {
		t.Fatal("expected model-guided skill retrieval")
	}
	if !strings.Contains(bundle.Prompt, "Use task.add.") {
		t.Fatal("expected selected skill body to be loaded")
	}
}

func TestAgentKernelPreservesUnsupportedArtifactWithoutSelectedSkill(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"give_up","classification":"unsupported","taskShape":"immediate_reply","level":"low","estimatedMinutes":1,"requestedOutputFormats":["pptx"],"initialToolNames":["file_deliver"],"responseLanguage":"ko","reason":"previous permission failure","userFacingReply":"PPTX 파일 생성은 불가능합니다."}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"artifacts/deck/deck.pptx"}}`,
		finishMessageWithEvidence("deck.pptx 파일을 첨부했습니다.", "obs-001", "file_deliver", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"terminal_run", "file_write", "file.promote", "file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath: "/workspace/private/people/person-1/artifacts/deck/deck.pptx",
				Filename:   "deck.pptx",
			}},
		}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "다시 해봐 이제 될 거야",
		ToolSet:           toolRegistry,
	}))
	if errorValue != nil {
		t.Fatalf("expected unsupported intake to complete: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusBlocked {
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
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"artifacts/company-guide/company-guide.docx"}}`,
		finishMessageWithEvidence("company-guide.docx 파일을 첨부했습니다.", "obs-002", "file_deliver", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath: "/workspace/private/people/person-1/artifacts/company-guide/company-guide.docx",
				Filename:   "company-guide.docx",
			}},
		}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "direct-1",
		Prompt:            "전달해줘야지 그럼",
		ToolSet:           toolRegistry,
		PriorTask: PriorTaskContext{
			TaskRunID: "88894f",
			Status:    string(taskstate.TaskStatusFailed),
			Prompt:    "기업 문서 가이드를 워드 파일로 만들어줘",
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools:      []string{"file_deliver"},
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
	}))

	if errorValue != nil {
		t.Fatalf("expected prior task attachment recovery to complete: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
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
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":["docx"],"responseLanguage":"ko","reason":"latest message asks for the prior Word file as an attachment","userFacingReply":"","initialToolNames":["file_deliver"],"priorTaskReference":"outcome_recovery"}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("기존 작업이 이미 완료되어 파일이 준비되었습니다."),
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"artifacts/company-guide/company-guide.docx"}}`,
		finishMessageWithEvidence("company-guide.docx 파일을 첨부했습니다.", "obs-002", "file_deliver", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"conversation_history", "file_read", "file_write", "terminal_run", "file.promote", "file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath: "/workspace/private/people/person-1/artifacts/company-guide/company-guide.docx",
				Filename:   "company-guide.docx",
			}},
		}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "direct-1",
		Prompt:            "링크로 전달된 적 없어. 첨부파일로 줘야지 그리고.",
		ToolSet:           toolRegistry,
		PriorTask: PriorTaskContext{
			TaskRunID: "88894f",
			Status:    string(taskstate.TaskStatusCompleted),
			Prompt:    "기업 문서 가이드를 워드 파일로 만들어줘",
			Result:    "요청하신 작업이 이미 성공적으로 완료되었습니다.",
		},
	}))

	if errorValue != nil {
		t.Fatalf("expected fallback prior task attachment recovery to complete: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
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

func TestAgentKernelUsesIntakeBeforeRunningTools(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"clarify","classification":"needs_confirmation","taskShape":"approval_gated_task","level":"medium","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"ambiguous target","clarificationQuestion":"Which one do you mean?","clarificationOptions":[{"key":"A","label":"First","value":"First"},{"key":"B","label":"Second","value":"Second"}],"userFacingReply":"Which one do you mean?"}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("should not run"),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"expensive"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "expensive"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("expensive result"), nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do the entire thing",
		ToolSet:           toolRegistry,
	}))
	if errorValue != nil {
		t.Fatalf("expected intake-only result: %v", errorValue)
	}
	if result.UserNotice != "Which one do you mean?" {
		t.Fatalf("expected clarification reply, got %q", result.UserNotice)
	}
	if result.TaskRun.Status != taskstate.TaskStatusWaitingUserInput {
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

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "소개 자료 만들어줘",
		ToolSet:           newTestToolSet([]string{toolcontract.AskInputToolName}),
	}))
	if errorValue != nil {
		t.Fatalf("expected clarify result: %v", errorValue)
	}

	if result.UserNotice != "어떤 형식으로 만들까요?" {
		t.Fatalf("expected clarification question, got %q", result.UserNotice)
	}
	if result.TaskRun.Status != taskstate.TaskStatusWaitingUserInput {
		t.Fatalf("expected waiting user input, got %s", result.TaskRun.Status)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "ask.requested", `"kind":"ask_input"`) {
		t.Fatalf("expected option-bearing ask_input event, got %+v", events)
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
	toolRegistry := newTestToolSet([]string{"expensive", "ask_input"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "expensive"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("expensive result"), nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "hello",
		ToolSet:           toolRegistry,
	}))
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
	if strings.Contains(actionSchema, toolcontract.AskInputToolName) {
		t.Fatalf("expected quick reply schema to hide ask_input without typed interaction, got %s", actionSchema)
	}
	if !strings.Contains(strings.Join(result.ToolNames, ","), "expensive") {
		t.Fatalf("expected quick reply result to preserve tools, got %+v", result.ToolNames)
	}
}

func TestAgentKernelRunTurnPreservesCheckpointSender(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"initialToolNames":["alpha"],"reason":"needs tool","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","message":"확인 중입니다.","toolName":"alpha","toolInput":{"value":"one"}}`,
		finishMessageWithEvidence("done", "obs-002", "alpha", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "alpha"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("alpha result"), nil
	})
	checkpoints := []AgentCheckpoint{}

	precomputedDecision := TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeMaintenanceTask,
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
		Reason:           "needs tool",
		UserFacingReply:  "",
		InitialToolNames: []string{"alpha"},
	}
	result, errorValue := services.kernel.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "확인해줘",
		ToolSet:                    toolRegistry,
		PrecomputedTurnDecision:    &precomputedDecision,
		IsPrecomputedDecisionExact: true,
		CheckpointSender: func(_ context.Context, checkpoint AgentCheckpoint) error {
			checkpoints = append(checkpoints, checkpoint)
			return nil
		},
	})
	if errorValue != nil {
		t.Fatalf("expected task to complete: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
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
		`{"route":"start_task","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"initialToolNames":["primary_lookup","backup_lookup"],"reason":"quick with useful tool","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"primary_lookup","toolInput":{"query":"hello"}}`,
		`{"action":"continue","toolName":"backup_lookup","toolInput":{"query":"hello"}}`,
		finishMessageWithEvidence("backup answer", "obs-003", "backup_lookup", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestCapabilityToolSet([]string{"primary_lookup", "backup_lookup"})
	primaryCallCount := 0
	backupCallCount := 0
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "primary_lookup", SideEffectClass: toolcontract.ToolSideEffectRead}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		primaryCallCount++
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "primary_lookup", "primary lookup failed"), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "backup_lookup", SideEffectClass: toolcontract.ToolSideEffectRead}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		backupCallCount++
		return testToolSuccess("backup result"), nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "lookup hello",
		ToolSet:           toolRegistry,
	}))
	if errorValue != nil {
		t.Fatalf("expected quick recovery: %v", errorValue)
	}
	if result.FinishMessage != "backup answer" {
		t.Fatalf("expected recovered final reply, got finish=%q notice=%q status=%s failure=%q", result.FinishMessage, result.UserNotice, result.TaskRun.Status, result.TaskRun.FailureReason)
	}
	if primaryCallCount != 1 || backupCallCount != 1 {
		t.Fatalf("expected one primary failure and one backup recovery, got primary=%d backup=%d", primaryCallCount, backupCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.recovery_attempt", "inspection") {
		t.Fatal("expected the read-only backup lookup to be recorded as an inspection recovery")
	}
}

func TestAgentKernelQuickReplyFailureDoesNotInventToolFailure(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"direct answer","userFacingReply":""}`,
	}}
	services := newKernelIntakeTestServices(failingLanguageModel{}, intakeLanguageModel)

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "1+1=",
	}))
	if errorValue != nil {
		t.Fatalf("expected direct reply failure result: %v", errorValue)
	}
	if strings.Contains(strings.ToLower(result.UserNotice), "calculation tool") || strings.Contains(strings.ToLower(result.UserNotice), "data processing") {
		t.Fatalf("expected no invented tool failure, got %q", result.UserNotice)
	}
	if result.ReplySuppressed || !strings.Contains(result.UserNotice, "llm action failed: language model unavailable") {
		t.Fatalf("expected raw model error reply, got reply=%q suppressed=%v", result.UserNotice, result.ReplySuppressed)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "raw_error") {
		t.Fatal("expected raw error failure reply event")
	}
}

func TestAgentKernelQuickReplyCanUseInitialTool(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"initialToolNames":["schedule_list"],"responseLanguage":"ko","reason":"schedule question","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"schedule_list","toolInput":{}}`,
		finishMessageWithEvidence("오늘 등록된 일정은 없어요.", "obs-001", "schedule_list", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestCapabilityToolSet([]string{"schedule_list"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "schedule_list"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess(`{"schedules":[]}`), nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "오늘 일정 뭐 있어?",
		ToolSet:           toolRegistry,
	}))
	if errorValue != nil {
		t.Fatalf("expected quick initial-tool reply: %v", errorValue)
	}
	if result.FinishMessage != "오늘 등록된 일정은 없어요." {
		t.Fatalf("expected initial-tool final reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.schedule_list.result", "schedule_list") {
		t.Fatal("expected initial tool event")
	}
}

func TestAgentKernelQuickReplyUsesAskInputForExplicitChoiceRequest(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"expectedResults":[{"id":"interactive-choice","type":"message","description":"사용자가 직접 고를 수 있는 선택지 UI가 표시됨","required":true,"acceptanceHints":["ask_input"]}],"responseLanguage":"ko","reason":"choice probe","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("아래 세 가지 중 하나를 선택해 주세요.\n\n1. 선택지 1\n2. 선택지 2\n3. 선택지 3"),
		`{"action":"continue","toolName":"ask_input","toolInput":{"question":"아래 세 가지 중 하나를 선택해 주세요.","options":["선택지 1","선택지 2","선택지 3"],"recommendedOptionKey":"1","selectionMode":"single"}}`,
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := newTestToolSet([]string{"ask_input"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "ask_input"}, func(toolContext context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		taskRunID := TaskRunIDFromContext(toolContext)
		if taskRunID == "" {
			return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "ask_choice", "missing task run"), nil
		}
		_, errorValue := services.taskRunService.PauseTaskRun(taskRunID, taskstate.TaskStatusWaitingUserInput, "아래 세 가지 중 하나를 선택해 주세요.")
		if errorValue != nil {
			return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "ask_choice", errorValue.Error()), nil
		}
		services.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", string(invocation.Input))
		return testToolSuccess(`{"kind":"choice_single","question":"아래 세 가지 중 하나를 선택해 주세요."}`), nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "나한테 1 2 3 선택지 줘봐. 잘 동작하는지 테스트해보게",
		ToolSet:           toolRegistry,
	}))
	if errorValue != nil {
		t.Fatalf("expected choice request: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusWaitingUserInput {
		t.Fatalf("expected waiting user input, got %s", result.TaskRun.Status)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "agent.completion_required", "ask_input") {
		t.Fatalf("expected text-only finish to be rejected, got %+v", events)
	}
	if !taskEventsContain(events, "ask.requested", `"selectionMode":"single"`) {
		t.Fatalf("expected ask_input request event, got %+v", events)
	}
	if len(replyLanguageModel.requests) != 2 {
		t.Fatalf("expected finish rejection then ask_input action, got %d requests", len(replyLanguageModel.requests))
	}
}

func TestAgentKernelPreservesQuickReplyAfterSkillSelection(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"reason":"direct answer","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("deck created too early"),
		`{"action":"continue","message":"deck attached: deck.pptx","toolName":"file_deliver","toolInput":{"path":"deck.pptx"}}`,
		finishMessageWithEvidence("deck attached: deck.pptx", "obs-003", "file_deliver", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(staticSkillRetriever{result: SkillRetrievalResult{SelectedCandidates: []SkillCandidate{{Name: "presentation", Score: 1, Reason: "test"}}}})
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{
			Skills: []SkillInstruction{{
				Name:           "presentation",
				Description:    "Create presentation slides, 피피티, and PPTX files.",
				Prompt:         "Create and attach PPTX files.",
				ToolReferences: []string{"terminal_run", "file_write", "file_deliver"},
				Source:         InstructionSource{Path: "skills/presentation/SKILL.md", SkillName: "presentation"},
			}},
		}
	})
	toolRegistry := newTestToolSet([]string{"terminal_run", "file_write", "file_deliver"})
	for _, toolName := range toolRegistry.ListToolNames() {
		currentToolName := toolName
		registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: currentToolName}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			if currentToolName == "file_deliver" {
				return toolcontract.ToolResult{
					Output: toolcontract.ToolOutput{Content: "attached"},
					Attachments: []toolcontract.FileAttachment{{
						DevicePath: "artifacts/deck/deck.pptx",
						Filename:   "deck.pptx",
					}},
				}, nil
			}
			return testToolSuccess("ok"), nil
		})
	}

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "너 뭐 할 수 있는지 피피티 만들어서 보내줘봐",
		ToolSet:           toolRegistry,
	}))
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
		`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":["html"],"initialToolNames":["file_deliver"],"reason":"explicit html output","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"deck.html"}}`,
		finishMessageWithEvidence("HTML 파일을 첨부했습니다: deck.html", "obs-001", "file_deliver", 0),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(NewEmbeddingSkillRetriever(nil, ""))
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{{
			Name:           "html-attachment",
			Description:    "Attach HTML deliverables for HTML output requests.",
			Prompt:         "Use file_deliver for HTML deliverables.",
			ToolReferences: []string{"file_deliver"},
			Source:         InstructionSource{Path: "skills/html-attachment/SKILL.md", SkillName: "html-attachment"},
		}}}
	})
	toolRegistry := newTestToolSet([]string{"file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath: "artifacts/deck/deck.html",
				Filename:   "deck.html",
				SizeBytes:  12,
			}},
		}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), services.kernel, AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "html만 주면 돼",
		ToolSet:           toolRegistry,
	}))
	if errorValue != nil {
		t.Fatalf("expected structured output format task to complete: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
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

type kernelIntakeTestServices struct {
	kernel           *AgentKernel
	taskRunService   *taskstate.TaskRunService
	taskEventService *taskstate.TaskEventService
}

func newKernelIntakeTestServices(replyLanguageModel model.LanguageModelProvider, intakeLanguageModel model.LanguageModelProvider) kernelIntakeTestServices {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskStepService := taskstate.NewTaskStepService()
	kernel := NewAgentKernel(taskRunService, taskStepService)
	kernel.UseLanguageModelProvider(replyLanguageModel)
	kernel.UseIntakeLanguageModelProvider(intakeLanguageModel)
	kernel.UseIntakeOptions(IntakeOptions{
		IsEnabled:        true,
		DefaultTaskLevel: TaskLevelLow,
	})
	return kernelIntakeTestServices{kernel: kernel, taskRunService: taskRunService, taskEventService: taskEventService}
}
