package model

import "context"

type EmbeddingProvider interface {
	GenerateEmbedding(ctx context.Context, input string) ([]float32, error)
}
