package initialize

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

const (
	embeddingModelURL      = "https://huggingface.co/ggml-org/embeddinggemma-300M-qat-q4_0-GGUF/resolve/main/embeddinggemma-300M-qat-Q4_0.gguf"
	embeddingModelFilename = "embedding.gguf"
	modelsDirectory        = "models"
)

func EnsureEmbeddingModel(blueclawDirectory string, forceDownload bool) error {
	modelPath := filepath.Join(blueclawDirectory, modelsDirectory, embeddingModelFilename)
	if !forceDownload {
		if _, err := os.Stat(modelPath); err == nil {
			return nil
		}
	}
	modelsPath := filepath.Join(blueclawDirectory, modelsDirectory)
	if err := os.MkdirAll(modelsPath, 0755); err != nil {
		return fmt.Errorf("creating models directory: %w", err)
	}
	log.Printf("downloading embedding model to %s (this may take a moment)...", modelPath)
	return downloadFile(embeddingModelURL, modelPath)
}

func downloadFile(sourceURL string, destinationPath string) error {
	temporaryPath := destinationPath + ".download"
	response, err := http.Get(sourceURL)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", sourceURL, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading model: HTTP %d", response.StatusCode)
	}
	temporaryFile, err := os.Create(temporaryPath)
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}
	written, err := io.Copy(temporaryFile, response.Body)
	if closeError := temporaryFile.Close(); closeError != nil && err == nil {
		err = closeError
	}
	if err != nil {
		os.Remove(temporaryPath)
		return fmt.Errorf("writing model file: %w", err)
	}
	log.Printf("embedding model downloaded (%d MB)", written/1024/1024)
	if err := os.Rename(temporaryPath, destinationPath); err != nil {
		os.Remove(temporaryPath)
		return fmt.Errorf("moving model file into place: %w", err)
	}
	return nil
}
