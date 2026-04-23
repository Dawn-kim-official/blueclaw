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

func (replyContextBuilder ReplyContextBuilder) BuildReplyContext(prompt string, memoryRecords []memory.MemoryRecord) ReplyContext {
	memoryDescriptions := []string{}
	for _, memoryRecord := range memoryRecords {
		memoryDescriptions = append(memoryDescriptions, string(memoryRecord.ContentCiphertext))
	}

	return ReplyContext{
		Prompt:             strings.TrimSpace(prompt),
		MemoryDescriptions: memoryDescriptions,
	}
}
