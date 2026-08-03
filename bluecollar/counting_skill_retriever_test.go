package bluecollar

import (
	"context"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/model"
)

type countingSkillRetriever struct {
	result      SkillRetrievalResult
	searchCount int
	requests    []AgentRequest
}

func (retriever *countingSkillRetriever) Available(_ AgentRequest, skillInstructions []SkillInstruction) []SkillInstruction {
	return skillInstructions
}

func (retriever *countingSkillRetriever) Retrieve(_ context.Context, request AgentRequest, _ []SkillInstruction, _ int) SkillRetrievalResult {
	retriever.searchCount++
	retriever.requests = append(retriever.requests, request)
	return retriever.result
}

func (retriever *countingSkillRetriever) Search(_ context.Context, request AgentRequest, _ []SkillInstruction, _ SkillSearchQuerySet, _ int) SkillRetrievalResult {
	retriever.searchCount++
	retriever.requests = append(retriever.requests, request)
	return retriever.result
}

func (retriever *countingSkillRetriever) Refresh(context.Context, []SkillInstruction) {}

func joinedMessageContent(messages []model.Message) string {
	parts := []string{}
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}
