package tool

import (
	"context"
	"fmt"
	"time"

	"github.com/blueclaw/blueclaw/internal/memory"
)

type RememberTool struct {
	graphStore *memory.GraphStore
	embedding  memory.EmbeddingGenerator
}

func NewRememberTool(graphStore *memory.GraphStore, embedding memory.EmbeddingGenerator) *RememberTool {
	return &RememberTool{graphStore: graphStore, embedding: embedding}
}

func (tool *RememberTool) Name() string { return "remember" }

func (tool *RememberTool) Description() string {
	return "Save an important piece of information to memory for future recall. Use type 'fact' for stated truths, 'preference' for behavioral patterns, 'episode' for time-bound events."
}

func (tool *RememberTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "Short, unique topic label for the memory",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The information to remember",
			},
			"type": map[string]any{
				"type":        "string",
				"enum":        []string{"fact", "preference", "episode"},
				"description": "Memory type: fact (permanent truth), preference (behavioral pattern), episode (time-bound event)",
			},
		},
		"required": []string{"title", "content", "type"},
	}
}

func (tool *RememberTool) Execute(executionContext context.Context, arguments map[string]any) (Result, error) {
	title, _ := arguments["title"].(string)
	content, _ := arguments["content"].(string)
	memTypeStr, _ := arguments["type"].(string)
	if title == "" {
		return Result{Error: "title is required"}, nil
	}
	if content == "" {
		return Result{Error: "content is required"}, nil
	}
	memType, err := parseMemoryType(memTypeStr)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	expiresAt := defaultExpiresAt(memType)
	id, err := tool.graphStore.Save(title, content, memType, expiresAt)
	if err != nil {
		return Result{}, fmt.Errorf("saving memory: %w", err)
	}
	if tool.embedding != nil {
		embedding, err := tool.embedding.Generate(executionContext, title)
		if err == nil {
			tool.graphStore.SaveEmbedding(id, embedding)
		}
	}
	return Result{Output: fmt.Sprintf("remembered: %s", title)}, nil
}

func parseMemoryType(value string) (memory.MemoryType, error) {
	switch memory.MemoryType(value) {
	case memory.MemoryTypeFact, memory.MemoryTypePreference, memory.MemoryTypeEpisode:
		return memory.MemoryType(value), nil
	default:
		return "", fmt.Errorf("invalid memory type %q: must be fact, preference, or episode", value)
	}
}

func defaultExpiresAt(memType memory.MemoryType) *time.Time {
	if memType != memory.MemoryTypeEpisode {
		return nil
	}
	expiry := time.Now().Add(memory.DefaultEpisodeTTL)
	return &expiry
}
