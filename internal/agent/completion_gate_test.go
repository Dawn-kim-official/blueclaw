package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/task"
)

func TestCompletionGateRejectsSatisfiedFinishWithUnresolvedFailureDebt(t *testing.T) {
	goalSatisfied := true
	failedObservation := newFailureObservation("obs-001", "continue", "file.read", "permission denied", FailurePermissionDenied, FailureCodes.AccessDenied, "file_read")
	failedObservation.ToolInputKey = "file.read\x00{\"path\":\"restricted.txt\"}"
	result := validateCompletionGate(
		nil,
		[]turnObservation{failedObservation},
		nil,
		turnActionDocument{
			Action:             "finish",
			Message:            "작업을 완료했습니다.",
			GoalStatus:         "satisfied",
			GoalSatisfied:      &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{},
			QualityReview:      []qualityReviewItem{},
		},
	)
	if result.IsSatisfied {
		t.Fatal("expected completion gate to reject unresolved failure debt")
	}
	if !strings.Contains(result.Message, "FailureDebt") {
		t.Fatalf("expected failure debt guidance, got %q", result.Message)
	}
}

func TestCompletionGateRejectsFinishMessageThatRepeatsSentCheckpoint(t *testing.T) {
	goalSatisfied := true
	checkpoint := newContentObservation("obs-001", "checkpoint", "", `{"message":"날씨 알림 일정을 설정합니다.","status":"sent"}`)
	result := validateCompletionGate(nil, []turnObservation{checkpoint}, nil, turnActionDocument{
		Action:        "finish",
		Message:       "날씨 알림 일정을 설정합니다.",
		GoalStatus:    "satisfied",
		GoalSatisfied: &goalSatisfied,
	})

	if result.IsSatisfied {
		t.Fatal("expected repeated checkpoint wording to reject finish")
	}
	if !strings.Contains(result.Message, "fresh completed-result") {
		t.Fatalf("expected completed-result guidance, got %q", result.Message)
	}
}

