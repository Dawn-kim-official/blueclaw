package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"blueclaw/internal/llm"
	"blueclaw/internal/memory"
)

type IntakeClassification string

const (
	IntakeClassificationQuickReply        IntakeClassification = "quick_reply"
	IntakeClassificationBoundedTask       IntakeClassification = "bounded_task"
	IntakeClassificationNeedsConfirmation IntakeClassification = "needs_confirmation"
	IntakeClassificationUnsupported       IntakeClassification = "unsupported"
)

type IntakeOptions struct {
	IsEnabled               bool
	MaxIterationsPerRequest int
	MaxToolCallsPerRequest  int
	MaxWallClockSecond      int
}

type AgentRequest struct {
	RequesterPersonID string
	ConversationID    string
	Prompt            string
	VisibleContext    VisibleContext
	MemoryFacts       []memory.MemoryFact
	ToolRegistry      *ToolRegistry
}

type IntakeDecision struct {
	Classification            IntakeClassification `json:"classification"`
	MaxIterationsPerRequest   int                  `json:"maxIterationsPerRequest"`
	MaxToolCallsPerRequest    int                  `json:"maxToolCallsPerRequest"`
	MaxWallClockSecond        int                  `json:"maxWallClockSecond"`
	Reason                    string               `json:"reason"`
	UserFacingReply           string               `json:"userFacingReply"`
	UsedDeterministicFallback bool                 `json:"usedDeterministicFallback"`
}

type TaskIntakePlanner struct {
	languageModel llm.LanguageModelProvider
	options       IntakeOptions
}

func NewTaskIntakePlanner(languageModel llm.LanguageModelProvider, options IntakeOptions) TaskIntakePlanner {
	return TaskIntakePlanner{
		languageModel: languageModel,
		options:       normalizeIntakeOptions(options),
	}
}

func normalizeIntakeOptions(options IntakeOptions) IntakeOptions {
	if options.MaxIterationsPerRequest <= 0 {
		options.MaxIterationsPerRequest = 8
	}
	if options.MaxToolCallsPerRequest <= 0 {
		options.MaxToolCallsPerRequest = 8
	}
	if options.MaxWallClockSecond <= 0 {
		options.MaxWallClockSecond = 120
	}
	return options
}

func (taskIntakePlanner TaskIntakePlanner) Plan(ctx context.Context, request AgentRequest) IntakeDecision {
	defaultDecision := taskIntakePlanner.deterministicDecision(request)
	if !taskIntakePlanner.options.IsEnabled || taskIntakePlanner.languageModel == nil {
		return defaultDecision
	}

	intakeDecision, errorValue := taskIntakePlanner.planWithLanguageModel(ctx, request)
	if errorValue != nil {
		return defaultDecision
	}
	return taskIntakePlanner.normalizeDecision(intakeDecision, defaultDecision)
}

