package agentcontract

import (
	"fmt"
	"strings"
	"time"
)

func FormatContextTimestamp(sentAt time.Time) string {
	if sentAt.IsZero() {
		return ""
	}
	return sentAt.In(ContextRenderLocation()).Format("01-02 15:04")
}

func ContextRenderLocation() *time.Location {
	location, errorValue := time.LoadLocation("Asia/Seoul")
	if errorValue != nil {
		return time.Local
	}
	return location
}

func BuildVisibleContextDescription(visibleContext VisibleContext) string {
	contextLines := []string{}
	for _, message := range visibleContext.Messages {
		speaker := formatSpeakerLabel(message.SpeakerCallingName, message.SpeakerHandle, message.Speaker)
		prefix := "- "
		if stamp := FormatContextTimestamp(message.SentAt); stamp != "" {
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

func visibleContextMaterialLooksLikeImage(material VisibleContextMaterial) bool {
	contentType := strings.ToLower(strings.TrimSpace(material.ContentType))
	if strings.HasPrefix(contentType, "image/") {
		return true
	}
	filename := strings.ToLower(strings.TrimSpace(material.Filename))
	return strings.HasSuffix(filename, ".png") ||
		strings.HasSuffix(filename, ".jpg") ||
		strings.HasSuffix(filename, ".jpeg") ||
		strings.HasSuffix(filename, ".gif") ||
		strings.HasSuffix(filename, ".webp")
}
