package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"blueclaw/internal/llm"
	"github.com/google/jsonschema-go/jsonschema"
)

const (
	operationContractSchemaName       = "blueclaw_operation_contract"
	operationContractReviewSchemaName = "blueclaw_operation_contract_review"
	operationContractVersion          = 1
	maximumOperationRequirementCount  = 64
)

type operationContractDocument struct {
	Operations []operationRequirementDocument `json:"operations"`
}

type operationRequirementDocument struct {
	ToolName           string `json:"toolName"`
	RequiredValuesJSON string `json:"requiredValuesJSON"`
}

type operationDescriptorDocument struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type operationContractReviewDocument struct {
	IsComplete bool   `json:"isComplete"`
	Reason     string `json:"reason"`
}

func compileOperationRequirements(responseContext context.Context, languageModel llm.LanguageModelProvider, request AgentRequest, toolSet *ToolSet, contract OutcomeContract) (OutcomeContract, error) {
	toolNames := stateChangingRequiredToolNames(toolSet, contract.RequiredEvidenceTools)
	if len(toolNames) == 0 {
		if contract.OperationContract != nil {
			return contract, errors.New("operation contract has no required state-changing operation")
		}
		return contract, nil
	}
	if contract.OperationContract != nil {
		if errorValue := validateOperationContract(contract.OperationContract, toolSet, toolNames); errorValue != nil {
			return contract, errorValue
		}
		return contract, nil
	}
	if languageModel == nil {
		return contract, errors.New("operation contract language model was not configured")
	}
	descriptors, errorValue := operationDescriptorDocuments(toolSet, toolNames)
	if errorValue != nil {
		return contract, errorValue
	}
	reviewReason := ""
	for range 2 {
		response, generationError := languageModel.GenerateStructuredResponse(responseContext, operationContractRequest(request, descriptors, toolNames, reviewReason))
		if generationError != nil {
			return contract, fmt.Errorf("compile operation contract: %w", generationError)
		}
		requirements, parseError := parseOperationRequirements(response.Content, toolSet, toolNames)
		if parseError != nil {
			return contract, parseError
		}
		review, reviewError := reviewOperationRequirements(responseContext, languageModel, request, descriptors, requirements)
		if reviewError != nil {
			return contract, reviewError
		}
		if review.IsComplete {
			contract.OperationContract = &OperationContract{
				Version:      operationContractVersion,
				Requirements: requirements,
			}
			return normalizeOutcomeContract(contract), nil
		}
		reviewReason = firstNonEmptyString(strings.TrimSpace(review.Reason), "the candidate omitted or invented requested operation values")
	}
	return contract, fmt.Errorf("operation contract review failed: %s", reviewReason)
}

func stateChangingRequiredToolNames(toolSet *ToolSet, toolNames []string) []string {
	stateChangingToolNames := []string{}
	for _, toolName := range appendUniqueStrings(toolNames) {
		if requiredEvidenceToolNeedsSuccessfulSideEffect(toolSet, toolName) {
			stateChangingToolNames = append(stateChangingToolNames, toolName)
		}
	}
	return stateChangingToolNames
}

func operationContractRequest(request AgentRequest, descriptors []operationDescriptorDocument, toolNames []string, reviewReason string) llm.StructuredResponseRequest {
	descriptorDocument, _ := json.Marshal(descriptors)
	messages := []llm.Message{
		{Role: "system", Content: operationContractInstruction()},
		{Role: "system", Content: buildTemporalContextDescription(request.TurnStartedAt)},
		{Role: "system", Content: "Allowed operations:\n" + string(descriptorDocument)},
	}
	if visibleContext := buildVisibleContextDescription(request.VisibleContext); visibleContext != "" {
		messages = append(messages, llm.Message{Role: "system", Content: visibleContext})
	}
	if strings.TrimSpace(reviewReason) != "" {
		messages = append(messages, llm.Message{Role: "system", Content: "Correct the previous candidate. Independent review: " + strings.TrimSpace(reviewReason)})
	}
	messages = append(messages, llm.Message{Role: "user", Content: request.Prompt})
	return llm.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               operationContractSchemaName,
			Document:           operationContractSchema(toolNames),
			IsStrictlyEnforced: true,
		},
	}
}

func operationContractInstruction() string {
	return strings.Join([]string{
		"Preserve explicit user values for the listed final state-changing operations.",
		"Use visible context only when the latest user message directly refers to it.",
		"Resolve relative dates from the runtime temporal context.",
		"Do not invent defaults, estimates, identifiers, or helpful values.",
		"Include every input property whose value the user explicitly supplied or directly normalized; do not omit explicit values.",
		"Return one entry for every requested operation occurrence. Repeated operations must remain separate entries.",
		"Return every listed operation at least once.",
		"requiredValuesJSON must be a JSON object containing only explicitly requested input properties.",
	}, "\n")
}

