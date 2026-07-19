package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/config"
	"github.com/google/jsonschema-go/jsonschema"
)

const serverValidationTimeout = 3 * time.Second

type McpRegistry struct {
	mutex            sync.RWMutex
	serverClient     ServerClient
	serverDefinition map[string]*registeredServer
}

type registeredServer struct {
	definition ServerDefinition
	session    *serverSession
}

func NewMcpRegistry() *McpRegistry {
	return &McpRegistry{
		serverClient:     ServerClient{},
		serverDefinition: map[string]*registeredServer{},
	}
}

func (mcpRegistry *McpRegistry) LoadServerDefinition(configurations []config.MCPServerConfiguration) LoadReport {
	loadReport := LoadReport{Quarantined: []QuarantinedServer{}}
	serverNameCount := mcpServerNameCount(configurations)
	reportedDuplicateName := map[string]bool{}
	for _, configuration := range configurations {
		serverName := strings.TrimSpace(configuration.Name)
		if serverNameCount[serverName] > 1 {
			mcpRegistry.removeServer(serverName)
			if !reportedDuplicateName[serverName] {
				loadReport.Quarantined = append(loadReport.Quarantined, quarantine(serverName, "duplicate server name"))
				reportedDuplicateName[serverName] = true
			}
			continue
		}
		serverDefinition, errorValue := buildServerDefinition(configuration)
		if errorValue != nil {
			mcpRegistry.removeServer(serverName)
			loadReport.Quarantined = append(loadReport.Quarantined, quarantine(serverName, "invalid configuration"))
			continue
		}
		validationContext, cancelValidation := context.WithTimeout(context.Background(), serverValidationTimeout)
		session, connectError := mcpRegistry.serverClient.Connect(validationContext, serverDefinition)
		var discoveredTools []ToolDefinition
		if connectError == nil {
			discoveredTools, connectError = discoverTools(validationContext, mcpRegistry.serverClient, session, serverDefinition)
		}
		cancelValidation()
		if connectError != nil {
			mcpRegistry.removeServer(serverDefinition.Name)
			if session != nil {
				_ = session.session.Close()
			}
			loadReport.Quarantined = append(loadReport.Quarantined, quarantine(serverDefinition.Name, "server unavailable"))
			continue
		}
		mcpRegistry.mutex.Lock()
		serverDefinition.Tools = discoveredTools
		previousServer := mcpRegistry.serverDefinition[serverDefinition.Name]
		mcpRegistry.serverDefinition[serverDefinition.Name] = &registeredServer{definition: serverDefinition, session: session}
		mcpRegistry.mutex.Unlock()
		if previousServer != nil {
			_ = previousServer.session.session.Close()
		}
	}
	return loadReport
}

func mcpServerNameCount(configurations []config.MCPServerConfiguration) map[string]int {
	serverNameCount := map[string]int{}
	for _, configuration := range configurations {
		serverNameCount[strings.TrimSpace(configuration.Name)]++
	}
	return serverNameCount
}

func (mcpRegistry *McpRegistry) removeServer(name string) {
	mcpRegistry.mutex.Lock()
	previousServer := mcpRegistry.serverDefinition[name]
	delete(mcpRegistry.serverDefinition, name)
	mcpRegistry.mutex.Unlock()
	if previousServer != nil {
		_ = previousServer.session.session.Close()
	}
}

func (mcpRegistry *McpRegistry) ListTool() []ToolDefinition {
	mcpRegistry.mutex.RLock()
	defer mcpRegistry.mutex.RUnlock()

	toolDefinitions := []ToolDefinition{}
	for _, server := range mcpRegistry.serverDefinition {
		toolDefinitions = append(toolDefinitions, server.definition.Tools...)
	}
	return toolDefinitions
}

func (mcpRegistry *McpRegistry) InvokeTool(ctx context.Context, invocation Invocation) (string, error) {
	mcpRegistry.mutex.RLock()
	server, isFound := mcpRegistry.serverDefinition[invocation.ServerName]
	mcpRegistry.mutex.RUnlock()
	if !isFound {
		return "", errors.New("mcp server definition not found")
	}
	if !serverHasTool(server.definition, invocation.ToolName) {
		return "", errors.New("mcp tool definition not found")
	}
	invocation.ToolName = serverRemoteToolName(server.definition, invocation.ToolName)
	return mcpRegistry.serverClient.InvokeTool(ctx, server.session, invocation)
}

