package agentcontract

import (
	"fmt"
	"strings"
)

const memorySummaryContentLimit = 240

func BuildMemoryContext(memoryFacts []MemoryFact) string {
	sections := []string{}
	userMemoryDescriptions := buildScopedMemoryDescriptions(memoryFacts, MemoryScopeUser)
	circleMemoryDescriptions := buildScopedMemoryDescriptions(memoryFacts, MemoryScopeCircle)
	workspaceMemoryDescriptions := buildScopedMemoryDescriptions(memoryFacts, MemoryScopeWorkspace)
	conversationMemoryDescriptions := buildScopedMemoryDescriptions(memoryFacts, MemoryScopeConversation)
	if len(userMemoryDescriptions) > 0 {
		sections = append(sections, "User memory:\n"+strings.Join(userMemoryDescriptions, "\n"))
	}
	if len(circleMemoryDescriptions) > 0 {
		sections = append(sections, "Circle memory:\n"+strings.Join(circleMemoryDescriptions, "\n"))
	}
	if len(workspaceMemoryDescriptions) > 0 {
		sections = append(sections, "Workspace memory:\n"+strings.Join(workspaceMemoryDescriptions, "\n"))
	}
	if len(conversationMemoryDescriptions) > 0 {
		sections = append(sections, "Conversation memory:\n"+strings.Join(conversationMemoryDescriptions, "\n"))
	}
	if len(sections) == 0 {
		return ""
	}
	return "Relevant Blueclaw memory (policy-filtered compact summaries):\n" + strings.Join(sections, "\n\n")
}

func buildScopedMemoryDescriptions(memoryFacts []MemoryFact, scopeType string) []string {
	descriptions := []string{}
	for _, memoryFact := range memoryFacts {
		if normalizedMemoryScope(memoryFact.ScopeType) != scopeType {
			continue
		}
		description := formatMemorySummary(memoryFact)
		if description != "" {
			descriptions = append(descriptions, "- "+description)
		}
	}
	return descriptions
}

func formatMemorySummary(memoryFact MemoryFact) string {
	content := compactMemoryContent(memoryFact.Content)
	if content == "" {
		return ""
	}
	attributes := memorySummaryAttributes(memoryFact)
	if len(attributes) == 0 {
		return content
	}
	return "[" + strings.Join(attributes, " ") + "] " + content
}

func memorySummaryAttributes(memoryFact MemoryFact) []string {
	attributes := []string{}
	if memoryFact.Score != 0 {
		attributes = append(attributes, fmt.Sprintf("score=%.2f", memoryFact.Score))
	}
	if strings.TrimSpace(memoryFact.SourceKind) != "" {
		attributes = append(attributes, "kind="+strings.TrimSpace(memoryFact.SourceKind))
	}
	if strings.TrimSpace(memoryFact.SourceEpisodeID) != "" {
		attributes = append(attributes, "source="+strings.TrimSpace(memoryFact.SourceEpisodeID))
	}
	if !memoryFact.ValidAt.IsZero() {
		attributes = append(attributes, "validAt="+memoryFact.ValidAt.Format("2006-01-02"))
	}
	return attributes
}

func compactMemoryContent(content string) string {
	trimmedContent := strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	if trimmedContent == "" {
		return ""
	}
	runes := []rune(trimmedContent)
	if len(runes) <= memorySummaryContentLimit {
		return trimmedContent
	}
	return string(runes[:memorySummaryContentLimit]) + "..."
}

func normalizedMemoryScope(scopeType string) string {
	switch strings.TrimSpace(scopeType) {
	case MemoryScopeCircle:
		return MemoryScopeCircle
	case MemoryScopeWorkspace:
		return MemoryScopeWorkspace
	case MemoryScopeConversation:
		return MemoryScopeConversation
	default:
		return MemoryScopeUser
	}
}
