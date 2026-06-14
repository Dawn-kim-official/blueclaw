package agentruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"blueclaw/internal/access"
	"blueclaw/internal/agent"
	"blueclaw/internal/security"
	"blueclaw/internal/workspacepath"
)

const inlineAttachmentMaximumBytes = 25 * 1024 * 1024

const defaultFileReadMaximumBytes = 128 * 1024

const maximumFileReadBytes = 1024 * 1024

const maximumEditableTextFileBytes = 2 * 1024 * 1024

const maximumFilePreviewBytes = 200 * 1024

type fileReadToolInput struct {
	Path           string `json:"path"`
	MaterialID     string `json:"materialID"`
	MaxOutputBytes int    `json:"maxOutputBytes"`
	StartLine      int    `json:"startLine"`
	LineCount      int    `json:"lineCount"`
}

type filePreviewToolInput struct {
	Path       string `json:"path"`
	MaterialID string `json:"materialID"`
}

type fileWriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    uint32 `json:"mode"`
}

type fileEditToolInput struct {
	Path    string `json:"path"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type filePatchToolInput struct {
	Edits []filePatchEditInput `json:"edits"`
}

type filePatchEditInput struct {
	Path    string `json:"path"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type fileAttachToolInput struct {
	Path        string                `json:"path"`
	Filename    string                `json:"filename"`
	ContentType string                `json:"contentType"`
	Title       string                `json:"title"`
	Files       []fileAttachFileInput `json:"files"`
}

type fileAttachFileInput struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Title       string `json:"title"`
}

