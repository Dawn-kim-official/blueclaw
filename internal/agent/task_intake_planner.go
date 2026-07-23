package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/memory"
)

type IntakeClassification string
type TaskShape string
type TurnRoute string
type ApprovalSignal string
type BusyRoute string
type PriorTaskReference string

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

	PriorTaskReferenceNone            PriorTaskReference = "none"
	PriorTaskReferenceOutcomeRecovery PriorTaskReference = "outcome_recovery"

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
	DefaultTaskLevel      TaskLevel
	SkillTaskLevelFloor   TaskLevel
	DebugAddressingReason bool
}

type AgentRequest struct {
	RequesterPersonID          string
	RequesterName              string
	RequesterCallingName       string
	RequesterHandle            string
	RequesterCircles           []string
	SourceReference            string
	IsApprovalContinuation     bool
	IsRuntimeRestartResume     bool
	ExistingTaskRunID          string
	OriginReplyTargetID        string
	OriginIsThread             bool
	ProfileName                string
	ConversationID             string
	ConversationType           string
	Prompt                     string
	InputParts                 []AgentPart
	ResponseLanguage           string
	VisibleContext             VisibleContext
	MemoryFacts                []memory.MemoryFact
	ToolSet                    *ToolSet
	PinnedToolNames            []string
	PinnedSkillNames           []string
	WorkspaceRootPath          string
	ActivePaths                []string
	InstructionPrompt          string
	ActiveGoal                 ActiveGoal
	PriorTask                  PriorTaskContext
	ScheduledRun               ScheduledRunContext
	ActiveTask                 ActiveTaskContext
	PendingConfirmation        PendingConfirmationContext
	PendingChoice              PendingChoiceContext
	PendingInput               PendingInputContext
	TaskShape                  TaskShape
	AllowGiveUp                bool
	AllowGiveUpReason          string
	PrecomputedTurnDecision    *TurnDecision
	IsPrecomputedDecisionExact bool
	SkipSkillSelection         bool
	AmbientDuty                AmbientDutyContext
	TaskLevel                  TaskLevel
	TurnStartedAt              time.Time
	CheckpointSender           AgentCheckpointSender
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
	TaskRunID     string
	Question      string
	SelectionMode string
	Options       []ChoiceReplyOption
}

type IntakeDecision struct {
	Classification         IntakeClassification  `json:"classification"`
	TaskShape              TaskShape             `json:"taskShape"`
	TaskLevel              TaskLevel             `json:"level"`
	EstimatedMinutes       int                   `json:"estimatedMinutes"`
	RequestedOutputFormats []string              `json:"requestedOutputFormats"`
	ExpectedResults        []ExpectedResult      `json:"expectedResults,omitempty"`
	ResponseLanguage       string                `json:"responseLanguage"`
	Reason                 string                `json:"reason"`
	UserFacingReply        string                `json:"userFacingReply"`
	InitialToolNames       []string              `json:"initialToolNames,omitempty"`
	PriorTaskReference     PriorTaskReference    `json:"priorTaskReference,omitempty"`
	ClarificationQuestion  string                `json:"clarificationQuestion,omitempty"`
	ClarificationOptions   []ClarificationOption `json:"clarificationOptions,omitempty"`
}

type ClarificationOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value,omitempty"`
}

type TurnDecision struct {
	Route                  TurnRoute             `json:"route"`
	Classification         IntakeClassification  `json:"classification"`
	TaskShape              TaskShape             `json:"taskShape"`
	TaskLevel              TaskLevel             `json:"level"`
	EstimatedMinutes       int                   `json:"estimatedMinutes"`
	RequestedOutputFormats []string              `json:"requestedOutputFormats"`
	ExpectedResults        []ExpectedResult      `json:"expectedResults,omitempty"`
	ResponseLanguage       string                `json:"responseLanguage"`
	Reason                 string                `json:"reason"`
	UserFacingReply        string                `json:"userFacingReply"`
	InitialToolNames       []string              `json:"initialToolNames,omitempty"`
	PriorTaskReference     PriorTaskReference    `json:"priorTaskReference,omitempty"`
	Approval               *ApprovalSignal       `json:"approval,omitempty"`
	Choices                []string              `json:"choices,omitempty"`
	ClarificationQuestion  string                `json:"clarificationQuestion,omitempty"`
	ClarificationOptions   []ClarificationOption `json:"clarificationOptions,omitempty"`
	ReactionEmojiName      string                `json:"reactionEmojiName,omitempty"`
	BusyRoute              BusyRoute             `json:"busyRoute,omitempty"`
	BusyInstruction        string                `json:"busyInstruction,omitempty"`
}

