package agent

import "testing"

// Regression guard: an outcome-recovery request that asks to redo a prior
// artifact as a concrete file format (e.g. "발표자료니까 IR덱으로 다시 만들어",
// requestedOutputFormats=[pptx]) took the continuation branch of
// outcomeContractForRequest. That branch never derived the file.deliver
// completion evidence from the requested attachment suffix, so when the model
// named invalid evidence (capability.invoke) the contract had no valid
// completion path left and the task blocked with "required evidence must name
// a registered native tool or capability operation". The delivery evidence must
// be present whenever a file artifact is requested, in the recovery branch too.
func TestOutcomeRecoveryArtifactRequiresFileDelivery(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "발표자료니까 IR덱으로 제대로 다시 만들어",
			ToolSet: newTestToolSet(KernelToolNames()),
			ActiveGoal: ActiveGoal{
				OutcomeContract: OutcomeContract{
					ExpectedResults: []ExpectedResult{{ID: "ir_deck", Type: ExpectedResultTypeFile, Required: true}},
				},
			},
		},
		IntakeDecision{
			Classification:         IntakeClassificationBoundedTask,
			TaskShape:              TaskShapeMaintenanceTask,
			RequestedOutputFormats: []string{"pptx"},
			RequiredEvidenceTools:  []string{"file.read", "capability.invoke"},
			PriorTaskReference:     PriorTaskReferenceOutcomeRecovery,
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		[]string{".pptx"},
	)

	if !stringSliceContains(contract.RequiredEvidenceTools, FileDeliverToolName) {
		t.Fatalf("artifact recovery must require file.deliver as a valid completion path, got %+v", contract.RequiredEvidenceTools)
	}
}
