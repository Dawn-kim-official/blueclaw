package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueclaw/blueclaw/internal/memory"
)

func TestRecallTool_MissingQuery(t *testing.T) {
	tool := NewRecallTool(newTestGraphStore(t), nil, 5)
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for missing query")
	}
}

func TestRecallTool_NoMatches_ReturnsEmpty(t *testing.T) {
	tool := NewRecallTool(newTestGraphStore(t), nil, 5)
	result, err := tool.Execute(context.Background(), map[string]any{"query": "anything"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	var output map[string]any
	json.Unmarshal([]byte(result.Output), &output)
	memories, _ := output["memories"].([]any)
	if len(memories) != 0 {
		t.Errorf("expected 0 memories, got %d", len(memories))
	}
}

func TestRecallTool_NeighborIncluded(t *testing.T) {
	graphStore := newTestGraphStore(t)
	embed := &fixedEmbedding{value: 0.5}
	rememberTool := NewRememberTool(graphStore, embed)
	rememberTool.Execute(context.Background(), map[string]any{"title": "A", "content": "content A", "type": "fact"})
	rememberTool.Execute(context.Background(), map[string]any{"title": "B", "content": "content B", "type": "fact"})
	memA, _ := graphStore.Load("A")
	memB, _ := graphStore.Load("B")
	graphStore.Connect(memA.ID, memB.ID, "extends")
	recallTool := NewRecallTool(graphStore, embed, 5)
	result, err := recallTool.Execute(context.Background(), map[string]any{"query": "content A"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	titles := extractTitles(t, result.Output)
	if !titleInSlice(titles,"A") {
		t.Errorf("expected 'A' in results, got %v", titles)
	}
	if !titleInSlice(titles,"B") {
		t.Errorf("expected neighbor 'B' in results, got %v", titles)
	}
}

func TestRecallTool_SourceField(t *testing.T) {
	graphStore := newTestGraphStore(t)
	embed := &fixedEmbedding{value: 0.5}
	rememberTool := NewRememberTool(graphStore, embed)
	rememberTool.Execute(context.Background(), map[string]any{"title": "main", "content": "main content", "type": "fact"})
	rememberTool.Execute(context.Background(), map[string]any{"title": "related", "content": "related content", "type": "fact"})
	main, _ := graphStore.Load("main")
	related, _ := graphStore.Load("related")
	graphStore.Connect(main.ID, related.ID, "extends")
	recallTool := NewRecallTool(graphStore, embed, 5)
	result, _ := recallTool.Execute(context.Background(), map[string]any{"query": "main content"})
	sources := extractSources(t, result.Output)
	if !sourceMatches(sources,"main", "search") && !sourceMatches(sources,"main", "neighbor") {
		t.Errorf("'main' should appear in results, sources: %v", sources)
	}
	if !sourceMatches(sources,"related", "neighbor") && !sourceMatches(sources,"related", "search") {
		t.Errorf("'related' should appear in results, sources: %v", sources)
	}
}

func TestRecallTool_BidirectionalNeighbor(t *testing.T) {
	graphStore := newTestGraphStore(t)
	embed := &fixedEmbedding{value: 0.5}
	rememberTool := NewRememberTool(graphStore, embed)
	rememberTool.Execute(context.Background(), map[string]any{"title": "C", "content": "content C", "type": "fact"})
	rememberTool.Execute(context.Background(), map[string]any{"title": "D", "content": "content D", "type": "fact"})
	memC, _ := graphStore.Load("C")
	memD, _ := graphStore.Load("D")
	graphStore.Connect(memC.ID, memD.ID, "extends")
	recallTool := NewRecallTool(graphStore, embed, 5)
	result, _ := recallTool.Execute(context.Background(), map[string]any{"query": "content D"})
	titles := extractTitles(t, result.Output)
	if !titleInSlice(titles,"C") {
		t.Errorf("reverse neighbor 'C' should appear when searching D, got %v", titles)
	}
}

func TestRecallTool_Deduplication(t *testing.T) {
	graphStore := newTestGraphStore(t)
	embed := &fixedEmbedding{value: 0.5}
	rememberTool := NewRememberTool(graphStore, embed)
	rememberTool.Execute(context.Background(), map[string]any{"title": "E", "content": "content E", "type": "fact"})
	rememberTool.Execute(context.Background(), map[string]any{"title": "F", "content": "content F", "type": "fact"})
	memE, _ := graphStore.Load("E")
	memF, _ := graphStore.Load("F")
	graphStore.Connect(memE.ID, memF.ID, "extends")
	graphStore.Connect(memF.ID, memE.ID, "extends")
	recallTool := NewRecallTool(graphStore, embed, 5)
	result, _ := recallTool.Execute(context.Background(), map[string]any{"query": "content"})
	titles := extractTitles(t, result.Output)
	count := 0
	for _, title := range titles {
		if title == "E" {
			count++
		}
	}
	if count > 1 {
		t.Errorf("'E' appeared %d times, expected at most 1 (deduplication)", count)
	}
}

func TestRecallTool_EpisodePromotedAfterThreeRecalls(t *testing.T) {
	graphStore := newTestGraphStore(t)
	embed := &fixedEmbedding{value: 0.5}
	rememberTool := NewRememberTool(graphStore, embed)
	rememberTool.Execute(context.Background(), map[string]any{"title": "ep", "content": "episode", "type": "episode"})
	recallTool := NewRecallTool(graphStore, embed, 5)
	for range 3 {
		recallTool.Execute(context.Background(), map[string]any{"query": "episode"})
	}
	m, _ := graphStore.Load("ep")
	if m.ExpiresAt != nil {
		t.Errorf("episode recalled 3x should be promoted (nil expires_at), got %v", m.ExpiresAt)
	}
}

func extractTitles(t *testing.T, output string) []string {
	t.Helper()
	var result map[string][]map[string]any
	json.Unmarshal([]byte(output), &result)
	var titles []string
	for _, m := range result["memories"] {
		if title, ok := m["title"].(string); ok {
			titles = append(titles, title)
		}
	}
	return titles
}

func extractSources(t *testing.T, output string) map[string]string {
	t.Helper()
	var result map[string][]map[string]any
	json.Unmarshal([]byte(output), &result)
	sources := make(map[string]string)
	for _, m := range result["memories"] {
		title, _ := m["title"].(string)
		source, _ := m["source"].(string)
		sources[title] = source
	}
	return sources
}

func titleInSlice(slice []string, value string) bool {
	for _, s := range slice {
		if s == value {
			return true
		}
	}
	return false
}

func sourceMatches(sources map[string]string, title, source string) bool {
	return sources[title] == source
}

type fixedEmbedding struct{ value float32 }

func (e *fixedEmbedding) Generate(_ context.Context, _ string) ([]float32, error) {
	embedding := make([]float32, memory.EmbeddingDimension)
	for i := range embedding {
		embedding[i] = e.value
	}
	return embedding, nil
}
