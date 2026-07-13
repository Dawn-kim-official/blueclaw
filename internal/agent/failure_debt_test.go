package agent

import (
	"encoding/json"
	"testing"
)

func TestActiveFailureDebtKeepsDebtAfterInspectionToolWithoutRecoveryStep(t *testing.T) {
	_, hasFailureDebt := activeFailureDebt([]turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "site.publish",
			Failure:       &ToolFailure{Code: FailureCodes.OperationFailed.String()},
			ToolInputKey:  "site.publish\x00{\"siteID\":\"site-1\"}",
		},
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          "site.status",
			Output:        ToolOutput{Content: `{"siteID":"site-1","status":"failed","publishedURL":"https://portfolio.example"}`},
		},
	})

	if !hasFailureDebt {
		t.Fatal("expected inspection status result to keep failure debt active")
	}
}

func TestActiveFailureDebtIgnoresMissingOptionalSiteControlFile(t *testing.T) {
	_, hasFailureDebt := activeFailureDebt([]turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "file.read",
			Failure:       &ToolFailure{Code: FailureCodes.NotFound.String()},
			ToolInputKey:  "file.read\x00{\"path\":\"home/sites/site-1/.internkim/artifact-brief.md\"}",
		},
	})

	if hasFailureDebt {
		t.Fatal("expected missing optional site control file not to create failure debt")
	}
}

func TestActiveFailureDebtKeepsDebtAfterUnrelatedSuccessfulTerminalCall(t *testing.T) {
	failureDebt, hasFailureDebt := activeFailureDebt([]turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "terminal.run",
			Failure:       &ToolFailure{Code: FailureCodes.OperationFailed.String()},
			ToolInputKey:  "terminal.run\x00{\"command\":\"make build\"}",
		},
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          "terminal.run",
			Output:        ToolOutput{Content: "/workspace"},
			ToolInputKey:  "terminal.run\x00{\"command\":\"pwd\"}",
			RecoveryStep:  recoveryStepCorrectedRetry,
		},
	})

	if !hasFailureDebt || failureDebt.LatestFailure.ObservationID != "obs-001" {
		t.Fatalf("unrelated pwd success must retain debt, got %+v", failureDebt)
	}
}

func TestPreviousFailedToolInputSurvivesUnrelatedSuccess(t *testing.T) {
	toolInput := json.RawMessage(`{"materialID":"missing"}`)
	failureKey := canonicalToolCallKey("file.read", toolInput)
	failure, isFound := latestFailedToolInput([]turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "file.read",
			Failure:       &ToolFailure{Code: FailureCodes.InvalidInput.String()},
			ToolInputKey:  failureKey,
		},
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          "file.write",
			Output:        ToolOutput{Content: `{"path":"home/documents/result.html"}`},
			ToolInputKey:  "file.write\x00{}",
		},
	}, "file.read", toolInput)

	if !isFound || failure.ObservationID != "obs-001" {
		t.Fatalf("expected exact failed fingerprint to remain protected, got %+v", failure)
	}
}

func TestLatestFailedToolInputStopsAtLaterSuccess(t *testing.T) {
	toolInput := json.RawMessage(`{"materialID":"material-1"}`)
	toolInputKey := canonicalToolCallKey("file.read", toolInput)
	failure, isFound := latestFailedToolInput([]turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "file.read",
			Failure:       &ToolFailure{Code: FailureCodes.InvalidInput.String()},
			ToolInputKey:  toolInputKey,
		},
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          "file.read",
			Output:        ToolOutput{Content: "read"},
			ToolInputKey:  toolInputKey,
		},
	}, "file.read", toolInput)

	if isFound {
		t.Fatalf("expected later exact success to supersede old failure, got %+v", failure)
	}
}

func TestClassifyRecoveryStepLeavesUnrelatedToolUnlinked(t *testing.T) {
	failureDebt := FailureDebt{LatestFailure: turnObservation{
		ObservationID: "obs-001",
		Tool:          "file.read",
		Failure:       &ToolFailure{Code: FailureCodes.InvalidInput.String()},
	}}

	if recoveryStep, isRecovery := classifyRecoveryStep(failureDebt, "file.write"); isRecovery || recoveryStep != "" {
		t.Fatalf("expected unrelated file.write not to recover file.read, got step=%q", recoveryStep)
	}
}

