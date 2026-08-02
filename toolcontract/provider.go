package toolcontract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

var canonicalToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,128}$`)

// A name that arrives from outside is not ours to shape; it is normalized into the
// canonical grammar when it is registered, and kept verbatim for the remote call.
var remoteToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.\-/]{1,128}$`)

func IsRemoteToolName(name string) bool {
	return remoteToolNamePattern.MatchString(name)
}

func IsCanonicalToolName(name string) bool {
	return canonicalToolNamePattern.MatchString(name)
}

const (
	ToolVisibilityModel    = "visible"
	ToolVisibilityInternal = "hidden"
	ToolVisibilityControl  = "control"

	ToolIdempotencyNone      = "none"
	ToolIdempotencySupported = "supported"
	ToolIdempotencyRequired  = "required"

	ToolCompletionNone        = "none"
	ToolCompletionObservation = "observation"

	ToolProviderTrusted  = "trusted"
	ToolProviderExternal = "external"
)

type ToolProvider interface {
	ProviderID() string
	ListTools(context.Context) ([]BoundTool, error)
}

type ToolProviderRegistration struct {
	Provider ToolProvider
	Trust    string
}

type QuarantinedToolProvider struct {
	ProviderID string
	Reason     string
}

type preparedToolProvider struct {
	providerID string
	tools      []BoundTool
}

