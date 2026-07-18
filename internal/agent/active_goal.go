package agent

import (
	"encoding/json"
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
	SelectedToolNames   []string         `json:"selectedToolNames,omitempty"`
	SelectedSkillNames  []string         `json:"selectedSkillNames,omitempty"`
	OutcomeContract     OutcomeContract  `json:"outcomeContract,omitempty"`
	Status              ActiveGoalStatus `json:"status,omitempty"`
	RestoreError        string           `json:"-"`
}

type OutcomeContract struct {
	RequiredEvidenceTools      []string           `json:"requiredEvidenceTools,omitempty"`
	RequiredEvidenceAnyOf      [][]string         `json:"requiredEvidenceAnyOf,omitempty"`
	OperationContract          *OperationContract `json:"operationContract,omitempty"`
	RequiredAttachmentSuffixes []string           `json:"requiredAttachmentSuffixes,omitempty"`
	RequiredEffects            []OutcomeEffect    `json:"requiredEffects,omitempty"`
	ExpectedResults            []ExpectedResult   `json:"expectedResults,omitempty"`
	ArtifactRequirement        string             `json:"artifactRequirement,omitempty"`
	SelectedEvidenceHints      []string           `json:"selectedEvidenceHints,omitempty"`
	Source                     string             `json:"source,omitempty"`
}

type OperationContract struct {
	Version      int                    `json:"version"`
	Requirements []OperationRequirement `json:"requirements"`
}

type OperationInputMode string

const (
	OperationInputContainsExplicit OperationInputMode = "contains_explicit"
	OperationInputNoExplicitValues OperationInputMode = "no_explicit_values"
)

type OperationRequirement struct {
	RequirementID string             `json:"requirementID"`
	ToolID        string             `json:"toolID"`
	ToolName      string             `json:"toolName"`
	InputMode     OperationInputMode `json:"inputMode"`
	RequiredInput json.RawMessage    `json:"requiredInput"`
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
	contract.OperationContract = normalizePersistedOperationContract(contract.OperationContract)
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
	case "ask.choice":
		return AskInputToolName
	case "artifact.deliver", "file.attach":
		return FileDeliverToolName
	case "site.promote":
		return "site.publish"
	case "terminal.session":
		return TerminalRunToolName
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

func normalizePersistedOperationContract(contract *OperationContract) *OperationContract {
	if contract == nil {
		return nil
	}
	normalizedContract := *contract
	normalizedContract.Requirements = append([]OperationRequirement{}, contract.Requirements...)
	for index := range normalizedContract.Requirements {
		normalizedContract.Requirements[index].ToolName = normalizePersistedToolName(normalizedContract.Requirements[index].ToolName)
	}
	return &normalizedContract
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
	return "Active conversation goal:\n" + string(document) + "\nTreat the current user message as input to this goal unless it clearly starts a new unrelated request. Preserve the current user message as the latest user input; do not rewrite it."
}

func normalizeOutcomeContract(contract OutcomeContract) OutcomeContract {
	contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools)
	contract.RequiredAttachmentSuffixes = appendUniqueStrings(contract.RequiredAttachmentSuffixes)
	contract.SelectedEvidenceHints = appendUniqueStrings(contract.SelectedEvidenceHints)
	contract.RequiredEvidenceAnyOf = normalizeEvidenceAnyOf(contract.RequiredEvidenceAnyOf)
	contract.OperationContract = normalizeOperationContract(contract.OperationContract)
	contract.RequiredEffects = normalizeOutcomeEffects(contract.RequiredEffects)
	contract.ExpectedResults = normalizeExpectedResults(contract.ExpectedResults)
	contract.ArtifactRequirement = normalizeArtifactRequirement(contract.ArtifactRequirement)
	if expectedResultRequiresFileAttachment(contract) {
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, FileDeliverToolName)
		contract.ArtifactRequirement = ArtifactRequirementRequired
	}
	contract.Source = strings.TrimSpace(contract.Source)
	return contract
}

func normalizeOperationContract(contract *OperationContract) *OperationContract {
	if contract == nil {
		return nil
	}
	normalizedContract := &OperationContract{
		Version:      contract.Version,
		Requirements: make([]OperationRequirement, 0, len(contract.Requirements)),
	}
	for _, requirement := range contract.Requirements {
		requirement.RequirementID = strings.TrimSpace(requirement.RequirementID)
		requirement.ToolID = strings.TrimSpace(requirement.ToolID)
		requirement.ToolName = strings.TrimSpace(requirement.ToolName)
		requirement.InputMode = OperationInputMode(strings.TrimSpace(string(requirement.InputMode)))
		requirement.RequiredInput = normalizeOperationRequiredInput(requirement.RequiredInput)
		normalizedContract.Requirements = append(normalizedContract.Requirements, requirement)
	}
	return normalizedContract
}

func normalizeOperationRequiredInput(requiredInput json.RawMessage) json.RawMessage {
	if len(requiredInput) == 0 {
		return nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(requiredInput, &values) != nil || values == nil {
		return append(json.RawMessage{}, requiredInput...)
	}
	normalizedInput, _ := json.Marshal(values)
	return normalizedInput
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