func (turnDecision TurnDecision) IntakeDecision() IntakeDecision {
	return IntakeDecision{
		Classification:         turnDecision.Classification,
		TaskShape:              turnDecision.TaskShape,
		TaskLevel:              NormalizeTaskLevel(string(turnDecision.TaskLevel)),
		EstimatedMinutes:       turnDecision.EstimatedMinutes,
		RequestedOutputFormats: append([]string{}, turnDecision.RequestedOutputFormats...),
		ExpectedResults:        normalizeExpectedResults(turnDecision.ExpectedResults),
		ResponseLanguage:       turnDecision.ResponseLanguage,
		Reason:                 turnDecision.Reason,
		UserFacingReply:        turnDecision.UserFacingReply,
		InitialToolNames:       append([]string{}, turnDecision.InitialToolNames...),
		PriorTaskReference:     normalizePriorTaskReference(turnDecision.PriorTaskReference),
		ClarificationQuestion:  turnDecision.ClarificationQuestion,
		ClarificationOptions:   append([]ClarificationOption{}, turnDecision.ClarificationOptions...),
	}
}

func (turnDecision TurnDecision) WithRestoredIntakeState(intakeDecision IntakeDecision) TurnDecision {
	if intakeDecision.EstimatedMinutes < 1 {
		return turnDecision
	}
	turnDecision.Classification = intakeDecision.Classification
	turnDecision.TaskShape = intakeDecision.TaskShape
	turnDecision.TaskLevel = intakeDecision.TaskLevel
	turnDecision.EstimatedMinutes = intakeDecision.EstimatedMinutes
	turnDecision.RequestedOutputFormats = append([]string{}, intakeDecision.RequestedOutputFormats...)
	turnDecision.ExpectedResults = normalizeExpectedResults(intakeDecision.ExpectedResults)
	turnDecision.InitialToolNames = append([]string{}, intakeDecision.InitialToolNames...)
	return turnDecision
}

type TaskIntakePlanner struct {
	languageModel llm.LanguageModelProvider
	options       IntakeOptions
}

type TurnRouter struct {
	languageModel llm.LanguageModelProvider
	options       IntakeOptions
}

const turnRouterMaxTokens = 1600

const taskRecordRoutingInstruction = "Treat requests to add, update, list, or delete a task or reminder as management of the task record, not execution of the future work described in its title or notes. A task title, description, and any explicitly requested due date are sufficient to add the record. Do not ask for files, credentials, or other inputs that would only be needed when performing that future task. Editing a record's own fields — title, date, status, notes — with values the message already states is executable as written: route it as a bounded maintenance_task, never to clarify or needs_confirmation, and never treat the edit itself as approval-gated."
const clarificationReviewInstruction = "Review the previous clarification decision. Use clarify with needs_confirmation only when essential user input is missing. Approval for risky, destructive, paid, or externally visible work is handled after routing, so never ask for approval here. If the request is executable as written, return start_task with bounded_task. If essential input is truly missing, keep clarify and ask exactly for that input."

var ErrTurnRouterDisabled = errors.New("turn router disabled")
var ErrTurnRouterLanguageModelUnavailable = errors.New("turn router language model unavailable")

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
	if NormalizeTaskLevel(string(options.DefaultTaskLevel)) == "" {
		options.DefaultTaskLevel = TaskLevelLow
	}
	options.SkillTaskLevelFloor = NormalizeTaskLevel(string(options.SkillTaskLevelFloor))
	return options
}

func (taskIntakePlanner TaskIntakePlanner) Plan(ctx context.Context, request AgentRequest) (IntakeDecision, error) {
	turnDecision, errorValue := NewTurnRouter(taskIntakePlanner.languageModel, taskIntakePlanner.options).Plan(ctx, request)
	return turnDecision.IntakeDecision(), errorValue
}

func (turnRouter TurnRouter) Plan(ctx context.Context, request AgentRequest) (TurnDecision, error) {
	if request.PrecomputedTurnDecision != nil {
		if request.IsPrecomputedDecisionExact {
			return *request.PrecomputedTurnDecision, nil
		}
		return turnRouter.normalizeDecision(*request.PrecomputedTurnDecision, request)
	}
	if !turnRouter.options.IsEnabled {
		return TurnDecision{}, ErrTurnRouterDisabled
	}
	if turnRouter.languageModel == nil {
		return TurnDecision{}, ErrTurnRouterLanguageModelUnavailable
	}
	turnDecision, errorValue := turnRouter.planWithLanguageModel(ctx, request)
	if errorValue != nil {
		return TurnDecision{}, fmt.Errorf("turn router: %w", errorValue)
	}
	normalizedDecision, normalizationError := turnRouter.normalizeDecision(turnDecision, request)
	if !clarificationDecisionNeedsReview(turnDecision) {
		return normalizedDecision, normalizationError
	}
	reviewedDecision, errorValue := turnRouter.reviewClarificationDecision(ctx, request, turnDecision)
	if errorValue != nil {
		if normalizationError == nil {
			return normalizedDecision, nil
		}
		return TurnDecision{}, fmt.Errorf("turn router clarification review: %w", errorValue)
	}
	return turnRouter.normalizeDecision(reviewedDecision, request)
}

