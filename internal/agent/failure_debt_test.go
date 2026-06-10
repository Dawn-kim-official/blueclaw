package agent

import "testing"

func TestActiveFailureDebtKeepsDebtAfterInspectionToolWithoutRecoveryStep(t *testing.T) {
	_, hasFailureDebt := activeFailureDebt([]turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "site.app.publish",
			Failure:       &ToolFailure{Code: FailureCodes.OperationFailed.String()},
			ToolInputKey:  "site.app.publish\x00{\"siteID\":\"site-1\"}",
		},
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          "site.app.status",
			Output:        ToolOutput{Content: `{"siteID":"site-1","status":"failed","publishedURL":"https://portfolio.example"}`},
		},
	})

	if !hasFailureDebt {
		t.Fatal("expected inspection status result to keep failure debt active")
	}
}