type filePromoteToolInput struct {
	Path                     string `json:"path"`
	DestinationDirectoryPath string `json:"destinationDirectoryPath"`
	Overwrite                bool   `json:"overwrite"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerFileTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[fileWriteToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.write",
			Description: "Overwrite one UTF-8 text file under the Blueclaw workspace. Treat content as the complete file body, like terminal redirection to a file: include the text exactly as it should appear in the file, with real line breaks for multiline source.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Overwrites one workspace text file with the exact content string.",
				Produces:   "A written source, document, script, or config file at the requested path.",
				SideEffect: "workspace_write",
				UseWhen:    "A source file, design document, script, or generated text artifact must be created or replaced.",
				AvoidWhen:  "You only need to inspect files, append shell output, or run commands; do not pass escaped newline sequences when writing multiline source.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace path to create or overwrite."},"content":{"type":"string","description":"Complete file body as plain UTF-8 text. Use real line breaks for multiline files; this is the text that will be written exactly."},"mode":{"type":"number","description":"Optional POSIX file mode."}},"required":["path","content"]}`),
		},
		Handler: func(toolContext context.Context, input fileWriteToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.writeFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[fileReadToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.read",
			Description: "Read exact UTF-8 workspace text or a real file line range with honest size and truncation metadata. Use file.preview first for attached HTML, PDF, DOCX, PPTX, XLSX, or other documents.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Reads a text file or requested line range from the actual workspace file; attachment materialID falls back to cached preview text.",
				Produces:   "Text content plus path, line range, original size, returned size, line count if known, and truncation metadata.",
				SideEffect: "read",
				UseWhen:    "You need current file content before file.edit, file.patch, or file.write.",
				AvoidWhen:  "The file is binary, an attached document needing conversion, or you already have the exact current text needed for an edit.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace text file path to read."},"materialID":{"type":"string","description":"Attachment materialID from Current attachments or Previous attachments. Use file.preview first; file.read returns cached preview text if no exact workspace file is available."},"startLine":{"type":"integer","description":"Optional 1-based first line to return."},"lineCount":{"type":"integer","description":"Optional number of lines to return from startLine."}}}`),
		},
		Handler: func(toolContext context.Context, input fileReadToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.readFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[filePreviewToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.preview",
			Description: "Preview an attached or workspace file path from the conversation attachment catalog using cached AgentPart markdownPreview when available, or the existing document.read MarkItDown provider for convertible documents.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Returns a document preview or file metadata without inventing content.",
				Produces:   "Path, filename, content type, size, markdown preview, conversion status, and conversion message.",
				SideEffect: "read",
				UseWhen:    "The attachment catalog lists a materialID or path for an HTML, PDF, DOCX, PPTX, XLSX, text, or data file and you need to understand it.",
				AvoidWhen:  "You need exact source lines for an edit; use file.read after previewing.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace file path to preview. Use this when the attachment catalog has a readable path."},"materialID":{"type":"string","description":"Attachment materialID from Current attachments or Previous attachments. Use this when the catalog lists a materialID, especially if no readable path is available."}}}`),
		},
		Handler: func(toolContext context.Context, input filePreviewToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.previewFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[fileEditToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.edit",
			Description: "Apply one exact text replacement in a UTF-8 workspace file. The oldText must appear exactly once.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Replaces one exact oldText occurrence with newText in one workspace text file.",
				Produces:   "A modified source, document, script, or config file with match metadata.",
				SideEffect: "workspace_write",
				UseWhen:    "A small targeted source or document change is needed and the current oldText is known.",
				AvoidWhen:  "The change creates a new file, rewrites most of a file, or oldText is missing or ambiguous; use file.read first.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace text file path to modify."},"oldText":{"type":"string","description":"Exact existing text to replace; must appear exactly once."},"newText":{"type":"string","description":"Replacement text to write in place of oldText."}},"required":["path","oldText","newText"]}`),
		},
		Handler: func(toolContext context.Context, input fileEditToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.editFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[filePatchToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.patch",
			Description: "Apply multiple exact text replacements as one all-or-nothing workspace patch. Each oldText must appear exactly once at the point it is applied.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Applies structured exact replacements across one or more workspace text files.",
				Produces:   "Modified source, document, script, or config files only after every edit validates.",
				SideEffect: "workspace_write",
				UseWhen:    "Several targeted edits should be applied together after reading current files.",
				AvoidWhen:  "You need unified diff syntax, broad file rewrites, or the current oldText snippets are not known.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"edits":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string","description":"Workspace text file path to modify."},"oldText":{"type":"string","description":"Exact existing text to replace; must appear exactly once when this edit is applied."},"newText":{"type":"string","description":"Replacement text."}},"required":["path","oldText","newText"]}}},"required":["edits"]}`),
		},
		Handler: func(toolContext context.Context, input filePatchToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.patchFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[fileAttachToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.attach",
			Description: "Attach one or more existing workspace files to the final reply evidence.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","description":"One or more finished workspace files to attach in this single call.","items":{"type":"object","properties":{"path":{"type":"string","description":"Workspace path to an existing file."},"filename":{"type":"string","description":"Optional display filename."},"contentType":{"type":"string","description":"Optional MIME type."},"title":{"type":"string","description":"Optional attachment title."}},"required":["path"]}}},"required":["files"]}`),
		},
		Handler: func(toolContext context.Context, input fileAttachToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.attachFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[filePromoteToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.promote",
			Description: "Copy one finished draft file from tmp/<slug>/build into artifacts/<slug>/ or an allowed durable circle/shared directory before attaching. Use once per output file; do not pass a directory path.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"destinationDirectoryPath":{"type":"string"},"overwrite":{"type":"boolean"}},"required":["path","destinationDirectoryPath"]}`),
		},
		Handler: func(toolContext context.Context, input filePromoteToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.promoteFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) writeFileTool(toolContext context.Context, input fileWriteToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_write", "path is required"), nil
	}
	if input.Content == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_write", "content is required"), nil
	}
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(path, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_write", errorValue.Error()), nil
	}
	if isManagedSitePackageManifestPath(resolvedPath.VirtualPath) {
		return managedSiteManifestProtectedFailure(resolvedPath.VirtualPath), nil
	}
	if isImmutableSkillPath(toolCatalogBuilder.workspaceRootPath, resolvedPath.ConcretePath) {
		return agent.ToolFailureResult(agent.FailurePolicyBlocked, agent.FailureCodes.PolicyBlocked, "file_write", "file.write cannot modify built-in skill files"), nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, resolvedPath.ConcretePath) {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_write", "current account cannot write this file"), nil
	}
	fileMode := workspaceFileCreateMode(resolvedPath)
	if input.Mode != 0 {
		fileMode = os.FileMode(input.Mode)
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	if errorValue := workspaceActor.MkdirAll(toolContext, resolvedPath.Parent(), workspaceDirectoryCreateMode(resolvedPath.Parent())); errorValue != nil {
		return actorToolFailure("mkdir_all", "file_write", resolvedPath.VirtualPath, errorValue), nil
	}
	if errorValue := workspaceActor.WriteFile(toolContext, resolvedPath, []byte(input.Content), fileMode); errorValue != nil {
		return actorToolFailure("write_file", "file_write", resolvedPath.VirtualPath, errorValue), nil
	}
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"path":      resolvedPath.VirtualPath,
		"sizeBytes": len(input.Content),
	})), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) readFileTool(toolContext context.Context, input fileReadToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	path := strings.TrimSpace(input.Path)
	materialID := strings.TrimSpace(input.MaterialID)
	if materialID != "" {
		if result, isCached := cachedFileReadResultByMaterialID(handlerContext.request.InputParts, materialID, input); isCached {
			return result, nil
		}
	}
	if path == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", "path or materialID is required"), nil
	}
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(path, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", errorValue.Error()), nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionRead, resolvedPath.ConcretePath) {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_read", "current account cannot read this file"), nil
	}
	maxOutputBytes := input.MaxOutputBytes
	if maxOutputBytes <= 0 || maxOutputBytes > maximumFileReadBytes {
		maxOutputBytes = defaultFileReadMaximumBytes
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	fileInformation, errorValue := workspaceActor.Stat(toolContext, resolvedPath)
	if errorValue != nil {
		if result, isCached := cachedFileReadResult(handlerContext.request.InputParts, path, input); isCached {
			return result, nil
		}
		if result, fallbackError, isFound := toolCatalogBuilder.fileReadFallbackFromAttachmentMaterial(toolContext, resolvedPath.VirtualPath, input, handlerContext); isFound {
			return result, fallbackError
		}
		return actorToolFailure("stat", "file_read", resolvedPath.VirtualPath, errorValue), nil
	}
	if !fileInformation.IsRegular {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", "path is not a regular file"), nil
	}
	readMaximumBytes := maximumFileReadBytes
	if maxOutputBytes > readMaximumBytes {
		readMaximumBytes = maxOutputBytes
	}
	content, errorValue := workspaceActor.ReadFile(toolContext, resolvedPath, int64(readMaximumBytes+1))
	if errorValue != nil {
		if fileInformation.SizeBytes > int64(readMaximumBytes) {
			return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", "file is too large for exact text read; use file.preview for document or attachment understanding"), nil
		}
		return actorToolFailure("read_file", "file_read", resolvedPath.VirtualPath, errorValue), nil
	}
	isFileTruncated := len(content) > readMaximumBytes
	if isFileTruncated {
		content = content[:readMaximumBytes]
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", "file.read supports UTF-8 text files; use file.preview or a specialized document tool for binary files"), nil
	}
	readResult := fileReadResult(string(content), input.StartLine, input.LineCount, maxOutputBytes)
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"path":              resolvedPath.VirtualPath,
		"content":           readResult.Content,
		"startLine":         readResult.StartLine,
		"endLine":           readResult.EndLine,
		"totalLines":        readResult.TotalLines,
		"totalLinesKnown":   !isFileTruncated,
		"originalSizeBytes": fileInformation.SizeBytes,
		"returnedBytes":     len([]byte(readResult.Content)),
		"sizeBytes":         fileInformation.SizeBytes,
		"isTruncated":       isFileTruncated || readResult.IsTruncated,
	})), nil
}

func cachedFileReadResultByMaterialID(parts []agent.AgentPart, materialID string, input fileReadToolInput) (agent.ToolResult, bool) {
	preview, isCached := cachedFilePreviewResultByMaterialID(parts, materialID)
	if !isCached {
		return agent.ToolResult{}, false
	}
	return cachedFileReadResultFromPreview(preview, input), true
}