func (turnRouter TurnRouter) planWithLanguageModel(ctx context.Context, request AgentRequest) (TurnDecision, error) {
	return turnRouter.planWithMessages(ctx, request, turnRouter.buildMessages(request))
}

func (turnRouter TurnRouter) planWithMessages(ctx context.Context, request AgentRequest, messages []llm.Message) (TurnDecision, error) {
	structuredResponse, errorValue := turnRouter.generateStructuredResponse(ctx, turnRouterRequest(request, messages))
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

func (turnRouter TurnRouter) generateStructuredResponse(ctx context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	structuredResponse, errorValue := turnRouter.languageModel.GenerateStructuredResponse(ctx, request)
	if errorValue == nil {
		return structuredResponse, nil
	}
	if errors.Is(errorValue, context.Canceled) || errors.Is(errorValue, context.DeadlineExceeded) || ctx.Err() != nil {
		return llm.StructuredResponse{}, errorValue
	}
	correction, isCorrectable := llm.StructuredOutputCorrectionFromError(errorValue)
	if !isCorrectable {
		return llm.StructuredResponse{}, errorValue
	}
	correctionRequest := request
	correctionRequest.Messages = append([]llm.Message{}, request.Messages...)
	correctionRequest.Messages = append(correctionRequest.Messages, llm.Message{
		Role:    "system",
		Content: turnRouterCorrectionInstruction(correction),
	})
	return turnRouter.languageModel.GenerateStructuredResponse(ctx, correctionRequest)
}

func turnRouterRequest(request AgentRequest, messages []llm.Message) llm.StructuredResponseRequest {
	maxTokens := turnRouterMaxTokens
	return llm.StructuredResponseRequest{
		Messages:          messages,
		GenerationOptions: llm.GenerationOptions{MaxTokens: &maxTokens},
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_turn_router",
			Document:           turnRouterSchema(request),
			IsStrictlyEnforced: true,
		},
	}
}

func turnRouterCorrectionInstruction(correction llm.StructuredOutputCorrection) string {
	messageParts := []string{
		"The previous response did not match the required structured output.",
		"Regenerate the complete response against the same schema.",
		"Correction code: " + correction.Code + ".",
		"Diagnostic category: " + string(correction.Diagnostic.Category) + ".",
	}
	if correction.Diagnostic.FinishReason != "" {
		messageParts = append(messageParts, "Finish reason: "+string(correction.Diagnostic.FinishReason)+".")
	}
	for _, issue := range correction.Diagnostic.ValidationIssues {
		messageParts = append(messageParts, "Validation issue: "+issue.FieldPath+" ("+string(issue.Code)+").")
	}
	return strings.Join(messageParts, " ")
}

func (turnRouter TurnRouter) reviewClarificationDecision(ctx context.Context, request AgentRequest, decision TurnDecision) (TurnDecision, error) {
	document, errorValue := json.Marshal(decision)
	if errorValue != nil {
		return TurnDecision{}, errorValue
	}
	messages := append(turnRouter.buildMessages(request),
		llm.Message{Role: "assistant", Content: string(document)},
		llm.Message{Role: "user", Content: clarificationReviewInstruction},
	)
	return turnRouter.planWithMessages(ctx, request, messages)
}

func clarificationDecisionNeedsReview(decision TurnDecision) bool {
	if decision.Route != TurnRouteClarify && decision.Classification != IntakeClassificationNeedsConfirmation {
		return false
	}
	return strings.TrimSpace(decision.ClarificationQuestion) == "" ||
		len(decision.RequestedOutputFormats) > 0 ||
		len(decision.ExpectedResults) > 0 ||
		len(decision.InitialToolNames) > 0
}

