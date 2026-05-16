package agentruntime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"blueclaw/internal/agent"
	"blueclaw/internal/workspacepath"
)

const (
	workspacePathKindDraft        = workspacepath.KindDraft
	workspacePathKindArtifact     = workspacepath.KindArtifact
	workspacePathKindCircle       = workspacepath.KindCircle
	workspacePathKindSharedPublic = workspacepath.KindSharedPublic
	workspacePathKindSkills       = workspacepath.KindSkills
	workspacePathKindWorkspace    = workspacepath.KindWorkspace
)

type WorkspaceScope struct {
	WorkspaceRootPath         string
	RequesterRootPath         string
	RequesterTmpRootPath      string
	RequesterDraftRootPath    string
	RequesterArtifactRootPath string
	DependencyCacheRootPath   string
	CircleRootPath            string
	DefaultDirectoryPath      string
	RequesterPersonID         string
	ConversationCircleID      string
	ConversationResource      string
	ConversationKind          string
}

type ResolvedWorkspacePath = workspacepath.Path

type WorkspacePathResolver struct {
	WorkspaceRootPath string
}

func WorkspaceScopeForRequest(workspaceRootPath string, request ToolCatalogRequest, taskRunID string) WorkspaceScope {
	conversationScope := ConversationScopeForRequest(workspaceRootPath, request)
	personID := strings.TrimSpace(request.RequesterPersonID)
	requesterRootPath := workspaceRootPath
	requesterTmpRootPath := filepath.Join(workspaceRootPath, "tmp")
	requesterDraftRootPath := requesterTmpRootPath
	requesterArtifactRootPath := filepath.Join(workspaceRootPath, "artifacts")
	if personID != "" {
		requesterRootPath = filepath.Join(workspaceRootPath, "private", "people", personID)
		requesterTmpRootPath = filepath.Join(requesterRootPath, "tmp")
		requesterArtifactRootPath = filepath.Join(requesterRootPath, "artifacts")
		requesterDraftRootPath = requesterTmpRootPath
		if strings.TrimSpace(taskRunID) != "" {
			requesterDraftRootPath = filepath.Join(requesterTmpRootPath, sanitizeWorkspaceSlug(taskRunID))
		}
	}
	circleRootPath := ""
	if strings.TrimSpace(conversationScope.CircleID) != "" {
		circleRootPath = filepath.Join(workspaceRootPath, "circles", strings.TrimSpace(conversationScope.CircleID))
	}
	return WorkspaceScope{
		WorkspaceRootPath:         workspaceRootPath,
		RequesterRootPath:         requesterRootPath,
		RequesterTmpRootPath:      requesterTmpRootPath,
		RequesterDraftRootPath:    requesterDraftRootPath,
		RequesterArtifactRootPath: requesterArtifactRootPath,
		DependencyCacheRootPath:   filepath.Join(workspaceRootPath, "shared", "cache", "dependencies"),
		CircleRootPath:            circleRootPath,
		DefaultDirectoryPath:      requesterDraftRootPath,
		RequesterPersonID:         personID,
		ConversationCircleID:      conversationScope.CircleID,
		ConversationResource:      conversationScope.Resource,
		ConversationKind:          conversationScope.Kind,
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) workspaceScopeForToolContext(toolContext context.Context, request ToolCatalogRequest) WorkspaceScope {
	return WorkspaceScopeForRequest(toolCatalogBuilder.workspaceRootPath, request, agent.TaskRunIDFromContext(toolContext))
}

func NewWorkspacePathResolver(workspaceRootPath string) WorkspacePathResolver {
	return WorkspacePathResolver{WorkspaceRootPath: workspaceRootPath}
}

func (resolver WorkspacePathResolver) Resolve(value string, scope WorkspaceScope) (ResolvedWorkspacePath, error) {
	trimmedPath := strings.TrimSpace(value)
	if trimmedPath == "" {
		return ResolvedWorkspacePath{}, errors.New("path is required")
	}
	if strings.HasPrefix(trimmedPath, "~") {
		return ResolvedWorkspacePath{}, errors.New("home-relative paths are not supported; use tmp/<slug> or artifacts/<slug>")
	}
	if isDeniedAbsoluteWorkspacePath(trimmedPath) {
		return ResolvedWorkspacePath{}, errors.New("path is outside the supported workspace artifact contract")
	}
	if filepath.IsAbs(trimmedPath) {
		return resolver.resolveAbsolute(trimmedPath, scope)
	}
	return resolver.resolveVirtual(trimmedPath, scope)
}

func (resolver WorkspacePathResolver) ResolveDirectory(value string, scope WorkspaceScope) (ResolvedWorkspacePath, error) {
	if strings.TrimSpace(value) == "" {
		return resolver.Resolve("tmp", scope)
	}
	return resolver.Resolve(value, scope)
}

func (resolver WorkspacePathResolver) resolveVirtual(value string, scope WorkspaceScope) (ResolvedWorkspacePath, error) {
	cleanPath := filepath.Clean(value)
	if cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, "../") {
		return ResolvedWorkspacePath{}, errors.New("relative path traversal is not supported")
	}
	if cleanPath == "tmp" {
		return resolver.resolvedPath(scope.RequesterDraftRootPath, "tmp", workspacePathKindDraft, false)
	}
	if strings.HasPrefix(cleanPath, "tmp/") {
		suffix := strings.TrimPrefix(cleanPath, "tmp/")
		return resolver.resolvedPath(filepath.Join(scope.RequesterDraftRootPath, suffix), filepath.ToSlash(cleanPath), workspacePathKindDraft, false)
	}
	if cleanPath == "artifacts" {
		return resolver.resolvedPath(scope.RequesterArtifactRootPath, "artifacts", workspacePathKindArtifact, true)
	}
	if strings.HasPrefix(cleanPath, "artifacts/") {
		suffix := strings.TrimPrefix(cleanPath, "artifacts/")
		return resolver.resolvedPath(filepath.Join(scope.RequesterArtifactRootPath, suffix), filepath.ToSlash(cleanPath), workspacePathKindArtifact, true)
	}
	return resolver.resolvedPath(filepath.Join(scope.DefaultDirectoryPath, cleanPath), filepath.ToSlash(filepath.Join("tmp", cleanPath)), workspacePathKindDraft, false)
}

