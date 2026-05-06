package memory

import (
	"context"
	"encoding/json"
	"strings"

	"blueclaw/internal/llm"
)

type GraphitiIngestionInput struct {
	PersonID                 string
	Prompt                   string
	ConversationID           string
	WorkspaceID              string
	DefaultSecurityLevelRank int
	DefaultRequiredClasses   []string
	IsPrivateConversation    bool
}

type GraphitiIngestionRoute struct {
	ShouldStore bool
	Namespaces  []MemoryNamespace
	Reason      string
	Confidence  float64
}

type GraphitiIngestionRouter struct {
	languageModel llm.LanguageModelProvider
	workspaceID   string
}

type graphitiIngestionRouteDocument struct {
	ShouldStore       bool     `json:"shouldStore"`
	StoreWorkspace    bool     `json:"storeWorkspace"`
	SecurityLevelRank int      `json:"securityLevelRank"`
	RequiredClasses   []string `json:"requiredClasses"`
	Reason            string   `json:"reason"`
	Confidence        float64  `json:"confidence"`
}

func NewGraphitiIngestionRouter(languageModel llm.LanguageModelProvider, workspaceID string) *GraphitiIngestionRouter {
	return &GraphitiIngestionRouter{
		languageModel: languageModel,
		workspaceID:   strings.TrimSpace(workspaceID),
	}
}

func (router *GraphitiIngestionRouter) Route(ctx context.Context, input GraphitiIngestionInput) (GraphitiIngestionRoute, error) {
	input.WorkspaceID = firstNonEmpty(input.WorkspaceID, router.workspaceID, DefaultWorkspaceID)
	if !shouldStoreByHeuristic(input.Prompt) {
		return GraphitiIngestionRoute{ShouldStore: false, Reason: "transient_message", Confidence: 0.8}, nil
	}
	namespaces := baseIngestionNamespaces(input)
	if router == nil || router.languageModel == nil || strings.TrimSpace(input.Prompt) == "" {
		return GraphitiIngestionRoute{ShouldStore: true, Namespaces: namespaces, Reason: "heuristic_durable", Confidence: 0.6}, nil
	}

	routeDocument, errorValue := router.routeWithLanguageModel(ctx, input)
	if errorValue != nil {
		return GraphitiIngestionRoute{}, errorValue
	}
	if !routeDocument.ShouldStore {
		return GraphitiIngestionRoute{
			ShouldStore: false,
			Reason:      firstNonEmpty(routeDocument.Reason, "router_skipped"),
			Confidence:  routeConfidence(routeDocument.Confidence),
		}, nil
	}
	if routeDocument.StoreWorkspace && !input.IsPrivateConversation {
		securityLevelRank := maximumInt(input.DefaultSecurityLevelRank, routeDocument.SecurityLevelRank)
		requiredClasses := unionStrings(input.DefaultRequiredClasses, routeDocument.RequiredClasses)
		namespaces = append(namespaces, WorkspaceNamespace(input.WorkspaceID, securityLevelRank, requiredClasses))
	}
	return GraphitiIngestionRoute{
		ShouldStore: true,
		Namespaces:  namespaces,
		Reason:      firstNonEmpty(routeDocument.Reason, "router_durable"),
		Confidence:  routeConfidence(routeDocument.Confidence),
	}, nil
}

func (router *GraphitiIngestionRouter) routeWithLanguageModel(ctx context.Context, input GraphitiIngestionInput) (graphitiIngestionRouteDocument, error) {
	response, errorValue := router.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: []llm.Message{
			{Role: "system", Content: graphitiIngestionRouterSystemPrompt()},
			{Role: "user", Content: input.Prompt},
		},
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_graphiti_ingestion_route",
			Document:           graphitiIngestionRouterSchema(),
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return graphitiIngestionRouteDocument{}, errorValue
	}

	var routeDocument graphitiIngestionRouteDocument
	errorValue = json.Unmarshal([]byte(response.Content), &routeDocument)
	return routeDocument, errorValue
}

func baseIngestionNamespaces(input GraphitiIngestionInput) []MemoryNamespace {
	if input.IsPrivateConversation {
		return []MemoryNamespace{
			UserNamespace(input.PersonID),
			PrivatePersonNamespace(input.PersonID),
		}
	}
	return []MemoryNamespace{
		UserNamespace(input.PersonID),
		ConversationNamespace(input.ConversationID, input.DefaultSecurityLevelRank, input.DefaultRequiredClasses),
	}
}

func shouldStoreByHeuristic(prompt string) bool {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	if normalizedPrompt == "" {
		return false
	}
	transientMessages := map[string]bool{
		"ok": true, "okay": true, "thanks": true, "thank you": true, "thx": true,
		"네": true, "넵": true, "응": true, "ㅇㅇ": true, "고마워": true, "감사": true, "ㅋㅋ": true,
	}
	if transientMessages[normalizedPrompt] {
		return false
	}
	return true
}

func graphitiIngestionRouterSystemPrompt() string {
	return "Route this message for Blueclaw Graphiti memory ingestion. Do not extract facts. Return shouldStore=true only when the message contains durable user preferences or facts, conversation-useful facts, or shared company/team/project/process knowledge. Return shouldStore=false for acknowledgements, transient task commands, pure control chatter, and low-value small talk. Return storeWorkspace=true only for company, team, policy, project, process, operational, or shared business knowledge. Personal first-person facts stay in the user graph. Conversation-local facts stay in the conversation graph. Never lower the security label."
}

func graphitiIngestionRouterSchema() string {
	return `{"type":"object","properties":{"shouldStore":{"type":"boolean"},"storeWorkspace":{"type":"boolean"},"securityLevelRank":{"type":"integer"},"requiredClasses":{"type":"array","items":{"type":"string"}},"reason":{"type":"string"},"confidence":{"type":"number"}},"required":["shouldStore","storeWorkspace","securityLevelRank","requiredClasses","reason","confidence"],"additionalProperties":false}`
}

func routeConfidence(confidence float64) float64 {
	if confidence <= 0 {
		return 0.5
	}
	if confidence > 1 {
		return 1
	}
	return confidence
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
