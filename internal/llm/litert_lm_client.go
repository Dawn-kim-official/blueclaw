package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type WrapperCommandExecutor func(context.Context, string, []string, []byte) ([]byte, error)

type LiteRTLMClient struct {
	WrapperPath        string
	WrapperArguments   []string
	ModelPath          string
	BackendPreference  []string
	AllowCPUFallback   bool
	ConstraintProvider string
	CommandExecutor    WrapperCommandExecutor
}

type liteRTLMWrapperRequestDocument struct {
	ModelPath                 string                                  `json:"modelPath"`
	BackendPreference         []string                                `json:"backendPreference"`
	AllowCPUFallback          bool                                    `json:"allowCPUFallback"`
	Messages                  []Message                               `json:"messages"`
	EnableConstrainedDecoding bool                                    `json:"enableConstrainedDecoding"`
	ConstraintProvider        string                                  `json:"constraintProvider"`
	DecodingConstraint        liteRTLMDecodingConstraintConfiguration `json:"decodingConstraint"`
}

type liteRTLMDecodingConstraintConfiguration struct {
	Type             string `json:"type"`
	ConstraintString string `json:"constraintString"`
}

type liteRTLMWrapperResponseDocument struct {
	Content         string `json:"content"`
	Error           string `json:"error"`
	SelectedBackend string `json:"selectedBackend"`
}

func (liteRTLMClient LiteRTLMClient) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	structuredResponse, errorValue := liteRTLMClient.GenerateStructuredResponse(
		responseContext,
		StructuredResponseRequest{
			Messages: []Message{
				{
					Role:    "user",
					Content: prompt,
				},
			},
			StructuredOutputSchema: StructuredOutputSchema{
				Name:               "plain_text_response",
				Document:           `{"type":"object","properties":{"content":{"type":"string"}},"required":["content"],"additionalProperties":false}`,
				IsStrictlyEnforced: true,
			},
		},
	)
	if errorValue != nil {
		return "", errorValue
	}

	return structuredResponse.Content, nil
}

func (liteRTLMClient LiteRTLMClient) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (StructuredResponse, error) {
	if liteRTLMClient.WrapperPath == "" {
		return StructuredResponse{}, errors.New("litert-lm wrapper path is not configured")
	}

	requestDocument, errorValue := liteRTLMClient.BuildStructuredRequestDocument(structuredResponseRequest)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	commandExecutor := liteRTLMClient.CommandExecutor
	if commandExecutor == nil {
		commandExecutor = defaultWrapperCommandExecutor
	}

	responseDocument, errorValue := commandExecutor(
		responseContext,
		liteRTLMClient.WrapperPath,
		append([]string{}, liteRTLMClient.WrapperArguments...),
		requestDocument,
	)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	var parsedResponse liteRTLMWrapperResponseDocument
	errorValue = json.Unmarshal(responseDocument, &parsedResponse)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}
	if strings.TrimSpace(parsedResponse.Error) != "" {
		return StructuredResponse{}, errors.New(parsedResponse.Error)
	}
	errorValue = liteRTLMClient.validateSelectedBackend(parsedResponse.SelectedBackend)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	return StructuredResponse{
		ProviderName: "litert-lm",
		ModelName:    liteRTLMClient.ModelPath,
		Content:      parsedResponse.Content,
	}, nil
}

func (liteRTLMClient LiteRTLMClient) BuildStructuredRequestDocument(structuredResponseRequest StructuredResponseRequest) ([]byte, error) {
	_, errorValue := normalizeStructuredOutputSchema(structuredResponseRequest.StructuredOutputSchema)
	if errorValue != nil {
		return nil, errorValue
	}

	constraintProvider := liteRTLMClient.ConstraintProvider
	if constraintProvider == "" {
		constraintProvider = "llguidance"
	}

	backendPreference := liteRTLMClient.NormalizedBackendPreference()
	requestDocument := liteRTLMWrapperRequestDocument{
		ModelPath:                 liteRTLMClient.ModelPath,
		BackendPreference:         backendPreference,
		AllowCPUFallback:          liteRTLMClient.AllowCPUFallback,
		Messages:                  append([]Message{}, structuredResponseRequest.Messages...),
		EnableConstrainedDecoding: true,
		ConstraintProvider:        constraintProvider,
		DecodingConstraint: liteRTLMDecodingConstraintConfiguration{
			Type:             "jsonSchema",
			ConstraintString: structuredResponseRequest.StructuredOutputSchema.Document,
		},
	}

	return json.Marshal(requestDocument)
}

func (liteRTLMClient LiteRTLMClient) NormalizedBackendPreference() []string {
	return normalizeLiteRTLMBackendPreference(liteRTLMClient.BackendPreference)
}

func (liteRTLMClient LiteRTLMClient) validateSelectedBackend(selectedBackend string) error {
	selectedBackend = strings.ToLower(strings.TrimSpace(selectedBackend))
	if selectedBackend == "" {
		return errors.New("litert-lm wrapper did not report selected backend")
	}
	if selectedBackend == "cpu" && !liteRTLMClient.AllowCPUFallback {
		return errors.New("litert-lm selected cpu backend but cpu fallback is not allowed")
	}
	for _, backendName := range liteRTLMClient.NormalizedBackendPreference() {
		if backendName == selectedBackend {
			return nil
		}
	}

	return fmt.Errorf("litert-lm selected backend %q is not in backend preference", selectedBackend)
}

func normalizeLiteRTLMBackendPreference(backendPreference []string) []string {
	normalizedBackendPreference := make([]string, 0, len(backendPreference))
	seenBackendNames := map[string]bool{}
	for _, backendName := range backendPreference {
		normalizedBackendName := strings.ToLower(strings.TrimSpace(backendName))
		if normalizedBackendName == "" || seenBackendNames[normalizedBackendName] {
			continue
		}
		seenBackendNames[normalizedBackendName] = true
		normalizedBackendPreference = append(normalizedBackendPreference, normalizedBackendName)
	}
	if len(normalizedBackendPreference) > 0 {
		return normalizedBackendPreference
	}

	return []string{"gpu", "cpu"}
}

func defaultWrapperCommandExecutor(responseContext context.Context, executablePath string, arguments []string, standardInput []byte) ([]byte, error) {
	command := exec.CommandContext(responseContext, executablePath, arguments...)
	command.Stdin = bytes.NewReader(standardInput)
	output, errorValue := command.Output()
	if errorValue != nil && len(output) == 0 {
		return nil, errorValue
	}
	return output, nil
}
