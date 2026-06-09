package agent

import (
	"encoding/json"
	"strings"
)

type ExecutionContract struct {
	Version        int            `json:"version"`
	TaskShape      TaskShape      `json:"taskShape,omitempty"`
	FinishPolicy   FinishPolicy   `json:"finishPolicy,omitempty"`
	ToolPolicy     ToolPolicy     `json:"toolPolicy,omitempty"`
	ActionPolicy   ActionPolicy   `json:"actionPolicy,omitempty"`
	EvidencePolicy EvidencePolicy `json:"evidencePolicy,omitempty"`
	Source         string         `json:"source,omitempty"`
}

type FinishPolicy struct {
	RequiresAttachment         bool             `json:"requiresAttachment,omitempty"`
	RequiredAttachmentSuffixes []string         `json:"requiredAttachmentSuffixes,omitempty"`
	RequiredEvidenceTools      []string         `json:"requiredEvidenceTools,omitempty"`
	RequiredExpectedResults    []ExpectedResult `json:"requiredExpectedResults,omitempty"`
}

type ToolPolicy struct {
	RequiredToolNames []string `json:"requiredToolNames,omitempty"`
	HintToolNames     []string `json:"hintToolNames,omitempty"`
	RecoveryToolNames []string `json:"recoveryToolNames,omitempty"`
	MaxCallableTools  int      `json:"maxCallableTools,omitempty"`
}

type ActionPolicy struct {
	CanSelectTools        bool                 `json:"canSelectTools"`
	CanSetQualityCriteria bool                 `json:"canSetQualityCriteria"`
	CanFail               bool                 `json:"canFail"`
	FinishExposure        FinishExposurePolicy `json:"finishExposure,omitempty"`
}

type FinishExposurePolicy string

const (
	FinishExposureAlways    FinishExposurePolicy = "always"
	FinishExposureWhenReady FinishExposurePolicy = "when_ready"
)

type EvidencePolicy struct {
	RequiredEvidence []ExecutionEvidence `json:"requiredEvidence,omitempty"`
}

type ExecutionEvidence struct {
	Kind        string   `json:"kind"`
	AnyOfTools  []string `json:"anyOfTools,omitempty"`
	Description string   `json:"description,omitempty"`
}

type ExecutionReadiness struct {
	CanFinish bool     `json:"canFinish"`
	Reasons   []string `json:"reasons,omitempty"`
}

func executionContractForRequest(request AgentRequest, intakeDecision IntakeDecision, instructionBundle InstructionBundle, outcomeContract OutcomeContract, executionPlan ExecutionPlan, hasExecutionPlan bool) ExecutionContract {
	_ = request
	_ = executionPlan
	_ = hasExecutionPlan
	contract := ExecutionContract{
		Version:      1,
		TaskShape:    intakeDecision.TaskShape,
		FinishPolicy: finishPolicyFromOutcomeContract(outcomeContract),
		ToolPolicy:   toolPolicyFromOutcomeContract(instructionBundle, outcomeContract),
		ActionPolicy: ActionPolicy{
			CanSelectTools:        true,
			CanSetQualityCriteria: true,
			CanFail:               true,
			FinishExposure:        finishExposurePolicyForOutcomeContract(outcomeContract),
		},
		EvidencePolicy: evidencePolicyFromOutcomeContract(outcomeContract),
		Source:         "outcome_contract_projection",
	}
	return normalizeExecutionContract(contract)
}

func executionContractForTurnRequest(request AgentTurnRequest) ExecutionContract {
	if activeExecutionContract(request.ExecutionContract) {
		return normalizeExecutionContract(request.ExecutionContract)
	}
	if !activeGoalOutcomeContractHasRequirements(request.OutcomeContract) {
		return normalizeExecutionContract(request.ExecutionContract)
	}
	instructionBundle := InstructionBundle{
		Skills:         append([]SkillInstruction{}, request.AvailableSkills...),
		SkillDecisions: append([]SkillSelectionDecision{}, request.SkillDecisions...),
	}
	contract := ExecutionContract{
		Version:        1,
		TaskShape:      TaskShapeMaintenanceTask,
		FinishPolicy:   finishPolicyFromOutcomeContract(request.OutcomeContract),
		ToolPolicy:     toolPolicyFromOutcomeContract(instructionBundle, request.OutcomeContract),
		ActionPolicy:   ActionPolicy{CanSelectTools: true, CanSetQualityCriteria: true, CanFail: true, FinishExposure: finishExposurePolicyForOutcomeContract(request.OutcomeContract)},
		EvidencePolicy: evidencePolicyFromOutcomeContract(request.OutcomeContract),
		Source:         "turn_outcome_contract_projection",
	}
	return normalizeExecutionContract(contract)
}

