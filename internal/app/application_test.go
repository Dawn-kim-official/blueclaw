package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"blueclaw/internal/config"
	"blueclaw/internal/llm"
)

func TestResolveLanguageModelProviderDefaultsToCapabilityLLM(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "must-not-be-read")
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.Capability.Model = "gemma-4-E4B-it"

	languageModelProvider := resolveLanguageModelProvider(runtimeConfiguration)
	if languageModelProvider == nil {
		t.Fatal("expected capability provider to be inferred")
	}
	capabilityLLMClient, isCapabilityProvider := languageModelProvider.(llm.CapabilityLLMClient)
	if !isCapabilityProvider {
		t.Fatalf("expected capability provider, got %T", languageModelProvider)
	}
	if capabilityLLMClient.ModelName != "gemma-4-E4B-it" {
		t.Fatalf("expected capability model, got %q", capabilityLLMClient.ModelName)
	}
}

func TestResolveIntakeLanguageModelProviderUsesAutomaticCapabilityModel(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Agent.Intake.Enabled = true
	runtimeConfiguration.Agent.Intake.Model = "local/gemma-4-E4B-it-litert-lm"
	runtimeConfiguration.Agent.Intake.ExecutionMode = "auto"

	languageModelProvider := resolveIntakeLanguageModelProvider(runtimeConfiguration, newCapabilityClient(runtimeConfiguration))
	capabilityLLMClient, isCapabilityProvider := languageModelProvider.(llm.CapabilityLLMClient)
	if !isCapabilityProvider {
		t.Fatalf("expected capability intake provider, got %T", languageModelProvider)
	}
	if capabilityLLMClient.ModelName != "local/gemma-4-E4B-it-litert-lm" {
		t.Fatalf("expected local intake model, got %q", capabilityLLMClient.ModelName)
	}
	if capabilityLLMClient.ExecutionMode != "auto" {
		t.Fatalf("expected automatic intake execution mode, got %q", capabilityLLMClient.ExecutionMode)
	}
}

func TestNewApplicationRegistersSecretlessConnectorTransports(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()

	application := NewApplication(runtimeConfiguration, "")

	transportNames := strings.Join(application.connectorTransportNames(), ",")
	for _, expectedName := range []string{"mattermost:mattermost-internal-ingress", "slack:slack-internal-ingress", "signal:signal-internal-ingress"} {
		if !strings.Contains(transportNames, expectedName) {
			t.Fatalf("expected transport %q in %q", expectedName, transportNames)
		}
	}
	if strings.Contains(transportNames, "websocket") {
		t.Fatalf("expected no platform-owned websocket transport, got %q", transportNames)
	}
}

func TestNewApplicationAllowsSignalInternalIngress(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	runtimeConfiguration.Connectors.Signal.Enabled = true

	application := NewApplication(runtimeConfiguration, "")

	if application.startupError != nil {
		t.Fatalf("expected signal internal ingress to be allowed: %v", application.startupError)
	}
}

func TestApplicationConnectorRouteAcceptsNormalizedSlackEvent(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	application := NewApplication(runtimeConfiguration, "")

	payload := []byte(`{}`)
	request := httptest.NewRequest(http.MethodPost, "/connectors/slack/events", bytes.NewReader(payload))
	responseRecorder := httptest.NewRecorder()
	application.httpServer.Handler.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected normalized event status ok, got %d", responseRecorder.Code)
	}
	var responseDocument map[string]any
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &responseDocument); errorValue != nil {
		t.Fatalf("expected response document: %v", errorValue)
	}
	if responseDocument["platform"] != "slack" {
		t.Fatalf("expected slack platform response, got %+v", responseDocument)
	}
	if responseDocument["reason"] != "no_event" {
		t.Fatalf("expected no_event response, got %+v", responseDocument)
	}
}

func TestApplicationRegistersSignalHTTPRoute(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	application := NewApplication(runtimeConfiguration, "")

	payload := []byte(`{}`)
	request := httptest.NewRequest(http.MethodPost, "/connectors/signal/events", bytes.NewReader(payload))
	responseRecorder := httptest.NewRecorder()
	application.httpServer.Handler.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected signal normalized event status ok, got %d", responseRecorder.Code)
	}
}
