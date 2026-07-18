package agentruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"blueclaw/internal/capability"
	"blueclaw/internal/workspacepath"
)

func TestSiteSourceFilePathRejectsPathsOutsideSourceWorkspace(t *testing.T) {
	sourceWorkspace := workspacepath.Directory{
		ConcretePath: "/workspace/circles/staff/sites/site-1/draft",
		VirtualPath:  "/workspace/circles/staff/sites/site-1/draft",
	}
	for _, candidate := range []string{"", ".", "..", "../secret", "/workspace/other"} {
		if _, errorValue := siteSourceFilePath(sourceWorkspace, candidate); errorValue == nil {
			t.Fatalf("expected path %q to be rejected", candidate)
		}
	}
	path, errorValue := siteSourceFilePath(sourceWorkspace, "app/src/App.tsx")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if path.VirtualPath != "/workspace/circles/staff/sites/site-1/draft/app/src/App.tsx" {
		t.Fatalf("unexpected source path: %+v", path)
	}
}

func TestValidateSiteSourceFilesRejectsDuplicateCanonicalPaths(t *testing.T) {
	errorValue := validateSiteSourceFiles([]siteSourceFile{
		{Path: "app/src/App.tsx"},
		{Path: "app/src/../src/App.tsx"},
	})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "unique") {
		t.Fatalf("expected duplicate source path rejection, got %v", errorValue)
	}
}

func TestMaterializeSiteCreateResultRejectsMalformedBridgePayload(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	for _, result := range []json.RawMessage{
		json.RawMessage(`{`),
		json.RawMessage(`{"siteID":"site-1","sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft"}`),
		json.RawMessage(`{"siteID":"site-1","sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft","sourceFiles":[{"path":"../secret","content":"value"}]}`),
	} {
		toolFailure, errorValue := toolCatalogBuilder.materializeSiteCreateResult(context.Background(), ToolCatalogRequest{}, &result)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if toolFailure == nil || !toolFailure.Failed() {
			t.Fatalf("expected malformed bridge payload failure, got %+v", toolFailure)
		}
	}
}

func TestResolveSitePublishSourceUsesExactSiteID(t *testing.T) {
	httpClient := &recordingHTTPClient{
		responseBody: `{"result":{"siteID":"site-1","slug":"support","sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft"}}`,
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(
		capability.Client{Endpoint: "http://capability", HTTPClient: httpClient},
		[]CapabilityToolDescriptor{{Name: "site.status"}},
	)

	record, errorValue := toolCatalogBuilder.resolveSitePublishSource(context.Background(), ToolCatalogRequest{}, "site-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if record.SiteID != "site-1" || record.SourceWorkspacePath != "/workspace/circles/staff/sites/site-1/draft" {
		t.Fatalf("unexpected site source record: %+v", record)
	}
	var requestDocument struct {
		Input map[string]any `json:"input"`
	}
	if errorValue := json.Unmarshal([]byte(httpClient.requestBody), &requestDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(requestDocument.Input) != 1 || requestDocument.Input["siteReference"] != "site-1" {
		t.Fatalf("expected exact site reference lookup, got %s", httpClient.requestBody)
	}
}

func TestResolveSitePublishSourceRejectsInvalidStatusResult(t *testing.T) {
	for _, responseBody := range []string{
		`{"result":`,
		`{"result":{"siteID":"site-2","sourceWorkspacePath":"/workspace/circles/staff/sites/site-2/draft"}}`,
		`{"result":{"siteID":"site-1"}}`,
	} {
		httpClient := &recordingHTTPClient{responseBody: responseBody}
		toolCatalogBuilder := NewToolCatalogBuilder()
		toolCatalogBuilder.UseTestCapabilityToolDescriptors(
			capability.Client{Endpoint: "http://capability", HTTPClient: httpClient},
			[]CapabilityToolDescriptor{{Name: "site.status"}},
		)
		if _, errorValue := toolCatalogBuilder.resolveSitePublishSource(context.Background(), ToolCatalogRequest{}, "site-1"); errorValue == nil {
			t.Fatalf("expected invalid status result rejection for %s", responseBody)
		}
	}
}

func TestSiteSourceBundleSHA256IdentifiesDecodedBundle(t *testing.T) {
	contentBase64 := base64.StdEncoding.EncodeToString([]byte("site source bundle"))
	sourceSHA256, errorValue := siteSourceBundleSHA256(contentBase64)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if sourceSHA256 != "254cc09182b94752e96474af9ba307f74dcfff4e8dfa5b0c4a76f97e634c1c28" {
		t.Fatalf("unexpected source SHA-256: %s", sourceSHA256)
	}
	if _, errorValue := siteSourceBundleSHA256("not-base64"); errorValue == nil {
		t.Fatal("expected invalid base64 rejection")
	}
}