func (turnRouter TurnRouter) buildMessages(request AgentRequest) []llm.Message {
	toolDescriptions := "No tools are available."
	if request.ToolSet != nil && len(request.ToolSet.ListToolNames()) > 0 {
		toolDescriptions = intakeToolDescriptions(request.ToolSet)
	}
	messages := []llm.Message{
		{
			Role:    "system",
			Content: "You are Blueclaw's channel-agnostic turn router and task intake planner. Choose the route for the latest user message and classify the task shape. Keep terminal decisions consistent: needs_confirmation uses route=clarify and taskShape=approval_gated_task; unsupported uses route=give_up and taskShape=immediate_reply; consume uses classification=quick_reply and taskShape=immediate_reply. The latest user message is authoritative. Prior conversation may be used only when it helps interpret whether the latest message continues, revises, asks about, cancels, replaces an active task, or is a bare assistant mention requesting a response to recent context. Do not carry stale subjects, tools, or artifact formats into a self-contained new request. Use quick_reply for direct answers that may answer directly or use a small useful read-only or computation tool once, including greetings, jokes, playful office banter, capability questions, arithmetic, short synthetic verification probes, opinions, casual recommendations, brainstorming, and answers available from common knowledge or visible conversation context. research_task requires actual information acquisition from an external or private source, or synthesis across source material. If the assistant can choose a useful answer from its own judgment, common knowledge, or visible context, use immediate_reply even when the user calls it a recommendation. Do not require a preference merely to improve an answer when a reasonable answer can be given now. Do not ignore jokes or casual addressed remarks; answer like a concise coworker. Use bounded_task for executable tool work. Use maintenance_task or approval_gated_task only for work that changes state; use research_task for private or external reads and lookups, and immediate_reply for tool-free answers. Use needs_confirmation only when essential user input is missing; approval for risky, destructive, paid, or externally visible work is handled after routing. unsupported ONLY for requests that are pointless to even attempt — physically impossible or nonsensical (for example fetching a physical object), or plainly improper on their face such as revealing another person's password or private national ID number. unsupported is NOT a security or permission gate: the operating system enforces real permission at execution, so an action the requester lacks rights for simply fails there — never pre-refuse over permissions, just attempt it. Answer ordinary work needs such as a coworker's contact details, schedules, or documents rather than refusing. Use common sense; whenever the work could plausibly be done with terminal commands, skills, file tools, or capability operations, prefer bounded_task and attempt it. Set level to the single difficulty tier that sizes both the model and the work budget: low for ordinary bounded work with a clear short outcome that normally produces one final user reply even if it needs a few tools; medium for multi-step work, research, or artifact generation where progress updates are useful; high for long, wide, deployment, or verification-heavy work. Do not choose above high; the runtime raises the tier on its own for website and presentation deliverables and for tasks that stall. Set estimatedMinutes to how many wall-clock minutes a careful human professional would realistically spend doing this specific task well end to end, including drafting, building, and reviewing and iterating on the result — not a rushed minimum. Do not lowball: a quick lookup is about 1, a normal bounded task a few, and design, document, deck, or site work that involves building and visual review is typically many minutes and often 15 or more, scaled up further for breadth and polish. This estimate is internal planning metadata only: never mention it, a duration, an ETA, or a completion time in any user-facing reply or progress message. Use clarify when the latest request cannot be routed safely without a user choice. Do not use clarify for a message that only mentions the assistant when recent visible context gives a clear topic. When route is clarify, provide clarificationQuestion and 2-5 clarificationOptions whenever finite choices are natural. Use consume for addressed messages that need no text reply; consume is delivered as an emoji reaction, not a text reply. Prefer consume with reactionEmojiName for lightweight acknowledgement. Never consume a message that asks the assistant to do, check, read, verify, or report anything: a question or work request always needs a worked text reply even when the outcome seems obvious, so route it as a task or quick_reply instead. For consume, put a concise natural fallback acknowledgement in userFacingReply; the runtime sends it only when a direct-message reaction cannot be delivered. For non-consume routes, set reactionEmojiName to null or omit it. PriorTaskContext, when present, is a candidate previous task in the same conversation or reply target, not an active task. Set priorTaskReference=outcome_recovery only when the latest message asks to deliver, retry, continue, or revise that prior task's outcome. Set priorTaskReference=none for unrelated or self-contained requests, including a follow-up that asks to read, open, check, or summarize an artifact the prior task already delivered — that is a new read request, not a recovery. Set requestedOutputFormats to the explicit deliverable file formats when the latest request asks to create, edit, convert, generate, or deliver a file artifact; leave it null for reading, summarizing, searching, or analyzing an input attachment, unless priorTaskReference=outcome_recovery and the prior task prompt, result, known contract, or latest message identifies the deliverable format. A request to read or confirm the content of an existing file is answered in the reply text and has no deliverable format, even when that file itself has one. requestedOutputFormats should contain only explicit deliverable formats such as html, pptx, pdf, txt, docx, xlsx, csv, or json. Set initialToolNames to exact callable tool names copied from Available tools that this request will most likely call first; include only confident picks and leave it empty when unsure or when no tool is needed. When the visible conversation shows a site, document, or artifact the assistant already created for this requester, a request to change, extend, preview, or publish it is a follow-up edit on that same artifact: suggest its status or read tool and the edit tool, never the create tool. Pick tools whose effect matches the visible outcome the user asked for: a note, memo, or announcement the user wants visible in a conversation or channel is a message send, while memory tools store private assistant recall that nobody sees. Use values like html, pptx, pdf, txt, docx, xlsx, csv, or json when explicit. Treat words like presentation, slides, deck, ppt, 피피티, and 발표자료 as the kind of artifact, not as a .pptx file format unless the user explicitly requests a PowerPoint/PPTX file or asks for all common slide formats. If the user asks for a presentation as HTML, requestedOutputFormats should be [\"html\"], not [\"html\",\"pptx\"]. A request to create or update a website or web page is a live site deliverable, not a file: leave requestedOutputFormats null unless the user explicitly asks for an HTML file to download or send. Set responseLanguage to the language the assistant should use for user-facing replies; use same_as_conversation only when an explicit runtime preference already defines it.",
		},
		{
			Role:    "system",
			Content: responseLanguageInstruction(request.ResponseLanguage),
		},
		{
			Role:    "system",
			Content: "bounded_task must use a task shape other than immediate_reply. immediate_reply is only for quick_reply and unsupported decisions.",
		},
		{
			Role:    "system",
			Content: taskRecordRoutingInstruction,
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
	if priorTaskDescription := priorTaskContextDescription(request.PriorTask); priorTaskDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: priorTaskDescription})
	}
	if scheduledRunDescription := (LLMContextBuilder{}).scheduledRunContext(request.ScheduledRun); scheduledRunDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: scheduledRunDescription})
	}
	if routingContext := turnRoutingContextDescription(request); routingContext != "" {
		messages = append(messages, llm.Message{Role: "system", Content: routingContext})
	}
	messages = append(messages, llm.Message{Role: "system", Content: buildTemporalContextDescription(request.TurnStartedAt)})
	messages = append(messages, llm.Message{Role: "user", Content: request.Prompt})
	return messages
}

