package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"

	"blueclaw/internal/agent"
	"blueclaw/internal/security"
)

const siteSourceBundleMaximumBytes = 64 * 1024 * 1024

func siteToolNeedsSourceBundle(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "site.publish", "site.preview":
		return true
	default:
		return false
	}
}

func siteSourceBundleExcludeNames() []string {
	return []string{".git", "node_modules", ".cache"}
}

type siteSourceFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type siteSourceRecord struct {
	SiteID              string `json:"siteID"`
	Slug                string `json:"slug"`
	SourceWorkspacePath string `json:"sourceWorkspacePath"`
}

type siteSourceBundleTransport struct {
	WorkspacePath string `json:"workspacePath"`
	ContentBase64 string `json:"contentBase64"`
	Format        string `json:"format"`
	SHA256        string `json:"sha256"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) materializeSiteCreateResult(toolContext context.Context, request ToolCatalogRequest, result *json.RawMessage) (*agent.ToolResult, error) {
	var payload struct {
		SourceWorkspacePath string           `json:"sourceWorkspacePath"`
		SourceFiles         []siteSourceFile `json:"sourceFiles"`
	}
	if errorValue := json.Unmarshal(*result, &payload); errorValue != nil {
		return invalidSiteCapabilityResult("site.create", "result is not valid JSON"), nil
	}
	if len(payload.SourceFiles) == 0 || strings.TrimSpace(payload.SourceWorkspacePath) == "" {
		return invalidSiteCapabilityResult("site.create", "result must include sourceWorkspacePath and sourceFiles"), nil
	}
	if errorValue := validateSiteSourceFiles(payload.SourceFiles); errorValue != nil {
		return invalidSiteCapabilityResult("site.create", errorValue.Error()), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return actorFailure, nil
	}
	sourceWorkspacePath := toolCatalogBuilder.nativeRequesterPath(request, payload.SourceWorkspacePath)
	if toolFailure := writeSiteSourceFiles(toolContext, workspaceActor, sourceWorkspacePath, payload.SourceFiles); toolFailure != nil {
		return toolFailure, nil
	}
	return nil, stripSiteSourceFilesFromResult(result)
}

func (toolCatalogBuilder *ToolCatalogBuilder) removeSiteProjectAfterDelete(toolContext context.Context, request ToolCatalogRequest, result *json.RawMessage) (*agent.ToolResult, error) {
	var payload struct {
		SiteID  string `json:"siteID"`
		Deleted bool   `json:"deleted"`
	}
	if json.Unmarshal(*result, &payload) != nil || !payload.Deleted || strings.TrimSpace(payload.SiteID) == "" {
		return nil, nil
	}
	guestProjectPath := "~/sites/" + strings.TrimSpace(payload.SiteID)
	outcome, actorFailure := toolCatalogBuilder.runRequesterShell(toolContext, request, requesterShellCommand{
		Command: "rm -rf -- " + shellPathArgument(guestProjectPath),
	})
	if actorFailure != nil {
		return nil, nil
	}
	if outcome.RunError != nil {
		slog.Warn("site.delete guest project cleanup failed", "path", guestProjectPath, "error", outcome.RunError)
	}
	return nil, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) prepareSiteSourceBundle(toolContext context.Context, request ToolCatalogRequest, toolInput json.RawMessage) (map[string]any, *agent.ToolResult, error) {
	inputDocument := map[string]any{}
	if len(toolInput) > 0 {
		if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
			return nil, nil, errorValue
		}
	}
	siteID := siteInputString(inputDocument, "siteID")
	if siteID == "" {
		return nil, nil, errors.New("siteID is required")
	}
	site, errorValue := toolCatalogBuilder.resolveSitePublishSource(toolContext, request, siteID)
	if errorValue != nil {
		return nil, nil, errorValue
	}
	sourceWorkspacePath := strings.TrimSpace(site.SourceWorkspacePath)
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return nil, actorFailure, nil
	}
	sourcePath := toolCatalogBuilder.nativeRequesterPath(request, sourceWorkspacePath)
	sourceBundle, errorValue := workspaceActor.BundleDirectory(toolContext, sourcePath, security.WorkspaceActorBundleOptions{
		Format:       "tar.gz",
		MaxBytes:     siteSourceBundleMaximumBytes,
		ExcludeNames: siteSourceBundleExcludeNames(),
	})
	if errorValue != nil {
		toolFailure := actorToolFailure("bundle_directory", "site_publish", sourceWorkspacePath, errorValue)
		return nil, &toolFailure, nil
	}
	sourceSHA256, errorValue := siteSourceBundleSHA256(sourceBundle.ContentBase64)
	if errorValue != nil {
		return nil, nil, errorValue
	}
	transport := siteSourceBundleTransport{
		WorkspacePath: sourceWorkspacePath,
		ContentBase64: sourceBundle.ContentBase64,
		Format:        sourceBundle.Format,
		SHA256:        sourceSHA256,
	}
	return map[string]any{"siteSourceBundle": transport}, nil, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveSitePublishSource(toolContext context.Context, request ToolCatalogRequest, siteID string) (siteSourceRecord, error) {
	statusRaw, errorValue := json.Marshal(map[string]string{"siteReference": siteID})
	if errorValue != nil {
		return siteSourceRecord{}, errorValue
	}
	var response struct {
		Result json.RawMessage `json:"result"`
	}
	requestDocument, errorValue := toolCatalogBuilder.capabilityRequestForOperation(toolContext, "site.status", request, statusRaw)
	if errorValue != nil {
		return siteSourceRecord{}, errorValue
	}
	if errorValue := toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/site.status/invoke", requestDocument, &response); errorValue != nil {
		return siteSourceRecord{}, errorValue
	}
	record := siteSourceRecord{}
	if errorValue := json.Unmarshal(response.Result, &record); errorValue != nil {
		return siteSourceRecord{}, errors.New("site.status returned invalid result")
	}
	if strings.TrimSpace(record.SiteID) != siteID {
		return siteSourceRecord{}, errors.New("site.status returned a different siteID")
	}
	if strings.TrimSpace(record.SourceWorkspacePath) == "" {
		return siteSourceRecord{}, errors.New("site.status result is missing sourceWorkspacePath")
	}
	return record, nil
}

func writeSiteSourceFiles(toolContext context.Context, workspaceActor security.WorkspaceActor, sourceWorkspacePath string, files []siteSourceFile) *agent.ToolResult {
	if errorValue := workspaceActor.MkdirAll(toolContext, sourceWorkspacePath); errorValue != nil {
		toolFailure := actorToolFailure("mkdir_all", "site_create", sourceWorkspacePath, errorValue)
		return &toolFailure
	}
	for _, file := range files {
		path, errorValue := siteSourceFilePath(sourceWorkspacePath, file.Path)
		if errorValue != nil {
			return invalidSiteCapabilityResult("site.create", errorValue.Error())
		}
		if siteSourceFileAlreadyPresent(toolContext, workspaceActor, path) {
			continue
		}
		if errorValue := workspaceActor.MkdirAll(toolContext, filepath.Dir(path)); errorValue != nil {
			toolFailure := actorToolFailure("mkdir_all", "site_create", path, errorValue)
			return &toolFailure
		}
		if errorValue := workspaceActor.WriteFile(toolContext, path, []byte(file.Content)); errorValue != nil {
			toolFailure := actorToolFailure("write_file", "site_create", path, errorValue)
			return &toolFailure
		}
	}
	return nil
}

func siteSourceFileAlreadyPresent(toolContext context.Context, workspaceActor security.WorkspaceActor, path string) bool {
	information, errorValue := workspaceActor.Stat(toolContext, path)
	return errorValue == nil && information.IsRegular
}

func siteSourceFilePath(sourceWorkspacePath string, relativePath string) (string, error) {
	canonicalRelativePath, errorValue := canonicalSiteSourceRelativePath(relativePath)
	if errorValue != nil {
		return "", errorValue
	}
	return filepath.Join(sourceWorkspacePath, canonicalRelativePath), nil
}

func canonicalSiteSourceRelativePath(relativePath string) (string, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(relativePath))
	if cleanPath == "." || filepath.IsAbs(cleanPath) || cleanPath == ".." || strings.HasPrefix(filepath.ToSlash(cleanPath), "../") {
		return "", errors.New("site.create sourceFiles paths must be relative to sourceWorkspacePath")
	}
	return cleanPath, nil
}

func validateSiteSourceFiles(files []siteSourceFile) error {
	seenPaths := map[string]bool{}
	for _, file := range files {
		canonicalRelativePath, errorValue := canonicalSiteSourceRelativePath(file.Path)
		if errorValue != nil {
			return errorValue
		}
		if seenPaths[canonicalRelativePath] {
			return errors.New("site.create sourceFiles paths must be unique")
		}
		seenPaths[canonicalRelativePath] = true
	}
	return nil
}

func stripSiteSourceFilesFromResult(result *json.RawMessage) error {
	document := map[string]any{}
	if errorValue := json.Unmarshal(*result, &document); errorValue != nil {
		return errorValue
	}
	delete(document, "sourceFiles")
	cleaned, errorValue := json.Marshal(document)
	if errorValue != nil {
		return errorValue
	}
	*result = cleaned
	return nil
}

func invalidSiteCapabilityResult(toolName string, message string) *agent.ToolResult {
	failureStage := strings.ReplaceAll(toolName, ".", "_") + "_result"
	result := agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, failureStage, message)
	return &result
}

func siteSourceBundleSHA256(contentBase64 string) (string, error) {
	content, errorValue := base64.StdEncoding.DecodeString(contentBase64)
	if errorValue != nil {
		return "", errors.New("site source bundle is not valid base64")
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func siteInputString(document map[string]any, key string) string {
	value, _ := document[key].(string)
	return strings.TrimSpace(value)
}
