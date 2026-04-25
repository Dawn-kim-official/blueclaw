package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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

func TestResolveLanguageModelProviderUsesCapabilityProviderWithoutSecret(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "capability"
	runtimeConfiguration.LanguageModel.Capability.ModelName = "openai/gpt-4.1-mini"

	languageModelProvider := resolveLanguageModelProvider(runtimeConfiguration)
	if _, isCapabilityProvider := languageModelProvider.(llm.CapabilityClient); !isCapabilityProvider {
		t.Fatalf("expected capability provider, got %T", languageModelProvider)
	}
}

func TestNewApplicationRegistersUnifiedConnectorTransports(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()

	application := NewApplication(runtimeConfiguration, "")

	transportNames := strings.Join(application.connectorTransportNames(), ",")
	for _, expectedName := range []string{"mattermost:mattermost-http-webhook", "slack:slack-events-api"} {
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
