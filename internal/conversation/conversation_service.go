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

func (conversationService *ConversationService) GenerateReply(prompt string, memoryFacts []memory.MemoryFact) string {
	replyContext := conversationService.replyContextBuilder.BuildReplyContext(prompt, memoryFacts)
	return conversationService.replyGenerator.GenerateReply(replyContext)
}
