package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"blueclaw/internal/llm"
)

func TestGraphitiIngestionRouterStoresDurableUserFact(t *testing.T) {
	router := NewGraphitiIngestionRouter(nil, "acme")

	route, errorValue := router.Route(context.Background(), GraphitiIngestionInput{
		PersonID:                 "person-1",
		ConversationID:           "channel-1",
		Prompt:                   "내 이름은 민수야",
		DefaultSecurityLevelRank: 10,
		DefaultRequiredClasses:   []string{"internal"},
	})
	if errorValue != nil {
		t.Fatalf("expected route to succeed: %v", errorValue)
	}
	if !route.ShouldStore {
		t.Fatalf("expected durable user fact to store, got %+v", route)
	}
	if !hasNamespace(route.Namespaces, "user:person-1") {
		t.Fatalf("expected user namespace, got %+v", route.Namespaces)
	}
	if !hasNamespacePrefix(route.Namespaces, "conversation:channel-1:") {
		t.Fatalf("expected conversation namespace, got %+v", route.Namespaces)
	}
}

func TestGraphitiIngestionRouterAddsWorkspaceNamespaceWithoutExtractingFacts(t *testing.T) {
	router := NewGraphitiIngestionRouter(staticGraphitiRouteLanguageModel{
		content: `{"shouldStore":true,"storeWorkspace":true,"securityLevelRank":50,"requiredClasses":["finance"],"reason":"workspace_policy","confidence":0.9}`,
	}, "acme")

	route, errorValue := router.Route(context.Background(), GraphitiIngestionInput{
		PersonID:                 "person-1",
		ConversationID:           "channel-1",
		Prompt:                   "우리 회사 법인카드는 재무팀만 써",
		DefaultSecurityLevelRank: 10,
		DefaultRequiredClasses:   []string{"internal"},
	})
	if errorValue != nil {
		t.Fatalf("expected route to succeed: %v", errorValue)
	}
	if !route.ShouldStore {
		t.Fatalf("expected workspace fact to store, got %+v", route)
	}
	if !hasNamespacePrefix(route.Namespaces, "workspace:acme:rank:50:") {
		t.Fatalf("expected workspace namespace, got %+v", route.Namespaces)
	}
}

func TestGraphitiIngestionRouterSkipsTransientChatter(t *testing.T) {
	router := NewGraphitiIngestionRouter(staticGraphitiRouteLanguageModel{
		content: `{"shouldStore":true,"storeWorkspace":true,"securityLevelRank":50,"requiredClasses":["finance"],"reason":"ignored","confidence":0.9}`,
	}, "acme")

	route, errorValue := router.Route(context.Background(), GraphitiIngestionInput{
		PersonID:       "person-1",
		ConversationID: "channel-1",
		Prompt:         "고마워",
	})
	if errorValue != nil {
		t.Fatalf("expected route to succeed: %v", errorValue)
	}
	if route.ShouldStore {
		t.Fatalf("expected transient message to skip, got %+v", route)
	}
}

func TestGraphitiIngestionRouterReturnsErrorForFallbackCaller(t *testing.T) {
	router := NewGraphitiIngestionRouter(staticGraphitiRouteLanguageModel{errorValue: errors.New("route failed")}, "acme")

	_, errorValue := router.Route(context.Background(), GraphitiIngestionInput{
		PersonID:       "person-1",
		ConversationID: "channel-1",
		Prompt:         "내 이름은 민수야",
	})
	if errorValue == nil {
		t.Fatal("expected router error")
	}
}

func hasNamespace(namespaces []MemoryNamespace, namespaceID string) bool {
	for _, namespace := range namespaces {
		if namespace.NamespaceID == namespaceID {
			return true
		}
	}
	return false
}

func hasNamespacePrefix(namespaces []MemoryNamespace, prefix string) bool {
	for _, namespace := range namespaces {
		if strings.HasPrefix(namespace.NamespaceID, prefix) {
			return true
		}
	}
	return false
}

type staticGraphitiRouteLanguageModel struct {
	content    string
	errorValue error
}

func (languageModel staticGraphitiRouteLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticGraphitiRouteLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if languageModel.errorValue != nil {
		return llm.StructuredResponse{}, languageModel.errorValue
	}
	if request.StructuredOutputSchema.Name != "blueclaw_graphiti_ingestion_route" {
		return llm.StructuredResponse{}, nil
	}
	return llm.StructuredResponse{Content: languageModel.content}, nil
}
