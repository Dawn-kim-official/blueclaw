package agentruntime

import (
	"path/filepath"
	"strings"
	"testing"

	"blueclaw/internal/policy"
	"blueclaw/internal/security"
)

func TestWorkspacePathResolverResolvesOutsideWorkspacePathsForPOSIXToDecide(t *testing.T) {
	workspacePath := t.TempDir()
	resolver := NewWorkspacePathResolver(workspacePath)
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")

	resolvedPath, errorValue := resolver.Resolve("/tmp/a", scope)

	if errorValue != nil {
		t.Fatalf("expected outside-workspace paths to resolve so POSIX decides access, got %v", errorValue)
	}
	if resolvedPath.ConcretePath != "/tmp/a" {
		t.Fatalf("expected the real path unchanged, got %+v", resolvedPath)
	}
}

func TestWorkspacePathResolverResolvesConcretePrivatePathInsteadOfRejecting(t *testing.T) {
	workspacePath := t.TempDir()
	resolver := NewWorkspacePathResolver(workspacePath)
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")
	resolvedPath, errorValue := resolver.Resolve(filepath.Join(workspacePath, "private", "people", "person-1", "documents", "a.docx"), scope)
	if errorValue != nil {
		t.Fatalf("concrete own private path must resolve, not be rejected: %v", errorValue)
	}
	expectedConcretePath := filepath.Join(workspacePath, "private", "people", "person-1", "documents", "a.docx")
	if resolvedPath.ConcretePath != expectedConcretePath {
		t.Fatalf("unexpected concrete path: %+v", resolvedPath)
	}
}

func TestWorkspacePathResolverResolvesCurrentDirectoryToRequesterRootInsteadOfRejecting(t *testing.T) {
	workspacePath := t.TempDir()
	resolver := NewWorkspacePathResolver(workspacePath)
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")
	resolvedPath, errorValue := resolver.Resolve(".", scope)
	if errorValue != nil {
		t.Fatalf("current directory must resolve to the requester root, not be rejected as traversal: %v", errorValue)
	}
	if resolvedPath.ConcretePath != filepath.Join(workspacePath, "private", "people", "person-1") {
		t.Fatalf("unexpected current-directory root: %+v", resolvedPath)
	}
	// A relative path that stays inside the workspace resolves; POSIX and policy, not a
	// string filter, decide whether the actor may use it.
	if _, errorValue := resolver.Resolve("../person-2", scope); errorValue != nil {
		t.Fatalf("in-workspace relative path must resolve and be gated by POSIX, not string-rejected: %v", errorValue)
	}
	// Escaping the workspace root or reaching service internals is still refused by the
	// concrete-path boundary.
	if _, errorValue := resolver.Resolve("../../../../../../etc/passwd", scope); errorValue == nil {
		t.Fatal("escaping the workspace root must be refused")
	}
	if _, errorValue := resolver.Resolve("../../../../../.blueclaw/state", scope); errorValue == nil {
		t.Fatal("reaching service-internal .blueclaw must be refused")
	}
}

func TestWorkspacePathResolverExpandsHomeTildeToRequesterPrivateRoot(t *testing.T) {
	workspacePath := t.TempDir()
	resolver := NewWorkspacePathResolver(workspacePath)
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")
	resolvedPath, errorValue := resolver.Resolve("~/documents/회의록.md", scope)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedConcretePath := filepath.Join(workspacePath, "private", "people", "person-1", "documents", "회의록.md")
	if resolvedPath.ConcretePath != expectedConcretePath {
		t.Fatalf("unexpected concrete path: %+v", resolvedPath)
	}
	if resolvedPath.VirtualPath != "documents/회의록.md" {
		t.Fatalf("unexpected virtual path: %+v", resolvedPath)
	}
	homeRoot, errorValue := resolver.Resolve("~", scope)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if homeRoot.ConcretePath != filepath.Join(workspacePath, "private", "people", "person-1") {
		t.Fatalf("unexpected home root: %+v", homeRoot)
	}
}

func TestWorkspacePathResolverMapsTemporaryDirectoryToRequesterDraft(t *testing.T) {
	workspacePath := t.TempDir()
	resolver := NewWorkspacePathResolver(workspacePath)
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")
	resolvedPath, errorValue := resolver.ResolveDirectory("/tmp/capability", scope)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedConcretePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "capability")
	if resolvedPath.ConcretePath != expectedConcretePath {
		t.Fatalf("unexpected concrete path: %+v", resolvedPath)
	}
	if resolvedPath.VirtualPath != "tmp/capability" {
		t.Fatalf("unexpected virtual path: %+v", resolvedPath)
	}
}