func TestAgentTurnRunnerClassifiesFailedFinishAsFailed(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"set_quality_criteria","qualityCriteria":["요청한 작업이 실제로 완료됨"],"goalStatus":"in_progress","goalSatisfied":false}`,
		`{"action":"continue","toolName":"unstable","toolInput":{}}`,
		`{"action":"finish","message":"요청을 수행하지 못했습니다.","goalStatus":"satisfied","goalSatisfied":true,"failureResolution":"no_tool_fallback","completionEvidenceIDs":[],"qualityReview":[{"id":"criterion-1","passed":false,"evidenceIDs":[]}]}`,
		failureReportDocument("요청 실행이 실패했습니다.", "unstable", "{}", FailureCodes.OperationFailed.String(), "unstable", "request failed"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		MaxIterationCount: 6,
		RecoveryBudget:    exhaustedRecoveryBudgetForTest(),
	})
	toolRegistry := newTestToolSet([]string{"unstable"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "unstable"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "unstable", "request failed"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "요청을 실행해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})

	if errorValue != nil {
		t.Fatalf("expected an accurately classified task result: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed status, got %s", result.TaskRun.Status)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(taskEvents, "task.completed", "") {
		t.Fatal("expected failed finish never to emit task.completed")
	}
	if !taskEventsContain(taskEvents, "agent.completion_required", "qualityReview") {
		t.Fatal("expected failed finish to be rejected before persistence")
	}
}

func TestFinalizeSatisfiedTurnDoesNotIgnoreCompletionPersistenceFailure(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{finishMessageDocument("완료했습니다.")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", "작업해줘")
	services.taskRunService.UseRepository(completionFailureRepository{
		errorValue: errors.New("database unavailable token=secret-value"),
	})

	result, isFinalized := services.runner.finalizeSatisfiedTurn(
		context.Background(),
		taskRun.TaskRunID,
		AgentTurnRequest{Prompt: "작업해줘"},
		nil,
		nil,
		nil,
		ExecutionState{},
	)

	if !isFinalized {
		t.Fatal("expected persistence failure to terminate with an accurate failed result")
	}
	if result.TaskRun.Status != task.TaskStatusFailed || strings.TrimSpace(result.UserNotice) == "" || result.FinishMessage != "" {
		t.Fatalf("expected failed user-visible result without success reply, got %+v", result)
	}
	taskEvents := services.taskEventService.ListTaskEvent(taskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.completion_persist_failed", "database unavailable") {
		t.Fatal("expected completion persistence failure event")
	}
	if taskEventsContain(taskEvents, "agent.completion_persist_failed", "secret-value") {
		t.Fatal("expected diagnostic event to redact secrets")
	}
}

func TestAgentTurnRunnerReportsCompletionPersistenceFailureAsFailed(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			finishMessageDocument("완료했습니다."),
			recoveryDecisionDocument("완료 상태 저장 실패", "작업 결과는 생성됨", "잠시 후 다시 시도", "저장 실패를 알림"),
		},
		textResponses: []string{"작업 결과를 저장하지 못해 완료 처리되지 않았습니다. 잠시 후 다시 시도해 주세요."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	repository := newCompletionTransitionFailureRepository(errors.New("database unavailable token=secret-value"))
	services.taskRunService.UseRepository(repository)

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "작업해줘",
		ResponseLanguage:  "ko",
	})

	if errorValue != nil {
		t.Fatalf("expected failed result without runner error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed status, got %s", result.TaskRun.Status)
	}
	if strings.TrimSpace(result.UserNotice) == "" || result.FinishMessage != "" || result.ReplySuppressed {
		t.Fatalf("expected visible failure notice and no success reply, got %+v", result)
	}
	if repository.completedTransitionCount != 1 || repository.failedTransitionCount != 1 {
		t.Fatalf("expected failed completion followed by failed-state persistence, got %+v", repository)
	}
}

func TestTerminalNoToolsFinishReportsCompletionPersistenceFailureAsFailed(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			recoveryDecisionDocument("완료 상태 저장 실패", "작업 결과는 생성됨", "잠시 후 다시 시도", "저장 실패를 알림"),
		},
		textResponses: []string{"작업 결과를 저장하지 못해 완료 처리되지 않았습니다. 잠시 후 다시 시도해 주세요."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	repository := newCompletionTransitionFailureRepository(errors.New("database unavailable"))
	services.taskRunService.UseRepository(repository)
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", "작업해줘")
	if _, errorValue := services.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "test"); errorValue != nil {
		t.Fatalf("expected running task fixture: %v", errorValue)
	}
	goalSatisfied := true

	result, isComplete, validationMessage := services.runner.completeTerminalNoToolsFinish(
		context.Background(),
		taskRun.TaskRunID,
		taskRun.TaskRunID+":finish",
		AgentTurnRequest{Prompt: "작업해줘", ResponseLanguage: "ko"},
		&agentTaskState{},
		turnActionDocument{
			Action:             "finish",
			Message:            "완료했습니다.",
			GoalStatus:         "satisfied",
			GoalSatisfied:      &goalSatisfied,
			FailureResolution:  failureResolutionNoToolFallback,
			CompletionEvidence: []completionEvidenceReference{},
		},
	)

	if !isComplete || validationMessage != "" {
		t.Fatalf("expected terminal failure result, complete=%v validation=%q", isComplete, validationMessage)
	}
	if result.TaskRun.Status != task.TaskStatusFailed || strings.TrimSpace(result.UserNotice) == "" || result.FinishMessage != "" {
		t.Fatalf("expected failed terminal result without success reply, got %+v", result)
	}
}

func TestCompletionGateRequiresEmptyRemainingWork(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionGate(nil, nil, nil, turnActionDocument{
		Action:             "finish",
		Message:            "작업을 완료했습니다.",
		GoalStatus:         "satisfied",
		GoalSatisfied:      &goalSatisfied,
		CompletionEvidence: []completionEvidenceReference{},
		QualityReview:      []qualityReviewItem{},
		RemainingWork:      "still pending",
	})
	if result.IsSatisfied || result.Message != "finish requires remainingWork to be empty" {
		t.Fatalf("expected nonempty remainingWork to reject finish, got %+v", result)
	}
}

func TestCompletionGateRejectsExternalSendFinishWithoutSendEvidence(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionGateForRequestWithRecoveryBudget(
		AgentTurnRequest{
			RequiredEvidenceTools: []string{"mail.message.send"},
		},
		nil,
		nil,
		nil,
		turnActionDocument{
			Action:             "finish",
			Message:            "완료했습니다.",
			GoalStatus:         "satisfied",
			GoalSatisfied:      &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{},
		},
		defaultRecoveryBudget(),
	)

	if result.IsSatisfied {
		t.Fatal("expected external send finish without send evidence to be rejected")
	}
	if !strings.Contains(result.Message, "call one of these tools to perform the actual send") {
		t.Fatalf("expected send evidence guidance, got %q", result.Message)
	}
	if len(result.SuggestedNextTools) != 1 || result.SuggestedNextTools[0] != "mail.message.send" {
		t.Fatalf("expected suggested send tool, got %+v", result.SuggestedNextTools)
	}
	observation := withCompletionGateRecoveryPacket(completionGateObservation(1, result.Message), result)
	if observation.RecoveryPacket == nil {
		t.Fatal("expected recovery packet")
	}
	if len(observation.RecoveryPacket.AllowedTools) != 1 || observation.RecoveryPacket.AllowedTools[0] != "mail.message.send" {
		t.Fatalf("expected recovery packet allowed send tool, got %+v", observation.RecoveryPacket.AllowedTools)
	}
}

func TestCompletionGateRejectsRequiredSendToolFinishWithSuggestedNextTools(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionGate(
		[]toolUseRequirement{{ToolName: "slack.message.send"}},
		[]turnObservation{newContentObservation("obs-001", "continue", "slack.message.send", "sent")},
		nil,
		turnActionDocument{
			Action:             "finish",
			Message:            "완료했습니다.",
			GoalStatus:         "satisfied",
			GoalSatisfied:      &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{},
		},
	)

	if result.IsSatisfied {
		t.Fatal("expected required send tool finish without send evidence to be rejected")
	}
	if !strings.Contains(result.Message, "call one of these tools to perform the actual send") {
		t.Fatalf("expected send evidence guidance, got %q", result.Message)
	}
	if len(result.SuggestedNextTools) != 1 || result.SuggestedNextTools[0] != "slack.message.send" {
		t.Fatalf("expected suggested send tool, got %+v", result.SuggestedNextTools)
	}
}

func TestCompletionGateAcceptsExternalSendFinishWithSendEvidence(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionGateForRequestWithRecoveryBudget(
		AgentTurnRequest{RequiredEvidenceTools: []string{"message.send"}},
		nil,
		[]turnObservation{newContentObservation("obs-001", "continue", "message.send", "sent")},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "전송했습니다.",
			GoalStatus:    "satisfied",
			GoalSatisfied: &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{{
				ObservationID: "obs-001",
				ToolName:      "message.send",
			}},
		},
		defaultRecoveryBudget(),
	)

	if !result.IsSatisfied {
		t.Fatalf("expected external send finish with send evidence to pass, got %q", result.Message)
	}
}

func TestCompletionGateAllowsSitePublishFinishWithoutStraySendEvidence(t *testing.T) {
	goalSatisfied := true
	request := AgentTurnRequest{
		RequiredEvidenceTools: []string{"site.create", "site.publish", "message.send", "site.status"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site.create", "site.publish", "message.send", "site.status"},
			ExpectedResults: []ExpectedResult{
				{ID: "site-public-link", Type: ExpectedResultTypeLink, Description: "public site URL", Required: true},
				{ID: "final-message", Type: ExpectedResultTypeMessage, Description: "final reply to the user", Required: true},
			},
		},
	}
	observations := []turnObservation{
		newContentObservation("obs-004", "continue", "site.publish", `{"siteID":"site-1","status":"published","publishedURL":"https://banchan-table.example.test"}`),
	}
	result := validateExpectedResultCompletionGate(
		request,
		observations,
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "게시했습니다. https://banchan-table.example.test",
			GoalStatus:    "satisfied",
			GoalSatisfied: &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{{
				ObservationID: "obs-004",
			}},
		},
		defaultRecoveryBudget(),
	)

	if !result.IsSatisfied {
		t.Fatalf("expected site finish backed by a successful site.publish observation to pass without message.send evidence, got %q", result.Message)
	}
}

func TestCompletionGateLeavesNonSendFinishUnaffected(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionGateForRequestWithRecoveryBudget(
		AgentTurnRequest{},
		nil,
		nil,
		nil,
		turnActionDocument{
			Action:             "finish",
			Message:            "완료했습니다.",
			GoalStatus:         "satisfied",
			GoalSatisfied:      &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{},
		},
		defaultRecoveryBudget(),
	)

	if !result.IsSatisfied {
		t.Fatalf("expected non-send finish to pass, got %q", result.Message)
	}
}

func TestCompletionGateAcceptsCalendarFinishClaimWithCalendarObservation(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionGateForRequestWithRecoveryBudget(
		AgentTurnRequest{ToolSet: newTestToolSet([]string{"calendar.add"})},
		nil,
		[]turnObservation{newContentObservation("obs-001", "continue", "calendar.add", `{"id":"event-1","title":"미팅"}`)},
		nil,
		turnActionDocument{
			Action:             "finish",
			Message:            "7월 13일 미팅을 오전 10시~11시로 등록했습니다.",
			GoalStatus:         "satisfied",
			GoalSatisfied:      &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{},
		},
		defaultRecoveryBudget(),
	)

	if !result.IsSatisfied {
		t.Fatalf("expected calendar finish claim with calendar observation to pass, got %q", result.Message)
	}
}

func TestAgentTurnRunnerRejectsRequiredFileWithoutAttachmentEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("파일이 준비되었습니다."),
		`{"action":"fail","reason":"attachment evidence missing"}`,
		recoveryDecisionDocument("attachment evidence was missing", "no attachment was available", "ask the user to retry file generation", "explain that attachment evidence was missing"),
	}, textResponses: []string{
		"첨부 파일을 만들거나 보냈다고 확인할 근거가 없어 여기서 멈췄어요. 파일이 필요하면 다시 시도해 주세요.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, RecoveryBudget: exhaustedRecoveryBudgetForTest()})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "파일 만들어서 보내줘",
		RequiredEvidenceTools: []string{
			"file.deliver",
		},
		OutcomeContract: OutcomeContract{
			ArtifactRequirement: ArtifactRequirementRequired,
			ExpectedResults: []ExpectedResult{{
				ID:       "attached-file",
				Type:     ExpectedResultTypeFile,
				Required: true,
			}},
		},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task after missing file evidence, got %s", result.TaskRun.Status)
	}
	if !strings.Contains(result.UserNotice, "근거가 없어") {
		t.Fatalf("expected generated failure reply, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "required file expected result") {
		t.Fatal("expected completion gate to reject required file without evidence")
	}
}

func TestAgentTurnRunnerRejectsHtmlClaimBackedByMarkdownAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"DESIGN.md"}}`,
		finishMessageWithEvidence("HTML 파일을 전달해 드립니다.", "obs-001", "file.deliver", 0),
		`{"action":"fail","reason":"html attachment missing"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/deck/DESIGN.md",
				Filename:   "DESIGN.md",
			}},
		}, nil
	})
	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "html만 주면 돼",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		RequiredEvidenceTools:      []string{"file.deliver"},
		RequiredAttachmentSuffixes: []string{".html"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task after mismatched attachment claim, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", ".html") {
		t.Fatal("expected completion gate to reject missing html attachment")
	}
}

func TestAgentTurnRunnerAcceptsHtmlRequestWithHtmlAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"deck.html"}}`,
		finishMessageWithEvidence("HTML 파일을 전달해 드립니다.", "obs-001", "file.deliver", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
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
	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "html만 주면 돼",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		RequiredEvidenceTools:      []string{"file.deliver"},
		RequiredAttachmentSuffixes: []string{".html"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.html" {
		t.Fatalf("expected html attachment, got %+v", result.Attachments)
	}
}

