package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
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

// materializeSiteCreateResult writes the scaffold files the admin service returned
// from site.create into the requester's personal workspace so the model edits them
// with ordinary file tools. The admin service owns the scaffold content; the runtime
// only transports it into the guest workspace the agent can reach.
func (toolCatalogBuilder *ToolCatalogBuilder) materializeSiteCreateResult(toolContext context.Context, request ToolCatalogRequest, result *json.RawMessage) (*agent.ToolResult, error) {
	var payload struct {
		SourceWorkspacePath string           `json:"sourceWorkspacePath"`
		SourceFiles         []siteSourceFile `json:"sourceFiles"`
	}
	if errorValue := json.Unmarshal(*result, &payload); errorValue != nil {
		return nil, nil
	}
	if len(payload.SourceFiles) == 0 || strings.TrimSpace(payload.SourceWorkspacePath) == "" {
		return nil, nil
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

// enrichSitePublishInput bundles the requester's personal site workspace into the
// publish request so the admin service receives the edits the model made in the
// guest workspace.
func (toolCatalogBuilder *ToolCatalogBuilder) enrichSitePublishInput(toolContext context.Context, request ToolCatalogRequest, toolInput json.RawMessage) (json.RawMessage, *agent.ToolResult, error) {
	inputDocument := map[string]any{}
	if len(toolInput) > 0 {
		if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
			return nil, nil, errorValue
		}
	}
	sourceWorkspacePath := siteInputString(inputDocument, "sourceWorkspacePath")
	if sourceWorkspacePath == "" {
		site, errorValue := toolCatalogBuilder.resolveSitePublishSource(toolContext, request, inputDocument)
		if errorValue != nil {
			return nil, nil, errorValue
		}
		if strings.TrimSpace(site.SiteID) != "" {
			inputDocument["siteID"] = site.SiteID
		}
		sourceWorkspacePath = strings.TrimSpace(site.SourceWorkspacePath)
	}
	if sourceWorkspacePath == "" {
		return toolInput, nil, nil
	}
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
	inputDocument["sourceWorkspacePath"] = resolvedSourcePath.VirtualPath
	inputDocument["sourceBundleBase64"] = sourceBundle.ContentBase64
	inputDocument["sourceBundleFormat"] = sourceBundle.Format
	document, errorValue := json.Marshal(inputDocument)
	return document, nil, errorValue
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveSitePublishSource(toolContext context.Context, request ToolCatalogRequest, inputDocument map[string]any) (siteSourceRecord, error) {
	statusInput := map[string]any{}
	for _, key := range []string{"siteID", "slug", "conversationID", "platform"} {
		if value := siteInputString(inputDocument, key); value != "" {
			statusInput[key] = value
		}
	}
	statusRaw, errorValue := json.Marshal(statusInput)
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
	if len(strings.TrimSpace(string(response.Result))) > 0 {
		_ = json.Unmarshal(response.Result, &record)
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
		path := siteSourceFilePath(sourceWorkspace, file.Path)
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

func siteSourceFilePath(sourceWorkspace workspacepath.Directory, relativePath string) workspacepath.Path {
	cleanPath := filepath.Clean(strings.TrimSpace(relativePath))
	return workspacepath.Path{
		ConcretePath:      filepath.Join(sourceWorkspace.ConcretePath, cleanPath),
		VirtualPath:       filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, cleanPath)),
		Kind:              sourceWorkspace.Kind,
		IsDurableArtifact: sourceWorkspace.IsDurableArtifact,
	}
}

func stripSiteSourceFilesFromResult(result *json.RawMessage) error {
	document := map[string]any{}
	if errorValue := json.Unmarshal(*result, &document); errorValue != nil {
		return nil
	}
	delete(document, "sourceFiles")
	cleaned, errorValue := json.Marshal(document)
	if errorValue != nil {
		return errorValue
	}
	*result = cleaned
	return nil
}

func siteInputString(document map[string]any, key string) string {
	value, _ := document[key].(string)
	return strings.TrimSpace(value)
}
