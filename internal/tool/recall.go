package tool

import (
	"context"
	"encoding/json"

	"github.com/blueclaw/blueclaw/internal/memory"
)

type RecallTool struct {
	store       *memory.Store
	searchIndex *memory.SearchIndex
	embedding   memory.EmbeddingGenerator
	topK        int
}

func NewRecallTool(store *memory.Store, searchIndex *memory.SearchIndex, embedding memory.EmbeddingGenerator, topK int) *RecallTool {
	return &RecallTool{
		store:       store,
		searchIndex: searchIndex,
		embedding:   embedding,
		topK:        topK,
	}
}

func (tool *RecallTool) Name() string { return "recall" }

func (tool *RecallTool) Description() string {
	return "Search memories by semantic similarity to find previously saved information."
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
	if tool.searchIndex == nil || tool.embedding == nil {
		return tool.recallByListing()
	}
	queryEmbedding, err := tool.embedding.Generate(executionContext, query)
	if err != nil {
		return tool.recallByListing()
	}
	results, err := tool.searchIndex.TopK(queryEmbedding, tool.topK)
	if err != nil {
		return tool.recallByListing()
	}
	recallResults := make([]recallResult, 0, len(results))
	for _, result := range results {
		slug := memory.Slugify(result.Subject)
		loadedMemory, err := tool.store.Load(result.Storage, slug)
		if err != nil {
			continue
		}
		loadedMemory.RecallCount++
		tool.store.IncrementRecallCount(&loadedMemory)
		tool.searchIndex.UpdateRecallCount(result.Subject, loadedMemory.RecallCount)
		if loadedMemory.RecallCount >= memory.PromotionThreshold && loadedMemory.Storage == "short-term" {
			memory.PromoteIfEligible(tool.store, tool.searchIndex, &loadedMemory)
		}
		recallResults = append(recallResults, recallResult{
			Subject:  result.Subject,
			Content:  loadedMemory.Content,
			Distance: result.Distance,
			Storage:  loadedMemory.Storage,
		})
	}
	output, _ := json.Marshal(map[string]any{"memories": recallResults})
	return Result{Output: string(output)}, nil
}

func (tool *RecallTool) recallByListing() (Result, error) {
	var all []memory.Memory
	for _, storage := range []string{"short-term", "long-term"} {
		memories, err := tool.store.ListMemories(storage)
		if err == nil {
			all = append(all, memories...)
		}
	}
	recallResults := make([]recallResult, 0, len(all))
	for _, m := range all {
		recallResults = append(recallResults, recallResult{
			Subject: m.Subject,
			Content: m.Content,
			Storage: m.Storage,
		})
	}
	output, _ := json.Marshal(map[string]any{"memories": recallResults})
	return Result{Output: string(output)}, nil
}

type recallResult struct {
	Subject  string  `json:"subject"`
	Content  string  `json:"content"`
	Distance float64 `json:"distance"`
	Storage  string  `json:"storage"`
}