func TestValidateCompletionEvidenceDoesNotDeliverImageReadAttachment(t *testing.T) {
	attachmentIndex := 0
	attachments, errorValue := validateCompletionEvidence(nil, []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "image.read",
		Attachments: []FileAttachment{{
			DevicePath:    "/workspace/inbox/mascot.png",
			Filename:      "mascot.png",
			ContentType:   "image/png",
			ContentBase64: "aW1hZ2U=",
		}},
	}}, []completionEvidenceReference{{
		ObservationID:   "obs-001",
		ToolName:        "image.read",
		AttachmentIndex: &attachmentIndex,
	}})
	if errorValue != nil {
		t.Fatalf("expected image.read evidence to validate: %v", errorValue)
	}
	if len(attachments) != 0 {
		t.Fatalf("expected image.read evidence to produce no delivery attachments, got %+v", attachments)
	}
}

func TestAgentTurnRunnerRequiresToolEvidenceBeforeFinishMessage(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("browser tool is unavailable"),
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"memory.search","input":{}}}`,
		finishMessageDocument("still no screenshot"),
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"browser.screenshot","input":{}}}`,
		finishMessageWithEvidence("observed", "obs-004", "browser.screenshot", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestCapabilityToolSet([]string{"browser.screenshot", "memory.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "memory.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`[]`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.screenshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: `{"devicePath":"/tmp/internkim-companion-files/screenshot.png"}`},
			Attachments: []FileAttachment{{
				DevicePath: "/tmp/internkim-companion-files/screenshot.png",
				Filename:   "screenshot.png",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글 서치바에 hello world라고 치고 스크린샷",
		TaskShape:         TaskShapeBrowserHandoffTask,
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected browser tool requirement to recover: %v", errorValue)
	}
	if result.FinishMessage != "observed" {
		t.Fatalf("expected final reply after tool use, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "browser.") {
		t.Fatal("expected completion requirement event")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.capability.invoke.result", "[]") {
		t.Fatal("expected memory search observation before screenshot")
	}
	if len(result.Attachments) != 1 || result.Attachments[0].DevicePath != "/tmp/internkim-companion-files/screenshot.png" {
		t.Fatalf("expected screenshot attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.capability.invoke.result", "/tmp/internkim-companion-files/screenshot.png") {
		t.Fatal("expected browser screenshot observation")
	}
}

func TestAgentTurnRunnerRequiresSelectedSkillEvidenceBeforeFinishMessage(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("PPT 못 만들어요"),
		`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"deck.pptx"}}`,
		finishMessageWithEvidence("PPTX를 첨부했습니다: deck.pptx", "obs-002", "file.deliver", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/deck/deck.pptx",
				Filename:   "deck.pptx",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "피피티 만들어줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"file.deliver"},
	})
	if errorValue != nil {
		t.Fatalf("expected required evidence to recover: %v", errorValue)
	}
	if !strings.Contains(result.FinishMessage, "deck.pptx") {
		t.Fatalf("expected artifact-aware reply, got %q events=%+v", result.FinishMessage, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected pptx attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "file.deliver") {
		t.Fatal("expected completion required event for selected skill evidence")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.evidence_missing", "evidence_missing") {
		t.Fatal("expected structured evidence missing event")
	}
}

func TestAgentTurnRunnerRequiresSideEffectToolInCompletionEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"deck.html"}}`,
		`{"action":"finish","message":"HTML 파일을 첨부했습니다: deck.html","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001","obs-002"],"completionEvidence":[{"observationID":"obs-001","toolName":"file.write"},{"observationID":"obs-002","toolName":"file.deliver","attachmentIndex":0}],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"file.write", "file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"path":"tmp/deck/presentation.md","sizeBytes":6}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/deck/deck.html",
				Filename:   "deck.html",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "html 만들어줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"file.write", "file.deliver"},
	})
	if errorValue != nil {
		t.Fatalf("expected required evidence to recover: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, events)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.html" {
		t.Fatalf("expected html attachment, got %+v", result.Attachments)
	}
}

func TestAgentTurnRunnerRequiresAttachmentSuffixEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"DESIGN.md"}}`,
		finishMessageWithEvidence("첨부했습니다.", "obs-001", "file.deliver", 0),
		`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"deck.pptx"}}`,
		finishMessageWithEvidence("PPTX를 첨부했습니다: deck.pptx", "obs-003", "file.deliver", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return ToolResult{}, errorValue
		}
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/deck/" + request.Path,
				Filename:   request.Path,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		RequiredEvidenceTools:      []string{"file.deliver"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected required suffix evidence to recover: %v", errorValue)
	}
	if !strings.Contains(result.FinishMessage, "deck.pptx") {
		t.Fatalf("expected artifact-aware reply, got %q events=%+v", result.FinishMessage, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected pptx attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", ".pptx") {
		t.Fatal("expected completion required event for missing attachment suffix")
	}
}

func TestAgentTurnRunnerAcceptsReadableFileAttachObservation(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "presentation.md"), "Hermes Agent 장단점 분석")
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "deck.html"), "<html><body>Hermes Agent 장단점 분석</body></html>")
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"artifacts/deck/deck.html"}}`,
		finishMessageWithEvidence("deck.html 파일을 첨부했습니다.", "obs-001", "file.deliver", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3})
	toolRegistry := newTestToolSet([]string{"file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/deck/deck.html",
				Filename:   "deck.html",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "html 만들어줘",
		WorkspaceRootPath:     workspaceRootPath,
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"file.deliver"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed turn without runner error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, events)
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("expected readable attachment to be delivered, got %+v", result.Attachments)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.artifact_attach_rejected", "deck intent manifest is missing") {
		t.Fatal("did not expect intent manifest rejection event")
	}
}

func TestAgentTurnRunnerAutoAttachesRequiredWorkspaceArtifacts(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))
	writeValidPDFTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pdf"))

	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"finish","message":"요청하신 두 파일을 첨부했습니다.","replyParts":[{"type":"text","text":"요청하신 두 파일을 첨부했습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"file.deliver","attachmentIndex":0},{"observationID":"obs-002","toolName":"file.deliver","attachmentIndex":0}],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return ToolResult{}, errorValue
		}
		attachments := []FileAttachment{{
			DevicePath: request.Path,
			Filename:   filepath.Base(request.Path),
		}}
		return ToolResult{Output: ToolOutput{Content: "file attached"}, Attachments: attachments}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file.deliver"},
		RequiredAttachmentSuffixes: []string{".pptx", ".pdf"},
	})
	if errorValue != nil {
		t.Fatalf("expected auto attachment evidence to succeed: %v", errorValue)
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected two attachments, got %+v events=%+v", result.Attachments, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if result.Attachments[0].Filename != "deck.pptx" && result.Attachments[1].Filename != "deck.pptx" {
		t.Fatalf("expected deck.pptx attachment, got %+v", result.Attachments)
	}
	if result.Attachments[0].Filename != "deck.pdf" && result.Attachments[1].Filename != "deck.pdf" {
		t.Fatalf("expected deck.pdf attachment, got %+v", result.Attachments)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.completion_state_transition", "attach_existing_artifacts") {
		t.Fatal("expected completion state attachment transition")
	}
	if !taskEventsContain(taskEvents, "tool.file.deliver.requested", "deck.pptx") {
		t.Fatal("expected automatic file.deliver request")
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected one model call for final wording, got %d", len(languageModel.requests))
	}
}

