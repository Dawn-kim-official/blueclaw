package bluecollar

import (
	"encoding/base64"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/model"
)

type AgentMessage struct {
	Role  string      `json:"role"`
	Parts []AgentPart `json:"parts,omitempty"`
}

func TextAgentPart(text string) AgentPart {
	return AgentPart{
		Type: AgentPartTypeText,
		Text: strings.TrimSpace(text),
	}
}

func FileAttachmentAgentPart(attachment toolcontract.FileAttachment, source AgentPartSource) AgentPart {
	file := &AgentFilePart{
		Path:        strings.TrimSpace(attachment.DevicePath),
		Filename:    strings.TrimSpace(attachment.Filename),
		ContentType: strings.TrimSpace(attachment.ContentType),
		SizeBytes:   attachment.SizeBytes,
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.ContentType)), "image/") {
		return AgentPart{
			Type: AgentPartTypeImage,
			Image: &AgentImagePart{
				MimeType:   strings.TrimSpace(attachment.ContentType),
				DataBase64: strings.TrimSpace(attachment.ContentBase64),
				Path:       strings.TrimSpace(attachment.DevicePath),
				Filename:   strings.TrimSpace(attachment.Filename),
			},
			File:       file,
			Source:     source,
			Visibility: "llm",
		}
	}
	return AgentPart{
		Type:       AgentPartTypeFile,
		File:       file,
		Source:     source,
		Visibility: "llm",
	}
}

func AgentPartsToLLMParts(parts []AgentPart) []model.MessagePart {
	result := []model.MessagePart{}
	for _, part := range parts {
		switch strings.TrimSpace(part.Type) {
		case AgentPartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				result = append(result, model.MessagePart{Type: "text", Text: strings.TrimSpace(part.Text)})
			}
		case AgentPartTypeImage:
			imagePart := agentImageToLLMPart(part)
			if strings.TrimSpace(imagePart.DataBase64) != "" && strings.TrimSpace(imagePart.MimeType) != "" {
				result = append(result, imagePart)
			}
			if fileText := agentFileContextText(part); fileText != "" {
				result = append(result, model.MessagePart{Type: "text", Text: fileText})
			}
		case AgentPartTypeFile:
			if fileText := agentFileContextText(part); fileText != "" {
				result = append(result, model.MessagePart{Type: "text", Text: fileText})
			}
		}
	}
	return result
}

func agentImageToLLMPart(part AgentPart) model.MessagePart {
	if part.Image == nil {
		return model.MessagePart{}
	}
	dataBase64 := strings.TrimSpace(part.Image.DataBase64)
	if dataBase64 == "" && strings.TrimSpace(part.Image.Path) != "" {
		return model.MessagePart{}
	}
	if _, errorValue := base64.StdEncoding.DecodeString(dataBase64); errorValue != nil {
		return model.MessagePart{}
	}
	return model.MessagePart{
		Type:       "image",
		MimeType:   strings.TrimSpace(part.Image.MimeType),
		DataBase64: dataBase64,
		Text:       strings.TrimSpace(part.Image.Filename),
	}
}

func agentFileContextText(part AgentPart) string {
	if part.File == nil {
		return ""
	}
	lines := []string{"Attached file:"}
	if part.File.Filename != "" {
		lines = append(lines, "- filename: "+strings.TrimSpace(part.File.Filename))
	}
	if part.File.ContentType != "" {
		lines = append(lines, "- contentType: "+strings.TrimSpace(part.File.ContentType))
	}
	if part.File.Path != "" {
		lines = append(lines, "- path: "+strings.TrimSpace(part.File.Path))
	}
	if part.File.ConversionStatus != "" {
		lines = append(lines, "- conversionStatus: "+strings.TrimSpace(part.File.ConversionStatus))
	}
	if part.File.ConversionMessage != "" {
		lines = append(lines, "- conversionMessage: "+strings.TrimSpace(part.File.ConversionMessage))
	}
	if strings.TrimSpace(part.File.MarkdownPreview) != "" {
		lines = append(lines, "Markdown preview:\n"+strings.TrimSpace(part.File.MarkdownPreview))
	}
	return strings.Join(lines, "\n")
}
