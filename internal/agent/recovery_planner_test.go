package agent

import (
	"strings"
	"testing"
)

func TestRecoveryPacketDoesNotHardCodeToolAllowedList(t *testing.T) {
	observation := turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "site.serve",
		Output:        ToolOutput{Content: "site workspace must contain app/dist; build in Blueclaw before publishing"},
		Failure: &ToolFailure{
			Kind:            FailureExternalService,
			Code:            FailureCodes.OperationFailed.String(),
			Stage:           "site.serve",
			UserSafeSummary: "site workspace must contain app/dist; build in Blueclaw before publishing",
		},
	}

	packet := buildRecoveryPacket(observation)

	if len(packet.AllowedTools) != 0 {
		t.Fatalf("expected recovery packet not to hard-code tool choices, got %+v", packet.AllowedTools)
	}
	if packet.WhatFailed == "" || packet.WhyLikely == "" || len(packet.MustDoNext) == 0 {
		t.Fatalf("expected factual recovery context, got %+v", packet)
	}
}

func TestRecoveryPacketSchemaFailureRetriesSameToolWithFixedInput(t *testing.T) {
	observation := turnObservation{
		ObservationID: "obs-002",
		Action:        "continue",
		Tool:          "ask.confirm",
		ToolInputKey:  "ask.confirm\x00{}",
		Failure: &ToolFailure{
			Kind:            FailureInvalidInput,
			Code:            FailureCodes.InvalidInput.String(),
			Stage:           "ask_confirm",
			UserSafeSummary: "ask.confirm requires userFacingMessage",
		},
	}

	packet := buildRecoveryPacket(observation)

	if packet.RetryPolicy == retryPolicyDoNotRetry {
		t.Fatalf("expected a missing-field schema failure to be retryable with corrected input, got %q", packet.RetryPolicy)
	}
	joined := strings.Join(packet.MustDoNext, " ")
	if !strings.Contains(joined, "same tool") {
		t.Fatalf("expected guidance to retry the same tool with corrected input, got %+v", packet.MustDoNext)
	}
}

func TestRecoveryPacketKeepsTypedHintTools(t *testing.T) {
	failedObservation := turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "site.serve",
		ToolInputKey:  "site.serve\x00{\"siteID\":\"site-1\"}",
		Failure: &ToolFailure{
			RecoveryHints: []RecoveryHint{{ToolNames: []string{"file.edit"}}},
		},
	}

	packet := buildRecoveryPacket(failedObservation)
	if len(packet.AllowedTools) != 1 || packet.AllowedTools[0] != "file.edit" {
		t.Fatalf("expected typed recovery hint tools to remain available, got %+v", packet.AllowedTools)
	}
}
