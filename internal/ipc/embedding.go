package ipc

import (
	"context"
	"fmt"
)

// StdioEmbeddingClient implements memory.EmbeddingGenerator by proxying
// embedding generation requests to the daemon via the stdio transport.
type StdioEmbeddingClient struct {
	transport *StdioTransport
}

func NewStdioEmbeddingClient(transport *StdioTransport) *StdioEmbeddingClient {
	return &StdioEmbeddingClient{transport: transport}
}

func (client *StdioEmbeddingClient) Generate(_ context.Context, text string) ([]float32, error) {
	if err := client.transport.WriteOutbound(StdioOutbound{
		Type:             "embedding_request",
		EmbeddingRequest: &EmbeddingCreate{Text: text},
	}); err != nil {
		return nil, fmt.Errorf("writing embedding request: %w", err)
	}
	inbound, err := client.transport.ReadInbound()
	if err != nil {
		return nil, fmt.Errorf("reading embedding response: %w", err)
	}
	if inbound.ErrorMessage != "" {
		return nil, fmt.Errorf("embedding error: %s", inbound.ErrorMessage)
	}
	if len(inbound.EmbeddingVector) == 0 {
		return nil, fmt.Errorf("empty embedding vector in response")
	}
	return inbound.EmbeddingVector, nil
}
