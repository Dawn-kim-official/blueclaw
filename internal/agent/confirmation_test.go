package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConfirmationPolicyAllowsLowRiskDailyReport(t *testing.T) {
	decision := EvaluateConfirmationPolicy(ExecutionPlan{
		Summary:  "매일 오전 9시에 현재 대화로 보고합니다.",
		Schedule: "daily",
		Cadence:  "daily",
		Repeated: true,
	})
	if decision.RequiresConfirmation || decision.RequiresClarification {
		t.Fatalf("expected low-risk daily report to proceed, got %+v", decision)
	}
}

func TestConfirmationPolicyClarifiesUnboundedHighFrequencyExternalSend(t *testing.T) {
	decision := EvaluateConfirmationPolicy(ExecutionPlan{
		Summary:                "동하에게 1분마다 메시지를 보냅니다.",
		Targets:                []string{"동하"},
		ExternalSend:           true,
		ThirdPartyExternalSend: true,
		Repeated:               true,
		HighFrequency:          true,
	})
	if !decision.RequiresClarification || decision.Reason != "repeated_external_send_needs_end" {
		t.Fatalf("expected missing finite bound clarification, got %+v", decision)
	}
}

func TestConfirmationPolicyConfirmsBoundedExternalSend(t *testing.T) {
	decision := EvaluateConfirmationPolicy(ExecutionPlan{
		Summary:                "동하에게 1분마다 18:30까지 메시지를 보냅니다.",
		Targets:                []string{"동하"},
		EndAt:                  "2026-05-12T18:30:00+09:00",
		ExternalSend:           true,
		ThirdPartyExternalSend: true,
		Repeated:               true,
		HighFrequency:          true,
	})
	if !decision.RequiresConfirmation || decision.RequiresClarification {
		t.Fatalf("expected bounded external repeat confirmation, got %+v", decision)
	}
}

func TestConfirmationPlanMessagesIncludeTemporalContextAndScheduleGuard(t *testing.T) {
	messages := confirmationPlanMessages(AgentRequest{
		Prompt:        "김인턴 구조 소개 웹사이트 만들어서 배포해줘",
		TurnStartedAt: time.Date(2026, time.May, 17, 1, 2, 3, 0, time.UTC),
	}, []string{"site.publish"})

	body := joinMessageContent(messages)
	if !strings.Contains(body, "Current date: 2026-05-17") || !strings.Contains(body, "Current weekday: Sunday") {
		t.Fatalf("expected confirmation plan temporal context, got %s", body)
	}
	if !strings.Contains(body, "Do not invent schedule, startAt, endAt, or cadence") {
		t.Fatalf("expected schedule hallucination guard, got %s", body)
	}
}

func TestSitePrototypePublishDoesNotBuildConfirmationPlan(t *testing.T) {
	toolSet := newTestToolSet([]string{"site.create", "site.publish", "terminal.run"})
	request := AgentRequest{
		Prompt:  "김인턴 구조 소개 웹사이트 만들어서 배포해줘",
		ToolSet: toolSet,
	}
	decision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskShape:      TaskShapeMaintenanceTask,
	}

	if shouldBuildExecutionPlanForConfirmation(request, decision, []string{"site.create", "site.publish"}) {
		t.Fatal("site prototype publish is part of the normal create workflow and must not request approval")
	}
}

func TestSitePrototypeContinuationDoesNotBuildConfirmationPlan(t *testing.T) {
	toolSet := newTestToolSet([]string{"site.create", "site.publish", "terminal.run"})
	request := AgentRequest{
		Prompt:  "다시 해봐 그럼 될 거야",
		ToolSet: toolSet,
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"site.create", "terminal.run", "site.publish"},
		}},
	}
	decision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskShape:      TaskShapeMaintenanceTask,
	}

	if shouldBuildExecutionPlanForConfirmation(request, decision, []string{"site.create", "site.publish"}) {
		t.Fatal("site prototype continuation must not request approval")
	}
}

func TestDestructiveSiteManagementStillBuildsConfirmationPlan(t *testing.T) {
	toolSet := newTestToolSet([]string{"site.create", "site.publish", "terminal.run"})
	request := AgentRequest{
		Prompt:    "이 사이트 내려줘",
		ToolSet:   toolSet,
		WorkKinds: []string{WorkKindDestructiveAction},
	}
	decision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskShape:      TaskShapeMaintenanceTask,
	}

	if !shouldBuildExecutionPlanForConfirmation(request, decision, []string{"site.publish"}) {
		t.Fatal("destructive site management should still request confirmation")
	}
}

func TestConfirmationMessageIncludesTemporalContextAndAvoidsInventedTiming(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"reply":"김인턴 구조 소개 웹사이트를 제작해 인터넷에 배포합니다. 승인하면 진행하겠습니다."}`,
	}}
	kernel := NewAgentKernel(nil, nil)
	kernel.UseLanguageModelProvider(languageModel)

	_, errorValue := kernel.GenerateConfirmationMessage(context.Background(), AgentRequest{
		Prompt:           "김인턴 구조 소개 웹사이트 만들어서 배포해줘",
		ResponseLanguage: "ko",
		TurnStartedAt:    time.Date(2026, time.May, 17, 1, 2, 3, 0, time.UTC),
	}, ExecutionPlan{
		OriginalInstruction: "김인턴 구조 소개 웹사이트 만들어서 배포해줘",
		Summary:             "김인턴 구조 소개 웹사이트를 제작해 배포합니다.",
		PublicDeploy:        true,
	}, ConfirmationPolicyDecision{RequiresConfirmation: true, Reason: "risky_side_effect"})
	if errorValue != nil {
		t.Fatalf("expected confirmation message: %v", errorValue)
	}

	if len(languageModel.requests) != 1 {
		t.Fatalf("expected one confirmation message request, got %d", len(languageModel.requests))
	}
	body := joinMessageContent(languageModel.requests[0].Messages)
	if !strings.Contains(body, "Current date: 2026-05-17") || !strings.Contains(body, "Current weekday: Sunday") {
		t.Fatalf("expected confirmation message temporal context, got %s", body)
	}
	if !strings.Contains(body, "Mention repeat, start, or end conditions only when they are present") {
		t.Fatalf("expected confirmation timing guard, got %s", body)
	}
}
