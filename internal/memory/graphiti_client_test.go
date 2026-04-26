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
			return graphitiResponse(http.StatusOK, `{}`), nil
		}},
	}

	errorValue := client.AddEpisode(context.Background(), MemoryEpisode{
		EpisodeID:      "mattermost:dm-1:post-1",
		Prompt:         "내 이름은 민수야",
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
}

func TestGraphitiClientSearchesFacts(t *testing.T) {
	client := GraphitiClient{
		Endpoint: "http://graphiti-memoryd",
		HTTPClient: fakeGraphitiHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/v1/search" {
				t.Fatalf("expected search endpoint, got %q", request.URL.Path)
			}
			return graphitiResponse(http.StatusOK, `{"facts":[{"factID":"fact-1","scopeType":"user","namespaceID":"user:person-1","content":"사용자의 이름은 민수다.","score":0.91}]}`), nil
		}},
	}

	facts, errorValue := client.SearchFacts(context.Background(), MemorySearchRequest{
		Query:      "내 이름 뭐야?",
		Namespaces: []MemoryNamespace{UserNamespace("person-1")},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if len(facts) != 1 {
		t.Fatalf("expected one fact, got %d", len(facts))
	}
	if facts[0].Content != "사용자의 이름은 민수다." {
		t.Fatalf("expected fact content, got %q", facts[0].Content)
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
