package memory

import (
	"os"
	"testing"
	"time"
)

func newTestGraphStore(t *testing.T) *GraphStore {
	t.Helper()
	path := t.TempDir() + "/test_memory.db"
	store, err := NewGraphStore(path)
	if err != nil {
		t.Fatalf("NewGraphStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestNewGraphStore_CreatesSchema(t *testing.T) {
	store := newTestGraphStore(t)
	tables := []string{"memories", "memory_connections", "vec_memories"}
	for _, table := range tables {
		var name string
		err := store.database.QueryRow("SELECT name FROM sqlite_master WHERE type='table' OR type='shadow' AND name=?", table).Scan(&name)
		if err != nil {
			err = store.database.QueryRow("SELECT name FROM sqlite_master WHERE name=?", table).Scan(&name)
		}
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

func TestNewGraphStore_BadPath(t *testing.T) {
	_, err := NewGraphStore("/nonexistent/deeply/nested/path/db.sqlite")
	if err == nil {
		t.Fatal("expected error for unwritable path, got nil")
	}
}

func TestGraphStore_Save_Fact_NoExpiration(t *testing.T) {
	store := newTestGraphStore(t)
	id, err := store.Save("user name", "The user is Lee", MemoryTypeFact, nil)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}
	m, err := store.Load("user name")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Type != MemoryTypeFact {
		t.Errorf("type: got %q, want %q", m.Type, MemoryTypeFact)
	}
	if m.ExpiresAt != nil {
		t.Errorf("fact should have nil expires_at, got %v", m.ExpiresAt)
	}
	if m.Content != "The user is Lee" {
		t.Errorf("content: got %q, want %q", m.Content, "The user is Lee")
	}
}

func TestGraphStore_Save_Episode_HasExpiration(t *testing.T) {
	store := newTestGraphStore(t)
	expiry := time.Now().Add(DefaultEpisodeTTL)
	_, err := store.Save("meeting 2026", "Q1 planning meeting", MemoryTypeEpisode, &expiry)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	m, err := store.Load("meeting 2026")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Type != MemoryTypeEpisode {
		t.Errorf("type: got %q, want %q", m.Type, MemoryTypeEpisode)
	}
	if m.ExpiresAt == nil {
		t.Fatal("episode should have non-nil expires_at")
	}
}

func TestGraphStore_Save_Preference_NoExpiration(t *testing.T) {
	store := newTestGraphStore(t)
	_, err := store.Save("prefers brevity", "User prefers concise answers", MemoryTypePreference, nil)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	m, err := store.Load("prefers brevity")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.ExpiresAt != nil {
		t.Errorf("preference should have nil expires_at, got %v", m.ExpiresAt)
	}
}

func TestGraphStore_Save_DuplicateTitleUpdatesContent(t *testing.T) {
	store := newTestGraphStore(t)
	_, err := store.Save("user name", "First content", MemoryTypeFact, nil)
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}
	_, err = store.Save("user name", "Updated content", MemoryTypeFact, nil)
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	m, err := store.Load("user name")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Content != "Updated content" {
		t.Errorf("content: got %q, want %q", m.Content, "Updated content")
	}
}

func TestGraphStore_Load_NotFound(t *testing.T) {
	store := newTestGraphStore(t)
	_, err := store.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing memory, got nil")
	}
}

func TestGraphStore_Connect_StoresEdge(t *testing.T) {
	store := newTestGraphStore(t)
	idA, _ := store.Save("memory A", "content A", MemoryTypeFact, nil)
	idB, _ := store.Save("memory B", "content B", MemoryTypeFact, nil)
	if err := store.Connect(idA, idB, "extends"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	neighbors, err := store.Neighbors(idA)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 1 || neighbors[0].Title != "memory B" {
		t.Errorf("expected neighbor 'memory B', got %v", neighbors)
	}
}

func TestGraphStore_Connect_ReplacesDuplicatePair(t *testing.T) {
	store := newTestGraphStore(t)
	idA, _ := store.Save("A", "a", MemoryTypeFact, nil)
	idB, _ := store.Save("B", "b", MemoryTypeFact, nil)
	if err := store.Connect(idA, idB, "extends"); err != nil {
		t.Fatalf("first Connect: %v", err)
	}
	if err := store.Connect(idA, idB, "updates"); err != nil {
		t.Fatalf("second Connect: %v", err)
	}
	var relation string
	err := store.database.QueryRow("SELECT relation FROM memory_connections WHERE from_id=? AND to_id=?", idA, idB).Scan(&relation)
	if err != nil {
		t.Fatalf("querying relation: %v", err)
	}
	if relation != "updates" {
		t.Errorf("relation: got %q, want %q", relation, "updates")
	}
}

func TestGraphStore_Connect_SelfLoopRejected(t *testing.T) {
	store := newTestGraphStore(t)
	id, _ := store.Save("A", "a", MemoryTypeFact, nil)
	if err := store.Connect(id, id, "extends"); err == nil {
		t.Fatal("expected error for self-loop, got nil")
	}
}

func TestGraphStore_Neighbors_Bidirectional(t *testing.T) {
	store := newTestGraphStore(t)
	idA, _ := store.Save("A", "a", MemoryTypeFact, nil)
	idB, _ := store.Save("B", "b", MemoryTypeFact, nil)
	store.Connect(idA, idB, "extends")
	neighbors, err := store.Neighbors(idB)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 1 || neighbors[0].Title != "A" {
		t.Errorf("expected neighbor 'A' from reverse direction, got %v", neighbors)
	}
}

func TestGraphStore_Neighbors_Empty(t *testing.T) {
	store := newTestGraphStore(t)
	id, _ := store.Save("isolated", "no connections", MemoryTypeFact, nil)
	neighbors, err := store.Neighbors(id)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 0 {
		t.Errorf("expected 0 neighbors, got %d", len(neighbors))
	}
}

func TestGraphStore_CascadeDelete(t *testing.T) {
	store := newTestGraphStore(t)
	idA, _ := store.Save("A", "a", MemoryTypeFact, nil)
	idB, _ := store.Save("B", "b", MemoryTypeFact, nil)
	store.Connect(idA, idB, "extends")
	if _, err := store.database.Exec("DELETE FROM memories WHERE id=?", idA); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var count int
	store.database.QueryRow("SELECT COUNT(*) FROM memory_connections WHERE from_id=?", idA).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 connections after cascade delete, got %d", count)
	}
}

func TestGraphStore_IncrementRecall(t *testing.T) {
	store := newTestGraphStore(t)
	id, _ := store.Save("recall test", "content", MemoryTypeFact, nil)
	if err := store.IncrementRecall(id); err != nil {
		t.Fatalf("IncrementRecall: %v", err)
	}
	m, _ := store.Load("recall test")
	if m.RecallCount != 1 {
		t.Errorf("recall_count: got %d, want 1", m.RecallCount)
	}
	if m.LastRecalledAt == nil {
		t.Error("last_recalled_at should be set after recall")
	}
}

func TestGraphStore_ExtendExpiration(t *testing.T) {
	store := newTestGraphStore(t)
	expiry := time.Now().Add(time.Hour)
	id, _ := store.Save("ep", "content", MemoryTypeEpisode, &expiry)
	if err := store.ExtendExpiration(id, 7*24*time.Hour); err != nil {
		t.Fatalf("ExtendExpiration: %v", err)
	}
	m, _ := store.Load("ep")
	if m.ExpiresAt == nil {
		t.Fatal("expires_at should not be nil after extension")
	}
	if m.ExpiresAt.Before(time.Now().Add(6 * 24 * time.Hour)) {
		t.Errorf("expected expires_at > now+6d, got %v", m.ExpiresAt)
	}
}

func TestGraphStore_Promote_ClearsExpiration(t *testing.T) {
	store := newTestGraphStore(t)
	expiry := time.Now().Add(time.Hour)
	id, _ := store.Save("episode", "content", MemoryTypeEpisode, &expiry)
	if err := store.Promote(id); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	m, _ := store.Load("episode")
	if m.ExpiresAt != nil {
		t.Errorf("expires_at should be nil after promotion, got %v", m.ExpiresAt)
	}
}

func TestGraphStore_CleanupExpired_DeletesExpiredEpisode(t *testing.T) {
	store := newTestGraphStore(t)
	pastExpiry := time.Now().Add(-time.Hour)
	store.Save("old episode", "content", MemoryTypeEpisode, &pastExpiry)
	if err := store.CleanupExpired(); err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	_, err := store.Load("old episode")
	if err == nil {
		t.Error("expired episode should have been deleted")
	}
}

func TestGraphStore_CleanupExpired_KeepsFuture(t *testing.T) {
	store := newTestGraphStore(t)
	futureExpiry := time.Now().Add(24 * time.Hour)
	store.Save("future episode", "content", MemoryTypeEpisode, &futureExpiry)
	if err := store.CleanupExpired(); err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	_, err := store.Load("future episode")
	if err != nil {
		t.Errorf("future episode should not be deleted: %v", err)
	}
}

func TestGraphStore_CleanupExpired_NeverDeletesFact(t *testing.T) {
	store := newTestGraphStore(t)
	store.Save("permanent fact", "content", MemoryTypeFact, nil)
	if err := store.CleanupExpired(); err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	_, err := store.Load("permanent fact")
	if err != nil {
		t.Errorf("fact should not be deleted by cleanup: %v", err)
	}
}

func TestGraphStore_SaveEmbedding_And_TopK(t *testing.T) {
	store := newTestGraphStore(t)
	id, err := store.Save("embedding test", "content", MemoryTypeFact, nil)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	embedding := make([]float32, EmbeddingDimension)
	for i := range embedding {
		embedding[i] = float32(i) / float32(EmbeddingDimension)
	}
	if err := store.SaveEmbedding(id, embedding); err != nil {
		t.Fatalf("SaveEmbedding: %v", err)
	}
	results, err := store.TopK(embedding, 5)
	if err != nil {
		t.Fatalf("TopK: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one TopK result")
	}
	if results[0].Title != "embedding test" {
		t.Errorf("top result: got %q, want %q", results[0].Title, "embedding test")
	}
}

func TestGraphStore_SaveEmbedding_UpsertReplaces(t *testing.T) {
	store := newTestGraphStore(t)
	id, _ := store.Save("upd", "c", MemoryTypeFact, nil)
	embedding1 := make([]float32, EmbeddingDimension)
	embedding2 := make([]float32, EmbeddingDimension)
	for i := range embedding2 {
		embedding2[i] = 1.0
	}
	if err := store.SaveEmbedding(id, embedding1); err != nil {
		t.Fatalf("first SaveEmbedding: %v", err)
	}
	if err := store.SaveEmbedding(id, embedding2); err != nil {
		t.Fatalf("second SaveEmbedding: %v", err)
	}
}

func TestGraphStore_EnsureDBDirectory(t *testing.T) {
	dir := t.TempDir() + "/nested/deep"
	path := dir + "/memory.db"
	store, err := NewGraphStore(path)
	if err != nil {
		t.Fatalf("NewGraphStore with nested path: %v", err)
	}
	store.Close()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("database file not created: %v", err)
	}
}
