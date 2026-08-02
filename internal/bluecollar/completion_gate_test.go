package bluecollar

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/model"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

func TestCompletionStateWaitsForModelWordingBeforeCompleting(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{}, TurnOptions{})
	transition := services.runner.finalizeCompletionState(context.Background(), "", "", AgentTurnRequest{}, nil, nil, nil, nil, CompletionState{}, "")
	if transition.IsCompleted || transition.DidTransition {
		t.Fatalf("expected empty model wording to defer completion, got %+v", transition)
	}
}

type contextExpiringJudgeLanguageModel struct {
	cancel     context.CancelFunc
	content    string
	errorValue error
}

func (languageModel *contextExpiringJudgeLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *contextExpiringJudgeLanguageModel) GenerateStructuredResponse(_ context.Context, _ model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.cancel()
	return model.StructuredResponse{Content: languageModel.content}, languageModel.errorValue
}

func completionGateSideEffectToolSetAndObservations() (*toolcontract.ToolSet, []turnObservation) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{testToolDescriptor("task_delete")})
	observations := []turnObservation{successfulSideEffectObservation("obs-001", "task_delete", `{"taskID":"task-1"}`, `{"deleted":true}`)}
	return toolSet, observations
}

func TestFinalizeCompletionStateCompletesDespiteJudgeContextDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	languageModel := &contextExpiringJudgeLanguageModel{cancel: cancel, errorValue: context.DeadlineExceeded}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolSet, observations := completionGateSideEffectToolSetAndObservations()
	request := AgentTurnRequest{
		Prompt:          "오래된 작업을 삭제해줘",
		ToolSet:         toolSet,
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_delete"}},
	}
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)

	transition := services.runner.finalizeCompletionState(ctx, taskRun.TaskRunID, "step-1", request, nil, observations, nil, nil, CompletionState{}, "오래된 작업을 삭제했습니다.")

	if !transition.IsCompleted || !transition.DidTransition {
		t.Fatalf("expected the turn to complete despite judge context expiry, got %+v", transition)
	}
	if transition.Result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected task run to be completed, got status %q", transition.Result.TaskRun.Status)
	}
	if !strings.Contains(transition.Result.FinishMessage, "오래된 작업을 삭제했습니다.") {
		t.Fatalf("expected the generated reply to be preserved, got %q", transition.Result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRun.TaskRunID), "completion_judge.degraded", "") {
		t.Fatal("expected a completion_judge.degraded event to be recorded")
	}
}

func TestFinalizeCompletionStateDeliversBestEffortWhenJudgeUnsatisfiedAndBudgetExpired(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	languageModel := &contextExpiringJudgeLanguageModel{
		cancel:  cancel,
		content: `{"satisfied":false,"missingWork":["첨부 확인 누락"],"reason":"완료 확인 불가"}`,
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolSet, observations := completionGateSideEffectToolSetAndObservations()
	request := AgentTurnRequest{
		Prompt:          "오래된 작업을 삭제해줘",
		ToolSet:         toolSet,
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_delete"}},
	}
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)

	transition := services.runner.finalizeCompletionState(ctx, taskRun.TaskRunID, "step-1", request, nil, observations, nil, nil, CompletionState{}, "오래된 작업을 삭제했습니다.")

	if !transition.IsCompleted || !transition.DidTransition {
		t.Fatalf("expected best-effort completion when the judge is unsatisfied and the budget is expired, got %+v", transition)
	}
	if transition.Result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected task run to be completed, got status %q", transition.Result.TaskRun.Status)
	}
	if !strings.Contains(transition.Result.FinishMessage, "오래된 작업을 삭제했습니다.") {
		t.Fatalf("expected the original reply to be preserved, got %q", transition.Result.FinishMessage)
	}
	if !strings.Contains(transition.Result.FinishMessage, "완료 확인 불가") {
		t.Fatalf("expected the judge's rejection reason appended as a caveat, got %q", transition.Result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRun.TaskRunID), "completion_judge.verdict", `"satisfied":false`) {
		t.Fatal("expected a completion_judge.verdict event recording the unsatisfied verdict")
	}
}

func TestFinalizeCompletionStateKeepsRetryingWhenJudgeUnsatisfiedAndBudgetRemains(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: model.StructuredResponse{Content: `{"satisfied":false,"missingWork":["첨부 확인 누락"],"reason":"완료 확인 불가"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolSet, observations := completionGateSideEffectToolSetAndObservations()
	request := AgentTurnRequest{
		Prompt:          "오래된 작업을 삭제해줘",
		ToolSet:         toolSet,
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_delete"}},
	}
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)

	transition := services.runner.finalizeCompletionState(context.Background(), taskRun.TaskRunID, "step-1", request, nil, observations, nil, nil, CompletionState{}, "오래된 작업을 삭제했습니다.")

	if transition.IsCompleted {
		t.Fatalf("expected the turn to keep retrying when the judge is unsatisfied and the budget is not expired, got %+v", transition)
	}
	completedTaskRun, isFound := services.taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || completedTaskRun.Status == taskstate.TaskStatusCompleted {
		t.Fatalf("expected the task run to remain uncompleted, got %+v", completedTaskRun)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRun.TaskRunID), "agent.completion_state_rejected", "완료 확인 불가") {
		t.Fatal("expected the unsatisfied judge verdict to reject completion and record the rejection reason")
	}
}

func TestCompletionReplyPromptUsesOriginalInstructionForContinuation(t *testing.T) {
	prompt := buildCompletionReplyPrompt(AgentTurnRequest{
		Prompt: "승인",
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "고객지원 보고서를 JSON으로 만들어 이 DM에 첨부해줘.",
		},
	}, nil, nil)

	if !strings.Contains(prompt, "고객지원 보고서를 JSON으로 만들어 이 DM에 첨부해줘.") {
		t.Fatalf("expected original instruction in completion prompt, got %q", prompt)
	}
	if strings.Contains(prompt, "Original request:\n승인") {
		t.Fatalf("continuation prompt must not replace the original instruction: %q", prompt)
	}
}

func TestCompletionGateRejectsSatisfiedFinishWithUnresolvedFailureDebt(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionGate(
		nil,
		nil,
		[]turnObservation{
			newFailureObservation("obs-001", "continue", "file_read", "permission denied", toolcontract.FailurePermissionDenied, toolcontract.FailureCodes.AccessDenied, "file_read"),
		},
		nil,
		turnActionDocument{
			Action:             "finish",
			Message:            "버튼 기능을 직접 구현할 수 있는 상태가 아닙니다.",
			FailureResolution:  failureResolutionNoToolFallback,
			GoalStatus:         "satisfied",
			GoalSatisfied:      &goalSatisfied,
			HasRemainingWork:   true,
			CompletionEvidence: []completionEvidenceReference{},
			QualityReview:      []qualityReviewItem{},
			RemainingWork:      "권한 확인 후 재시도 필요",
		},
	)
	if result.IsSatisfied {
		t.Fatal("expected completion gate to reject unresolved failure debt")
	}
	if !strings.Contains(result.Message, "hasRemainingWork") {
		t.Fatalf("expected remaining work guidance, got %q", result.Message)
	}
}

func TestCompletionGateAcceptsZeroRemainingWork(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionGate(nil, nil, nil, nil, turnActionDocument{
		Action:             "finish",
		Message:            "작업을 완료했습니다.",
		GoalStatus:         "satisfied",
		GoalSatisfied:      &goalSatisfied,
		HasRemainingWork:   false,
		CompletionEvidence: []completionEvidenceReference{},
		QualityReview:      []qualityReviewItem{},
		RemainingWork:      "0",
	})
	if !result.IsSatisfied {
		t.Fatalf("expected zero remaining work to satisfy completion gate, got %q", result.Message)
	}
}

func TestCompletionGateRejectsEvidenceThatMissesDeclaredResultCondition(t *testing.T) {
	goalSatisfied := true
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{{
		Name:         "artifact_review",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		ResultContract: &toolcontract.ToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{"passed":{"type":"boolean"}},"required":["passed"],"additionalProperties":false}`),
			EvidenceCondition: &toolcontract.EvidenceCondition{
				ResultField: "passed",
				Equals:      json.RawMessage(`true`),
			},
		},
	}})
	observation := turnObservation{
		ObservationID: "obs-001",
		Tool:          "artifact_review",
		Output:        toolcontract.ToolOutput{Data: json.RawMessage(`{"passed":false}`)},
	}

	result := validateCompletionGate(toolSet, []toolUseRequirement{{ToolName: "artifact_review"}}, []turnObservation{observation}, nil, turnActionDocument{
		Action:           "finish",
		Message:          "검토했습니다.",
		GoalStatus:       "satisfied",
		GoalSatisfied:    &goalSatisfied,
		HasRemainingWork: false,
		CompletionEvidence: []completionEvidenceReference{{
			ObservationID: "obs-001",
			ToolName:      "artifact_review",
		}},
	})

	if result.IsSatisfied || result.EvidenceKind != evidenceKindRequiredTool {
		t.Fatalf("expected failed review verdict to be rejected as completion evidence, got %+v", result)
	}
	if observation.Failed() {
		t.Fatal("expected review issues to remain available to the model as successful tool output")
	}
}