func intakeToolDescriptions(toolSet *ToolSet) string {
	callableToolDescriptions := callableToolDescriptionsForIntake(toolSet)
	lines := []string{}
	if len(callableToolDescriptions) > 0 {
		lines = append(lines, "Available tools:\n"+strings.Join(callableToolDescriptions, "\n"))
	}
	if len(lines) == 0 {
		return "No tools are available."
	}
	return strings.Join(lines, "\n")
}

func callableToolDescriptionsForIntake(toolSet *ToolSet) []string {
	descriptions := []string{}
	if toolSet == nil {
		return descriptions
	}
	for _, toolDefinition := range toolSet.ListRegisteredToolDefinitions() {
		toolName := strings.TrimSpace(toolDefinition.Name)
		if !toolIsModelCallable(toolName) || !requestToolSetCanReachTool(toolSet, toolName) {
			continue
		}
		description := strings.TrimSpace(toolDefinition.Description)
		if description == "" {
			descriptions = append(descriptions, "- "+toolName)
			continue
		}
		descriptions = append(descriptions, "- "+toolName+": "+description)
	}
	return descriptions
}

func (turnRouter TurnRouter) normalizeDecision(decision TurnDecision, request AgentRequest) (TurnDecision, error) {
	decision.Route = normalizeTurnRoute(decision.Route)
	if decision.Route == "" {
		return TurnDecision{}, errors.New("turn router returned an invalid route")
	}
	hasPendingConfirmation := strings.TrimSpace(request.PendingConfirmation.TaskRunID) != ""
	decision.Approval = normalizeApprovalSignal(decision.Approval, hasPendingConfirmation)
	if hasPendingConfirmation && decision.Approval != nil && *decision.Approval == ApprovalSignalApprove {
		decision.Route = TurnRouteContinueTask
	}
	decision.Choices = normalizeChoiceSelections(decision.Choices, pendingChoiceContext(request))
	if strings.TrimSpace(request.ActiveTask.TaskRunID) != "" && !isValidBusyRoute(decision.BusyRoute) {
		return TurnDecision{}, errors.New("turn router returned an invalid busy route")
	}
	if strings.TrimSpace(request.ActiveTask.TaskRunID) == "" {
		decision.BusyRoute = ""
	}
	decision.BusyInstruction = strings.TrimSpace(decision.BusyInstruction)
	decision.ClarificationQuestion = strings.TrimSpace(decision.ClarificationQuestion)
	decision.ClarificationOptions = normalizeClarificationOptions(decision.ClarificationOptions)
	decision.ReactionEmojiName = NormalizeReactionEmojiName(decision.ReactionEmojiName)
	normalizedClassification := normalizeClassification(decision.Classification)
	if normalizedClassification == "" {
		return TurnDecision{}, errors.New("turn router returned an invalid classification")
	}
	decision.Classification = normalizedClassification
	normalizedTaskShape := normalizeTaskShape(decision.TaskShape)
	if normalizedTaskShape == "" {
		return TurnDecision{}, errors.New("turn router returned an invalid task shape")
	}
	decision.TaskShape = normalizedTaskShape
	decision.RequestedOutputFormats = normalizeRequestedOutputFormats(decision.RequestedOutputFormats)
	decision.ExpectedResults = normalizeExpectedResults(decision.ExpectedResults)
	decision = normalizeTurnDecisionFileRequirement(decision)
	decision = normalizeSideEffectTurnDecision(decision, request.ToolSet)
	if decision.Classification == IntakeClassificationBoundedTask && decision.TaskShape == TaskShapeImmediateReply {
		decision.TaskShape = TaskShapeMaintenanceTask
	}
	decision = canonicalizeTurnDecision(decision)
	normalizedTaskLevel := NormalizeTaskLevel(string(decision.TaskLevel))
	if normalizedTaskLevel == "" {
		return TurnDecision{}, errors.New("turn router returned an invalid task level")
	}
	decision.TaskLevel = normalizedTaskLevel
	if decision.EstimatedMinutes < 1 {
		return TurnDecision{}, errors.New("turn router returned invalid estimated minutes")
	}
	if errorValue := validateTurnDecisionConsistency(decision); errorValue != nil {
		return TurnDecision{}, errorValue
	}
	decision.InitialToolNames = registeredToolNamesOnly(request.ToolSet, appendUniqueStrings(decision.InitialToolNames))
	decision.ResponseLanguage = resolveDecisionResponseLanguage(decision.ResponseLanguage, request.ResponseLanguage)
	decision.Reason = strings.TrimSpace(decision.Reason)
	decision.PriorTaskReference = normalizePriorTaskReference(decision.PriorTaskReference)
	return decision, nil
}