func cachedFileReadResult(parts []agent.AgentPart, path string, input fileReadToolInput) (agent.ToolResult, bool) {
	preview, isCached := cachedFilePreviewResult(parts, path)
	if !isCached {
		return agent.ToolResult{}, false
	}
	return cachedFileReadResultFromPreview(preview, input), true
}

func cachedFileReadResultFromPreview(preview map[string]any, input fileReadToolInput) agent.ToolResult {
	content := stringMapValue(preview, "markdownPreview")
	readResult := fileReadResult(content, input.StartLine, input.LineCount, defaultFileReadMaximumBytes)
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"path":              stringMapValue(preview, "path"),
		"content":           readResult.Content,
		"startLine":         readResult.StartLine,
		"endLine":           readResult.EndLine,
		"totalLines":        readResult.TotalLines,
		"totalLinesKnown":   true,
		"originalSizeBytes": int64MapValue(preview, "sizeBytes"),
		"returnedBytes":     len([]byte(readResult.Content)),
		"sizeBytes":         int64MapValue(preview, "sizeBytes"),
		"isTruncated":       readResult.IsTruncated,
		"source":            "attachmentPreview",
		"isExactFileRead":   false,
	}))
}

func (toolCatalogBuilder *ToolCatalogBuilder) fileReadFallbackFromAttachmentMaterial(toolContext context.Context, path string, input fileReadToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error, bool) {
	material, isFound := visibleAttachmentMaterialForPath(handlerContext.request.VisibleContext, path)
	if !isFound {
		return agent.ToolResult{}, nil, false
	}
	materialID := strings.TrimSpace(material.MaterialID)
	if materialID == "" {
		return agent.ToolResult{}, nil, false
	}
	resolvedMaterial, errorValue := resolveReadableAttachmentMaterial(toolContext, handlerContext.request, materialID)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", errorValue.Error()), nil, true
	}
	if attachmentMaterialLooksLikeImage(resolvedMaterial) {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", "attachment material is an image; use image.read"), nil, true
	}
	if preview, hasPreview := filePreviewResultFromVisibleMaterial(resolvedMaterial); hasPreview {
		return cachedFileReadResultFromPreview(preview, input), nil, true
	}
	fallbackPath := strings.TrimSpace(resolvedMaterial.Path)
	if fallbackPath == "" || fallbackPath == strings.TrimSpace(path) {
		return agent.ToolResult{}, nil, false
	}
	fallbackInput := input
	fallbackInput.Path = fallbackPath
	fallbackInput.MaterialID = ""
	result, readError := toolCatalogBuilder.readFileTool(toolContext, fallbackInput, handlerContext)
	return result, readError, true
}

func stringMapValue(document map[string]any, key string) string {
	value, _ := document[key].(string)
	return strings.TrimSpace(value)
}

