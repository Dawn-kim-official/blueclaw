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
	TaskShapeCodingTask         TaskShape = "coding_task"

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
	RequesterPersonID       string
	RequesterName           string
	RequesterCallingName    string
	RequesterHandle         string
	RequesterCircles        []string
	SourceReference         string
	IsApprovalContinuation  bool
	IsRuntimeRestartResume  bool
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
	PriorTask               PriorTaskContext
	ScheduledRun            ScheduledRunContext
	ActiveTask              ActiveTaskContext
	PendingConfirmation     PendingConfirmationContext
	PendingChoice           PendingChoiceContext
	PendingInput            PendingInputContext
	AllowGiveUp             bool
	AllowGiveUpReason       string
	PrecomputedTurnDecision *TurnDecision
	AmbientDuty             AmbientDutyContext
	TaskLevel               TaskLevel
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
	TaskLevel                 TaskLevel             `json:"level"`
	EstimatedMinutes          int                   `json:"estimatedMinutes"`
	LaunchNotice              string                `json:"launchNotice"`
	RequestedOutputFormats    []string              `json:"requestedOutputFormats"`
	ExpectedResults           []ExpectedResult      `json:"expectedResults,omitempty"`
	RequiredEvidenceTools     []string              `json:"requiredEvidence,omitempty"`
	SiteRequestEvidence       string                `json:"siteRequestEvidence"`
	ResponseLanguage          string                `json:"responseLanguage"`
	Reason                    string                `json:"reason"`
	UserFacingReply           string                `json:"userFacingReply"`
	InitialToolNames          []string              `json:"initialToolNames,omitempty"`
	PriorTaskReference        PriorTaskReference    `json:"priorTaskReference,omitempty"`
	ClarificationQuestion     string                `json:"clarificationQuestion,omitempty"`
	ClarificationOptions      []ClarificationOption `json:"clarificationOptions,omitempty"`
	UsedDeterministicFallback bool                  `json:"usedDeterministicFallback"`
	siteNormalizationReport   siteRequirementNormalizationReport
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
	TaskLevel                 TaskLevel             `json:"level"`
	EstimatedMinutes          int                   `json:"estimatedMinutes"`
	LaunchNotice              string                `json:"launchNotice"`
	RequestedOutputFormats    []string              `json:"requestedOutputFormats"`
	ExpectedResults           []ExpectedResult      `json:"expectedResults,omitempty"`
	RequiredEvidenceTools     []string              `json:"requiredEvidence,omitempty"`
	SiteRequestEvidence       string                `json:"siteRequestEvidence"`
	ResponseLanguage          string                `json:"responseLanguage"`
	Reason                    string                `json:"reason"`
	UserFacingReply           string                `json:"userFacingReply"`
	InitialToolNames          []string              `json:"initialToolNames,omitempty"`
	PriorTaskReference        PriorTaskReference    `json:"priorTaskReference,omitempty"`
	Approval                  *ApprovalSignal       `json:"approval,omitempty"`
	Choices                   []string              `json:"choices,omitempty"`
	ClarificationQuestion     string                `json:"clarificationQuestion,omitempty"`
	ClarificationOptions      []ClarificationOption `json:"clarificationOptions,omitempty"`
	ReactionEmojiName         string                `json:"reactionEmojiName,omitempty"`
	BusyRoute                 BusyRoute             `json:"busyRoute,omitempty"`
	BusyInstruction           string                `json:"busyInstruction,omitempty"`
	UsedDeterministicFallback bool                  `json:"usedDeterministicFallback"`
	siteNormalizationReport   siteRequirementNormalizationReport
}

