package agent

import "testing"

func TestOutcomeContractNeedsQualityCriteriaOnlyForArtifacts(t *testing.T) {
	testCases := []struct {
		name     string
		contract OutcomeContract
		expected bool
	}{
		{name: "task CRUD", contract: OutcomeContract{RequiredEvidenceTools: []string{"task.add"}}},
		{name: "required artifact", contract: OutcomeContract{ArtifactRequirement: ArtifactRequirementRequired}, expected: true},
		{name: "file result", contract: OutcomeContract{ExpectedResults: []ExpectedResult{{Type: ExpectedResultTypeFile}}}, expected: true},
		{name: "link result", contract: OutcomeContract{ExpectedResults: []ExpectedResult{{Type: ExpectedResultTypeLink}}}, expected: true},
		{name: "attachment", contract: OutcomeContract{RequiredAttachmentSuffixes: []string{".docx"}}, expected: true},
		{name: "website", contract: OutcomeContract{RequiredEvidenceTools: []string{"site.publish"}}, expected: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := outcomeContractNeedsQualityCriteria(testCase.contract); actual != testCase.expected {
				t.Fatalf("expected %t, got %t", testCase.expected, actual)
			}
		})
	}
}