func (toolSet *ToolSet) RegisterProvider(toolContext context.Context, provider ToolProvider) error {
	if toolSet == nil {
		return errors.New("tool set is unavailable")
	}
	if provider == nil {
		return errors.New("tool provider is required")
	}
	providerID := strings.TrimSpace(provider.ProviderID())
	if providerID == "" {
		return errors.New("tool provider identifier is required")
	}
	boundTools, errorValue := provider.ListTools(toolContext)
	if errorValue != nil {
		return fmt.Errorf("load tool provider %s: %w", providerID, errorValue)
	}
	normalizedTools, errorValue := normalizeProviderTools(providerID, boundTools)
	if errorValue != nil {
		return errorValue
	}
	if errorValue := toolSet.validateProviderCollisions(normalizedTools); errorValue != nil {
		return errorValue
	}
	for _, boundTool := range normalizedTools {
		if errorValue := toolSet.RegisterBoundTool(boundTool); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func (toolSet *ToolSet) RegisterProviders(toolContext context.Context, registrations []ToolProviderRegistration) ([]QuarantinedToolProvider, error) {
	quarantinedProviders := []QuarantinedToolProvider{}
	externalProviders := []preparedToolProvider{}
	for _, registration := range registrations {
		if errorValue := validateToolProviderTrust(registration.Trust); errorValue != nil {
			return quarantinedProviders, errorValue
		}
		if strings.TrimSpace(registration.Trust) != ToolProviderExternal {
			if errorValue := toolSet.RegisterProvider(toolContext, registration.Provider); errorValue != nil {
				return quarantinedProviders, errorValue
			}
			continue
		}
		preparedProvider, errorValue := prepareToolProvider(toolContext, registration.Provider)
		if errorValue != nil {
			quarantinedProviders = append(quarantinedProviders, quarantineToolProvider(registration.Provider, errorValue))
			continue
		}
		externalProviders = append(externalProviders, preparedProvider)
	}
	collisionReasons := externalProviderCollisionReasons(toolSet, externalProviders)
	for _, provider := range externalProviders {
		if reason := collisionReasons[provider.providerID]; reason != "" {
			quarantinedProviders = append(quarantinedProviders, QuarantinedToolProvider{ProviderID: provider.providerID, Reason: reason})
			continue
		}
		for _, boundTool := range provider.tools {
			if errorValue := toolSet.RegisterBoundTool(boundTool); errorValue != nil {
				return quarantinedProviders, errorValue
			}
		}
	}
	toolSet.quarantinedProviders = append(toolSet.quarantinedProviders, quarantinedProviders...)
	return quarantinedProviders, nil
}

func validateToolProviderTrust(trust string) error {
	switch strings.TrimSpace(trust) {
	case ToolProviderTrusted, ToolProviderExternal:
		return nil
	default:
		return errors.New("tool provider trust is invalid")
	}
}

func prepareToolProvider(toolContext context.Context, provider ToolProvider) (preparedToolProvider, error) {
	if provider == nil {
		return preparedToolProvider{}, errors.New("tool provider is required")
	}
	providerID := strings.TrimSpace(provider.ProviderID())
	if providerID == "" {
		return preparedToolProvider{}, errors.New("tool provider identifier is required")
	}
	boundTools, errorValue := provider.ListTools(toolContext)
	if errorValue != nil {
		return preparedToolProvider{}, fmt.Errorf("load tool provider %s: %w", providerID, errorValue)
	}
	normalizedTools, errorValue := normalizeProviderTools(providerID, boundTools)
	if errorValue != nil {
		return preparedToolProvider{}, errorValue
	}
	return preparedToolProvider{providerID: providerID, tools: normalizedTools}, nil
}

func quarantineToolProvider(provider ToolProvider, errorValue error) QuarantinedToolProvider {
	providerID := ""
	if provider != nil {
		providerID = strings.TrimSpace(provider.ProviderID())
	}
	return QuarantinedToolProvider{ProviderID: providerID, Reason: errorValue.Error()}
}

func externalProviderCollisionReasons(toolSet *ToolSet, providers []preparedToolProvider) map[string]string {
	reasons := map[string]string{}
	providerIDsByToolName := map[string][]string{}
	providerIDsByToolID := map[string][]string{}
	providerIDCount := map[string]int{}
	for _, provider := range providers {
		providerIDCount[provider.providerID]++
		for _, boundTool := range provider.tools {
			descriptor := boundTool.Definition
			providerIDsByToolName[descriptor.Name] = append(providerIDsByToolName[descriptor.Name], provider.providerID)
			providerIDsByToolID[descriptor.ID] = append(providerIDsByToolID[descriptor.ID], provider.providerID)
			if toolSet.IsRegistered(descriptor.Name) {
				reasons[provider.providerID] = "tool name collides with a trusted provider: " + descriptor.Name
			}
			if _, isRegistered := toolSet.boundToolNameByID[descriptor.ID]; isRegistered {
				reasons[provider.providerID] = "tool identifier collides with a trusted provider: " + descriptor.ID
			}
		}
	}
	for providerID, count := range providerIDCount {
		if count > 1 {
			reasons[providerID] = "tool provider identifier is duplicated: " + providerID
		}
	}
	markExternalCollisions(reasons, providerIDsByToolName, "tool name is duplicated across external providers: ")
	markExternalCollisions(reasons, providerIDsByToolID, "tool identifier is duplicated across external providers: ")
	return reasons
}

func markExternalCollisions(reasons map[string]string, providerIDsByValue map[string][]string, prefix string) {
	for value, providerIDs := range providerIDsByValue {
		if len(providerIDs) < 2 {
			continue
		}
		for _, providerID := range providerIDs {
			reasons[providerID] = prefix + value
		}
	}
}

func normalizeProviderTools(providerID string, boundTools []BoundTool) ([]BoundTool, error) {
	normalizedTools := make([]BoundTool, 0, len(boundTools))
	toolNameByID := map[string]string{}
	toolIDByName := map[string]string{}
	for _, boundTool := range boundTools {
		normalizedTool, errorValue := normalizeProviderTool(providerID, boundTool)
		if errorValue != nil {
			return nil, errorValue
		}
		toolDescriptor := normalizedTool.Definition
		if existingName := toolNameByID[toolDescriptor.ID]; existingName != "" {
			return nil, fmt.Errorf("tool provider %s repeats identifier %s for %s and %s", providerID, toolDescriptor.ID, existingName, toolDescriptor.Name)
		}
		if existingID := toolIDByName[toolDescriptor.Name]; existingID != "" {
			return nil, fmt.Errorf("tool provider %s repeats model name %s for %s and %s", providerID, toolDescriptor.Name, existingID, toolDescriptor.ID)
		}
		toolNameByID[toolDescriptor.ID] = toolDescriptor.Name
		toolIDByName[toolDescriptor.Name] = toolDescriptor.ID
		normalizedTools = append(normalizedTools, normalizedTool)
	}
	return normalizedTools, nil
}

func normalizeProviderTool(providerID string, boundTool BoundTool) (BoundTool, error) {
	toolDescriptor := boundTool.Definition
	toolDescriptor.ProviderID = strings.TrimSpace(toolDescriptor.ProviderID)
	if toolDescriptor.ProviderID == "" {
		toolDescriptor.ProviderID = providerID
	}
	if toolDescriptor.ProviderID != providerID {
		return BoundTool{}, fmt.Errorf("tool %s belongs to provider %s, not %s", toolDescriptor.Name, toolDescriptor.ProviderID, providerID)
	}
	toolDescriptor.ID = strings.TrimSpace(toolDescriptor.ID)
	toolDescriptor.Namespace = strings.TrimSpace(toolDescriptor.Namespace)
	toolDescriptor.Name = strings.TrimSpace(toolDescriptor.Name)
	toolDescriptor.Description = strings.TrimSpace(toolDescriptor.Description)
	toolDescriptor.PrivacyClass = strings.TrimSpace(toolDescriptor.PrivacyClass)
	toolDescriptor.Visibility = strings.TrimSpace(toolDescriptor.Visibility)
	toolDescriptor.SideEffectClass = normalizeToolSideEffectClass(toolDescriptor.SideEffectClass)
	toolDescriptor.PolicyResource = strings.TrimSpace(toolDescriptor.PolicyResource)
	toolDescriptor.Idempotency = strings.TrimSpace(toolDescriptor.Idempotency)
	toolDescriptor.IdempotencyScope = strings.TrimSpace(toolDescriptor.IdempotencyScope)
	toolDescriptor.Completion.Mode = strings.TrimSpace(toolDescriptor.Completion.Mode)
	normalizedInputSchema, errorValue := normalizeProviderToolSchema(toolDescriptor.InputSchema)
	if errorValue != nil {
		return BoundTool{}, fmt.Errorf("invalid tool descriptor %s: inputSchema %w", firstNonEmptyString(toolDescriptor.ID, toolDescriptor.Name), errorValue)
	}
	normalizedOutputSchema, errorValue := normalizeProviderToolSchema(toolDescriptor.OutputSchema)
	if errorValue != nil {
		return BoundTool{}, fmt.Errorf("invalid tool descriptor %s: outputSchema %w", firstNonEmptyString(toolDescriptor.ID, toolDescriptor.Name), errorValue)
	}
	normalizedInputIntentSchema, errorValue := normalizeProviderToolSchema(toolDescriptor.InputIntentSchema)
	if errorValue != nil {
		return BoundTool{}, fmt.Errorf("invalid tool descriptor %s: inputIntentSchema %w", firstNonEmptyString(toolDescriptor.ID, toolDescriptor.Name), errorValue)
	}
	toolDescriptor.InputSchema = normalizedInputSchema
	toolDescriptor.InputIntentSchema = normalizedInputIntentSchema
	toolDescriptor.OutputSchema = normalizedOutputSchema
	if toolDescriptor.ResultContract != nil {
		normalizedResultSchema, resultSchemaError := normalizeProviderToolSchema(toolDescriptor.ResultContract.Schema)
		if resultSchemaError != nil {
			return BoundTool{}, fmt.Errorf("invalid tool descriptor %s: resultContract.schema %w", firstNonEmptyString(toolDescriptor.ID, toolDescriptor.Name), resultSchemaError)
		}
		toolDescriptor.ResultContract = &ToolResultContract{
			Schema:            normalizedResultSchema,
			Effects:           copyResourceEffectContracts(toolDescriptor.ResultContract.Effects),
			EvidenceCondition: copyEvidenceCondition(toolDescriptor.ResultContract.EvidenceCondition),
		}
	}
	boundTool.Definition = toolDescriptor
	if errorValue := validateProviderTool(boundTool); errorValue != nil {
		return BoundTool{}, fmt.Errorf("invalid tool descriptor %s: %w", firstNonEmptyString(toolDescriptor.ID, toolDescriptor.Name), errorValue)
	}
	return boundTool, nil
}

func normalizeProviderToolSchema(schema json.RawMessage) (json.RawMessage, error) {
	var document any
	if len(bytes.TrimSpace(schema)) == 0 {
		return schema, nil
	}
	if errorValue := json.Unmarshal(schema, &document); errorValue != nil {
		return nil, errorValue
	}
	if errorValue := validateExplicitlyClosedProviderSchemaObjects(document); errorValue != nil {
		return nil, errorValue
	}
	return json.Marshal(document)
}

func validateExplicitlyClosedProviderSchemaObjects(value any) error {
	switch document := value.(type) {
	case []any:
		for _, item := range document {
			if errorValue := validateExplicitlyClosedProviderSchemaObjects(item); errorValue != nil {
				return errorValue
			}
		}
	case map[string]any:
		if SchemaTypeIncludesObject(document["type"]) {
			additionalProperties, exists := document["additionalProperties"]
			if !exists || !isExplicitlyClosedAdditionalProperties(additionalProperties) {
				return errors.New("object schema must explicitly set additionalProperties to false")
			}
		}
		for _, child := range document {
			if errorValue := validateExplicitlyClosedProviderSchemaObjects(child); errorValue != nil {
				return errorValue
			}
		}
	}
	return nil
}

func SchemaTypeIncludesObject(value any) bool {
	if value == "object" {
		return true
	}
	schemaTypes, isArray := value.([]any)
	if !isArray {
		return false
	}
	return slices.Contains(schemaTypes, any("object"))
}

func isExplicitlyClosedAdditionalProperties(value any) bool {
	additionalProperties, isBoolean := value.(bool)
	return isBoolean && !additionalProperties
}

func validateProviderTool(boundTool BoundTool) error {
	toolDescriptor := boundTool.Definition
	requiredValues := map[string]string{
		"id":              toolDescriptor.ID,
		"providerID":      toolDescriptor.ProviderID,
		"namespace":       toolDescriptor.Namespace,
		"name":            toolDescriptor.Name,
		"description":     toolDescriptor.Description,
		"privacyClass":    toolDescriptor.PrivacyClass,
		"visibility":      toolDescriptor.Visibility,
		"sideEffectClass": toolDescriptor.SideEffectClass,
		"policyResource":  toolDescriptor.PolicyResource,
		"completion.mode": toolDescriptor.Completion.Mode,
		"idempotency":     toolDescriptor.Idempotency,
	}
	for fieldName, fieldValue := range requiredValues {
		if fieldValue == "" {
			return errors.New(fieldName + " is required")
		}
	}
	if !IsCanonicalToolName(toolDescriptor.Name) {
		return errors.New("name must match ^[A-Za-z0-9_.-]{1,128}$")
	}
	if boundTool.Handler == nil {
		return errors.New("handler is required")
	}
	if !isOneOf(toolDescriptor.Visibility, ToolVisibilityModel, ToolVisibilityInternal, ToolVisibilityControl) {
		return errors.New("visibility is invalid")
	}
	if toolDescriptor.Visibility == ToolVisibilityModel && toolDescriptor.ResultContract == nil {
		return errors.New("resultContract is required for model-visible tools")
	}
	if !isOneOf(
		toolDescriptor.SideEffectClass,
		ToolSideEffectNone,
		ToolSideEffectRead,
		ToolSideEffectComputation,
		ToolSideEffectStateChange,
		ToolSideEffectWorkspaceWrite,
		ToolSideEffectExternalWrite,
		ToolSideEffectApproval,
		ToolSideEffectConnect,
		ToolSideEffectDestructive,
		ToolSideEffectExternalSend,
		ToolSideEffectExternalPublish,
		ToolSideEffectLocalFile,
		ToolSideEffectPlatformReply,
		ToolSideEffectSitePublish,
	) {
		return errors.New("sideEffectClass is invalid")
	}
	if !isOneOf(toolDescriptor.Completion.Mode, ToolCompletionNone, ToolCompletionObservation) {
		return errors.New("completion.mode is invalid")
	}
	if !isOneOf(toolDescriptor.Idempotency, ToolIdempotencyNone, ToolIdempotencySupported, ToolIdempotencyRequired) {
		return errors.New("idempotency is invalid")
	}
	if toolDescriptor.Idempotency != ToolIdempotencyNone && toolDescriptor.IdempotencyScope == "" {
		return errors.New("idempotencyScope is required when idempotency is supported or required")
	}
	if !isOneOf(boundTool.Availability.Status, ToolAvailabilityAvailable, ToolAvailabilityAsk, ToolAvailabilityUnavailable, ToolAvailabilityDenied) {
		return errors.New("availability.status is invalid")
	}
	if errorValue := validateToolSchema("inputSchema", toolDescriptor.InputSchema, true); errorValue != nil {
		return errorValue
	}
	if ToolDescriptorRequiresInputIntentSchema(toolDescriptor) && len(toolDescriptor.InputIntentSchema) == 0 {
		return errors.New("inputIntentSchema is required for model-visible state-changing tools")
	}
	if len(toolDescriptor.InputIntentSchema) > 0 {
		if errorValue := validateToolSchema("inputIntentSchema", toolDescriptor.InputIntentSchema, true); errorValue != nil {
			return errorValue
		}
		if errorValue := validateInputIntentSchema(toolDescriptor); errorValue != nil {
			return errorValue
		}
	}
	if errorValue := validateToolSchema("outputSchema", toolDescriptor.OutputSchema, true); errorValue != nil {
		return errorValue
	}
	return validateToolResultContract(toolDescriptor.ResultContract)
}

func validateInputIntentSchema(toolDescriptor ToolDescriptor) error {
	if _, errorValue := ValidateToolInput(toolDescriptor.InputIntentSchema, json.RawMessage(`{}`)); errorValue != nil {
		return errors.New("inputIntentSchema must accept an empty object")
	}
	inputProperties, errorValue := toolSchemaPropertyNames(toolDescriptor.InputSchema)
	if errorValue != nil {
		return errorValue
	}
	intentProperties, errorValue := toolSchemaPropertyNames(toolDescriptor.InputIntentSchema)
	if errorValue != nil {
		return errorValue
	}
	for propertyName := range intentProperties {
		if !inputProperties[propertyName] {
			return errors.New("inputIntentSchema property is absent from inputSchema: " + propertyName)
		}
	}
	return nil
}

func toolSchemaPropertyNames(schema json.RawMessage) (map[string]bool, error) {
	var document struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if errorValue := json.Unmarshal(schema, &document); errorValue != nil {
		return nil, errorValue
	}
	properties := make(map[string]bool, len(document.Properties))
	for propertyName := range document.Properties {
		properties[propertyName] = true
	}
	return properties, nil
}

func validateToolResultContract(contract *ToolResultContract) error {
	if contract == nil {
		return nil
	}
	if errorValue := validateToolSchema("resultContract.schema", contract.Schema, true); errorValue != nil {
		return errorValue
	}
	if errorValue := validateEvidenceCondition(contract.Schema, contract.EvidenceCondition); errorValue != nil {
		return errorValue
	}
	seenEffects := map[string]bool{}
	for _, effectContract := range contract.Effects {
		objectType := strings.TrimSpace(effectContract.ObjectType)
		effect := strings.TrimSpace(effectContract.Effect)
		resultField := strings.TrimSpace(effectContract.ResultField)
		if objectType == "" || effect == "" || resultField == "" {
			return errors.New("resultContract effect must include objectType, effect, and resultField")
		}
		if !isOneOf(strings.TrimSpace(effectContract.EffectIdentity), "id", "path", "url") {
			return errors.New("resultContract effectIdentity is invalid")
		}
		if effectContract.When == nil {
			if !schemaRequiresEffectIdentityField(contract.Schema, resultField) {
				return errors.New("resultContract resultField must name a required string or nonempty unique string array property")
			}
		} else {
			if !schemaDefinesEffectIdentityField(contract.Schema, resultField) {
				return errors.New("resultContract conditional effect resultField must name a string or nonempty unique string array property")
			}
			if errorValue := validateEvidenceCondition(contract.Schema, effectContract.When); errorValue != nil {
				return errors.New("resultContract effect when condition is invalid: " + errorValue.Error())
			}
		}
		effectKey := objectType + "\x00" + effect + "\x00" + strings.TrimSpace(effectContract.EffectIdentity)
		if seenEffects[effectKey] {
			return errors.New("resultContract effect is duplicated")
		}
		seenEffects[effectKey] = true
	}
	return nil
}

func copyEvidenceCondition(condition *EvidenceCondition) *EvidenceCondition {
	if condition == nil {
		return nil
	}
	return &EvidenceCondition{
		ResultField: strings.TrimSpace(condition.ResultField),
		Equals:      append(json.RawMessage{}, condition.Equals...),
	}
}

func copyResourceEffectContracts(effects []ResourceEffectContract) []ResourceEffectContract {
	copiedEffects := make([]ResourceEffectContract, len(effects))
	for index, effect := range effects {
		effect.When = copyEvidenceCondition(effect.When)
		copiedEffects[index] = effect
	}
	return copiedEffects
}

func validateEvidenceCondition(schema json.RawMessage, condition *EvidenceCondition) error {
	if condition == nil {
		return nil
	}
	if len(bytes.TrimSpace(condition.Equals)) == 0 || !json.Valid(condition.Equals) {
		return errors.New("resultContract evidenceCondition.equals must be valid JSON")
	}
	if !schemaAcceptsEvidenceValue(schema, strings.TrimSpace(condition.ResultField), condition.Equals) {
		return errors.New("resultContract evidenceCondition must match a required result property")
	}
	return nil
}

func schemaAcceptsEvidenceValue(document json.RawMessage, fieldName string, value json.RawMessage) bool {
	var schema jsonschema.Schema
	if json.Unmarshal(document, &schema) != nil || !slices.Contains(schema.Required, fieldName) {
		return false
	}
	property, isDefined := schema.Properties[fieldName]
	if !isDefined {
		return false
	}
	var instance any
	if json.Unmarshal(value, &instance) != nil {
		return false
	}
	resolvedProperty, errorValue := property.Resolve(nil)
	return errorValue == nil && resolvedProperty.Validate(instance) == nil
}

func schemaRequiresEffectIdentityField(document json.RawMessage, fieldName string) bool {
	var schema struct {
		Required []string `json:"required"`
	}
	if json.Unmarshal(document, &schema) != nil || !slices.Contains(schema.Required, fieldName) {
		return false
	}
	return schemaDefinesEffectIdentityField(document, fieldName)
}

func schemaDefinesEffectIdentityField(document json.RawMessage, fieldName string) bool {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(document, &schema) != nil {
		return false
	}
	var property struct {
		Type        string `json:"type"`
		MinItems    int    `json:"minItems"`
		UniqueItems bool   `json:"uniqueItems"`
		Items       struct {
			Type string `json:"type"`
		} `json:"items"`
	}
	if json.Unmarshal(schema.Properties[fieldName], &property) != nil {
		return false
	}
	return property.Type == "string" ||
		property.Type == "array" && property.Items.Type == "string" && property.MinItems >= 1 && property.UniqueItems
}

func validateToolSchema(fieldName string, schema json.RawMessage, requiresObject bool) error {
	if len(bytes.TrimSpace(schema)) == 0 {
		return errors.New(fieldName + " is required")
	}
	var document map[string]any
	if errorValue := json.Unmarshal(schema, &document); errorValue != nil {
		return fmt.Errorf("%s is invalid JSON: %w", fieldName, errorValue)
	}
	if len(document) == 0 {
		return errors.New(fieldName + " is empty")
	}
	if requiresObject && document["type"] != "object" {
		return errors.New(fieldName + " must describe an object")
	}
	var compiledSchema jsonschema.Schema
	if errorValue := json.Unmarshal(schema, &compiledSchema); errorValue != nil {
		return fmt.Errorf("%s cannot be compiled: %w", fieldName, errorValue)
	}
	if _, errorValue := compiledSchema.Resolve(nil); errorValue != nil {
		return fmt.Errorf("%s cannot be resolved: %w", fieldName, errorValue)
	}
	return nil
}

func (toolSet *ToolSet) validateProviderCollisions(boundTools []BoundTool) error {
	for _, boundTool := range boundTools {
		toolDescriptor := boundTool.Definition
		if _, isRegistered := toolSet.boundToolByName[toolDescriptor.Name]; isRegistered {
			return errors.New("tool name is already registered: " + toolDescriptor.Name)
		}
		if registeredToolName, isRegistered := toolSet.boundToolNameByID[toolDescriptor.ID]; isRegistered {
			return errors.New("tool identifier is already registered by " + registeredToolName + ": " + toolDescriptor.ID)
		}
	}
	return nil
}

func isOneOf(value string, expectedValues ...string) bool {
	for _, expectedValue := range expectedValues {
		if value == expectedValue {
			return true
		}
	}
	return false
}