func (turnDecision TurnDecision) IntakeDecision() IntakeDecision {
	return IntakeDecision{
		Classification:            turnDecision.Classification,
		TaskShape:                 turnDecision.TaskShape,
		TaskLevel:                 NormalizeTaskLevel(string(turnDecision.TaskLevel)),
		EstimatedMinutes:          turnDecision.EstimatedMinutes,
		LaunchNotice:              strings.TrimSpace(turnDecision.LaunchNotice),
		RequestedOutputFormats:    append([]string{}, turnDecision.RequestedOutputFormats...),
		ExpectedResults:           normalizeExpectedResults(turnDecision.ExpectedResults),
		RequiredEvidenceTools:     appendUniqueStrings(turnDecision.RequiredEvidenceTools),
		SiteRequestEvidence:       strings.TrimSpace(turnDecision.SiteRequestEvidence),
		ResponseLanguage:          turnDecision.ResponseLanguage,
		Reason:                    turnDecision.Reason,
		UserFacingReply:           turnDecision.UserFacingReply,
		InitialToolNames:          append([]string{}, turnDecision.InitialToolNames...),
		PriorTaskReference:        normalizePriorTaskReference(turnDecision.PriorTaskReference),
		ClarificationQuestion:     turnDecision.ClarificationQuestion,
		ClarificationOptions:      append([]ClarificationOption{}, turnDecision.ClarificationOptions...),
		UsedDeterministicFallback: turnDecision.UsedDeterministicFallback,
		siteNormalizationReport:   turnDecision.siteNormalizationReport,
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
	if NormalizeTaskLevel(string(options.DefaultTaskLevel)) == "" {
		options.DefaultTaskLevel = TaskLevelLow
	}
	options.SkillTaskLevelFloor = NormalizeTaskLevel(string(options.SkillTaskLevelFloor))
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
		return busyRouteSteerOnActiveTask(defaultDecision, request)
	}
	turnDecision, errorValue := turnRouter.planWithLanguageModel(ctx, request)
	if errorValue != nil {
		return busyRouteSteerOnActiveTask(defaultDecision, request)
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

const requiredEvidenceReaskInstruction = "This request was already classified as side-effect work but requiredEvidence came back empty, which is not allowed. This task performs a side effect and its completion must be observable. Set requiredEvidence to one or more exact names copied from Registered requiredEvidence names above whose successful observation proves this side effect happened; never use a dispatcher tool name. requiredEvidence must not be empty for this decision."

func (turnRouter TurnRouter) ReaskRequiredEvidence(ctx context.Context, request AgentRequest) (TurnDecision, error) {
	if turnRouter.languageModel == nil {
		return TurnDecision{}, errors.New("intake language model unavailable for required evidence re-ask")
	}
	messages := append(turnRouter.buildMessages(request), llm.Message{
		Role:    "system",
		Content: requiredEvidenceReaskInstruction,
	})
	structuredResponse, errorValue := turnRouter.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: messages,
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
		toolDescriptions = intakeToolDescriptions(request.ToolSet)
	}
	messages := []llm.Message{
		{
			Role:    "system",
			Content: "You are Blueclaw's channel-agnostic turn router and task intake planner. Choose the route for the latest user message and classify the task shape. The latest user message is authoritative. Prior conversation may be used only when it helps interpret whether the latest message continues, revises, asks about, cancels, replaces an active task, or is a bare assistant mention requesting a response to recent context. Do not carry stale subjects, tools, or artifact formats into a self-contained new request. Use quick_reply for direct answers that may answer directly or use a small useful read-only or computation tool once, including greetings, jokes, playful office banter, capability questions, arithmetic, and short synthetic verification probes. Do not ignore jokes or casual addressed remarks; answer like a concise coworker. Use bounded_task for one-request tool work, needs_confirmation for large, risky, destructive, or externally visible work, and unsupported ONLY for requests that are pointless to even attempt — physically impossible or nonsensical (for example fetching a physical object), or plainly improper on their face such as revealing another person's password or private national ID number. unsupported is NOT a security or permission gate: the operating system enforces real permission at execution, so an action the requester lacks rights for simply fails there — never pre-refuse over permissions, just attempt it. Answer ordinary work needs such as a coworker's contact details, schedules, or documents rather than refusing. Use common sense; whenever the work could plausibly be done with terminal commands, skills, file tools, or capability operations, prefer bounded_task and attempt it. Set level to the single difficulty tier that sizes both the model and the work budget: low for ordinary bounded work with a clear short outcome that normally produces one final user reply even if it needs a few tools; medium for multi-step work, research, or artifact generation where progress updates are useful; high for long, wide, deployment, or verification-heavy work. Do not choose above high; the runtime raises the tier on its own for website and presentation deliverables and for tasks that stall. Set taskShape to coding_task when the requested work is software programming such as writing, debugging, refactoring, or reviewing source code, so the runtime can route it to the coding model. Set estimatedMinutes to how many wall-clock minutes a careful human professional would realistically spend doing this specific task well end to end, including drafting, building, and reviewing and iterating on the result — not a rushed minimum. Do not lowball: a quick lookup is about 1, a normal bounded task a few, and design, document, deck, or site work that involves building and visual review is typically many minutes and often 15 or more, scaled up further for breadth and polish. When estimatedMinutes is more than 2, set launchNotice to one short sentence in the response language that acknowledges you have started and are working on it, so the user is not left waiting; do not state any specific time estimate, minute count, or internal budget in launchNotice. Leave launchNotice empty for quick work that finishes in about two minutes or less. Use clarify when the latest request cannot be routed safely without a user choice. Do not use clarify for a message that only mentions the assistant when recent visible context gives a clear topic. When route is clarify, provide clarificationQuestion and 2-5 clarificationOptions whenever finite choices are natural. Use consume for addressed messages that need no text reply; consume is delivered as an emoji reaction, not a text reply. Prefer consume with reactionEmojiName for lightweight acknowledgement. For non-consume routes, set reactionEmojiName to null or omit it. PriorTaskContext, when present, is a candidate previous task in the same conversation or reply target, not an active task. Set priorTaskReference=outcome_recovery only when the latest message asks to deliver, retry, continue, or revise that prior task's outcome. Set priorTaskReference=none for unrelated or self-contained requests. Set requestedOutputFormats to the explicit deliverable file formats when the latest request asks to create, edit, convert, generate, or deliver a file artifact; leave it null for reading, summarizing, searching, or analyzing an input attachment, unless priorTaskReference=outcome_recovery and the prior task prompt, result, known contract, or latest message identifies the deliverable format. requestedOutputFormats should contain only explicit deliverable formats such as html, pptx, pdf, txt, docx, xlsx, or csv. Set requiredEvidence to the exact registered native tool or capability operation names whose successful observations are required before the task can be considered complete; requiredEvidence is an AND array. Use only names from Registered requiredEvidence names when they match the requested outcome, and never use a dispatcher tool name as requiredEvidence. Use [] for direct answers, summaries, analysis, or tool-free replies that do not require a side effect or delivered file. For side-effect work, externally visible work, scheduled work, and deliverable files, name the registered tool or capability operation whose success will prove completion; if you are unsure of the exact name, name the closest registered one rather than an empty array. When the latest request asks for a website, page, or web app deliverable, or a link-type expected result represents one, set siteRequestEvidence to a verbatim substring copied from the latest user message that requested it; otherwise leave siteRequestEvidence empty. Set initialToolNames to exact callable tool names copied from Available tools that this request will most likely call first; include only confident picks and leave it empty when unsure or when no tool is needed. Use values like html, pptx, pdf, txt, docx, xlsx, or csv when explicit. Treat words like presentation, slides, deck, ppt, 피피티, and 발표자료 as the kind of artifact, not as a .pptx file format unless the user explicitly requests a PowerPoint/PPTX file or asks for all common slide formats. If the user asks for a presentation as HTML, requestedOutputFormats should be [\"html\"], not [\"html\",\"pptx\"]. Set responseLanguage to the language the assistant should use for user-facing replies; use same_as_conversation only when an explicit runtime preference already defines it.",
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
	if priorTaskDescription := priorTaskContextDescription(request.PriorTask); priorTaskDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: priorTaskDescription})
	}
	if scheduledRunDescription := (LLMContextBuilder{}).scheduledRunContext(request.ScheduledRun); scheduledRunDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: scheduledRunDescription})
	}
	if routingContext := turnRoutingContextDescription(request); routingContext != "" {
		messages = append(messages, llm.Message{Role: "system", Content: routingContext})
	}
	messages = append(messages, llm.Message{Role: "user", Content: request.Prompt})
	return messages
}

