package bluecollar

import (
	"encoding/base64"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
)

const (
	AgentPartTypeText  = "text"
	AgentPartTypeImage = "image"
	AgentPartTypeFile  = "file"
)

type AgentMessage struct {
	Role  string      `json:"role"`
	Parts []AgentPart `json:"parts,omitempty"`
}

type AgentPart struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	Image      *AgentImagePart `json:"image,omitempty"`
	File       *AgentFilePart  `json:"file,omitempty"`
	Source     AgentPartSource `json:"source,omitempty"`
	Visibility string          `json:"visibility,omitempty"`
}

type AgentImagePart struct {
	MimeType   string `json:"mimeType,omitempty"`
	DataBase64 string `json:"dataBase64,omitempty"`
	Path       string `json:"path,omitempty"`
	Filename   string `json:"filename,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

type AgentFilePart struct {
	Path              string `json:"path,omitempty"`
	Filename          string `json:"filename,omitempty"`
	ContentType       string `json:"contentType,omitempty"`
	SizeBytes         int64  `json:"sizeBytes,omitempty"`
	MarkdownPreview   string `json:"markdownPreview,omitempty"`
	ConversionStatus  string `json:"conversionStatus,omitempty"`
	ConversionMessage string `json:"conversionMessage,omitempty"`
}

type AgentPartSource struct {
	Platform      string `json:"platform,omitempty"`
	MessageID     string `json:"messageID,omitempty"`
	FileID        string `json:"fileID,omitempty"`
	ObservationID string `json:"observationID,omitempty"`
	ToolName      string `json:"toolName,omitempty"`
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

func AgentPartsToLLMParts(parts []AgentPart) []llm.MessagePart {
	result := []llm.MessagePart{}
	for _, part := range parts {
		switch strings.TrimSpace(part.Type) {
		case AgentPartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				result = append(result, llm.MessagePart{Type: "text", Text: strings.TrimSpace(part.Text)})
			}
		case AgentPartTypeImage:
			imagePart := agentImageToLLMPart(part)
			if strings.TrimSpace(imagePart.DataBase64) != "" && strings.TrimSpace(imagePart.MimeType) != "" {
				result = append(result, imagePart)
			}
			if fileText := agentFileContextText(part); fileText != "" {
				result = append(result, llm.MessagePart{Type: "text", Text: fileText})
			}
		case AgentPartTypeFile:
			if fileText := agentFileContextText(part); fileText != "" {
				result = append(result, llm.MessagePart{Type: "text", Text: fileText})
			}
		}
	}
	return result
}

func agentImageToLLMPart(part AgentPart) llm.MessagePart {
	if part.Image == nil {
		return llm.MessagePart{}
	}
	dataBase64 := strings.TrimSpace(part.Image.DataBase64)
	if dataBase64 == "" && strings.TrimSpace(part.Image.Path) != "" {
		return llm.MessagePart{}
	}
	if _, errorValue := base64.StdEncoding.DecodeString(dataBase64); errorValue != nil {
		return llm.MessagePart{}
	}
	return llm.MessagePart{
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