func (mcpRegistry *McpRegistry) Close() error {
	mcpRegistry.mutex.Lock()
	defer mcpRegistry.mutex.Unlock()

	var firstError error
	for name, server := range mcpRegistry.serverDefinition {
		if errorValue := server.session.session.Close(); errorValue != nil && firstError == nil {
			firstError = errorValue
		}
		delete(mcpRegistry.serverDefinition, name)
	}
	return firstError
}

func buildServerDefinition(configuration config.MCPServerConfiguration) (ServerDefinition, error) {
	if strings.TrimSpace(configuration.Name) == "" || len(configuration.Tools) == 0 {
		return ServerDefinition{}, errors.New("mcp server requires canonical tool definitions")
	}
	if configuration.Transport != TransportStdio && configuration.Transport != TransportStreamableHTTP {
		return ServerDefinition{}, errors.New("mcp server transport is unsupported")
	}
	serverDefinition := ServerDefinition{
		Name:      strings.TrimSpace(configuration.Name),
		Transport: configuration.Transport,
		Command:   strings.TrimSpace(configuration.Command),
		Arguments: append([]string{}, configuration.Arguments...),
		Endpoint:  strings.TrimSpace(configuration.Endpoint),
		Tools:     make([]ToolDefinition, 0, len(configuration.Tools)),
	}
	seenToolNames := map[string]bool{}
	seenRemoteNames := map[string]bool{}
	for _, toolConfiguration := range configuration.Tools {
		toolDefinition, errorValue := buildToolDefinition(serverDefinition.Name, toolConfiguration)
		if errorValue != nil || seenToolNames[toolDefinition.Name] || seenRemoteNames[toolDefinition.remoteName] {
			return ServerDefinition{}, errors.New("mcp tool metadata is invalid")
		}
		seenToolNames[toolDefinition.Name] = true
		seenRemoteNames[toolDefinition.remoteName] = true
		serverDefinition.Tools = append(serverDefinition.Tools, toolDefinition)
	}
	return serverDefinition, nil
}

func buildToolDefinition(serverName string, configuration config.MCPToolConfiguration) (ToolDefinition, error) {
	toolName := strings.TrimSpace(configuration.Name)
	namespace := strings.TrimSpace(configuration.Namespace)
	if toolName == "" ||
		namespace == "" ||
		strings.TrimSpace(configuration.Description) == "" ||
		configuration.Policy == nil ||
		!isObjectSchema(configuration.InputSchema) ||
		!isObjectSchema(configuration.OutputSchema) {
		return ToolDefinition{}, errors.New("mcp tool metadata is incomplete")
	}
	if !validPolicyMetadata(*configuration.Policy) {
		return ToolDefinition{}, errors.New("mcp tool metadata is incomplete")
	}
	if agent.ToolDescriptorRequiresInputIntentSchema(agent.ToolDescriptor{
		Visibility:      strings.TrimSpace(configuration.Policy.ModelVisibility),
		SideEffectClass: strings.TrimSpace(configuration.Policy.SideEffectClass),
	}) && !isObjectSchema(configuration.InputIntentSchema) {
		return ToolDefinition{}, errors.New("mcp state-changing tool requires inputIntentSchema")
	}
	if len(configuration.InputIntentSchema) > 0 && !isObjectSchema(configuration.InputIntentSchema) {
		return ToolDefinition{}, errors.New("mcp tool inputIntentSchema must describe an object")
	}
	resultContract, errorValue := buildToolResultContract(configuration.OutputSchema, configuration.ResultContract)
	if errorValue != nil {
		return ToolDefinition{}, errorValue
	}
	return ToolDefinition{
		Name:              qualifiedToolName(namespace, toolName),
		Namespace:         namespace,
		ServerName:        serverName,
		Description:       strings.TrimSpace(configuration.Description),
		InputSchema:       append([]byte{}, configuration.InputSchema...),
		InputIntentSchema: append([]byte{}, configuration.InputIntentSchema...),
		OutputSchema:      append([]byte{}, configuration.OutputSchema...),
		ResultContract:    resultContract,
		Policy:            policyMetadata(*configuration.Policy),
		remoteName:        toolName,
	}, nil
}

