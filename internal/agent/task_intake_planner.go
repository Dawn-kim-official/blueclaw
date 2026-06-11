package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/memory"
)

type IntakeClassification string
type TaskShape string
type TaskComplexity string
type TurnRoute string
type ApprovalSignal string
type BusyRoute string

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

	TaskComplexitySimple  TaskComplexity = "simple"
	TaskComplexityNormal  TaskComplexity = "normal"
	TaskComplexityComplex TaskComplexity = "complex"

	TurnRouteContinueTask   TurnRoute = "continue_task"
	TurnRouteReviseTask     TurnRoute = "revise_task"
	TurnRouteAnswerQuestion TurnRoute = "answer_question"
	TurnRouteStartTask      TurnRoute = "start_task"
	TurnRouteAnswerMeta     TurnRoute = "answer_meta"
	TurnRouteClarify        TurnRoute = "clarify"
	TurnRouteConsume        TurnRoute = "consume"
	TurnRouteGiveUp         TurnRoute = "give_up"

	BusyRouteStatus    BusyRoute = "status"
	BusyRouteSteer     BusyRoute = "steer"
	BusyRouteReplace   BusyRoute = "replace"
	BusyRouteCancel    BusyRoute = "cancel"
	BusyRouteNewTask   BusyRoute = "new_task"
	BusyRouteUnrelated BusyRoute = "unrelated"

	DefaultReactionEmojiName = "white_check_mark"

	ApprovalSignalApprove ApprovalSignal = "approve"
	ApprovalSignalReject  ApprovalSignal = "reject"
	ApprovalSignalUnclear ApprovalSignal = "unclear"
)

var allowedReactionEmojiNames = []string{
	DefaultReactionEmojiName,
	"thumbsup",
	"eyes",
	"tada",
	"heart",
	"pray",
	"raised_hands",
	"clap",
	"thinking_face",
	"rocket",
	"ok_hand",
	"memo",
	"hourglass_flowing_sand",
	"mag",
	"bulb",
	"sparkles",
	"fire",
	"sob",
	"sweat_smile",
	"wave",
}

type IntakeOptions struct {
	IsEnabled             bool
	DefaultEffortLevel    EffortLevel
	DebugAddressingReason bool
}

type AgentRequest struct {
	RequesterPersonID       string
	RequesterName           string
	RequesterCallingName    string
	RequesterHandle         string
	RequesterCircles        []string
	IsApprovalContinuation  bool
	ExistingTaskRunID       string
	OriginReplyTargetID     string
	OriginIsThread          bool
	ProfileName             string
	ConversationID          string
	Prompt                  string
	InputParts              []AgentPart
	ResponseLanguage        string
	VisibleContext          VisibleContext
	MemoryFacts             []memory.MemoryFact
	ToolSet                 *ToolSet
	PinnedToolNames         []string
	PinnedSkillNames        []string
	WorkspaceRootPath       string
	ActivePaths             []string
	InstructionPrompt       string
	ActiveGoal              ActiveGoal
	ActiveTask              ActiveTaskContext
	PendingConfirmation     PendingConfirmationContext
	PendingChoice           PendingChoiceContext
	PendingInput            PendingInputContext
	AllowGiveUp             bool
	AllowGiveUpReason       string
	PrecomputedTurnDecision *TurnDecision
	TaskComplexity          TaskComplexity
	TurnStartedAt           time.Time
	CheckpointSender        AgentCheckpointSender
}

type PendingConfirmationContext struct {
	TaskRunID string
	Prompt    string
	Question  string
}

type ActiveTaskContext struct {
	TaskRunID string
	Prompt    string
	Status    string
	Summary   string
}

type PendingChoiceContext struct {
	TaskRunID     string
	Question      string
	SelectionMode string
	Options       []ChoiceReplyOption
}

type PendingInputContext struct {
	TaskRunID string
	Question  string
}

type IntakeDecision struct {
	Classification            IntakeClassification  `json:"classification"`
	TaskShape                 TaskShape             `json:"taskShape"`
	TaskComplexity            TaskComplexity        `json:"taskComplexity"`
	EffortLevel               EffortLevel           `json:"effortLevel"`
	RequestedOutputFormats    []string              `json:"requestedOutputFormats"`
	ExpectedResults           []ExpectedResult      `json:"expectedResults,omitempty"`
	ResponseLanguage          string                `json:"responseLanguage"`
	Reason                    string                `json:"reason"`
	UserFacingReply           string                `json:"userFacingReply"`
	ClarificationQuestion     string                `json:"clarificationQuestion,omitempty"`
	ClarificationOptions      []ClarificationOption `json:"clarificationOptions,omitempty"`
	UsedDeterministicFallback bool                  `json:"usedDeterministicFallback"`
}

type ClarificationOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value,omitempty"`
}

type TurnDecision struct {
	Route                     TurnRoute             `json:"route"`
	Classification            IntakeClassification  `json:"classification"`
	TaskShape                 TaskShape             `json:"taskShape"`
	TaskComplexity            TaskComplexity        `json:"taskComplexity"`
	EffortLevel               EffortLevel           `json:"effortLevel"`
	RequestedOutputFormats    []string              `json:"requestedOutputFormats"`
	ExpectedResults           []ExpectedResult      `json:"expectedResults,omitempty"`
	ResponseLanguage          string                `json:"responseLanguage"`
	Reason                    string                `json:"reason"`
	UserFacingReply           string                `json:"userFacingReply"`
	Approval                  *ApprovalSignal       `json:"approval,omitempty"`
	Choices                   []string              `json:"choices,omitempty"`
	ClarificationQuestion     string                `json:"clarificationQuestion,omitempty"`
	ClarificationOptions      []ClarificationOption `json:"clarificationOptions,omitempty"`
	ReactionEmojiName         string                `json:"reactionEmojiName,omitempty"`
	BusyRoute                 BusyRoute             `json:"busyRoute,omitempty"`
	BusyInstruction           string                `json:"busyInstruction,omitempty"`
	UsedDeterministicFallback bool                  `json:"usedDeterministicFallback"`
}

