package agentruntime

import (
	"embed"
	"html"
	"io/fs"
	"path/filepath"
	"strings"
)

//go:embed site_scaffold/react-vite-ts
var siteScaffoldFS embed.FS

const siteScaffoldRoot = "site_scaffold/react-vite-ts"

func siteAppScaffoldFiles(site siteCreateResult) []siteStarterFile {
	files := []siteStarterFile{}
	_ = fs.WalkDir(siteScaffoldFS, siteScaffoldRoot, func(path string, directoryEntry fs.DirEntry, walkError error) error {
		if walkError != nil || directoryEntry.IsDir() {
			return nil
		}
		document, readError := siteScaffoldFS.ReadFile(path)
		if readError != nil {
			return nil
		}
		relativePath, relativeError := filepath.Rel(siteScaffoldRoot, path)
		if relativeError != nil {
			return nil
		}
		files = append(files, siteStarterFile{
			Path:    filepath.ToSlash(filepath.Join("app", relativePath)),
			Content: siteScaffoldContent(site, string(document)),
		})
		return nil
	})
	return files
}

func siteManagedBuildScriptContent() []byte {
	document, errorValue := siteScaffoldFS.ReadFile(filepath.Join(siteScaffoldRoot, "scripts", "build.ts"))
	if errorValue != nil {
		return nil
	}
	return document
}

func siteScaffoldContent(site siteCreateResult, content string) string {
	replacements := map[string]string{
		"__SITE_PACKAGE_NAME__": sanitizeWorkspaceSlug(site.Slug),
		"__SITE_TITLE__":        html.EscapeString(firstNonEmptyString(site.Title, site.Slug)),
	}
	result := content
	for key, value := range replacements {
		result = strings.ReplaceAll(result, key, value)
	}
	return result
}
