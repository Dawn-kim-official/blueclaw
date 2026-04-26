package conversation

import (
	"strings"

	"blueclaw/internal/memory"
)

type ReplyContext struct {
	Prompt             string   `json:"prompt"`
	MemoryDescriptions []string `json:"memoryDescriptions"`
}

type ReplyContextBuilder struct{}

func (replyContextBuilder ReplyContextBuilder) BuildReplyContext(prompt string, memoryFacts []memory.MemoryFact) ReplyContext {
	memoryDescriptions := []string{}
	for _, memoryFact := range memoryFacts {
		memoryDescriptions = append(memoryDescriptions, memoryFact.Content)
	}

	return ReplyContext{
		Prompt:             strings.TrimSpace(prompt),
		MemoryDescriptions: memoryDescriptions,
	}
}