func (turnDecision TurnDecision) IntakeDecision() IntakeDecision {
	return IntakeDecision{
		Classification:            turnDecision.Classification,
		TaskShape:                 turnDecision.TaskShape,
		TaskComplexity:            turnDecision.TaskComplexity,
		EffortLevel:               turnDecision.EffortLevel,
		RequestedOutputFormats:    append([]string{}, turnDecision.RequestedOutputFormats...),
		ExpectedResults:           normalizeExpectedResults(turnDecision.ExpectedResults),
		ResponseLanguage:          turnDecision.ResponseLanguage,
		Reason:                    turnDecision.Reason,
		UserFacingReply:           turnDecision.UserFacingReply,
		ClarificationQuestion:     turnDecision.ClarificationQuestion,
		ClarificationOptions:      append([]ClarificationOption{}, turnDecision.ClarificationOptions...),
		UsedDeterministicFallback: turnDecision.UsedDeterministicFallback,
	}
}

type TaskIntakePlanner struct {
	languageModel llm.LanguageModelProvider
	options       IntakeOptions
}

type TurnRouter struct {
	languageModel llm.LanguageModelProvider
	options       IntakeOptions
}

func NewTaskIntakePlanner(languageModel llm.LanguageModelProvider, options IntakeOptions) TaskIntakePlanner {
	return TaskIntakePlanner{
		languageModel: languageModel,
		options:       normalizeIntakeOptions(options),
	}
}

func NewTurnRouter(languageModel llm.LanguageModelProvider, options IntakeOptions) TurnRouter {
	return TurnRouter{
		languageModel: languageModel,
		options:       normalizeIntakeOptions(options),
	}
}

func normalizeIntakeOptions(options IntakeOptions) IntakeOptions {
	if NormalizeEffortLevel(string(options.DefaultEffortLevel)) == "" {
		options.DefaultEffortLevel = EffortLevelStandard
	}
	return options
}

func (taskIntakePlanner TaskIntakePlanner) Plan(ctx context.Context, request AgentRequest) IntakeDecision {
	return NewTurnRouter(taskIntakePlanner.languageModel, taskIntakePlanner.options).Plan(ctx, request).IntakeDecision()
}

func (turnRouter TurnRouter) Plan(ctx context.Context, request AgentRequest) TurnDecision {
	if request.PrecomputedTurnDecision != nil {
		return turnRouter.normalizeDecision(*request.PrecomputedTurnDecision, turnRouter.deterministicDecision(request), request)
	}
	defaultDecision := turnRouter.deterministicDecision(request)
	if !turnRouter.options.IsEnabled || turnRouter.languageModel == nil {
		return defaultDecision
	}
	turnDecision, errorValue := turnRouter.planWithLanguageModel(ctx, request)
	if errorValue != nil {
		return defaultDecision
	}
	return turnRouter.normalizeDecision(turnDecision, defaultDecision, request)
}

func (turnRouter TurnRouter) planWithLanguageModel(ctx context.Context, request AgentRequest) (TurnDecision, error) {
	structuredResponse, errorValue := turnRouter.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: turnRouter.buildMessages(request),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_turn_router",
			Document:           turnRouterSchema(request),
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return TurnDecision{}, errorValue
	}

	var turnDecision TurnDecision
	errorValue = json.Unmarshal([]byte(structuredResponse.Content), &turnDecision)
	if errorValue != nil {
		return TurnDecision{}, errorValue
	}
	return turnDecision, nil
}

func (turnRouter TurnRouter) buildMessages(request AgentRequest) []llm.Message {
	toolDescriptions := "No tools are available."
	if request.ToolSet != nil && len(request.ToolSet.ListToolNames()) > 0 {
		toolNames := request.ToolSet.ListToolNames()
		toolDescriptions = "Available tools: " + strings.Join(toolNames, ", ")
	}
	messages := []llm.Message{
		{
			Role:    "system",
			Content: "You are Blueclaw's channel-agnostic turn router and task intake planner. Choose the route for the latest user message and classify the task shape. The latest user message is authoritative. Prior conversation may be used only when it helps interpret whether the latest message continues, revises, asks about, cancels, or replaces an active task. Do not carry stale subjects, websites, tools, or artifact formats into a self-contained new request. Use quick_reply for direct answers that may either answer directly or use a small useful tool once, including greetings, capability questions, arithmetic, and short synthetic verification probes that only need an acknowledgement. Use bounded_task for one-request tool work, needs_confirmation for large or destructive work, and unsupported for work that cannot be done safely. Set taskComplexity=simple when a bounded task has a clear short outcome and should normally produce only one final user reply even if it needs tools, such as adding one calendar event, reading one visible attachment, or checking one obvious fact. Set taskComplexity=normal for ordinary bounded work, and taskComplexity=complex for long research, artifact generation, deployment, verification, or work where progress updates are useful. Use clarify when the latest request cannot be routed safely without a user choice; when route is clarify, provide clarificationQuestion and 2-5 clarificationOptions whenever finite choices are natural. Use consume for addressed messages that need no text reply; consume is delivered as an emoji reaction, not a text reply. Prefer consume with reactionEmojiName for lightweight acknowledgement instead of writing an emoji in userFacingReply. When route is consume, set reactionEmojiName to one enum value that matches the message. For non-consume routes, set reactionEmojiName to null or omit it. If schedule.create is available, recurring reminders, periodic reports, finite repeated messages, and future follow-ups are supported as bounded scheduled_task creation; do not reject them as background loops. If site.app.* tools are available, website prototype creation and publishing are supported as bounded tool work unless the request is destructive or asks for paid production infrastructure. Set requestedOutputFormats to null unless the user explicitly asks for deliverable file formats. Use values like html, pptx, pdf, txt, docx, xlsx, or csv when explicit. Treat words like presentation, slides, deck, ppt, 피피티, and 발표자료 as the kind of artifact, not as a .pptx file format unless the user explicitly requests a PowerPoint/PPTX file or asks for all common slide formats. If the user asks for a presentation as HTML, requestedOutputFormats should be [\"html\"], not [\"html\",\"pptx\"]. Set responseLanguage to the language the assistant should use for user-facing replies; use same_as_conversation only when an explicit runtime preference already defines it.",
		},
		{
			Role:    "system",
			Content: responseLanguageInstruction(request.ResponseLanguage),
		},
		{
			Role:    "system",
			Content: buildTemporalContextDescription(request.TurnStartedAt),
		},
		{
			Role:    "system",
			Content: toolDescriptions,
		},
	}
	if contextDescription := buildVisibleContextDescription(request.VisibleContext); contextDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: contextDescription})
	}
	if goalDescription := activeGoalDescription(request.ActiveGoal); goalDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: goalDescription})
	}
	if routingContext := turnRoutingContextDescription(request); routingContext != "" {
		messages = append(messages, llm.Message{Role: "system", Content: routingContext})
	}
	messages = append(messages, llm.Message{Role: "user", Content: request.Prompt})
	return messages
}

