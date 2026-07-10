package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceFilesHandlerListsAndDownloads(t *testing.T) {
	rootPath := t.TempDir()
	deckDirectory := filepath.Join(rootPath, "private", "people", "person-1", "tmp", "deck", "build")
	if errorValue := os.MkdirAll(deckDirectory, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(deckDirectory, "deck.pptx"), []byte("deck-bytes"), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.MkdirAll(filepath.Join(rootPath, "private", "people", "person-1", ".blueclaw"), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	handler := WorkspaceFilesHandler{WorkspaceRootPath: rootPath}

	recorder := httptest.NewRecorder()
	handler.HandleList(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/workspace/list?path=/workspace/private/people/person-1", nil))
	var listResponse struct {
		Entries []workspaceFileEntry `json:"entries"`
	}
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &listResponse); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(listResponse.Entries) != 1 || listResponse.Entries[0].Name != "tmp" || !listResponse.Entries[0].IsDirectory {
		t.Fatalf("expected only the tmp directory (with .blueclaw hidden), got %+v", listResponse.Entries)
	}

	downloadRecorder := httptest.NewRecorder()
	handler.HandleDownload(downloadRecorder, httptest.NewRequest(http.MethodGet, "/admin/api/workspace/download?path=/workspace/private/people/person-1/tmp/deck/build/deck.pptx", nil))
	if downloadRecorder.Code != http.StatusOK || downloadRecorder.Body.String() != "deck-bytes" {
		t.Fatalf("expected the deck bytes, got status %d body %q", downloadRecorder.Code, downloadRecorder.Body.String())
	}
}

func TestWorkspaceFilesHandlerRejectsPathEscape(t *testing.T) {
	handler := WorkspaceFilesHandler{WorkspaceRootPath: t.TempDir()}
	recorder := httptest.NewRecorder()
	handler.HandleList(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/workspace/list?path=/workspace/../../etc", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected a path escape to be rejected, got status %d", recorder.Code)
	}
}
