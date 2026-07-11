package agent

import "testing"

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
