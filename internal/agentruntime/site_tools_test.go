package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
	"blueclaw/internal/security/actortest"
	"blueclaw/internal/workspacepath"
)

func TestSitePublishInputIncludesEditableWorkspaceBundle(t *testing.T) {
	workspacePath := t.TempDir()
	sourceWorkspacePath := filepath.Join(workspacePath, "circles", "staff", "sites", "site-1", "draft")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "dist", "index.html"), "<html>ok</html>")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "node_modules", "ignored.js"), "ignored")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "DESIGN.md"), "custom design")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)

	toolInput, errorValue := toolCatalogBuilder.enrichCapabilityToolInput("site.app.publish", ToolCatalogRequest{
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	}, agent.MarshalToolInput(map[string]any{"siteID": "site-1", "sourceWorkspacePath": "/workspace/circles/staff/sites/site-1"}))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var inputDocument map[string]any
	if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if inputDocument["sourceWorkspacePath"] != "/workspace/circles/staff/sites/site-1/draft" {
		t.Fatalf("unexpected source workspace path: %+v", inputDocument)
	}
	if inputDocument["sourceBundleFormat"] != "tar.gz" {
		t.Fatalf("unexpected source bundle format: %+v", inputDocument)
	}
	bundledPaths := siteSourceBundlePaths(t, inputDocument["sourceBundleBase64"].(string))
	if !containsTestString(bundledPaths, "app/dist/index.html") || !containsTestString(bundledPaths, "DESIGN.md") {
		t.Fatalf("expected source files in bundle, got %+v", bundledPaths)
	}
	if containsTestString(bundledPaths, "app/node_modules/ignored.js") {
		t.Fatalf("expected node_modules to be omitted from bundle: %+v", bundledPaths)
	}
}

func TestSiteReactScaffoldIncludesManagedBuildQualityContract(t *testing.T) {
	files := siteAppScaffoldFiles(siteCreateResult{Slug: "demo-site", Title: "Demo Site"})
	fileMap := map[string]string{}
	for _, file := range files {
		fileMap[file.Path] = file.Content
	}
	for _, path := range []string{
		"app/package.json",
		"app/index.html",
		"app/scripts/build.ts",
		"app/tsconfig.json",
		"app/vite.config.ts",
		"app/src/App.tsx",
		"app/src/main.tsx",
		"app/src/index.css",
		"app/src/prototype-data.ts",
	} {
		if strings.TrimSpace(fileMap[path]) == "" {
			t.Fatalf("expected React scaffold file %s", path)
		}
	}
	if _, exists := fileMap["app/src/content.html"]; exists {
		t.Fatalf("legacy HTML scaffold file should not be present")
	}
	for _, expectedText := range []string{`"react"`, `"vite"`, `"@vitejs/plugin-react"`, `"bun scripts/build.ts"`} {
		if !strings.Contains(fileMap["app/package.json"], expectedText) {
			t.Fatalf("site package manifest must contain %q", expectedText)
		}
	}
	if strings.Contains(fileMap["app/package.json"], "@google/design.md") {
		t.Fatalf("site package manifest must not depend on nested design.md CLI")
	}
	if strings.Contains(fileMap["app/package.json"], `": "^`) {
		t.Fatalf("site package manifest must pin exact dependency versions")
	}
	buildScript := fileMap["app/scripts/build.ts"]
	if strings.Contains(buildScript, "site quality gate failed") {
		t.Fatalf("build script must report quality issues without failing the build")
	}
	if !strings.Contains(buildScript, "suggestedFix") {
		t.Fatalf("build script must include actionable quality fixes")
	}
	for _, forbiddenText := range []string{`Bun.execPath`, `name: "bunx"`} {
		if strings.Contains(buildScript, forbiddenText) {
			t.Fatalf("build script must rely on canonical runtime PATH, not %q", forbiddenText)
		}
	}
	if !strings.Contains(buildScript, `PATH: canonicalRuntimePATH`) {
		t.Fatalf("build script must pass canonical PATH to child commands")
	}
	if strings.Contains(buildScript, `existsSync("node_modules")`) {
		t.Fatalf("build script must refresh dependencies instead of trusting stale node_modules")
	}
	if !strings.Contains(buildScript, `collectDesignQualityIssues`) || !strings.Contains(buildScript, `category: "designDocument"`) {
		t.Fatalf("build script must report DESIGN.md quality issues in-process")
	}
	if strings.Contains(buildScript, "@google/design.md") {
		t.Fatalf("build script must not spawn nested design.md CLI")
	}
	if strings.Contains(buildScript, "DESIGN.md lint failed") {
		t.Fatalf("build script must not fail solely because DESIGN.md quality issues were reported")
	}
	if strings.Contains(buildScript, "DESIGN.md is required") {
		t.Fatalf("build script must not fail solely because DESIGN.md is missing")
	}
	if !strings.Contains(buildScript, `arguments: ["--bun", "./node_modules/vite/bin/vite.js", "build"]`) {
		t.Fatalf("build script must call the installed local Vite with Bun")
	}
	if strings.Contains(buildScript, `import("vite")`) {
		t.Fatalf("build script must not resolve Vite through Bun's package cache")
	}
	if strings.Contains(buildScript, `name: "./node_modules/.bin/vite"`) {
		t.Fatalf("build script must not require a node executable through Vite's shebang")
	}
	if strings.Contains(buildScript, `arguments: ["x", "vite", "build"]`) {
		t.Fatalf("build script must not spawn nested bun x vite")
	}
	viteIndex := strings.Index(buildScript, `await buildVite();`)
	qualityIndex := strings.LastIndex(buildScript, "writeBuildQuality(qualityIssues);")
	if viteIndex < 0 || qualityIndex < viteIndex {
		t.Fatalf("build script must write build-quality.json after vite build")
	}
}