func buildToolResultContract(outputSchema json.RawMessage, configuration *config.MCPToolResultContract) (*ToolResultContract, error) {
	if configuration == nil || !schemasEqual(outputSchema, configuration.Schema) {
		return nil, errors.New("mcp tool result contract must match output schema")
	}
	effects := make([]ResourceEffectContract, 0, len(configuration.Effects))
	seenEffects := map[string]bool{}
	for _, effect := range configuration.Effects {
		normalizedEffect, errorValue := buildResourceEffectContract(configuration.Schema, effect)
		if errorValue != nil {
			return nil, errorValue
		}
		effectKey := normalizedEffect.ObjectType + "\x00" + normalizedEffect.Effect
		if seenEffects[effectKey] {
			return nil, errors.New("mcp tool result contract effect is duplicated")
		}
		seenEffects[effectKey] = true
		effects = append(effects, normalizedEffect)
	}
	evidenceCondition, errorValue := buildEvidenceCondition(configuration.Schema, configuration.EvidenceCondition)
	if errorValue != nil {
		return nil, errorValue
	}
	return &ToolResultContract{
		Schema:            append([]byte{}, configuration.Schema...),
		Effects:           effects,
		EvidenceCondition: evidenceCondition,
	}, nil
}

func buildEvidenceCondition(schema json.RawMessage, configuration *config.EvidenceCondition) (*EvidenceCondition, error) {
	if configuration == nil {
		return nil, nil
	}
	resultField := strings.TrimSpace(configuration.ResultField)
	if len(bytes.TrimSpace(configuration.Equals)) == 0 || !json.Valid(configuration.Equals) {
		return nil, errors.New("mcp tool evidence condition equals must be valid JSON")
	}
	if !schemaAcceptsEvidenceValue(schema, resultField, configuration.Equals) {
		return nil, errors.New("mcp tool evidence condition must match a required result property")
	}
	return &EvidenceCondition{
		ResultField: resultField,
		Equals:      append(json.RawMessage{}, configuration.Equals...),
	}, nil
}

func buildResourceEffectContract(schema json.RawMessage, configuration config.MCPResourceEffectContract) (ResourceEffectContract, error) {
	effect := ResourceEffectContract{
		ObjectType:     strings.TrimSpace(configuration.ObjectType),
		Effect:         strings.TrimSpace(configuration.Effect),
		ResultField:    strings.TrimSpace(configuration.ResultField),
		EffectIdentity: strings.TrimSpace(configuration.EffectIdentity),
	}
	if effect.ObjectType == "" || effect.Effect == "" || effect.ResultField == "" {
		return ResourceEffectContract{}, errors.New("mcp tool result contract effect metadata is incomplete")
	}
	if effect.EffectIdentity != "id" && effect.EffectIdentity != "path" && effect.EffectIdentity != "url" {
		return ResourceEffectContract{}, errors.New("mcp tool result contract effect identity is invalid")
	}
	if !schemaRequiresEffectIdentityField(schema, effect.ResultField) {
		return ResourceEffectContract{}, errors.New("mcp tool result contract result field must name a required string or nonempty unique string array property")
	}
	return effect, nil
}

