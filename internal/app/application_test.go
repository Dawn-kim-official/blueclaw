package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueclaw/internal/config"
	"blueclaw/internal/llm"
)

func TestResolveLanguageModelProviderDefaultsToOpenRouterWhenConfigured(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.OpenRouter.ModelName = "openai/gpt-4.1-mini"

	languageModelProvider := resolveLanguageModelProvider(runtimeConfiguration)
	if languageModelProvider == nil {
		t.Fatal("expected openrouter provider to be inferred from configuration")
	}
	if _, isOpenRouterProvider := languageModelProvider.(llm.OpenRouterClient); !isOpenRouterProvider {
		t.Fatalf("expected openrouter provider, got %T", languageModelProvider)
	}
}

func TestResolveLanguageModelProviderReadsOpenRouterAPIKeyPath(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "openrouter-api-key")
	if errorValue := os.WriteFile(secretPath, []byte("file-secret\n"), 0o600); errorValue != nil {
		t.Fatalf("expected secret file to be written: %v", errorValue)
	}
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.OpenRouter.ModelName = "openai/gpt-4.1-mini"
	runtimeConfiguration.LanguageModel.OpenRouter.APIKeyPath = secretPath

	languageModelProvider := resolveLanguageModelProvider(runtimeConfiguration)
	openRouterClient, isOpenRouterProvider := languageModelProvider.(llm.OpenRouterClient)
	if !isOpenRouterProvider {
		t.Fatalf("expected openrouter provider, got %T", languageModelProvider)
	}
	if openRouterClient.APIKey != "file-secret" {
		t.Fatalf("expected api key from file, got %q", openRouterClient.APIKey)
	}
}

func TestNewApplicationRegistersUnifiedConnectorTransports(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()

	application := NewApplication(runtimeConfiguration, "")

	transportNames := strings.Join(application.connectorTransportNames(), ",")
	for _, expectedName := range []string{"mattermost:mattermost-http-webhook", "slack:slack-events-api", "mattermost:mattermost-websocket"} {
		if !strings.Contains(transportNames, expectedName) {
			t.Fatalf("expected transport %q in %q", expectedName, transportNames)
		}
	}
}

func TestNewApplicationRejectsSignalProductionEnable(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	runtimeConfiguration.Connectors.Signal.Enabled = true

	application := NewApplication(runtimeConfiguration, "")

	if application.startupError == nil {
		t.Fatal("expected signal enabled configuration to fail startup")
	}
}

func TestApplicationSlackHTTPRouteHandlesURLVerification(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	application := NewApplication(runtimeConfiguration, "")

	request := httptest.NewRequest(http.MethodPost, "/connectors/slack/events", bytes.NewReader([]byte(`{"type":"url_verification","challenge":"blueclaw-challenge"}`)))
	responseRecorder := httptest.NewRecorder()
	application.httpServer.Handler.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected slack challenge status ok, got %d", responseRecorder.Code)
	}
	if responseRecorder.Body.String() != "blueclaw-challenge" {
		t.Fatalf("expected challenge response, got %q", responseRecorder.Body.String())
	}
}

func TestResolveLanguageModelProviderAddsLiteRTLMFallbackWhenConfigured(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.OpenRouter.ModelName = "openai/gpt-4.1-mini"
	runtimeConfiguration.LanguageModel.LiteRTLM.WrapperPath = "/usr/local/bin/blueclaw-litert-wrapper"

	languageModelProvider := resolveLanguageModelProvider(runtimeConfiguration)
	if languageModelProvider == nil {
		t.Fatal("expected language model provider to be inferred from configuration")
	}
	if _, isFallbackProvider := languageModelProvider.(llm.FallbackLanguageModelProvider); !isFallbackProvider {
		t.Fatalf("expected fallback language model provider, got %T", languageModelProvider)
	}
}