func reviewOperationRequirements(responseContext context.Context, languageModel llm.LanguageModelProvider, request AgentRequest, descriptors []operationDescriptorDocument, requirements []OperationRequirement) (operationContractReviewDocument, error) {
	candidateDocument, _ := json.Marshal(operationRequirementDocuments(requirements))
	descriptorDocument, _ := json.Marshal(descriptors)
	messages := []llm.Message{
		{Role: "system", Content: strings.Join([]string{
			"Independently verify an operation contract against the user's request.",
			"isComplete is true only when every explicitly supplied or directly normalized input value is preserved, no value was invented, every requested operation occurrence is present, and no unrelated operation was added.",
			"Resolve relative dates from the runtime temporal context.",
			"Return a concise correction reason when isComplete is false.",
		}, "\n")},
		{Role: "system", Content: buildTemporalContextDescription(request.TurnStartedAt)},
		{Role: "system", Content: "Allowed operations:\n" + string(descriptorDocument)},
		{Role: "system", Content: "Candidate operation contract:\n" + string(candidateDocument)},
	}
	if visibleContext := buildVisibleContextDescription(request.VisibleContext); visibleContext != "" {
		messages = append(messages, llm.Message{Role: "system", Content: visibleContext})
	}
	messages = append(messages, llm.Message{Role: "user", Content: request.Prompt})
	response, errorValue := languageModel.GenerateStructuredResponse(responseContext, llm.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               operationContractReviewSchemaName,
			Document:           `{"type":"object","properties":{"isComplete":{"type":"boolean"},"reason":{"type":"string"}},"required":["isComplete","reason"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return operationContractReviewDocument{}, fmt.Errorf("review operation contract: %w", errorValue)
	}
	var review operationContractReviewDocument
	if errorValue := json.Unmarshal([]byte(response.Content), &review); errorValue != nil {
		return operationContractReviewDocument{}, fmt.Errorf("decode operation contract review: %w", errorValue)
	}
	review.Reason = strings.TrimSpace(review.Reason)
	return review, nil
}

func operationRequirementDocuments(requirements []OperationRequirement) []operationRequirementDocument {
	documents := make([]operationRequirementDocument, 0, len(requirements))
	for _, requirement := range requirements {
		documents = append(documents, operationRequirementDocument{
			ToolName:           requirement.ToolName,
			RequiredValuesJSON: string(requirement.RequiredInput),
		})
	}
	return documents
}

func operationDescriptorDocuments(toolSet *ToolSet, toolNames []string) ([]operationDescriptorDocument, error) {
	descriptors := []operationDescriptorDocument{}
	for _, toolName := range toolNames {
		descriptor, isRegistered := toolSet.ToolDefinition(toolName)
		if !isRegistered || strings.TrimSpace(descriptor.ID) == "" {
			return nil, fmt.Errorf("operation %s has no canonical descriptor ID", toolName)
		}
		if len(descriptor.InputSchema) == 0 {
			return nil, fmt.Errorf("operation %s has no input schema", toolName)
		}
		if _, errorValue := validateRequiredOperationInput(`{}`, descriptor.InputSchema); errorValue != nil {
			return nil, fmt.Errorf("operation %s has invalid input schema: %w", toolName, errorValue)
		}
		descriptors = append(descriptors, operationDescriptorDocument{
			Name:        descriptor.Name,
			Description: descriptor.Description,
			InputSchema: descriptor.InputSchema,
		})
	}
	return descriptors, nil
}

func operationContractSchema(toolNames []string) string {
	document := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"operations"},
		"properties": map[string]any{
			"operations": map[string]any{
				"type":     "array",
				"minItems": len(toolNames),
				"maxItems": maximumOperationRequirementCount,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"toolName", "requiredValuesJSON"},
					"properties": map[string]any{
						"toolName":           map[string]any{"type": "string", "enum": toolNames},
						"requiredValuesJSON": map[string]any{"type": "string", "maxLength": 4096},
					},
				},
			},
		},
	}
	encodedDocument, _ := json.Marshal(document)
	return string(encodedDocument)
}

func parseOperationRequirements(content string, toolSet *ToolSet, expectedToolNames []string) ([]OperationRequirement, error) {
	var document operationContractDocument
	if errorValue := json.Unmarshal([]byte(content), &document); errorValue != nil {
		return nil, fmt.Errorf("decode operation contract: %w", errorValue)
	}
	if len(document.Operations) < len(expectedToolNames) || len(document.Operations) > maximumOperationRequirementCount {
		return nil, errors.New("operation contract did not contain every required operation")
	}
	requirements := make([]OperationRequirement, 0, len(document.Operations))
	seenToolNames := map[string]bool{}
	for index, operation := range document.Operations {
		requirement, errorValue := parseOperationRequirement(index, operation, toolSet, expectedToolNames)
		if errorValue != nil {
			return nil, errorValue
		}
		seenToolNames[requirement.ToolName] = true
		requirements = append(requirements, requirement)
	}
	for _, toolName := range expectedToolNames {
		if !seenToolNames[toolName] {
			return nil, fmt.Errorf("operation contract omitted %s", toolName)
		}
	}
	return requirements, nil
}

func parseOperationRequirement(index int, document operationRequirementDocument, toolSet *ToolSet, expectedToolNames []string) (OperationRequirement, error) {
	toolName := strings.TrimSpace(document.ToolName)
	if !stringSliceContains(expectedToolNames, toolName) {
		return OperationRequirement{}, fmt.Errorf("operation contract returned unknown tool %s", toolName)
	}
	descriptor, isRegistered := toolSet.ToolDefinition(toolName)
	if !isRegistered || strings.TrimSpace(descriptor.ID) == "" {
		return OperationRequirement{}, fmt.Errorf("operation %s has no canonical descriptor ID", toolName)
	}
	requiredInput, errorValue := validateRequiredOperationInput(document.RequiredValuesJSON, descriptor.InputSchema)
	if errorValue != nil {
		return OperationRequirement{}, fmt.Errorf("operation %s: %w", toolName, errorValue)
	}
	return OperationRequirement{
		RequirementID: fmt.Sprintf("operation-%d", index+1),
		ToolID:        descriptor.ID,
		ToolName:      toolName,
		InputMode:     operationInputMode(requiredInput),
		RequiredInput: requiredInput,
	}, nil
}

func operationInputMode(requiredInput json.RawMessage) OperationInputMode {
	requiredValues, errorValue := decodeJSONObject(requiredInput)
	if errorValue == nil && len(requiredValues) == 0 {
		return OperationInputNoExplicitValues
	}
	return OperationInputContainsExplicit
}

func validateRequiredOperationInput(document string, inputSchema json.RawMessage) (json.RawMessage, error) {
	requiredInput, errorValue := decodeJSONObject([]byte(document))
	if errorValue != nil {
		return nil, fmt.Errorf("required values must be a JSON object: %w", errorValue)
	}
	var validationInput map[string]any
	if errorValue := json.Unmarshal([]byte(document), &validationInput); errorValue != nil || validationInput == nil {
		return nil, errors.New("required values must be a JSON object")
	}
	var schema jsonschema.Schema
	if errorValue := json.Unmarshal(inputSchema, &schema); errorValue != nil {
		return nil, fmt.Errorf("descriptor input schema is invalid: %w", errorValue)
	}
	if schema.Type != "object" && !stringSliceContains(schema.Types, "object") {
		return nil, errors.New("descriptor input schema must describe an object")
	}
	schema.Required = nil
	resolvedSchema, errorValue := schema.Resolve(nil)
	if errorValue != nil {
		return nil, fmt.Errorf("descriptor input schema cannot be resolved: %w", errorValue)
	}
	if errorValue := resolvedSchema.Validate(validationInput); errorValue != nil {
		return nil, fmt.Errorf("required values do not match the descriptor input schema: %w", errorValue)
	}
	normalizedInput, _ := json.Marshal(requiredInput)
	return normalizedInput, nil
}

func decodeJSONObject(document []byte) (map[string]any, error) {
	value, errorValue := decodeJSONValue(document)
	if errorValue != nil {
		return nil, errorValue
	}
	object, isObject := value.(map[string]any)
	if !isObject {
		return nil, errors.New("value is not an object")
	}
	return object, nil
}

func decodeJSONValue(document []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	var value any
	if errorValue := decoder.Decode(&value); errorValue != nil {
		return nil, errorValue
	}
	if errorValue := decoder.Decode(&struct{}{}); errorValue != io.EOF {
		if errorValue == nil {
			return nil, errors.New("document contains multiple JSON values")
		}
		return nil, errorValue
	}
	return value, nil
}

func validateOperationContract(contract *OperationContract, toolSet *ToolSet, expectedToolNames []string) error {
	if contract == nil || contract.Version != operationContractVersion || len(contract.Requirements) == 0 {
		return errors.New("operation contract metadata is invalid")
	}
	seenRequirementIDs := map[string]bool{}
	seenToolNames := map[string]bool{}
	for _, requirement := range contract.Requirements {
		requirementID := strings.TrimSpace(requirement.RequirementID)
		if requirementID == "" || seenRequirementIDs[requirementID] {
			return errors.New("operation contract requirement identifier is missing or duplicated")
		}
		seenRequirementIDs[requirementID] = true
		toolName := strings.TrimSpace(requirement.ToolName)
		if !stringSliceContains(expectedToolNames, toolName) {
			return fmt.Errorf("operation contract contains unexpected tool %s", toolName)
		}
		descriptor, isRegistered := toolSet.ToolDefinition(toolName)
		if !isRegistered || strings.TrimSpace(requirement.ToolID) != strings.TrimSpace(descriptor.ID) {
			return fmt.Errorf("operation contract descriptor identity does not match %s", toolName)
		}
		if _, errorValue := validateRequiredOperationInput(string(requirement.RequiredInput), descriptor.InputSchema); errorValue != nil {
			return fmt.Errorf("operation contract requirement %s is invalid: %w", requirementID, errorValue)
		}
		if requirement.InputMode != operationInputMode(requirement.RequiredInput) {
			return fmt.Errorf("operation contract requirement %s input mode is invalid", requirementID)
		}
		seenToolNames[toolName] = true
	}
	for _, toolName := range expectedToolNames {
		if !seenToolNames[toolName] {
			return fmt.Errorf("operation contract omitted %s", toolName)
		}
	}
	return nil
}

func operationRequirementsSatisfied(contract *OperationContract, observations []turnObservation) bool {
	if contract == nil {
		return true
	}
	if contract.Version != operationContractVersion || len(contract.Requirements) == 0 {
		return false
	}
	matchedRequirementByObservation := make([]int, len(observations))
	for index := range matchedRequirementByObservation {
		matchedRequirementByObservation[index] = -1
	}
	for requirementIndex := range contract.Requirements {
		visitedObservations := make([]bool, len(observations))
		if !assignOperationObservation(requirementIndex, contract.Requirements, observations, matchedRequirementByObservation, visitedObservations) {
			return false
		}
	}
	return true
}

func assignOperationObservation(requirementIndex int, requirements []OperationRequirement, observations []turnObservation, matchedRequirementByObservation []int, visitedObservations []bool) bool {
	for observationIndex, observation := range observations {
		if visitedObservations[observationIndex] || !operationRequirementMatchesObservation(requirements[requirementIndex], observation) {
			continue
		}
		visitedObservations[observationIndex] = true
		previousRequirementIndex := matchedRequirementByObservation[observationIndex]
		if previousRequirementIndex == -1 || assignOperationObservation(previousRequirementIndex, requirements, observations, matchedRequirementByObservation, visitedObservations) {
			matchedRequirementByObservation[observationIndex] = requirementIndex
			return true
		}
	}
	return false
}

func operationRequirementMatchesObservation(requirement OperationRequirement, observation turnObservation) bool {
	if strings.TrimSpace(requirement.RequirementID) == "" || observation.Failed() {
		return false
	}
	if observation.ToolID != requirement.ToolID || observation.Tool != requirement.ToolName {
		return false
	}
	return requiredInputMatches(requirement.InputMode, requirement.RequiredInput, observation.ToolInput)
}

func requiredInputMatches(inputMode OperationInputMode, requiredInput json.RawMessage, actualInput json.RawMessage) bool {
	requiredValue, requiredError := decodeJSONValue(requiredInput)
	actualValue, actualError := decodeJSONValue(actualInput)
	if requiredError != nil || actualError != nil {
		return false
	}
	switch inputMode {
	case OperationInputNoExplicitValues:
		_, isObject := actualValue.(map[string]any)
		return isObject
	case OperationInputContainsExplicit:
		requiredObject, isObject := requiredValue.(map[string]any)
		return isObject && len(requiredObject) > 0 && jsonContains(actualValue, requiredValue)
	default:
		return false
	}
}

func jsonContains(actualValue any, requiredValue any) bool {
	requiredObject, isRequiredObject := requiredValue.(map[string]any)
	if !isRequiredObject {
		return jsonschema.Equal(actualValue, requiredValue)
	}
	actualObject, isActualObject := actualValue.(map[string]any)
	if !isActualObject {
		return false
	}
	for propertyName, requiredPropertyValue := range requiredObject {
		actualPropertyValue, exists := actualObject[propertyName]
		if !exists || !jsonContains(actualPropertyValue, requiredPropertyValue) {
			return false
		}
	}
	return true
}
