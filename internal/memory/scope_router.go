package memory

import (
	"context"
	"encoding/json"
	"strings"

	"blueclaw/internal/llm"
)

type ScopeRouteInput struct {
	PersonID                 string
	Prompt                   string
	ConversationID           string
	WorkspaceID              string
	DefaultSecurityLevelRank int
	DefaultRequiredClasses   []string
}

type ScopeRoute struct {
	Namespaces []MemoryNamespace
}

type MemoryScopeRouter struct {
	languageModel llm.LanguageModelProvider
	workspaceID   string
}

type scopeRouteDocument struct {
	StoreWorkspace    bool     `json:"storeWorkspace"`
	SecurityLevelRank int      `json:"securityLevelRank"`
	RequiredClasses   []string `json:"requiredClasses"`
}

func NewMemoryScopeRouter(languageModel llm.LanguageModelProvider, workspaceID string) *MemoryScopeRouter {
	return &MemoryScopeRouter{
		languageModel: languageModel,
		workspaceID:   strings.TrimSpace(workspaceID),
	}
}

func (router *MemoryScopeRouter) Route(ctx context.Context, input ScopeRouteInput) (ScopeRoute, error) {
	input.WorkspaceID = firstNonEmpty(input.WorkspaceID, router.workspaceID, DefaultWorkspaceID)
	namespaces := []MemoryNamespace{
		UserNamespace(input.PersonID),
		ConversationNamespace(input.ConversationID, input.DefaultSecurityLevelRank, input.DefaultRequiredClasses),
	}
	if router == nil || router.languageModel == nil || strings.TrimSpace(input.Prompt) == "" {
		return ScopeRoute{Namespaces: namespaces}, nil
	}

	routeDocument, errorValue := router.routeWorkspace(ctx, input)
	if errorValue != nil {
		return ScopeRoute{Namespaces: namespaces}, errorValue
	}
	if routeDocument.StoreWorkspace {
		securityLevelRank := maximumInt(input.DefaultSecurityLevelRank, routeDocument.SecurityLevelRank)
		requiredClasses := unionStrings(input.DefaultRequiredClasses, routeDocument.RequiredClasses)
		namespaces = append(namespaces, WorkspaceNamespace(input.WorkspaceID, securityLevelRank, requiredClasses))
	}
	return ScopeRoute{Namespaces: namespaces}, nil
}

func (router *MemoryScopeRouter) routeWorkspace(ctx context.Context, input ScopeRouteInput) (scopeRouteDocument, error) {
	response, errorValue := router.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: []llm.Message{
			{Role: "system", Content: scopeRouterSystemPrompt()},
			{Role: "user", Content: input.Prompt},
		},
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_memory_scope_route",
			Document:           scopeRouterSchema(),
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return scopeRouteDocument{}, errorValue
	}

	var routeDocument scopeRouteDocument
	errorValue = json.Unmarshal([]byte(response.Content), &routeDocument)
	return routeDocument, errorValue
}

func scopeRouterSystemPrompt() string {
	return "Route this message for Blueclaw memory ingestion. Do not extract facts. Return storeWorkspace=true only for company, team, policy, project, process, operational, or shared business knowledge. Personal first-person facts stay in the user graph. Conversation-local facts stay in the conversation graph. Never lower the security label."
}

func scopeRouterSchema() string {
	return `{"type":"object","properties":{"storeWorkspace":{"type":"boolean"},"securityLevelRank":{"type":"integer"},"requiredClasses":{"type":"array","items":{"type":"string"}}},"required":["storeWorkspace","securityLevelRank","requiredClasses"],"additionalProperties":false}`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func maximumInt(leftValue int, rightValue int) int {
	if leftValue > rightValue {
		return leftValue
	}
	return rightValue
}

func unionStrings(leftValues []string, rightValues []string) []string {
	valueByName := map[string]bool{}
	values := []string{}
	for _, value := range append(append([]string{}, leftValues...), rightValues...) {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" || valueByName[trimmedValue] {
			continue
		}
		valueByName[trimmedValue] = true
		values = append(values, trimmedValue)
	}
	return normalizeClasses(values)
}
