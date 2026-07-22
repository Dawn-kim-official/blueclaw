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
	operationContractSchemaName      = "blueclaw_operation_contract"
	operationContractVersion         = 1
	maximumOperationRequirementCount = 64
)

type operationContractDocument struct {
	Operations []operationRequirementDocument `json:"operations"`
}

type operationRequirementDocument struct {
	ToolName       string          `json:"toolName"`
	RequiredValues json.RawMessage `json:"requiredValues"`
}

type operationDescriptorDocument struct {
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	InputIntentSchema json.RawMessage `json:"inputIntentSchema"`
}

func compileOperationRequirements(responseContext context.Context, languageModel llm.LanguageModelProvider, request AgentRequest, toolSet *ToolSet, contract OutcomeContract) (OutcomeContract, error) {
	toolNames := stateChangingRequiredToolNames(toolSet, operationCandidateToolNames(contract))
	if len(toolNames) == 0 {
		if contract.OperationContract != nil {
			return contract, errors.New("operation contract has no required state-changing operation")
		}
		return contract, nil
	}
	descriptors, errorValue := operationDescriptorDocuments(toolSet, toolNames)
	if errorValue != nil {
		return contract, errorValue
	}
	descriptors = operationDescriptorsWithBindableIntent(descriptors)
	toolNames = operationDescriptorNames(descriptors)
	if len(toolNames) == 0 {
		if contract.OperationContract != nil {
			return contract, errors.New("operation contract has no bindable required operation")
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
	requirements, errorValue := generateOperationRequirements(responseContext, languageModel, request, descriptors, toolSet, toolNames)
	if errorValue != nil {
		return contract, errorValue
	}
	contract.OperationContract = &OperationContract{
		Version:      operationContractVersion,
		Requirements: requirements,
	}
	return normalizeOutcomeContract(contract), nil
}

func operationCandidateToolNames(contract OutcomeContract) []string {
	if contract.OperationContract != nil {
		toolNames := make([]string, 0, len(contract.OperationContract.Requirements))
		for _, requirement := range contract.OperationContract.Requirements {
			toolNames = append(toolNames, requirement.ToolName)
		}
		return appendUniqueStrings(toolNames)
	}
	toolNames := append([]string{}, contract.RequiredEvidenceTools...)
	for _, toolNameGroup := range contract.RequiredEvidenceAnyOf {
		if len(toolNameGroup) == 1 {
			toolNames = append(toolNames, toolNameGroup...)
		}
	}
	return appendUniqueStrings(toolNames)
}

func operationDescriptorsWithBindableIntent(descriptors []operationDescriptorDocument) []operationDescriptorDocument {
	result := make([]operationDescriptorDocument, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if !hasEmptyClosedObjectSchema(descriptor.InputIntentSchema) {
			result = append(result, descriptor)
		}
	}
	return result
}

func hasEmptyClosedObjectSchema(document json.RawMessage) bool {
	var schemaDocument struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		AdditionalProperties any                        `json:"additionalProperties"`
	}
	if json.Unmarshal(document, &schemaDocument) != nil {
		return false
	}
	isClosed, isBoolean := schemaDocument.AdditionalProperties.(bool)
	return len(schemaDocument.Properties) == 0 && isBoolean && !isClosed
}

func operationDescriptorNames(descriptors []operationDescriptorDocument) []string {
	names := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		names = append(names, descriptor.Name)
	}
	return names
}

func generateOperationRequirements(responseContext context.Context, languageModel llm.LanguageModelProvider, request AgentRequest, descriptors []operationDescriptorDocument, toolSet *ToolSet, toolNames []string) ([]OperationRequirement, error) {
	if len(descriptors) == 0 {
		return nil, nil
	}
	response, errorValue := languageModel.GenerateStructuredResponse(responseContext, operationContractRequest(request, descriptors, ""))
	if errorValue != nil {
		correctionReason, canCorrect := operationContractStructuredCorrectionReason(errorValue)
		if !canCorrect {
			return nil, fmt.Errorf("compile operation contract: %w", errorValue)
		}
		return correctOperationRequirements(responseContext, languageModel, request, descriptors, toolSet, toolNames, correctionReason)
	}
	requirements, parseError := parseOperationRequirements(response.Content, toolSet, toolNames)
	if parseError == nil {
		return requirements, nil
	}
	correctionReason := "The previous candidate failed operation contract validation: " + parseError.Error()
	return correctOperationRequirements(responseContext, languageModel, request, descriptors, toolSet, toolNames, correctionReason)
}