func TestCompletionGateRejectsExternalSendFinishWithoutSendEvidence(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionGateForRequestWithRecoveryBudget(
		AgentTurnRequest{
			RequiredEvidenceTools: []string{"mail_message_send"},
			ToolSet:               externalSendCompletionTestToolSet(t, "mail_message_send"),
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
	if len(result.SuggestedNextTools) != 1 || result.SuggestedNextTools[0] != "mail_message_send" {
		t.Fatalf("expected suggested send tool, got %+v", result.SuggestedNextTools)
	}
	observation := withCompletionGateRecoveryPacket(completionGateObservation(1, result, nil), result)
	if observation.RecoveryPacket == nil {
		t.Fatal("expected recovery packet")
	}
	if len(observation.RecoveryPacket.AllowedTools) != 1 || observation.RecoveryPacket.AllowedTools[0] != "mail_message_send" {
		t.Fatalf("expected recovery packet allowed send tool, got %+v", observation.RecoveryPacket.AllowedTools)
	}
}

func TestCompletionGateRejectsRequiredSendToolFinishWithSuggestedNextTools(t *testing.T) {
	goalSatisfied := true
	toolSet := externalSendCompletionTestToolSet(t, "slack.message.send")
	result := validateCompletionGate(
		toolSet,
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

func externalSendCompletionTestToolSet(t *testing.T, toolName string) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{toolName})
	if errorValue := registerTestTool(toolSet, toolcontract.ToolDefinition{
		Name:            toolName,
		Description:     "Send a message.",
		InputSchema:     json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		SideEffectClass: toolcontract.ToolSideEffectExternalSend,
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("sent"), nil
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	return toolSet
}

func TestCompletionGateAcceptsExternalSendFinishWithSendEvidence(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionGateForRequestWithRecoveryBudget(
		AgentTurnRequest{RequiredEvidenceTools: []string{"message_send"}},
		nil,
		[]turnObservation{newContentObservation("obs-001", "continue", "message_send", "sent")},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "전송했습니다.",
			GoalStatus:    "satisfied",
			GoalSatisfied: &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{{
				ObservationID: "obs-001",
				ToolName:      "message_send",
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
		RequiredEvidenceTools: []string{"site_serve", "site_serve", "message_send", "site_list"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site_serve", "site_serve", "message_send", "site_list"},
			ExpectedResults: []ExpectedResult{
				{ID: "site-public-link", Type: ExpectedResultTypeLink, Description: "public site URL", Required: true},
				{ID: "final-message", Type: ExpectedResultTypeMessage, Description: "final reply to the user", Required: true},
			},
		},
	}
	observations := []turnObservation{
		newContentObservation("obs-004", "continue", "site_serve", `{"siteID":"site-1","status":"published","publishedURL":"https://banchan-table.example.test"}`),
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
		t.Fatalf("expected site finish backed by a successful site_serve observation to pass without message_send evidence, got %q", result.Message)
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
		AgentTurnRequest{ToolSet: newTestToolSet([]string{"calendar_add"})},
		nil,
		[]turnObservation{newContentObservation("obs-001", "continue", "calendar_add", `{"id":"event-1","title":"미팅"}`)},
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
		recoveryDecisionDocument("ask the user to retry file generation", "explain that attachment evidence was missing"),
	}, textResponses: []string{
		"첨부 파일을 만들거나 보냈다고 확인할 근거가 없어 여기서 멈췄어요. 파일이 필요하면 다시 시도해 주세요.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, RecoveryBudget: exhaustedRecoveryBudgetForTest()})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "파일 만들어서 보내줘",
		RequiredEvidenceTools: []string{
			"file_deliver",
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
	if result.TaskRun.Status != taskstate.TaskStatusFailed {
		t.Fatalf("expected failed task after missing file evidence, got %s", result.TaskRun.Status)
	}
	if !strings.Contains(result.UserNotice, "근거가 없어") {
		t.Fatalf("expected generated failure reply, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "required file expected result") {
		t.Fatal("expected completion gate to reject required file without evidence")
	}
}

func TestExpectedResultCompletionGateSuggestsFileDelivery(t *testing.T) {
	goalSatisfied := true
	request := AgentTurnRequest{
		ToolSet: newTestToolSet([]string{toolcontract.FileDeliverToolName}),
		OutcomeContract: OutcomeContract{
			RequiredAttachmentSuffixes: []string{".pdf"},
			ExpectedResults: []ExpectedResult{{
				Type:        ExpectedResultTypeFile,
				Description: "attached report",
				Required:    true,
			}},
		},
	}
	action := turnActionDocument{
		Action:        "finish",
		Message:       "완료했습니다.",
		GoalStatus:    "satisfied",
		GoalSatisfied: &goalSatisfied,
	}

	t.Run("missing attachment", func(t *testing.T) {
		result := validateExpectedResultCompletionGate(request, nil, nil, action, defaultRecoveryBudget())

		assertSameStrings(t, result.SuggestedNextTools, []string{toolcontract.FileDeliverToolName})
	})

	t.Run("wrong suffix", func(t *testing.T) {
		observation := newContentObservation("obs-001", "continue", toolcontract.FileDeliverToolName, "attached")
		observation.Attachments = []toolcontract.FileAttachment{{Filename: "report.md"}}
		action.CompletionEvidence = []completionEvidenceReference{{
			ObservationID: observation.ObservationID,
			ToolName:      toolcontract.FileDeliverToolName,
		}}

		result := validateExpectedResultCompletionGate(request, []turnObservation{observation}, nil, action, defaultRecoveryBudget())

		assertSameStrings(t, result.SuggestedNextTools, []string{toolcontract.FileDeliverToolName})
	})
}

func TestAgentTurnRunnerRejectsHtmlClaimBackedByMarkdownAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"DESIGN.md"}}`,
		finishMessageWithEvidence("HTML 파일을 전달해 드립니다.", "obs-001", "file_deliver", 0),
		`{"action":"fail","reason":"html attachment missing"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
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
		RequiredEvidenceTools:      []string{"file_deliver"},
		RequiredAttachmentSuffixes: []string{".html"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusFailed {
		t.Fatalf("expected failed task after mismatched attachment claim, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", ".html") {
		t.Fatal("expected completion gate to reject missing html attachment")
	}
}

func TestAgentTurnRunnerAcceptsHtmlRequestWithHtmlAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"deck.html"}}`,
		finishMessageWithEvidence("HTML 파일을 전달해 드립니다.", "obs-001", "file_deliver", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
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

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "html만 주면 돼",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		RequiredEvidenceTools:      []string{"file_deliver"},
		RequiredAttachmentSuffixes: []string{".html"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.html" {
		t.Fatalf("expected html attachment, got %+v", result.Attachments)
	}
}

func TestValidateCompletionEvidenceDoesNotDeliverImageReadAttachment(t *testing.T) {
	attachmentIndex := 0
	attachments, errorValue := validateCompletionEvidence(nil, nil, []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "image_read",
		Attachments: []toolcontract.FileAttachment{{
			DevicePath:    "/workspace/inbox/mascot.png",
			Filename:      "mascot.png",
			ContentType:   "image/png",
			ContentBase64: "aW1hZ2U=",
		}},
	}}, []completionEvidenceReference{{
		ObservationID:   "obs-001",
		ToolName:        "image_read",
		AttachmentIndex: &attachmentIndex,
	}})
	if errorValue != nil {
		t.Fatalf("expected image_read evidence to validate: %v", errorValue)
	}
	if len(attachments) != 0 {
		t.Fatalf("expected image_read evidence to produce no delivery attachments, got %+v", attachments)
	}
}

func TestAgentTurnRunnerRequiresToolEvidenceBeforeFinishMessage(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("browser tool is unavailable"),
		`{"action":"continue","toolName":"memory_search","toolInput":{}}`,
		finishMessageDocument("still no screenshot"),
		`{"action":"continue","toolName":"browser_screenshot","toolInput":{}}`,
		finishMessageWithEvidence("observed", "obs-004", "browser_screenshot", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestCapabilityToolSet([]string{"browser_screenshot", "memory_search"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "memory_search"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess(`[]`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "browser_screenshot"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: `{"devicePath":"/tmp/internkim-companion-files/screenshot.png"}`},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath: "/tmp/internkim-companion-files/screenshot.png",
				Filename:   "screenshot.png",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "구글 서치바에 hello world라고 치고 스크린샷",
		TaskShape:             TaskShapeBrowserHandoffTask,
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"browser_screenshot"},
	})
	if errorValue != nil {
		t.Fatalf("expected browser tool requirement to recover: %v", errorValue)
	}
	if result.FinishMessage != "observed" {
		t.Fatalf("expected final reply after tool use, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "browser_") {
		t.Fatal("expected completion requirement event")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.memory_search.result", "[]") {
		t.Fatal("expected memory search observation before screenshot")
	}
	if len(result.Attachments) != 1 || result.Attachments[0].DevicePath != "/tmp/internkim-companion-files/screenshot.png" {
		t.Fatalf("expected screenshot attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.browser_screenshot.result", "/tmp/internkim-companion-files/screenshot.png") {
		t.Fatal("expected browser screenshot observation")
	}
}

func TestAgentTurnRunnerRequiresSelectedSkillEvidenceBeforeFinishMessage(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("PPT 못 만들어요"),
		`{"action":"continue","message":"PPTX를 첨부했습니다: deck.pptx","toolName":"file_deliver","toolInput":{"path":"deck.pptx"}}`,
		finishMessageWithEvidence("PPTX를 첨부했습니다: deck.pptx", "obs-003", "file_deliver", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
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
		RequiredEvidenceTools: []string{"file_deliver"},
	})
	if errorValue != nil {
		t.Fatalf("expected required evidence to recover: %v", errorValue)
	}
	if !strings.Contains(result.FinishMessage, "deck.pptx") {
		t.Fatalf("expected artifact-aware reply, got %q", result.FinishMessage)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected pptx attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "file_deliver") {
		t.Fatal("expected completion required event for selected skill evidence")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.evidence_missing", "evidence_missing") {
		t.Fatal("expected structured evidence missing event")
	}
}

func TestAgentTurnRunnerDoesNotRequireNonAttachmentToolInCompletionEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file_write","toolInput":{"path":"tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"deck.html"}}`,
		finishMessageWithEvidence("HTML 파일을 첨부했습니다: deck.html", "obs-002", "file_deliver", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"file_write", "file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_write"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess(`{"path":"tmp/deck/presentation.md","sizeBytes":6}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
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
		RequiredEvidenceTools: []string{"file_write", "file_deliver"},
	})
	if errorValue != nil {
		t.Fatalf("expected required evidence to recover: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, events)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.html" {
		t.Fatalf("expected html attachment, got %+v", result.Attachments)
	}
}

func TestAgentTurnRunnerRequiresAttachmentSuffixEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"DESIGN.md"}}`,
		finishMessageWithEvidence("첨부했습니다.", "obs-001", "file_deliver", 0),
		`{"action":"continue","message":"PPTX를 첨부했습니다: deck.pptx","toolName":"file_deliver","toolInput":{"path":"deck.pptx"}}`,
		finishMessageWithEvidence("PPTX를 첨부했습니다: deck.pptx", "obs-004", "file_deliver", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return toolcontract.ToolResult{}, errorValue
		}
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
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
		RequiredEvidenceTools:      []string{"file_deliver"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected required suffix evidence to recover: %v", errorValue)
	}
	if !strings.Contains(result.FinishMessage, "deck.pptx") {
		t.Fatalf("expected artifact-aware reply, got %q", result.FinishMessage)
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
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"artifacts/deck/deck.html"}}`,
		finishMessageWithEvidence("deck.html 파일을 첨부했습니다.", "obs-001", "file_deliver", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3})
	toolRegistry := newTestToolSet([]string{"file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
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
		RequiredEvidenceTools: []string{"file_deliver"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed turn without runner error: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
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
		`{"action":"finish","message":"자료를 첨부했습니다.","completionSummary":"자료를 첨부했습니다.","replyParts":[{"type":"text","text":"자료를 첨부했습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"file_deliver","attachmentIndex":0},{"observationID":"obs-002","toolName":"file_deliver","attachmentIndex":0}],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return toolcontract.ToolResult{}, errorValue
		}
		attachments := []toolcontract.FileAttachment{{
			DevicePath: request.Path,
			Filename:   filepath.Base(request.Path),
		}}
		return toolcontract.ToolResult{Output: toolcontract.ToolOutput{Content: "file attached"}, Attachments: attachments}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file_deliver"},
		RequiredAttachmentSuffixes: []string{".pptx", ".pdf"},
	})
	if errorValue != nil {
		t.Fatalf("expected auto attachment evidence to succeed: %v", errorValue)
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected two attachments, got %+v", result.Attachments)
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
	if !taskEventsContain(taskEvents, "tool.file_deliver.requested", "deck.pptx") {
		t.Fatal("expected automatic file_deliver request")
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected one model call for the final reply, got %d", len(languageModel.requests))
	}
}

func TestAgentTurnRunnerCompletesAfterRequiredArtifactsExist(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","message":"자료를 완성했습니다.","toolName":"terminal_run","toolInput":{"command":"build deck"}}`,
		`{"action":"finish","message":"완성한 발표 자료를 첨부했습니다.","completionSummary":"발표 자료 완성 및 첨부","replyParts":[{"type":"text","text":"완성한 발표 자료를 첨부했습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-003","toolName":"file_deliver","attachmentIndex":0},{"observationID":"obs-004","toolName":"file_deliver","attachmentIndex":0}],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"terminal_run", "file_deliver"})
	terminalCallCount := 0
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "terminal_run"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		terminalCallCount++
		if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
			return toolcontract.ToolResult{}, errorValue
		}
		writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))
		writeValidPDFTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pdf"))
		return testToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return toolcontract.ToolResult{}, errorValue
		}
		attachments := []toolcontract.FileAttachment{{DevicePath: request.Path, Filename: filepath.Base(request.Path)}}
		return toolcontract.ToolResult{Output: toolcontract.ToolOutput{Content: "file attached"}, Attachments: attachments}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		WorkspaceRootPath:          workspaceRootPath,
		RequiredEvidenceTools:      []string{"file_deliver"},
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
	if result.FinishMessage != "완성한 발표 자료를 첨부했습니다." {
		t.Fatalf("expected post-evidence completion wording, got %q", result.FinishMessage)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected a post-evidence model call, got %d", len(languageModel.requests))
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
	toolRegistry := newTestToolSet([]string{"file_deliver"})
	attachmentCallCount := 0
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		attachmentCallCount++
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "tool", "attachment unavailable"), nil
	})

	_, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file_deliver"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected failed turn to return result without runner error: %v", errorValue)
	}
	if attachmentCallCount != 1 {
		t.Fatalf("expected one automatic attachment attempt, got %d", attachmentCallCount)
	}
}

func TestAgentTurnRunnerBlocksReadableArtifactWithWrongFormat(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"), "not a valid pptx")

	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageWithEvidence("자료를 첨부했습니다.", "obs-001", "file_deliver", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"file_deliver"})
	attachmentCallCount := 0
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		attachmentCallCount++
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
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
		RequiredEvidenceTools:      []string{"file_deliver"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected invalid artifact turn to return result without runner error: %v", errorValue)
	}
	if attachmentCallCount != 0 {
		t.Fatalf("expected wrong-format artifact not to be attached, got %d calls", attachmentCallCount)
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("expected no wrong-format artifact attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.validity_review", `"passed":false`) {
		t.Fatal("expected failed format validity review event")
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

	languageModel := &sequenceLanguageModel{contents: []string{finishMessageDocument("unused")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return toolcontract.ToolResult{}, errorValue
		}
		attachments := []toolcontract.FileAttachment{{DevicePath: request.Path, Filename: filepath.Base(request.Path)}}
		return toolcontract.ToolResult{Output: toolcontract.ToolOutput{Content: "file attached"}, Attachments: attachments}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file_deliver"},
		RequiredAttachmentSuffixes: []string{".pptx", ".pdf"},
	})
	if errorValue != nil {
		t.Fatalf("expected artifact validity completion without core quality checks: %v", errorValue)
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected attachments, got %+v", result.Attachments)
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
		`{"action":"continue","toolName":"unstable","toolInput":{}}`,
		finishMessageWithEvidence("done", "obs-001", "unstable", 0),
		failureReportDocument("tool failed", "unstable", "{}", toolcontract.FailureCodes.OperationFailed.String(), "unstable", "failed"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"unstable"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "unstable"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "unstable", "failed"), nil
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
	if result.TaskRun.Status != taskstate.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "unknown or failed observation") {
		t.Fatal("expected failed evidence gate event")
	}
}

func TestAgentTurnRunnerNoToolFallbackWaivesFailedRequiredEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"schedule_list","toolInput":{"range":"today"}}`,
		noToolFallbackFinishMessageDocument("Nothing in today's conversation mentioned a scheduled task."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestCapabilityToolSet([]string{"schedule_list"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "schedule_list"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return structuredFailureToolResult("schedule storage unavailable", "schedule storage unavailable", "schedule_lookup_failed", "schedule_lookup", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "what is scheduled for today?",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"schedule_list"},
	})
	if errorValue != nil {
		t.Fatalf("expected no-tool fallback to complete: %v", errorValue)
	}
	if result.FinishMessage != "Nothing in today's conversation mentioned a scheduled task." {
		t.Fatalf("expected direct fallback answer, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.schedule_list.result", toolcontract.FailureCodes.OperationFailed.String()) {
		t.Fatal("expected internal tool failure event to remain recorded")
	}
}

func TestCompletionGateDoesNotWaiveFlowTaskEvidenceWithNoToolFallback(t *testing.T) {
	goalSatisfied := true
	request := AgentTurnRequest{
		RequiredEvidenceTools: []string{"task_add"},
		ToolSet:               newTestToolSet([]string{"task_add"}),
	}
	result := validateCompletionGateForRequestWithRecoveryBudget(
		request,
		deriveToolUseRequirements(request),
		[]turnObservation{
			newFailureObservation("obs-001", "continue", "task_add", "task add failed", toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "task_add"),
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
		t.Fatal("expected flow task finish without successful task_add evidence to be rejected")
	}
	if !strings.Contains(result.Message, "task_add") {
		t.Fatalf("expected missing flow task evidence message, got %q", result.Message)
	}
}

func TestCompletionGateDoesNotSatisfyCalendarAddWithScheduleCreate(t *testing.T) {
	goalSatisfied := true
	request := AgentTurnRequest{
		RequiredEvidenceTools: []string{"calendar_add"},
		ToolSet:               newTestToolSet([]string{"calendar_add", "schedule_create"}),
	}
	result := validateCompletionGateForRequestWithRecoveryBudget(
		request,
		deriveToolUseRequirements(request),
		[]turnObservation{
			newContentObservation("obs-001", "continue", "schedule_create", `{"taskScheduleID":"schedule-1"}`),
			newFailureObservation("obs-002", "continue", "calendar_add", "calendar add failed", toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "calendar_add"),
		},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "일정을 추가했습니다.",
			GoalStatus:    "satisfied",
			GoalSatisfied: &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{{
				ObservationID: "obs-001",
				ToolName:      "schedule_create",
			}},
		},
		defaultRecoveryBudget(),
	)

	if result.IsSatisfied {
		t.Fatal("expected schedule_create not to satisfy calendar_add")
	}
	if !strings.Contains(result.Message, "calendar_add") {
		t.Fatalf("expected missing calendar_add evidence message, got %q", result.Message)
	}
}

func TestCompletionGateDoesNotTreatApprovalAsRequiredEvidence(t *testing.T) {
	goalSatisfied := true
	request := AgentTurnRequest{
		RequiredEvidenceTools: []string{"calendar_add"},
		ToolSet:               newTestToolSet([]string{"calendar_add", toolcontract.AskConfirmToolName}),
	}
	result := validateCompletionGateForRequestWithRecoveryBudget(
		request,
		deriveToolUseRequirements(request),
		[]turnObservation{newContentObservation("obs-001", "continue", toolcontract.AskConfirmToolName, `{"approved":true}`)},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "승인받아 일정을 추가했습니다.",
			GoalStatus:    "satisfied",
			GoalSatisfied: &goalSatisfied,
			CompletionEvidence: []completionEvidenceReference{{
				ObservationID: "obs-001",
				ToolName:      toolcontract.AskConfirmToolName,
			}},
		},
		defaultRecoveryBudget(),
	)

	if result.IsSatisfied {
		t.Fatal("expected approval observation not to satisfy calendar_add")
	}
}

func TestCompletionGateRequiresFileDeliverEvidenceEvenWhenFileExists(t *testing.T) {
	goalSatisfied := true
	request := AgentTurnRequest{
		RequiredEvidenceTools: []string{toolcontract.FileDeliverToolName},
		ToolSet:               newTestToolSet([]string{toolcontract.FileDeliverToolName}),
	}
	result := validateCompletionGateForRequestWithRecoveryBudget(
		request,
		deriveToolUseRequirements(request),
		[]turnObservation{newContentObservation("obs-001", "continue", toolcontract.FileWriteToolName, `{"path":"tmp/report.pdf"}`)},
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
	if !strings.Contains(result.Message, toolcontract.FileDeliverToolName) {
		t.Fatalf("expected file_deliver evidence message, got %q", result.Message)
	}
}

func TestAgentTurnRunnerRemovesQualityCriteriaActionAfterCriteriaAreSet(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"set_quality_criteria","qualityCriteria":["done once: criteria are declared"],"goalStatus":"in_progress","goalSatisfied":false}`,
		`{"action":"continue","toolName":"alpha","toolInput":{}}`,
		`{"action":"finish","message":"done","replyParts":[{"type":"text","text":"done"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-002"],"qualityReview":[{"id":"done-once-criteria-are-declared","passed":true,"evidenceIDs":["obs-002"]}]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "alpha"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("alpha result"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "make an artifact",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		OutcomeContract:   OutcomeContract{ArtifactRequirement: ArtifactRequirementPreferred},
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

func TestAgentTurnRunnerDoesNotBlockFinishedExpectedResultForMissingQualityReview(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"set_quality_criteria","qualityCriteria":["visual review: review the artifact"],"goalStatus":"in_progress","goalSatisfied":false}`,
		`{"action":"continue","toolName":"site_serve","toolInput":{"siteID":"site-1"},"nextStepPlan":{"objective":"finish with the public URL","expectedTools":[],"expectedNextResults":["public URL"],"doneCriteria":["public URL is available"],"risk":"none","workingSetReason":"publish satisfies the link expected result"}}`,
		`{"action":"finish","message":"배포했습니다: https://portfolio.example","replyParts":[{"type":"text","text":"배포했습니다: https://portfolio.example"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-002"]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestCapabilityToolSet([]string{"site_serve"})
	registerTestTool(toolRegistry, canonicalLinkToolDefinition("site_serve"), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return canonicalLinkToolResult("https://portfolio.example"), nil
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
		t.Fatalf("expected finish to pass without qualityReview hard gate: %v", errorValue)
	}
	if result.FinishMessage != "배포했습니다: https://portfolio.example" {
		t.Fatalf("expected final publish message, got %q", result.FinishMessage)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "qualityReview") {
		t.Fatal("expected missing qualityReview to remain a review hint, not a completion blocker")
	}
}

func TestAgentTurnRunnerCanonicalLinkGateBlocksEarlyFinish(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file_write","toolInput":{"path":"~/sites/portfolio/app/public/site-content.json","content":"{}"},"nextStepPlan":{"objective":"create draft","expectedTools":[],"expectedNextResults":["draft site project exists"],"doneCriteria":["draft exists"],"risk":"none","workingSetReason":"the draft prepares the project"}}`,
			`{"action":"finish","message":"초안을 만들었습니다.","replyParts":[{"type":"text","text":"초안을 만들었습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"file_write"}]}`,
			`{"action":"continue","toolName":"site_serve","toolInput":{"title":"Portfolio","sourceWorkspacePath":"~/sites/portfolio","mode":"publish"},"nextStepPlan":{"objective":"finish after public URL","expectedTools":[],"expectedNextResults":["public URL exists"],"doneCriteria":["public URL exists"],"risk":"none","workingSetReason":"serve should satisfy the expected result"}}`,
			`{"action":"finish","message":"배포했습니다: https://portfolio.example","replyParts":[{"type":"text","text":"배포했습니다: https://portfolio.example"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-003","toolName":"site_serve"}]}`,
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	toolRegistry := newTestCapabilityToolSet([]string{"file_write", "site_serve"})
	toolCalls := []string{}
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_write"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		toolCalls = append(toolCalls, "file_write")
		return testToolSuccess(`{"path":"~/sites/portfolio/app/public/site-content.json"}`), nil
	})
	registerTestTool(toolRegistry, canonicalLinkToolDefinition("site_serve"), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		toolCalls = append(toolCalls, "site_serve")
		return canonicalLinkToolResult("https://portfolio.example"), nil
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
	if strings.Join(toolCalls, ",") != "file_write,site_serve" {
		t.Fatalf("expected draft creation then serve, got %+v", toolCalls)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "canonical link result") {
		t.Fatal("expected canonical link delivery gate event")
	}
}

func TestCompletionGateSkipsResultVerifierForEmptyContract(t *testing.T) {
	languageModel := &sequenceLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	goalSatisfied := true

	result := validateCompletionGateForRequestWithExpectedResults(AgentTurnRequest{}, nil, nil, nil, nil, turnActionDocument{
		Action:             "finish",
		Message:            "완료했습니다.",
		GoalStatus:         "satisfied",
		GoalSatisfied:      &goalSatisfied,
		CompletionEvidence: nil,
	}, services.runner.options.RecoveryBudget)

	if !result.IsSatisfied {
		t.Fatalf("expected empty contract to stay on fast path, got %+v", result)
	}
}

func TestCompletionGateUsesNoResultVerifierForExpectedResults(t *testing.T) {
	languageModel := &sequenceLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	goalSatisfied := true
	observations := []turnObservation{
		newContentObservation("obs-001", "continue", "task_update", `{"id":"task-1","status":"진행"}`),
	}
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{"task_update"},
		ExpectedResults: []ExpectedResult{
			{ID: "task-status-update", Type: ExpectedResultTypeMessage, Description: "task status is updated", Required: true},
			{ID: "final-message", Type: ExpectedResultTypeMessage, Description: "final reply to the user", Required: true},
		},
	}
	result := validateCompletionGateForRequestWithExpectedResults(AgentTurnRequest{
		ToolSet:         newTestToolSet([]string{"task_update"}),
		OutcomeContract: contract,
	}, nil, observations, nil, nil, turnActionDocument{
		Action:        "finish",
		Message:       "업무 상태를 진행으로 변경했습니다.",
		GoalStatus:    "satisfied",
		GoalSatisfied: &goalSatisfied,
		CompletionEvidence: []completionEvidenceReference{{
			ObservationID: "obs-001",
			ToolName:      "task_update",
		}},
	}, services.runner.options.RecoveryBudget)

	if !result.IsSatisfied {
		t.Fatalf("expected verified task update and ready final message to complete, got %+v", result)
	}
}

func TestCompletionGateUsesAttachmentsFromCompletionEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	goalSatisfied := true
	observation := newContentObservation("obs-001", "continue", toolcontract.FileDeliverToolName, "file attached")
	observation.Attachments = []toolcontract.FileAttachment{{
		DevicePath:  "/workspace/private/people/person-1/report.json",
		Filename:    "report.json",
		ContentType: "application/json",
	}}
	result := validateCompletionGateForRequestWithExpectedResults(AgentTurnRequest{
		ToolSet: newTestToolSet([]string{toolcontract.FileDeliverToolName}),
		OutcomeContract: OutcomeContract{
			ArtifactRequirement:        ArtifactRequirementRequired,
			RequiredAttachmentSuffixes: []string{".json"},
			ExpectedResults: []ExpectedResult{{
				ID:          "attached-file",
				Type:        ExpectedResultTypeFile,
				Description: "requested JSON file attached",
				Required:    true,
			}},
		},
	}, nil, []turnObservation{observation}, nil, nil, turnActionDocument{
		Action:        "finish",
		Message:       "JSON 파일을 첨부했습니다.",
		GoalStatus:    "satisfied",
		GoalSatisfied: &goalSatisfied,
		CompletionEvidence: []completionEvidenceReference{{
			ObservationID: "obs-001",
			ToolName:      toolcontract.FileDeliverToolName,
		}},
	}, services.runner.options.RecoveryBudget)

	if !result.IsSatisfied || len(result.Attachments) != 1 {
		t.Fatalf("expected completion evidence attachment to satisfy verification, got %+v", result)
	}
}

func TestAgentTurnRunnerUsesNoToolChatWhenCompletionEvidenceIsReady(t *testing.T) {
	languageModel := &completionReplyLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	workspaceRootPath := t.TempDir()
	artifactPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "report", "report.json")
	toolSet := newTestToolSet([]string{"file_write", toolcontract.FileDeliverToolName})
	registerTestTool(toolSet, toolcontract.ToolDefinition{Name: "file_write"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		if errorValue := os.MkdirAll(filepath.Dir(artifactPath), 0700); errorValue != nil {
			return toolcontract.ToolResult{}, errorValue
		}
		if errorValue := os.WriteFile(artifactPath, []byte(`{"status":"ready"}`), 0600); errorValue != nil {
			return toolcontract.ToolResult{}, errorValue
		}
		return toolcontract.ToolResult{Output: toolcontract.ToolOutput{Content: "file written"}}, nil
	})
	registerTestTool(toolSet, toolcontract.ToolDefinition{Name: toolcontract.FileDeliverToolName}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath:  artifactPath,
				Filename:    "report.json",
				ContentType: "application/json",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "JSON 보고서를 이 DM에 첨부해줘.",
		ResponseLanguage:           "ko",
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              time.Now().Add(-time.Minute),
		ToolSet:                    toolSet,
		PinnedToolNames:            toolSet.ListToolNames(),
		RequiredEvidenceTools:      []string{"file_write", toolcontract.FileDeliverToolName},
		RequiredAttachmentSuffixes: []string{".json"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools:      []string{"file_write", toolcontract.FileDeliverToolName},
			ArtifactRequirement:        ArtifactRequirementRequired,
			RequiredAttachmentSuffixes: []string{".json"},
			ExpectedResults: []ExpectedResult{{
				ID:          "attached-file",
				Type:        ExpectedResultTypeFile,
				Description: "requested JSON file attached",
				Required:    true,
			}},
		},
	})

	if errorValue != nil || result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected completed file delivery, got result=%+v error=%v", result, errorValue)
	}
	if result.FinishMessage != "JSON 보고서를 첨부했습니다." || len(result.Attachments) != 1 {
		t.Fatalf("expected completion reply and attachment, got %+v", result)
	}
	if languageModel.actionCalls != 1 || len(languageModel.completionRequests) != 1 {
		t.Fatalf("expected one tool action then one completion reply, got actions=%d completions=%d", languageModel.actionCalls, len(languageModel.completionRequests))
	}
	request := languageModel.completionRequests[0]
	if len(request.Tools) != 0 || len(request.ToolChoice) != 0 ||
		!strings.Contains(request.Messages[0].Content, "file written") ||
		!strings.Contains(request.Messages[0].Content, "report.json") {
		t.Fatalf("expected evidence-grounded no-tool completion request, got %+v", request)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(taskEvents, "agent.finalizer_rejected", "") {
		t.Fatal("expected completion-ready chat to avoid structured finalizer rejection")
	}
	if !taskEventsContain(taskEvents, "tool.file_deliver.result", "report.json") {
		t.Fatal("expected completion state to deliver the written file")
	}
	if !taskEventsContain(taskEvents, "agent.completion_state_finalized", `"observationID":"obs-001"`) ||
		!taskEventsContain(taskEvents, "agent.completion_state_finalized", `"observationID":"obs-002"`) {
		t.Fatal("expected finalization event to preserve exact write and delivery evidence")
	}
}

func TestRejectedFinishWordingSurvivesAttachmentRepair(t *testing.T) {
	finishMessage := "보고서 이름은 '고객지원 주간 운영 점검', 상태는 '검토 중', 담당은 '운영팀'입니다."
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file_write","toolInput":{"path":"report.json","content":"{\"status\":\"ready\"}"}}`,
		`{"action":"finish","message":"` + finishMessage + `","replyParts":[{"type":"text","text":"` + finishMessage + `"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"file_write"}]}`,
		`{"action":"continue","toolName":"file_deliver","toolInput":{"files":[{"path":"report.json"}]}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	workspaceRootPath := t.TempDir()
	artifactPath := filepath.Join(workspaceRootPath, "report.json")
	toolSet := newTestToolSet([]string{"file_write", toolcontract.FileDeliverToolName})
	registerTestTool(toolSet, toolcontract.ToolDefinition{Name: "file_write"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		if errorValue := os.WriteFile(artifactPath, []byte(`{"status":"ready"}`), 0600); errorValue != nil {
			return toolcontract.ToolResult{}, errorValue
		}
		return toolcontract.ToolResult{Output: toolcontract.ToolOutput{Content: "file written"}}, nil
	})
	registerTestTool(toolSet, toolcontract.ToolDefinition{Name: toolcontract.FileDeliverToolName}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath:  artifactPath,
				Filename:    "report.json",
				ContentType: "application/json",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "JSON 파일을 읽어서 보고서 이름, 상태, 담당을 확인해줘.",
		ResponseLanguage:           "ko",
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              time.Now().Add(-time.Minute),
		ToolSet:                    toolSet,
		PinnedToolNames:            toolSet.ListToolNames(),
		RequiredEvidenceTools:      []string{toolcontract.FileDeliverToolName},
		RequiredAttachmentSuffixes: []string{".json"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools:      []string{toolcontract.FileDeliverToolName},
			ArtifactRequirement:        ArtifactRequirementRequired,
			RequiredAttachmentSuffixes: []string{".json"},
			ExpectedResults: []ExpectedResult{{
				ID:          "attached-file",
				Type:        ExpectedResultTypeFile,
				Description: "requested JSON file attached",
				Required:    true,
			}},
		},
	})

	if errorValue != nil || result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected completed delivery repair, got result=%+v error=%v", result, errorValue)
	}
	if result.FinishMessage != finishMessage {
		t.Fatalf("expected the rejected finish wording to be delivered verbatim, got %q", result.FinishMessage)
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("expected one delivered attachment, got %+v", result.Attachments)
	}
}

type completionReplyLanguageModel struct {
	actionCalls        int
	completionRequests []model.ChatCompletionRequest
}

func (languageModel *completionReplyLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *completionReplyLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{}, fmt.Errorf("unexpected structured schema %s", request.StructuredOutputSchema.Name)
}

func (languageModel *completionReplyLanguageModel) GenerateChatCompletion(_ context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	switch request.SchemaName {
	case agentActionSchemaName:
		languageModel.actionCalls++
		if languageModel.actionCalls > 1 {
			return model.ChatCompletionResponse{}, fmt.Errorf("unexpected repeated action request")
		}
		return model.ChatCompletionResponse{
			FinishReason: "tool_calls",
			Message: model.ChatCompletionMessage{
				Role: "assistant",
				ToolCalls: []model.ChatCompletionToolCall{
					nativeAgentActionToolCall("file_write", `{"path":"report.json","content":"{\"status\":\"ready\"}"}`),
				},
			},
		}, nil
	case completionReplySchemaName:
		languageModel.completionRequests = append(languageModel.completionRequests, request)
		return model.ChatCompletionResponse{
			FinishReason: "stop",
			Message:      model.ChatCompletionMessage{Role: "assistant", Content: "JSON 보고서를 첨부했습니다."},
		}, nil
	default:
		return model.ChatCompletionResponse{}, fmt.Errorf("unexpected chat schema %s", request.SchemaName)
	}
}

func TestCompletionGateUsesNoVerifierForExactToolOnlyContract(t *testing.T) {
	languageModel := &sequenceLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	goalSatisfied := true
	observations := []turnObservation{
		newContentObservation("obs-001", "continue", "task_add", `{"id":"task-1","title":"고객지원 분기 결산","endDate":"2026-07-17"}`),
	}
	contract := OutcomeContract{RequiredEvidenceTools: []string{"task_add"}}

	result := validateCompletionGateForRequestWithExpectedResults(AgentTurnRequest{
		ToolSet:         newTestToolSet([]string{"task_add"}),
		OutcomeContract: contract,
	}, []toolUseRequirement{{ToolName: "task_add"}}, observations, nil, nil, turnActionDocument{
		Action:        "finish",
		Message:       "고객지원 분기 결산 업무를 7월 17일 마감으로 등록했습니다.",
		GoalStatus:    "satisfied",
		GoalSatisfied: &goalSatisfied,
		CompletionEvidence: []completionEvidenceReference{{
			ObservationID: "obs-001",
			ToolName:      "task_add",
		}},
	}, services.runner.options.RecoveryBudget)

	if !result.IsSatisfied {
		t.Fatalf("expected exact task_add evidence and post-evidence finish judgment to complete, got %+v", result)
	}
}

func TestCompletionGateDoesNotRequirePreferredArtifact(t *testing.T) {
	languageModel := &sequenceLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	goalSatisfied := true

	result := validateCompletionGateForRequestWithExpectedResults(AgentTurnRequest{
		ToolSet: newTestToolSet([]string{"file_deliver"}),
		OutcomeContract: OutcomeContract{
			ArtifactRequirement: ArtifactRequirementPreferred,
		},
	}, nil, nil, nil, nil, turnActionDocument{
		Action:             "finish",
		Message:            "완료했습니다.",
		GoalStatus:         "satisfied",
		GoalSatisfied:      &goalSatisfied,
		CompletionEvidence: nil,
	}, services.runner.options.RecoveryBudget)

	if !result.IsSatisfied {
		t.Fatalf("expected preferred artifact to remain optional, got %+v", result)
	}
}

func TestCompletionGateRequiresOneSuccessfulToolFromEachEvidenceGroup(t *testing.T) {
	goalSatisfied := true
	contract := OutcomeContract{RequiredEvidenceAnyOf: [][]string{{"task_add", "task_update"}, {"task.history"}}}
	observations := []turnObservation{newContentObservation("obs-001", "continue", "task_update", `{"id":"task-1"}`)}

	result := validateCompletionGateForRequestWithExpectedResults(AgentTurnRequest{OutcomeContract: contract}, nil, observations, nil, nil, turnActionDocument{
		Action: "finish", Message: "완료했습니다.", GoalStatus: "satisfied", GoalSatisfied: &goalSatisfied,
	}, defaultRecoveryBudget())

	if result.IsSatisfied {
		t.Fatal("expected the unsatisfied task.history evidence group to block completion")
	}
	if strings.Join(result.SuggestedNextTools, ",") != "task.history" {
		t.Fatalf("expected missing group tools, got %+v", result.SuggestedNextTools)
	}
}

func TestAgentTurnRunnerExpectedResultsRequireTheirTypedToolEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"site_serve","toolInput":{"siteID":"site-1","message":"Publish"},"nextStepPlan":{"objective":"finish with public URL","expectedTools":[],"expectedNextResults":["public URL exists"],"doneCriteria":["public URL exists"],"risk":"none","workingSetReason":"publish should satisfy the expected result"}}`,
			`{"action":"finish","message":"배포했습니다: https://portfolio.example","replyParts":[{"type":"text","text":"배포했습니다: https://portfolio.example"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"site_serve"}]}`,
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"site_serve"})
	registerTestTool(toolRegistry, canonicalLinkToolDefinition("site_serve"), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return canonicalLinkToolResult("https://portfolio.example"), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: toolcontract.FileDeliverToolName}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.NotFound, "test_tool", "tool is not registered"), nil
	})
	toolRegistry = toolRegistry.WithAdditionalAllowedToolNames([]string{toolcontract.FileDeliverToolName})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "개인 홈페이지 배포해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"site_serve"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site_serve"},
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
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if strings.Contains(result.FinishMessage, "첨부") {
		t.Fatalf("expected link result, got %q", result.FinishMessage)
	}
}

func TestAgentTurnRunnerFileExpectedResultRequiresAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file.promote","toolInput":{"path":"tmp/deck/build/deck.pptx","destinationDirectoryPath":"artifacts/deck","overwrite":true},"nextStepPlan":{"objective":"attach promoted file","expectedTools":["file_deliver"],"expectedNextResults":["attached pptx"],"doneCriteria":["file attached"],"risk":"none","workingSetReason":"file deliverable requires attachment"}}`,
			`{"action":"finish","message":"PPTX를 첨부했습니다.","replyParts":[{"type":"text","text":"PPTX를 첨부했습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"file.promote"}]}`,
			`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"artifacts/deck/deck.pptx"},"nextStepPlan":{"objective":"finish","expectedTools":[],"expectedNextResults":["final message"],"doneCriteria":["attached file delivered"],"risk":"none","workingSetReason":"attachment now exists"}}`,
			finishMessageWithEvidence("PPTX를 첨부했습니다.", "obs-003", "file_deliver", 0),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	toolRegistry := newTestCapabilityToolSet([]string{"file.promote"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file.promote"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess(`{"path":"artifacts/deck/deck.pptx"}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath:  "/tmp/deck.pptx",
				Filename:    "deck.pptx",
				ContentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			}},
		}, nil
	})

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
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "file_deliver") {
		t.Fatal("expected completion gate to require file_deliver")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.file_deliver.requested", "deck.pptx") {
		t.Fatal("expected file_deliver after promoted-only finish was rejected")
	}
}

func TestAgentTurnRunnerFinalizesOneShotEvidenceToolAfterSuccess(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"calendar_add","toolInput":{"title":"휴가","startISO":"2026-05-10T00:00:00+09:00","endISO":"2026-05-13T00:00:00+09:00","timeZone":"Asia/Seoul","isAllDay":true}}`,
		finishMessageWithEvidence("휴가 일정을 등록했습니다.", "obs-001", "calendar_add", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestCapabilityToolSet([]string{"calendar_add"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "calendar_add"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"id":"event-1","title":"휴가","startISO":"2026-05-10T00:00:00+09:00","endISO":"2026-05-13T00:00:00+09:00","timeZone":"Asia/Seoul","isAllDay":true}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "내일부터 화요일까지 휴가 등록해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"calendar_add"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed calendar turn: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected one calendar write, got %d", toolCallCount)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected a final model reply after evidence success, got %d requests", len(languageModel.requests))
	}
	if result.FinishMessage != "휴가 일정을 등록했습니다." {
		t.Fatalf("expected model-authored finish reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.action", "finish") {
		t.Fatal("expected model finish action after calendar evidence")
	}
}

func TestAgentTurnRunnerFinalizesScheduleCreateAfterSuccess(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"schedule_create","toolInput":{"taskInstruction":"현재 대화에 \"죄송합니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}}`,
		finishMessageWithEvidence("반복 일정을 만들었습니다.", "obs-001", "schedule_create", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestCapabilityToolSet([]string{"schedule_create"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "schedule_create"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"taskScheduleID":"schedule-1","taskInstruction":"현재 대화에 \"죄송합니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"nextRunAt":"2026-05-09T05:07:00Z"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "1분에 한 번씩 나한테 죄송합니다 10번 해봐",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"schedule_create"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed schedule turn: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected one schedule create, got %d", toolCallCount)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected a final model reply after schedule success, got %d requests", len(languageModel.requests))
	}
	if result.FinishMessage != "반복 일정을 만들었습니다." {
		t.Fatalf("expected model-authored finish reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.action", "finish") {
		t.Fatal("expected model finish action after schedule evidence")
	}
}

func TestAgentTurnRunnerDoesNotBlockTerminalRerunForMissingFile(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal_run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"file_write","toolInput":{"path":"tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"continue","toolName":"terminal_run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 6})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal_run", "file_write"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "terminal_run"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		terminalCallCount++
		if terminalCallCount == 1 {
			return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "terminal_run", `{"exitCode":1,"stdout":"","stderr":"Error: presentation.md not found. Create presentation.md or set SRC=yourfile.md\n","timedOut":false}`), nil
		}
		return testToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_write"}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "tool", errorValue.Error()), nil
		}
		return testToolSuccess(`{"path":"` + input.Path + `","sizeBytes":5}`), nil
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
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 2 {
		t.Fatalf("expected terminal rerun to remain available, got %d terminal calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "presentation.md") {
		t.Fatal("did not expect terminal precondition block event")
	}
}

func TestAgentTurnRunnerStopsRepeatedMissingEvidenceState(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"terminal_run","toolInput":{"command":"build deck"}}`,
			noToolFallbackFinishMessageDocument("텍스트로 대신 드립니다."),
			noToolFallbackFinishMessageDocument("텍스트로 대신 드립니다."),
			noToolFallbackFinishMessageDocument("텍스트로 대신 드립니다."),
			recoveryDecisionDocument("stop the repeated state", "report the missing artifact"),
		},
		textResponses: []string{"PPTX 첨부를 완료하지 못했습니다. 빌드 실패 뒤에도 필수 첨부 증거가 없어 작업을 중단했습니다."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 40, RecoveryAttemptLimit: 3})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal_run", "file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "terminal_run"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		terminalCallCount++
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "terminal_run", `{"exitCode":1,"stderr":"EACCES: permission denied, open 'deck.html'"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		RequiredEvidenceTools:      []string{"file_deliver"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected repeated state stop without error: %v", errorValue)
	}
	if result.TaskRun.Status == taskstate.TaskStatusRunning {
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
		`{"action":"continue","toolName":"terminal_run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"file_write","toolInput":{"path":"tmp/deck/DESIGN.md","content":"colors: blue"}}`,
		`{"action":"continue","toolName":"terminal_run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 6})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal_run", "file_write"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "terminal_run"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		terminalCallCount++
		if terminalCallCount == 1 {
			return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "terminal_run", `{"exitCode":1,"stdout":"","stderr":"DESIGN.md is missing colors:\n","timedOut":false}`), nil
		}
		return testToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_write"}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "tool", errorValue.Error()), nil
		}
		return testToolSuccess(`{"path":"` + input.Path + `","sizeBytes":12}`), nil
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
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
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
		`{"action":"continue","toolName":"terminal_run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"file_write","toolInput":{"path":"tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"continue","toolName":"terminal_run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"finish","message":"done","replyParts":[{"type":"text","text":"done"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"file_write"}]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5, MaxToolCallCount: 5})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal_run", "file_write"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "terminal_run"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		terminalCallCount++
		return testToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_write"}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "tool", errorValue.Error()), nil
		}
		return testToolSuccess(`{"path":"` + input.Path + `","sizeBytes":5}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "build deck",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"file_write"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 2 {
		t.Fatalf("expected rebuild after file_write to run instead of duplicate rejection, got %d calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "first required workspace file") {
		t.Fatal("did not expect required file_write precondition block event")
	}
}

func TestCompletionGateObservationStatesZeroToolRealityForFirstTurnFinish(t *testing.T) {
	result := completionGateResult{Message: "completionEvidence references an unknown observation", EvidenceKind: "evidence_reference_invalid"}
	observation := completionGateObservation(1, result, nil)
	if !strings.Contains(observation.ContentText(), "ZERO successful tool observations") {
		t.Fatalf("expected recorded-reality statement, got: %s", observation.ContentText())
	}
	successfulObservation := turnObservation{ObservationID: "obs-001", Action: "tool", Tool: "task_add"}
	observationAfterTool := completionGateObservation(2, result, []turnObservation{successfulObservation})
	if strings.Contains(observationAfterTool.ContentText(), "ZERO successful tool observations") {
		t.Fatalf("did not expect recorded-reality statement after a successful tool observation")
	}
}

func TestFinishHiddenAfterEvidenceMissingRejectionWithoutToolEvidence(t *testing.T) {
	rejection := completionGateObservation(1, completionGateResult{Message: "no evidence", EvidenceKind: "evidence_reference_invalid"}, nil)
	if !finishWasRejectedWithoutAnyToolEvidence([]turnObservation{rejection}) {
		t.Fatalf("expected finish hidden after gate rejection with zero tool evidence")
	}
	successfulTool := turnObservation{ObservationID: "obs-002", Action: "continue", Tool: "task_add"}
	if finishWasRejectedWithoutAnyToolEvidence([]turnObservation{successfulTool, rejection}) {
		t.Fatalf("expected finish exposed once a successful tool observation exists")
	}
	if finishWasRejectedWithoutAnyToolEvidence([]turnObservation{rejection, successfulTool}) {
		t.Fatalf("expected finish exposed when the latest observation is not a gate rejection")
	}
	if finishWasRejectedWithoutAnyToolEvidence(nil) {
		t.Fatalf("expected finish exposed with no observations")
	}
}

func TestFinishHiddenAfterAttachmentRejectionDespiteToolEvidence(t *testing.T) {
	successfulRead := turnObservation{ObservationID: "obs-001", Action: "continue", Tool: "file_read"}
	rejection := completionGateObservation(2, completionGateResult{Message: "attach the artifact", EvidenceKind: "attachment_missing"}, []turnObservation{successfulRead})
	if !finishWasRejectedWithoutAnyToolEvidence([]turnObservation{successfulRead, rejection}) {
		t.Fatalf("expected finish hidden after attachment rejection even with prior tool evidence")
	}
}
