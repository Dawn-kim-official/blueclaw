package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Dawn-kim-official/blueclaw/internal/agentruntime"
	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/mcpserver"
	"github.com/Dawn-kim-official/blueclaw/internal/policy"
	"github.com/Dawn-kim-official/blueclaw/internal/security"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

type actorRequestRecorder struct {
	mutex           sync.Mutex
	personIDByOrder []string
}

func (recorder *actorRequestRecorder) Requester(_ context.Context, request security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	recorder.mutex.Lock()
	recorder.personIDByOrder = append(recorder.personIDByOrder, request.PersonAccess.PersonID)
	recorder.mutex.Unlock()
	return &recordingWorkspaceActor{factory: &recordingWorkspaceActorFactory{}}, nil
}

func (recorder *actorRequestRecorder) personIDs() []string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]string{}, recorder.personIDByOrder...)
}

func requesterToolSetFor(t *testing.T, personID string, actorFactory *actorRequestRecorder, workspaceRootPath string) *toolcontract.ToolSet {
	t.Helper()
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspaceRootPath)
	toolCatalogBuilder.UseTerminalService(security.NewTerminalSessionService(config.TerminalConfiguration{
		Mode:              "native",
		WorkspaceRootPath: workspaceRootPath,
		TimeoutSecond:     30,
		OutputMaxBytes:    32768,
		SessionMaxCount:   4,
	}))
	toolCatalogBuilder.UseWorkspaceActorFactory(actorFactory)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {toolcontract.TerminalRunToolName},
	}, nil)
	return toolCatalogBuilder.BuildToolSet(agentruntime.ToolCatalogRequest{
		RequesterPersonID: personID,
		ProfileName:       "default",
		Prompt:            "목록 보여줘",
		PersonAccess:      policy.PersonAccess{PersonID: personID, SecurityLevelRank: 100},
	})
}

func TestConcurrentRequestersNeverBorrowEachOthersIdentity(t *testing.T) {
	workspaceRootPath := t.TempDir()
	actorFactory := &actorRequestRecorder{}
	requesterPersonIDs := []string{"person-a", "person-b", "person-c", "person-d"}

	tokenCount := 0
	tokenMutex := sync.Mutex{}
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string {
		tokenMutex.Lock()
		defer tokenMutex.Unlock()
		tokenCount++
		return "session-token-" + string(rune('a'+tokenCount-1))
	})
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(catalogServer.Close)

	sessionTokenByPerson := map[string]string{}
	for _, personID := range requesterPersonIDs {
		sessionToken, errorValue := resolver.GrantSessionToken(mcpserver.RequesterToolSet{
			RequesterPersonID: personID,
			ToolSet:           requesterToolSetFor(t, personID, actorFactory, workspaceRootPath),
		})
		if errorValue != nil {
			t.Fatalf("expected a catalog grant for %s: %v", personID, errorValue)
		}
		sessionTokenByPerson[personID] = sessionToken
	}

	callsPerRequester := 5
	waitGroup := sync.WaitGroup{}
	failures := make(chan string, len(requesterPersonIDs)*callsPerRequester)
	for _, personID := range requesterPersonIDs {
		waitGroup.Add(1)
		go func(personID string, sessionToken string) {
			defer waitGroup.Done()
			clientSession, errorValue := mcp.NewClient(&mcp.Implementation{Name: personID, Version: "test"}, nil).Connect(context.Background(), &mcp.StreamableClientTransport{
				Endpoint:   catalogServer.URL,
				HTTPClient: &http.Client{Transport: catalogBearer{bearerToken: sessionToken}},
			}, nil)
			if errorValue != nil {
				failures <- personID + " could not reach the catalog: " + errorValue.Error()
				return
			}
			defer clientSession.Close()
			for callIndex := 0; callIndex < callsPerRequester; callIndex++ {
				callResult, errorValue := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
					Name:      toolcontract.TerminalRunToolName,
					Arguments: map[string]any{"command": "ls"},
				})
				if errorValue != nil {
					failures <- personID + " call failed: " + errorValue.Error()
					return
				}
				if callResult.IsError {
					failures <- personID + " tool reported an error"
					return
				}
			}
		}(personID, sessionTokenByPerson[personID])
	}
	waitGroup.Wait()
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}

	observedPersonIDs := actorFactory.personIDs()
	if len(observedPersonIDs) != len(requesterPersonIDs)*callsPerRequester {
		t.Fatalf("expected every concurrent call to resolve its own actor, got %d for %d calls", len(observedPersonIDs), len(requesterPersonIDs)*callsPerRequester)
	}
	countByPerson := map[string]int{}
	for _, personID := range observedPersonIDs {
		countByPerson[personID]++
	}
	for _, personID := range requesterPersonIDs {
		if countByPerson[personID] != callsPerRequester {
			t.Fatalf("expected %s to run exactly its own %d calls, got %d; concurrent requesters are being confused for one another", personID, callsPerRequester, countByPerson[personID])
		}
	}
}

func TestARevokedSessionCannotBeUsedByAnotherRequesterMidRun(t *testing.T) {
	workspaceRootPath := t.TempDir()
	actorFactory := &actorRequestRecorder{}
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token-shared" })
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(catalogServer.Close)

	sessionToken, _ := resolver.GrantSessionToken(mcpserver.RequesterToolSet{
		RequesterPersonID: "person-a",
		ToolSet:           requesterToolSetFor(t, "person-a", actorFactory, workspaceRootPath),
	})
	resolver.RevokeSessionToken(sessionToken)

	_, errorValue := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "test"}, nil).Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   catalogServer.URL,
		HTTPClient: &http.Client{Transport: catalogBearer{bearerToken: sessionToken}},
	}, nil)
	if errorValue == nil {
		t.Fatal("expected a finished turn's token to stop working, because a harness that outlives its turn must not keep the requester's tools")
	}
}