func TestBroadToolFamilyDoesNotInventAlternateRecoveryRoute(t *testing.T) {
	terminalFailure := FailureDebt{LatestFailure: turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "terminal.run",
		ToolInputKey:  "terminal.run\x00{}",
		Failure:       &ToolFailure{Code: FailureCodes.OperationFailed.String()},
	}}
	if recoveryStep, isRecovery := classifyRecoveryStep(terminalFailure, "terminal.session"); isRecovery {
		t.Fatalf("expected unrelated terminal tool not to resolve debt, got step %q", recoveryStep)
	}

	browserFailure := FailureDebt{LatestFailure: turnObservation{
		ObservationID: "obs-002",
		Action:        "continue",
		Tool:          "browser.navigate",
		ToolInputKey:  "browser.navigate\x00{}",
		Failure:       &ToolFailure{Code: FailureCodes.OperationFailed.String()},
	}}
	if recoveryStep, isRecovery := classifyRecoveryStep(browserFailure, "web.search"); isRecovery {
		t.Fatalf("expected unrelated web-family tool not to resolve debt, got step %q", recoveryStep)
	}
}

func TestClassifyRecoveryStepRequiresTypedSameToolRetry(t *testing.T) {
	failureDebt := FailureDebt{LatestFailure: turnObservation{
		ObservationID: "obs-001",
		Tool:          "terminal.run",
		Failure:       &ToolFailure{Code: FailureCodes.OperationFailed.String()},
	}}
	if recoveryStep, isRecovery := classifyRecoveryStep(failureDebt, "terminal.run"); isRecovery {
		t.Fatalf("expected untyped same-tool work to remain unrelated, got step=%q", recoveryStep)
	}
	failureDebt.LatestFailure.Failure.RetryPolicy = retryPolicyDifferentInput
	if recoveryStep, isRecovery := classifyRecoveryStep(failureDebt, "terminal.run"); !isRecovery || recoveryStep != recoveryStepCorrectedRetry {
		t.Fatalf("expected typed same-tool retry, got step=%q recovery=%v", recoveryStep, isRecovery)
	}
	failureDebt.LatestFailure.Failure.RetryPolicy = retryPolicyDoNotRetry
	if recoveryStep, isRecovery := classifyRecoveryStep(failureDebt, "terminal.run"); isRecovery {
		t.Fatalf("expected do-not-retry policy to reject same-tool recovery, got step=%q", recoveryStep)
	}
}

func TestClassifyRecoveryStepUsesTypedRecoveryHint(t *testing.T) {
	failureDebt := FailureDebt{LatestFailure: turnObservation{
		ObservationID: "obs-001",
		Tool:          "primary.lookup",
		Failure: &ToolFailure{
			Code:          FailureCodes.OperationFailed.String(),
			RecoveryHints: []RecoveryHint{{ToolNames: []string{"backup.lookup"}}},
		},
	}}

	recoveryStep, isRecovery := classifyRecoveryStep(failureDebt, "backup.lookup")
	if !isRecovery || recoveryStep != recoveryStepAdjacentTool {
		t.Fatalf("expected typed adjacent recovery, got step=%q recovery=%v", recoveryStep, isRecovery)
	}
}

func TestAdjacentRecoveryAvailabilityRequiresTypedHint(t *testing.T) {
	toolSet := newTestToolSet([]string{"file.read", "file.write"})
	failedObservation := newFailureObservation("obs-001", "continue", "file.read", "missing", FailureInvalidInput, FailureCodes.InvalidInput, "file_read")
	if adjacentRecoveryToolIsAvailable(toolSet, failedObservation) {
		t.Fatal("expected unrelated file.write not to count as recovery capacity")
	}
	failedObservation.Failure.RecoveryHints = []RecoveryHint{{ToolNames: []string{"file.write"}}}
	if !adjacentRecoveryToolIsAvailable(toolSet, failedObservation) {
		t.Fatal("expected typed file.write recovery hint to count as recovery capacity")
	}
}

func TestActiveFailureDebtClearsAfterStructurallyLinkedRecovery(t *testing.T) {
	_, hasFailureDebt := activeFailureDebt([]turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "schedule.create",
			Failure:       &ToolFailure{Code: FailureCodes.InvalidInput.String()},
			ToolInputKey:  "schedule.create\x00{\"cron\":\"0 9 * * *\"}",
		},
		{
			ObservationID:            "obs-002",
			Action:                   "continue",
			Tool:                     "schedule.create",
			Output:                   ToolOutput{Content: `{\"scheduleID\":\"schedule-1\"}`},
			ToolInputKey:             "schedule.create\x00{\"cron\":\"0 9 * * *\",\"repeatPolicy\":\"unbounded\"}",
			RecoveryStep:             recoveryStepCorrectedRetry,
			RecoveryForObservationID: "obs-001",
		},
	})

	if hasFailureDebt {
		t.Fatal("expected structurally linked successful retry to clear failure debt")
	}
}
