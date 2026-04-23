package conversation

import "strings"

type ReplyGenerator struct{}

func (replyGenerator ReplyGenerator) GenerateReply(replyContext ReplyContext) string {
	if len(replyContext.MemoryDescriptions) == 0 {
		return replyContext.Prompt
	}

	return strings.TrimSpace(replyContext.Prompt + "\n\n" + strings.Join(replyContext.MemoryDescriptions, "\n"))
}
