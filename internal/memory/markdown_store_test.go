package memory

import (
	"context"
	"strings"
	"testing"
)

func TestMarkdownStoreLoadsPersonMemory(t *testing.T) {
	store := NewMarkdownStore(t.TempDir(), 1200)
	if _, errorValue := store.MergePersonMemory(context.Background(), "person-1", "Call the user master."); errorValue != nil {
		t.Fatal(errorValue)
	}

	memoryFacts, errorValue := store.LoadPinnedMemory(context.Background(), "person-1")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(memoryFacts) != 1 {
		t.Fatalf("expected person memory, got %+v", memoryFacts)
	}
	if memoryFacts[0].SourceKind != MemorySourceKindPinned {
		t.Fatalf("expected pinned memory source kind, got %+v", memoryFacts)
	}
}

func TestMarkdownStoreCompressesOverHardLimit(t *testing.T) {
	store := NewMarkdownStore(t.TempDir(), 80)
	store.UseCompressor(staticMarkdownMemoryCompressor{content: "# Memory\n- Compressed durable preference."}, 40)
	for _, content := range []string{
		"First durable preference with several words.",
		"Second durable preference with several words.",
		"Third durable preference with several words.",
	} {
		if _, errorValue := store.MergePersonMemory(context.Background(), "person-1", content); errorValue != nil {
			t.Fatal(errorValue)
		}
	}

	memoryFacts, errorValue := store.LoadPinnedMemory(context.Background(), "person-1")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(memoryFacts) != 1 {
		t.Fatalf("expected one person memory fact, got %+v", memoryFacts)
	}
	if len([]rune(memoryFacts[0].Content)) > 80 {
		t.Fatalf("expected compacted memory under limit, got %q", memoryFacts[0].Content)
	}
	if !strings.Contains(memoryFacts[0].Content, "Compressed durable") {
		t.Fatalf("expected compressed memory, got %q", memoryFacts[0].Content)
	}
}

func TestMemoryUpdateProcessorUpdatesGraphitiAndMarkdown(t *testing.T) {
	graphStore := &recordingGraphStore{}
	memoryService := &MemoryService{}
	memoryService.UseGraphStore(graphStore)
	markdownStore := NewMarkdownStore(t.TempDir(), 1200)
	processor := NewMemoryUpdateProcessor(memoryService, markdownStore)

	result := processor.Process(context.Background(), MemoryUpdateJob{
		Namespace:      UserNamespace("person-1"),
		Content:        "Call the user master.",
		SenderPersonID: "person-1",
	})

	if !result.GraphitiSucceeded || result.GraphitiError != "" {
		t.Fatalf("expected graph update success, got %+v", result)
	}
	if !result.MarkdownUpdated || result.MarkdownError != "" {
		t.Fatalf("expected markdown update success, got %+v", result)
	}
	if len(graphStore.episodes) != 1 || graphStore.episodes[0].Prompt != "Call the user master." {
		t.Fatalf("expected graph episode, got %+v", graphStore.episodes)
	}
	memoryFacts, errorValue := markdownStore.LoadPinnedMemory(context.Background(), "person-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(memoryFacts) != 1 || !strings.Contains(memoryFacts[0].Content, "master") {
		t.Fatalf("expected markdown memory, got %+v", memoryFacts)
	}
}

func TestMemoryUpdateProcessorSkipsMarkdownForCircleMemory(t *testing.T) {
	graphStore := &recordingGraphStore{}
	memoryService := &MemoryService{}
	memoryService.UseGraphStore(graphStore)
	markdownStore := NewMarkdownStore(t.TempDir(), 1200)
	processor := NewMemoryUpdateProcessor(memoryService, markdownStore)

	result := processor.Process(context.Background(), MemoryUpdateJob{
		Namespace:      CircleNamespace("default", "staff"),
		Content:        "Staff prefers concise updates.",
		SenderPersonID: "person-1",
	})

	if !result.GraphitiSucceeded || result.GraphitiError != "" {
		t.Fatalf("expected graph update success, got %+v", result)
	}
	if result.MarkdownUpdated || result.MarkdownError != "" {
		t.Fatalf("expected markdown to be skipped, got %+v", result)
	}
}

type staticMarkdownMemoryCompressor struct {
	content string
}

func (compressor staticMarkdownMemoryCompressor) CompressMemory(context.Context, MarkdownMemoryCompressionRequest) (string, error) {
	return compressor.content, nil
}

type recordingGraphStore struct {
	episodes []MemoryEpisode
}

func (store *recordingGraphStore) AddEpisode(_ context.Context, episode MemoryEpisode) (MemoryIngestionResult, error) {
	store.episodes = append(store.episodes, episode)
	return MemoryIngestionResult{EpisodeID: episode.EpisodeID, NamespaceCount: len(episode.Namespaces)}, nil
}

func (store *recordingGraphStore) SearchFacts(context.Context, MemorySearchRequest) ([]MemoryFact, error) {
	return nil, nil
}
