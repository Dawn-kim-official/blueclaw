package agent

import (
	"context"
	"testing"
	"time"
)

type blockingEmbeddingProvider struct{}

func (blockingEmbeddingProvider) GenerateEmbedding(ctx context.Context, _ string) ([]float32, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSkillSearchDegradesToBM25WhenEmbeddingBlocks(t *testing.T) {
	retriever := NewEmbeddingSkillRetriever(blockingEmbeddingProvider{}, t.TempDir())
	skillInstructions := []SkillInstruction{{Name: "calendar", Description: "calendar management", Prompt: "calendar skill"}}

	startedAt := time.Now()
	result := retriever.Search(context.Background(), AgentRequest{Prompt: "회의 잡아줘"}, skillInstructions, SkillSearchQuerySet{}, 3)

	if elapsed := time.Since(startedAt); elapsed > skillEmbeddingSearchTimeout+5*time.Second {
		t.Fatalf("expected the search to degrade within the timeout, took %s", elapsed)
	}
	if result.RetrievalMode != "bm25_fallback" {
		t.Fatalf("expected BM25 degradation when embedding blocks, got %+v", result)
	}
}
