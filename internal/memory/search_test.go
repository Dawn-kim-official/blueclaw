package memory

import (
	"path/filepath"
	"testing"
)

func TestSearchIndexCreation(t *testing.T) {
	temporaryDirectory := t.TempDir()
	databasePath := filepath.Join(temporaryDirectory, "test.db")
	index, err := NewSearchIndex(databasePath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()
	var journalMode string
	index.database.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if journalMode != "wal" {
		t.Errorf("expected WAL mode, got %q", journalMode)
	}
}

func TestUpsertAndTopK(t *testing.T) {
	temporaryDirectory := t.TempDir()
	databasePath := filepath.Join(temporaryDirectory, "test.db")
	index, err := NewSearchIndex(databasePath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()
	embedding1 := makeTestEmbedding(0.1)
	embedding2 := makeTestEmbedding(0.9)
	if err := index.Upsert("close match", "short-term-memory/close-match.md", "short-term", embedding1); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}
	if err := index.Upsert("far match", "short-term-memory/far-match.md", "short-term", embedding2); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}
	queryEmbedding := makeTestEmbedding(0.1)
	results, err := index.TopK(queryEmbedding, 5)
	if err != nil {
		t.Fatalf("TopK failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Subject != "close match" {
		t.Errorf("expected closest match first, got %q", results[0].Subject)
	}
}

func TestTopKEmptyDatabase(t *testing.T) {
	temporaryDirectory := t.TempDir()
	databasePath := filepath.Join(temporaryDirectory, "test.db")
	index, err := NewSearchIndex(databasePath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()
	results, err := index.TopK(makeTestEmbedding(0.5), 5)
	if err != nil {
		t.Fatalf("TopK failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestTopKRespectsLimit(t *testing.T) {
	temporaryDirectory := t.TempDir()
	databasePath := filepath.Join(temporaryDirectory, "test.db")
	index, err := NewSearchIndex(databasePath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()
	for i := range 5 {
		value := float32(i) * 0.2
		if err := index.Upsert(
			"memory-"+string(rune('A'+i)),
			"short-term-memory/mem.md",
			"short-term",
			makeTestEmbedding(value),
		); err != nil {
			t.Fatalf("Upsert failed: %v", err)
		}
	}
	results, err := index.TopK(makeTestEmbedding(0.0), 2)
	if err != nil {
		t.Fatalf("TopK failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestUpsertUpdatesExisting(t *testing.T) {
	temporaryDirectory := t.TempDir()
	databasePath := filepath.Join(temporaryDirectory, "test.db")
	index, err := NewSearchIndex(databasePath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()
	if err := index.Upsert("test", "old-path.md", "short-term", makeTestEmbedding(0.1)); err != nil {
		t.Fatalf("first Upsert failed: %v", err)
	}
	if err := index.Upsert("test", "new-path.md", "long-term", makeTestEmbedding(0.5)); err != nil {
		t.Fatalf("second Upsert failed: %v", err)
	}
	results, err := index.TopK(makeTestEmbedding(0.5), 10)
	if err != nil {
		t.Fatalf("TopK failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after upsert, got %d", len(results))
	}
	if results[0].Storage != "long-term" {
		t.Errorf("expected updated storage %q, got %q", "long-term", results[0].Storage)
	}
}

func TestUpdateRecallCount(t *testing.T) {
	temporaryDirectory := t.TempDir()
	databasePath := filepath.Join(temporaryDirectory, "test.db")
	index, err := NewSearchIndex(databasePath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()
	index.Upsert("test", "path.md", "short-term", makeTestEmbedding(0.1))
	if err := index.UpdateRecallCount("test", 5); err != nil {
		t.Fatalf("UpdateRecallCount failed: %v", err)
	}
}

func makeTestEmbedding(value float32) []float32 {
	embedding := make([]float32, EmbeddingDimension)
	for i := range embedding {
		embedding[i] = value
	}
	return embedding
}