func TestWorkspacePathResolverResolvesVirtualHomePrefixToRequesterRoot(t *testing.T) {
	workspacePath := t.TempDir()
	resolver := NewWorkspacePathResolver(workspacePath)
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")
	resolvedPath, errorValue := resolver.Resolve("home/inbox/mattermost/conv-1/check.json", scope)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedConcretePath := filepath.Join(workspacePath, "private", "people", "person-1", "inbox", "mattermost", "conv-1", "check.json")
	if resolvedPath.ConcretePath != expectedConcretePath {
		t.Fatalf("unexpected concrete path: %+v", resolvedPath)
	}
	if resolvedPath.VirtualPath != "home/inbox/mattermost/conv-1/check.json" {
		t.Fatalf("unexpected virtual path: %+v", resolvedPath)
	}
	tildePath, errorValue := resolver.Resolve("~/inbox/mattermost/conv-1/check.json", scope)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if tildePath.ConcretePath != resolvedPath.ConcretePath {
		t.Fatalf("expected home/ and ~/ to name the same file, got %+v and %+v", resolvedPath, tildePath)
	}
	homeRoot, errorValue := resolver.Resolve("home", scope)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if homeRoot.ConcretePath != filepath.Join(workspacePath, "private", "people", "person-1") {
		t.Fatalf("unexpected home root: %+v", homeRoot)
	}
}

func TestWorkspacePathResolverResolvesHomeRelativePathToRequesterRoot(t *testing.T) {
	workspacePath := t.TempDir()
	resolver := NewWorkspacePathResolver(workspacePath)
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")
	resolvedPath, errorValue := resolver.Resolve("sites/site-1/DESIGN.md", scope)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if resolvedPath.VirtualPath != "sites/site-1/DESIGN.md" {
		t.Fatalf("unexpected virtual path: %+v", resolvedPath)
	}
	expectedConcretePath := filepath.Join(workspacePath, "private", "people", "person-1", "sites", "site-1", "DESIGN.md")
	if resolvedPath.ConcretePath != expectedConcretePath {
		t.Fatalf("unexpected concrete path: %+v", resolvedPath)
	}
}

func TestWorkspacePathResolverUsesPersonAccessWhenRequesterPersonIDIsEmpty(t *testing.T) {
	workspacePath := t.TempDir()
	resolver := NewWorkspacePathResolver(workspacePath)
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{
		PersonAccess: policy.PersonAccess{PersonID: "person-1"},
	}, "task-1")
	resolvedPath, errorValue := resolver.Resolve("sites/site-1/DESIGN.md", scope)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedConcretePath := filepath.Join(workspacePath, "private", "people", "person-1", "sites", "site-1", "DESIGN.md")
	if resolvedPath.ConcretePath != expectedConcretePath {
		t.Fatalf("unexpected concrete path: %+v", resolvedPath)
	}
}

func TestWorkspaceScopeEnvironmentStaysUnderRequesterPrivateRoot(t *testing.T) {
	workspacePath := t.TempDir()
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")
	environmentVariables := scope.EnvironmentVariables()
	requesterRootPath := filepath.Join(workspacePath, "private", "people", "person-1")
	taskRuntimeRootPath := filepath.Join(requesterRootPath, "tmp", ".runtime")
	if environmentVariables["PATH"] != security.CanonicalRuntimePATH {
		t.Fatalf("expected canonical runtime PATH, got %+v", environmentVariables)
	}
	expectedEnvironmentPaths := map[string]string{
		"HOME":                  requesterRootPath,
		"TMPDIR":                filepath.Join(taskRuntimeRootPath, "tmp"),
		"TMP":                   filepath.Join(taskRuntimeRootPath, "tmp"),
		"TEMP":                  filepath.Join(taskRuntimeRootPath, "tmp"),
		"XDG_CACHE_HOME":        filepath.Join(taskRuntimeRootPath, "cache"),
		"XDG_CONFIG_HOME":       filepath.Join(taskRuntimeRootPath, "config"),
		"XDG_RUNTIME_DIR":       filepath.Join(taskRuntimeRootPath, "runtime"),
		"BUN_TMPDIR":            filepath.Join(taskRuntimeRootPath, "bun", "tmp"),
		"BUN_INSTALL":           filepath.Join(taskRuntimeRootPath, "bun", "install"),
		"BUN_INSTALL_CACHE_DIR": filepath.Join(workspacePath, "shared", "cache", "dependencies", "bun"),
		"npm_config_cache":      filepath.Join(taskRuntimeRootPath, "npm"),
	}
	for name, expectedPath := range expectedEnvironmentPaths {
		actualPath := environmentVariables[name]
		if actualPath != expectedPath {
			t.Fatalf("expected %s to be %s, got %s", name, expectedPath, actualPath)
		}
		if name != "HOME" && name != "BUN_INSTALL_CACHE_DIR" && !strings.HasPrefix(actualPath, requesterRootPath+string(filepath.Separator)) {
			t.Fatalf("expected %s to stay under requester root, got %s", name, actualPath)
		}
		for _, deniedPrefix := range []string{"/tmp", "/opt", filepath.Join(workspacePath, "tmp"), filepath.Join(workspacePath, ".blueclaw")} {
			if actualPath == deniedPrefix || strings.HasPrefix(actualPath, deniedPrefix+string(filepath.Separator)) {
				t.Fatalf("expected %s to avoid denied prefix %s, got %s", name, deniedPrefix, actualPath)
			}
		}
	}
}
