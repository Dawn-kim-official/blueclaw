package tool

import (
	"context"
	"fmt"

	"github.com/blueclaw/blueclaw/internal/memory"
)

type RememberTool struct {
	store       *memory.Store
	searchIndex *memory.SearchIndex
	embedding   memory.EmbeddingGenerator
}

func NewRememberTool(store *memory.Store, searchIndex *memory.SearchIndex, embedding memory.EmbeddingGenerator) *RememberTool {
	return &RememberTool{
		store:       store,
		searchIndex: searchIndex,
		embedding:   embedding,
	}
}

func (tool *RememberTool) Name() string { return "remember" }

func (tool *RememberTool) Description() string {
	return "Save an important piece of information to memory for future recall."
}

func (tool *RememberTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subject": map[string]any{
				"type":        "string",
				"description": "Short topic label for the memory",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The information to remember",
			},
		},
		"required": []string{"subject", "content"},
	}
}

func (tool *RememberTool) Execute(executionContext context.Context, arguments map[string]any) (Result, error) {
	subject, _ := arguments["subject"].(string)
	content, _ := arguments["content"].(string)
	if subject == "" {
		return Result{Error: "subject is required"}, nil
	}
	if content == "" {
		return Result{Error: "content is required"}, nil
	}
	filePath, err := tool.store.Save(subject, content)
	if err != nil {
		return Result{}, fmt.Errorf("saving memory: %w", err)
	}
	if tool.searchIndex == nil || tool.embedding == nil {
		return Result{Output: fmt.Sprintf("saved to %s", filePath)}, nil
	}
	embedding, err := tool.embedding.Generate(executionContext, subject+" "+content)
	if err != nil {
		return Result{Output: fmt.Sprintf("saved to %s (embedding failed: %v)", filePath, err)}, nil
	}
	if err := tool.searchIndex.Upsert(subject, filePath, "short-term", embedding); err != nil {
		return Result{Output: fmt.Sprintf("saved to %s (index failed: %v)", filePath, err)}, nil
	}
	return Result{Output: fmt.Sprintf("saved to %s", filePath)}, nil
}