func (resolver WorkspacePathResolver) resolveAbsolute(value string, scope WorkspaceScope) (ResolvedWorkspacePath, error) {
	workspacePath := resolver.resolveAgentWorkspacePath(value)
	cleanWorkspaceRootPath, errorValue := filepath.Abs(resolver.WorkspaceRootPath)
	if errorValue != nil {
		return ResolvedWorkspacePath{}, errorValue
	}
	cleanPath, errorValue := filepath.Abs(filepath.Clean(workspacePath))
	if errorValue != nil {
		return ResolvedWorkspacePath{}, errorValue
	}
	relativePath, errorValue := filepath.Rel(cleanWorkspaceRootPath, cleanPath)
	if errorValue != nil {
		return ResolvedWorkspacePath{}, errorValue
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, "../") {
		return ResolvedWorkspacePath{}, errors.New("path must stay under the workspace root")
	}
	virtualPath := filepath.ToSlash(filepath.Join("/workspace", relativePath))
	parts := strings.Split(filepath.ToSlash(relativePath), "/")
	if len(parts) >= 1 && parts[0] == ".blueclaw" {
		return ResolvedWorkspacePath{}, errors.New("/workspace/.blueclaw is not available for model tool work")
	}
	if len(parts) >= 1 && parts[0] == "skills" {
		return ResolvedWorkspacePath{ConcretePath: cleanPath, VirtualPath: virtualPath, Kind: workspacePathKindSkills}, nil
	}
	if len(parts) >= 3 && parts[0] == "private" && parts[1] == "people" {
		if scope.RequesterPersonID == "" || parts[2] != scope.RequesterPersonID {
			return ResolvedWorkspacePath{}, errors.New("another person's private workspace is not available")
		}
		if len(parts) >= 4 && parts[3] == "artifacts" {
			return ResolvedWorkspacePath{ConcretePath: cleanPath, VirtualPath: virtualPath, Kind: workspacePathKindArtifact, IsDurableArtifact: true}, nil
		}
		if len(parts) >= 4 && parts[3] == "tmp" {
			return ResolvedWorkspacePath{ConcretePath: cleanPath, VirtualPath: virtualPath, Kind: workspacePathKindDraft}, nil
		}
		return ResolvedWorkspacePath{ConcretePath: cleanPath, VirtualPath: virtualPath, Kind: workspacePathKindWorkspace}, nil
	}
	if len(parts) >= 2 && parts[0] == "circles" {
		return ResolvedWorkspacePath{ConcretePath: cleanPath, VirtualPath: virtualPath, Kind: workspacePathKindCircle, IsDurableArtifact: true}, nil
	}
	if len(parts) >= 2 && parts[0] == "shared" && parts[1] == "public" {
		return ResolvedWorkspacePath{ConcretePath: cleanPath, VirtualPath: virtualPath, Kind: workspacePathKindSharedPublic, IsDurableArtifact: true}, nil
	}
	return ResolvedWorkspacePath{ConcretePath: cleanPath, VirtualPath: virtualPath, Kind: workspacePathKindWorkspace}, nil
}

