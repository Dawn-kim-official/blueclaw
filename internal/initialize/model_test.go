package initialize

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureEmbeddingModelSkipsExistingFile(t *testing.T) {
	temporaryDirectory := t.TempDir()
	modelsPath := filepath.Join(temporaryDirectory, "models")
	if err := os.MkdirAll(modelsPath, 0755); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(modelsPath, "embedding.gguf")
	if err := os.WriteFile(modelPath, []byte("fake model"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureEmbeddingModel(temporaryDirectory, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, _ := os.ReadFile(modelPath)
	if string(content) != "fake model" {
		t.Error("existing model file was overwritten")
	}
}

func TestDownloadFileWritesContent(t *testing.T) {
	expectedContent := "model-binary-data"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Write([]byte(expectedContent))
	}))
	defer server.Close()
	destinationPath := filepath.Join(t.TempDir(), "test.gguf")
	if err := downloadFile(server.URL, destinationPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(content) != expectedContent {
		t.Errorf("got %q, want %q", string(content), expectedContent)
	}
}

func TestDownloadFileCleansUpOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	destinationPath := filepath.Join(t.TempDir(), "test.gguf")
	err := downloadFile(server.URL, destinationPath)
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
	if _, statError := os.Stat(destinationPath); !os.IsNotExist(statError) {
		t.Error("destination file should not exist after failed download")
	}
	temporaryPath := destinationPath + ".download"
	if _, statError := os.Stat(temporaryPath); !os.IsNotExist(statError) {
		t.Error("temporary file should be cleaned up after failed download")
	}
}

func TestDownloadFileAtomicRename(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Write([]byte("complete"))
	}))
	defer server.Close()
	destinationPath := filepath.Join(t.TempDir(), "test.gguf")
	if err := downloadFile(server.URL, destinationPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	temporaryPath := destinationPath + ".download"
	if _, err := os.Stat(temporaryPath); !os.IsNotExist(err) {
		t.Error("temporary .download file should not remain after successful download")
	}
}