func TestAgentTurnRunnerCompletesAfterRequiredArtifactsExist(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"build deck"}}`,
		`{"action":"finish","message":"요청하신 파일을 첨부했습니다.","completionSummary":"요청하신 파일을 첨부했습니다.","replyParts":[{"type":"text","text":"요청하신 파일을 첨부했습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-002","obs-003"],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.deliver"})
	terminalCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
			return ToolResult{}, errorValue
		}
		writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))
		writeValidPDFTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pdf"))
		return ToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return ToolResult{}, errorValue
		}
		attachments := []FileAttachment{{DevicePath: request.Path, Filename: filepath.Base(request.Path)}}
		return ToolResult{Output: ToolOutput{Content: "file attached"}, Attachments: attachments}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		WorkspaceRootPath:          workspaceRootPath,
		RequiredEvidenceTools:      []string{"file.deliver"},
		RequiredAttachmentSuffixes: []string{".pptx", ".pdf"},
	})
	if errorValue != nil {
		t.Fatalf("expected auto artifact completion: %v", errorValue)
	}
	if terminalCallCount != 1 {
		t.Fatalf("expected one build command before auto completion, got %d", terminalCallCount)
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected two attachments, got %+v", result.Attachments)
	}
	if result.FinishMessage != "요청하신 파일을 첨부했습니다." {
		t.Fatalf("expected model-authored final reply, got %q", result.FinishMessage)
	}
}

func TestAgentTurnRunnerDoesNotRepeatFailedAutomaticAttachment(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))

	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"fail","reason":"attachment unavailable"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"file.deliver"})
	attachmentCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		attachmentCallCount++
		return ToolFailureResult(FailureUnknown, FailureCodes.OperationFailed, "tool", "attachment unavailable"), nil
	})

	_, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file.deliver"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected failed turn to return result without runner error: %v", errorValue)
	}
	if attachmentCallCount != 1 {
		t.Fatalf("expected one automatic attachment attempt, got %d", attachmentCallCount)
	}
}

func TestAgentTurnRunnerAttachesReadableImperfectArtifactCandidate(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"), "not a valid pptx")

	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"finish","message":"읽을 수 있는 파일을 첨부했습니다.","completionSummary":"읽을 수 있는 파일을 첨부했습니다.","replyParts":[{"type":"text","text":"읽을 수 있는 파일을 첨부했습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"file.deliver"})
	attachmentCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		attachmentCallCount++
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/private/people/person-1/artifacts/deck/deck.pptx",
				Filename:   "deck.pptx",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file.deliver"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected invalid artifact turn to return result without runner error: %v", errorValue)
	}
	if attachmentCallCount == 0 {
		t.Fatal("expected readable imperfect artifact to be attached")
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("expected imperfect artifact attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.validity_review", `"passed":true`) {
		t.Fatal("expected passing basic validity review event")
	}
}

func TestAgentTurnRunnerAutoCompletionKeepsQualityOutOfCorePolicy(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))
	writeValidPDFTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pdf"))

	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"finish","message":"요청하신 두 파일을 첨부했습니다.","replyParts":[{"type":"text","text":"요청하신 두 파일을 첨부했습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"file.deliver","attachmentIndex":0},{"observationID":"obs-002","toolName":"file.deliver","attachmentIndex":0}],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return ToolResult{}, errorValue
		}
		attachments := []FileAttachment{{DevicePath: request.Path, Filename: filepath.Base(request.Path)}}
		return ToolResult{Output: ToolOutput{Content: "file attached"}, Attachments: attachments}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file.deliver"},
		RequiredAttachmentSuffixes: []string{".pptx", ".pdf"},
	})
	if errorValue != nil {
		t.Fatalf("expected artifact validity completion without core quality checks: %v", errorValue)
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected attachments, got %+v events=%+v", result.Attachments, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.quality_review", "marp_build_log_success") {
		t.Fatal("expected no hard-coded slide quality check event")
	}
}

func TestAgentTurnRunnerRejectsUnsatisfiedFinishMessage(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"finish","message":"done","replyParts":[{"type":"text","text":"done"}],"goalStatus":"in_progress","goalSatisfied":false,"completionEvidence":[]}`,
		finishMessageDocument("now done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "say hello",
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover: %v", errorValue)
	}
	if result.FinishMessage != "now done" {
		t.Fatalf("expected recovered final reply, got %q", result.FinishMessage)
	}
	if len(languageModel.requests) < 2 {
		t.Fatalf("expected structured retry request after finish rejection, got %d requests", len(languageModel.requests))
	}
	if !messagesContain(languageModel.requests[1].Messages, "finish requires goalSatisfied=true") {
		t.Fatalf("expected retry request to include finish rejection reason, got %+v", languageModel.requests[1].Messages)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "goalSatisfied=true") {
		t.Fatal("expected goalSatisfied completion gate event")
	}
}

func TestAgentTurnRunnerRejectsCompletionEvidenceFromErrorObservation(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"unstable","input":{}}}`,
		finishMessageWithEvidence("done", "obs-001", "unstable", 0),
		failureReportDocument("tool failed", "unstable", "{}", FailureCodes.OperationFailed.String(), "unstable", "failed"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"unstable"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "unstable"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolFailureResult(FailureUnknown, FailureCodes.OperationFailed, "unstable", "failed"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to fail safely: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "FailureDebt") {
		t.Fatal("expected active failure debt gate event")
	}
}