func int64MapValue(document map[string]any, key string) int64 {
	switch value := document[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

type fileReadOutput struct {
	Content     string
	StartLine   int
	EndLine     int
	TotalLines  int
	IsTruncated bool
}

func fileReadResult(content string, startLine int, lineCount int, maxOutputBytes int) fileReadOutput {
	lines := splitFileLines(content)
	totalLines := len(lines)
	if totalLines == 0 {
		return fileReadOutput{}
	}
	if startLine <= 0 {
		content, isTruncated := truncateTextByBytes(content, maxOutputBytes)
		return fileReadOutput{
			Content:     content,
			StartLine:   1,
			EndLine:     totalLines,
			TotalLines:  totalLines,
			IsTruncated: isTruncated,
		}
	}
	if startLine > totalLines {
		return fileReadOutput{
			StartLine:  startLine,
			EndLine:    startLine - 1,
			TotalLines: totalLines,
		}
	}
	if lineCount <= 0 {
		lineCount = 200
	}
	endLine := startLine + lineCount - 1
	if endLine > totalLines {
		endLine = totalLines
	}
	content, isTruncated := truncateTextByBytes(strings.Join(lines[startLine-1:endLine], "\n"), maxOutputBytes)
	return fileReadOutput{
		Content:     content,
		StartLine:   startLine,
		EndLine:     endLine,
		TotalLines:  totalLines,
		IsTruncated: isTruncated,
	}
}

func splitFileLines(content string) []string {
	if content == "" {
		return nil
	}
	normalizedContent := strings.TrimSuffix(content, "\n")
	if normalizedContent == "" {
		return []string{""}
	}
	return strings.Split(normalizedContent, "\n")
}

func (toolCatalogBuilder *ToolCatalogBuilder) previewFileTool(toolContext context.Context, input filePreviewToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	if cachedPreview, isCached := cachedFilePreviewResultForInput(handlerContext.request.InputParts, input); isCached {
		return agent.ToolSuccess(marshalToolResult(cachedPreview)), nil
	}
	if materialPreview, isResolved := toolCatalogBuilder.filePreviewResolvedMaterial(toolContext, input, handlerContext.request); isResolved {
		return materialPreview, nil
	}
	previewPath, materialFailure := toolCatalogBuilder.filePreviewPath(toolContext, input, handlerContext.request)
	if materialFailure != nil {
		return *materialFailure, nil
	}
	if cachedPreview, isCached := cachedFilePreviewResult(handlerContext.request.InputParts, previewPath); isCached {
		return agent.ToolSuccess(marshalToolResult(cachedPreview)), nil
	}
	resolvedPath, failureResult, errorValue := toolCatalogBuilder.resolveReadableWorkspacePath(previewPath, scope, handlerContext.request, "file_preview")
	if failureResult != nil || errorValue != nil {
		return firstToolFailureResult(failureResult, errorValue, "file_preview"), nil
	}
	if cachedPreview, isCached := cachedFilePreviewResult(handlerContext.request.InputParts, resolvedPath.VirtualPath); isCached {
		return agent.ToolSuccess(marshalToolResult(cachedPreview)), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	fileInformation, errorValue := workspaceActor.Stat(toolContext, resolvedPath)
	if errorValue != nil {
		if fallbackPath, fallbackFailure, isFound := toolCatalogBuilder.filePreviewFallbackPath(toolContext, resolvedPath.VirtualPath, handlerContext.request); isFound {
			if fallbackFailure != nil {
				return *fallbackFailure, nil
			}
			if strings.TrimSpace(fallbackPath) != "" && strings.TrimSpace(fallbackPath) != strings.TrimSpace(resolvedPath.VirtualPath) {
				return toolCatalogBuilder.previewFileTool(toolContext, filePreviewToolInput{Path: fallbackPath}, handlerContext)
			}
		}
		return actorToolFailure("stat", "file_preview", resolvedPath.VirtualPath, errorValue), nil
	}
	contentType := previewContentType(resolvedPath.VirtualPath)
	if strings.HasPrefix(contentType, "image/") {
		return agent.ToolSuccess(marshalToolResult(filePreviewResult(resolvedPath.VirtualPath, contentType, fileInformation.SizeBytes, "", "image", "use the image input part or image.read for visual inspection"))), nil
	}
	if toolCatalogBuilder.capabilityClient.HTTPClient != nil {
		if result, isConverted := toolCatalogBuilder.convertFilePreviewWithCapability(toolContext, handlerContext.request, resolvedPath.VirtualPath, contentType, fileInformation.SizeBytes); isConverted {
			return result, nil
		}
	}
	return toolCatalogBuilder.previewTextFile(toolContext, workspaceActor, resolvedPath, contentType, fileInformation.SizeBytes), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) filePreviewResolvedMaterial(toolContext context.Context, input filePreviewToolInput, request ToolCatalogRequest) (agent.ToolResult, bool) {
	if strings.TrimSpace(input.Path) != "" || strings.TrimSpace(input.MaterialID) == "" {
		return agent.ToolResult{}, false
	}
	material, errorValue := resolveReadableAttachmentMaterial(toolContext, request, input.MaterialID)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", errorValue.Error()), true
	}
	if attachmentMaterialLooksLikeImage(material) {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", "attachment material is an image; use image.read"), true
	}
	if result, hasPreview := filePreviewResultFromVisibleMaterial(material); hasPreview {
		return agent.ToolSuccess(marshalToolResult(result)), true
	}
	return agent.ToolResult{}, false
}

func filePreviewResultFromVisibleMaterial(material agent.VisibleContextMaterial) (map[string]any, bool) {
	preview := strings.TrimSpace(material.MarkdownPreview)
	status := strings.TrimSpace(material.ConversionStatus)
	message := strings.TrimSpace(material.ConversionMessage)
	if preview == "" && status == "" && message == "" {
		return nil, false
	}
	contentType := firstNonEmptyString(strings.TrimSpace(material.ContentType), previewContentType(material.Path))
	return filePreviewResult(material.Path, contentType, material.SizeBytes, preview, status, message), true
}

func (toolCatalogBuilder *ToolCatalogBuilder) filePreviewFallbackPath(toolContext context.Context, path string, request ToolCatalogRequest) (string, *agent.ToolResult, bool) {
	material, isFound := visibleAttachmentMaterialForPath(request.VisibleContext, path)
	if !isFound {
		return "", nil, false
	}
	materialID := strings.TrimSpace(material.MaterialID)
	if materialID == "" {
		return "", nil, false
	}
	resolvedMaterial, errorValue := resolveReadableAttachmentMaterial(toolContext, request, materialID)
	if errorValue != nil {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", errorValue.Error())
		return "", &result, true
	}
	if attachmentMaterialLooksLikeImage(resolvedMaterial) {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", "attachment material is an image; use image.read")
		return "", &result, true
	}
	return resolvedMaterial.Path, nil, true
}

func visibleAttachmentMaterialForPath(visibleContext agent.VisibleContext, path string) (agent.VisibleContextMaterial, bool) {
	candidates := visibleAttachmentMaterials(visibleContext)
	if material, isFound := visibleAttachmentMaterialWithExactPath(candidates, path); isFound {
		return material, true
	}
	return visibleAttachmentMaterialWithFilename(candidates, filepath.Base(strings.TrimSpace(path)))
}

func visibleAttachmentMaterials(visibleContext agent.VisibleContext) []agent.VisibleContextMaterial {
	materials := append([]agent.VisibleContextMaterial{}, visibleContext.CurrentMaterials...)
	materials = append(materials, visibleContext.Materials...)
	for _, message := range visibleContext.Messages {
		materials = append(materials, message.Materials...)
	}
	return uniqueVisibleAttachmentMaterials(materials)
}

func uniqueVisibleAttachmentMaterials(materials []agent.VisibleContextMaterial) []agent.VisibleContextMaterial {
	seen := map[string]bool{}
	result := make([]agent.VisibleContextMaterial, 0, len(materials))
	for _, material := range materials {
		key := visibleAttachmentMaterialKey(material)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, material)
	}
	return result
}

func visibleAttachmentMaterialKey(material agent.VisibleContextMaterial) string {
	if materialID := strings.TrimSpace(material.MaterialID); materialID != "" {
		return "material:" + materialID
	}
	if path := strings.TrimSpace(material.Path); path != "" {
		return "path:" + path
	}
	if filename := strings.TrimSpace(material.Filename); filename != "" {
		return "filename:" + filename
	}
	return ""
}

func visibleAttachmentMaterialWithExactPath(materials []agent.VisibleContextMaterial, path string) (agent.VisibleContextMaterial, bool) {
	trimmedPath := strings.TrimSpace(path)
	for _, material := range materials {
		if strings.TrimSpace(material.Path) == trimmedPath {
			return material, true
		}
	}
	return agent.VisibleContextMaterial{}, false
}

func visibleAttachmentMaterialWithFilename(materials []agent.VisibleContextMaterial, filename string) (agent.VisibleContextMaterial, bool) {
	trimmedFilename := strings.TrimSpace(filename)
	if trimmedFilename == "" || trimmedFilename == "." {
		return agent.VisibleContextMaterial{}, false
	}
	matches := []agent.VisibleContextMaterial{}
	for _, material := range materials {
		if strings.TrimSpace(material.Filename) == trimmedFilename || filepath.Base(strings.TrimSpace(material.Path)) == trimmedFilename {
			matches = append(matches, material)
		}
	}
	if len(matches) != 1 {
		return agent.VisibleContextMaterial{}, false
	}
	return matches[0], true
}

func (toolCatalogBuilder *ToolCatalogBuilder) filePreviewPath(toolContext context.Context, input filePreviewToolInput, request ToolCatalogRequest) (string, *agent.ToolResult) {
	path := strings.TrimSpace(input.Path)
	materialID := strings.TrimSpace(input.MaterialID)
	if path != "" {
		return path, nil
	}
	if materialID == "" {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", "path or materialID is required")
		return "", &result
	}
	material, errorValue := resolveReadableAttachmentMaterial(toolContext, request, materialID)
	if errorValue != nil {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", errorValue.Error())
		return "", &result
	}
	if attachmentMaterialLooksLikeImage(material) {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", "attachment material is an image; use image.read")
		return "", &result
	}
	return material.Path, nil
}

func attachmentMaterialLooksLikeImage(material agent.VisibleContextMaterial) bool {
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

func (toolCatalogBuilder *ToolCatalogBuilder) resolveReadableWorkspacePath(path string, scope WorkspaceScope, request ToolCatalogRequest, stage string) (workspacepath.Path, *agent.ToolResult, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, stage, "path is required")
		return workspacepath.Path{}, &result, nil
	}
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(trimmedPath, scope)
	if errorValue != nil {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, stage, errorValue.Error())
		return workspacepath.Path{}, &result, nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionRead, resolvedPath.ConcretePath) {
		result := agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, stage, "current account cannot read this file")
		return workspacepath.Path{}, &result, nil
	}
	return resolvedPath, nil, nil
}