func intakeToolDescriptions(toolSet *ToolSet) string {
	callableToolNames := toolSet.ListToolNames()
	registeredEvidenceDescriptions := registeredEvidenceDescriptionsForIntake(toolSet)
	lines := []string{}
	if len(callableToolNames) > 0 {
		lines = append(lines, "Available tools: "+strings.Join(callableToolNames, ", "))
	}
	if len(registeredEvidenceDescriptions) > 0 {
		lines = append(lines, "Registered requiredEvidence names:\n"+strings.Join(registeredEvidenceDescriptions, "\n"))
	}
	if len(lines) == 0 {
		return "No tools are available."
	}
	return strings.Join(lines, "\n")
}

func registeredEvidenceDescriptionsForIntake(toolSet *ToolSet) []string {
	descriptions := []string{}
	if toolSet == nil {
		return descriptions
	}
	for _, toolDefinition := range toolSet.ListToolDefinitions() {
		trimmedToolName := strings.TrimSpace(toolDefinition.Name)
		if trimmedToolName == "" || !requiredEvidenceToolCanBeSatisfied(toolSet, trimmedToolName) {
			continue
		}
		descriptions = appendUniqueStrings(descriptions, evidenceDescriptionLine(trimmedToolName, toolDefinition))
	}
	return descriptions
}