func TestAgentTurnRunnerNoToolFallbackWaivesFailedRequiredEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"math.calculate","input":{"expression":"1+2/4"}}}`,
		noToolFallbackFinishMessageDocument("1 + 2/4 = 1.5"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestCapabilityToolSet([]string{"math.calculate"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "math.calculate"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("exec: \"bc\": executable file not found in $PATH", "bc: command not found", "calculator_failed", "bc_execution", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "1+2/4=",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"math.calculate"},
	})
	if errorValue != nil {
		t.Fatalf("expected no-tool fallback to complete: %v", errorValue)
	}
	if result.FinishMessage != "1 + 2/4 = 1.5" {
		t.Fatalf("expected direct fallback answer, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.capability.invoke.result", FailureCodes.OperationFailed.String()) {
		t.Fatal("expected internal tool failure event to remain recorded")
	}
}

func TestCompletionGateDoesNotWaiveFlowTaskEvidenceWithNoToolFallback(t *testing.T) {
	goalSatisfied := true
	request := AgentTurnRequest{
		RequiredEvidenceTools: []string{"task.add"},
		ToolSet:               newTestToolSet([]string{"task.add"}),
	}
	result := validateCompletionGateForRequestWithRecoveryBudget(
		request,
		deriveToolUseRequirements(request),
		[]turnObservation{
			newFailureObservation("obs-001", "continue", "task.add", "task add failed", FailureExternalService, FailureCodes.OperationFailed, "task_add"),
		},
		nil,
		turnActionDocument{
			Action:            "finish",
			Message:           "업무를 등록했습니다.",
			GoalStatus:        "satisfied",
			GoalSatisfied:     &goalSatisfied,
			FailureResolution: failureResolutionNoToolFallback,
		},
		defaultRecoveryBudget(),
	)

	if result.IsSatisfied {
		t.Fatal("expected flow task finish without successful task.add evidence to be rejected")
	}
	if !strings.Contains(result.Message, "task.add") {
		t.Fatalf("expected missing flow task evidence message, got %q", result.Message)
	}
}

func TestCompletionGateDoesNotSatisfyCalendarAddWithScheduleCreate(t *testing.T) {
	goalSatisfied := true
	request := AgentTurnRequest{
		RequiredEvidenceTools: []string{"calendar.add"},
		ToolSet:               newTestToolSet([]string{"calendar.add", "schedule.create"}),
	}
	result := validateCompletionGateForRequestWithRecoveryBudget(
		request,
		deriveToolUseRequirements(request),
		[]turnObservation{
			newContentObservation("obs-001", "continue", "schedule.create", `{"taskScheduleID":"schedule-1"}`),
			newFailureObservation("obs-002", "continue", "calendar.add", "calendar add failed", FailureExternalService, FailureCodes.OperationFailed, "calendar_add"),
		},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "일정을 추가했습니다.",
			GoalStatus:    "satisfied",
			GoalSatisfied: &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{{
				ObservationID: "obs-001",
				ToolName:      "schedule.create",
			}},
		},
		defaultRecoveryBudget(),
	)

	if result.IsSatisfied {
		t.Fatal("expected schedule.create not to satisfy calendar.add")
	}
	if !strings.Contains(result.Message, "calendar.add") {
		t.Fatalf("expected missing calendar.add evidence message, got %q", result.Message)
	}
}

func TestCompletionGateDoesNotTreatApprovalAsRequiredEvidence(t *testing.T) {
	goalSatisfied := true
	request := AgentTurnRequest{
		RequiredEvidenceTools: []string{"calendar.add"},
		ToolSet:               newTestToolSet([]string{"calendar.add", AskConfirmToolName}),
	}
	result := validateCompletionGateForRequestWithRecoveryBudget(
		request,
		deriveToolUseRequirements(request),
		[]turnObservation{newContentObservation("obs-001", "continue", AskConfirmToolName, `{"approved":true}`)},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "승인받아 일정을 추가했습니다.",
			GoalStatus:    "satisfied",
			GoalSatisfied: &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{{
				ObservationID: "obs-001",
				ToolName:      AskConfirmToolName,
			}},
		},
		defaultRecoveryBudget(),
	)

	if result.IsSatisfied {
		t.Fatal("expected approval observation not to satisfy calendar.add")
	}
}

func TestCompletionGateRequiresFileDeliverEvidenceEvenWhenFileExists(t *testing.T) {
	goalSatisfied := true
	request := AgentTurnRequest{
		RequiredEvidenceTools: []string{FileDeliverToolName},
		ToolSet:               newTestToolSet([]string{FileDeliverToolName}),
	}
	result := validateCompletionGateForRequestWithRecoveryBudget(
		request,
		deriveToolUseRequirements(request),
		[]turnObservation{newContentObservation("obs-001", "continue", FileWriteToolName, `{"path":"tmp/report.pdf"}`)},
		nil,
		turnActionDocument{
			Action:             "finish",
			Message:            "파일을 만들었습니다.",
			GoalStatus:         "satisfied",
			GoalSatisfied:      &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{},
		},
		defaultRecoveryBudget(),
	)

	if result.IsSatisfied {
		t.Fatal("expected file deliver evidence to be required")
	}
	if !strings.Contains(result.Message, FileDeliverToolName) {
		t.Fatalf("expected file.deliver evidence message, got %q", result.Message)
	}
}

func TestAgentTurnRunnerRemovesQualityCriteriaActionAfterCriteriaAreSet(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"set_quality_criteria","qualityCriteria":["done once: criteria are declared"],"goalStatus":"in_progress","goalSatisfied":false}`,
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"alpha","input":{}}}`,
		`{"action":"finish","message":"done","replyParts":[{"type":"text","text":"done"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-002"],"qualityReview":[{"id":"done-once-criteria-are-declared","passed":true,"evidenceIDs":["obs-002"]}]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("alpha result"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:         "person-1",
		ConversationID:            "conversation-1",
		Prompt:                    "make an artifact",
		ToolSet:                   toolRegistry,
		PinnedToolNames:           toolRegistry.ListToolNames(),
		QualityAcceptanceGuidance: []string{"declare criteria first"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "done" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if len(languageModel.requests) < 2 {
		t.Fatalf("expected at least two model requests, got %d", len(languageModel.requests))
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, "set_quality_criteria") {
		t.Fatalf("expected initial schema to allow quality criteria, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if strings.Contains(languageModel.requests[1].StructuredOutputSchema.Document, "set_quality_criteria") {
		t.Fatalf("expected next schema to remove quality criteria, got %s", languageModel.requests[1].StructuredOutputSchema.Document)
	}
}

func TestAgentTurnRunnerRequiresReviewForDeclaredQualityCriteria(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"set_quality_criteria","qualityCriteria":["visual review: review the artifact"],"goalStatus":"in_progress","goalSatisfied":false}`,
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"site.publish","input":{"siteID":"site-1"}},"nextStepPlan":{"objective":"finish with the public URL","expectedTools":[],"expectedNextResults":["public URL"],"doneCriteria":["public URL is available"],"risk":"none","workingSetReason":"publish satisfies the link expected result"}}`,
		`{"action":"finish","message":"배포했습니다: https://portfolio.example","replyParts":[{"type":"text","text":"배포했습니다: https://portfolio.example"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-002"]}`,
		`{"action":"finish","message":"배포했습니다: https://portfolio.example","replyParts":[{"type":"text","text":"배포했습니다: https://portfolio.example"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-002"],"qualityReview":[{"id":"visual-review-review-the-artifact","passed":true,"evidenceIDs":["obs-002"]}]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestCapabilityToolSet([]string{"site.publish"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.publish"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"publishedURL":"https://portfolio.example","status":"published"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "사이트를 배포해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		OutcomeContract: OutcomeContract{ExpectedResults: []ExpectedResult{{
			ID:          "site-public-link",
			Type:        ExpectedResultTypeLink,
			Description: "사용자가 열 수 있는 public URL의 웹사이트",
			Required:    true,
		}}},
	})
	if errorValue != nil {
		t.Fatalf("expected finish to pass after quality review: %v", errorValue)
	}
	if result.FinishMessage != "배포했습니다: https://portfolio.example" {
		t.Fatalf("expected final publish message, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "qualityReview") {
		t.Fatal("expected missing required quality review to block the first finish")
	}
}

func TestAgentTurnRunnerExpectedResultVerifierBlocksEarlyFinish(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		resultVerifications: []string{
			`{"overallStatus":"missing","summary":"missing public URL","results":[{"id":"site-public-link","status":"missing","reason":"Only a draft exists.","citedObservationIDs":["obs-001"],"missingDescription":"A public URL is still missing.","suggestedNextTools":["site.publish"]}]}`,
			`{"overallStatus":"satisfied","summary":"public URL exists","results":[{"id":"site-public-link","status":"satisfied","reason":"Publish returned a public URL.","citedObservationIDs":["obs-003"],"missingDescription":"","suggestedNextTools":[]}]}`,
		},
		contents: []string{
			`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"site.create","input":{"slug":"portfolio","title":"Portfolio"}},"nextStepPlan":{"objective":"create draft","expectedTools":[],"expectedNextResults":["draft site project exists"],"doneCriteria":["draft exists"],"risk":"none","workingSetReason":"create prepares the project"}}`,
			`{"action":"finish","message":"초안을 만들었습니다.","replyParts":[{"type":"text","text":"초안을 만들었습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"site.create"}]}`,
			`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"site.publish","input":{"siteID":"site-1","message":"Publish"}},"nextStepPlan":{"objective":"finish after public URL","expectedTools":[],"expectedNextResults":["public URL exists"],"doneCriteria":["public URL exists"],"risk":"none","workingSetReason":"publish should satisfy the expected result"}}`,
			`{"action":"finish","message":"배포했습니다: https://portfolio.example","replyParts":[{"type":"text","text":"배포했습니다: https://portfolio.example"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-003","toolName":"site.publish"}]}`,
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	toolRegistry := newTestCapabilityToolSet([]string{"site.create", "site.publish"})
	toolCalls := []string{}
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.create")
		return ToolSuccess(`{"siteID":"site-1","status":"draft"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.publish"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.publish")
		return ToolSuccess(`{"siteID":"site-1","status":"published","publishedURL":"https://portfolio.example"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "개인 홈페이지 만들어서 배포해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		OutcomeContract: OutcomeContract{
			ExpectedResults: []ExpectedResult{{
				ID:          "site-public-link",
				Type:        ExpectedResultTypeLink,
				Description: "사용자가 열 수 있는 public URL의 개인 홈페이지",
				Required:    true,
			}},
		},
	})
	if errorValue != nil {
		t.Fatalf("expected run to complete after verifier-guided recovery: %v", errorValue)
	}
	if strings.Join(toolCalls, ",") != "site.create,site.publish" {
		t.Fatalf("expected create then publish, got %+v", toolCalls)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.expected_result_verification", "missing public URL") {
		t.Fatal("expected result verification event")
	}
}

func TestCompletionContractVerifierSkipsEmptyContract(t *testing.T) {
	languageModel := &sequenceLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	goalSatisfied := true

	result := services.runner.validateCompletionGateForRequestWithExpectedResults(context.Background(), "task-1", AgentTurnRequest{}, nil, nil, nil, nil, turnActionDocument{
		Action:             "finish",
		Message:            "완료했습니다.",
		GoalStatus:         "satisfied",
		GoalSatisfied:      &goalSatisfied,
		CompletionEvidence: nil,
	})

	if !result.IsSatisfied {
		t.Fatalf("expected empty contract to stay on fast path, got %+v", result)
	}
	if len(languageModel.contractRequests) != 0 {
		t.Fatalf("expected no contract verifier call for empty contract, got %d", len(languageModel.contractRequests))
	}
}

func TestCompletionContractVerifierRejectsMissingAttachmentEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contractVerifications: []string{
			`{"satisfied":false,"reason":"The user requested an attached file, but no file attachment evidence exists.","missingDescription":"Attach the generated file before finishing.","suggestedNextTools":["file.deliver"]}`,
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	goalSatisfied := true

	result := services.runner.validateCompletionGateForRequestWithExpectedResults(context.Background(), "task-1", AgentTurnRequest{
		ToolSet: newTestToolSet([]string{"file.deliver"}),
		OutcomeContract: OutcomeContract{
			ArtifactRequirement: ArtifactRequirementPreferred,
		},
	}, nil, nil, nil, nil, turnActionDocument{
		Action:             "finish",
		Message:            "완료했습니다.",
		GoalStatus:         "satisfied",
		GoalSatisfied:      &goalSatisfied,
		CompletionEvidence: nil,
	})

	if result.IsSatisfied {
		t.Fatalf("expected contract verifier to reject missing attachment evidence")
	}
	if !strings.Contains(result.Message, "Attach the generated file before finishing.") {
		t.Fatalf("expected verifier missing description in gate message, got %q", result.Message)
	}
	if len(result.SuggestedNextTools) != 1 || result.SuggestedNextTools[0] != "file.deliver" {
		t.Fatalf("expected verifier to suggest file.deliver, got %+v", result.SuggestedNextTools)
	}
	if len(languageModel.contractRequests) != 1 {
		t.Fatalf("expected one contract verifier call, got %d", len(languageModel.contractRequests))
	}
}

func TestAgentTurnRunnerExpectedResultsDoNotRequireLegacyToolEvidenceFirst(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		resultVerifications: []string{
			`{"overallStatus":"satisfied","summary":"public URL exists","results":[{"id":"site-public-link","status":"satisfied","reason":"Publish returned a public URL.","citedObservationIDs":["obs-001"],"missingDescription":"","suggestedNextTools":[]}]}`,
		},
		contents: []string{
			`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"site.publish","input":{"siteID":"site-1","message":"Publish"}},"nextStepPlan":{"objective":"finish with public URL","expectedTools":[],"expectedNextResults":["public URL exists"],"doneCriteria":["public URL exists"],"risk":"none","workingSetReason":"publish should satisfy the expected result"}}`,
			`{"action":"finish","message":"배포했습니다: https://portfolio.example","replyParts":[{"type":"text","text":"배포했습니다: https://portfolio.example"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"site.publish"}]}`,
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"site.publish"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.publish"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"status":"published","publishedURL":"https://portfolio.example"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: FileDeliverToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolFailureResult(FailureUnknown, FailureCodes.NotFound, "test_tool", "tool is not registered"), nil
	})
	toolRegistry = toolRegistry.WithAdditionalAllowedToolNames([]string{FileDeliverToolName})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "개인 홈페이지 배포해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"file.deliver"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"file.deliver"},
			ExpectedResults: []ExpectedResult{{
				ID:          "site-public-link",
				Type:        ExpectedResultTypeLink,
				Description: "사용자가 열 수 있는 public URL의 개인 홈페이지",
				Required:    true,
			}},
		},
	})
	if errorValue != nil {
		t.Fatalf("expected run to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if strings.Contains(result.FinishMessage, "첨부") {
		t.Fatalf("expected link result, got %q", result.FinishMessage)
	}
}