func (turnRouter TurnRouter) deterministicDecision(request AgentRequest) TurnDecision {
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	classification := IntakeClassificationQuickReply
	reason := "short request can be answered directly"
	effortLevel := EffortLevelQuick
	if request.ToolSet != nil && len(request.ToolSet.ListToolNames()) > 0 && looksLikeToolRequest(prompt) {
		classification = IntakeClassificationBoundedTask
		reason = "request may benefit from bounded tool use"
		effortLevel = turnRouter.options.DefaultEffortLevel
	}
	if requestRequiresFollowUpToolWork(request) {
		classification = IntakeClassificationBoundedTask
		reason = "request resumes previous visible tool work"
		effortLevel = turnRouter.options.DefaultEffortLevel
	}
	if request.VisibleContext.HasMoreBefore {
		classification = IntakeClassificationBoundedTask
		reason = "request has additional retrievable conversation history"
		effortLevel = turnRouter.options.DefaultEffortLevel
	}
	if looksLikeLargeRequest(prompt) {
		classification = IntakeClassificationNeedsConfirmation
		reason = "request appears too large for one bounded execution"
	}
	if looksUnsupported(prompt) {
		classification = IntakeClassificationUnsupported
		reason = "request is outside the available execution boundary"
	}
	responseLanguage := ResolveResponseLanguage(request.ResponseLanguage, request.VisibleContext.ResponseLanguage)
	return TurnDecision{
		Route:                     deterministicTurnRoute(request),
		Classification:            classification,
		TaskShape:                 deterministicTaskShape(request, classification),
		TaskComplexity:            TaskComplexityNormal,
		EffortLevel:               LargerEffortLevel(effortLevel, minimumEffortLevelForRequest(request)),
		Reason:                    reason,
		ResponseLanguage:          responseLanguage,
		UserFacingReply:           defaultUserFacingReplyForLanguage(classification, responseLanguage),
		UsedDeterministicFallback: true,
	}
}