func correctOperationRequirements(responseContext context.Context, languageModel llm.LanguageModelProvider, request AgentRequest, descriptors []operationDescriptorDocument, toolSet *ToolSet, toolNames []string, correctionReason string) ([]OperationRequirement, error) {
	correctionRequest := operationContractRequest(request, descriptors, correctionReason)
	correctedResponse, correctionError := languageModel.GenerateStructuredResponse(responseContext, correctionRequest)
	if correctionError != nil {
		return nil, fmt.Errorf("correct operation contract: %w", correctionError)
	}
	requirements, parseError := parseOperationRequirements(correctedResponse.Content, toolSet, toolNames)
	if parseError != nil {
		return nil, fmt.Errorf("correct operation contract: %w", parseError)
	}
	return requirements, nil
}

func operationContractStructuredCorrectionReason(errorValue error) (string, bool) {
	correction, isCorrectable := llm.StructuredOutputCorrectionFromError(errorValue)
	if !isCorrectable {
		return "", false
	}
	messageParts := []string{
		"The previous candidate failed structured output validation.",
		"Diagnostic category: " + string(correction.Diagnostic.Category) + ".",
	}
	for _, issue := range correction.Diagnostic.ValidationIssues {
		messageParts = append(messageParts, "Validation issue: "+issue.FieldPath+" ("+string(issue.Code)+").")
	}
	return strings.Join(messageParts, " "), true
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

func operationContractRequest(request AgentRequest, descriptors []operationDescriptorDocument, correctionReason string) llm.StructuredResponseRequest {
	descriptorDocument, _ := json.Marshal(descriptors)
	messages := []llm.Message{
		{Role: "system", Content: operationContractInstruction()},
		{Role: "system", Content: buildTemporalContextDescription(request.TurnStartedAt)},
		{Role: "system", Content: "Allowed operations:\n" + string(descriptorDocument)},
	}
	if visibleContext := buildVisibleContextDescription(request.VisibleContext); visibleContext != "" {
		messages = append(messages, llm.Message{Role: "system", Content: visibleContext})
	}
	if strings.TrimSpace(correctionReason) != "" {
		messages = append(messages, llm.Message{Role: "system", Content: "Correct the previous candidate using this validation diagnostic: " + strings.TrimSpace(correctionReason)})
	}
	messages = append(messages, llm.Message{Role: "user", Content: request.Prompt})
	return llm.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               operationContractSchemaName,
			Document:           operationContractSchema(descriptors),
			IsStrictlyEnforced: true,
		},
	}
}

func operationContractInstruction() string {
	return strings.Join([]string{
		"Preserve explicit user values for the listed final state-changing operations.",
		operationRequiredValueBoundaryInstruction(),
		"Use visible context only when the latest user message directly refers to it.",
		"Resolve relative dates from the runtime temporal context.",
		"Do not invent defaults, estimates, identifiers, or helpful values.",
		"Include every input property whose value the user explicitly supplied or directly normalized; do not omit explicit values.",
		"Return one entry for every requested operation occurrence. Repeated operations must remain separate entries.",
		"Return every listed operation at least once.",
		"requiredValues must contain only explicitly requested input properties.",
	}, "\n")
}

func operationRequiredValueBoundaryInstruction() string {
	return strings.Join([]string{
		"A required value must be an invocation field whose value the user supplied for that same field, except for directly normalized dates and times.",
		"Do not turn requested artifact facts into a generated content string or invent paths, filenames, titles, MIME types, identifiers, or wrapper objects.",
		"A reference such as the completed file requires delivery but contributes no requiredValues unless the user named the exact file.",
		"Do not copy an execution choice or an output reference from one operation into another operation's requiredValues.",
	}, "\n")
}