func (taskIntakePlanner TaskIntakePlanner) planWithLanguageModel(ctx context.Context, request AgentRequest) (IntakeDecision, error) {
	structuredResponse, errorValue := taskIntakePlanner.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: taskIntakePlanner.buildMessages(request),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_task_intake_budget",
			Document:           `{"type":"object","properties":{"classification":{"type":"string","enum":["quick_reply","bounded_task","needs_confirmation","unsupported"]},"maxIterationsPerRequest":{"type":"integer"},"maxToolCallsPerRequest":{"type":"integer"},"maxWallClockSecond":{"type":"integer"},"reason":{"type":"string"},"userFacingReply":{"type":"string"}},"required":["classification","maxIterationsPerRequest","maxToolCallsPerRequest","maxWallClockSecond","reason"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return IntakeDecision{}, errorValue
	}

	var intakeDecision IntakeDecision
	errorValue = json.Unmarshal([]byte(structuredResponse.Content), &intakeDecision)
	if errorValue != nil {
		return IntakeDecision{}, errorValue
	}
	return intakeDecision, nil
}

func (taskIntakePlanner TaskIntakePlanner) buildMessages(request AgentRequest) []llm.Message {
	toolDescriptions := "No tools are available."
	if request.ToolRegistry != nil && len(request.ToolRegistry.ListToolDefinitions()) > 0 {
		toolNames := []string{}
		for _, toolDefinition := range request.ToolRegistry.ListToolDefinitions() {
			toolNames = append(toolNames, toolDefinition.Name)
		}
		toolDescriptions = "Available tools: " + strings.Join(toolNames, ", ")
	}
	return []llm.Message{
		{
			Role:    "system",
			Content: "You are Blueclaw's channel-agnostic task intake planner. Classify whether the current request can be handled in one bounded execution. Do not use platform-specific assumptions. Use quick_reply for direct answers, bounded_task for one-request tool work, needs_confirmation for large or destructive work, and unsupported for work that cannot be done safely.",
		},
		{
			Role:    "system",
			Content: toolDescriptions,
		},
		{
			Role:    "user",
			Content: request.Prompt,
		},
	}
}

func (taskIntakePlanner TaskIntakePlanner) deterministicDecision(request AgentRequest) IntakeDecision {
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	classification := IntakeClassificationQuickReply
	reason := "short request can be answered directly"
	if request.ToolRegistry != nil && len(request.ToolRegistry.ListToolDefinitions()) > 0 && looksLikeToolRequest(prompt) {
		classification = IntakeClassificationBoundedTask
		reason = "request may benefit from bounded tool use"
	}
	if request.VisibleContext.HasMoreBefore {
		classification = IntakeClassificationBoundedTask
		reason = "request has additional retrievable conversation history"
	}
	if looksLikeLargeRequest(prompt) {
		classification = IntakeClassificationNeedsConfirmation
		reason = "request appears too large for one bounded execution"
	}
	if looksUnsupported(prompt) {
		classification = IntakeClassificationUnsupported
		reason = "request is outside the available execution boundary"
	}
	return IntakeDecision{
		Classification:            classification,
		MaxIterationsPerRequest:   taskIntakePlanner.options.MaxIterationsPerRequest,
		MaxToolCallsPerRequest:    taskIntakePlanner.options.MaxToolCallsPerRequest,
		MaxWallClockSecond:        taskIntakePlanner.options.MaxWallClockSecond,
		Reason:                    reason,
		UserFacingReply:           defaultUserFacingReply(classification),
		UsedDeterministicFallback: true,
	}
}

func (taskIntakePlanner TaskIntakePlanner) normalizeDecision(decision IntakeDecision, defaultDecision IntakeDecision) IntakeDecision {
	normalizedClassification := normalizeClassification(decision.Classification)
	if normalizedClassification == "" {
		return defaultDecision
	}
	decision.Classification = normalizedClassification
	decision.MaxIterationsPerRequest = clampPositiveBudget(decision.MaxIterationsPerRequest, taskIntakePlanner.options.MaxIterationsPerRequest)
	decision.MaxToolCallsPerRequest = clampPositiveBudget(decision.MaxToolCallsPerRequest, taskIntakePlanner.options.MaxToolCallsPerRequest)
	decision.MaxWallClockSecond = clampPositiveBudget(decision.MaxWallClockSecond, taskIntakePlanner.options.MaxWallClockSecond)
	if strings.TrimSpace(decision.Reason) == "" {
		decision.Reason = defaultDecision.Reason
	}
	if strings.TrimSpace(decision.UserFacingReply) == "" {
		decision.UserFacingReply = defaultUserFacingReply(decision.Classification)
	}
	return decision
}

func normalizeClassification(classification IntakeClassification) IntakeClassification {
	switch classification {
	case IntakeClassificationQuickReply, IntakeClassificationBoundedTask, IntakeClassificationNeedsConfirmation, IntakeClassificationUnsupported:
		return classification
	default:
		return ""
	}
}

func clampPositiveBudget(value int, maximum int) int {
	if maximum <= 0 {
		return value
	}
	if value <= 0 {
		return maximum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func defaultUserFacingReply(classification IntakeClassification) string {
	switch classification {
	case IntakeClassificationNeedsConfirmation:
		return "This looks too large for one bounded run. Please confirm a narrower scope or split it into smaller steps."
	case IntakeClassificationUnsupported:
		return "I cannot safely complete that within the current execution boundary. Please narrow the request."
	default:
		return ""
	}
}

func looksLikeToolRequest(prompt string) bool {
	toolWords := []string{"search", "find", "lookup", "check", "read", "fetch", "compare", "analyze", "summarize", "검색", "찾", "확인", "읽", "분석", "요약"}
	return containsAny(prompt, toolWords)
}

func looksLikeLargeRequest(prompt string) bool {
	largeWords := []string{"entire", "all files", "whole repo", "everything", "대부분", "전부", "전체", "모든", "오래", "대량"}
	return containsAny(prompt, largeWords) || len([]rune(prompt)) > 1200
}

func looksUnsupported(prompt string) bool {
	unsupportedWords := []string{"forever", "무기한", "계속 감시", "계속 실행"}
	return containsAny(prompt, unsupportedWords)
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func (intakeDecision IntakeDecision) Validate() error {
	if normalizeClassification(intakeDecision.Classification) == "" {
		return errors.New("intake classification is invalid")
	}
	return nil
}
