package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"blueclaw/internal/agent"
	"blueclaw/internal/security"
)

const siteSourceBundleMaximumBytes = 64 * 1024 * 1024

func siteToolNeedsSourceBundle(toolName string) bool {
	return strings.TrimSpace(toolName) == "site.serve"
}

func siteSourceBundleExcludeNames() []string {
	return []string{".git", "node_modules", ".cache"}
}

type siteSourceBundleTransport struct {
	WorkspacePath string `json:"workspacePath"`
	ContentBase64 string `json:"contentBase64"`
	Format        string `json:"format"`
	SHA256        string `json:"sha256"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) prepareSiteSourceBundle(toolContext context.Context, request ToolCatalogRequest, toolInput json.RawMessage) (map[string]any, *agent.ToolResult, error) {
	inputDocument := map[string]any{}
	if len(toolInput) > 0 {
		if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
			return nil, nil, errorValue
		}
	}
	sourceWorkspacePath := siteInputString(inputDocument, "sourceWorkspacePath")
	if sourceWorkspacePath == "" {
		return nil, nil, errors.New("sourceWorkspacePath is required")
	}
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
		toolFailure := actorToolFailure("bundle_directory", "site_serve", sourceWorkspacePath, errorValue)
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