func TestSuccessfulSiteBuildQualityNormalizesAfterBuild(t *testing.T) {
	workspacePath := t.TempDir()
	factory := actortest.NewDirectWorkspaceActorFactory()
	workspaceActor, errorValue := factory.Requester(context.Background(), security.WorkspaceActorRequest{
		WorkspaceRootPath: workspacePath,
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	qualityPath := workspacepath.Path{
		ConcretePath: filepath.Join(workspacePath, "sites", "site-1", ".internkim", "build-quality.json"),
		VirtualPath:  "/workspace/sites/site-1/.internkim/build-quality.json",
	}
	if toolFailure := writeSuccessfulSiteBuildQuality(context.Background(), workspaceActor, qualityPath); toolFailure != nil {
		t.Fatalf("unexpected quality write failure: %s", toolFailure.ContentText())
	}
	document, errorValue := os.ReadFile(qualityPath.ConcretePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(document), `"postBuildNormalized": true`) || !strings.Contains(string(document), `"blockingIssueCount": 0`) {
		t.Fatalf("unexpected normalized build quality: %s", string(document))
	}
}

func TestSiteBuildQualityPayloadReportsIssuesAsSuccessData(t *testing.T) {
	workspacePath := t.TempDir()
	sourceWorkspacePath := filepath.Join(workspacePath, "sites", "site-1")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, ".internkim", "build-quality.json"), `{
  "blockingIssueCount": 1,
  "issues": [
    {
      "severity": "blocking",
      "category": "templateSmell",
      "target": "src/App.tsx",
      "message": "Replace the scaffold starter.",
      "suggestedFix": "Use a domain-specific first screen."
    }
  ]
}`)
	factory := actortest.NewDirectWorkspaceActorFactory()
	workspaceActor, errorValue := factory.Requester(context.Background(), security.WorkspaceActorRequest{
		WorkspaceRootPath: workspacePath,
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	sourceWorkspace := workspacepath.Path{
		ConcretePath: sourceWorkspacePath,
		VirtualPath:  "/workspace/sites/site-1",
	}
	appWorkspace := workspacepath.Path{
		ConcretePath: filepath.Join(sourceWorkspacePath, "app"),
		VirtualPath:  "/workspace/sites/site-1/app",
	}

	payload := siteBuildQualityPayload(context.Background(), workspaceActor, sourceWorkspace, appWorkspace)
	if payload["qualityStatus"] != "delivery_blocked" || payload["qualityIssueCount"] != 1 || payload["blockingIssueCount"] != 1 {
		t.Fatalf("expected quality issue payload, got %+v", payload)
	}
	if payload["deliveryBlocked"] != true {
		t.Fatalf("expected starter leakage to block delivery, got %+v", payload)
	}
	targets, _ := payload["editableTargets"].([]string)
	if !containsTestString(targets, "/workspace/sites/site-1/app/src/App.tsx") {
		t.Fatalf("expected editable target, got %+v", payload)
	}
	if _, exists := payload["recommendedNextActions"]; !exists {
		t.Fatalf("expected recommended next actions, got %+v", payload)
	}
}

func TestSiteDeliveryBlockedBuildResultCreatesRecoveryFailure(t *testing.T) {
	result := siteDeliveryBlockedBuildResult(map[string]any{
		"deliveryBlocked": true,
		"deliveryBlockers": []string{
			"src/App.tsx: Replace the scaffold starter.",
		},
		"editableTargets": []string{"/workspace/sites/site-1/app/src/App.tsx"},
	})
	if result.Failure == nil {
		t.Fatal("expected delivery blocked build to create recoverable failure")
	}
	if result.Failure.Stage != "site_build_delivery" {
		t.Fatalf("expected site_build_delivery failure, got %+v", result.Failure)
	}
	if !containsTestString(result.Failure.RequiredPreconditions, "source_changed") {
		t.Fatalf("expected source_changed precondition, got %+v", result.Failure.RequiredPreconditions)
	}
	if len(result.Failure.RecoveryHints) == 0 || !containsTestString(result.Failure.RecoveryHints[0].ToolNames, "file.write") {
		t.Fatalf("expected file.write recovery hint, got %+v", result.Failure.RecoveryHints)
	}
	if len(result.Failure.AffectedResources) != 1 || result.Failure.AffectedResources[0].Path != "/workspace/sites/site-1/app/src/App.tsx" {
		t.Fatalf("expected affected source resource, got %+v", result.Failure.AffectedResources)
	}
}

func TestSiteBuildCommandFailureClassifiesSourceSyntaxErrors(t *testing.T) {
	result := siteBuildCommandFailureResult(agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_run", `/workspace/private/people/person/sites/site-1/draft/app/src/App.tsx:1:27: ERROR: Syntax error "n"`), workspacepath.Path{
		VirtualPath: "/workspace/circles/staff/sites/site-1/draft/app",
	})
	if result.Failure == nil {
		t.Fatal("expected source syntax failure")
	}
	if result.Failure.Stage != "site_build_source" {
		t.Fatalf("expected site_build_source failure, got %+v", result.Failure)
	}
	if !containsTestString(result.Failure.RequiredPreconditions, "source_changed") {
		t.Fatalf("expected source_changed precondition, got %+v", result.Failure.RequiredPreconditions)
	}
	if len(result.Failure.RecoveryHints) == 0 || !containsTestString(result.Failure.RecoveryHints[0].ToolNames, "file.write") {
		t.Fatalf("expected file.write recovery hint, got %+v", result.Failure.RecoveryHints)
	}
	if len(result.Failure.AffectedResources) != 1 || result.Failure.AffectedResources[0].Path != "/workspace/circles/staff/sites/site-1/draft/app/src/App.tsx" {
		t.Fatalf("expected App.tsx affected resource, got %+v", result.Failure.AffectedResources)
	}
}

func TestSiteCreateMaterializesEditableSourceWithRequesterActor(t *testing.T) {
	workspacePath := t.TempDir()
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","description":"Demo site description","idea":"Demo site idea","purpose":"portfolio","audience":"buyers","archetype":"portfolio","publishedURL":"https://demo.device.example.test","sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft","workspacePath":"/workspace/circles/staff/sites/site-1","status":"draft","ownerIdentity":{"personID":"person-1","displayName":"Owner"}}}`}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.create", "file.read"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.create",
		PolicyResource: "tool:site.app.create",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})
	privateRootPath := filepath.Join(workspacePath, "private", "people", "person-1")
	if errorValue := os.MkdirAll(privateRootPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.Chmod(privateRootPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.create",
		Input:    agent.MarshalToolInput(map[string]string{"slug": "demo"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected site.app.create success, got %s", result.ContentText())
	}
	sourceWorkspacePath := filepath.Join(workspacePath, "circles", "staff", "sites", "site-1", "draft")
	sourceWorkspaceInformation, errorValue := os.Stat(sourceWorkspacePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if sourceWorkspaceInformation.Mode().Perm() != 0770 {
		t.Fatalf("expected staff-circle source workspace permissions 0770, got %v", sourceWorkspaceInformation.Mode().Perm())
	}
	for _, relativePath := range []string{".internkim/site.json", ".internkim/idea.md", ".internkim/artifact-brief.md", ".internkim/review-log.json", "DESIGN.md", "app/package.json", "app/scripts/build.ts", "app/src/App.tsx", "app/src/main.tsx", "app/src/index.css", "app/src/prototype-data.ts"} {
		if _, errorValue := os.Stat(filepath.Join(sourceWorkspacePath, relativePath)); errorValue != nil {
			t.Fatalf("expected materialized source file %s: %v", relativePath, errorValue)
		}
	}
	if _, errorValue := os.Stat(filepath.Join(sourceWorkspacePath, "pocketbase", "pb_hooks", ".gitkeep")); !os.IsNotExist(errorValue) {
		t.Fatalf("site scaffold must not create PocketBase hooks by default: %v", errorValue)
	}
	packageDocument, errorValue := os.ReadFile(filepath.Join(sourceWorkspacePath, "app", "package.json"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(packageDocument), `"build": "bun scripts/build.ts"`) || strings.Contains(string(packageDocument), "latest") || !strings.Contains(string(packageDocument), `"react"`) {
		t.Fatalf("expected React scaffold package manifest, got %s", string(packageDocument))
	}
	metadataDocument, errorValue := os.ReadFile(filepath.Join(sourceWorkspacePath, ".internkim", "site.json"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(metadataDocument), `"idea": "Demo site idea"`) || !strings.Contains(string(metadataDocument), `"archetype": "portfolio"`) || !strings.Contains(string(metadataDocument), `"owner"`) {
		t.Fatalf("expected site metadata mirror, got %s", string(metadataDocument))
	}
	ideaDocument, errorValue := os.ReadFile(filepath.Join(sourceWorkspacePath, ".internkim", "idea.md"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(ideaDocument), "Demo site idea") {
		t.Fatalf("expected site idea mirror, got %s", string(ideaDocument))
	}
	if !strings.Contains(result.ContentText(), `"sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft"`) ||
		!strings.Contains(result.ContentText(), `"appWorkspacePath":"/workspace/circles/staff/sites/site-1/draft/app"`) {
		t.Fatalf("expected virtual source workspace in result, got %s", result.ContentText())
	}
	if strings.Contains(result.ContentText(), `"workspacePath":"home/sites/site-1"`) {
		t.Fatalf("site create result must not encourage non-canonical source paths, got %s", result.ContentText())
	}
	readResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.read",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "/workspace/circles/staff/sites/site-1/draft/app/src/App.tsx",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if readResult.Failed() || !strings.Contains(readResult.ContentText(), "function App") {
		t.Fatalf("expected file.read to inspect materialized site source, got %s", readResult.ContentText())
	}
	workspaceActor, errorValue := toolCatalogBuilder.workspaceActorFactory.Requester(context.Background(), security.WorkspaceActorRequest{
		WorkspaceRootPath: workspacePath,
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	requesterWritePath := workspacepath.Path{
		ConcretePath: filepath.Join(sourceWorkspacePath, "requester-write.txt"),
		VirtualPath:  "/workspace/circles/staff/sites/site-1/draft/requester-write.txt",
	}
	if errorValue := workspaceActor.WriteFile(context.Background(), requesterWritePath, []byte("ok"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	assertTestFileContent(t, requesterWritePath.ConcretePath, "ok")
}

func TestSiteCreateWithoutRequesterPersonIDDoesNotTargetWorkspaceRoot(t *testing.T) {
	workspacePath := t.TempDir()
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft","workspacePath":"/workspace/circles/staff/sites/site-1","status":"draft"}}`}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.create"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.create",
		PolicyResource: "tool:site.app.create",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			Circles: []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.create",
		Input:    agent.MarshalToolInput(map[string]string{"slug": "demo"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "requester personID is required") {
		t.Fatalf("expected requester identity failure, got %s", result.ContentText())
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "sites", "site-1", "draft")); !os.IsNotExist(errorValue) {
		t.Fatalf("site create must not fall back to workspace root, got %v", errorValue)
	}
}

func TestSiteCreateAppWorkspaceSupportsBunLikeBuildRuntime(t *testing.T) {
	workspacePath := t.TempDir()
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","publishedURL":"https://demo.device.example.test","sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft","workspacePath":"/workspace/circles/staff/sites/site-1","status":"draft"}}`}
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.create", "terminal.run"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.create",
		PolicyResource: "tool:site.app.create",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	createResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.create",
		Input:    agent.MarshalToolInput(map[string]string{"slug": "demo"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if createResult.Failed() {
		t.Fatalf("expected site.app.create success, got %s", createResult.ContentText())
	}
	var createDocument map[string]any
	if errorValue := json.Unmarshal([]byte(createResult.ContentText()), &createDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	appWorkspacePath, isString := createDocument["appWorkspacePath"].(string)
	if !isString || strings.TrimSpace(appWorkspacePath) == "" {
		t.Fatalf("expected appWorkspacePath in site.app.create result, got %s", createResult.ContentText())
	}

	buildCommand := `
bun() {
  if [ "$1" != "run" ] || [ "$2" != "build" ]; then
    return 127
  fi
  for directory in "$HOME" "$TMPDIR" "$TMP" "$TEMP" "$XDG_CACHE_HOME" "$XDG_CONFIG_HOME" "$XDG_RUNTIME_DIR" "$BUN_TMPDIR" "$BUN_INSTALL" "$BUN_INSTALL_CACHE_DIR" "$npm_config_cache"; do
    if [ ! -d "$directory" ] || [ ! -w "$directory" ]; then
      echo 'error: AccessDenied accessing temporary directory. Please set $BUN_TMPDIR or $BUN_INSTALL' >&2
      return 74
    fi
  done
  case "$BUN_TMPDIR" in "$HOME"/*) ;; *) echo "BUN_TMPDIR escaped requester home: $BUN_TMPDIR" >&2; return 75 ;; esac
  case "$BUN_INSTALL" in "$HOME"/*) ;; *) echo "BUN_INSTALL escaped requester home: $BUN_INSTALL" >&2; return 75 ;; esac
  test -f package.json || return 76
  mkdir -p dist
  printf ok > dist/index.html
}
bun run build
`
	buildResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"workingDirectoryPath": appWorkspacePath,
			"environmentVariables": map[string]string{
				"BUN_TMPDIR":  "/tmp/not-blueclaw",
				"BUN_INSTALL": "/opt/not-blueclaw",
				"TMPDIR":      "/tmp/not-blueclaw",
			},
			"command": buildCommand,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if buildResult.Failed() {
		t.Fatalf("expected Bun-like build to succeed with requester runtime dirs, got %s", buildResult.ContentText())
	}
	distPath := filepath.Join(workspacePath, "circles", "staff", "sites", "site-1", "draft", "app", "dist", "index.html")
	distDocument, errorValue := os.ReadFile(distPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(distDocument) != "ok" {
		t.Fatalf("expected build output from Bun-like command, got %q", string(distDocument))
	}
}

func TestSiteStatusAnnotatesWorkspaceHealth(t *testing.T) {
	workspacePath := t.TempDir()
	sourceWorkspacePath := filepath.Join(workspacePath, "circles", "staff", "sites", "site-1", "draft")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "src", "App.tsx"), "export default function App() { return null }\n")
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","publishedURL":"https://demo.device.example.test","sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft","workspacePath":"home/sites/site-1","appWorkspacePath":"/workspace/circles/staff/sites/site-1/draft/app","status":"draft"}}`}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.status"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.status",
		PolicyResource: "tool:site.app.status",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.status",
		Input:    agent.MarshalToolInput(map[string]string{"siteID": "site-1"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(result.ContentText(), `"workspaceHealth":"stale_build"`) ||
		!strings.Contains(result.ContentText(), `"suggestedNextTool":"site.app.build"`) ||
		!strings.Contains(result.ContentText(), `"sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft"`) ||
		!strings.Contains(result.ContentText(), `"appWorkspacePath":"/workspace/circles/staff/sites/site-1/draft/app"`) ||
		!strings.Contains(result.ContentText(), `"sourceManifest"`) ||
		!strings.Contains(result.ContentText(), `"path":"/workspace/circles/staff/sites/site-1/draft/.internkim/artifact-brief.md"`) ||
		!strings.Contains(result.ContentText(), `"present":false`) {
		t.Fatalf("expected workspace health annotation, got %s", result.ContentText())
	}
	if strings.Contains(result.ContentText(), `"workspacePath":"home/sites/site-1"`) {
		t.Fatalf("site status must not encourage non-canonical source paths, got %s", result.ContentText())
	}
	if strings.Contains(result.ContentText(), `"publishedURL"`) {
		t.Fatalf("expected draft site status annotation to omit publishedURL, got %s", result.ContentText())
	}
}

func TestSiteStatusMineSchemaAndAnnotationPassThrough(t *testing.T) {
	workspacePath := t.TempDir()
	sourceWorkspacePath := filepath.Join(workspacePath, "circles", "staff", "sites", "site-1", "draft")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "src", "App.tsx"), "export default function App() { return null }\n")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "dist", "index.html"), "<!doctype html><html></html>")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, ".internkim", "build-quality.json"), `{"status":"fresh"}`)
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"status":"ok","sites":[{"siteID":"site-1","slug":"demo","title":"Demo","status":"published","publishedURL":"https://demo.device.example.test","liveHTTPStatus":200,"updatedAt":"2026-06-12T00:00:00Z"},{"siteID":"site-2","slug":"draft","title":"Draft","status":"draft","publishedURL":"https://draft.device.example.test","updatedAt":"2026-06-12T00:00:00Z"}]}}`}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.status"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.status",
		PolicyResource: "tool:site.app.status",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"},"slug":{"type":"string"},"scope":{"type":"string","enum":["conversation","mine"]},"checkLive":{"type":"boolean"}},"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})
	toolDefinition, isFound := findToolDefinition(toolRegistry.ListToolDefinitions(), "site.app.status")
	if !isFound {
		t.Fatal("expected site.app.status definition")
	}
	if !strings.Contains(string(toolDefinition.InputSchema), `"scope"`) || !strings.Contains(string(toolDefinition.InputSchema), `"checkLive"`) {
		t.Fatalf("expected owner-wide live fields in schema, got %s", string(toolDefinition.InputSchema))
	}
	if !strings.Contains(toolDefinition.Description, "scope=mine") || !strings.Contains(toolDefinition.Description, "checkLive=true") {
		t.Fatalf("expected owner-wide live guidance in description, got %s", toolDefinition.Description)
	}

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.status",
		Input: agent.MarshalToolInput(map[string]any{
			"scope":     "mine",
			"checkLive": true,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(httpClient.requestBody, `"scope":"mine"`) || !strings.Contains(httpClient.requestBody, `"checkLive":true`) {
		t.Fatalf("expected owner-wide live input to pass through, got %s", httpClient.requestBody)
	}
	if !strings.Contains(result.ContentText(), `"liveHTTPStatus":200`) || !strings.Contains(result.ContentText(), `"workspaceHealth":"ready"`) {
		t.Fatalf("expected live status and workspace health annotation, got %s", result.ContentText())
	}
	if strings.Contains(result.ContentText(), `"sourceWorkspacePath"`) || strings.Contains(result.ContentText(), `"workspaceHealthDetails"`) {
		t.Fatalf("expected compact owner-wide site records, got %s", result.ContentText())
	}
	if strings.Contains(result.ContentText(), `https://draft.device.example.test`) {
		t.Fatalf("expected non-published site URL to be stripped, got %s", result.ContentText())
	}
}

func TestSiteBuildRejectsSourceSubdirectoryCWD(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.build"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.build",
		Input: agent.MarshalToolInput(map[string]string{
			"appWorkspacePath": "/workspace/circles/staff/sites/site-1/app/src",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "not app/src") || !strings.Contains(result.ContentText(), "/workspace/circles/staff/sites/site-1/app") {
		t.Fatalf("expected canonical cwd failure, got %s", result.ContentText())
	}
}

func TestSiteRepairRecreatesEditableWorkspace(t *testing.T) {
	workspacePath := t.TempDir()
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","description":"Demo site","idea":"Demo idea","purpose":"portfolio","archetype":"portfolio","sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft","appWorkspacePath":"/workspace/circles/staff/sites/site-1/draft/app","status":"draft"}}`}
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.repair"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.status",
		PolicyResource: "tool:site.app.status",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.repair",
		Input:    agent.MarshalToolInput(map[string]string{"siteID": "site-1"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected repair success, got %s", result.ContentText())
	}
	sourceWorkspacePath := filepath.Join(workspacePath, "circles", "staff", "sites", "site-1", "draft")
	for _, relativePath := range []string{".internkim/site.json", ".internkim/idea.md", "DESIGN.md", "app/package.json"} {
		if _, errorValue := os.Stat(filepath.Join(sourceWorkspacePath, relativePath)); errorValue != nil {
			t.Fatalf("expected repaired file %s: %v", relativePath, errorValue)
		}
	}
	if !strings.Contains(result.ContentText(), `"publishedUnchanged":true`) {
		t.Fatalf("expected repair result to preserve published snapshot, got %s", result.ContentText())
	}
}

func TestSiteRepairResolvesCurrentConversationSiteWhenInputIsEmpty(t *testing.T) {
	workspacePath := t.TempDir()
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","description":"Demo site","idea":"Demo idea","purpose":"portfolio","archetype":"portfolio","sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft","appWorkspacePath":"/workspace/circles/staff/sites/site-1/draft/app","status":"failed"}}`}
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.repair"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.status",
		PolicyResource: "tool:site.app.status",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "thread:channel:post",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.repair",
		Input:    agent.MarshalToolInput(map[string]string{}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected context repair success, got %s", result.ContentText())
	}
	if !strings.Contains(httpClient.requestBody, `"conversationID":"thread:channel:post"`) {
		t.Fatalf("expected site.app.status request to include conversation context, got %s", httpClient.requestBody)
	}
	if !strings.Contains(result.ContentText(), `"appWorkspacePath":"/workspace/circles/staff/sites/site-1/draft/app"`) {
		t.Fatalf("expected repaired app workspace path from resolved site, got %s", result.ContentText())
	}
}

func TestSiteCreateAppWorkspaceBuildsOfflineWithBun(t *testing.T) {
	if !terminalTestCanResolveBun() {
		t.Skip("bun is not installed")
	}
	workspacePath := t.TempDir()
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","publishedURL":"https://demo.device.example.test","sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft","workspacePath":"/workspace/circles/staff/sites/site-1","status":"draft"}}`}
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.create", "terminal.run"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.create",
		PolicyResource: "tool:site.app.create",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	createResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.create",
		Input:    agent.MarshalToolInput(map[string]string{"slug": "demo"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if createResult.Failed() {
		t.Fatalf("expected site.app.create success, got %s", createResult.ContentText())
	}
	var createDocument map[string]any
	if errorValue := json.Unmarshal([]byte(createResult.ContentText()), &createDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	appWorkspacePath := createDocument["appWorkspacePath"].(string)

	buildResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"workingDirectoryPath": appWorkspacePath,
			"command":              "bun run build",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if buildResult.Failed() {
		t.Fatalf("expected offline Bun build to succeed, got %s", buildResult.ContentText())
	}
	distPath := filepath.Join(workspacePath, "circles", "staff", "sites", "site-1", "draft", "app", "dist", "index.html")
	distDocument, errorValue := os.ReadFile(distPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	distContent := string(distDocument)
	for _, expectedText := range []string{"Dependency-free site scaffold", "InternKim site prototype loaded"} {
		if !strings.Contains(distContent, expectedText) {
			t.Fatalf("expected built site to contain %q, got %s", expectedText, distContent)
		}
	}
	for _, forbiddenText := range []string{"__SITE_STYLES__", "__SITE_BODY__", "__SITE_SCRIPT__"} {
		if strings.Contains(distContent, forbiddenText) {
			t.Fatalf("expected built site to replace placeholder %q, got %s", forbiddenText, distContent)
		}
	}
}

func TestSitePublishInputRejectsInaccessibleWorkspaceBundle(t *testing.T) {
	workspacePath := t.TempDir()
	sourceWorkspacePath := filepath.Join(workspacePath, "circles", "finance", "sites", "site-1")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "dist", "index.html"), "<html>ok</html>")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)

	_, errorValue := toolCatalogBuilder.enrichCapabilityToolInput("site.app.publish", ToolCatalogRequest{
		PersonAccess: policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	}, agent.MarshalToolInput(map[string]any{
		"siteID":              "site-1",
		"sourceWorkspacePath": "/workspace/circles/finance/sites/site-1",
	}))
	if errorValue == nil {
		t.Fatal("expected inaccessible workspace rejection")
	}
}