func normalizeSideEffectTurnDecision(decision TurnDecision, toolSet *ToolSet) TurnDecision {
	if decision.Classification != IntakeClassificationQuickReply || !includesRegisteredSideEffectEvidence(toolSet, decision.InitialToolNames) {
		return decision
	}
	decision.Classification = IntakeClassificationBoundedTask
	if decision.TaskShape == TaskShapeImmediateReply {
		decision.TaskShape = TaskShapeMaintenanceTask
	}
	switch decision.Route {
	case TurnRouteStartTask, TurnRouteContinueTask, TurnRouteReviseTask:
	default:
		decision.Route = TurnRouteStartTask
	}
	return decision
}

func includesRegisteredSideEffectEvidence(toolSet *ToolSet, toolNames []string) bool {
	for _, toolName := range toolNames {
		registeredToolName, isRegistered := requiredEvidenceRegisteredToolName(toolSet, toolName)
		if !isRegistered || !requiredEvidenceToolCanBeSatisfied(toolSet, registeredToolName) {
			continue
		}
		toolDefinition, isDefined := toolSet.ToolDefinition(registeredToolName)
		if isDefined && ToolDefinitionRequiresSideEffectEvidence(toolDefinition) {
			return true
		}
	}
	return false
}

func canonicalizeTurnDecision(decision TurnDecision) TurnDecision {
	switch decision.Classification {
	case IntakeClassificationQuickReply:
		decision.TaskShape = TaskShapeImmediateReply
	case IntakeClassificationNeedsConfirmation:
		decision.Route = TurnRouteClarify
		decision.TaskShape = TaskShapeApprovalGatedTask
	case IntakeClassificationUnsupported:
		decision.Route = TurnRouteGiveUp
		decision.TaskShape = TaskShapeImmediateReply
	}
	return decision
}

func validateTurnDecisionConsistency(decision TurnDecision) error {
	switch decision.Classification {
	case IntakeClassificationBoundedTask:
		if decision.Route == TurnRouteConsume || decision.Route == TurnRouteClarify || decision.Route == TurnRouteGiveUp {
			return errors.New("turn router returned bounded_task with a terminal route")
		}
	}
	if decision.Route == TurnRouteConsume && decision.Classification != IntakeClassificationQuickReply {
		return errors.New("turn router returned consume without quick_reply classification")
	}
	if decision.Route == TurnRouteClarify && decision.Classification != IntakeClassificationNeedsConfirmation {
		return errors.New("turn router returned clarify without needs_confirmation classification")
	}
	if decision.Route == TurnRouteGiveUp && decision.Classification != IntakeClassificationUnsupported {
		return errors.New("turn router returned give_up without unsupported classification")
	}
	return nil
}

func isValidBusyRoute(busyRoute BusyRoute) bool {
	switch busyRoute {
	case BusyRouteStatus, BusyRouteSteer, BusyRouteReplace, BusyRouteCancel, BusyRouteNewTask, BusyRouteUnrelated:
		return true
	default:
		return false
	}
}

func normalizeTurnDecisionFileRequirement(decision TurnDecision) TurnDecision {
	if hasArtifactOutputFormat(decision.RequestedOutputFormats) {
		return decision
	}
	decision.ExpectedResults = removeExpectedResultsByType(decision.ExpectedResults, ExpectedResultTypeFile)
	decision.InitialToolNames = removeToolName(decision.InitialToolNames, FileDeliverToolName)
	return decision
}

