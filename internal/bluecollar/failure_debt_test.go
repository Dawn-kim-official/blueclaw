package bluecollar

import (
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
)

import "testing"

func TestActiveFailureDebtKeepsDebtAfterInspectionToolWithoutRecoveryStep(t *testing.T) {
	_, hasFailureDebt := activeFailureDebt([]turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "site.serve",
			Failure:       &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.OperationFailed.String()},
			ToolInputKey:  "site.serve\x00{\"siteReference\":\"site-1\"}",
		},
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          "site.list",
			Output:        toolcontract.ToolOutput{Content: `{"siteID":"site-1","status":"failed","publishedURL":"https://portfolio.example"}`},
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
			Failure:       &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.NotFound.String()},
			ToolInputKey:  "file.read\x00{\"path\":\"home/sites/site-1/.internkim/artifact-brief.md\"}",
		},
	})

	if hasFailureDebt {
		t.Fatal("expected missing optional site control file not to create failure debt")
	}
}
