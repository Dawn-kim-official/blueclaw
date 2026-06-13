package agentruntime

import (
	"path/filepath"
	"strings"
	"testing"

	"blueclaw/internal/policy"
	"blueclaw/internal/security"
)

func TestWorkspacePathResolverRejectsDeniedPrefixes(t *testing.T) {
	workspacePath := t.TempDir()
	resolver := NewWorkspacePathResolver(workspacePath)
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")
	for _, path := range []string{"/tmp/a", "~/.cache/a", "/workspace/.blueclaw/tmp/a", "/workspace/private/people/person-1/tmp/a", "/workspace/private/people/person-2/tmp/a", "../escape"} {
		if _, errorValue := resolver.Resolve(path, scope); errorValue == nil {
			t.Fatalf("expected resolver to reject %q", path)
		}
	}
}

func TestWorkspacePathResolverMapsVirtualHomeToRequesterPrivateRoot(t *testing.T) {
	workspacePath := t.TempDir()
	resolver := NewWorkspacePathResolver(workspacePath)
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")
	resolvedPath, errorValue := resolver.Resolve("home/sites/site-1/DESIGN.md", scope)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if resolvedPath.VirtualPath != "home/sites/site-1/DESIGN.md" {
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
	resolvedPath, errorValue := resolver.Resolve("home/sites/site-1/DESIGN.md", scope)
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
	taskRuntimeRootPath := filepath.Join(requesterRootPath, "tmp", "task-1", ".runtime")
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
		"BUN_INSTALL_CACHE_DIR": filepath.Join(taskRuntimeRootPath, "bun", "cache"),
		"npm_config_cache":      filepath.Join(taskRuntimeRootPath, "npm"),
	}
	for name, expectedPath := range expectedEnvironmentPaths {
		actualPath := environmentVariables[name]
		if actualPath != expectedPath {
			t.Fatalf("expected %s to be %s, got %s", name, expectedPath, actualPath)
		}
		if name != "HOME" && !strings.HasPrefix(actualPath, requesterRootPath+string(filepath.Separator)) {
			t.Fatalf("expected %s to stay under requester root, got %s", name, actualPath)
		}
		for _, deniedPrefix := range []string{"/tmp", "/opt", filepath.Join(workspacePath, "tmp"), filepath.Join(workspacePath, ".blueclaw")} {
			if actualPath == deniedPrefix || strings.HasPrefix(actualPath, deniedPrefix+string(filepath.Separator)) {
				t.Fatalf("expected %s to avoid denied prefix %s, got %s", name, deniedPrefix, actualPath)
			}
		}
	}
}