func normalizePriorTaskReference(reference PriorTaskReference) PriorTaskReference {
	switch reference {
	case PriorTaskReferenceOutcomeRecovery:
		return PriorTaskReferenceOutcomeRecovery
	default:
		return PriorTaskReferenceNone
	}
}

func turnRouterSchema(request AgentRequest) string {
	callableToolNames := []string{}
	if request.ToolSet != nil {
		for _, toolName := range request.ToolSet.ListToolNames() {
			if toolIsModelCallable(toolName) {
				callableToolNames = append(callableToolNames, toolName)
			}
		}
		for _, toolDefinition := range request.ToolSet.ListRegisteredToolDefinitions() {
			toolName := strings.TrimSpace(toolDefinition.Name)
			if toolName != "" && requiredEvidenceToolCanBeSatisfied(request.ToolSet, toolName) {
				callableToolNames = appendUniqueStrings(callableToolNames, toolName)
			}
		}
	}
	routeValues := []string{
		string(TurnRouteContinueTask),
		string(TurnRouteReviseTask),
		string(TurnRouteAnswerQuestion),
		string(TurnRouteStartTask),
		string(TurnRouteAnswerMeta),
		string(TurnRouteClarify),
		string(TurnRouteConsume),
		string(TurnRouteGiveUp),
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
		"level": map[string]any{"type": "string", "enum": []string{
			string(TaskLevelLow),
			string(TaskLevelMedium),
			string(TaskLevelHigh),
		}},
		"estimatedMinutes": map[string]any{"type": "integer", "minimum": 1},
		"requestedOutputFormats": map[string]any{"anyOf": []any{
			map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string", "enum": []string{"html", "pptx", "pdf", "txt", "docx", "xlsx", "csv", "json"}}},
			map[string]any{"type": "null"},
		}},
		"expectedResults":  expectedResultsSchema(),
		"responseLanguage": map[string]any{"type": "string", "enum": []string{"ko", "en", "same_as_conversation"}},
		"reason":           map[string]any{"type": "string", "maxLength": 512},
		"userFacingReply":  map[string]any{"type": "string", "maxLength": 512},
		"initialToolNames": boundedNamedStringArraySchema(callableToolNames),
		"priorTaskReference": map[string]any{"type": "string", "enum": []string{
			string(PriorTaskReferenceNone),
			string(PriorTaskReferenceOutcomeRecovery),
		}},
		"clarificationQuestion": map[string]any{
			"type": "string", "maxLength": 256,
		},
		"clarificationOptions": clarificationOptionsSchema(),
		"reactionEmojiName": map[string]any{"anyOf": []any{
			map[string]any{"type": "string", "enum": allowedReactionEmojiNames},
			map[string]any{"type": "null"},
		}},
	}
	requiredProperties := []string{"route", "classification", "taskShape", "level", "estimatedMinutes", "requestedOutputFormats", "responseLanguage", "reason", "userFacingReply", "priorTaskReference"}
	if strings.TrimSpace(request.PendingConfirmation.TaskRunID) != "" {
		properties["approval"] = map[string]any{"type": "string", "enum": []string{string(ApprovalSignalApprove), string(ApprovalSignalReject), string(ApprovalSignalUnclear)}}
		requiredProperties = append(requiredProperties, "approval")
	}
	if pendingChoice := pendingChoiceContext(request); strings.TrimSpace(pendingChoice.TaskRunID) != "" {
		choiceKeys := pendingChoiceKeys(pendingChoice)
		properties["choices"] = map[string]any{"type": "array", "maxItems": len(choiceKeys), "items": map[string]any{"type": "string", "enum": choiceKeys}}
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
		properties["busyInstruction"] = map[string]any{"type": "string", "maxLength": 512}
		requiredProperties = append(requiredProperties, "busyRoute", "busyInstruction")
	}
	document, errorValue := json.Marshal(map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             requiredProperties,
		"additionalProperties": false,
	})
	if errorValue != nil {
		return `{"type":"object","properties":{"route":{"type":"string"},"classification":{"type":"string"},"taskShape":{"type":"string"},"level":{"type":"string"},"requestedOutputFormats":{"type":"null"},"responseLanguage":{"type":"string"},"reason":{"type":"string"},"userFacingReply":{"type":"string"}},"required":["route","classification","taskShape","level","requestedOutputFormats","responseLanguage","reason","userFacingReply"],"additionalProperties":false}`
	}
	return string(document)
}

