package agent

import "testing"

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
