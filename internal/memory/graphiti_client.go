package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultGraphitiEndpoint = "http://127.0.0.1:7791"

type GraphitiClient struct {
	Endpoint   string
	HTTPClient HTTPDoer
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type graphitiSearchResponse struct {
	Facts []MemoryFact `json:"facts"`
}

func NewGraphitiClient(endpoint string, timeout time.Duration) GraphitiClient {
	cleanEndpoint := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if cleanEndpoint == "" {
		cleanEndpoint = DefaultGraphitiEndpoint
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return GraphitiClient{
		Endpoint:   cleanEndpoint,
		HTTPClient: &http.Client{Timeout: timeout},
	}
}

func (client GraphitiClient) AddEpisode(ctx context.Context, episode MemoryEpisode) error {
	return client.post(ctx, "/v1/episodes", episode, nil)
}

func (client GraphitiClient) SearchFacts(ctx context.Context, request MemorySearchRequest) ([]MemoryFact, error) {
	var response graphitiSearchResponse
	errorValue := client.post(ctx, "/v1/search", request, &response)
	if errorValue != nil {
		return nil, errorValue
	}
	return response.Facts, nil
}

func (client GraphitiClient) post(ctx context.Context, path string, requestDocument any, responseDocument any) error {
	if client.HTTPClient == nil {
		return errors.New("graphiti http client is not configured")
	}
	requestBody, errorValue := json.Marshal(requestDocument)
	if errorValue != nil {
		return errorValue
	}
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, client.endpointURL(path), bytes.NewReader(requestBody))
	if errorValue != nil {
		return errorValue
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, errorValue := client.HTTPClient.Do(httpRequest)
	if errorValue != nil {
		return errorValue
	}
	defer httpResponse.Body.Close()

	responseBody, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return errorValue
	}
	if httpResponse.StatusCode >= http.StatusBadRequest {
		return errors.New(strings.TrimSpace(string(responseBody)))
	}
	if responseDocument == nil || len(responseBody) == 0 {
		return nil
	}
	return json.Unmarshal(responseBody, responseDocument)
}

func (client GraphitiClient) endpointURL(path string) string {
	return strings.TrimRight(client.Endpoint, "/") + "/" + strings.TrimLeft(path, "/")
}
