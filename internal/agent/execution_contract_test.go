package agent

import "testing"

func TestExecutionContractRequiresAttachmentForFileResult(t *testing.T) {
	outcomeContract := OutcomeContract{
		RequiredAttachmentSuffixes: []string{".pptx"},
		ExpectedResults: []ExpectedResult{{
			ID:          "attached-file",
			Type:        ExpectedResultTypeFile,
			Description: "PPTX deck",
			Required:    true,
		}},
		ArtifactRequirement: ArtifactRequirementRequired,
	}

	contract := executionContractForRequest(
		AgentRequest{},
		IntakeDecision{TaskShape: TaskShapeMaintenanceTask},
		InstructionBundle{},
		outcomeContract,
		ExecutionPlan{},
		false,
	)

	if !contract.FinishPolicy.RequiresAttachment {
		t.Fatalf("expected file result to require attachment, got %+v", contract)
	}
	if !containsString(contract.FinishPolicy.RequiredAttachmentSuffixes, ".pptx") {
		t.Fatalf("expected required suffix to be preserved, got %+v", contract.FinishPolicy.RequiredAttachmentSuffixes)
	}
	if contract.ActionPolicy.FinishExposure != FinishExposureWhenReady {
		t.Fatalf("expected file result to expose finish only when ready, got %+v", contract.ActionPolicy)
	}
}

func TestExecutionContractDoesNotRequireAttachmentForPublicLinkResult(t *testing.T) {
	outcomeContract := OutcomeContract{
		ExpectedResults: []ExpectedResult{{
			ID:          "public-link",
			Type:        ExpectedResultTypeLink,
			Description: "published URL",
			Required:    true,
		}},
		ArtifactRequirement: ArtifactRequirementNone,
	}

	contract := executionContractForRequest(
		AgentRequest{Prompt: "웹사이트를 만들어서 배포해줘"},
		IntakeDecision{TaskShape: TaskShapeMaintenanceTask},
		InstructionBundle{},
		outcomeContract,
		ExecutionPlan{PublicDeploy: true},
		true,
	)

	if contract.FinishPolicy.RequiresAttachment {
		t.Fatalf("expected public link result not to require attachment, got %+v", contract)
	}
	if contract.ActionPolicy.FinishExposure != FinishExposureAlways {
		t.Fatalf("expected link-only result to keep flexible finish exposure, got %+v", contract.ActionPolicy)
	}
}

func TestExecutionContractPreservesExternalSendEvidenceTool(t *testing.T) {
	outcomeContract := OutcomeContract{
		RequiredEvidenceTools: []string{"platform.message.send"},
		ExpectedResults: []ExpectedResult{{
			ID:              "sent-message",
			Type:            ExpectedResultTypeMessage,
			Description:     "message was sent",
			Required:        true,
			AcceptanceHints: []string{"platform.message.send"},
		}},
	}

	contract := executionContractForRequest(
		AgentRequest{Prompt: "샘플에게 DM 보내줘"},
		IntakeDecision{TaskShape: TaskShapeMaintenanceTask},
		InstructionBundle{},
		outcomeContract,
		ExecutionPlan{ExternalSend: true, ThirdPartyExternalSend: true},
		true,
	)

	if !containsString(contract.FinishPolicy.RequiredEvidenceTools, "platform.message.send") {
		t.Fatalf("expected send tool in finish policy, got %+v", contract.FinishPolicy.RequiredEvidenceTools)
	}
	if !containsString(contract.ToolPolicy.RequiredToolNames, "platform.message.send") {
		t.Fatalf("expected send tool in required tool policy, got %+v", contract.ToolPolicy.RequiredToolNames)
	}
}
