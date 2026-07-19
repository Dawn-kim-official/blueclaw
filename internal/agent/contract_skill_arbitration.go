package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"blueclaw/internal/llm"
)

const maxContractSkillArbitrationCandidates = 8

type contractSkillArbitrationStatus string

const (
	contractSkillArbitrationNotApplicable contractSkillArbitrationStatus = "not_applicable"
	contractSkillArbitrationSucceeded     contractSkillArbitrationStatus = "succeeded"
	contractSkillArbitrationFailed        contractSkillArbitrationStatus = "failed"
)

type contractSkillArbitrationResult struct {
	Arbitration contractSkillArbitration
	Status      contractSkillArbitrationStatus
}

type contractSkillArbitration struct {
	SelectedSkillNames []string `json:"selectedSkillNames"`
	RejectedSkillNames []string `json:"rejectedSkillNames"`
	RequiredNextTools  []string `json:"requiredNextToolNames"`
	ExpectedEvidence   []string `json:"expectedEvidence"`
	UnmetPreconditions []string `json:"unmetPreconditions"`
	Reason             string   `json:"reason"`
}

type contractSkillCandidateCard struct {
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	ToolReferences  []string `json:"toolReferences,omitempty"`
	Score           float64  `json:"score,omitempty"`
	RetrievalReason string   `json:"retrievalReason,omitempty"`
	SourcePath      string   `json:"sourcePath,omitempty"`
	PromptExcerpt   string   `json:"promptExcerpt,omitempty"`
}

type contractSkillArbitrationVocabulary struct {
	SkillNames       []string
	ToolNames        []string
	EvidenceNames    []string
	RequiresEvidence bool
}

func (skillSearchQueryRouter SkillSearchQueryRouter) ArbitrateContractSkills(ctx context.Context, request AgentRequest, candidates []SkillInstruction, candidateByName map[string]SkillCandidate) contractSkillArbitrationResult {
	if !requestHasOutcomeContractForSkillArbitration(request) || len(candidates) == 0 {
		return contractSkillArbitrationResult{Status: contractSkillArbitrationNotApplicable}
	}
	if skillSearchQueryRouter.languageModel == nil {
		return contractSkillArbitrationResult{Status: contractSkillArbitrationFailed}
	}
	candidates = limitSkillInstructions(candidates, maxContractSkillArbitrationCandidates)
	messages := contractSkillArbitrationMessages(request, candidates, candidateByName)
	vocabulary := buildContractSkillArbitrationVocabulary(request, candidates)
	schema := contractSkillArbitrationSchema(vocabulary)
	for attempt := 0; attempt < 2; attempt++ {
		arbitration, errorValue := skillSearchQueryRouter.generateContractSkillArbitration(ctx, messages, schema)
		if errorValue == nil {
			errorValue = validateContractSkillArbitration(arbitration, request, candidates, vocabulary)
		}
		if errorValue == nil {
			return contractSkillArbitrationResult{Arbitration: arbitration, Status: contractSkillArbitrationSucceeded}
		}
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: "The previous candidate was invalid. Return only exact enum values from the schema. Validation: " + errorValue.Error(),
		})
	}
	return contractSkillArbitrationResult{Status: contractSkillArbitrationFailed}
}

func (skillSearchQueryRouter SkillSearchQueryRouter) generateContractSkillArbitration(ctx context.Context, messages []llm.Message, schema string) (contractSkillArbitration, error) {
	response, errorValue := skillSearchQueryRouter.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_contract_skill_arbitration",
			Document:           schema,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return contractSkillArbitration{}, errorValue
	}
	var arbitration contractSkillArbitration
	errorValue = json.Unmarshal([]byte(response.Content), &arbitration)
	return arbitration, errorValue
}

