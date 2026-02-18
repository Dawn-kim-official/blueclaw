package tool

import (
	"context"
	"encoding/json"

	"github.com/blueclaw/blueclaw/internal/memory"
)

type RecallTool struct {
	graphStore *memory.GraphStore
	embedding  memory.EmbeddingGenerator
	topK       int
}

func NewRecallTool(graphStore *memory.GraphStore, embedding memory.EmbeddingGenerator, topK int) *RecallTool {
	return &RecallTool{graphStore: graphStore, embedding: embedding, topK: topK}
}

func (tool *RecallTool) Name() string { return "recall" }

func (tool *RecallTool) Description() string {
	return "Search memories by semantic similarity. Returns matching memories plus their directly connected neighbors."
}

func (tool *RecallTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query to find related memories",
			},
		},
		"required": []string{"query"},
	}
}

func (tool *RecallTool) Execute(executionContext context.Context, arguments map[string]any) (Result, error) {
	query, _ := arguments["query"].(string)
	if query == "" {
		return Result{Error: "query is required"}, nil
	}
	if tool.embedding == nil {
		return tool.recallByRecency()
	}
	queryEmbedding, err := tool.embedding.Generate(executionContext, query)
	if err != nil {
		return tool.recallByRecency()
	}
	hits, err := tool.graphStore.TopK(queryEmbedding, tool.topK)
	if err != nil {
		return emptyRecallResult()
	}
	if len(hits) == 0 {
		return emptyRecallResult()
	}
	return tool.buildResult(hits, "search")
}

func (tool *RecallTool) recallByRecency() (Result, error) {
	memories, err := tool.graphStore.Recent(tool.topK)
	if err != nil || len(memories) == 0 {
		return emptyRecallResult()
	}
	return tool.buildResult(memories, "recent")
}

func (tool *RecallTool) buildResult(hits []memory.Memory, source string) (Result, error) {
	seen := make(map[int64]bool)
	results := make([]recallResult, 0, len(hits))
	for _, hit := range hits {
		if seen[hit.ID] {
			continue
		}
		seen[hit.ID] = true
		results = append(results, recallResult{
			Title:   hit.Title,
			Content: hit.Content,
			Type:    string(hit.Type),
			Source:  source,
		})
		tool.updateRecallState(hit)
		neighbors, err := tool.graphStore.Neighbors(hit.ID)
		if err != nil {
			continue
		}
		for _, neighbor := range neighbors {
			if seen[neighbor.ID] {
				continue
			}
			seen[neighbor.ID] = true
			results = append(results, recallResult{
				Title:   neighbor.Title,
				Content: neighbor.Content,
				Type:    string(neighbor.Type),
				Source:  "neighbor",
			})
		}
	}
	output, _ := json.Marshal(map[string]any{"memories": results})
	return Result{Output: string(output)}, nil
}

func (tool *RecallTool) updateRecallState(m memory.Memory) {
	if m.Type != memory.MemoryTypeEpisode {
		tool.graphStore.IncrementRecall(m.ID)
		return
	}
	newCount := m.RecallCount + 1
	if newCount >= memory.PromotionThreshold {
		tool.graphStore.Promote(m.ID)
	} else {
		tool.graphStore.ExtendExpiration(m.ID, memory.ExpirationExtension)
	}
	tool.graphStore.IncrementRecall(m.ID)
}

func emptyRecallResult() (Result, error) {
	output, _ := json.Marshal(map[string]any{"memories": []recallResult{}})
	return Result{Output: string(output)}, nil
}

type recallResult struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Type    string `json:"type"`
	Source  string `json:"source"`
}
