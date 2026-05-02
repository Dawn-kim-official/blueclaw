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
type TaskShape string

const (
	IntakeClassificationQuickReply        IntakeClassification = "quick_reply"
	IntakeClassificationBoundedTask       IntakeClassification = "bounded_task"
	IntakeClassificationNeedsConfirmation IntakeClassification = "needs_confirmation"
	IntakeClassificationUnsupported       IntakeClassification = "unsupported"

	TaskShapeImmediateReply     TaskShape = "immediate_reply"
	TaskShapeResearchTask       TaskShape = "research_task"
	TaskShapeMaintenanceTask    TaskShape = "maintenance_task"
	TaskShapeScheduledTask      TaskShape = "scheduled_task"
	TaskShapeBrowserHandoffTask TaskShape = "browser_handoff_task"
	TaskShapeApprovalGatedTask  TaskShape = "approval_gated_task"
)

type IntakeOptions struct {
	IsEnabled          bool
	DefaultBudgetClass BudgetClass
}

type AgentRequest struct {
	RequesterPersonID    string
	RequesterName        string
	RequesterCallingName string
	RequesterHandle      string
	ProfileName          string
	ConversationID       string
	Prompt               string
	VisibleContext       VisibleContext
	MemoryFacts          []memory.MemoryFact
	ToolRegistry         *ToolRegistry
	InstructionPrompt    string
}

