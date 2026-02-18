package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueclaw/blueclaw/internal/provider"
	"github.com/blueclaw/blueclaw/internal/tool"
)

type PromptContext struct {
	BlueclawDirectory string
	ToolRegistry      *tool.Registry
}

func BuildRequest(promptContext PromptContext, session *Session) (provider.Request, error) {
	systemPrompt, err := buildSystemPrompt(promptContext.BlueclawDirectory)
	if err != nil {
		return provider.Request{}, fmt.Errorf("building system prompt: %w", err)
	}
	var toolDefinitions []provider.ToolDefinition
	if promptContext.ToolRegistry != nil {
		toolDefinitions = convertToolDefinitions(promptContext.ToolRegistry.ListDefinitions())
	}
	return provider.Request{
		SystemPrompt:    systemPrompt,
		Messages:        session.Messages,
		ToolDefinitions: toolDefinitions,
	}, nil
}

func buildSystemPrompt(blueclawDirectory string) (string, error) {
	soulPath := filepath.Join(blueclawDirectory, "SOUL.md")
	soulContent, err := os.ReadFile(soulPath)
	if err != nil {
		if os.IsNotExist(err) {
			return appendTimeContext("You are Blueclaw, a helpful and concise personal AI assistant. Always respond in the same language the user writes in."), nil
		}
		return "", fmt.Errorf("reading SOUL.md: %w", err)
	}
	return appendTimeContext(strings.TrimRight(string(soulContent), "\n")), nil
}

func appendTimeContext(soulContent string) string {
	return fmt.Sprintf("%s\n\nCurrent time: %s", soulContent, time.Now().Format("Monday, January 2, 2006 at 3:04 PM MST"))
}

func convertToolDefinitions(definitions []tool.Definition) []provider.ToolDefinition {
	providerDefinitions := make([]provider.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		providerDefinitions = append(providerDefinitions, provider.ToolDefinition{
			Name:        definition.Name,
			Description: definition.Description,
			Parameters:  definition.Parameters,
		})
	}
	return providerDefinitions
}
