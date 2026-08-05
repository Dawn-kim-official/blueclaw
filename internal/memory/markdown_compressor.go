package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/llm"
)

type LLMMarkdownMemoryCompressor struct {
	languageModel llm.LanguageModelProvider
}

type markdownMemoryCompressionDocument struct {
	Content string `json:"content"`
}

func NewLLMMarkdownMemoryCompressor(languageModel llm.LanguageModelProvider) LLMMarkdownMemoryCompressor {
	return LLMMarkdownMemoryCompressor{languageModel: languageModel}
}

func (compressor LLMMarkdownMemoryCompressor) CompressMemory(ctx context.Context, request MarkdownMemoryCompressionRequest) (string, error) {
	if compressor.languageModel == nil {
		return "", errors.New("memory compressor language model is unavailable")
	}
	response, errorValue := compressor.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: []llm.Message{
			{Role: "system", Content: markdownMemoryCompressionSystemPrompt(request)},
			{Role: "user", Content: markdownMemoryCompressionUserPrompt(request)},
		},
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_markdown_memory_compression",
			Document:           `{"type":"object","properties":{"content":{"type":"string"}},"required":["content"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return "", errorValue
	}
	var document markdownMemoryCompressionDocument
	if errorValue := json.Unmarshal([]byte(response.Content), &document); errorValue != nil {
		return "", errorValue
	}
	return strings.TrimSpace(document.Content), nil
}

func markdownMemoryCompressionSystemPrompt(request MarkdownMemoryCompressionRequest) string {
	return fmt.Sprintf("Compress a user-specific MEMORY.md file for Blueclaw. Preserve durable preferences, identity facts, naming preferences, repeated working style, and ongoing long-lived context. Aggressively forget one-off tasks, stale details, low-utility chatter, duplicate facts, and transient project minutiae. Summarize related bullets together. Return valid Markdown only, beginning with '# Memory'. Aim for about %d characters and never exceed %d characters.", request.TargetLength, request.HardLimit)
}

func markdownMemoryCompressionUserPrompt(request MarkdownMemoryCompressionRequest) string {
	return strings.TrimSpace("Current MEMORY.md:\n\n" + strings.TrimSpace(request.CurrentContent) + "\n\nNew memory to incorporate:\n\n- " + strings.TrimSpace(request.NewContent))
}