func schemaRequiresEffectIdentityField(schema json.RawMessage, fieldName string) bool {
	var document struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if json.Unmarshal(schema, &document) != nil {
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
	if json.Unmarshal(document.Properties[fieldName], &property) != nil {
		return false
	}
	if !slices.Contains(document.Required, fieldName) {
		return false
	}
	return property.Type == "string" ||
		property.Type == "array" && property.Items.Type == "string" && property.MinItems >= 1 && property.UniqueItems
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

func policyMetadata(policy config.MCPToolPolicyMetadata) PolicyMetadata {
	return PolicyMetadata{
		PrivacyClass:         policy.PrivacyClass,
		RequiresUserPresence: policy.RequiresUserPresence,
		WorksOffline:         policy.WorksOffline,
		ModelVisibility:      policy.ModelVisibility,
		PolicyResource:       policy.PolicyResource,
		SideEffectClass:      policy.SideEffectClass,
		RequiresApproval:     policy.RequiresApproval,
		CompletionMode:       policy.CompletionMode,
		CompletionAction:     policy.CompletionAction,
		CompletionTargetKind: policy.CompletionTargetKind,
		Idempotency:          policy.Idempotency,
		IdempotencyScope:     policy.IdempotencyScope,
	}
}

func validPolicyMetadata(policy config.MCPToolPolicyMetadata) bool {
	if strings.TrimSpace(policy.PrivacyClass) == "" ||
		strings.TrimSpace(policy.ModelVisibility) == "" ||
		strings.TrimSpace(policy.PolicyResource) == "" ||
		strings.TrimSpace(policy.SideEffectClass) == "" ||
		strings.TrimSpace(policy.CompletionMode) == "" ||
		strings.TrimSpace(policy.Idempotency) == "" ||
		strings.TrimSpace(policy.IdempotencyScope) == "" {
		return false
	}
	completionMode := strings.TrimSpace(policy.CompletionMode)
	if completionMode != "none" && completionMode != "observation" {
		return false
	}
	if completionMode == "observation" && (strings.TrimSpace(policy.CompletionAction) == "" || strings.TrimSpace(policy.CompletionTargetKind) == "") {
		return false
	}
	return true
}

func discoverTools(ctx context.Context, serverClient ServerClient, session *serverSession, serverDefinition ServerDefinition) ([]ToolDefinition, error) {
	protocolTools, errorValue := serverClient.ListTools(ctx, session)
	if errorValue != nil {
		return nil, errorValue
	}
	configuredTools := map[string]ToolDefinition{}
	for _, toolDefinition := range serverDefinition.Tools {
		configuredTools[toolDefinition.remoteName] = toolDefinition
	}
	discoveredTools := make([]ToolDefinition, 0, len(protocolTools))
	for _, protocolTool := range protocolTools {
		if protocolTool == nil {
			continue
		}
		toolDefinition, isConfigured := configuredTools[protocolTool.Name]
		if !isConfigured ||
			!schemasEqual(toolDefinition.InputSchema, protocolTool.InputSchema) ||
			!schemasEqual(toolDefinition.OutputSchema, protocolTool.OutputSchema) {
			continue
		}
		discoveredTools = append(discoveredTools, toolDefinition)
	}
	if len(discoveredTools) != len(configuredTools) {
		return nil, errors.New("mcp server omitted configured tools")
	}
	return discoveredTools, nil
}

func schemasEqual(configuredSchema any, discoveredSchema any) bool {
	var configuredDocument any
	var discoveredDocument any
	configuredBytes, configuredError := json.Marshal(configuredSchema)
	discoveredBytes, discoveredError := json.Marshal(discoveredSchema)
	if configuredError != nil || discoveredError != nil {
		return false
	}
	if json.Unmarshal(configuredBytes, &configuredDocument) != nil || json.Unmarshal(discoveredBytes, &discoveredDocument) != nil {
		return false
	}
	return reflect.DeepEqual(configuredDocument, discoveredDocument) && isObjectSchema(discoveredSchema)
}

func isObjectSchema(schema any) bool {
	encoded, errorValue := json.Marshal(schema)
	if errorValue != nil || len(encoded) == 0 {
		return false
	}
	var document struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(encoded, &document) == nil && document.Type == "object"
}

func serverHasTool(serverDefinition ServerDefinition, toolName string) bool {
	for _, toolDefinition := range serverDefinition.Tools {
		if toolDefinition.Name == toolName {
			return true
		}
	}
	return false
}

func serverRemoteToolName(serverDefinition ServerDefinition, toolName string) string {
	for _, toolDefinition := range serverDefinition.Tools {
		if toolDefinition.Name == toolName {
			return toolDefinition.remoteName
		}
	}
	return toolName
}

func qualifiedToolName(namespace string, toolName string) string {
	if toolName == namespace || strings.HasPrefix(toolName, namespace+".") {
		return toolName
	}
	return namespace + "." + toolName
}

func quarantine(name string, reason string) QuarantinedServer {
	return QuarantinedServer{Name: strings.TrimSpace(name), Reason: reason}
}
