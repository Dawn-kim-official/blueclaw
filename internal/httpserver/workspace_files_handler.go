package httpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// WorkspaceFilesHandler serves read-only listings and downloads of the guest's
// live workspace filesystem. The workspace lives inside the Firecracker guest
// image, so a host-side file browser cannot read it; admind proxies here to show
// a person their own workspace. Access control is the caller's (admind
// authorizes the web actor before proxying); this handler only reads within the
// workspace root and never writes.
type WorkspaceFilesHandler struct {
	WorkspaceRootPath string
}

type workspaceFileEntry struct {
	Name        string `json:"name"`
	IsDirectory bool   `json:"isDirectory"`
	Size        int64  `json:"size"`
	ModifiedAt  string `json:"modifiedAt"`
}

func (handler WorkspaceFilesHandler) HandleList(responseWriter http.ResponseWriter, request *http.Request) {
	hostPath, ok := handler.resolveHostPath(request.URL.Query().Get("path"))
	if !ok {
		http.Error(responseWriter, "invalid workspace path", http.StatusBadRequest)
		return
	}
	directoryEntries, errorValue := os.ReadDir(hostPath)
	if errorValue != nil {
		if os.IsNotExist(errorValue) {
			writeJSON(responseWriter, map[string]any{"entries": []workspaceFileEntry{}})
			return
		}
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	entries := []workspaceFileEntry{}
	for _, directoryEntry := range directoryEntries {
		if directoryEntry.Name() == ".blueclaw" {
			continue
		}
		information, errorValue := directoryEntry.Info()
		if errorValue != nil {
			continue
		}
		entries = append(entries, workspaceFileEntry{
			Name:        directoryEntry.Name(),
			IsDirectory: directoryEntry.IsDir(),
			Size:        information.Size(),
			ModifiedAt:  information.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(entries, func(first int, second int) bool {
		if entries[first].IsDirectory != entries[second].IsDirectory {
			return entries[first].IsDirectory
		}
		return strings.ToLower(entries[first].Name) < strings.ToLower(entries[second].Name)
	})
	writeJSON(responseWriter, map[string]any{"entries": entries})
}

func (handler WorkspaceFilesHandler) HandleDownload(responseWriter http.ResponseWriter, request *http.Request) {
	hostPath, ok := handler.resolveHostPath(request.URL.Query().Get("path"))
	if !ok {
		http.Error(responseWriter, "invalid workspace path", http.StatusBadRequest)
		return
	}
	information, errorValue := os.Stat(hostPath)
	if errorValue != nil || information.IsDir() {
		http.Error(responseWriter, "file not found", http.StatusNotFound)
		return
	}
	file, errorValue := os.Open(hostPath)
	if errorValue != nil {
		http.Error(responseWriter, "file not readable", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	responseWriter.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(hostPath)+"\"")
	http.ServeContent(responseWriter, request, filepath.Base(hostPath), information.ModTime(), file)
}

func (handler WorkspaceFilesHandler) resolveHostPath(requestedPath string) (string, bool) {
	rootPath := firstNonEmptyWorkspaceRoot(handler.WorkspaceRootPath)
	cleanRoot := filepath.Clean(rootPath)
	relativePath := strings.TrimPrefix(strings.TrimSpace(requestedPath), "/workspace")
	hostPath := filepath.Clean(filepath.Join(cleanRoot, relativePath))
	if hostPath != cleanRoot && !strings.HasPrefix(hostPath, cleanRoot+string(filepath.Separator)) {
		return "", false
	}
	return hostPath, true
}

func firstNonEmptyWorkspaceRoot(workspaceRootPath string) string {
	if trimmed := strings.TrimSpace(workspaceRootPath); trimmed != "" {
		return trimmed
	}
	return "/workspace"
}

func writeJSON(responseWriter http.ResponseWriter, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(responseWriter).Encode(value)
}