func boundedNamedStringArraySchema(values []string) map[string]any {
	itemSchema := map[string]any{"type": "string"}
	if len(values) > 0 {
		itemSchema["enum"] = values
	}
	maximumItems := len(values)
	if maximumItems > 16 {
		maximumItems = 16
	}
	return map[string]any{"type": "array", "maxItems": maximumItems, "items": itemSchema}
}

func expectedResultsSchema() map[string]any {
	return map[string]any{
		"type":     "array",
		"maxItems": 8,
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":              map[string]any{"type": "string", "maxLength": 128},
				"type":            map[string]any{"type": "string", "enum": []string{ExpectedResultTypeMessage, ExpectedResultTypeFile, ExpectedResultTypeLink}},
				"description":     map[string]any{"type": "string", "maxLength": 256},
				"required":        map[string]any{"type": "boolean"},
				"acceptanceHints": map[string]any{"type": "array", "maxItems": 4, "items": map[string]any{"type": "string", "maxLength": 128}},
			},
			"required":             []string{"description", "required"},
			"additionalProperties": false,
		},
	}
}

func pendingChoiceContext(request AgentRequest) PendingChoiceContext {
	if strings.TrimSpace(request.PendingChoice.TaskRunID) != "" {
		return request.PendingChoice
	}
	if strings.TrimSpace(request.PendingInput.TaskRunID) == "" || len(request.PendingInput.Options) == 0 {
		return PendingChoiceContext{}
	}
	return PendingChoiceContext{
		TaskRunID:     request.PendingInput.TaskRunID,
		Question:      request.PendingInput.Question,
		SelectionMode: request.PendingInput.SelectionMode,
		Options:       request.PendingInput.Options,
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
				"key":   map[string]any{"type": "string", "maxLength": 64},
				"label": map[string]any{"type": "string", "maxLength": 128},
				"value": map[string]any{"type": "string", "maxLength": 256},
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
	if pendingChoice := pendingChoiceContext(request); strings.TrimSpace(pendingChoice.TaskRunID) != "" {
		optionLines := []string{}
		for index, option := range pendingChoice.Options {
			optionLines = append(optionLines, strconv.Itoa(index+1)+". "+strings.TrimSpace(option.Label)+" / key "+strings.TrimSpace(option.Key))
		}
		lines = append(lines,
			"Pending input options:",
			"- Question: "+strings.TrimSpace(pendingChoice.Question),
			"- Selection mode: "+strings.TrimSpace(pendingChoice.SelectionMode),
			"- Options: "+strings.Join(optionLines, "; "),
			"- Return choices as option keys when the latest natural-language answer matches options. Return an empty array for a valid custom answer.",
			"- Preserve the latest user message as the task input; choices classify it but do not replace its wording.",
		)
	}
	if strings.TrimSpace(request.PendingInput.TaskRunID) != "" {
		lines = append(lines,
			"Pending input:",
			"- Question: "+strings.TrimSpace(request.PendingInput.Question),
			"- Use continue_task or revise_task when the latest message answers or modifies this pending input.",
			"- Use start_task when the latest message is a self-contained question or independent request instead of an answer.",
			"- Treat messages that delegate the missing choice back to the assistant as an answer to continue the task; do not ask the same question again.",
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
		case "html", "pptx", "pdf", "txt", "docx", "xlsx", "csv", "json":
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

func hasArtifactOutputFormat(formats []string) bool {
	for _, format := range normalizeRequestedOutputFormats(formats) {
		switch format {
		case "html", "pptx", "pdf", "txt", "docx", "xlsx", "csv", "json":
			return true
		}
	}
	return false
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

func normalizeTurnRoute(route TurnRoute) TurnRoute {
	switch route {
	case TurnRouteContinueTask, TurnRouteReviseTask, TurnRouteAnswerQuestion, TurnRouteStartTask, TurnRouteAnswerMeta, TurnRouteClarify, TurnRouteConsume, TurnRouteGiveUp:
		return route
	default:
		return ""
	}
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

func registeredToolNamesOnly(toolRegistry *ToolSet, toolNames []string) []string {
	if toolRegistry == nil || len(toolNames) == 0 {
		return nil
	}
	registeredToolNames := []string{}
	for _, toolName := range appendUniqueStrings([]string{}, toolNames...) {
		trimmedToolName := strings.TrimSpace(toolName)
		if toolRegistry.IsAllowed(trimmedToolName) || requiredEvidenceToolCanBeSatisfied(toolRegistry, trimmedToolName) {
			registeredToolNames = appendUniqueStrings(registeredToolNames, trimmedToolName)
		}
	}
	return registeredToolNames
}

func (intakeDecision IntakeDecision) Validate() error {
	if normalizeClassification(intakeDecision.Classification) == "" {
		return errors.New("intake classification is invalid")
	}
	if NormalizeTaskLevel(string(intakeDecision.TaskLevel)) == "" {
		return errors.New("intake task level is invalid")
	}
	return nil
}