func evidenceDescriptionLine(toolName string, toolDefinition ToolDefinition) string {
	description := strings.TrimSpace(toolDefinition.Description)
	if description == "" {
		return "- " + toolName
	}
	return "- " + toolName + ": " + description
}

func (turnRouter TurnRouter) deterministicDecision(request AgentRequest) TurnDecision {
	responseLanguage := ResolveResponseLanguage(request.ResponseLanguage, request.VisibleContext.ResponseLanguage)
	requestedOutputFormats := []string{}
	priorTaskReference := deterministicPriorTaskReferenceForRequest(request)
	if priorTaskReference == PriorTaskReferenceOutcomeRecovery {
		priorTask := normalizePriorTaskContext(request.PriorTask)
		requestedOutputFormats = appendUniqueStrings(requestedOutputFormats, priorTask.RequestedOutputFormats...)
		requestedOutputFormats = appendUniqueStrings(requestedOutputFormats, artifactFormatsForAttachmentSuffixes(priorTask.OutcomeContract.RequiredAttachmentSuffixes)...)
	}
	return TurnDecision{
		Route:                     deterministicTurnRoute(request),
		Classification:            IntakeClassificationBoundedTask,
		TaskShape:                 deterministicTaskShape(request, IntakeClassificationBoundedTask),
		TaskLevel:                 LargerTaskLevel(turnRouter.options.DefaultTaskLevel, minimumTaskLevelForRequest(request)),
		RequestedOutputFormats:    requestedOutputFormats,
		Reason:                    "intake language model unavailable; treating request as bounded work",
		ResponseLanguage:          responseLanguage,
		InitialToolNames:          deterministicInitialToolNamesForRequest(request, requestedOutputFormats),
		PriorTaskReference:        priorTaskReference,
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
		decision.TaskLevel = TaskLevelXLow
		decision.RequestedOutputFormats = nil
		decision.Reason = firstNonEmptyString(decision.Reason, "synthetic connector verification probe")
		decision.UserFacingReply = ""
	}
	wasReclassifiedAwayFromConfirmation := false
	if shouldTreatConfirmationAsBoundedLocalArtifact(request, decision.IntakeDecision()) {
		decision.Classification = IntakeClassificationBoundedTask
		decision.Reason = firstNonEmptyString(decision.Reason, "local workspace artifact generation can run as bounded tool work")
		decision.UserFacingReply = ""
		wasReclassifiedAwayFromConfirmation = true
	}
	decision.RequestedOutputFormats = normalizeRequestedOutputFormats(decision.RequestedOutputFormats)
	decision.ExpectedResults = normalizeExpectedResults(decision.ExpectedResults)
	decision.RequiredEvidenceTools = appendUniqueStrings(decision.RequiredEvidenceTools)
	decision.PriorTaskReference = normalizePriorTaskReference(decision.PriorTaskReference)
	decision = promoteFileArtifactClassification(decision, request)
	decision.InitialToolNames = registeredToolNamesOnly(request.ToolSet, appendUniqueStrings(decision.InitialToolNames, deterministicInitialToolNamesForDecision(request, decision)...))
	decision.SiteRequestEvidence = strings.TrimSpace(decision.SiteRequestEvidence)
	decision, decision.siteNormalizationReport = normalizeTurnDecisionSiteRequirement(request, decision)
	if shouldTreatAsBoundedSitePrototype(request, decision.IntakeDecision()) {
		decision.Classification = IntakeClassificationBoundedTask
		decision.Reason = "available site.app capability operations can create and publish the requested prototype"
		decision.UserFacingReply = ""
		wasReclassifiedAwayFromConfirmation = true
	}
	normalizedTaskShape := normalizeTaskShape(decision.TaskShape)
	if normalizedTaskShape == "" {
		normalizedTaskShape = deterministicTaskShape(request, decision.Classification)
	}
	if wasReclassifiedAwayFromConfirmation && normalizedTaskShape == TaskShapeApprovalGatedTask {
		normalizedTaskShape = deterministicTaskShape(request, decision.Classification)
	}
	decision.TaskShape = normalizedTaskShape
	normalizedTaskLevel := NormalizeTaskLevel(string(decision.TaskLevel))
	if normalizedTaskLevel == "" {
		normalizedTaskLevel = defaultDecision.TaskLevel
	}
	decision.TaskLevel = LargerTaskLevel(normalizedTaskLevel, minimumTaskLevelForRequest(request))
	decision.RequestedOutputFormats = normalizeRequestedOutputFormats(decision.RequestedOutputFormats)
	decision.ExpectedResults = normalizeExpectedResults(decision.ExpectedResults)
	decision.ResponseLanguage = resolveDecisionResponseLanguage(decision.ResponseLanguage, request.ResponseLanguage)
	if strings.TrimSpace(decision.Reason) == "" {
		decision.Reason = defaultDecision.Reason
	}
	decision.PriorTaskReference = normalizePriorTaskReference(decision.PriorTaskReference)
	decision = promoteFileArtifactClassification(decision, request)
	decision = normalizeTaskfulConsumeRoute(decision, request)
	decision.UserFacingReply = taskIntakeOnlyUserFacingReply(decision)
	return decision
}

