package agent

import (
	"encoding/json"
	"strings"
)

type ActiveGoalStatus string

const (
	ActiveGoalStatusActive           ActiveGoalStatus = "active"
	ActiveGoalStatusWaitingUserInput ActiveGoalStatus = "waiting_user_input"
	ActiveGoalStatusWaitingApproval  ActiveGoalStatus = "waiting_approval"
	ActiveGoalStatusCompleted        ActiveGoalStatus = "completed"
	ActiveGoalStatusBlocked          ActiveGoalStatus = "blocked"
)

type ActiveGoal struct {
	GoalID              string           `json:"goalID,omitempty"`
	TaskRunID           string           `json:"taskRunID,omitempty"`
	OriginalInstruction string           `json:"originalInstruction,omitempty"`
	CurrentObjective    string           `json:"currentObjective,omitempty"`
	KnownContext        []string         `json:"knownContext,omitempty"`
	MissingInformation  []string         `json:"missingInformation,omitempty"`
	OutcomeContract     OutcomeContract  `json:"outcomeContract,omitempty"`
	Status              ActiveGoalStatus `json:"status,omitempty"`
}

type OutcomeContract struct {
	RequiredEvidenceTools      []string   `json:"requiredEvidenceTools,omitempty"`
	RequiredEvidenceAnyOf      [][]string `json:"requiredEvidenceAnyOf,omitempty"`
	RequiredAttachmentSuffixes []string   `json:"requiredAttachmentSuffixes,omitempty"`
	ArtifactRequirement        string     `json:"artifactRequirement,omitempty"`
	SelectedEvidenceHints      []string   `json:"selectedEvidenceHints,omitempty"`
	Source                     string     `json:"source,omitempty"`
}

const (
	ArtifactRequirementNone      = "none"
	ArtifactRequirementPreferred = "preferred"
	ArtifactRequirementRequired  = "required"
)

func activeGoalDescription(activeGoal ActiveGoal) string {
	if strings.TrimSpace(activeGoal.GoalID) == "" &&
		strings.TrimSpace(activeGoal.TaskRunID) == "" &&
		strings.TrimSpace(activeGoal.OriginalInstruction) == "" &&
		strings.TrimSpace(activeGoal.CurrentObjective) == "" {
		return ""
	}
	document, errorValue := json.Marshal(activeGoal)
	if errorValue != nil {
		return ""
	}
	return "Active conversation goal:\n" + string(document) + "\nTreat the current user message as input to this goal unless it clearly starts a new unrelated request. Preserve the current user message as the latest user input; do not rewrite it."
}

func normalizeOutcomeContract(contract OutcomeContract) OutcomeContract {
	contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools)
	contract.RequiredAttachmentSuffixes = appendUniqueStrings(contract.RequiredAttachmentSuffixes)
	contract.SelectedEvidenceHints = appendUniqueStrings(contract.SelectedEvidenceHints)
	contract.RequiredEvidenceAnyOf = normalizeEvidenceAnyOf(contract.RequiredEvidenceAnyOf)
	contract.ArtifactRequirement = normalizeArtifactRequirement(contract.ArtifactRequirement)
	contract.Source = strings.TrimSpace(contract.Source)
	return contract
}

func normalizeArtifactRequirement(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ArtifactRequirementRequired:
		return ArtifactRequirementRequired
	case ArtifactRequirementPreferred:
		return ArtifactRequirementPreferred
	case ArtifactRequirementNone, "":
		return ArtifactRequirementNone
	default:
		return ArtifactRequirementNone
	}
}

func normalizeEvidenceAnyOf(values [][]string) [][]string {
	result := [][]string{}
	seenGroup := map[string]bool{}
	for _, group := range values {
		normalizedGroup := appendUniqueStrings(group)
		if len(normalizedGroup) == 0 {
			continue
		}
		key := strings.Join(normalizedGroup, "\x00")
		if seenGroup[key] {
			continue
		}
		seenGroup[key] = true
		result = append(result, normalizedGroup)
	}
	return result
}
