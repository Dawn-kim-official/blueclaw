package llm

import "context"

type GeminiEmbeddingClient struct{}

func (geminiEmbeddingClient GeminiEmbeddingClient) GenerateEmbedding(ctx context.Context, input string) ([]float32, error) {
	_ = ctx
	embedding := make([]float32, 768)
	if len(input) > 0 {
		embedding[0] = float32(len(input))
	}
	return embedding, nil
}
