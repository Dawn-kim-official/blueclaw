package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

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
	toolDescriptor.Completion.Mode = strings.TrimSpace(toolDescriptor.Completion.Mode)
	toolDescriptor.Completion.Action = strings.TrimSpace(toolDescriptor.Completion.Action)
	toolDescriptor.Completion.TargetKind = strings.TrimSpace(toolDescriptor.Completion.TargetKind)
	normalizedInputSchema, errorValue := normalizeProviderToolSchema(toolDescriptor.InputSchema)
	if errorValue != nil {
		return BoundTool{}, fmt.Errorf("invalid tool descriptor %s: inputSchema %w", firstNonEmptyString(toolDescriptor.ID, toolDescriptor.Name), errorValue)
	}
	normalizedOutputSchema, errorValue := normalizeProviderToolSchema(toolDescriptor.OutputSchema)
	if errorValue != nil {
		return BoundTool{}, fmt.Errorf("invalid tool descriptor %s: outputSchema %w", firstNonEmptyString(toolDescriptor.ID, toolDescriptor.Name), errorValue)
	}
	toolDescriptor.InputSchema = normalizedInputSchema
	toolDescriptor.OutputSchema = normalizedOutputSchema
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
	if errorValue := closeProviderSchemaObjects(document); errorValue != nil {
		return nil, errorValue
	}
	return json.Marshal(document)
}

func closeProviderSchemaObjects(value any) error {
	switch document := value.(type) {
	case []any:
		for _, item := range document {
			if errorValue := closeProviderSchemaObjects(item); errorValue != nil {
				return errorValue
			}
		}
	case map[string]any:
		if document["type"] == "object" {
			additionalProperties, exists := document["additionalProperties"]
			if !exists {
				document["additionalProperties"] = false
			} else if !isClosedAdditionalProperties(additionalProperties) {
				return errors.New("must not allow additional properties")
			}
		}
		for _, child := range document {
			if errorValue := closeProviderSchemaObjects(child); errorValue != nil {
				return errorValue
			}
		}
	}
	return nil
}

func isClosedAdditionalProperties(value any) bool {
	switch additionalProperties := value.(type) {
	case bool:
		return !additionalProperties
	case map[string]any:
		return len(additionalProperties) > 0
	default:
		return false
	}
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
	if boundTool.Handler == nil {
		return errors.New("handler is required")
	}
	if !isOneOf(toolDescriptor.Visibility, ToolVisibilityModel, ToolVisibilityInternal, ToolVisibilityControl) {
		return errors.New("visibility is invalid")
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
	if toolDescriptor.Completion.Mode == ToolCompletionObservation && (toolDescriptor.Completion.Action == "" || toolDescriptor.Completion.TargetKind == "") {
		return errors.New("completion.action and completion.targetKind are required for observation")
	}
	if !isOneOf(toolDescriptor.Idempotency, ToolIdempotencyNone, ToolIdempotencySupported, ToolIdempotencyRequired) {
		return errors.New("idempotency is invalid")
	}
	if !isOneOf(boundTool.Availability.Status, ToolAvailabilityAvailable, ToolAvailabilityAsk, ToolAvailabilityUnavailable, ToolAvailabilityDenied) {
		return errors.New("availability.status is invalid")
	}
	if errorValue := validateToolSchema("inputSchema", toolDescriptor.InputSchema, true); errorValue != nil {
		return errorValue
	}
	return validateToolSchema("outputSchema", toolDescriptor.OutputSchema, true)
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