func TestAgentTurnRunnerFileExpectedResultRequiresAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		resultVerifications: []string{
			`{"overallStatus":"satisfied","summary":"PPTX attached","results":[{"id":"attached-file","status":"satisfied","reason":"file.deliver returned an attachment.","citedObservationIDs":["obs-003"],"missingDescription":"","suggestedNextTools":[]}]}`,
		},
		contents: []string{
			`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"file.promote","input":{"path":"tmp/deck/build/deck.pptx","destinationDirectoryPath":"artifacts/deck","overwrite":true}},"nextStepPlan":{"objective":"attach promoted file","expectedTools":["file.deliver"],"expectedNextResults":["attached pptx"],"doneCriteria":["file attached"],"risk":"none","workingSetReason":"file deliverable requires attachment"}}`,
			`{"action":"finish","message":"PPTX를 첨부했습니다.","replyParts":[{"type":"text","text":"PPTX를 첨부했습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"file.promote"}]}`,
			`{"action":"continue","toolName":"file.deliver","toolInput":{"path":"artifacts/deck/deck.pptx"},"nextStepPlan":{"objective":"finish","expectedTools":[],"expectedNextResults":["final message"],"doneCriteria":["attached file delivered"],"risk":"none","workingSetReason":"attachment now exists"}}`,
			finishMessageWithEvidence("PPTX를 첨부했습니다.", "obs-003", "file.deliver", 0),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	toolRegistry := newTestCapabilityToolSet([]string{"file.promote"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.promote"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"path":"artifacts/deck/deck.pptx"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath:  "/tmp/deck.pptx",
				Filename:    "deck.pptx",
				ContentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			}},
		}, nil
	})
	toolRegistry = toolRegistry.WithAllowedToolNames([]string{CapabilityInvokeToolName, "file.promote", "file.deliver"})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "PPTX 파일로 첨부해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		OutcomeContract: OutcomeContract{
			ArtifactRequirement:        ArtifactRequirementRequired,
			RequiredAttachmentSuffixes: []string{".pptx"},
			ExpectedResults: []ExpectedResult{{
				ID:          "attached-file",
				Type:        ExpectedResultTypeFile,
				Description: "수정 가능한 PPTX 파일 한 개",
				Required:    true,
			}},
		},
	})
	if errorValue != nil {
		t.Fatalf("expected run to complete after attachment: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "file.deliver") {
		t.Fatal("expected completion gate to require file.deliver")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.file.deliver.requested", "deck.pptx") {
		t.Fatal("expected file.deliver after promoted-only finish was rejected")
	}
}

