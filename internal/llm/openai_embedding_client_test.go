package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIEmbeddingClientUsesConfiguredModelWithoutTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		var requestDocument openAIEmbeddingRequest
		if errorValue := json.NewDecoder(request.Body).Decode(&requestDocument); errorValue != nil {
			t.Fatalf("decode request: %v", errorValue)
		}
		if requestDocument.Model != DefaultEmbeddingModelName || requestDocument.Input != "task search" {
			t.Fatalf("request = %+v", requestDocument)
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer server.Close()

	client := OpenAIEmbeddingClient{Endpoint: server.URL, ModelName: DefaultEmbeddingModelName}
	embedding, errorValue := client.GenerateEmbedding(context.Background(), "task search")

	if errorValue != nil {
		t.Fatalf("generate embedding: %v", errorValue)
	}
	if len(embedding) != 3 {
		t.Fatalf("embedding = %+v", embedding)
	}
	if client.httpClient().Timeout != 0 {
		t.Fatalf("expected no default provider timeout, got %s", client.httpClient().Timeout)
	}
}