func firstToolFailureResult(failureResult *agent.ToolResult, errorValue error, stage string) agent.ToolResult {
	if failureResult != nil {
		return *failureResult
	}
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, stage, errorValue.Error())
	}
	return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, stage, "invalid file preview request")
}

func cachedFilePreviewResultForInput(parts []agent.AgentPart, input filePreviewToolInput) (map[string]any, bool) {
	if materialID := strings.TrimSpace(input.MaterialID); materialID != "" {
		return cachedFilePreviewResultByMaterialID(parts, materialID)
	}
	return cachedFilePreviewResult(parts, strings.TrimSpace(input.Path))
}

func cachedFilePreviewResultByMaterialID(parts []agent.AgentPart, materialID string) (map[string]any, bool) {
	trimmedMaterialID := strings.TrimSpace(materialID)
	for _, part := range parts {
		if agentPartMaterialID(part) != trimmedMaterialID {
			continue
		}
		return cachedFilePreviewResultFromPart(part)
	}
	return nil, false
}

func cachedFilePreviewResult(parts []agent.AgentPart, path string) (map[string]any, bool) {
	for _, part := range parts {
		if part.File == nil || strings.TrimSpace(part.File.Path) != strings.TrimSpace(path) {
			continue
		}
		return cachedFilePreviewResultFromPart(part)
	}
	return nil, false
}

func cachedFilePreviewResultFromPart(part agent.AgentPart) (map[string]any, bool) {
	if part.File == nil {
		return nil, false
	}
	if strings.TrimSpace(part.File.MarkdownPreview) == "" && strings.TrimSpace(part.File.ConversionStatus) == "" {
		return nil, false
	}
	return filePreviewResult(
		part.File.Path,
		part.File.ContentType,
		part.File.SizeBytes,
		part.File.MarkdownPreview,
		firstNonEmptyString(part.File.ConversionStatus, "cached"),
		part.File.ConversionMessage,
	), true
}

func agentPartMaterialID(part agent.AgentPart) string {
	fileID := strings.TrimSpace(part.Source.FileID)
	if fileID == "" {
		return ""
	}
	return firstNonEmptyString(strings.TrimSpace(part.Source.Platform), "attachment") + ":" + fileID
}

func (toolCatalogBuilder *ToolCatalogBuilder) convertFilePreviewWithCapability(toolContext context.Context, request ToolCatalogRequest, path string, contentType string, sizeBytes int64) (agent.ToolResult, bool) {
	var response struct {
		Content      string          `json:"content"`
		IsError      bool            `json:"isError"`
		Status       string          `json:"status"`
		Message      string          `json:"message"`
		ErrorCode    string          `json:"errorCode"`
		FailureStage string          `json:"failureStage"`
		Result       json.RawMessage `json:"result"`
	}
	input := agent.MarshalToolInput(map[string]any{"path": path, "maxOutputBytes": maximumFilePreviewBytes})
	errorValue := toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/document.read/invoke", capabilityToolRequest(toolContext, "document.read", request, input), &response)
	if errorValue != nil || response.IsError || response.Status == "error" || response.Status == "denied" {
		return agent.ToolResult{}, false
	}
	var document struct {
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
		Format    string `json:"format"`
	}
	if json.Unmarshal(response.Result, &document) != nil {
		document.Content = response.Content
	}
	conversionStatus := "converted"
	if document.Truncated {
		conversionStatus = "truncated"
	}
	result := filePreviewResult(path, contentType, sizeBytes, document.Content, conversionStatus, "")
	if strings.TrimSpace(document.Format) != "" {
		result["previewFormat"] = strings.TrimSpace(document.Format)
	}
	return agent.ToolSuccess(marshalToolResult(result)), true
}

func (toolCatalogBuilder *ToolCatalogBuilder) previewTextFile(toolContext context.Context, workspaceActor security.WorkspaceActor, path workspacepath.Path, contentType string, sizeBytes int64) agent.ToolResult {
	document, errorValue := workspaceActor.ReadFile(toolContext, path, maximumFilePreviewBytes+1)
	if errorValue != nil {
		if sizeBytes > maximumFilePreviewBytes {
			return agent.ToolSuccess(marshalToolResult(filePreviewResult(path.VirtualPath, contentType, sizeBytes, "", "unsupported", "file is too large for local text preview; use document.read/MarkItDown provider when available")))
		}
		return actorToolFailure("read_file", "file_preview", path.VirtualPath, errorValue)
	}
	isTruncated := len(document) > maximumFilePreviewBytes
	if isTruncated {
		document = document[:maximumFilePreviewBytes]
	}
	if !utf8.Valid(document) || bytes.IndexByte(document, 0) >= 0 {
		return agent.ToolSuccess(marshalToolResult(filePreviewResult(path.VirtualPath, contentType, sizeBytes, "", "unsupported", "file is not UTF-8 text and no MarkItDown preview is available")))
	}
	content, isContentTruncated := truncateTextByBytes(string(document), maximumFilePreviewBytes)
	conversionStatus := "converted"
	if isTruncated || isContentTruncated {
		conversionStatus = "truncated"
	}
	return agent.ToolSuccess(marshalToolResult(filePreviewResult(path.VirtualPath, contentType, sizeBytes, content, conversionStatus, "")))
}

