package bluecollar

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/model"
)

func formatContextTimestamp(sentAt time.Time) string {
	if sentAt.IsZero() {
		return ""
	}
	return sentAt.In(defaultTurnLocation()).Format("01-02 15:04")
}

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

func buildVisibleContextDescription(visibleContext VisibleContext) string {
	contextLines := []string{}
	for _, message := range visibleContext.Messages {
		speaker := formatSpeakerLabel(message.SpeakerCallingName, message.SpeakerHandle, message.Speaker)
		prefix := "- "
		if stamp := formatContextTimestamp(message.SentAt); stamp != "" {
			prefix = "- [" + stamp + "] "
		}
		text := strings.TrimSpace(message.Text)
		if text != "" {
			contextLines = append(contextLines, prefix+speaker+": "+text)
		}
		for _, material := range message.Materials {
			if line := formatVisibleContextMaterial(material); line != "" {
				contextLines = append(contextLines, prefix+speaker+" attached "+line)
			}
		}
	}
	currentMaterialLines := []string{}
	for _, material := range visibleContext.CurrentMaterials {
		if line := formatVisibleContextMaterial(material); line != "" {
			currentMaterialLines = append(currentMaterialLines, "- "+line)
		}
	}
	materialLines := []string{}
	for _, material := range visibleContext.Materials {
		if line := formatVisibleContextMaterial(material); line != "" {
			materialLines = append(materialLines, "- "+line)
		}
	}

	if len(contextLines) == 0 && len(currentMaterialLines) == 0 && len(materialLines) == 0 && !visibleContext.HasMoreBefore {
		return ""
	}

	historyLine := "No earlier visible messages are available."
	if visibleContext.HasMoreBefore {
		historyLine = "There are earlier visible messages not included here. Ask for conversation_history if older context is needed."
	}

	if len(contextLines) == 0 && len(currentMaterialLines) == 0 && len(materialLines) == 0 {
		return "Recent visible conversation context:\n" + historyLine
	}

	sections := []string{}
	if len(currentMaterialLines) > 0 {
		sections = append(sections, "Current attachments:\nUse the listed fileHint exactly with file_preview, file_read, image_read, or file.materialize. fileHint is a deterministic locator, not a natural-language description.\n"+strings.Join(currentMaterialLines, "\n"))
	}
	if len(contextLines) > 0 {
		sections = append(sections, strings.Join(contextLines, "\n"))
	}
	if len(materialLines) > 0 {
		sections = append(sections, "Previous attachments:\nUse the listed fileHint exactly with file_preview, file_read, image_read, or file.materialize when older conversation context is relevant.\n"+strings.Join(materialLines, "\n"))
	}
	sections = append(sections, historyLine)
	return "Recent visible conversation context:\n" + strings.Join(sections, "\n")
}

func formatVisibleContextMaterial(material VisibleContextMaterial) string {
	filename := strings.TrimSpace(material.Filename)
	path := strings.TrimSpace(material.Path)
	fileHint := strings.TrimSpace(material.FileHint)
	materialID := strings.TrimSpace(material.MaterialID)
	if fileHint == "" && materialID == "" && filename == "" && path == "" {
		return ""
	}
	includeDiagnosticMetadata := path == "" || !material.IsAvailable
	values := []string{}
	if fileHint != "" {
		values = append(values, "fileHint="+fileHint)
	}
	if materialID != "" {
		values = append(values, "materialID="+materialID)
	}
	if path != "" {
		values = append(values, "path="+path)
	}
	if includeDiagnosticMetadata && filename != "" {
		values = append(values, "filename="+filename)
	}
	if shouldIncludeVisibleContextContentType(material, path) {
		values = append(values, "contentType="+material.ContentType)
	}
	if includeDiagnosticMetadata && material.SizeBytes > 0 {
		values = append(values, fmt.Sprintf("sizeBytes=%d", material.SizeBytes))
	}
	if material.MessageID != "" {
		values = append(values, "sourceMessageID="+material.MessageID)
	}
	if !material.IsAvailable {
		values = append(values, "available=false")
	}
	if material.ErrorCode != "" {
		values = append(values, "errorCode="+material.ErrorCode)
	}
	if material.Message != "" {
		values = append(values, "message="+material.Message)
	}
	if path != "" || materialID != "" {
		values = append(values, "availableTools="+strings.Join(visibleContextMaterialToolNames(material), ","))
	}
	return strings.Join(values, " ")
}

func shouldIncludeVisibleContextContentType(material VisibleContextMaterial, path string) bool {
	contentType := strings.TrimSpace(material.ContentType)
	if contentType == "" {
		return false
	}
	if strings.TrimSpace(path) == "" || !material.IsAvailable {
		return true
	}
	return !strings.Contains(strings.TrimSpace(path), ".")
}

func visibleContextMaterialToolNames(material VisibleContextMaterial) []string {
	if visibleContextMaterialLooksLikeImage(material) {
		return []string{"image_read"}
	}
	return []string{"file_preview", "file_read"}
}

func formatSpeakerLabel(callingName string, handle string, fullName string) string {
	primary := strings.TrimSpace(callingName)
	if primary == "" {
		primary = strings.TrimSpace(fullName)
	}
	if primary == "" {
		return "Someone"
	}
	trimmedHandle := strings.TrimSpace(handle)
	if trimmedHandle == "" {
		return primary
	}
	return primary + " (@" + trimmedHandle + ")"
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
