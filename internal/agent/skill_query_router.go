package agent

import (
	"context"
	"encoding/json"
	"strings"

	"blueclaw/internal/llm"
)

type SkillSearchQueryRouter struct {
	languageModel llm.LanguageModelProvider
}

func NewSkillSearchQueryRouter(languageModel llm.LanguageModelProvider) SkillSearchQueryRouter {
	return SkillSearchQueryRouter{languageModel: languageModel}
}

func (skillSearchQueryRouter SkillSearchQueryRouter) Build(ctx context.Context, request AgentRequest) (SkillSearchQuerySet, bool) {
	if skillSearchQueryRouter.languageModel == nil {
		return SkillSearchQuerySet{}, false
	}
	structuredResponse, errorValue := skillSearchQueryRouter.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: skillSearchQueryRouter.buildMessages(request),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_skill_search_queries",
			Document:           `{"type":"object","properties":{"queries":{"type":"array","minItems":0,"maxItems":5,"items":{"type":"object","properties":{"description":{"type":"string"}},"required":["description"],"additionalProperties":false}}},"required":["queries"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return SkillSearchQuerySet{}, false
	}
	var document map[string]json.RawMessage
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &document); errorValue != nil {
		return SkillSearchQuerySet{}, false
	}
	queries, isFound := document["queries"]
	if !isFound {
		return SkillSearchQuerySet{}, false
	}
	var querySet SkillSearchQuerySet
	if errorValue := json.Unmarshal(queries, &querySet.Queries); errorValue != nil {
		return SkillSearchQuerySet{}, false
	}
	return normalizeSkillSearchQuerySet(querySet), true
}

func (skillSearchQueryRouter SkillSearchQueryRouter) buildMessages(request AgentRequest) []llm.Message {
	messages := []llm.Message{
		{
			Role:    "system",
			Content: "You convert the user's current request into zero to five short skill search descriptions. Return an empty queries array when no skill or external tool capability is needed. Each description must be a concise action-oriented sentence in English. Do not include workflow instructions, safety policy, tool arguments, or final answers.",
		},
		{
			Role:    "system",
			Content: "Available tools: " + strings.Join(skillSearchAvailableToolNames(request), ", "),
		},
	}
	if contextDescription := buildVisibleContextDescription(request.VisibleContext); contextDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: contextDescription})
	}
	if goalDescription := activeGoalDescription(request.ActiveGoal); goalDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: goalDescription})
	}
	messages = append(messages, llm.Message{Role: "user", Content: request.Prompt})
	return messages
}

func skillSearchAvailableToolNames(request AgentRequest) []string {
	if request.ToolSet == nil {
		return nil
	}
	return request.ToolSet.ListToolNames()
}
