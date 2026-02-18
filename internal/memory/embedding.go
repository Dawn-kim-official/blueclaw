package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

const EmbeddingDimension = 768

type EmbeddingGenerator interface {
	Generate(requestContext context.Context, text string) ([]float32, error)
}

type EmbeddingClient struct {
	endpoint   string
	client     *http.Client
	available  bool
	mutex      sync.RWMutex
}

func NewEmbeddingClient(embeddingPort int) *EmbeddingClient {
	return &EmbeddingClient{
		endpoint:  fmt.Sprintf("http://127.0.0.1:%d/v1/embeddings", embeddingPort),
		client:    &http.Client{Timeout: 30 * time.Second},
		available: false,
	}
}

func (client *EmbeddingClient) IsAvailable() bool {
	client.mutex.RLock()
	defer client.mutex.RUnlock()
	return client.available
}

func (client *EmbeddingClient) SetAvailable(available bool) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.available = available
}

func (client *EmbeddingClient) Generate(requestContext context.Context, text string) ([]float32, error) {
	if !client.IsAvailable() {
		return make([]float32, EmbeddingDimension), nil
	}
	requestBody := embeddingRequest{
		Input: text,
		Model: "embedding",
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling embedding request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating embedding request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, err := client.client.Do(httpRequest)
	if err != nil {
		client.SetAvailable(false)
		return make([]float32, EmbeddingDimension), nil
	}
	defer httpResponse.Body.Close()
	responseBody, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return nil, fmt.Errorf("reading embedding response: %w", err)
	}
	if httpResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding API error (status %d): %s", httpResponse.StatusCode, string(responseBody))
	}
	return parseEmbeddingResponse(responseBody)
}

type embeddingRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

type embeddingResponse struct {
	Data []embeddingData `json:"data"`
}

type embeddingData struct {
	Embedding []float32 `json:"embedding"`
}

func parseEmbeddingResponse(body []byte) ([]float32, error) {
	var response embeddingResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parsing embedding response: %w", err)
	}
	if len(response.Data) == 0 {
		return nil, fmt.Errorf("embedding response contained no data")
	}
	return response.Data[0].Embedding, nil
}

type SidecarProcess struct {
	command        *exec.Cmd
	embeddingPort  int
	modelPath      string
	shutdownSignal chan struct{}
}

func NewSidecarProcess(embeddingPort int, modelPath string) *SidecarProcess {
	return &SidecarProcess{
		embeddingPort:  embeddingPort,
		modelPath:      modelPath,
		shutdownSignal: make(chan struct{}),
	}
}

func (sidecar *SidecarProcess) Start() error {
	if _, err := os.Stat(sidecar.modelPath); err != nil {
		return fmt.Errorf("embedding model not found at %s: %w", sidecar.modelPath, err)
	}
	sidecar.command = exec.Command("llama-server",
		"--model", sidecar.modelPath,
		"--port", fmt.Sprintf("%d", sidecar.embeddingPort),
		"--embedding",
		"--ctx-size", "512",
	)
	sidecar.command.Stdout = os.Stdout
	sidecar.command.Stderr = os.Stderr
	if err := sidecar.command.Start(); err != nil {
		return fmt.Errorf("starting llama-server: %w", err)
	}
	return nil
}

func (sidecar *SidecarProcess) Stop() {
	close(sidecar.shutdownSignal)
	if sidecar.command != nil && sidecar.command.Process != nil {
		sidecar.command.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- sidecar.command.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			sidecar.command.Process.Kill()
		}
	}
}

func (sidecar *SidecarProcess) HealthCheckLoop(client *EmbeddingClient) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	time.Sleep(3 * time.Second)
	for {
		select {
		case <-sidecar.shutdownSignal:
			return
		case <-ticker.C:
			available := sidecar.checkHealth()
			client.SetAvailable(available)
		}
	}
}

func (sidecar *SidecarProcess) checkHealth() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", sidecar.embeddingPort))
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}
