package bluecollar

import (
	"context"
	"errors"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/model"
)

func (agentKernel *AgentKernel) GenerateReply(responseContext context.Context, prompt string) (string, error) {
	return agentKernel.GenerateReplyWithMemory(responseContext, prompt, nil)
}

func (agentKernel *AgentKernel) GenerateReplyWithMemory(responseContext context.Context, prompt string, memoryFacts []MemoryFact) (string, error) {
	return agentKernel.GenerateReplyWithContext(responseContext, prompt, VisibleContext{}, memoryFacts)
}

func (agentKernel *AgentKernel) GenerateReplyWithContext(responseContext context.Context, prompt string, visibleContext VisibleContext, memoryFacts []MemoryFact) (string, error) {
	if agentKernel.languageModel == nil {
		return "", errors.New("language model provider is not configured")
	}
	instructionBundle := agentKernel.currentInstructionBundle()
	messages := buildReplyMessagesWithInstructions(prompt, visibleContext, memoryFacts, instructionBundle.Prompt)
	chatCompleter, isAvailable := model.ResolveTextChatCompleter(agentKernel.languageModel)
	if !isAvailable {
		return "", errors.New("language model provider does not support chat completion")
	}
	return generateChatReply(responseContext, chatCompleter, messages)
}

func generateChatReply(responseContext context.Context, chatCompleter model.ChatCompleter, messages []model.Message) (string, error) {
	response, errorValue := chatCompleter.GenerateChatCompletion(responseContext, model.ChatCompletionRequest{
		SchemaName: "blueclaw_reply",
		Messages:   chatMessages(messages),
	})
	if errorValue != nil {
		return "", errorValue
	}

	reply := strings.TrimSpace(response.Message.Content)
	if reply == "" {
		return "", errors.New("language model reply is empty")
	}
	return reply, nil
}

func chatMessages(messages []model.Message) []model.ChatCompletionMessage {
	chatMessages := make([]model.ChatCompletionMessage, 0, len(messages))
	for _, message := range messages {
		chatMessages = append(chatMessages, model.ChatCompletionMessage{
			Role:    message.Role,
			Content: message.Content,
		})
	}
	return chatMessages
}

func (agentKernel *AgentKernel) buildReplyMessages(prompt string, visibleContext VisibleContext, memoryFacts []MemoryFact) []model.Message {
	return buildReplyMessagesWithInstructions(prompt, visibleContext, memoryFacts, agentKernel.currentInstructionBundle().Prompt)
}

func buildReplyMessages(prompt string, visibleContext VisibleContext, memoryFacts []MemoryFact) []model.Message {
	return buildReplyMessagesWithInstructions(prompt, visibleContext, memoryFacts, "")
}

func buildReplyMessagesWithInstructions(prompt string, visibleContext VisibleContext, memoryFacts []MemoryFact, instructionPrompt string) []model.Message {
	return (PromptAssembler{}).BuildReplyMessages(prompt, visibleContext, buildMemoryContext(memoryFacts), instructionPrompt)
}

func buildSenderAddressingDescription(request AgentTurnRequest) string {
	callingName := strings.TrimSpace(request.RequesterCallingName)
	fullName := strings.TrimSpace(request.RequesterName)
	handle := strings.TrimSpace(request.RequesterHandle)
	if callingName == "" {
		callingName = fullName
	}
	if callingName == "" && handle == "" {
		return ""
	}

	descriptionLines := []string{"You are speaking with the following user:"}
	if fullName != "" {
		descriptionLines = append(descriptionLines, "- Full name: "+fullName)
	}
	if callingName != "" {
		descriptionLines = append(descriptionLines, "- Calling name: "+callingName)
	}
	if handle != "" {
		descriptionLines = append(descriptionLines, "- Handle: @"+handle)
	}
	descriptionLines = append(descriptionLines,
		"When addressing them in Korean, call them \""+callingName+" 님\".",
		"When addressing them in English, call them \""+callingName+"\".",
		"If multiple participants in this conversation share the same calling name, append \"@handle\" when addressing them to disambiguate.",
	)
	return strings.Join(descriptionLines, "\n")
}