func contractSkillArbitrationSchema(vocabulary contractSkillArbitrationVocabulary) string {
	document := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"selectedSkillNames", "rejectedSkillNames", "requiredNextToolNames", "expectedEvidence", "unmetPreconditions", "reason"},
		"properties": map[string]any{
			"selectedSkillNames":    exactStringArraySchema(vocabulary.SkillNames, 3),
			"rejectedSkillNames":    exactStringArraySchema(vocabulary.SkillNames, len(vocabulary.SkillNames)),
			"requiredNextToolNames": exactStringArraySchema(vocabulary.ToolNames, 12),
			"expectedEvidence":      exactStringArraySchema(vocabulary.EvidenceNames, 8),
			"unmetPreconditions": map[string]any{
				"type":     "array",
				"maxItems": 8,
				"items":    map[string]any{"type": "string"},
			},
			"reason": map[string]any{"type": "string"},
		},
	}
	encodedDocument, _ := json.Marshal(document)
	return string(encodedDocument)
}

func exactStringArraySchema(values []string, maximumItems int) map[string]any {
	itemSchema := map[string]any{"type": "string"}
	if len(values) > 0 {
		itemSchema["enum"] = values
	} else {
		maximumItems = 0
	}
	return map[string]any{
		"type":     "array",
		"maxItems": maximumItems,
		"items":    itemSchema,
	}
}

func buildContractSkillArbitrationVocabulary(request AgentRequest, candidates []SkillInstruction) contractSkillArbitrationVocabulary {
	vocabulary := contractSkillArbitrationVocabulary{
		RequiresEvidence: requiredEvidenceIncludesSideEffect(request.ToolSet, request.ActiveGoal.OutcomeContract.RequiredEvidenceTools),
	}
	for _, candidate := range candidates {
		vocabulary.SkillNames = appendUniqueStrings(vocabulary.SkillNames, candidate.Name)
		for _, toolName := range SkillToolNames(candidate) {
			if requestHasToolName(request, toolName) {
				vocabulary.ToolNames = appendUniqueStrings(vocabulary.ToolNames, toolName)
			}
		}
	}
	for _, toolName := range KernelToolNames() {
		if requestHasToolName(request, toolName) {
			vocabulary.ToolNames = appendUniqueStrings(vocabulary.ToolNames, toolName)
		}
	}
	evidenceCandidates := appendUniqueStrings(request.ActiveGoal.OutcomeContract.RequiredEvidenceTools)
	for _, candidate := range candidates {
		evidenceCandidates = appendUniqueStrings(evidenceCandidates, SkillToolNames(candidate)...)
	}
	for _, toolName := range evidenceCandidates {
		if requiredEvidenceToolCanBeSatisfied(request.ToolSet, toolName) {
			vocabulary.EvidenceNames = appendUniqueStrings(vocabulary.EvidenceNames, toolName)
		}
	}
	return vocabulary
}

func validateContractSkillArbitration(arbitration contractSkillArbitration, request AgentRequest, candidates []SkillInstruction, vocabulary contractSkillArbitrationVocabulary) error {
	if len(arbitration.SelectedSkillNames)+len(arbitration.RejectedSkillNames) == 0 {
		return fmt.Errorf("no skill was selected or rejected")
	}
	if errorValue := validateExactContractNames(arbitration.SelectedSkillNames, stringSet(vocabulary.SkillNames)); errorValue != nil {
		return errorValue
	}
	if errorValue := validateExactContractNames(arbitration.RejectedSkillNames, stringSet(vocabulary.SkillNames)); errorValue != nil {
		return errorValue
	}
	selectedSkills := contractSelectedSkillInstructions(arbitration.SelectedSkillNames, candidates)
	if errorValue := validateExactContractNames(arbitration.RequiredNextTools, selectedSkillNextToolNameSet(selectedSkills, request)); errorValue != nil {
		return fmt.Errorf("required next: %w", errorValue)
	}
	if errorValue := validateExactContractNames(arbitration.ExpectedEvidence, selectedSkillEvidenceToolNameSet(selectedSkills, request)); errorValue != nil {
		return fmt.Errorf("expected evidence: %w", errorValue)
	}
	if vocabulary.RequiresEvidence && len(arbitration.ExpectedEvidence) == 0 {
		return fmt.Errorf("expected evidence is required")
	}
	evidenceNames := stringSet(vocabulary.EvidenceNames)
	for _, toolName := range arbitration.ExpectedEvidence {
		if !evidenceNames[toolName] {
			return fmt.Errorf("expected evidence %s cannot prove completion", toolName)
		}
	}
	return nil
}

