package agent

import "testing"

func TestRecoveryPacketDoesNotHardCodeToolAllowedList(t *testing.T) {
	observation := turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "site.app.publish",
		Output:        ToolOutput{Content: "site workspace must contain app/dist; build in Blueclaw before publishing"},
		Failure: &ToolFailure{
			Kind:            FailureExternalService,
			Code:            FailureCodes.OperationFailed.String(),
			Stage:           "site.app.publish",
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
