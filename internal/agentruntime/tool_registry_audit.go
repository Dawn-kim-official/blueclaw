package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"blueclaw/internal/agent"
)

const toolRegistryVersion = "platform-message-v1"

var newPlatformMessageToolNames = []string{
	"platform.message.context",
	"platform.message.search",
	"platform.message.send",
	"platform.message.update",
	"platform.message.delete",
}

var oldPlatformMessageToolNames = []string{
	"platform.dm.inspect",
	"platform.dm.send",
	"mattermost.context.inspect",
	"mattermost.post.search",
	"mattermost.channel.posts.list",
	"mattermost.channel.post",
	"mattermost.post.update",
	"mattermost.post.delete",
}

type ToolRegistryAudit struct {
	ToolRegistryVersion            string `json:"toolRegistryVersion"`
	CapabilityDescriptorHash       string `json:"capabilityDescriptorHash"`
	LiveCapabilityHash             string `json:"liveCapabilityHash,omitempty"`
	AllowedToolHash                string `json:"allowedToolHash"`
	HasPlatformMessageDelete       bool   `json:"hasPlatformMessageDelete"`
	HasOldMattermostPostDelete     bool   `json:"hasOldMattermostPostDelete"`
	HasOldPlatformDMInspect        bool   `json:"hasOldPlatformDMInspect"`
	LiveHasPlatformMessageDelete   bool   `json:"liveHasPlatformMessageDelete,omitempty"`
	LiveHasOldMattermostPostDelete bool   `json:"liveHasOldMattermostPostDelete,omitempty"`
	LiveHasOldPlatformDMInspect    bool   `json:"liveHasOldPlatformDMInspect,omitempty"`
}

type capabilityRegistryResponse struct {
	DeviceCapabilities []capabilityRegistryDescriptor `json:"deviceCapabilities"`
}

type capabilityRegistryDescriptor struct {
	Name        string          `json:"name"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type toolRegistryMismatchError struct {
	audit ToolRegistryAudit
}

func (errorValue toolRegistryMismatchError) Error() string {
	return fmt.Sprintf("runtime_registry_mismatch: configuredHash=%s liveHash=%s platformMessageDelete=%t livePlatformMessageDelete=%t oldMattermostPostDelete=%t liveOldMattermostPostDelete=%t oldPlatformDMInspect=%t liveOldPlatformDMInspect=%t",
		errorValue.audit.CapabilityDescriptorHash,
		errorValue.audit.LiveCapabilityHash,
		errorValue.audit.HasPlatformMessageDelete,
		errorValue.audit.LiveHasPlatformMessageDelete,
		errorValue.audit.HasOldMattermostPostDelete,
		errorValue.audit.LiveHasOldMattermostPostDelete,
		errorValue.audit.HasOldPlatformDMInspect,
		errorValue.audit.LiveHasOldPlatformDMInspect,
	)
}

func (toolCatalogBuilder *ToolCatalogBuilder) BuildToolRegistryAudit(ctx context.Context, toolSet *agent.ToolSet) (ToolRegistryAudit, error) {
	configuredDescriptors := toolCatalogBuilder.capabilityToolDefinitions()
	configuredNames := capabilityDescriptorNames(configuredDescriptors)
	allowedToolNames := []string{}
	if toolSet != nil {
		allowedToolNames = toolSet.ListToolNames()
	}

	audit := ToolRegistryAudit{
		ToolRegistryVersion:        toolRegistryVersion,
		CapabilityDescriptorHash:   hashStrings(configuredNames),
		AllowedToolHash:            hashStrings(allowedToolNames),
		HasPlatformMessageDelete:   registryContainsString(configuredNames, "platform.message.delete"),
		HasOldMattermostPostDelete: registryContainsString(configuredNames, "mattermost.post.delete"),
		HasOldPlatformDMInspect:    registryContainsString(configuredNames, "platform.dm.inspect"),
	}

	if !requiresLiveMessageRegistryCheck(configuredNames) {
		return audit, nil
	}

	liveNames, liveHash, errorValue := toolCatalogBuilder.liveCapabilityToolNames(ctx)
	if errorValue != nil {
		return audit, fmt.Errorf("runtime_registry_mismatch: live capability registry unavailable: %w", errorValue)
	}
	audit.LiveCapabilityHash = liveHash
	audit.LiveHasPlatformMessageDelete = registryContainsString(liveNames, "platform.message.delete")
	audit.LiveHasOldMattermostPostDelete = registryContainsString(liveNames, "mattermost.post.delete")
	audit.LiveHasOldPlatformDMInspect = registryContainsString(liveNames, "platform.dm.inspect")

	if hasMessageRegistryMismatch(audit) {
		return audit, toolRegistryMismatchError{audit: audit}
	}

	return audit, nil
}

func capabilityDescriptorNames(toolDescriptors []CapabilityToolDescriptor) []string {
	toolNames := []string{}
	for _, toolDescriptor := range toolDescriptors {
		toolName := strings.TrimSpace(toolDescriptor.Name)
		if toolName != "" {
			toolNames = append(toolNames, toolName)
		}
	}
	return sortedUniqueRegistryStrings(toolNames)
}

func requiresLiveMessageRegistryCheck(toolNames []string) bool {
	return registryContainsString(toolNames, "platform.message.delete") || registryContainsAnyString(toolNames, oldPlatformMessageToolNames)
}

func (toolCatalogBuilder *ToolCatalogBuilder) liveCapabilityToolNames(ctx context.Context) ([]string, string, error) {
	var response capabilityRegistryResponse
	if errorValue := toolCatalogBuilder.capabilityClient.GetJSON(ctx, "/v1/capabilities", &response); errorValue != nil {
		return nil, "", errorValue
	}
	toolNames := []string{}
	for _, descriptor := range response.DeviceCapabilities {
		toolName := strings.TrimSpace(descriptor.Name)
		if toolName != "" {
			toolNames = append(toolNames, toolName)
		}
	}
	toolNames = sortedUniqueRegistryStrings(toolNames)
	return toolNames, hashStrings(toolNames), nil
}

func hasMessageRegistryMismatch(audit ToolRegistryAudit) bool {
	if audit.HasOldMattermostPostDelete || audit.HasOldPlatformDMInspect {
		return true
	}
	if audit.LiveHasOldMattermostPostDelete || audit.LiveHasOldPlatformDMInspect {
		return true
	}
	return audit.HasPlatformMessageDelete != audit.LiveHasPlatformMessageDelete
}

func hashStrings(values []string) string {
	normalizedValues := sortedUniqueRegistryStrings(values)
	document := strings.Join(normalizedValues, "\n")
	sum := sha256.Sum256([]byte(document))
	return hex.EncodeToString(sum[:])
}

func sortedUniqueRegistryStrings(values []string) []string {
	seenValues := map[string]bool{}
	result := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" || seenValues[trimmedValue] {
			continue
		}
		seenValues[trimmedValue] = true
		result = append(result, trimmedValue)
	}
	sort.Strings(result)
	return result
}

func registryContainsAnyString(values []string, candidates []string) bool {
	for _, candidate := range candidates {
		if registryContainsString(values, candidate) {
			return true
		}
	}
	return false
}

func registryContainsString(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}