func validateExactContractNames(values []string, allowedValues map[string]bool) error {
	seenValues := map[string]bool{}
	for _, value := range values {
		if !allowedValues[value] || seenValues[value] {
			return fmt.Errorf("invalid value %s", value)
		}
		seenValues[value] = true
	}
	return nil
}

func contractSelectedSkillInstructions(selectedSkillNames []string, candidates []SkillInstruction) []SkillInstruction {
	selectedNames := stringSet(selectedSkillNames)
	selectedSkills := []SkillInstruction{}
	for _, candidate := range candidates {
		if selectedNames[candidate.Name] {
			selectedSkills = append(selectedSkills, candidate)
		}
	}
	return selectedSkills
}

func requestHasOutcomeContractForSkillArbitration(request AgentRequest) bool {
	contract := request.ActiveGoal.OutcomeContract
	if len(artifactContractRequirementsForRequest(request)) > 0 {
		return true
	}
	if len(contract.RequiredEvidenceTools) > 0 || len(contract.RequiredEvidenceAnyOf) > 0 || len(contract.RequiredEffects) > 0 {
		return true
	}
	for _, expectedResult := range normalizeExpectedResults(contract.ExpectedResults) {
		if expectedResult.Required && strings.TrimSpace(expectedResult.Type) != "" {
			return true
		}
	}
	return false
}

func contractSkillArbitrationMessages(request AgentRequest, candidates []SkillInstruction, candidateByName map[string]SkillCandidate) []llm.Message {
	messages := []llm.Message{{
		Role: "system",
		Content: strings.Join([]string{
			"You decide which already retrieved SKILL.md candidates should be loaded for the current outcome contract.",
			"Select only skill names from the candidate list. Do not invent skills or tools.",
			"The latest user request is authoritative. Use prior conversation only to understand the current requested outcome.",
			"Choose capabilities needed to create, verify, deliver, or update the required result. Do not select skills merely because the requested content mentions their domain.",
			"requiredNextToolNames describes execution order. expectedEvidence contains only exact successful operations whose results prove the final requested outcome; do not promote intermediate operations to evidence.",
			"If no candidate is useful for the contract, return an empty selectedSkillNames array and reject the unsuitable candidates.",
		}, " "),
	}, {
		Role:    "system",
		Content: "Available tools: " + strings.Join(skillSearchAvailableToolNames(request), ", "),
	}, {
		Role:    "system",
		Content: "Outcome contract: " + outcomeContractJSON(request.ActiveGoal.OutcomeContract),
	}}
	if goalDescription := activeGoalDescription(request.ActiveGoal); goalDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: goalDescription})
	}
	if contextDescription := buildVisibleContextDescription(request.VisibleContext); contextDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: contextDescription})
	}
	messages = append(messages, llm.Message{
		Role:    "system",
		Content: "Candidate skills: " + contractSkillCandidateCardsJSON(candidates, candidateByName),
	})
	messages = append(messages, llm.Message{Role: "user", Content: request.Prompt})
	return messages
}

func contractSkillCandidateCardsJSON(candidates []SkillInstruction, candidateByName map[string]SkillCandidate) string {
	cards := []contractSkillCandidateCard{}
	for _, candidate := range candidates {
		skillCandidate := candidateByName[candidate.Name]
		cards = append(cards, contractSkillCandidateCard{
			Name:            candidate.Name,
			Description:     strings.TrimSpace(candidate.Description),
			ToolReferences:  appendUniqueStrings(candidate.ToolReferences),
			Score:           skillCandidate.Score,
			RetrievalReason: skillCandidate.Reason,
			SourcePath:      strings.TrimSpace(candidate.Source.Path),
			PromptExcerpt:   trimTextForSkillArbitration(candidate.Prompt, 1800),
		})
	}
	document, errorValue := json.Marshal(cards)
	if errorValue != nil {
		return "[]"
	}
	return string(document)
}

func limitSkillInstructions(skillInstructions []SkillInstruction, limit int) []SkillInstruction {
	if limit <= 0 || len(skillInstructions) <= limit {
		return skillInstructions
	}
	return skillInstructions[:limit]
}

func trimTextForSkillArbitration(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit]))
}
