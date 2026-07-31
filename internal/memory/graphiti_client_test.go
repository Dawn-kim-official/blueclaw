package memory

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGraphitiClientAddsEpisodeThroughSidecar(t *testing.T) {
	var requestedPath string
	client := GraphitiClient{
		Endpoint: "http://graphiti-memoryd",
		HTTPClient: fakeGraphitiHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
			requestedPath = request.URL.Path
			if request.Header.Get("Authorization") != "" {
				t.Fatalf("expected no authorization header, got %q", request.Header.Get("Authorization"))
			}
			return graphitiResponse(http.StatusOK, `{"episodeID":"mattermost:dm-1:post-1","namespaceCount":1}`), nil
		}},
	}

	result, errorValue := client.AddEpisode(context.Background(), MemoryEpisode{
		EpisodeID:      "mattermost:dm-1:post-1",
		Prompt:         "my name is Sam",
		OccurredAt:     time.Now().UTC(),
		Namespaces:     []MemoryNamespace{UserNamespace("person-1")},
		SenderPersonID: "person-1",
	})
	if errorValue != nil {
		t.Fatalf("expected episode to be added: %v", errorValue)
	}
	if requestedPath != "/v1/episodes" {
		t.Fatalf("expected episode endpoint, got %q", requestedPath)
	}
	if result.EpisodeID != "mattermost:dm-1:post-1" || result.NamespaceCount != 1 {
		t.Fatalf("expected ingestion result, got %+v", result)
	}
}

func TestGraphitiClientSearchesFacts(t *testing.T) {
	client := GraphitiClient{
		Endpoint: "http://graphiti-memoryd",
		HTTPClient: fakeGraphitiHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/v1/search" {
				t.Fatalf("expected search endpoint, got %q", request.URL.Path)
			}
			return graphitiResponse(http.StatusOK, `{"facts":[{"factID":"fact-1","scopeType":"user","namespaceID":"user:person-1","content":"the user's name is Sam.","score":0.91}]}`), nil
		}},
	}

	facts, errorValue := client.SearchFacts(context.Background(), MemorySearchRequest{
		Query:      "what is my name?",
		Namespaces: []MemoryNamespace{UserNamespace("person-1")},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if len(facts) != 1 {
		t.Fatalf("expected one fact, got %d", len(facts))
	}
	if facts[0].Content != "the user's name is Sam." {
		t.Fatalf("expected fact content, got %q", facts[0].Content)
	}
}

func TestGraphitiClientDeletesEpisodeThroughSidecar(t *testing.T) {
	client := GraphitiClient{
		Endpoint: "http://graphiti-memoryd",
		HTTPClient: fakeGraphitiHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/v1/episodes/delete" {
				t.Fatalf("expected delete endpoint, got %q", request.URL.Path)
			}
			body, errorValue := io.ReadAll(request.Body)
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !strings.Contains(string(body), `"episodeID":"episode-1"`) || !strings.Contains(string(body), `"namespaceIDs":["user:person-1"]`) {
				t.Fatalf("unexpected delete body %s", string(body))
			}
			return graphitiResponse(http.StatusOK, `{"episodeID":"episode-1","deleted":true,"namespaceCount":1}`), nil
		}},
	}

	result, errorValue := client.DeleteEpisode(context.Background(), MemoryEpisodeDeleteRequest{
		EpisodeID:    "episode-1",
		NamespaceIDs: []string{"user:person-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected delete to succeed: %v", errorValue)
	}
	if !result.Deleted || result.NamespaceCount != 1 {
		t.Fatalf("expected delete result, got %+v", result)
	}
}

func TestGraphitiClientChecksHealth(t *testing.T) {
	client := GraphitiClient{
		Endpoint: "http://graphiti-memoryd",
		HTTPClient: fakeGraphitiHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodGet || request.URL.Path != "/health" {
				t.Fatalf("expected health request, got %s %s", request.Method, request.URL.Path)
			}
			return graphitiResponse(http.StatusOK, `{"status":"ok"}`), nil
		}},
	}

	if errorValue := client.CheckHealth(context.Background()); errorValue != nil {
		t.Fatalf("expected health to pass: %v", errorValue)
	}
}

func TestGraphitiClientDefaultTimeoutAllowsGraphitiStartupLatency(t *testing.T) {
	client := NewGraphitiClient("http://graphiti-memoryd", 0)
	httpClient, isHTTPClient := client.HTTPClient.(*http.Client)
	if !isHTTPClient {
		t.Fatalf("expected standard HTTP client, got %T", client.HTTPClient)
	}
	if httpClient.Timeout != 60*time.Second {
		t.Fatalf("expected 60 second timeout, got %s", httpClient.Timeout)
	}
}

type fakeGraphitiHTTPClient struct {
	handler func(*http.Request) (*http.Response, error)
}

func (client fakeGraphitiHTTPClient) Do(request *http.Request) (*http.Response, error) {
	return client.handler(request)
}

func graphitiResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}