func taskIntakeOnlyUserFacingReply(decision TurnDecision) string {
	switch decision.Classification {
	case IntakeClassificationNeedsConfirmation, IntakeClassificationUnsupported:
		return strings.TrimSpace(decision.UserFacingReply)
	default:
		return ""
	}
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
			string(TaskShapeCodingTask),
		}},
		"level": map[string]any{"type": "string", "enum": []string{
			string(TaskLevelLow),
			string(TaskLevelMedium),
			string(TaskLevelHigh),
		}},
		"estimatedMinutes": map[string]any{"type": "integer", "minimum": 1},
		"launchNotice":     map[string]any{"type": "string"},
		"requestedOutputFormats": map[string]any{"anyOf": []any{
			map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"html", "pptx", "pdf", "txt", "docx", "xlsx", "csv"}}},
			map[string]any{"type": "null"},
		}},
		"expectedResults":  expectedResultsSchema(),
		"requiredEvidence": map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"type": "string"}},
		"siteRequestEvidence": map[string]any{
			"type": "string",
		},
		"responseLanguage": map[string]any{"type": "string", "enum": []string{"ko", "en", "same_as_conversation"}},
		"reason":           map[string]any{"type": "string"},
		"userFacingReply":  map[string]any{"type": "string"},
		"initialToolNames": map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"type": "string"}},
		"priorTaskReference": map[string]any{"type": "string", "enum": []string{
			string(PriorTaskReferenceNone),
			string(PriorTaskReferenceOutcomeRecovery),
		}},
		"clarificationQuestion": map[string]any{
			"type": "string",
		},
		"clarificationOptions": clarificationOptionsSchema(),
		"reactionEmojiName": map[string]any{"anyOf": []any{
			map[string]any{"type": "string", "enum": allowedReactionEmojiNames},
			map[string]any{"type": "null"},
		}},
	}
	requiredProperties := []string{"route", "classification", "taskShape", "level", "estimatedMinutes", "launchNotice", "requestedOutputFormats", "requiredEvidence", "siteRequestEvidence", "responseLanguage", "reason", "userFacingReply", "priorTaskReference"}
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
		return `{"type":"object","properties":{"route":{"type":"string"},"classification":{"type":"string"},"taskShape":{"type":"string"},"level":{"type":"string"},"requestedOutputFormats":{"type":"null"},"siteRequestEvidence":{"type":"string"},"responseLanguage":{"type":"string"},"reason":{"type":"string"},"userFacingReply":{"type":"string"}},"required":["route","classification","taskShape","level","requestedOutputFormats","siteRequestEvidence","responseLanguage","reason","userFacingReply"],"additionalProperties":false}`
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

