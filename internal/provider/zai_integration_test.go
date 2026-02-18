package provider_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/blueclaw/blueclaw/internal/configuration"
)

func TestZAIDirectCall(t *testing.T) {
	config, err := configuration.Load(configuration.ConfigFilePath())
	if err != nil {
		t.Fatalf("loading configuration: %v", err)
	}
	if config.GLMAPIKey == "" {
		t.Skip("no GLM API key configured")
	}

	requestBody, err := json.Marshal(map[string]any{
		"model": "glm-5",
		"messages": []map[string]any{
			{"role": "system", "content": "You are a useful AI assistant."},
			{"role": "user", "content": "Say hello."},
		},
		"stream":      false,
		"temperature": 1,
	})
	if err != nil {
		t.Fatalf("marshaling request: %v", err)
	}

	httpRequest, err := http.NewRequest(http.MethodPost,
		"https://api.z.ai/api/coding/paas/v4/chat/completions",
		bytes.NewReader(requestBody))
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+config.GLMAPIKey)
	httpRequest.Header.Set("Accept-Language", "en-US,en")

	httpResponse, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatalf("sending request: %v", err)
	}
	defer httpResponse.Body.Close()

	responseBody, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}

	t.Logf("status: %d", httpResponse.StatusCode)
	t.Logf("body: %s", string(responseBody))

	if httpResponse.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", httpResponse.StatusCode, string(responseBody))
	}

	var parsed map[string]any
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		t.Fatalf("parsing response: %v", err)
	}
	choices, ok := parsed["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("no choices in response: %s", string(responseBody))
	}
	choice := choices[0].(map[string]any)
	message := choice["message"].(map[string]any)
	fmt.Printf("GLM response: %s\n", message["content"])
}
