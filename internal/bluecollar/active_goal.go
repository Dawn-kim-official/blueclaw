package bluecollar

import (
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
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

const (
	ArtifactRequirementNone      = "none"
	ArtifactRequirementPreferred = "preferred"
	ArtifactRequirementRequired  = "required"

	ExpectedResultTypeMessage = "message"
	ExpectedResultTypeFile    = "file"
	ExpectedResultTypeLink    = "link"
)

func normalizePersistedActiveGoal(activeGoal ActiveGoal) ActiveGoal {
	activeGoal.RequiredNextTools = normalizePersistedToolNames(activeGoal.RequiredNextTools)
	activeGoal.SelectedToolNames = normalizePersistedToolNames(activeGoal.SelectedToolNames)
	activeGoal.OutcomeContract = normalizePersistedOutcomeContract(activeGoal.OutcomeContract)
	return activeGoal
}

func normalizePersistedOutcomeContract(contract OutcomeContract) OutcomeContract {
	contract.RequiredEvidenceTools = normalizePersistedToolNames(contract.RequiredEvidenceTools)
	contract.RequiredEvidenceAnyOf = normalizePersistedToolNameGroups(contract.RequiredEvidenceAnyOf)
	contract.SelectedEvidenceHints = normalizePersistedToolNames(contract.SelectedEvidenceHints)
	contract.ExpectedResults = normalizePersistedExpectedResults(contract.ExpectedResults)
	contract.RequiredEffects = normalizePersistedOutcomeEffects(contract.RequiredEffects)
	return normalizeOutcomeContract(contract)
}

func normalizePersistedToolNameGroups(groups [][]string) [][]string {
	normalizedGroups := make([][]string, 0, len(groups))
	for _, group := range groups {
		normalizedGroups = append(normalizedGroups, normalizePersistedToolNames(group))
	}
	return normalizedGroups
}

func normalizePersistedToolNames(toolNames []string) []string {
	normalizedToolNames := make([]string, 0, len(toolNames))
	for _, toolName := range toolNames {
		normalizedToolNames = appendUniqueStrings(normalizedToolNames, normalizePersistedToolName(toolName))
	}
	return normalizedToolNames
}

func normalizePersistedToolName(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "ask_choice":
		return toolcontract.AskInputToolName
	case "artifact.deliver", "file.attach":
		return toolcontract.FileDeliverToolName
	case "site.promote", "site.publish", "site.preview":
		return "site_serve"
	case "terminal.session":
		return toolcontract.TerminalRunToolName
	default:
		return strings.TrimSpace(toolName)
	}
}

func normalizePersistedExpectedResults(results []ExpectedResult) []ExpectedResult {
	normalizedResults := make([]ExpectedResult, 0, len(results))
	for _, result := range results {
		result.AcceptanceHints = normalizePersistedToolNames(result.AcceptanceHints)
		normalizedResults = append(normalizedResults, result)
	}
	return normalizedResults
}

func normalizePersistedOutcomeEffects(effects []OutcomeEffect) []OutcomeEffect {
	normalizedEffects := make([]OutcomeEffect, 0, len(effects))
	for _, effect := range effects {
		effect.SuggestedNextTools = normalizePersistedToolNames(effect.SuggestedNextTools)
		normalizedEffects = append(normalizedEffects, effect)
	}
	return normalizedEffects
}

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
	return "Active conversation goal:\n" + string(document) + "\nTreat the current user message as input to this goal unless it clearly starts a new unrelated request. Preserve the current user message as the latest user input; do not rewrite it.\nrequiredNextTools and selectedToolNames are intake suggestions, not commands: when a listed tool contradicts what the user visibly asked for, use the tool that actually fulfills the request (load it with request_tools if it is not exposed)."
}

func normalizeOutcomeContract(contract OutcomeContract) OutcomeContract {
	contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools)
	contract.RequiredAttachmentSuffixes = appendUniqueStrings(contract.RequiredAttachmentSuffixes)
	contract.SelectedEvidenceHints = appendUniqueStrings(contract.SelectedEvidenceHints)
	contract.RequiredEvidenceAnyOf = normalizeEvidenceAnyOf(contract.RequiredEvidenceAnyOf)
	contract.RequiredEffects = normalizeOutcomeEffects(contract.RequiredEffects)
	contract.ExpectedResults = normalizeExpectedResults(contract.ExpectedResults)
	contract.ArtifactRequirement = normalizeArtifactRequirement(contract.ArtifactRequirement)
	if expectedResultRequiresFileAttachment(contract) {
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, toolcontract.FileDeliverToolName)
		contract.ArtifactRequirement = ArtifactRequirementRequired
	}
	contract.Source = strings.TrimSpace(contract.Source)
	return contract
}

func normalizeOutcomeEffects(effects []OutcomeEffect) []OutcomeEffect {
	normalizedEffects := []OutcomeEffect{}
	seenEffects := map[string]bool{}
	for _, effect := range effects {
		normalizedEffect := normalizeOutcomeEffect(effect)
		if normalizedEffect.ObjectType == "" || normalizedEffect.Effect == "" {
			continue
		}
		key := normalizedEffect.ObjectType + "\x00" + normalizedEffect.Effect
		if seenEffects[key] {
			continue
		}
		seenEffects[key] = true
		normalizedEffects = append(normalizedEffects, normalizedEffect)
	}
	return normalizedEffects
}

func normalizeOutcomeEffect(effect OutcomeEffect) OutcomeEffect {
	return OutcomeEffect{
		ObjectType:         strings.TrimSpace(effect.ObjectType),
		Effect:             strings.TrimSpace(effect.Effect),
		Description:        strings.TrimSpace(effect.Description),
		SuggestedNextTools: appendUniqueStrings(effect.SuggestedNextTools),
	}
}

func normalizeExpectedResults(results []ExpectedResult) []ExpectedResult {
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
	result.AcceptanceHints = appendUniqueStrings(result.AcceptanceHints)
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