// busyRouteSteerOnActiveTask marks a deterministic fallback decision as steer whenever a task
// is already active, so an intake-planner outage never falls back to launching a duplicate task.
func busyRouteSteerOnActiveTask(decision TurnDecision, request AgentRequest) TurnDecision {
	if strings.TrimSpace(request.ActiveTask.TaskRunID) != "" {
		decision.BusyRoute = BusyRouteSteer
	}
	return decision
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

func promoteFileArtifactClassification(decision TurnDecision, request AgentRequest) TurnDecision {
	if hasArtifactOutputFormat(decision.RequestedOutputFormats) && canRunFileArtifactWork(request) && decision.Classification == IntakeClassificationUnsupported {
		decision.Classification = IntakeClassificationBoundedTask
		decision.TaskShape = deterministicTaskShape(request, decision.Classification)
		decision.Reason = firstNonEmptyString(decision.Reason, "requested file format can run as bounded local artifact work")
		decision.UserFacingReply = ""
	}
	return decision
}

func canRunFileArtifactWork(request AgentRequest) bool {
	if request.ToolSet == nil {
		return false
	}
	return request.ToolSet.IsAllowed(TerminalRunToolName) &&
		request.ToolSet.IsAllowed(FileDeliverToolName)
}

func deterministicPriorTaskReferenceForRequest(request AgentRequest) PriorTaskReference {
	if !priorTaskContextHasContent(request.PriorTask) {
		return PriorTaskReferenceNone
	}
	if latestMessageAsksForPriorOutcomeRecovery(request.Prompt) {
		return PriorTaskReferenceOutcomeRecovery
	}
	return PriorTaskReferenceNone
}

func latestMessageAsksForPriorOutcomeRecovery(prompt string) bool {
	text := strings.ToLower(strings.TrimSpace(prompt))
	if text == "" {
		return false
	}
	return containsAny(text, []string{
		"전달", "첨부", "다시", "재시도", "이어", "계속", "수정", "고쳐", "개선", "더 예쁘", "더 낫",
		"deliver", "attach", "retry", "again", "continue", "revise", "update", "improve",
	})
}

func deterministicTaskShape(request AgentRequest, classification IntakeClassification) TaskShape {
	if classification == IntakeClassificationQuickReply {
		return TaskShapeImmediateReply
	}
	if classification == IntakeClassificationNeedsConfirmation {
		return TaskShapeApprovalGatedTask
	}
	return TaskShapeResearchTask
}

func deterministicInitialToolNamesForRequest(request AgentRequest, requestedOutputFormats []string) []string {
	toolNames := []string{}
	if len(requestedOutputFormats) > 0 {
		toolNames = appendUniqueStrings(toolNames, TerminalRunToolName, FileDeliverToolName, SkillSearchToolName)
	}
	return registeredToolNamesOnly(request.ToolSet, toolNames)
}

func deterministicInitialToolNamesForDecision(request AgentRequest, decision TurnDecision) []string {
	toolNames := deterministicInitialToolNamesForRequest(request, decision.RequestedOutputFormats)
	for _, toolName := range decision.RequiredEvidenceTools {
		toolNames = appendUniqueStrings(toolNames, callableToolNameForRequiredEvidence(request.ToolSet, toolName))
	}
	return registeredToolNamesOnly(request.ToolSet, toolNames)
}

func callableToolNameForRequiredEvidence(toolSet *ToolSet, toolName string) string {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" || toolSet == nil {
		return ""
	}
	if toolSet.CanExpose(trimmedToolName) {
		return trimmedToolName
	}
	if requiredEvidenceToolIsCapabilityOperation(toolSet, trimmedToolName) {
		return CapabilityInvokeToolName
	}
	return ""
}