type IntakeDecision struct {
	Classification            IntakeClassification `json:"classification"`
	TaskShape                 TaskShape            `json:"taskShape"`
	BudgetClass               BudgetClass          `json:"budgetClass"`
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
	if NormalizeBudgetClass(string(options.DefaultBudgetClass)) == "" {
		options.DefaultBudgetClass = BudgetClassThirtyMinutes
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
	return taskIntakePlanner.normalizeDecision(intakeDecision, defaultDecision, request)
}

func (taskIntakePlanner TaskIntakePlanner) planWithLanguageModel(ctx context.Context, request AgentRequest) (IntakeDecision, error) {
	structuredResponse, errorValue := taskIntakePlanner.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: taskIntakePlanner.buildMessages(request),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_task_intake_budget",
			Document:           `{"type":"object","properties":{"classification":{"type":"string","enum":["quick_reply","bounded_task","needs_confirmation","unsupported"]},"taskShape":{"type":"string","enum":["immediate_reply","research_task","maintenance_task","scheduled_task","browser_handoff_task","approval_gated_task"]},"budgetClass":{"type":"string","enum":["five_minutes","ten_minutes","thirty_minutes","one_hour","six_hours","half_day"]},"reason":{"type":"string"},"userFacingReply":{"type":"string"}},"required":["classification","taskShape","budgetClass","reason"],"additionalProperties":false}`,
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
	if request.ToolRegistry != nil && len(request.ToolRegistry.ListToolNames()) > 0 {
		toolNames := request.ToolRegistry.ListToolNames()
		toolDescriptions = "Available tools: " + strings.Join(toolNames, ", ")
	}
	return []llm.Message{
		{
			Role:    "system",
			Content: "You are Blueclaw's channel-agnostic task intake planner. Classify whether the current request can be handled in one bounded execution and choose a task shape. Do not use platform-specific assumptions. Use quick_reply for direct answers, bounded_task for one-request tool work, needs_confirmation for large or destructive work, and unsupported for work that cannot be done safely.",
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
	budgetClass := BudgetClassFiveMinutes
	if request.ToolRegistry != nil && len(request.ToolRegistry.ListToolNames()) > 0 && looksLikeToolRequest(prompt) {
		classification = IntakeClassificationBoundedTask
		reason = "request may benefit from bounded tool use"
		budgetClass = taskIntakePlanner.options.DefaultBudgetClass
	}
	if request.VisibleContext.HasMoreBefore {
		classification = IntakeClassificationBoundedTask
		reason = "request has additional retrievable conversation history"
		budgetClass = taskIntakePlanner.options.DefaultBudgetClass
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
		TaskShape:                 deterministicTaskShape(request, classification),
		BudgetClass:               LargerBudgetClass(budgetClass, minimumBudgetClassForRequest(request)),
		Reason:                    reason,
		UserFacingReply:           defaultUserFacingReply(classification),
		UsedDeterministicFallback: true,
	}
}

func (taskIntakePlanner TaskIntakePlanner) normalizeDecision(decision IntakeDecision, defaultDecision IntakeDecision, request AgentRequest) IntakeDecision {
	normalizedClassification := normalizeClassification(decision.Classification)
	if normalizedClassification == "" {
		return defaultDecision
	}
	decision.Classification = normalizedClassification
	normalizedTaskShape := normalizeTaskShape(decision.TaskShape)
	if normalizedTaskShape == "" {
		normalizedTaskShape = deterministicTaskShape(request, decision.Classification)
	}
	decision.TaskShape = normalizedTaskShape
	normalizedBudgetClass := NormalizeBudgetClass(string(decision.BudgetClass))
	if normalizedBudgetClass == "" {
		normalizedBudgetClass = defaultDecision.BudgetClass
	}
	decision.BudgetClass = LargerBudgetClass(normalizedBudgetClass, minimumBudgetClassForRequest(request))
	if strings.TrimSpace(decision.Reason) == "" {
		decision.Reason = defaultDecision.Reason
	}
	if strings.TrimSpace(decision.UserFacingReply) == "" {
		decision.UserFacingReply = defaultUserFacingReply(decision.Classification)
	}
	return decision
}

func deterministicTaskShape(request AgentRequest, classification IntakeClassification) TaskShape {
	if classification == IntakeClassificationQuickReply {
		return TaskShapeImmediateReply
	}
	if classification == IntakeClassificationNeedsConfirmation {
		return TaskShapeApprovalGatedTask
	}
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	if hasToolPrefix(request.ToolRegistry, "browser.") && containsAny(prompt, []string{"browser", "website", "web", "브라우저", "사이트", "페이지"}) {
		return TaskShapeBrowserHandoffTask
	}
	if containsAny(prompt, []string{"schedule", "scheduled", "cron", "매일", "매주", "정기", "예약"}) {
		return TaskShapeScheduledTask
	}
	if containsAny(prompt, []string{"fix", "clean", "setup", "install", "deploy", "고쳐", "정리", "설치", "배포"}) {
		return TaskShapeMaintenanceTask
	}
	if classification == IntakeClassificationBoundedTask {
		return TaskShapeResearchTask
	}
	return TaskShapeImmediateReply
}

func normalizeTaskShape(taskShape TaskShape) TaskShape {
	switch taskShape {
	case TaskShapeImmediateReply, TaskShapeResearchTask, TaskShapeMaintenanceTask, TaskShapeScheduledTask, TaskShapeBrowserHandoffTask, TaskShapeApprovalGatedTask:
		return taskShape
	default:
		return ""
	}
}

func normalizeClassification(classification IntakeClassification) IntakeClassification {
	switch classification {
	case IntakeClassificationQuickReply, IntakeClassificationBoundedTask, IntakeClassificationNeedsConfirmation, IntakeClassificationUnsupported:
		return classification
	default:
		return ""
	}
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
	toolWords := []string{"search", "find", "lookup", "check", "read", "fetch", "compare", "analyze", "summarize", "browser", "screenshot", "click", "fill", "press", "검색", "찾", "확인", "읽", "분석", "요약", "브라우저", "인터넷", "스크린샷", "클릭", "입력"}
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

func minimumBudgetClassForRequest(request AgentRequest) BudgetClass {
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	if hasToolPrefix(request.ToolRegistry, "browser.") {
		if looksLikeBrowserControlSequence(prompt) {
			return BudgetClassThirtyMinutes
		}
		if !hasToolPrefix(request.ToolRegistry, "web.") {
			return BudgetClassTenMinutes
		}
	}
	if hasToolPrefix(request.ToolRegistry, "file.") || hasToolPrefix(request.ToolRegistry, "user.") {
		return BudgetClassTenMinutes
	}
	return BudgetClassFiveMinutes
}

func hasToolPrefix(toolRegistry *ToolRegistry, prefix string) bool {
	if toolRegistry == nil {
		return false
	}
	for _, toolName := range toolRegistry.ListToolNames() {
		if strings.HasPrefix(toolName, prefix) {
			return true
		}
	}
	return false
}

func looksLikeBrowserControlSequence(prompt string) bool {
	words := []string{"screenshot", "click", "fill", "press", "select", "navigate", "스크린샷", "캡처", "클릭", "입력", "눌러", "이동", "서치바"}
	return containsAny(prompt, words)
}

func (intakeDecision IntakeDecision) Validate() error {
	if normalizeClassification(intakeDecision.Classification) == "" {
		return errors.New("intake classification is invalid")
	}
	if NormalizeBudgetClass(string(intakeDecision.BudgetClass)) == "" {
		return errors.New("intake budget class is invalid")
	}
	return nil
}