func operationDescriptorDocuments(toolSet *ToolSet, toolNames []string) ([]operationDescriptorDocument, error) {
	descriptors := []operationDescriptorDocument{}
	for _, toolName := range toolNames {
		descriptor, isRegistered := toolSet.ToolDefinition(toolName)
		if !isRegistered || strings.TrimSpace(descriptor.ID) == "" {
			return nil, fmt.Errorf("operation %s has no canonical descriptor ID", toolName)
		}
		if len(descriptor.InputIntentSchema) == 0 {
			return nil, fmt.Errorf("operation %s has no input intent schema", toolName)
		}
		if errorValue := validateOperationInputSchema(descriptor.InputIntentSchema); errorValue != nil {
			return nil, fmt.Errorf("operation %s has invalid input intent schema: %w", toolName, errorValue)
		}
		descriptors = append(descriptors, operationDescriptorDocument{
			Name:              descriptor.Name,
			Description:       descriptor.Description,
			InputIntentSchema: descriptor.InputIntentSchema,
		})
	}
	return descriptors, nil
}

func operationContractSchema(descriptors []operationDescriptorDocument) string {
	itemSchema := any(operationRequirementSchema(descriptors[0]))
	if len(descriptors) > 1 {
		itemSchema = flatOperationRequirementSchema(descriptors)
	}
	document := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"operations"},
		"properties": map[string]any{
			"operations": map[string]any{
				"type":     "array",
				"minItems": len(descriptors),
				"maxItems": maximumOperationRequirementCount,
				"items":    itemSchema,
			},
		},
	}
	encodedDocument, _ := json.Marshal(document)
	return string(encodedDocument)
}

func flatOperationRequirementSchema(descriptors []operationDescriptorDocument) map[string]any {
	toolNames := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		toolNames = append(toolNames, descriptor.Name)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"toolName", "requiredValues"},
		"properties": map[string]any{
			"toolName": map[string]any{
				"type": "string",
				"enum": toolNames,
			},
			"requiredValues": map[string]any{"type": "object"},
		},
	}
}

func operationRequirementSchema(descriptor operationDescriptorDocument) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"toolName", "requiredValues"},
		"properties": map[string]any{
			"toolName": map[string]any{
				"type": "string",
				"enum": []string{descriptor.Name},
			},
			"requiredValues": descriptor.InputIntentSchema,
		},
	}
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
	requiredInput, errorValue := validateRequiredOperationInput(document.RequiredValues, descriptor.InputIntentSchema)
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

