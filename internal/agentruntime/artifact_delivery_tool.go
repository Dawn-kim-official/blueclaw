package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"blueclaw/internal/agent"
)

type artifactDeliverToolInput struct {
	Path                     string                     `json:"path"`
	Filename                 string                     `json:"filename"`
	ContentType              string                     `json:"contentType"`
	Title                    string                     `json:"title"`
	DestinationDirectoryPath string                     `json:"destinationDirectoryPath"`
	Overwrite                bool                       `json:"overwrite"`
	Files                    []artifactDeliverFileInput `json:"files"`
}

type artifactDeliverFileInput struct {
	Path                     string `json:"path"`
	Filename                 string `json:"filename"`
	ContentType              string `json:"contentType"`
	Title                    string `json:"title"`
	DestinationDirectoryPath string `json:"destinationDirectoryPath"`
	Overwrite                bool   `json:"overwrite"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerArtifactDeliveryTool(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[artifactDeliverToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        agent.ArtifactDeliverToolName,
			Description: "Deliver one or more completed workspace artifacts to the user. Optionally promote draft files into a durable artifact directory before attaching.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"filename":{"type":"string"},"contentType":{"type":"string"},"title":{"type":"string"},"destinationDirectoryPath":{"type":"string"},"overwrite":{"type":"boolean"},"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"filename":{"type":"string"},"contentType":{"type":"string"},"title":{"type":"string"},"destinationDirectoryPath":{"type":"string"},"overwrite":{"type":"boolean"}},"required":["path"]}}}}`),
		},
		Handler: func(toolContext context.Context, input artifactDeliverToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.deliverArtifactTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) deliverArtifactTool(toolContext context.Context, input artifactDeliverToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	files := normalizeArtifactDeliverFiles(input)
	if len(files) == 0 {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "artifact_deliver", "files must contain at least one path"), nil
	}
	attachmentFiles := []fileAttachFileInput{}
	deliveredFiles := []map[string]string{}
	for _, file := range files {
		attachmentPath, result, errorValue := toolCatalogBuilder.artifactDeliveryPath(toolContext, file, handlerContext)
		if result != nil || errorValue != nil {
			return firstToolFailureResult(result, errorValue, "artifact_deliver"), nil
		}
		attachmentFiles = append(attachmentFiles, fileAttachFileInput{
			Path:        attachmentPath,
			Filename:    file.Filename,
			ContentType: file.ContentType,
			Title:       file.Title,
		})
		deliveredFiles = append(deliveredFiles, map[string]string{
			"path":       attachmentPath,
			"sourcePath": strings.TrimSpace(file.Path),
		})
	}
	attachResult, errorValue := toolCatalogBuilder.attachFileTool(toolContext, fileAttachToolInput{Files: attachmentFiles}, handlerContext)
	if errorValue != nil || attachResult.Failure != nil {
		return attachResult, errorValue
	}
	content := marshalToolResult(map[string]any{
		"status":          "delivered",
		"files":           deliveredFiles,
		"attachmentCount": len(attachResult.Attachments),
	})
	return agent.ToolResult{
		Output:      agent.ToolOutput{Content: content, Data: json.RawMessage(content)},
		Attachments: attachResult.Attachments,
	}, nil
}

func normalizeArtifactDeliverFiles(input artifactDeliverToolInput) []artifactDeliverFileInput {
	if len(input.Files) > 0 {
		return input.Files
	}
	if strings.TrimSpace(input.Path) == "" {
		return nil
	}
	return []artifactDeliverFileInput{{
		Path:                     input.Path,
		Filename:                 input.Filename,
		ContentType:              input.ContentType,
		Title:                    input.Title,
		DestinationDirectoryPath: input.DestinationDirectoryPath,
		Overwrite:                input.Overwrite,
	}}
}

func (toolCatalogBuilder *ToolCatalogBuilder) artifactDeliveryPath(toolContext context.Context, input artifactDeliverFileInput, handlerContext toolHandlerContext) (string, *agent.ToolResult, error) {
	sourcePath := strings.TrimSpace(input.Path)
	if sourcePath == "" {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "artifact_deliver", "file path is required")
		return "", &result, nil
	}
	destinationDirectoryPath := strings.TrimSpace(input.DestinationDirectoryPath)
	if destinationDirectoryPath == "" {
		return sourcePath, nil, nil
	}
	result, errorValue := toolCatalogBuilder.promoteFileTool(toolContext, filePromoteToolInput{
		Path:                     sourcePath,
		DestinationDirectoryPath: destinationDirectoryPath,
		Overwrite:                input.Overwrite,
	}, handlerContext)
	if errorValue != nil || result.Failure != nil {
		return "", &result, errorValue
	}
	path, errorValue := promotedArtifactPath(result.Output.Content)
	if errorValue != nil {
		failure := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "artifact_deliver", errorValue.Error())
		return "", &failure, nil
	}
	return path, nil, nil
}

func promotedArtifactPath(content string) (string, error) {
	var document struct {
		Path string `json:"path"`
	}
	if errorValue := json.Unmarshal([]byte(content), &document); errorValue != nil {
		return "", errorValue
	}
	path := strings.TrimSpace(document.Path)
	if path == "" {
		return "", errors.New("file promotion did not return a path")
	}
	return path, nil
}
