package agent

import (
	"fmt"
	"strings"

	"blueclaw/internal/memory"
)

const memorySummaryContentLimit = 240

func buildMemoryContext(memoryFacts []memory.MemoryFact) string {
	sections := []string{}
	userMemoryDescriptions := buildScopedMemoryDescriptions(memoryFacts, memory.ScopeTypeUser)
	workspaceMemoryDescriptions := buildScopedMemoryDescriptions(memoryFacts, memory.ScopeTypeWorkspace)
	conversationMemoryDescriptions := buildScopedMemoryDescriptions(memoryFacts, memory.ScopeTypeConversation)
	if len(userMemoryDescriptions) > 0 {
		sections = append(sections, "User memory:\n"+strings.Join(userMemoryDescriptions, "\n"))
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

func buildScopedMemoryDescriptions(memoryFacts []memory.MemoryFact, scopeType string) []string {
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

func formatMemorySummary(memoryFact memory.MemoryFact) string {
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

func memorySummaryAttributes(memoryFact memory.MemoryFact) []string {
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
	case memory.ScopeTypeWorkspace:
		return memory.ScopeTypeWorkspace
	case memory.ScopeTypeConversation:
		return memory.ScopeTypeConversation
	default:
		return memory.ScopeTypeUser
	}
}