func TestAgentTurnRunnerRejectsQualityGateRetryUntilSourceChanges(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"site.build","input":{"siteID":"site-1"}}}`,
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"site.build","input":{"siteID":"site-1"}},"recoveryForObservationID":"obs-001"}`,
		`{"action":"continue","toolName":"file.write","toolInput":{"path":"/workspace/sites/site-1/app/src/App.tsx","content":"export default function App(){return <main>Portfolio</main>}"},"recoveryForObservationID":"obs-001"}`,
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"site.build","input":{"siteID":"site-1"}},"recoveryForObservationID":"obs-001"}`,
		finishMessageWithEvidence("수정 후 빌드했습니다.", "obs-005", "site.build", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8, MaxToolCallCount: 6})
	toolRegistry := newTestCapabilityToolSet([]string{"site.build"})
	buildCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		buildCallCount++
		if buildCallCount == 1 {
			return ToolResult{
				Output: ToolOutput{Content: `{"qualityPath":"/workspace/sites/site-1/.internkim/build-quality.json","issues":[{"target":"app/src/App.tsx"}]}`},
				Failure: &ToolFailure{
					Kind:                  FailureInvalidInput,
					Code:                  "site_quality_gate_failed",
					Stage:                 "site_build_quality_gate",
					UserSafeSummary:       "quality gate failed",
					Retryable:             true,
					FailureClass:          failureClassQuality,
					RetryPolicy:           retryPolicyAfterPrecondition,
					RequiredPreconditions: []string{"source_changed"},
					RecoveryHints:         []RecoveryHint{{Action: "edit_resource", ToolNames: []string{"file.write", "file.read"}}},
					AffectedResources:     []AffectedResource{{Path: "app/src/App.tsx", Role: "source"}},
				},
			}, nil
		}
		return ToolSuccess(`{"qualityPath":"/workspace/sites/site-1/.internkim/build-quality.json","distPath":"/workspace/sites/site-1/app/dist"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"path":"/workspace/sites/site-1/app/src/App.tsx","bytesWritten":64}`), nil
	})
	toolRegistry = toolRegistry.WithAllowedToolNames([]string{CapabilityInvokeToolName, "site.build", "file.write"})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "사이트 빌드해서 배포 준비해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"site.build"},
	})
	if errorValue != nil {
		t.Fatalf("expected recovery loop to finish: %v", errorValue)
	}
	if buildCallCount != 2 {
		t.Fatalf("expected duplicate build retry to be rejected before tool invocation, got %d build calls", buildCallCount)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "agent.recovery_choice_rejected", "source_changed") {
		t.Fatal("expected retry without source change to be rejected")
	}
	if !taskEventsContain(events, "agent.recovery_guidance", "RecoveryPacket") {
		t.Fatal("expected model-visible recovery packet")
	}
}

func TestAgentTurnRunnerRequestsFinalReplyAfterOneShotEvidenceToolSuccess(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"calendar.add","input":{"title":"휴가","startISO":"2026-05-10T00:00:00+09:00","endISO":"2026-05-13T00:00:00+09:00","timeZone":"Asia/Seoul","isAllDay":true}}}`,
		finishMessageWithEvidence("휴가 일정을 등록했습니다.", "obs-001", "calendar.add", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestCapabilityToolSet([]string{"calendar.add"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "calendar.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"id":"event-1","title":"휴가","startISO":"2026-05-10T00:00:00+09:00","endISO":"2026-05-13T00:00:00+09:00","timeZone":"Asia/Seoul","isAllDay":true}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "내일부터 화요일까지 휴가 등록해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"calendar.add"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed calendar turn: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected one calendar write, got %d", toolCallCount)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected one final model action after evidence success, got %d requests", len(languageModel.requests))
	}
	if result.FinishMessage != "휴가 일정을 등록했습니다." {
		t.Fatalf("expected model-authored final reply, got %q", result.FinishMessage)
	}
}

func TestAgentTurnRunnerRequestsFinalReplyAfterScheduleCreateSuccess(t *testing.T) {
	progressMessage := "반복 정책을 보정해 스케줄을 생성합니다. repeatPolicy=finite"
	finalMessage := "1분 간격으로 10번 보내도록 예약했습니다."
	languageModel := &sequenceLanguageModel{contents: []string{
		capabilityInvokeAction("continue", "스케줄을 생성합니다.", "schedule.create", `{"taskInstruction":"현재 대화에 죄송합니다라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"timeZone":"Asia/Seoul"}`),
		capabilityInvokeAction("continue", progressMessage, "schedule.create", `{"taskInstruction":"현재 대화에 죄송합니다라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}`, "obs-001"),
		finishMessageWithEvidence(finalMessage, "obs-003", "schedule.create", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5, MaxToolCallCount: 5})
	toolCallCount := 0
	toolRegistry := newTestCapabilityToolSet([]string{"schedule.create"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "schedule.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		if toolCallCount == 1 {
			return ToolFailureResult(FailureInvalidInput, FailureCodes.InvalidInput, "schedule_create", "repeatPolicy is required"), nil
		}
		return ToolSuccess(`{"taskScheduleID":"schedule-1","taskInstruction":"현재 대화에 \"죄송합니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"nextRunAt":"2026-05-09T05:07:00Z"}`), nil
	})
	checkpoints := []AgentCheckpoint{}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "1분에 한 번씩 나한테 죄송합니다 10번 해봐",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"schedule.create"},
		TaskLevel:             TaskLevelLow,
		EstimatedMinutes:      2,
		CheckpointSender: func(_ context.Context, checkpoint AgentCheckpoint) error {
			checkpoints = append(checkpoints, checkpoint)
			return nil
		},
	})
	if errorValue != nil {
		t.Fatalf("expected completed schedule turn: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 2 {
		t.Fatalf("expected corrected schedule retry, got %d calls", toolCallCount)
	}
	if len(languageModel.requests) != 3 {
		t.Fatalf("expected one final model action after schedule success, got %d requests", len(languageModel.requests))
	}
	if result.FinishMessage != finalMessage || result.FinishMessage == progressMessage {
		t.Fatalf("expected model-authored final reply, got %q", result.FinishMessage)
	}
	if len(checkpoints) != 0 {
		t.Fatalf("expected short task to stay silent until final, got %+v", checkpoints)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.checkpoint.skipped", "short_task") {
		t.Fatal("expected short task checkpoint skip event")
	}
	if !taskEventsContain(taskEvents, "task.completed", finalMessage) {
		t.Fatal("expected persisted completion to use explicit final wording")
	}
}