func finishPolicyFromOutcomeContract(contract OutcomeContract) FinishPolicy {
	return FinishPolicy{
		RequiresAttachment:         expectedResultRequiresFileAttachment(contract),
		RequiredAttachmentSuffixes: appendUniqueStrings(contract.RequiredAttachmentSuffixes),
		RequiredEvidenceTools:      appendUniqueStrings(contract.RequiredEvidenceTools),
		RequiredExpectedResults:    requiredExpectedResults(contract.ExpectedResults),
	}
}

func toolPolicyFromOutcomeContract(instructionBundle InstructionBundle, contract OutcomeContract) ToolPolicy {
	return ToolPolicy{
		RequiredToolNames: appendUniqueStrings(outcomeContractRequiredToolNames(contract)),
		HintToolNames:     appendUniqueStrings(outcomeContractToolNames(contract), selectedEvidenceHintTools(instructionBundle)...),
		MaxCallableTools:  maxSchemaCallableToolCount,
	}
}

func evidencePolicyFromOutcomeContract(contract OutcomeContract) EvidencePolicy {
	requiredEvidence := []ExecutionEvidence{}
	if len(contract.RequiredEvidenceTools) > 0 {
		requiredEvidence = append(requiredEvidence, ExecutionEvidence{
			Kind:        "tool_success",
			AnyOfTools:  appendUniqueStrings(contract.RequiredEvidenceTools),
			Description: "required evidence tools must succeed before delivery",
		})
	}
	for _, toolNames := range contract.RequiredEvidenceAnyOf {
		normalizedToolNames := appendUniqueStrings(toolNames)
		if len(normalizedToolNames) == 0 {
			continue
		}
		requiredEvidence = append(requiredEvidence, ExecutionEvidence{
			Kind:        "tool_success_any_of",
			AnyOfTools:  normalizedToolNames,
			Description: "at least one alternative evidence tool must succeed before delivery",
		})
	}
	if expectedResultRequiresFileAttachment(contract) {
		requiredEvidence = append(requiredEvidence, ExecutionEvidence{
			Kind:        "attachment",
			AnyOfTools:  []string{"file.attach"},
			Description: "required file result must be attached before delivery",
		})
	}
	return EvidencePolicy{RequiredEvidence: requiredEvidence}
}

func finishExposurePolicyForOutcomeContract(contract OutcomeContract) FinishExposurePolicy {
	if expectedResultRequiresFileAttachment(contract) || len(contract.RequiredEvidenceTools) > 0 || len(contract.RequiredEvidenceAnyOf) > 0 {
		return FinishExposureWhenReady
	}
	return FinishExposureAlways
}

func requiredExpectedResults(results []ExpectedResult) []ExpectedResult {
	requiredResults := []ExpectedResult{}
	for _, result := range normalizeExpectedResults(results) {
		if result.Required {
			requiredResults = append(requiredResults, result)
		}
	}
	return requiredResults
}

func normalizeExecutionContract(contract ExecutionContract) ExecutionContract {
	hasActionPolicy := contract.ActionPolicy.CanSelectTools ||
		contract.ActionPolicy.CanSetQualityCriteria ||
		contract.ActionPolicy.CanFail ||
		strings.TrimSpace(string(contract.ActionPolicy.FinishExposure)) != ""
	if contract.Version == 0 {
		contract.Version = 1
	}
	contract.FinishPolicy.RequiredAttachmentSuffixes = appendUniqueStrings(contract.FinishPolicy.RequiredAttachmentSuffixes)
	contract.FinishPolicy.RequiredEvidenceTools = appendUniqueStrings(contract.FinishPolicy.RequiredEvidenceTools)
	contract.FinishPolicy.RequiredExpectedResults = requiredExpectedResults(contract.FinishPolicy.RequiredExpectedResults)
	contract.ToolPolicy.RequiredToolNames = appendUniqueStrings(contract.ToolPolicy.RequiredToolNames)
	contract.ToolPolicy.HintToolNames = appendUniqueStrings(contract.ToolPolicy.HintToolNames)
	contract.ToolPolicy.RecoveryToolNames = appendUniqueStrings(contract.ToolPolicy.RecoveryToolNames)
	if contract.ToolPolicy.MaxCallableTools <= 0 {
		contract.ToolPolicy.MaxCallableTools = maxSchemaCallableToolCount
	}
	if !hasActionPolicy {
		contract.ActionPolicy.CanSelectTools = true
		contract.ActionPolicy.CanSetQualityCriteria = true
		contract.ActionPolicy.CanFail = true
	}
	if strings.TrimSpace(string(contract.ActionPolicy.FinishExposure)) == "" {
		contract.ActionPolicy.FinishExposure = FinishExposureAlways
	}
	contract.Source = strings.TrimSpace(contract.Source)
	return contract
}