func validateRequiredOperationInput(document json.RawMessage, inputIntentSchema json.RawMessage) (json.RawMessage, error) {
	requiredInput, errorValue := decodeJSONObject(document)
	if errorValue != nil {
		return nil, fmt.Errorf("required values must be a JSON object: %w", errorValue)
	}
	var validationInput map[string]any
	if errorValue := json.Unmarshal(document, &validationInput); errorValue != nil || validationInput == nil {
		return nil, errors.New("required values must be a JSON object")
	}
	schema, errorValue := decodeOperationInputSchema(inputIntentSchema)
	if errorValue != nil {
		return nil, errorValue
	}
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

func validateOperationInputSchema(inputSchema json.RawMessage) error {
	schema, errorValue := decodeOperationInputSchema(inputSchema)
	if errorValue != nil {
		return errorValue
	}
	if _, errorValue := schema.Resolve(nil); errorValue != nil {
		return fmt.Errorf("descriptor input schema cannot be resolved: %w", errorValue)
	}
	return nil
}

func decodeOperationInputSchema(inputSchema json.RawMessage) (jsonschema.Schema, error) {
	var schema jsonschema.Schema
	if errorValue := json.Unmarshal(inputSchema, &schema); errorValue != nil {
		return jsonschema.Schema{}, fmt.Errorf("descriptor input schema is invalid: %w", errorValue)
	}
	if schema.Type != "object" && !stringSliceContains(schema.Types, "object") {
		return jsonschema.Schema{}, errors.New("descriptor input schema must describe an object")
	}
	return schema, nil
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
		if _, errorValue := validateRequiredOperationInput(requirement.RequiredInput, descriptor.InputIntentSchema); errorValue != nil {
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

func firstPendingOperationRequirement(contract *OperationContract, observations []turnObservation) (OperationRequirement, bool) {
	pendingRequirements := pendingOperationRequirements(contract, observations)
	if len(pendingRequirements) == 0 {
		return OperationRequirement{}, false
	}
	return pendingRequirements[0], true
}

func firstPendingRequiredToolName(operationContract *OperationContract, requiredNextToolNames []string, observations []turnObservation) string {
	if requirement, isPending := firstPendingOperationRequirement(operationContract, observations); isPending {
		return strings.TrimSpace(requirement.ToolName)
	}
	nextToolIndex := 0
	requiredNextToolNames = appendUniqueStrings(requiredNextToolNames)
	for _, observation := range observations {
		if nextToolIndex >= len(requiredNextToolNames) {
			break
		}
		if observation.Failed() || strings.TrimSpace(observation.Tool) != requiredNextToolNames[nextToolIndex] {
			continue
		}
		nextToolIndex++
	}
	if nextToolIndex < len(requiredNextToolNames) {
		return requiredNextToolNames[nextToolIndex]
	}
	return ""
}

func pendingOperationRequirements(contract *OperationContract, observations []turnObservation) []OperationRequirement {
	if contract == nil || contract.Version != operationContractVersion || len(contract.Requirements) == 0 {
		return nil
	}
	matchedRequirementIndexes := matchedOperationRequirementIndexes(contract.Requirements, observations)
	pendingRequirements := make([]OperationRequirement, 0, len(contract.Requirements)-len(matchedRequirementIndexes))
	for requirementIndex, requirement := range contract.Requirements {
		if !matchedRequirementIndexes[requirementIndex] {
			pendingRequirements = append(pendingRequirements, requirement)
		}
	}
	return pendingRequirements
}

func matchedOperationRequirementCount(contract *OperationContract, observations []turnObservation) int {
	if contract == nil || contract.Version != operationContractVersion || len(contract.Requirements) == 0 {
		return 0
	}
	return len(matchedOperationRequirementIndexes(contract.Requirements, observations))
}

func matchedOperationRequirementIndexes(requirements []OperationRequirement, observations []turnObservation) map[int]bool {
	matchedRequirementByObservation := make([]int, len(observations))
	for index := range matchedRequirementByObservation {
		matchedRequirementByObservation[index] = -1
	}
	for requirementIndex := range requirements {
		visitedObservations := make([]bool, len(observations))
		assignOperationObservation(requirementIndex, requirements, observations, matchedRequirementByObservation, visitedObservations)
	}
	matchedRequirementIndexes := map[int]bool{}
	for _, requirementIndex := range matchedRequirementByObservation {
		if requirementIndex >= 0 {
			matchedRequirementIndexes[requirementIndex] = true
		}
	}
	return matchedRequirementIndexes
}

func pendingOperationRequirementContext(contract *OperationContract, observations []turnObservation) string {
	pendingRequirements := pendingOperationRequirements(contract, observations)
	if len(pendingRequirements) == 0 {
		return ""
	}
	document, errorValue := json.Marshal(pendingRequirements)
	if errorValue != nil {
		return ""
	}
	return "Pending typed operation requirements. A contracted mutation must satisfy one still-unmatched requiredInput:\n" + string(document)
}


func operationContractIncludesTool(contract *OperationContract, toolName string) bool {
	if contract == nil || contract.Version != operationContractVersion {
		return false
	}
	for _, requirement := range contract.Requirements {
		if requirement.ToolName == toolName {
			return true
		}
	}
	return false
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
	if isRequiredObject {
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
	requiredArray, isRequiredArray := requiredValue.([]any)
	if isRequiredArray {
		actualArray, isActualArray := actualValue.([]any)
		if !isActualArray || len(actualArray) != len(requiredArray) {
			return false
		}
		for index := range requiredArray {
			if !jsonContains(actualArray[index], requiredArray[index]) {
				return false
			}
		}
		return true
	}
	return jsonschema.Equal(actualValue, requiredValue)
}
