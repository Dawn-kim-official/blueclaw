package llm

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLiteRTLMBuildStructuredRequestDocumentUsesConstrainedDecoding(t *testing.T) {
	liteRTLMClient := LiteRTLMClient{
		WrapperPath:       "/usr/local/bin/blueclaw-litert-wrapper",
		ModelPath:         "/models/gemma-4-E4B-it.litertlm",
		BackendPreference: []string{"gpu"},
	}

	requestDocument, errorValue := liteRTLMClient.BuildStructuredRequestDocument(
		StructuredResponseRequest{
			Messages: []Message{
				{
					Role:    "user",
					Content: "extract payroll policy",
				},
			},
			StructuredOutputSchema: StructuredOutputSchema{
				Name:               "policy_result",
				Document:           `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`,
				IsStrictlyEnforced: true,
			},
		},
	)
	if errorValue != nil {
		t.Fatalf("expected wrapper request document to build: %v", errorValue)
	}

	var parsedDocument map[string]json.RawMessage
	errorValue = json.Unmarshal(requestDocument, &parsedDocument)
	if errorValue != nil {
		t.Fatalf("expected wrapper request document to be valid json: %v", errorValue)
	}

	if string(parsedDocument["enableConstrainedDecoding"]) != "true" {
		t.Fatalf("expected constrained decoding to be enabled, got %s", string(parsedDocument["enableConstrainedDecoding"]))
	}
	if string(parsedDocument["constraintProvider"]) != `"llguidance"` {
		t.Fatalf("expected llguidance constraint provider, got %s", string(parsedDocument["constraintProvider"]))
	}
	if string(parsedDocument["backendPreference"]) != `["gpu"]` {
		t.Fatalf("expected gpu backend preference, got %s", string(parsedDocument["backendPreference"]))
	}
	if string(parsedDocument["allowCPUFallback"]) != `false` {
		t.Fatalf("expected cpu fallback to be disabled, got %s", string(parsedDocument["allowCPUFallback"]))
	}
}

func TestLiteRTLMBuildStructuredRequestDocumentDefaultsToAcceleratedBackendPreference(t *testing.T) {
	liteRTLMClient := LiteRTLMClient{
		WrapperPath: "/usr/local/bin/blueclaw-litert-wrapper",
		ModelPath:   "/models/gemma-4-E4B-it.litertlm",
	}

	requestDocument, errorValue := liteRTLMClient.BuildStructuredRequestDocument(buildTestStructuredResponseRequest())
	if errorValue != nil {
		t.Fatalf("expected wrapper request document to build: %v", errorValue)
	}

	var parsedDocument map[string]json.RawMessage
	errorValue = json.Unmarshal(requestDocument, &parsedDocument)
	if errorValue != nil {
		t.Fatalf("expected wrapper request document to be valid json: %v", errorValue)
	}

	if string(parsedDocument["backendPreference"]) != `["gpu","cpu"]` {
		t.Fatalf("expected gpu then cpu backend preference, got %s", string(parsedDocument["backendPreference"]))
	}
	if string(parsedDocument["allowCPUFallback"]) != `false` {
		t.Fatalf("expected cpu fallback to require explicit opt-in, got %s", string(parsedDocument["allowCPUFallback"]))
	}
}

func TestLiteRTLMRejectsCPUBackendWhenCPUFallbackIsDisabled(t *testing.T) {
	liteRTLMClient := LiteRTLMClient{
		WrapperPath:       "/usr/local/bin/blueclaw-litert-wrapper",
		ModelPath:         "/models/gemma-4-E4B-it.litertlm",
		BackendPreference: []string{"gpu", "cpu"},
		AllowCPUFallback:  false,
		CommandExecutor:   staticLiteRTLMWrapperResponse(`{"content":"{\"reply\":\"hello\"}","selectedBackend":"cpu"}`),
	}

	_, errorValue := liteRTLMClient.GenerateStructuredResponse(context.Background(), buildTestStructuredResponseRequest())
	if errorValue == nil {
		t.Fatal("expected cpu fallback to be rejected")
	}
}

func TestLiteRTLMAcceptsCPUBackendWhenCPUFallbackIsExplicitlyEnabled(t *testing.T) {
	liteRTLMClient := LiteRTLMClient{
		WrapperPath:       "/usr/local/bin/blueclaw-litert-wrapper",
		ModelPath:         "/models/gemma-4-E4B-it.litertlm",
		BackendPreference: []string{"gpu", "cpu"},
		AllowCPUFallback:  true,
		CommandExecutor:   staticLiteRTLMWrapperResponse(`{"content":"{\"reply\":\"hello\"}","selectedBackend":"cpu"}`),
	}

	structuredResponse, errorValue := liteRTLMClient.GenerateStructuredResponse(context.Background(), buildTestStructuredResponseRequest())
	if errorValue != nil {
		t.Fatalf("expected explicit cpu fallback to be accepted: %v", errorValue)
	}
	if structuredResponse.Content != `{"reply":"hello"}` {
		t.Fatalf("expected wrapper content to be returned, got %q", structuredResponse.Content)
	}
}