func (turnRouter TurnRouter) normalizeDecision(decision TurnDecision, defaultDecision TurnDecision, request AgentRequest) TurnDecision {
	decision.Route = normalizeTurnRoute(decision.Route, request)
	if decision.Route == "" {
		decision.Route = defaultDecision.Route
	}
	hasPendingConfirmation := strings.TrimSpace(request.PendingConfirmation.TaskRunID) != ""
	decision.Approval = normalizeApprovalSignal(decision.Approval, hasPendingConfirmation)
	if hasPendingConfirmation && decision.Approval != nil && *decision.Approval == ApprovalSignalApprove {
		decision.Route = TurnRouteContinueTask
	}
	decision.Choices = normalizeChoiceSelections(decision.Choices, request.PendingChoice)
	decision.BusyRoute = normalizeBusyRoute(decision.BusyRoute, decision.Route, request)
	decision.BusyInstruction = strings.TrimSpace(decision.BusyInstruction)
	decision.ClarificationQuestion = strings.TrimSpace(decision.ClarificationQuestion)
	decision.ClarificationOptions = normalizeClarificationOptions(decision.ClarificationOptions)
	decision.ReactionEmojiName = NormalizeReactionEmojiName(decision.ReactionEmojiName)
	decision = applyRouteToIntakeDecision(decision)
	normalizedClassification := normalizeClassification(decision.Classification)
	if normalizedClassification == "" {
		return defaultDecision
	}
	decision.Classification = normalizedClassification
	if looksLikeSyntheticConnectorVerificationProbe(request.Prompt) {
		decision.Classification = IntakeClassificationQuickReply
		decision.TaskShape = TaskShapeImmediateReply
		decision.EffortLevel = EffortLevelQuick
		decision.RequestedOutputFormats = nil
		decision.Reason = firstNonEmptyString(decision.Reason, "synthetic connector verification probe")
		decision.UserFacingReply = ""
	}
	if requestRequiresFollowUpToolWork(request) && decision.Classification == IntakeClassificationQuickReply {
		decision.Classification = IntakeClassificationBoundedTask
		decision.Reason = firstNonEmptyString(decision.Reason, "request resumes previous visible tool work")
		decision.UserFacingReply = ""
	}
	if requestRequiresAttachmentFollowUpToolWork(request) && decision.Classification == IntakeClassificationQuickReply {
		decision.Classification = IntakeClassificationBoundedTask
		decision.Reason = firstNonEmptyString(decision.Reason, "request resumes previous visible attachment work")
		decision.UserFacingReply = ""
	}
	if shouldTreatConfirmationAsBoundedLocalArtifact(request, decision.IntakeDecision()) {
		decision.Classification = IntakeClassificationBoundedTask
		decision.Reason = firstNonEmptyString(decision.Reason, "local workspace artifact generation can run as bounded tool work")
		decision.UserFacingReply = ""
	}
	if shouldTreatAsBoundedSitePrototype(request, decision.IntakeDecision()) {
		decision.Classification = IntakeClassificationBoundedTask
		decision.Reason = "available site.app tools can create and publish the requested prototype"
		decision.UserFacingReply = ""
	}
	normalizedTaskShape := normalizeTaskShape(decision.TaskShape)
	if normalizedTaskShape == "" {
		normalizedTaskShape = deterministicTaskShape(request, decision.Classification)
	}
	if requestRequiresFollowUpToolWork(request) {
		normalizedTaskShape = TaskShapeBrowserHandoffTask
	}
	if shouldPreferAttachmentContinuationOverBrowser(request, normalizedTaskShape) {
		normalizedTaskShape = deterministicTaskShapeForAttachmentContinuation(decision.Classification)
	}
	if decision.Classification == IntakeClassificationBoundedTask && normalizedTaskShape == TaskShapeApprovalGatedTask {
		normalizedTaskShape = deterministicTaskShape(request, decision.Classification)
	}
	decision.TaskShape = normalizedTaskShape
	decision.TaskComplexity = normalizeTaskComplexity(decision.TaskComplexity)
	if decision.TaskComplexity == "" {
		decision.TaskComplexity = defaultDecision.TaskComplexity
	}
	normalizedEffortLevel := NormalizeEffortLevel(string(decision.EffortLevel))
	if normalizedEffortLevel == "" {
		normalizedEffortLevel = defaultDecision.EffortLevel
	}
	decision.EffortLevel = LargerEffortLevel(normalizedEffortLevel, minimumEffortLevelForRequest(request))
	decision.RequestedOutputFormats = normalizeRequestedOutputFormats(decision.RequestedOutputFormats)
	decision.ExpectedResults = normalizeExpectedResults(decision.ExpectedResults)
	decision.ResponseLanguage = resolveDecisionResponseLanguage(decision.ResponseLanguage, request.ResponseLanguage)
	if strings.TrimSpace(decision.Reason) == "" {
		decision.Reason = defaultDecision.Reason
	}
	if strings.TrimSpace(decision.UserFacingReply) == "" {
		decision.UserFacingReply = defaultUserFacingReplyForLanguage(decision.Classification, decision.ResponseLanguage)
	}
	return decision
}

func turnRouterSchema(request AgentRequest) string {
	routeValues := []string{
		string(TurnRouteContinueTask),
		string(TurnRouteReviseTask),
		string(TurnRouteAnswerQuestion),
		string(TurnRouteStartTask),
		string(TurnRouteAnswerMeta),
		string(TurnRouteClarify),
		string(TurnRouteConsume),
	}
	if request.AllowGiveUp {
		routeValues = append(routeValues, string(TurnRouteGiveUp))
	}
	properties := map[string]any{
		"route": map[string]any{"type": "string", "enum": routeValues},
		"classification": map[string]any{"type": "string", "enum": []string{
			string(IntakeClassificationQuickReply),
			string(IntakeClassificationBoundedTask),
			string(IntakeClassificationNeedsConfirmation),
			string(IntakeClassificationUnsupported),
		}},
		"taskShape": map[string]any{"type": "string", "enum": []string{
			string(TaskShapeImmediateReply),
			string(TaskShapeResearchTask),
			string(TaskShapeMaintenanceTask),
			string(TaskShapeScheduledTask),
			string(TaskShapeBrowserHandoffTask),
			string(TaskShapeApprovalGatedTask),
		}},
		"taskComplexity": map[string]any{"type": "string", "enum": []string{
			string(TaskComplexitySimple),
			string(TaskComplexityNormal),
			string(TaskComplexityComplex),
		}},
		"effortLevel": map[string]any{"type": "string", "enum": []string{
			string(EffortLevelQuick),
			string(EffortLevelStandard),
			string(EffortLevelDeep),
			string(EffortLevelExtended),
		}},
		"requestedOutputFormats": map[string]any{"anyOf": []any{
			map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"html", "pptx", "pdf", "txt", "docx", "xlsx", "csv"}}},
			map[string]any{"type": "null"},
		}},
		"expectedResults":  expectedResultsSchema(),
		"responseLanguage": map[string]any{"type": "string", "enum": []string{"ko", "en", "same_as_conversation"}},
		"reason":           map[string]any{"type": "string"},
		"userFacingReply":  map[string]any{"type": "string"},
		"clarificationQuestion": map[string]any{
			"type": "string",
		},
		"clarificationOptions": clarificationOptionsSchema(),
		"reactionEmojiName": map[string]any{"anyOf": []any{
			map[string]any{"type": "string", "enum": allowedReactionEmojiNames},
			map[string]any{"type": "null"},
		}},
	}
	requiredProperties := []string{"route", "classification", "taskShape", "taskComplexity", "effortLevel", "requestedOutputFormats", "responseLanguage", "reason", "userFacingReply"}
	if strings.TrimSpace(request.PendingConfirmation.TaskRunID) != "" {
		properties["approval"] = map[string]any{"type": "string", "enum": []string{string(ApprovalSignalApprove), string(ApprovalSignalReject), string(ApprovalSignalUnclear)}}
		requiredProperties = append(requiredProperties, "approval")
	}
	if strings.TrimSpace(request.PendingChoice.TaskRunID) != "" {
		properties["choices"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": pendingChoiceKeys(request.PendingChoice)}, "uniqueItems": true}
		requiredProperties = append(requiredProperties, "choices")
	}
	if strings.TrimSpace(request.ActiveTask.TaskRunID) != "" {
		properties["busyRoute"] = map[string]any{"type": "string", "enum": []string{
			string(BusyRouteStatus),
			string(BusyRouteSteer),
			string(BusyRouteReplace),
			string(BusyRouteCancel),
			string(BusyRouteNewTask),
			string(BusyRouteUnrelated),
		}}
		properties["busyInstruction"] = map[string]any{"type": "string"}
		requiredProperties = append(requiredProperties, "busyRoute", "busyInstruction")
	}
	document, errorValue := json.Marshal(map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             requiredProperties,
		"additionalProperties": false,
	})
	if errorValue != nil {
		return `{"type":"object","properties":{"route":{"type":"string"},"classification":{"type":"string"},"taskShape":{"type":"string"},"taskComplexity":{"type":"string"},"effortLevel":{"type":"string"},"requestedOutputFormats":{"type":"null"},"responseLanguage":{"type":"string"},"reason":{"type":"string"},"userFacingReply":{"type":"string"}},"required":["route","classification","taskShape","taskComplexity","effortLevel","requestedOutputFormats","responseLanguage","reason","userFacingReply"],"additionalProperties":false}`
	}
	return string(document)
}

func expectedResultsSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":              map[string]any{"type": "string"},
				"type":            map[string]any{"type": "string", "enum": []string{ExpectedResultTypeMessage, ExpectedResultTypeFile, ExpectedResultTypeLink}},
				"description":     map[string]any{"type": "string"},
				"required":        map[string]any{"type": "boolean"},
				"acceptanceHints": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required":             []string{"description", "required"},
			"additionalProperties": false,
		},
	}
}

func normalizeBusyRoute(busyRoute BusyRoute, turnRoute TurnRoute, request AgentRequest) BusyRoute {
	if strings.TrimSpace(request.ActiveTask.TaskRunID) == "" {
		return ""
	}
	switch busyRoute {
	case BusyRouteStatus, BusyRouteSteer, BusyRouteReplace, BusyRouteCancel, BusyRouteNewTask, BusyRouteUnrelated:
		return busyRoute
	}
	switch turnRoute {
	case TurnRouteAnswerQuestion, TurnRouteAnswerMeta:
		return BusyRouteStatus
	case TurnRouteContinueTask, TurnRouteReviseTask:
		return BusyRouteSteer
	case TurnRouteStartTask:
		return BusyRouteNewTask
	case TurnRouteConsume, TurnRouteGiveUp:
		return BusyRouteUnrelated
	default:
		return BusyRouteNewTask
	}
}

func pendingChoiceKeys(pendingChoice PendingChoiceContext) []string {
	keys := []string{}
	seenKeys := map[string]bool{}
	for index, option := range pendingChoice.Options {
		key := strings.TrimSpace(option.Key)
		if key != "" && !seenKeys[key] {
			keys = append(keys, key)
			seenKeys[key] = true
		}
		indexKey := strconv.Itoa(index + 1)
		if !seenKeys[indexKey] {
			keys = append(keys, indexKey)
			seenKeys[indexKey] = true
		}
	}
	return keys
}

func clarificationOptionsSchema() map[string]any {
	return map[string]any{
		"type":     "array",
		"minItems": 0,
		"maxItems": 5,
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key":   map[string]any{"type": "string"},
				"label": map[string]any{"type": "string"},
				"value": map[string]any{"type": "string"},
			},
			"required":             []string{"key", "label"},
			"additionalProperties": false,
		},
	}
}

