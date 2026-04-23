package conversation

import "blueclaw/internal/memory"

type ConversationService struct {
	replyContextBuilder ReplyContextBuilder
	replyGenerator      ReplyGenerator
}

func NewConversationService() *ConversationService {
	return &ConversationService{
		replyContextBuilder: ReplyContextBuilder{},
		replyGenerator:      ReplyGenerator{},
	}
}

func (conversationService *ConversationService) GenerateReply(prompt string, memoryRecords []memory.MemoryRecord) string {
	replyContext := conversationService.replyContextBuilder.BuildReplyContext(prompt, memoryRecords)
	return conversationService.replyGenerator.GenerateReply(replyContext)
}