func filePreviewResult(path string, contentType string, sizeBytes int64, markdownPreview string, conversionStatus string, conversionMessage string) map[string]any {
	return map[string]any{
		"path":              strings.TrimSpace(path),
		"filename":          filepath.Base(strings.TrimSpace(path)),
		"contentType":       strings.TrimSpace(contentType),
		"sizeBytes":         sizeBytes,
		"previewFormat":     "markdown",
		"markdownPreview":   strings.TrimSpace(markdownPreview),
		"conversionStatus":  strings.TrimSpace(conversionStatus),
		"conversionMessage": strings.TrimSpace(conversionMessage),
	}
}

func previewContentType(path string) string {
	if contentType := mime.TypeByExtension(filepath.Ext(strings.TrimSpace(path))); strings.TrimSpace(contentType) != "" {
		return strings.TrimSpace(contentType)
	}
	return "application/octet-stream"
}

func truncateTextByBytes(content string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len([]byte(content)) <= maxBytes {
		return content, false
	}
	document := []byte(content)
	if maxBytes > len(document) {
		maxBytes = len(document)
	}
	truncatedDocument := document[:maxBytes]
	for len(truncatedDocument) > 0 && !utf8.Valid(truncatedDocument) {
		truncatedDocument = truncatedDocument[:len(truncatedDocument)-1]
	}
	return string(truncatedDocument), true
}

func (toolCatalogBuilder *ToolCatalogBuilder) editFileTool(toolContext context.Context, input fileEditToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	edit := filePatchEditInput{Path: input.Path, OldText: input.OldText, NewText: input.NewText}
	patchInput := filePatchToolInput{Edits: []filePatchEditInput{edit}}
	result, errorValue := toolCatalogBuilder.patchFileTool(toolContext, patchInput, handlerContext)
	if result.Failed() && result.Failure.Stage == "file_patch" {
		result.Failure.Stage = "file_edit"
	}
	return result, errorValue
}

func (toolCatalogBuilder *ToolCatalogBuilder) patchFileTool(toolContext context.Context, input filePatchToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if len(input.Edits) == 0 {
		return fileExactEditFailure("file_patch", "", -1, 0, "edits is required"), nil
	}
	if len(input.Edits) > 100 {
		return fileExactEditFailure("file_patch", "", -1, len(input.Edits), "too many edits; split the patch into smaller groups"), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	patchState := newFilePatchState()
	for editIndex, edit := range input.Edits {
		if result := toolCatalogBuilder.validatePatchEdit(toolContext, handlerContext, workspaceActor, patchState, edit, editIndex); result != nil {
			return *result, nil
		}
	}
	if result := writePatchState(toolContext, workspaceActor, patchState); result != nil {
		return *result, nil
	}
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"editedFiles": patchState.virtualPaths(),
		"editCount":   len(input.Edits),
	})), nil
}

type filePatchState struct {
	originalContents map[string]string
	currentContents  map[string]string
	resolvedPaths    map[string]ResolvedWorkspacePath
	pathOrder        []string
}

func newFilePatchState() *filePatchState {
	return &filePatchState{
		originalContents: map[string]string{},
		currentContents:  map[string]string{},
		resolvedPaths:    map[string]ResolvedWorkspacePath{},
	}
}

func (patchState *filePatchState) virtualPaths() []string {
	paths := []string{}
	for _, key := range patchState.pathOrder {
		paths = append(paths, patchState.resolvedPaths[key].VirtualPath)
	}
	return paths
}

func (toolCatalogBuilder *ToolCatalogBuilder) validatePatchEdit(toolContext context.Context, handlerContext toolHandlerContext, workspaceActor security.WorkspaceActor, patchState *filePatchState, edit filePatchEditInput, editIndex int) *agent.ToolResult {
	if strings.TrimSpace(edit.Path) == "" {
		result := fileExactEditFailure("file_patch", "", editIndex, 0, "path is required")
		return &result
	}
	if edit.OldText == "" {
		result := fileExactEditFailure("file_patch", strings.TrimSpace(edit.Path), editIndex, 0, "oldText is required")
		return &result
	}
	resolvedPath, result := toolCatalogBuilder.resolveEditableFilePath(toolContext, handlerContext, workspaceActor, strings.TrimSpace(edit.Path), patchState)
	if result != nil {
		return result
	}
	key := resolvedPath.ConcretePath
	currentContent := patchState.currentContents[key]
	matchCount := strings.Count(currentContent, edit.OldText)
	if matchCount != 1 {
		result := fileExactEditFailure("file_patch", resolvedPath.VirtualPath, editIndex, matchCount, "oldText must match exactly once; read the file and retry with a more specific snippet")
		return &result
	}
	patchState.currentContents[key] = strings.Replace(currentContent, edit.OldText, edit.NewText, 1)
	return nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveEditableFilePath(toolContext context.Context, handlerContext toolHandlerContext, workspaceActor security.WorkspaceActor, path string, patchState *filePatchState) (ResolvedWorkspacePath, *agent.ToolResult) {
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(path, scope)
	if errorValue != nil {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_patch", errorValue.Error())
		return ResolvedWorkspacePath{}, &result
	}
	if isManagedSitePackageManifestPath(resolvedPath.VirtualPath) {
		result := agent.ToolFailureResult(agent.FailurePolicyBlocked, agent.FailureCodes.PolicyBlocked, "file_patch", "site.app.create manages this build manifest; edit DESIGN.md and app source files instead of app/package.json")
		return ResolvedWorkspacePath{}, &result
	}
	if isImmutableSkillPath(toolCatalogBuilder.workspaceRootPath, resolvedPath.ConcretePath) {
		result := agent.ToolFailureResult(agent.FailurePolicyBlocked, agent.FailureCodes.PolicyBlocked, "file_patch", "file.patch cannot modify built-in skill files")
		return ResolvedWorkspacePath{}, &result
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionRead, resolvedPath.ConcretePath) || !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, resolvedPath.ConcretePath) {
		result := agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_patch", "current account cannot edit this file")
		return ResolvedWorkspacePath{}, &result
	}
	if _, isLoaded := patchState.currentContents[resolvedPath.ConcretePath]; isLoaded {
		return resolvedPath, nil
	}
	content, errorValue := workspaceActor.ReadFile(toolContext, resolvedPath, maximumEditableTextFileBytes+1)
	if errorValue != nil {
		result := actorToolFailure("read_file", "file_patch", resolvedPath.VirtualPath, errorValue)
		return ResolvedWorkspacePath{}, &result
	}
	if len(content) > maximumEditableTextFileBytes {
		result := fileExactEditFailure("file_patch", resolvedPath.VirtualPath, -1, 0, "file is too large for exact edit; rewrite a smaller generated file or use a more specific workflow")
		return ResolvedWorkspacePath{}, &result
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_patch", "file.patch supports UTF-8 text files; use a specialized document or artifact tool for binary files")
		return ResolvedWorkspacePath{}, &result
	}
	patchState.originalContents[resolvedPath.ConcretePath] = string(content)
	patchState.currentContents[resolvedPath.ConcretePath] = string(content)
	patchState.resolvedPaths[resolvedPath.ConcretePath] = resolvedPath
	patchState.pathOrder = append(patchState.pathOrder, resolvedPath.ConcretePath)
	return resolvedPath, nil
}

