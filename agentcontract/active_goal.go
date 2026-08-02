package agentcontract

import (
	"strconv"
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

const (
	ArtifactRequirementNone      = "none"
	ArtifactRequirementPreferred = "preferred"
	ArtifactRequirementRequired  = "required"
)

const (
	ExpectedResultTypeMessage = "message"
	ExpectedResultTypeFile    = "file"
	ExpectedResultTypeLink    = "link"
)

type ActiveGoal struct {
	GoalID              string           `json:"goalID,omitempty"`
	TaskRunID           string           `json:"taskRunID,omitempty"`
	OriginalInstruction string           `json:"originalInstruction,omitempty"`
	CurrentObjective    string           `json:"currentObjective,omitempty"`
	KnownContext        []string         `json:"knownContext,omitempty"`
	MissingInformation  []string         `json:"missingInformation,omitempty"`
	RequiredNextTools   []string         `json:"requiredNextTools,omitempty"`
	SelectedToolNames   []string         `json:"selectedToolNames,omitempty"`
	SelectedSkillNames  []string         `json:"selectedSkillNames,omitempty"`
	OutcomeContract     OutcomeContract  `json:"outcomeContract,omitempty"`
	Status              ActiveGoalStatus `json:"status,omitempty"`
	RestoreError        string           `json:"-"`
}

type OutcomeContract struct {
	RequiredEvidenceTools      []string         `json:"requiredEvidenceTools,omitempty"`
	RequiredEvidenceAnyOf      [][]string       `json:"requiredEvidenceAnyOf,omitempty"`
	RequiredAttachmentSuffixes []string         `json:"requiredAttachmentSuffixes,omitempty"`
	RequiredEffects            []OutcomeEffect  `json:"requiredEffects,omitempty"`
	ExpectedResults            []ExpectedResult `json:"expectedResults,omitempty"`
	ArtifactRequirement        string           `json:"artifactRequirement,omitempty"`
	SelectedEvidenceHints      []string         `json:"selectedEvidenceHints,omitempty"`
	Source                     string           `json:"source,omitempty"`
}

type OutcomeEffect struct {
	ObjectType         string   `json:"objectType"`
	Effect             string   `json:"effect"`
	Description        string   `json:"description,omitempty"`
	SuggestedNextTools []string `json:"suggestedNextTools,omitempty"`
}

type ExpectedResult struct {
	ID              string   `json:"id,omitempty"`
	Type            string   `json:"type,omitempty"`
	Description     string   `json:"description,omitempty"`
	Required        bool     `json:"required"`
	AcceptanceHints []string `json:"acceptanceHints,omitempty"`
}

type PriorTaskContext struct {
	TaskRunID              string          `json:"taskRunID,omitempty"`
	Status                 string          `json:"status,omitempty"`
	Prompt                 string          `json:"prompt,omitempty"`
	Result                 string          `json:"result,omitempty"`
	FailureReason          string          `json:"failureReason,omitempty"`
	OutcomeContract        OutcomeContract `json:"outcomeContract,omitempty"`
	RequestedOutputFormats []string        `json:"requestedOutputFormats,omitempty"`
}

func OutcomeContractHasRequirements(contract OutcomeContract) bool {
	artifactRequirement := strings.TrimSpace(contract.ArtifactRequirement)
	return len(contract.ExpectedResults) > 0 ||
		len(contract.RequiredEvidenceTools) > 0 ||
		len(contract.RequiredEvidenceAnyOf) > 0 ||
		len(contract.RequiredAttachmentSuffixes) > 0 ||
		len(contract.RequiredEffects) > 0 ||
		(artifactRequirement != "" && artifactRequirement != ArtifactRequirementNone)
}

func NormalizeExpectedResults(results []ExpectedResult) []ExpectedResult {
	normalizedResults := []ExpectedResult{}
	seenResults := map[string]bool{}
	for _, result := range results {
		normalizedResult := normalizeExpectedResult(result, len(normalizedResults)+1)
		if strings.TrimSpace(normalizedResult.Description) == "" {
			continue
		}
		key := normalizedResult.Type + "\x00" + normalizedResult.Description
		if seenResults[key] {
			continue
		}
		seenResults[key] = true
		normalizedResults = append(normalizedResults, normalizedResult)
	}
	return normalizedResults
}

func normalizeExpectedResult(result ExpectedResult, index int) ExpectedResult {
	result.ID = strings.TrimSpace(result.ID)
	if result.ID == "" {
		result.ID = "result-" + strconv.Itoa(index)
	}
	result.Type = normalizeExpectedResultType(result.Type)
	result.Description = strings.TrimSpace(result.Description)
	result.AcceptanceHints = AppendUniqueStrings(result.AcceptanceHints)
	return result
}

func normalizeExpectedResultType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ExpectedResultTypeFile:
		return ExpectedResultTypeFile
	case ExpectedResultTypeLink:
		return ExpectedResultTypeLink
	default:
		return ExpectedResultTypeMessage
	}
}

func AppendUniqueStrings(values []string, candidates ...string) []string {
	nextValues := append([]string{}, values...)
	seenValue := map[string]bool{}
	for _, value := range nextValues {
		seenValue[value] = true
	}
	for _, candidate := range candidates {
		trimmedCandidate := strings.TrimSpace(candidate)
		if trimmedCandidate == "" || seenValue[trimmedCandidate] {
			continue
		}
		seenValue[trimmedCandidate] = true
		nextValues = append(nextValues, trimmedCandidate)
	}
	return nextValues
}