func (resolver WorkspacePathResolver) resolvedPath(concretePath string, virtualPath string, kind workspacepath.Kind, isDurableArtifact bool) (ResolvedWorkspacePath, error) {
	cleanWorkspaceRootPath, errorValue := filepath.Abs(resolver.WorkspaceRootPath)
	if errorValue != nil {
		return ResolvedWorkspacePath{}, errorValue
	}
	cleanPath, errorValue := filepath.Abs(filepath.Clean(concretePath))
	if errorValue != nil {
		return ResolvedWorkspacePath{}, errorValue
	}
	relativePath, errorValue := filepath.Rel(cleanWorkspaceRootPath, cleanPath)
	if errorValue != nil {
		return ResolvedWorkspacePath{}, errorValue
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, "../") {
		return ResolvedWorkspacePath{}, errors.New("path must stay under the workspace root")
	}
	return ResolvedWorkspacePath{
		ConcretePath:      cleanPath,
		VirtualPath:       filepath.ToSlash(virtualPath),
		Kind:              kind,
		IsDurableArtifact: isDurableArtifact,
	}, nil
}

func (resolver WorkspacePathResolver) resolveAgentWorkspacePath(value string) string {
	trimmedPath := strings.TrimSpace(value)
	if resolver.WorkspaceRootPath == "/workspace" {
		return trimmedPath
	}
	if trimmedPath == "/workspace" {
		return resolver.WorkspaceRootPath
	}
	if strings.HasPrefix(trimmedPath, "/workspace/") {
		return filepath.Join(resolver.WorkspaceRootPath, strings.TrimPrefix(trimmedPath, "/workspace/"))
	}
	return trimmedPath
}

func isDeniedAbsoluteWorkspacePath(path string) bool {
	cleanPath := filepath.Clean(path)
	return cleanPath == "/tmp" ||
		strings.HasPrefix(cleanPath, "/tmp/") ||
		cleanPath == "/opt" ||
		strings.HasPrefix(cleanPath, "/opt/") ||
		cleanPath == "/usr" ||
		strings.HasPrefix(cleanPath, "/usr/")
}

func (scope WorkspaceScope) EnvironmentVariables() map[string]string {
	return map[string]string{
		"BLUECLAW_REQUESTER_TMP":       scope.RequesterTmpRootPath,
		"BLUECLAW_TASK_TMP":            scope.RequesterDraftRootPath,
		"BLUECLAW_REQUESTER_ARTIFACTS": scope.RequesterArtifactRootPath,
		"BLUECLAW_DEPENDENCY_CACHE":    scope.DependencyCacheRootPath,
	}
}

func sanitizeWorkspaceSlug(value string) string {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return "task"
	}
	builder := strings.Builder{}
	for _, character := range trimmedValue {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == '.' {
			builder.WriteRune(character)
		} else {
			builder.WriteRune('-')
		}
	}
	result := strings.Trim(builder.String(), "-.")
	if result == "" {
		return "task"
	}
	return result
}
