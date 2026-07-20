package agent

import "strings"

const requiredEvidenceInvalidEventName = "agent.required_evidence_invalid"
const requiredEvidenceReaskEventName = "agent.required_evidence_reask"

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

type requiredEvidenceReaskReport struct {
	WasAttempted       bool     `json:"wasAttempted"`
	DidRecoverEvidence bool     `json:"didRecoverEvidence"`
	WasDeterministic   bool     `json:"wasDeterministic,omitempty"`
	RecoveredEvidence  []string `json:"recoveredEvidence,omitempty"`
	Reason             string   `json:"reason,omitempty"`
}

func uniqueSideEffectEvidenceCandidate(toolSet *ToolSet, candidates []string, registeredFallback []string) (string, bool) {
	candidatePool := appendUniqueStrings(candidates)
	if len(candidatePool) == 0 {
		candidatePool = appendUniqueStrings(registeredFallback)
	}
	sideEffectCandidates := []string{}
	for _, candidate := range candidatePool {
		if !requiredEvidenceToolCanBeSatisfied(toolSet, candidate) {
			continue
		}
		if !requiredEvidenceIncludesSideEffect(toolSet, []string{candidate}) {
			continue
		}
		sideEffectCandidates = append(sideEffectCandidates, candidate)
	}
	if len(sideEffectCandidates) != 1 {
		return "", false
	}
	return sideEffectCandidates[0], true
}

func validateRequiredEvidenceTools(toolSet *ToolSet, toolNames []string) requiredEvidenceValidationReport {
	requiredEvidence := appendUniqueStrings(toolNames)
	report := requiredEvidenceValidationReport{
		RequiredEvidence: requiredEvidence,
		Reason:           "required evidence must name an exact available registered tool",
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
	if trimmedToolName == "" {
		return "", false
	}
	if toolSet == nil {
		return "", false
	}
	registeredToolName, isRegistered := requiredEvidenceRegisteredToolName(toolSet, trimmedToolName)
	if !isRegistered {
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
	trimmedToolName := strings.TrimSpace(toolName)
	return trimmedToolName, toolSet.IsRegistered(trimmedToolName)
}

func requiredEvidenceToolIsCapabilityOperation(toolSet *ToolSet, toolName string) bool {
	return !IsKernelToolName(toolName) && toolSet.CanExpose(toolName)
}

func missingRequiredEvidenceReport(intakeDecision IntakeDecision, outcomeContract OutcomeContract, toolSet *ToolSet) requiredEvidenceValidationReport {
	if !requiredEvidenceMissingForSideEffect(intakeDecision, outcomeContract, toolSet) {
		return requiredEvidenceValidationReport{}
	}
	return requiredEvidenceValidationReport{
		Reason: "side-effect task has no side-effect evidence",
	}
}

func requiredEvidenceMissingForSideEffect(intakeDecision IntakeDecision, outcomeContract OutcomeContract, toolSet *ToolSet) bool {
	if len(outcomeContract.RequiredEvidenceTools) > 0 && !intakeDecisionHasRequiredSideEffect(intakeDecision, toolSet) {
		return false
	}
	if !intakeDecisionRequiresSideEffectEvidence(intakeDecision, toolSet) {
		return false
	}
	return !requiredEvidenceIncludesSideEffect(toolSet, outcomeContract.RequiredEvidenceTools)
}

func intakeDecisionRequiresSideEffectEvidence(intakeDecision IntakeDecision, toolSet *ToolSet) bool {
	if intakeDecision.Classification != IntakeClassificationBoundedTask {
		return false
	}
	return intakeDecisionHasRequiredSideEffect(intakeDecision, toolSet) ||
		requiredEvidenceInitialToolsNeedEvidence(toolSet, intakeDecision.InitialToolNames)
}

func intakeDecisionHasRequiredSideEffect(intakeDecision IntakeDecision, toolSet *ToolSet) bool {
	return intakeDecision.TaskShape == TaskShapeMaintenanceTask ||
		intakeDecision.TaskShape == TaskShapeScheduledTask ||
		intakeDecision.TaskShape == TaskShapeApprovalGatedTask ||
		hasArtifactOutputFormat(intakeDecision.RequestedOutputFormats) ||
		intakeDecisionRequiresSiteEvidence(intakeDecision, toolSet)
}

func requiredEvidenceIncludesNamespace(toolSet *ToolSet, toolNames []string, namespace string) bool {
	for _, toolName := range toolNames {
		if toolIsInNamespace(toolSet, toolName, namespace) {
			return true
		}
	}
	return false
}

func requiredEvidenceIncludesSideEffect(toolSet *ToolSet, toolNames []string) bool {
	for _, toolName := range toolNames {
		if IsArtifactDeliveryTool(toolName) || requiredEvidenceToolNeedsSuccessfulSideEffect(toolSet, toolName) {
			return true
		}
	}
	return false
}

func requiredEvidenceInitialToolsNeedEvidence(toolSet *ToolSet, toolNames []string) bool {
	for _, toolName := range toolNames {
		if requiredEvidenceToolNeedsSuccessfulSideEffect(toolSet, toolName) {
			return true
		}
	}
	return false
}