func normalizeTaskShape(taskShape TaskShape) TaskShape {
	switch taskShape {
	case TaskShapeImmediateReply, TaskShapeResearchTask, TaskShapeMaintenanceTask, TaskShapeScheduledTask, TaskShapeBrowserHandoffTask, TaskShapeApprovalGatedTask, TaskShapeCodingTask:
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
		return TurnRouteContinueTask
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

func normalizeTaskfulConsumeRoute(decision TurnDecision, request AgentRequest) TurnDecision {
	if decision.Route != TurnRouteConsume || !turnDecisionRequiresTaskLoop(decision) {
		return decision
	}
	decision.Route = deterministicTurnRoute(request)
	if decision.Route == "" || decision.Route == TurnRouteConsume {
		decision.Route = TurnRouteStartTask
	}
	return decision
}

func turnDecisionRequiresTaskLoop(decision TurnDecision) bool {
	if decision.Classification == IntakeClassificationBoundedTask {
		return true
	}
	if len(decision.RequiredEvidenceTools) > 0 || len(decision.InitialToolNames) > 0 || len(decision.ExpectedResults) > 0 || len(decision.RequestedOutputFormats) > 0 {
		return true
	}
	switch decision.TaskShape {
	case TaskShapeResearchTask, TaskShapeMaintenanceTask, TaskShapeScheduledTask, TaskShapeBrowserHandoffTask, TaskShapeApprovalGatedTask:
		return true
	default:
		return false
	}
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

func deterministicTaskShapeForAttachmentContinuation(classification IntakeClassification) TaskShape {
	if classification == IntakeClassificationQuickReply {
		return TaskShapeImmediateReply
	}
	if classification == IntakeClassificationNeedsConfirmation {
		return TaskShapeApprovalGatedTask
	}
	return TaskShapeResearchTask
}

func shouldTreatConfirmationAsBoundedLocalArtifact(request AgentRequest, decision IntakeDecision) bool {
	if decision.Classification != IntakeClassificationNeedsConfirmation {
		return false
	}
	if !hasAllTools(request.ToolSet, []string{TerminalRunToolName, FileDeliverToolName}) {
		return false
	}
	return hasArtifactOutputFormat(decision.RequestedOutputFormats)
}

func shouldTreatAsBoundedSitePrototype(request AgentRequest, decision IntakeDecision) bool {
	if decision.Classification != IntakeClassificationUnsupported && decision.Classification != IntakeClassificationNeedsConfirmation {
		return false
	}
	if !hasAllTools(request.ToolSet, []string{"site.create", "site.publish"}) {
		return false
	}
	return requiredEvidenceHasPrefix(decision.RequiredEvidenceTools, "site.")
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
		registeredToolName, isRegistered := requiredEvidenceRegisteredToolName(toolRegistry, trimmedToolName)
		if isRegistered && toolRegistry.CanExpose(registeredToolName) {
			registeredToolNames = appendUniqueStrings(registeredToolNames, registeredToolName)
		}
	}
	return registeredToolNames
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func minimumTaskLevelForRequest(request AgentRequest) TaskLevel {
	if looksLikeSyntheticConnectorVerificationProbe(strings.ToLower(strings.TrimSpace(request.Prompt))) {
		return TaskLevelXLow
	}
	if hasToolPrefix(request.ToolSet, "browser.") && !hasToolPrefix(request.ToolSet, "web.") {
		return TaskLevelMedium
	}
	if hasToolPrefix(request.ToolSet, "file.") || hasToolPrefix(request.ToolSet, "user.") {
		return TaskLevelLow
	}
	return TaskLevelXLow
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
	for _, toolDefinition := range toolRegistry.ListRegisteredToolDefinitions() {
		if strings.HasPrefix(strings.TrimSpace(toolDefinition.Name), prefix) {
			return true
		}
	}
	return false
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
