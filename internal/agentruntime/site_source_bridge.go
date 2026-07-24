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

	"blueclaw/internal/access"
	"blueclaw/internal/agent"
	"blueclaw/internal/security"
	"blueclaw/internal/workspacepath"
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
	sourceWorkspace, errorValue := toolCatalogBuilder.resolveWritableSiteWorkspace(request, payload.SourceWorkspacePath)
	if errorValue != nil {
		return nil, errorValue
	}
	if toolFailure := writeSiteSourceFiles(toolContext, workspaceActor, sourceWorkspace, payload.SourceFiles); toolFailure != nil {
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
	resolvedPath, errorValue := toolCatalogBuilder.resolveCapabilityWorkspacePath(request, "~/sites/"+strings.TrimSpace(payload.SiteID))
	if errorValue != nil {
		return nil, nil
	}
	outcome, actorFailure := toolCatalogBuilder.runRequesterShell(toolContext, request, requesterShellCommand{
		Command: "rm -rf -- " + shellSingleQuoted(resolvedPath.ConcretePath),
	})
	if actorFailure != nil {
		return nil, nil
	}
	if outcome.RunError != nil {
		slog.Warn("site.delete guest project cleanup failed", "path", resolvedPath.VirtualPath, "error", outcome.RunError)
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
	resolvedSourcePath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(sourceWorkspacePath, WorkspaceScopeForRequest(toolCatalogBuilder.workspaceRootPath, request, ""))
	if errorValue != nil {
		return nil, nil, errorValue
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionRead, resolvedSourcePath.ConcretePath) {
		return nil, nil, errors.New("current account cannot publish this site workspace path")
	}
	sourceBundle, errorValue := workspaceActor.BundleDirectory(toolContext, workspacepath.Directory(resolvedSourcePath), security.WorkspaceActorBundleOptions{
		Format:       "tar.gz",
		MaxBytes:     siteSourceBundleMaximumBytes,
		ExcludeNames: siteSourceBundleExcludeNames(),
	})
	if errorValue != nil {
		toolFailure := actorToolFailure("bundle_directory", "site_publish", resolvedSourcePath.VirtualPath, errorValue)
		return nil, &toolFailure, nil
	}
	sourceSHA256, errorValue := siteSourceBundleSHA256(sourceBundle.ContentBase64)
	if errorValue != nil {
		return nil, nil, errorValue
	}
	transport := siteSourceBundleTransport{
		WorkspacePath: resolvedSourcePath.VirtualPath,
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

func (toolCatalogBuilder *ToolCatalogBuilder) resolveWritableSiteWorkspace(request ToolCatalogRequest, path string) (workspacepath.Directory, error) {
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(path, WorkspaceScopeForRequest(toolCatalogBuilder.workspaceRootPath, request, ""))
	if errorValue != nil {
		return workspacepath.Directory{}, errorValue
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionWrite, resolvedPath.ConcretePath) {
		return workspacepath.Directory{}, errors.New("current account cannot write this site workspace path")
	}
	return workspacepath.Directory(resolvedPath), nil
}

func writeSiteSourceFiles(toolContext context.Context, workspaceActor security.WorkspaceActor, sourceWorkspace workspacepath.Directory, files []siteSourceFile) *agent.ToolResult {
	if errorValue := workspaceActor.MkdirAll(toolContext, sourceWorkspace, workspaceDirectoryCreateMode(sourceWorkspace)); errorValue != nil {
		toolFailure := actorToolFailure("mkdir_all", "site_create", sourceWorkspace.VirtualPath, errorValue)
		return &toolFailure
	}
	for _, file := range files {
		path, errorValue := siteSourceFilePath(sourceWorkspace, file.Path)
		if errorValue != nil {
			return invalidSiteCapabilityResult("site.create", errorValue.Error())
		}
		if siteSourceFileAlreadyPresent(toolContext, workspaceActor, path) {
			continue
		}
		if errorValue := workspaceActor.MkdirAll(toolContext, path.Parent(), workspaceDirectoryCreateMode(path.Parent())); errorValue != nil {
			toolFailure := actorToolFailure("mkdir_all", "site_create", path.VirtualPath, errorValue)
			return &toolFailure
		}
		if errorValue := workspaceActor.WriteFile(toolContext, path, []byte(file.Content), workspaceFileCreateMode(path)); errorValue != nil {
			toolFailure := actorToolFailure("write_file", "site_create", path.VirtualPath, errorValue)
			return &toolFailure
		}
	}
	return nil
}

func siteSourceFileAlreadyPresent(toolContext context.Context, workspaceActor security.WorkspaceActor, path workspacepath.Path) bool {
	information, errorValue := workspaceActor.Stat(toolContext, path)
	return errorValue == nil && information.IsRegular
}

func siteSourceFilePath(sourceWorkspace workspacepath.Directory, relativePath string) (workspacepath.Path, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(relativePath))
	if cleanPath == "." || filepath.IsAbs(cleanPath) || cleanPath == ".." || strings.HasPrefix(filepath.ToSlash(cleanPath), "../") {
		return workspacepath.Path{}, errors.New("site.create sourceFiles paths must be relative to sourceWorkspacePath")
	}
	return workspacepath.Path{
		ConcretePath:      filepath.Join(sourceWorkspace.ConcretePath, cleanPath),
		VirtualPath:       filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, cleanPath)),
		Kind:              sourceWorkspace.Kind,
		IsDurableArtifact: sourceWorkspace.IsDurableArtifact,
	}, nil
}

func validateSiteSourceFiles(files []siteSourceFile) error {
	seenPaths := map[string]bool{}
	for _, file := range files {
		path, errorValue := siteSourceFilePath(workspacepath.Directory{}, file.Path)
		if errorValue != nil {
			return errorValue
		}
		if seenPaths[path.VirtualPath] {
			return errors.New("site.create sourceFiles paths must be unique")
		}
		seenPaths[path.VirtualPath] = true
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