func writePatchState(toolContext context.Context, workspaceActor security.WorkspaceActor, patchState *filePatchState) *agent.ToolResult {
	writtenKeys := []string{}
	for _, key := range patchState.pathOrder {
		resolvedPath := patchState.resolvedPaths[key]
		if errorValue := workspaceActor.WriteFile(toolContext, resolvedPath, []byte(patchState.currentContents[key]), workspaceFileCreateMode(resolvedPath)); errorValue != nil {
			rollbackPatchWrites(toolContext, workspaceActor, patchState, writtenKeys)
			result := actorToolFailure("write_file", "file_patch", resolvedPath.VirtualPath, errorValue)
			return &result
		}
		writtenKeys = append(writtenKeys, key)
	}
	return nil
}

func rollbackPatchWrites(toolContext context.Context, workspaceActor security.WorkspaceActor, patchState *filePatchState, writtenKeys []string) {
	for _, key := range writtenKeys {
		resolvedPath := patchState.resolvedPaths[key]
		_ = workspaceActor.WriteFile(toolContext, resolvedPath, []byte(patchState.originalContents[key]), workspaceFileCreateMode(resolvedPath))
	}
}

func fileExactEditFailure(stage string, path string, editIndex int, matchCount int, guidance string) agent.ToolResult {
	content := marshalToolResult(map[string]any{
		"path":       strings.TrimSpace(path),
		"editIndex":  editIndex,
		"matchCount": matchCount,
		"guidance":   strings.TrimSpace(guidance),
	})
	result := agent.ToolFailureWithOutput(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, stage, content, json.RawMessage(content))
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	result.Failure.RetryPolicy = "different_input"
	result.Failure.RecoveryHints = []agent.RecoveryHint{{
		Action:    "inspect_or_edit_text",
		ToolNames: []string{"file.read", "file.edit", "file.patch", "file.write"},
		Reason:    "Read the current file content, then retry with an exact oldText snippet or rewrite the full file.",
	}}
	return result
}

func isManagedSitePackageManifestPath(virtualPath string) bool {
	cleanPath := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(strings.TrimSpace(virtualPath), "/workspace/")))
	parts := strings.Split(cleanPath, "/")
	if len(parts) == 5 &&
		parts[0] == "home" &&
		parts[1] == "sites" &&
		parts[3] == "app" &&
		parts[4] == "package.json" {
		return true
	}
	return len(parts) == 6 &&
		parts[0] == "home" &&
		parts[1] == "sites" &&
		parts[3] == "draft" &&
		parts[4] == "app" &&
		parts[5] == "package.json"
}

