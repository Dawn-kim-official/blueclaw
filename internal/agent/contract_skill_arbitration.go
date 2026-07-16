package agent

import (
	"context"
	"encoding/json"
	"strings"

	"blueclaw/internal/llm"
)

const maxContractSkillArbitrationCandidates = 8

type contractSkillArbitration struct {
	SelectedSkillNames []string `json:"selectedSkillNames"`
	RejectedSkillNames []string `json:"rejectedSkillNames"`
	RequiredNextTools  []string `json:"requiredNextToolNames"`
	ExpectedEvidence   []string `json:"expectedEvidence"`
	UnmetPreconditions []string `json:"unmetPreconditions"`
	Reason             string   `json:"reason"`
}

type contractSkillCandidateCard struct {
	Name                       string   `json:"name"`
	Description                string   `json:"description,omitempty"`
	WhenToUse                  string   `json:"whenToUse,omitempty"`
	AllowedTools               []string `json:"allowedTools,omitempty"`
	RequiredEvidenceTools      []string `json:"requiredEvidenceTools,omitempty"`
	RequiredAttachmentSuffixes []string `json:"requiredAttachmentSuffixes,omitempty"`
	Score                      float64  `json:"score,omitempty"`
	RetrievalReason            string   `json:"retrievalReason,omitempty"`
	SourcePath                 string   `json:"sourcePath,omitempty"`
	PromptExcerpt              string   `json:"promptExcerpt,omitempty"`
}

func (skillSearchQueryRouter SkillSearchQueryRouter) ArbitrateContractSkills(ctx context.Context, request AgentRequest, candidates []SkillInstruction, candidateByName map[string]SkillCandidate) (contractSkillArbitration, bool) {
	if skillSearchQueryRouter.languageModel == nil || !requestHasOutcomeContractForSkillArbitration(request) || len(candidates) == 0 {
		return contractSkillArbitration{}, false
	}
	candidates = limitSkillInstructions(candidates, maxContractSkillArbitrationCandidates)
	structuredResponse, errorValue := skillSearchQueryRouter.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: contractSkillArbitrationMessages(request, candidates, candidateByName),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_contract_skill_arbitration",
			Document:           `{"type":"object","properties":{"selectedSkillNames":{"type":"array","maxItems":3,"items":{"type":"string"}},"rejectedSkillNames":{"type":"array","items":{"type":"string"}},"requiredNextToolNames":{"type":"array","maxItems":12,"items":{"type":"string"}},"expectedEvidence":{"type":"array","maxItems":8,"items":{"type":"string"}},"unmetPreconditions":{"type":"array","maxItems":8,"items":{"type":"string"}},"reason":{"type":"string"}},"required":["selectedSkillNames","rejectedSkillNames","requiredNextToolNames","expectedEvidence","unmetPreconditions","reason"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return contractSkillArbitration{}, false
	}
	var arbitration contractSkillArbitration
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &arbitration); errorValue != nil {
		return contractSkillArbitration{}, false
	}
	arbitration = normalizeContractSkillArbitration(arbitration, candidates)
	if !contractSkillArbitrationHasContent(arbitration) {
		return contractSkillArbitration{}, false
	}
	return arbitration, true
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
			"expectedEvidence must name the exact successful operations that prove the requested outcome. Include each state-changing requiredNextTool in expectedEvidence; requiredNextToolNames only describes execution order.",
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
			Name:                       candidate.Name,
			Description:                strings.TrimSpace(candidate.Description),
			WhenToUse:                  strings.TrimSpace(candidate.WhenToUse),
			AllowedTools:               appendUniqueStrings(candidate.AllowedTools),
			RequiredEvidenceTools:      appendUniqueStrings(candidate.Completion.RequiredEvidenceTools),
			RequiredAttachmentSuffixes: appendUniqueStrings(candidate.Completion.RequiredAttachmentSuffixes),
			Score:                      skillCandidate.Score,
			RetrievalReason:            skillCandidate.Reason,
			SourcePath:                 strings.TrimSpace(candidate.Source.Path),
			PromptExcerpt:              trimTextForSkillArbitration(candidate.Prompt, 1800),
		})
	}
	document, errorValue := json.Marshal(cards)
	if errorValue != nil {
		return "[]"
	}
	return string(document)
}

func normalizeContractSkillArbitration(arbitration contractSkillArbitration, candidates []SkillInstruction) contractSkillArbitration {
	candidateNames := map[string]bool{}
	for _, candidate := range candidates {
		candidateNames[strings.TrimSpace(candidate.Name)] = true
	}
	arbitration.SelectedSkillNames = filterCandidateSkillNames(arbitration.SelectedSkillNames, candidateNames)
	arbitration.RejectedSkillNames = filterCandidateSkillNames(arbitration.RejectedSkillNames, candidateNames)
	arbitration.RequiredNextTools = appendUniqueStrings(arbitration.RequiredNextTools)
	arbitration.ExpectedEvidence = appendUniqueStrings(arbitration.ExpectedEvidence)
	arbitration.UnmetPreconditions = appendUniqueStrings(arbitration.UnmetPreconditions)
	arbitration.Reason = strings.TrimSpace(arbitration.Reason)
	return arbitration
}

func filterCandidateSkillNames(values []string, candidateNames map[string]bool) []string {
	result := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" && candidateNames[trimmedValue] {
			result = appendUniqueStrings(result, trimmedValue)
		}
	}
	return result
}

func contractSkillArbitrationHasContent(arbitration contractSkillArbitration) bool {
	return len(arbitration.SelectedSkillNames) > 0 || len(arbitration.RejectedSkillNames) > 0
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
