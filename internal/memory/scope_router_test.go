package memory

import (
	"context"
	"strings"
	"testing"

	"blueclaw/internal/llm"
)

func TestScopeRouterAlwaysIncludesUserAndConversationNamespaces(t *testing.T) {
	router := NewMemoryScopeRouter(nil, "acme")

	route, errorValue := router.Route(context.Background(), ScopeRouteInput{
		PersonID:                 "person-1",
		ConversationID:           "channel-1",
		Prompt:                   "내 이름은 민수야",
		DefaultSecurityLevelRank: 10,
		DefaultRequiredClasses:   []string{"internal"},
	})
	if errorValue != nil {
		t.Fatalf("expected route to succeed: %v", errorValue)
	}
	if !hasNamespace(route.Namespaces, "user:person-1") {
		t.Fatalf("expected user namespace, got %+v", route.Namespaces)
	}
	if !hasNamespacePrefix(route.Namespaces, "conversation:channel-1:") {
		t.Fatalf("expected conversation namespace, got %+v", route.Namespaces)
	}
}

func TestScopeRouterAddsWorkspaceNamespaceWithoutExtractingFacts(t *testing.T) {
	router := NewMemoryScopeRouter(staticScopeLanguageModel{
		content: `{"storeWorkspace":true,"securityLevelRank":50,"requiredClasses":["finance"]}`,
	}, "acme")

	route, errorValue := router.Route(context.Background(), ScopeRouteInput{
		PersonID:                 "person-1",
		ConversationID:           "channel-1",
		Prompt:                   "우리 회사 법인카드는 재무팀만 써",
		DefaultSecurityLevelRank: 10,
		DefaultRequiredClasses:   []string{"internal"},
	})
	if errorValue != nil {
		t.Fatalf("expected route to succeed: %v", errorValue)
	}
	if !hasNamespacePrefix(route.Namespaces, "workspace:acme:rank:50:") {
		t.Fatalf("expected workspace namespace, got %+v", route.Namespaces)
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

type staticScopeLanguageModel struct {
	content string
}

func (languageModel staticScopeLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticScopeLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name != "blueclaw_memory_scope_route" {
		return llm.StructuredResponse{}, nil
	}
	return llm.StructuredResponse{Content: languageModel.content}, nil
}