func TestAgentTurnRunnerDoesNotBlockTerminalRerunForMissingFile(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/deck/presentation.md","content":"# Deck"},"recoveryForObservationID":"obs-001"}`,
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"},"recoveryForObservationID":"obs-001"}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 6})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		if terminalCallCount == 1 {
			return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "terminal_run", `{"exitCode":1,"stdout":"","stderr":"Error: presentation.md not found. Create presentation.md or set SRC=yourfile.md\n","timedOut":false}`), nil
		}
		return ToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return ToolFailureResult(FailureUnknown, FailureCodes.OperationFailed, "tool", errorValue.Error()), nil
		}
		return ToolSuccess(`{"path":"` + input.Path + `","sizeBytes":5}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "build deck",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 2 {
		t.Fatalf("expected terminal rerun to remain available, got %d terminal calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "presentation.md") {
		t.Fatal("did not expect terminal precondition block event")
	}
}

type completionFailureRepository struct {
	errorValue error
}

type completionTransitionFailureRepository struct {
	taskRun                  task.TaskRun
	errorValue               error
	completedTransitionCount int
	failedTransitionCount    int
}

func newCompletionTransitionFailureRepository(errorValue error) *completionTransitionFailureRepository {
	return &completionTransitionFailureRepository{errorValue: errorValue}
}

func (repository *completionTransitionFailureRepository) SaveTaskRun(taskRun task.TaskRun) error {
	repository.taskRun = taskRun
	return nil
}

func (repository *completionTransitionFailureRepository) StartTaskRunAttempt(task.TaskRun, task.TaskAttempt) error {
	return nil
}

func (repository *completionTransitionFailureRepository) FinishTaskRunAttempt(task.TaskRun, task.TaskAttempt) error {
	return nil
}

func (repository *completionTransitionFailureRepository) TransitionTaskRun(transition task.TaskRunTransition) (task.TaskRun, error) {
	if transition.ToState == task.TaskStatusCompleted {
		repository.completedTransitionCount++
		return task.TaskRun{}, repository.errorValue
	}
	if transition.ToState == task.TaskStatusFailed {
		repository.failedTransitionCount++
	}
	repository.taskRun.Status = transition.ToState
	repository.taskRun.FailureReason = transition.FailureReason
	repository.taskRun.Result = transition.Result
	repository.taskRun.UpdatedAt = transition.UpdatedAt
	if transition.StartedAttempt != nil {
		repository.taskRun.CurrentAttemptID = transition.StartedAttempt.TaskAttemptID
	}
	return repository.taskRun, nil
}

func (repository *completionTransitionFailureRepository) FindTaskRun(taskRunID string) (task.TaskRun, bool, error) {
	return repository.taskRun, repository.taskRun.TaskRunID == taskRunID, nil
}

func (repository *completionTransitionFailureRepository) FindTaskAttempt(string) (task.TaskAttempt, bool, error) {
	return task.TaskAttempt{}, false, nil
}

func (repository *completionTransitionFailureRepository) ListTaskRun() ([]task.TaskRun, error) {
	return []task.TaskRun{repository.taskRun}, nil
}

func (repository *completionTransitionFailureRepository) ListTaskRunByPersonID(string) ([]task.TaskRun, error) {
	return []task.TaskRun{repository.taskRun}, nil
}

func (repository *completionTransitionFailureRepository) DeleteTaskRun(string, []string) (bool, error) {
	return false, nil
}

func (repository *completionTransitionFailureRepository) DeleteTaskRunsBefore(time.Time, []string) ([]string, error) {
	return nil, nil
}

func (repository completionFailureRepository) SaveTaskRun(task.TaskRun) error {
	return nil
}

func (repository completionFailureRepository) StartTaskRunAttempt(task.TaskRun, task.TaskAttempt) error {
	return nil
}

func (repository completionFailureRepository) FinishTaskRunAttempt(task.TaskRun, task.TaskAttempt) error {
	return nil
}

func (repository completionFailureRepository) TransitionTaskRun(task.TaskRunTransition) (task.TaskRun, error) {
	return task.TaskRun{}, repository.errorValue
}

func (repository completionFailureRepository) FindTaskRun(string) (task.TaskRun, bool, error) {
	return task.TaskRun{}, false, nil
}

func (repository completionFailureRepository) FindTaskAttempt(string) (task.TaskAttempt, bool, error) {
	return task.TaskAttempt{}, false, nil
}

func (repository completionFailureRepository) ListTaskRun() ([]task.TaskRun, error) {
	return nil, nil
}

func (repository completionFailureRepository) ListTaskRunByPersonID(string) ([]task.TaskRun, error) {
	return nil, nil
}

func (repository completionFailureRepository) DeleteTaskRun(string, []string) (bool, error) {
	return false, nil
}

func (repository completionFailureRepository) DeleteTaskRunsBefore(time.Time, []string) ([]string, error) {
	return nil, nil
}

func TestAgentTurnRunnerStopsRepeatedMissingEvidenceState(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"build deck"}}`,
			noToolFallbackFinishMessageDocument("텍스트로 대신 드립니다."),
			noToolFallbackFinishMessageDocument("텍스트로 대신 드립니다."),
			noToolFallbackFinishMessageDocument("텍스트로 대신 드립니다."),
			recoveryDecisionDocument("file attachment missing", "deck build failed", "stop the repeated state", "report the missing artifact"),
		},
		textResponses: []string{"PPTX 첨부를 완료하지 못했습니다. 빌드 실패 뒤에도 필수 첨부 증거가 없어 작업을 중단했습니다."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 40, RecoveryAttemptLimit: 3})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "terminal_run", `{"exitCode":1,"stderr":"EACCES: permission denied, open 'deck.html'"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		RequiredEvidenceTools:      []string{"file.deliver"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected repeated state stop without error: %v", errorValue)
	}
	if result.TaskRun.Status == task.TaskStatusRunning {
		t.Fatalf("expected the repeated missing-evidence loop to terminate, got status %s", result.TaskRun.Status)
	}
	if terminalCallCount != 1 {
		t.Fatalf("expected no repeated terminal command, got %d calls", terminalCallCount)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.stall_exit_directive", "") {
		t.Fatal("expected a stall-exit steer before terminating the repeated missing-evidence loop")
	}
	if taskEventsContain(taskEvents, "max_iterations", "") {
		t.Fatal("expected loop breaker before max_iterations")
	}
}

func TestAgentTurnRunnerDoesNotBlockTerminalRerunForMissingDesignFile(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/deck/DESIGN.md","content":"colors: blue"},"recoveryForObservationID":"obs-001"}`,
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"},"recoveryForObservationID":"obs-001"}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 6})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		if terminalCallCount == 1 {
			return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "terminal_run", `{"exitCode":1,"stdout":"","stderr":"DESIGN.md is missing colors:\n","timedOut":false}`), nil
		}
		return ToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return ToolFailureResult(FailureUnknown, FailureCodes.OperationFailed, "tool", errorValue.Error()), nil
		}
		return ToolSuccess(`{"path":"` + input.Path + `","sizeBytes":12}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "build deck",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 2 {
		t.Fatalf("expected terminal rerun to remain available, got %d terminal calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "DESIGN.md") {
		t.Fatal("did not expect DESIGN.md precondition block event")
	}
}

func TestAgentTurnRunnerDoesNotBlockTerminalBeforeRequiredFileWrite(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"finish","message":"done","replyParts":[{"type":"text","text":"done"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"file.write"}]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5, MaxToolCallCount: 5})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		return ToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return ToolFailureResult(FailureUnknown, FailureCodes.OperationFailed, "tool", errorValue.Error()), nil
		}
		return ToolSuccess(`{"path":"` + input.Path + `","sizeBytes":5}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "build deck",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"file.write"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 2 {
		t.Fatalf("expected rebuild after file.write to run instead of duplicate rejection, got %d calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "first required workspace file") {
		t.Fatal("did not expect required file.write precondition block event")
	}
}
