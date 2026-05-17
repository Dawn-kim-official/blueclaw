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
			Content: "You convert the user's latest request into zero to five short skill search descriptions. The latest user request is authoritative. Use prior conversation only when it is needed to understand what the latest request means. If the latest request naturally continues prior work, carry forward only relevant topic, constraints, and output expectations. If the latest request is self-contained or changes topic or output type, do not carry forward stale subjects, websites, tools, or artifact formats. The first query should restate the latest request's main task, including its topic and deliverable when present. Return an empty queries array when no skill or external tool capability is needed. Each description must be a concise action-oriented sentence in English. Do not include workflow instructions, safety policy, tool arguments, or final answers.",
		},
		{
			Role:    "system",
			Content: "Available tools: " + strings.Join(skillSearchAvailableToolNames(request), ", "),
		},
		{
			Role:    "system",
			Content: buildTemporalContextDescription(request.TurnStartedAt),
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
