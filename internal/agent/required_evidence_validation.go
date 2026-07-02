package agent

import "strings"

const requiredEvidenceInvalidEventName = "agent.required_evidence_invalid"

const (
	requiredEvidenceToolKindCapabilityOperation = "capability_operation"
	requiredEvidenceToolKindNativeTool          = "native_tool"
)

type requiredEvidenceValidationReport struct {
	RequiredEvidence []string          `json:"requiredEvidence,omitempty"`
	InvalidEvidence  []string          `json:"invalidEvidence,omitempty"`
	EvidenceKinds    map[string]string `json:"evidenceKinds,omitempty"`
	Reason           string            `json:"reason,omitempty"`
}

func validateRequiredEvidenceTools(toolSet *ToolSet, toolNames []string) requiredEvidenceValidationReport {
	requiredEvidence := appendUniqueStrings(toolNames)
	report := requiredEvidenceValidationReport{
		RequiredEvidence: requiredEvidence,
		Reason:           "required evidence must name a registered native tool or capability operation",
	}
	for _, toolName := range requiredEvidence {
		toolKind, isValid := requiredEvidenceToolKind(toolSet, toolName)
		if isValid {
			report.EvidenceKinds = addRequiredEvidenceKind(report.EvidenceKinds, toolName, toolKind)
			continue
		}
		report.InvalidEvidence = appendUniqueStrings(report.InvalidEvidence, toolName)
	}
	if len(report.InvalidEvidence) == 0 {
		report.Reason = ""
	}
	return report
}

func (report requiredEvidenceValidationReport) HasInvalidEvidence() bool {
	return len(report.InvalidEvidence) > 0
}

func addRequiredEvidenceKind(evidenceKinds map[string]string, toolName string, toolKind string) map[string]string {
	if evidenceKinds == nil {
		evidenceKinds = map[string]string{}
	}
	evidenceKinds[strings.TrimSpace(toolName)] = toolKind
	return evidenceKinds
}

func requiredEvidenceToolCanBeSatisfied(toolSet *ToolSet, toolName string) bool {
	_, isValid := requiredEvidenceToolKind(toolSet, toolName)
	return isValid
}

func requiredEvidenceToolKind(toolSet *ToolSet, toolName string) (string, bool) {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" || trimmedToolName == CapabilityInvokeToolName {
		return "", false
	}
	if toolSet == nil {
		return "", false
	}
	registeredToolName, isRegistered := requiredEvidenceRegisteredToolName(toolSet, trimmedToolName)
	if !isRegistered || registeredToolName == CapabilityInvokeToolName {
		return "", false
	}
	if toolSet.IsAllowed(registeredToolName) {
		return requiredEvidenceToolKindNativeTool, true
	}
	if requiredEvidenceToolIsCapabilityOperation(toolSet, registeredToolName) {
		return requiredEvidenceToolKindCapabilityOperation, true
	}
	return "", false
}

func requiredEvidenceRegisteredToolName(toolSet *ToolSet, toolName string) (string, bool) {
	if toolSet.IsRegistered(toolName) {
		return toolName, true
	}
	for _, registeredToolName := range toolSet.ListRegisteredToolNames() {
		if ToolNamesMatch(registeredToolName, toolName) {
			return registeredToolName, true
		}
	}
	return "", false
}

func requiredEvidenceToolIsCapabilityOperation(toolSet *ToolSet, toolName string) bool {
	return strings.Contains(strings.TrimSpace(toolName), ".") &&
		!IsKernelToolName(toolName) &&
		toolSet.IsAllowed(CapabilityInvokeToolName)
}

func missingRequiredEvidenceReport(intakeDecision IntakeDecision, outcomeContract OutcomeContract, toolSet *ToolSet) requiredEvidenceValidationReport {
	if !requiredEvidenceMissingForSideEffect(intakeDecision, outcomeContract, toolSet) {
		return requiredEvidenceValidationReport{}
	}
	return requiredEvidenceValidationReport{
		Reason: "side-effect task has no required evidence",
	}
}

func requiredEvidenceMissingForSideEffect(intakeDecision IntakeDecision, outcomeContract OutcomeContract, toolSet *ToolSet) bool {
	if len(outcomeContract.RequiredEvidenceTools) > 0 {
		return false
	}
	if intakeDecision.Classification != IntakeClassificationBoundedTask {
		return false
	}
	return intakeDecision.TaskShape == TaskShapeScheduledTask ||
		normalizeOutputKind(intakeDecision.OutputKind) != OutputKindNone ||
		requiredEvidenceInitialToolsNeedEvidence(toolSet, intakeDecision.InitialToolNames)
}

func requiredEvidenceInitialToolsNeedEvidence(toolSet *ToolSet, toolNames []string) bool {
	for _, toolName := range toolNames {
		if requiredEvidenceToolNeedsSuccessfulSideEffect(toolSet, toolName) {
			return true
		}
	}
	return false
}
