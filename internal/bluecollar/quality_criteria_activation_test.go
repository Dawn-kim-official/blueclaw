package bluecollar

import (
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
)

import "testing"

func TestOutcomeContractNeedsQualityCriteriaOnlyForArtifacts(t *testing.T) {
	testCases := []struct {
		name     string
		contract OutcomeContract
		expected bool
	}{
		{name: "task CRUD", contract: OutcomeContract{RequiredEvidenceTools: []string{"task_add"}}},
		{name: "required artifact", contract: OutcomeContract{ArtifactRequirement: ArtifactRequirementRequired}, expected: true},
		{name: "file result", contract: OutcomeContract{ExpectedResults: []ExpectedResult{{Type: ExpectedResultTypeFile}}}, expected: true},
		{name: "link result", contract: OutcomeContract{ExpectedResults: []ExpectedResult{{Type: ExpectedResultTypeLink}}}, expected: true},
		{name: "attachment", contract: OutcomeContract{RequiredAttachmentSuffixes: []string{".docx"}}, expected: true},
		{name: "website", contract: OutcomeContract{RequiredEvidenceTools: []string{"site_serve"}}, expected: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
				{Name: "task_add", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectWorkspaceWrite},
				{Name: "site_serve", Namespace: "site", SideEffectClass: toolcontract.ToolSideEffectExternalPublish},
			})
			if actual := outcomeContractNeedsQualityCriteria(toolSet, testCase.contract); actual != testCase.expected {
				t.Fatalf("expected %t, got %t", testCase.expected, actual)
			}
		})
	}
}
