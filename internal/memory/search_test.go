package memory

import (
	"testing"
)

func TestEnsureVecRegistered_Idempotent(t *testing.T) {
	ensureVecRegistered()
	ensureVecRegistered()
}

func TestCheckDatabaseIntegrity_NewStore(t *testing.T) {
	store := newTestGraphStore(t)
	if err := checkDatabaseIntegrity(store); err != nil {
		t.Errorf("integrity check on new store: %v", err)
	}
}

func makeTestEmbedding(value float32) []float32 {
	embedding := make([]float32, EmbeddingDimension)
	for i := range embedding {
		embedding[i] = value
	}
	return embedding
}