func turnRoutingContextDescription(request AgentRequest) string {
	lines := []string{}
	if strings.TrimSpace(request.PendingConfirmation.TaskRunID) != "" {
		lines = append(lines,
			"Pending confirmation:",
			"- Task: "+strings.TrimSpace(request.PendingConfirmation.Prompt),
			"- Question: "+strings.TrimSpace(request.PendingConfirmation.Question),
			"- Return approval=approve only when the latest user message clearly authorizes this exact pending action.",
			"- Use answer_question only when the latest user message asks about this pending confirmation.",
			"- If the latest user message changes the target, scope, conditions, or asks for a different action, use revise_task or start_task with approval=unclear.",
		)
	}
	if strings.TrimSpace(request.PendingChoice.TaskRunID) != "" {
		optionLines := []string{}
		for index, option := range request.PendingChoice.Options {
			optionLines = append(optionLines, strconv.Itoa(index+1)+". "+strings.TrimSpace(option.Label)+" / key "+strings.TrimSpace(option.Key))
		}
		lines = append(lines,
			"Pending choice:",
			"- Question: "+strings.TrimSpace(request.PendingChoice.Question),
			"- Selection mode: "+strings.TrimSpace(request.PendingChoice.SelectionMode),
			"- Options: "+strings.Join(optionLines, "; "),
			"- Return choices as option keys only. Return an empty array when the latest message does not select valid options.",
			"- If the latest user message changes the task instead of selecting an option, use revise_task or start_task with an empty choices array.",
		)
	}
	if strings.TrimSpace(request.PendingInput.TaskRunID) != "" {
		lines = append(lines,
			"Pending input:",
			"- Question: "+strings.TrimSpace(request.PendingInput.Question),
			"- Use continue_task or revise_task when the latest message answers or modifies this pending input.",
		)
	}
	if strings.TrimSpace(request.ActiveTask.TaskRunID) != "" {
		lines = append(lines,
			"Active task in this conversation:",
			"- Task run ID: "+strings.TrimSpace(request.ActiveTask.TaskRunID),
			"- Status: "+strings.TrimSpace(request.ActiveTask.Status),
			"- Original instruction: "+strings.TrimSpace(request.ActiveTask.Prompt),
			"- Current progress summary: "+strings.TrimSpace(request.ActiveTask.Summary),
			"- Choose busyRoute=status when the latest message asks whether work is happening or asks for progress.",
			"- Choose busyRoute=steer when the latest message corrects or redirects the active task without explicitly cancelling it.",
			"- Choose busyRoute=replace only when the latest message clearly cancels or replaces the active task with a new instruction.",
			"- Choose busyRoute=cancel when the latest message asks to stop, cancel, abort, or not continue the active task.",
			"- Choose busyRoute=new_task when the latest message is independent and should not affect the active task.",
			"- Choose busyRoute=unrelated when the message should not start or alter work.",
			"- Natural-language stop requests are normal messages; classify them by intent instead of requiring slash commands.",
		)
	}
	if request.AllowGiveUp {
		lines = append(lines, "Give-up route is allowed because: "+strings.TrimSpace(request.AllowGiveUpReason))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func resolveDecisionResponseLanguage(decisionLanguage string, requestLanguage string) string {
	normalizedDecisionLanguage := NormalizeResponseLanguage(decisionLanguage)
	if normalizedDecisionLanguage == ResponseLanguageSameAsConversation {
		return ResolveResponseLanguage(requestLanguage)
	}
	return ResolveResponseLanguage(normalizedDecisionLanguage, requestLanguage)
}

func normalizeRequestedOutputFormats(formats []string) []string {
	normalizedFormats := []string{}
	seenFormat := map[string]bool{}
	for _, format := range formats {
		normalizedFormat := strings.ToLower(strings.TrimSpace(format))
		switch normalizedFormat {
		case "html", "pptx", "pdf", "txt", "docx", "xlsx", "csv":
		default:
			continue
		}
		if seenFormat[normalizedFormat] {
			continue
		}
		seenFormat[normalizedFormat] = true
		normalizedFormats = append(normalizedFormats, normalizedFormat)
	}
	return normalizedFormats
}

func deterministicTaskShape(request AgentRequest, classification IntakeClassification) TaskShape {
	if classification == IntakeClassificationQuickReply {
		return TaskShapeImmediateReply
	}
	if classification == IntakeClassificationNeedsConfirmation {
		return TaskShapeApprovalGatedTask
	}
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	if requestRequiresFollowUpToolWork(request) {
		return TaskShapeBrowserHandoffTask
	}
	if hasToolPrefix(request.ToolSet, "browser.") && containsAny(prompt, []string{"browser", "website", "web", "브라우저", "사이트", "페이지"}) {
		return TaskShapeBrowserHandoffTask
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

func normalizeTaskComplexity(taskComplexity TaskComplexity) TaskComplexity {
	switch taskComplexity {
	case TaskComplexitySimple, TaskComplexityNormal, TaskComplexityComplex:
		return taskComplexity
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

func normalizeTurnRoute(route TurnRoute, request AgentRequest) TurnRoute {
	switch route {
	case TurnRouteContinueTask, TurnRouteReviseTask, TurnRouteAnswerQuestion, TurnRouteStartTask, TurnRouteAnswerMeta, TurnRouteClarify, TurnRouteConsume:
		return route
	case TurnRouteGiveUp:
		if request.AllowGiveUp {
			return route
		}
		if hasPendingOrActiveTaskContext(request) {
			return TurnRouteAnswerMeta
		}
		return TurnRouteClarify
	default:
		return ""
	}
}

func deterministicTurnRoute(request AgentRequest) TurnRoute {
	if strings.TrimSpace(request.PendingConfirmation.TaskRunID) != "" {
		return TurnRouteContinueTask
	}
	if strings.TrimSpace(request.PendingChoice.TaskRunID) != "" {
		return TurnRouteContinueTask
	}
	if strings.TrimSpace(request.PendingInput.TaskRunID) != "" {
		return TurnRouteContinueTask
	}
	if strings.TrimSpace(request.ActiveGoal.TaskRunID) != "" {
		if turnRouterPromptLooksIndependent(request.Prompt) && !turnRouterPromptLooksLikeGoalContinuation(request.Prompt) {
			return TurnRouteStartTask
		}
		return TurnRouteContinueTask
	}
	if turnRouterLooksLikeBareConfirmationReply(request.Prompt) {
		return TurnRouteConsume
	}
	return TurnRouteStartTask
}

func applyRouteToIntakeDecision(decision TurnDecision) TurnDecision {
	switch decision.Route {
	case TurnRouteClarify:
		decision.Classification = IntakeClassificationNeedsConfirmation
		decision.TaskShape = TaskShapeApprovalGatedTask
		if decision.UserFacingReply == "" {
			decision.UserFacingReply = decision.ClarificationQuestion
		}
		if decision.UserFacingReply == "" {
			decision.UserFacingReply = decision.Reason
		}
	case TurnRouteGiveUp:
		decision.Classification = IntakeClassificationUnsupported
		if decision.UserFacingReply == "" {
			decision.UserFacingReply = decision.Reason
		}
	}
	return decision
}

func hasPendingOrActiveTaskContext(request AgentRequest) bool {
	return strings.TrimSpace(request.PendingConfirmation.TaskRunID) != "" ||
		strings.TrimSpace(request.PendingChoice.TaskRunID) != "" ||
		strings.TrimSpace(request.PendingInput.TaskRunID) != "" ||
		strings.TrimSpace(request.ActiveGoal.TaskRunID) != "" ||
		strings.TrimSpace(request.ActiveTask.TaskRunID) != ""
}

func normalizeApprovalSignal(signal *ApprovalSignal, hasPendingConfirmation bool) *ApprovalSignal {
	if !hasPendingConfirmation {
		return nil
	}
	if signal == nil {
		unclear := ApprovalSignalUnclear
		return &unclear
	}
	normalizedSignal := ApprovalSignal(strings.TrimSpace(string(*signal)))
	switch normalizedSignal {
	case ApprovalSignalApprove, ApprovalSignalReject, ApprovalSignalUnclear:
		return &normalizedSignal
	default:
		unclear := ApprovalSignalUnclear
		return &unclear
	}
}

func normalizeChoiceSelections(selections []string, pendingChoice PendingChoiceContext) []string {
	if strings.TrimSpace(pendingChoice.TaskRunID) == "" {
		return nil
	}
	validChoices := map[string]bool{}
	choiceByIndex := map[string]string{}
	for index, option := range pendingChoice.Options {
		key := strings.TrimSpace(option.Key)
		if key != "" {
			validChoices[key] = true
			choiceByIndex[strconv.Itoa(index+1)] = key
		}
	}
	normalizedChoices := []string{}
	seenChoices := map[string]bool{}
	for _, selection := range selections {
		normalizedSelection := strings.TrimSpace(selection)
		if indexedSelection, isFound := choiceByIndex[normalizedSelection]; isFound {
			normalizedSelection = indexedSelection
		}
		if !validChoices[normalizedSelection] || seenChoices[normalizedSelection] {
			continue
		}
		seenChoices[normalizedSelection] = true
		normalizedChoices = append(normalizedChoices, normalizedSelection)
	}
	if strings.TrimSpace(pendingChoice.SelectionMode) != "multiple" && len(normalizedChoices) > 1 {
		return nil
	}
	return normalizedChoices
}

func normalizeClarificationOptions(options []ClarificationOption) []ClarificationOption {
	normalizedOptions := []ClarificationOption{}
	seenKeys := map[string]bool{}
	for index, option := range options {
		label := strings.TrimSpace(option.Label)
		if label == "" {
			continue
		}
		key := strings.TrimSpace(option.Key)
		if key == "" {
			key = clarificationOptionKey(index)
		}
		if seenKeys[key] {
			continue
		}
		seenKeys[key] = true
		value := strings.TrimSpace(option.Value)
		if value == "" {
			value = label
		}
		normalizedOptions = append(normalizedOptions, ClarificationOption{Key: key, Label: label, Value: value})
		if len(normalizedOptions) >= 5 {
			break
		}
	}
	if len(normalizedOptions) < 2 {
		return nil
	}
	return normalizedOptions
}

func clarificationOptionKey(index int) string {
	if index >= 0 && index < 26 {
		return string(rune('A' + index))
	}
	return "O"
}

func NormalizeReactionEmojiName(emojiName string) string {
	normalizedEmojiName := strings.Trim(strings.TrimSpace(emojiName), ":")
	if normalizedEmojiName == "" {
		return DefaultReactionEmojiName
	}
	normalizedEmojiName = strings.ToLower(normalizedEmojiName)
	for _, allowedEmojiName := range allowedReactionEmojiNames {
		if normalizedEmojiName == allowedEmojiName {
			return normalizedEmojiName
		}
	}
	return DefaultReactionEmojiName
}

func turnRouterLooksLikeBareConfirmationReply(prompt string) bool {
	normalizedPrompt := strings.TrimSpace(strings.ToLower(prompt))
	confirmationReplies := map[string]bool{
		"ㅇ": true, "응": true, "네": true, "예": true, "그래": true, "좋아": true,
		"진행해": true, "진행해줘": true, "해": true, "해줘": true,
		"approved": true, "rejected": true, "yes": true, "y": true, "no": true, "n": true,
		"ok": true, "okay": true, "go ahead": true,
	}
	return confirmationReplies[normalizedPrompt]
}

func turnRouterPromptLooksLikeGoalContinuation(prompt string) bool {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	return containsAny(normalizedPrompt, []string{
		"우선", "계속", "진행", "그대로", "좋아", "해봐", "다시 해", "다시 진행", "그럼",
		"continue", "go ahead", "proceed", "retry",
	})
}

func turnRouterPromptLooksIndependent(prompt string) bool {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	return containsAny(normalizedPrompt, []string{
		"캘린더", "일정", "회의", "휴가", "알림", "예약", "dm", "메일", "보내", "전송",
		"검색", "찾아", "조사", "작성", "만들", "수정", "삭제", "배포", "열어",
		"calendar", "meeting", "remind", "schedule", "send", "email", "search", "write", "create", "delete", "deploy",
	})
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
	toolWords := []string{"search", "find", "lookup", "check", "read", "fetch", "compare", "analyze", "summarize", "browser", "screenshot", "click", "fill", "press", "create", "write", "attach", "run", "검색", "찾", "확인", "읽", "분석", "요약", "브라우저", "인터넷", "스크린샷", "클릭", "입력", "만들", "작성", "첨부", "실행"}
	return containsAny(prompt, toolWords)
}

func requestRequiresFollowUpToolWork(request AgentRequest) bool {
	if !hasToolPrefix(request.ToolSet, "browser.") {
		return false
	}
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	if !looksLikeBrowserFollowUp(prompt) || !visibleContextMentionsBrowserWork(request.VisibleContext) {
		return false
	}
	if visibleContextHasAttachmentAnchor(request.VisibleContext) && !promptExplicitlyMentionsBrowser(prompt) {
		return false
	}
	return true
}

func requestRequiresAttachmentFollowUpToolWork(request AgentRequest) bool {
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	return looksLikeBrowserFollowUp(prompt) && visibleContextHasAttachmentAnchor(request.VisibleContext) && !promptExplicitlyMentionsBrowser(prompt)
}

func shouldPreferAttachmentContinuationOverBrowser(request AgentRequest, taskShape TaskShape) bool {
	if taskShape != TaskShapeBrowserHandoffTask {
		return false
	}
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	if !looksLikeBrowserFollowUp(prompt) {
		return false
	}
	return visibleContextHasAttachmentAnchor(request.VisibleContext) && !promptExplicitlyMentionsBrowser(prompt)
}

func deterministicTaskShapeForAttachmentContinuation(classification IntakeClassification) TaskShape {
	if classification == IntakeClassificationQuickReply {
		return TaskShapeImmediateReply
	}
	if classification == IntakeClassificationNeedsConfirmation {
		return TaskShapeApprovalGatedTask
	}
	return TaskShapeResearchTask
}

func looksLikeLargeRequest(prompt string) bool {
	largeWords := []string{"entire", "all files", "whole repo", "everything", "대부분", "전부", "전체", "모든", "오래", "대량"}
	return containsAny(prompt, largeWords) || len([]rune(prompt)) > 1200
}

func looksUnsupported(prompt string) bool {
	unsupportedWords := []string{"forever", "무기한", "계속 감시", "계속 실행"}
	return containsAny(prompt, unsupportedWords)
}

func shouldTreatConfirmationAsBoundedLocalArtifact(request AgentRequest, decision IntakeDecision) bool {
	if decision.Classification != IntakeClassificationNeedsConfirmation {
		return false
	}
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	if looksLikeDestructiveLocalWork(prompt) {
		return false
	}
	if !hasAllTools(request.ToolSet, []string{"terminal.run", "file.write", "file.promote", "file.attach"}) {
		return false
	}
	artifactWords := []string{"slide", "slides", "deck", "presentation", "ppt", "pptx", "pdf", "html", "artifact", "attach", "피피티", "파워포인트", "발표자료", "슬라이드", "자료", "첨부", "보내"}
	return containsAny(prompt, artifactWords) && containsAny(prompt, []string{"create", "make", "write", "generate", "export", "만들", "작성", "생성", "줘", "보내"})
}

func shouldTreatAsBoundedSitePrototype(request AgentRequest, decision IntakeDecision) bool {
	if decision.Classification != IntakeClassificationUnsupported && decision.Classification != IntakeClassificationNeedsConfirmation {
		return false
	}
	if !hasAllTools(request.ToolSet, []string{"site.app.create", "site.app.publish"}) {
		return false
	}
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	if looksLikeDestructiveLocalWork(prompt) || looksLikeLargeRequest(prompt) {
		return false
	}
	return containsAny(prompt, []string{"website", "web app", "site", "landing page", "prototype", "demo", "웹사이트", "사이트", "랜딩", "프로토타입", "데모"}) &&
		containsAny(prompt, []string{"create", "make", "build", "publish", "deploy", "만들", "생성", "배포", "올려"})
}

func looksLikeDestructiveLocalWork(prompt string) bool {
	destructiveWords := []string{"delete", "remove", "drop", "wipe", "format", "rm -rf", "destroy", "삭제", "제거", "초기화", "포맷"}
	return containsAny(prompt, destructiveWords)
}

func hasAllTools(toolRegistry *ToolSet, toolNames []string) bool {
	if toolRegistry == nil {
		return false
	}
	availableToolNames := map[string]bool{}
	for _, toolName := range toolRegistry.ListToolNames() {
		availableToolNames[toolName] = true
	}
	for _, toolName := range toolNames {
		if !availableToolNames[toolName] {
			return false
		}
	}
	return true
}

func hasTool(toolRegistry *ToolSet, toolName string) bool {
	if toolRegistry == nil {
		return false
	}
	for _, availableToolName := range toolRegistry.ListToolNames() {
		if availableToolName == toolName {
			return true
		}
	}
	return false
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func minimumEffortLevelForRequest(request AgentRequest) EffortLevel {
	prompt := strings.ToLower(strings.TrimSpace(request.Prompt))
	if looksLikeSyntheticConnectorVerificationProbe(prompt) {
		return EffortLevelQuick
	}
	if containsAny(prompt, []string{"migration", "backup", "restore", "delegated", "delegate", "scheduled workflow", "long-running", "마이그레이션", "백업", "복구", "위임", "장기", "예약 실행"}) {
		return EffortLevelExtended
	}
	if containsAny(prompt, []string{"thorough", "deep", "exhaustive", "debug", "fix", "code edit", "multi-file", "verification", "verify", "browser handoff", "maintenance", "꼼꼼히", "깊게", "전체적으로", "디버그", "고쳐", "수정", "검증", "브라우저 핸드오프", "유지보수"}) {
		return EffortLevelDeep
	}
	if hasToolPrefix(request.ToolSet, "browser.") {
		if looksLikeBrowserControlSequence(prompt) {
			return EffortLevelDeep
		}
		if !hasToolPrefix(request.ToolSet, "web.") {
			return EffortLevelDeep
		}
	}
	if hasToolPrefix(request.ToolSet, "file.") || hasToolPrefix(request.ToolSet, "user.") {
		return EffortLevelStandard
	}
	if request.ToolSet != nil && len(request.ToolSet.ListToolNames()) > 0 && looksLikeToolRequest(prompt) {
		return EffortLevelStandard
	}
	return EffortLevelQuick
}

func looksLikeSyntheticConnectorVerificationProbe(prompt string) bool {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(prompt)))
	if len(fields) != 3 {
		return false
	}
	if fields[0] != "verify" {
		return false
	}
	if fields[1] != "invited" && fields[1] != "uninvited" {
		return false
	}
	return isDigitString(fields[2])
}

func isDigitString(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func hasToolPrefix(toolRegistry *ToolSet, prefix string) bool {
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
	if NormalizeEffortLevel(string(intakeDecision.EffortLevel)) == "" {
		return errors.New("intake effort level is invalid")
	}
	return nil
}
