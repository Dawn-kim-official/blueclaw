package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
)

type WrapperCommandExecutor func(context.Context, string, []string, []byte) ([]byte, error)

type LiteRTLMClient struct {
	WrapperPath        string
	WrapperArguments   []string
	ModelPath          string
	Backend            string
	ConstraintProvider string
	CommandExecutor    WrapperCommandExecutor
}

type liteRTLMWrapperRequestDocument struct {
	ModelPath                 string                                  `json:"modelPath"`
	Backend                   string                                  `json:"backend"`
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
	Content string `json:"content"`
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

	requestDocument := liteRTLMWrapperRequestDocument{
		ModelPath:                 liteRTLMClient.ModelPath,
		Backend:                   liteRTLMClient.Backend,
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

func defaultWrapperCommandExecutor(responseContext context.Context, executablePath string, arguments []string, standardInput []byte) ([]byte, error) {
	command := exec.CommandContext(responseContext, executablePath, arguments...)
	command.Stdin = bytes.NewReader(standardInput)
	return command.Output()
}
