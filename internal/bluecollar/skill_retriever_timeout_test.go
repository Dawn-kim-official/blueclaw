package bluecollar

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
	result := retriever.Search(context.Background(), AgentRequest{Prompt: "set up a meeting"}, skillInstructions, SkillSearchQuerySet{}, 3)

	if elapsed := time.Since(startedAt); elapsed > skillEmbeddingSearchTimeout+5*time.Second {
		t.Fatalf("expected the search to degrade within the timeout, took %s", elapsed)
	}
	if result.RetrievalMode != "bm25_fallback" {
		t.Fatalf("expected BM25 degradation when embedding blocks, got %+v", result)
	}
}

func TestSkillSearchDegradesToBM25WhenIndexLockIsHeld(t *testing.T) {
	skillRetriever := NewEmbeddingSkillRetriever(blockingEmbeddingProvider{}, t.TempDir())
	skillRetriever.mutex.Lock()
	defer skillRetriever.mutex.Unlock()
	searchContext, cancelSearch := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelSearch()
	startedAt := time.Now()
	result := skillRetriever.Search(searchContext, AgentRequest{Prompt: "leave a note"}, []SkillInstruction{{Name: "mattermost", Description: "message"}}, SkillSearchQuerySet{}, 3)
	if result.RetrievalMode != "bm25_fallback" {
		t.Fatalf("expected bm25 fallback while the lock is held, got %+v", result)
	}
	if time.Since(startedAt) > 5*time.Second {
		t.Fatalf("expected fast fallback, took %s", time.Since(startedAt))
	}
}