func managedSiteManifestProtectedFailure(path string) agent.ToolResult {
	content := marshalToolResult(map[string]string{
		"code":   "managed_manifest_protected",
		"path":   strings.TrimSpace(path),
		"detail": "site.app.create manages this build manifest; edit DESIGN.md and app source files instead of overwriting app/package.json",
	})
	return agent.ToolResult{
		Output: agent.ToolOutput{Content: content, Data: json.RawMessage(content)},
		Failure: &agent.ToolFailure{
			Kind:            agent.FailurePolicyBlocked,
			Code:            "managed_manifest_protected",
			Stage:           "file_write",
			UserSafeSummary: content,
			Retryable:       true,
			SafeRetry:       true,
		},
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) attachFileTool(toolContext context.Context, input fileAttachToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	attachmentInputs := normalizeFileAttachInputs(input)
	if len(attachmentInputs) == 0 {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_attach", "files must contain at least one path"), nil
	}
	attachments := []agent.FileAttachment{}
	for _, attachmentInput := range attachmentInputs {
		attachment, failureResult, errorValue := toolCatalogBuilder.fileAttachment(toolContext, attachmentInput, handlerContext, scope)
		if failureResult != nil {
			return *failureResult, nil
		}
		if errorValue != nil {
			return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "file_attach", errorValue.Error()), nil
		}
		attachments = append(attachments, attachment)
	}
	_ = toolContext
	return agent.ToolResult{
		Output:      agent.ToolOutput{Content: "files attached"},
		Attachments: attachments,
	}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) promoteFileTool(toolContext context.Context, input filePromoteToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	sourcePath := strings.TrimSpace(input.Path)
	if sourcePath == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", "path is required"), nil
	}
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	resolver := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath)
	destinationDirectory, errorValue := resolver.ResolveDirectory(input.DestinationDirectoryPath, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", errorValue.Error()), nil
	}
	if !destinationDirectory.IsDurableArtifact {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", "destinationDirectoryPath must be artifacts/<slug>, /workspace/circles/<circleID>/..., or /workspace/shared/public/..."), nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, destinationDirectory.ConcretePath) {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_promote", "current account cannot write the promotion destination"), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	if errorValue := workspaceActor.MkdirAll(toolContext, workspacepath.Directory(destinationDirectory), workspaceDirectoryCreateMode(workspacepath.Directory(destinationDirectory))); errorValue != nil {
		return actorToolFailure("mkdir_all", "file_promote", destinationDirectory.VirtualPath, errorValue), nil
	}
	source, errorValue := resolver.Resolve(sourcePath, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", errorValue.Error()), nil
	}
	if source.Kind != workspacePathKindDraft {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", "source path must come from tmp/<slug> draft work"), nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionRead, source.ConcretePath) {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_promote", "current account cannot read the promotion source"), nil
	}
	sourceInformation, errorValue := workspaceActor.Stat(toolContext, source)
	if errorValue != nil {
		return actorToolFailure("stat", "file_promote", source.VirtualPath, errorValue), nil
	}
	if !sourceInformation.IsRegular {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", "source path is a directory or non-file; promote each output file separately, for example tmp/<slug>/build/deck.html and tmp/<slug>/build/deck.pptx"), nil
	}
	destination := workspacepath.Directory(destinationDirectory).JoinVirtualFile(source.BaseName())
	if !input.Overwrite {
		if _, errorValue := workspaceActor.Stat(toolContext, destination); errorValue == nil {
			return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", "destination already exists; set overwrite=true to replace it"), nil
		} else if !security.IsActorNotFoundError(errorValue) {
			return actorToolFailure("stat", "file_promote", destination.VirtualPath, errorValue), nil
		}
	}
	if errorValue := workspaceActor.CopyFile(toolContext, source, destination, workspaceFileCreateMode(destination), input.Overwrite); errorValue != nil {
		return actorToolFailure("copy_file", "file_promote", destination.VirtualPath, errorValue), nil
	}
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"path": destination.VirtualPath,
	})), nil
}

func normalizeFileAttachInputs(input fileAttachToolInput) []fileAttachFileInput {
	if len(input.Files) > 0 {
		return input.Files
	}
	if strings.TrimSpace(input.Path) == "" {
		return nil
	}
	return []fileAttachFileInput{{
		Path:        input.Path,
		Filename:    input.Filename,
		ContentType: input.ContentType,
		Title:       input.Title,
	}}
}

func (toolCatalogBuilder *ToolCatalogBuilder) fileAttachment(toolContext context.Context, input fileAttachFileInput, handlerContext toolHandlerContext, scope WorkspaceScope) (agent.FileAttachment, *agent.ToolResult, error) {
	path := strings.TrimSpace(input.Path)
	if path == "" {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_attach", "attachment path is required")
		return agent.FileAttachment{}, &result, nil
	}
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(path, scope)
	if errorValue != nil {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_attach", errorValue.Error())
		return agent.FileAttachment{}, &result, nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionRead, resolvedPath.ConcretePath) {
		result := agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_attach", "current account cannot read this file")
		return agent.FileAttachment{}, &result, nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return agent.FileAttachment{}, actorFailure, nil
	}
	fileInformation, errorValue := workspaceActor.Stat(toolContext, resolvedPath)
	if errorValue != nil {
		result := actorToolFailure("stat", "file_attach", resolvedPath.VirtualPath, errorValue)
		return agent.FileAttachment{}, &result, nil
	}
	if !fileInformation.IsRegular {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_attach", "attachment path is not a regular file")
		return agent.FileAttachment{}, &result, nil
	}
	document, errorValue := workspaceActor.ReadFile(toolContext, resolvedPath, inlineAttachmentMaximumBytes)
	if errorValue != nil {
		result := actorToolFailure("read_file", "file_attach", resolvedPath.VirtualPath, errorValue)
		return agent.FileAttachment{}, &result, nil
	}
	filename := attachmentFilename(input, resolvedPath.ConcretePath)
	contentType := firstNonEmptyString(input.ContentType, mime.TypeByExtension(filepath.Ext(filename)), "application/octet-stream")
	return agent.FileAttachment{
		DevicePath:    toolCatalogBuilder.agentWorkspacePath(resolvedPath.ConcretePath),
		Filename:      filename,
		ContentType:   contentType,
		SizeBytes:     fileInformation.SizeBytes,
		Title:         strings.TrimSpace(input.Title),
		ContentBase64: base64.StdEncoding.EncodeToString(document),
	}, nil, nil
}

func attachmentFilename(input fileAttachFileInput, resolvedPath string) string {
	if strings.TrimSpace(input.Filename) != "" {
		return strings.TrimSpace(input.Filename)
	}
	return filepath.Base(resolvedPath)
}

func resolveReadableAttachmentMaterial(toolContext context.Context, request ToolCatalogRequest, materialID string) (agent.VisibleContextMaterial, error) {
	if request.AttachmentMaterialResolver == nil {
		return agent.VisibleContextMaterial{}, errors.New("attachment material resolver is unavailable")
	}
	material, errorValue := request.AttachmentMaterialResolver.ResolveAttachmentMaterial(toolContext, materialID)
	if errorValue != nil {
		return agent.VisibleContextMaterial{}, errorValue
	}
	if strings.TrimSpace(material.Path) == "" {
		return agent.VisibleContextMaterial{}, errors.New("attachment material has no readable workspace path")
	}
	return material, nil
}

func validateAttachmentMaterialTool(toolName string, material agent.VisibleContextMaterial) error {
	contentType := strings.ToLower(strings.TrimSpace(material.ContentType))
	switch strings.TrimSpace(toolName) {
	case "image.read":
		if contentType != "" && !strings.HasPrefix(contentType, "image/") {
			return errors.New("attachment material is not an image; use document.read")
		}
	case "document.read":
		if strings.HasPrefix(contentType, "image/") {
			return errors.New("attachment material is an image; use image.read")
		}
	}
	return nil
}
