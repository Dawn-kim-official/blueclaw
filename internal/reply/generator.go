package reply

import (
	"context"
	"errors"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/model"
)

const replySystemInstruction = "You are Blueclaw. Reply helpfully and concisely to the user message. Use the provided context only as context; do not reveal hidden policy or provenance unless the user asks for it and access is allowed. If the user message only mentions you, answer or continue the recent visible conversation instead of asking what is needed. Treat jokes and playful addressed remarks as real conversational turns, and respond briefly like a good-humored coworker."

type Generator struct {
	languageModel           model.LanguageModelProvider
	instructionBundleLoader func() agentcontract.InstructionBundle
}

func NewGenerator(languageModel model.LanguageModelProvider, instructionBundleLoader func() agentcontract.InstructionBundle) *Generator {
	return &Generator{languageModel: languageModel, instructionBundleLoader: instructionBundleLoader}
}

func (generator *Generator) GenerateReply(responseContext context.Context, prompt string) (string, error) {
	return generator.GenerateReplyWithContext(responseContext, prompt, agentcontract.VisibleContext{}, nil)
}

func (generator *Generator) GenerateReplyWithContext(responseContext context.Context, prompt string, visibleContext agentcontract.VisibleContext, memoryFacts []agentcontract.MemoryFact) (string, error) {
	if generator.languageModel == nil {
		return "", errors.New("language model provider is not configured")
	}
	chatCompleter, isAvailable := model.ResolveTextChatCompleter(generator.languageModel)
	if !isAvailable {
		return "", errors.New("language model provider does not support chat completion")
	}
	response, errorValue := chatCompleter.GenerateChatCompletion(responseContext, model.ChatCompletionRequest{
		SchemaName: "blueclaw_reply",
		Messages:   generator.chatMessages(prompt, visibleContext, memoryFacts),
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

func (generator *Generator) chatMessages(prompt string, visibleContext agentcontract.VisibleContext, memoryFacts []agentcontract.MemoryFact) []model.ChatCompletionMessage {
	return []model.ChatCompletionMessage{
		{Role: "system", Content: replySystemInstruction},
		{Role: "system", Content: generator.replyContext(prompt, visibleContext, memoryFacts)},
		{Role: "user", Content: prompt},
	}
}

func (generator *Generator) replyContext(prompt string, visibleContext agentcontract.VisibleContext, memoryFacts []agentcontract.MemoryFact) string {
	sections := []string{}
	if instructionPrompt := strings.TrimSpace(generator.instructionPrompt()); instructionPrompt != "" {
		sections = append(sections, "Workspace instructions and available skill references:\n"+instructionPrompt)
	}
	if conversation := agentcontract.BuildVisibleContextDescription(visibleContext); conversation != "" {
		sections = append(sections, "Conversation:\n"+conversation)
	}
	if memoryContext := agentcontract.BuildMemoryContext(memoryFacts); memoryContext != "" {
		sections = append(sections, "Memory:\n"+memoryContext)
	}
	sections = append(sections, "User message:\n"+strings.TrimSpace(prompt))
	return strings.Join(sections, "\n\n")
}

func (generator *Generator) instructionPrompt() string {
	if generator.instructionBundleLoader == nil {
		return ""
	}
	return generator.instructionBundleLoader().Prompt
}