func evaluateExecutionReadiness(contract ExecutionContract, state agentTaskState) ExecutionReadiness {
	contract = normalizeExecutionContract(contract)
	reasons := []string{}
	if contract.ActionPolicy.FinishExposure != FinishExposureWhenReady {
		return ExecutionReadiness{CanFinish: true}
	}
	if contract.FinishPolicy.RequiresAttachment && len(state.Attachments) == 0 {
		reasons = append(reasons, "missing required attachment")
	}
	if missingSuffix := missingRequiredAttachmentSuffix(state.Attachments, contract.FinishPolicy.RequiredAttachmentSuffixes); missingSuffix != "" {
		reasons = append(reasons, "missing required attachment suffix "+missingSuffix)
	}
	if missingRequiredEvidenceTools(contract.FinishPolicy.RequiredEvidenceTools, state.Observations) {
		reasons = append(reasons, "missing required evidence tool")
	}
	if missingRequiredEvidenceAnyOf(contract.EvidencePolicy.RequiredEvidence, state.Observations) {
		reasons = append(reasons, "missing required alternative evidence tool")
	}
	return ExecutionReadiness{CanFinish: len(reasons) == 0, Reasons: reasons}
}

func actionPolicyForState(contract ExecutionContract, readiness ExecutionReadiness, state agentTaskState) ActionPolicy {
	policy := normalizeExecutionContract(contract).ActionPolicy
	policy.CanSetQualityCriteria = policy.CanSetQualityCriteria && len(state.QualityCriteria) == 0
	policy.CanFail = policy.CanFail && shouldExposeFailAction(state)
	if policy.FinishExposure == FinishExposureWhenReady && !readiness.CanFinish {
		return policy
	}
	policy.FinishExposure = FinishExposureAlways
	return policy
}

func canExposeFinish(contract ExecutionContract, state agentTaskState) bool {
	readiness := evaluateExecutionReadiness(contract, state)
	return readiness.CanFinish
}

func missingRequiredEvidenceTools(toolNames []string, observations []turnObservation) bool {
	for _, toolName := range appendUniqueStrings(toolNames) {
		if !hasSuccessfulToolObservationForTurn(observations, toolName) {
			return true
		}
	}
	return false
}

func missingRequiredEvidenceAnyOf(evidenceItems []ExecutionEvidence, observations []turnObservation) bool {
	for _, evidence := range evidenceItems {
		if evidence.Kind != "tool_success_any_of" {
			continue
		}
		if !hasAnySuccessfulToolObservationForTurn(observations, evidence.AnyOfTools) {
			return true
		}
	}
	return false
}

func hasAnySuccessfulToolObservationForTurn(observations []turnObservation, toolNames []string) bool {
	for _, toolName := range appendUniqueStrings(toolNames) {
		if hasSuccessfulToolObservationForTurn(observations, toolName) {
			return true
		}
	}
	return len(appendUniqueStrings(toolNames)) == 0
}

func activeExecutionContract(contract ExecutionContract) bool {
	return strings.TrimSpace(contract.Source) != "" ||
		contract.FinishPolicy.RequiresAttachment ||
		len(contract.FinishPolicy.RequiredAttachmentSuffixes) > 0 ||
		len(contract.FinishPolicy.RequiredEvidenceTools) > 0 ||
		len(contract.FinishPolicy.RequiredExpectedResults) > 0 ||
		len(contract.ToolPolicy.RequiredToolNames) > 0 ||
		len(contract.ToolPolicy.HintToolNames) > 0
}

func compactExecutionContractDescription(contract ExecutionContract) string {
	if !activeExecutionContract(contract) {
		return ""
	}
	contract = normalizeExecutionContract(contract)
	document, errorValue := json.Marshal(contract)
	if errorValue != nil {
		return ""
	}
	return "Execution contract:\n" + string(document)
}
