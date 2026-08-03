package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func sequentialSessionTokens() func() string {
	grantCount := 0
	return func() string {
		grantCount++
		return "session-token-" + strconv.Itoa(grantCount)
	}
}

func TestToolCatalogHandlerRefusesAConnectionWithoutABearerToken(t *testing.T) {
	invokedTool := ""
	resolver := NewSessionTokenRequesterResolver(sequentialSessionTokens())
	if _, errorValue := resolver.GrantSessionToken(RequesterToolSet{RequesterPersonID: "person-1", ToolSet: testToolSet(t, &invokedTool)}); errorValue != nil {
		t.Fatalf("expected a session grant: %v", errorValue)
	}
	server := httptest.NewServer(NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(server.Close)

	for _, testCase := range []struct {
		name        string
		bearerToken string
	}{
		{name: "no token", bearerToken: ""},
		{name: "unknown token", bearerToken: "session-token-forged"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request, _ := http.NewRequest(http.MethodPost, server.URL, nil)
			if testCase.bearerToken != "" {
				request.Header.Set("Authorization", "Bearer "+testCase.bearerToken)
			}
			response, errorValue := server.Client().Do(request)
			if errorValue != nil {
				t.Fatalf("expected a response: %v", errorValue)
			}
			defer response.Body.Close()
			if response.StatusCode < 400 {
				t.Fatalf("expected the tool catalog to refuse an unidentified caller, got %d", response.StatusCode)
			}
		})
	}
}

func TestToolCatalogHandlerServesTheToolSetTheTokenWasGrantedFor(t *testing.T) {
	firstInvokedTool := ""
	secondInvokedTool := ""
	resolver := NewSessionTokenRequesterResolver(sequentialSessionTokens())
	firstToolSet := testToolSet(t, &firstInvokedTool)
	secondToolSet := testToolSet(t, &secondInvokedTool)
	firstToken, _ := resolver.GrantSessionToken(RequesterToolSet{RequesterPersonID: "person-1", ToolSet: firstToolSet})
	secondToken, _ := resolver.GrantSessionToken(RequesterToolSet{RequesterPersonID: "person-2", ToolSet: secondToolSet.WithAllowedToolNames([]string{"file_read"})})

	server := httptest.NewServer(NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(server.Close)

	if toolNames := connectedToolNames(t, server.URL, firstToken); len(toolNames) != 2 {
		t.Fatalf("expected person-1 to see their whole catalog, got %+v", toolNames)
	}
	toolNames := connectedToolNames(t, server.URL, secondToken)
	if len(toolNames) != 1 || toolNames[0] != "file_read" {
		t.Fatalf("expected person-2 to see only their narrowed catalog, got %+v", toolNames)
	}

	resolver.RevokeSessionToken(firstToken)
	if _, errorValue := connectToolCatalog(server.URL, firstToken); errorValue == nil {
		t.Fatal("expected a revoked session to stop serving")
	}
}

func connectToolCatalog(serverURL string, bearerToken string) (*mcp.ClientSession, error) {
	transport := &mcp.StreamableClientTransport{
		Endpoint:   serverURL,
		HTTPClient: &http.Client{Transport: bearerTokenRoundTripper{bearerToken: bearerToken}},
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-harness", Version: "test"}, nil)
	return client.Connect(context.Background(), transport, nil)
}

func connectedToolNames(t *testing.T, serverURL string, bearerToken string) []string {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint:   serverURL,
		HTTPClient: &http.Client{Transport: bearerTokenRoundTripper{bearerToken: bearerToken}},
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-harness", Version: "test"}, nil)
	clientSession, errorValue := client.Connect(context.Background(), transport, nil)
	if errorValue != nil {
		t.Fatalf("expected the harness to connect: %v", errorValue)
	}
	defer clientSession.Close()
	toolList, errorValue := clientSession.ListTools(context.Background(), nil)
	if errorValue != nil {
		t.Fatalf("expected the harness to list tools: %v", errorValue)
	}
	toolNames := []string{}
	for _, tool := range toolList.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	return toolNames
}

type bearerTokenRoundTripper struct {
	bearerToken string
}

func (roundTripper bearerTokenRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if roundTripper.bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+roundTripper.bearerToken)
	}
	return http.DefaultTransport.RoundTrip(request)
}