func TestLiteRTLMRejectsUnreportedSelectedBackend(t *testing.T) {
	liteRTLMClient := LiteRTLMClient{
		WrapperPath:     "/usr/local/bin/blueclaw-litert-wrapper",
		ModelPath:       "/models/gemma-4-E4B-it.litertlm",
		CommandExecutor: staticLiteRTLMWrapperResponse(`{"content":"{\"reply\":\"hello\"}"}`),
	}

	_, errorValue := liteRTLMClient.GenerateStructuredResponse(context.Background(), buildTestStructuredResponseRequest())
	if errorValue == nil {
		t.Fatal("expected missing selected backend to be rejected")
	}
}

func TestLiteRTLMReturnsWrapperErrorDocument(t *testing.T) {
	liteRTLMClient := LiteRTLMClient{
		WrapperPath:     "/usr/local/bin/blueclaw-litert-wrapper",
		ModelPath:       "/models/gemma-4-E4B-it.litertlm",
		CommandExecutor: staticLiteRTLMWrapperResponse(`{"error":"gpu unavailable"}`),
	}

	_, errorValue := liteRTLMClient.GenerateStructuredResponse(context.Background(), buildTestStructuredResponseRequest())
	if errorValue == nil {
		t.Fatal("expected wrapper error to be returned")
	}
	if errorValue.Error() != "gpu unavailable" {
		t.Fatalf("expected wrapper error to match, got %q", errorValue.Error())
	}
}

func TestLiteRTLMJetsonGPUAcceptance(t *testing.T) {
	wrapperPath := os.Getenv("BLUECLAW_TEST_LITERT_WRAPPER_PATH")
	modelPath := os.Getenv("BLUECLAW_TEST_LITERT_MODEL_PATH")
	if wrapperPath == "" || modelPath == "" {
		t.Skip("set BLUECLAW_TEST_LITERT_WRAPPER_PATH and BLUECLAW_TEST_LITERT_MODEL_PATH to run LiteRT-LM acceptance")
	}

	liteRTLMClient := LiteRTLMClient{
		WrapperPath:       wrapperPath,
		WrapperArguments:  litertAcceptanceWrapperArguments(),
		ModelPath:         modelPath,
		BackendPreference: litertAcceptanceBackendPreference(),
		AllowCPUFallback:  os.Getenv("BLUECLAW_TEST_LITERT_ALLOW_CPU_FALLBACK") == "true",
	}

	structuredResponse, errorValue := liteRTLMClient.GenerateStructuredResponse(context.Background(), buildTestStructuredResponseRequest())
	if errorValue != nil {
		t.Fatalf("expected Jetson GPU LiteRT-LM acceptance to succeed: %v", errorValue)
	}
	if !json.Valid([]byte(structuredResponse.Content)) {
		t.Fatalf("expected constrained decoding to return valid json, got %q", structuredResponse.Content)
	}
}

func litertAcceptanceWrapperArguments() []string {
	arguments := strings.TrimSpace(os.Getenv("BLUECLAW_TEST_LITERT_WRAPPER_ARGUMENTS"))
	if arguments == "" {
		return []string{"--stdio"}
	}
	return strings.Fields(arguments)
}

func litertAcceptanceBackendPreference() []string {
	backendPreference := strings.TrimSpace(os.Getenv("BLUECLAW_TEST_LITERT_BACKEND_PREFERENCE"))
	if backendPreference == "" {
		return []string{"gpu"}
	}
	return strings.Split(backendPreference, ",")
}

func buildTestStructuredResponseRequest() StructuredResponseRequest {
	return StructuredResponseRequest{
		Messages: []Message{
			{
				Role:    "user",
				Content: "say hello",
			},
		},
		StructuredOutputSchema: StructuredOutputSchema{
			Name:               "reply",
			Document:           `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	}
}

func staticLiteRTLMWrapperResponse(responseDocument string) WrapperCommandExecutor {
	return func(responseContext context.Context, executablePath string, arguments []string, standardInput []byte) ([]byte, error) {
		_ = responseContext
		_ = executablePath
		_ = arguments
		_ = standardInput
		return []byte(responseDocument), nil
	}
}
