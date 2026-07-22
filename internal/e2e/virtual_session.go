package e2e

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/agenttest"
	"blueclaw/internal/capability"
	"blueclaw/internal/config"
	"blueclaw/internal/connectors"
	"blueclaw/internal/identity"
	"blueclaw/internal/llm"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
	"blueclaw/internal/security/actortest"
	"blueclaw/internal/skill"
	"blueclaw/internal/task"
	capabilitycatalog "blueclaw/protocol/generated"
)

type VirtualSessionScenario struct {
	Name                      string
	ProfileName               string
	ArtifactDirectoryPath     string
	LanguageModel             llm.LanguageModelProvider
	EmbeddingProvider         llm.EmbeddingProvider
	EmbeddingModel            string
	IntakeLanguageModel       llm.LanguageModelProvider
	LowLanguageModel          llm.LanguageModelProvider
	XLowLanguageModel         llm.LanguageModelProvider
	MediumLanguageModel       llm.LanguageModelProvider
	HighLanguageModel         llm.LanguageModelProvider
	XHighLanguageModel        llm.LanguageModelProvider
	MaxLanguageModel          llm.LanguageModelProvider
	CodingLanguageModel       llm.LanguageModelProvider
	DisableScriptedModel      bool
	UseLooseAssertions        bool
	FailOnLanguageModelError  bool
	SkillDirectoryPaths       []string
	Skills                    []agent.SkillInstruction
	AllowedTools              []string
	CapabilityToolNames       []string
	CapabilityToolDescriptors []agentruntime.CapabilityToolDescriptor
	InitialToolNames          []string
	InitialMemory             []memory.MemoryFact
	InitialSite               *VirtualSiteFixture
	RouterRequiredEvidence    []string
	RouterTaskShape           agent.TaskShape
	RouterTaskLevel           string
	CodingTierVisionFallback  bool
	AddressingResponse        string
	RouterSiteEvidence        string
	ScriptedExecutionPlan     *agent.ExecutionPlan
	ScriptedConfirmationReply string
	TurnOptions               agent.TurnOptions
	ProgressWriter            io.Writer
	Turns                     []VirtualTurn
}

type VirtualSiteFixture struct {
	SiteID      string
	Slug        string
	Title       string
	IsPublished bool
}

var virtualSiteSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

var virtualCanonicalMessageToolNames = []string{
	"message.context",
	"message.search",
	"message.send",
	"message.update",
	"message.delete",
	"channel.update",
}

var virtualGeneratedDescriptorToolNames = []string{
	"message.context",
	"message.search",
	"message.send",
	"message.update",
	"message.delete",
	"channel.update",
	"document.read",
	"image.read",
}

var virtualGeneratedResultContractToolNames = []string{
	"task.add",
	"task.list",
	"task.update",
	"task.delete",
	"calendar.add",
	"calendar.list",
	"calendar.update",
	"calendar.delete",
	"web.search",
	"site.create",
	"site.status",
	"site.preview",
	"site.publish",
	"site.delete",
}

var virtualCanonicalCapabilityToolDescriptorByName = mustLoadVirtualCanonicalCapabilityToolDescriptors()

func mustLoadVirtualCanonicalCapabilityToolDescriptors() map[string]agentruntime.CapabilityToolDescriptor {
	var catalog struct {
		Tools []agentruntime.CapabilityToolDescriptor `json:"tools"`
	}
	if errorValue := json.Unmarshal(capabilitycatalog.CapabilityToolCatalog(), &catalog); errorValue != nil {
		panic(errorValue)
	}
	descriptors := map[string]agentruntime.CapabilityToolDescriptor{}
	for _, descriptor := range catalog.Tools {
		descriptors[descriptor.Name] = descriptor
	}
	requiredToolNames := append([]string{}, virtualGeneratedDescriptorToolNames...)
	requiredToolNames = append(requiredToolNames, virtualGeneratedResultContractToolNames...)
	for _, toolName := range requiredToolNames {
		if _, isFound := descriptors[toolName]; !isFound {
			panic("generated capability descriptor is missing: " + toolName)
		}
	}
	return descriptors
}

type VirtualResponseExpectation string

const (
	VirtualResponseReply            VirtualResponseExpectation = "reply"
	VirtualResponseIgnore           VirtualResponseExpectation = "ignore"
	VirtualResponseIgnoreOrReact    VirtualResponseExpectation = "ignore_or_react"
	VirtualResponseReact            VirtualResponseExpectation = "react"
	VirtualResponseBackgroundAction VirtualResponseExpectation = "background_action"
)

type VirtualTurn struct {
	Prompt                    string
	ExpectedResponse          VirtualResponseExpectation
	ConversationType          string
	ChannelID                 string
	ChannelName               string
	ReplyTargetID             string
	Addressing                connectors.AddressingMetadata
	InputAttachments          []connectors.InputAttachment
	ContextMessages           []connectors.VisibleContextMessage
	ContextMaterials          []connectors.InputAttachment
	ActionResponses           []string
	CompletionJudgeResponses  []string
	RouterRequiredEvidence    []string
	RouterTaskShape           agent.TaskShape
	RouterSiteEvidence        string
	RouterApproval            string
	ExpectedSelectedSkills    []string
	ExpectedToolCalls         []string
	ExpectedAnyToolCalls      []string
	ExpectedEvents            []string
	ExpectedToolCallCounts    map[string]int
	ExpectedEventCounts       []VirtualEventCount
	ExpectedAttachments       []string
	ExpectedAttachmentFiles   []VirtualAttachmentFileExpectation
	ExpectedWorkspaceFiles    []VirtualWorkspaceFileExpectation
	ForbiddenWorkspaceFiles   []string
	ExpectedModelContexts     []string
	ForbiddenModelContexts    []string
	ExpectedReplyTargetID     string
	ExpectedReplyFragments    []string
	ForbiddenReplyFragments   []string
	MinimumReplyLength        int
	ExpectedSequence          []string
	ExpectedCheckpointReplies []string
	ForbiddenEvents           []string
	ExpectedTaskStatus        task.TaskStatus
}

type VirtualEventCount struct {
	Name         string
	BodyFragment string
	Count        int
}

type VirtualWorkspaceFileExpectation struct {
	PathGlob           string
	ContainsFragments  []string
	ForbiddenFragments []string
	FragmentCounts     map[string]int
}

type VirtualAttachmentFileExpectation struct {
	Suffix            string
	ContainsFragments []string
}

type VirtualSessionResult struct {
	ScenarioName          string
	ArtifactDirectoryPath string
	TurnResults           []VirtualTurnResult
	TaskSchedules         []task.TaskSchedule
}

type VirtualTurnResult struct {
	Handled                 bool
	Ignored                 bool
	Reason                  string
	DidReply                bool
	Reactions               []connectors.ReactionTarget
	TaskRunID               string
	TaskStatus              task.TaskStatus
	FailureReason           string
	FinishMessage           string
	ReplyTargetID           string
	Attachments             []agent.FileAttachment
	Events                  []task.TaskEvent
	LanguageModelCallEvents []VirtualLanguageModelCallEvent
	InformationalAssertions []VirtualInformationalAssertion
	ModelContext            string
	ModelImagePartCount     int
	UserModelImagePartCount int
}

type VirtualLanguageModelCallEvent struct {
	Kind                       string                          `json:"kind"`
	SchemaName                 string                          `json:"schemaName,omitempty"`
	Provider                   string                          `json:"provider,omitempty"`
	Model                      string                          `json:"model,omitempty"`
	SelectedBackend            string                          `json:"selectedBackend,omitempty"`
	FinishReason               string                          `json:"finishReason,omitempty"`
	LatencyMS                  int64                           `json:"latencyMs"`
	PromptBytes                int                             `json:"promptBytes"`
	ContentBytes               int                             `json:"contentBytes"`
	UsedFallback               bool                            `json:"usedFallback,omitempty"`
	PromptTokens               int64                           `json:"promptTokens,omitempty"`
	CompletionTokens           int64                           `json:"completionTokens,omitempty"`
	TotalTokens                int64                           `json:"totalTokens,omitempty"`
	IsError                    bool                            `json:"isError,omitempty"`
	Error                      string                          `json:"error,omitempty"`
	ResponseContent            string                          `json:"responseContent,omitempty"`
	ToolCalls                  []llm.ChatCompletionToolCall    `json:"toolCalls,omitempty"`
	IsDeadlineExceeded         bool                            `json:"isDeadlineExceeded,omitempty"`
	WasCorrected               bool                            `json:"wasCorrected,omitempty"`
	StructuredOutputCorrection *llm.StructuredOutputCorrection `json:"-"`
}

type VirtualInformationalAssertion struct {
	Name      string `json:"name"`
	Satisfied bool   `json:"satisfied"`
	Detail    string `json:"detail"`
}

type VirtualSessionHarness struct {
	scenario         VirtualSessionScenario
	artifactPath     string
	workspacePath    string
	scriptedModel    *agenttest.ScriptedLanguageModel
	requestRecorder  virtualLanguageModelRequestRecorder
	callRecorder     virtualLanguageModelCallRecorder
	taskRunService   *task.TaskRunService
	taskEventService *task.TaskEventService
	scheduleStore    *virtualTaskScheduleRepository
	memoryStore      *virtualMemoryStore
	runtime          *connectors.ConnectorRuntime
	adapter          *virtualAdapter
	cleanup          func()
}

type virtualLanguageModelRequestRecorder interface {
	RequestCount() int
	RequestsSince(int) []llm.StructuredResponseRequest
}

type virtualLanguageModelCallRecorder interface {
	CallCount() int
	CallsSince(int) []VirtualLanguageModelCallEvent
}

type virtualObservedLanguageModel struct {
	provider llm.LanguageModelProvider
	store    *virtualLanguageModelObservationStore
}

type virtualLanguageModelObservationStore struct {
	mutex    sync.Mutex
	requests []llm.StructuredResponseRequest
	calls    []VirtualLanguageModelCallEvent
}

type virtualObservedRecoveryLanguageModel struct {
	*virtualObservedLanguageModel
}

type virtualObservedRemoteRecoveryLanguageModel struct {
	*virtualObservedLanguageModel
}

type virtualObservedLocalRecoveryLanguageModel struct {
	*virtualObservedLanguageModel
}

func newVirtualObservedLanguageModel(provider llm.LanguageModelProvider) llm.LanguageModelProvider {
	return newVirtualObservedLanguageModelWithStore(provider, &virtualLanguageModelObservationStore{})
}

func newVirtualObservedLanguageModelWithStore(provider llm.LanguageModelProvider, store *virtualLanguageModelObservationStore) llm.LanguageModelProvider {
	base := &virtualObservedLanguageModel{provider: provider, store: store}
	_, hasRecovery := provider.(llm.RecoveryResponder)
	_, hasLocalRecovery := provider.(llm.LocalRecoveryResponder)
	switch {
	case hasRecovery && hasLocalRecovery:
		return virtualObservedRecoveryLanguageModel{base}
	case hasRecovery:
		return virtualObservedRemoteRecoveryLanguageModel{base}
	case hasLocalRecovery:
		return virtualObservedLocalRecoveryLanguageModel{base}
	default:
		return base
	}
}

func (languageModel *virtualObservedLanguageModel) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	startedAt := time.Now()
	reply, errorValue := languageModel.provider.GenerateResponse(ctx, prompt)
	languageModel.appendCall(virtualTextCallEvent("text", prompt, reply, startedAt, errorValue))
	return reply, errorValue
}

func (languageModel *virtualObservedLanguageModel) GenerateStructuredResponse(ctx context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.appendRequest(request)
	startedAt := time.Now()
	response, errorValue := languageModel.provider.GenerateStructuredResponse(ctx, request)
	languageModel.appendCall(virtualStructuredCallEvent(request, response, startedAt, errorValue))
	return response, errorValue
}

func (languageModel *virtualObservedLanguageModel) TextChatCompleter() (llm.ChatCompleter, bool) {
	completer, isAvailable := llm.ResolveTextChatCompleter(languageModel.provider)
	if !isAvailable {
		return nil, false
	}
	return virtualObservedChatCompleter{languageModel: languageModel, delegate: completer}, true
}

func (languageModel *virtualObservedLanguageModel) RecoveryChatCompleter() (llm.RecoveryChatCompleter, bool) {
	completer, isAvailable := llm.ResolveRecoveryChatCompleter(languageModel.provider)
	if !isAvailable {
		return nil, false
	}
	return virtualObservedRecoveryChatCompleter{languageModel: languageModel, delegate: completer}, true
}

func (languageModel *virtualObservedLanguageModel) LocalRecoveryChatCompleter() (llm.LocalRecoveryChatCompleter, bool) {
	completer, isAvailable := llm.ResolveLocalRecoveryChatCompleter(languageModel.provider)
	if !isAvailable {
		return nil, false
	}
	return virtualObservedLocalRecoveryChatCompleter{languageModel: languageModel, delegate: completer}, true
}

type virtualObservedChatCompleter struct {
	languageModel *virtualObservedLanguageModel
	delegate      llm.ChatCompleter
}

type virtualObservedRecoveryChatCompleter struct {
	languageModel *virtualObservedLanguageModel
	delegate      llm.RecoveryChatCompleter
}

type virtualObservedLocalRecoveryChatCompleter struct {
	languageModel *virtualObservedLanguageModel
	delegate      llm.LocalRecoveryChatCompleter
}

func (completer virtualObservedChatCompleter) GenerateChatCompletion(ctx context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	completer.languageModel.appendRequest(virtualStructuredRequestFromChat(request))
	startedAt := time.Now()
	response, errorValue := completer.delegate.GenerateChatCompletion(ctx, request)
	completer.languageModel.appendCall(virtualChatCallEvent("chat", request, response, startedAt, errorValue))
	return response, errorValue
}

func virtualStructuredRequestFromChat(request llm.ChatCompletionRequest) llm.StructuredResponseRequest {
	messages := make([]llm.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		messages = append(messages, llm.Message{Role: message.Role, Content: message.Content})
	}
	tools, _ := json.Marshal(request.Tools)
	return llm.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               request.SchemaName,
			Document:           string(tools),
			IsStrictlyEnforced: true,
		},
		GenerationOptions: request.GenerationOptions,
	}
}

func (completer virtualObservedRecoveryChatCompleter) GenerateRecoveryChatCompletion(ctx context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	startedAt := time.Now()
	response, errorValue := completer.delegate.GenerateRecoveryChatCompletion(ctx, request)
	completer.languageModel.appendCall(virtualChatCallEvent("recovery_chat", request, response, startedAt, errorValue))
	return response, errorValue
}

func (completer virtualObservedLocalRecoveryChatCompleter) GenerateLocalRecoveryChatCompletion(ctx context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	startedAt := time.Now()
	response, errorValue := completer.delegate.GenerateLocalRecoveryChatCompletion(ctx, request)
	completer.languageModel.appendCall(virtualChatCallEvent("local_recovery_chat", request, response, startedAt, errorValue))
	return response, errorValue
}

func (languageModel *virtualObservedRecoveryLanguageModel) GenerateRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return languageModel.recoveryResponse(ctx, prompt)
}

func (languageModel *virtualObservedRecoveryLanguageModel) GenerateLocalRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return languageModel.localRecoveryResponse(ctx, prompt)
}

func (languageModel *virtualObservedRemoteRecoveryLanguageModel) GenerateRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return languageModel.recoveryResponse(ctx, prompt)
}

func (languageModel *virtualObservedLocalRecoveryLanguageModel) GenerateLocalRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return languageModel.localRecoveryResponse(ctx, prompt)
}

func (languageModel *virtualObservedLanguageModel) recoveryResponse(ctx context.Context, prompt string) (string, error) {
	recoveryProvider, isRecoveryProvider := languageModel.provider.(llm.RecoveryResponder)
	if !isRecoveryProvider {
		return languageModel.GenerateResponse(ctx, prompt)
	}
	startedAt := time.Now()
	reply, errorValue := recoveryProvider.GenerateRecoveryResponse(ctx, prompt)
	languageModel.appendCall(virtualTextCallEvent("recovery_text", prompt, reply, startedAt, errorValue))
	return reply, errorValue
}

func (languageModel *virtualObservedLanguageModel) localRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	localRecoveryProvider, isLocalRecoveryProvider := languageModel.provider.(llm.LocalRecoveryResponder)
	if !isLocalRecoveryProvider {
		return languageModel.GenerateResponse(ctx, prompt)
	}
	startedAt := time.Now()
	reply, errorValue := localRecoveryProvider.GenerateLocalRecoveryResponse(ctx, prompt)
	languageModel.appendCall(virtualTextCallEvent("local_recovery_text", prompt, reply, startedAt, errorValue))
	return reply, errorValue
}

func (languageModel *virtualObservedLanguageModel) RequestCount() int {
	languageModel.store.mutex.Lock()
	defer languageModel.store.mutex.Unlock()
	return len(languageModel.store.requests)
}

func (languageModel *virtualObservedLanguageModel) RequestsSince(startIndex int) []llm.StructuredResponseRequest {
	languageModel.store.mutex.Lock()
	defer languageModel.store.mutex.Unlock()
	if startIndex < 0 || startIndex > len(languageModel.store.requests) {
		startIndex = 0
	}
	return append([]llm.StructuredResponseRequest{}, languageModel.store.requests[startIndex:]...)
}

func (languageModel *virtualObservedLanguageModel) CallCount() int {
	languageModel.store.mutex.Lock()
	defer languageModel.store.mutex.Unlock()
	return len(languageModel.store.calls)
}

func (languageModel *virtualObservedLanguageModel) CallsSince(startIndex int) []VirtualLanguageModelCallEvent {
	languageModel.store.mutex.Lock()
	defer languageModel.store.mutex.Unlock()
	if startIndex < 0 || startIndex > len(languageModel.store.calls) {
		startIndex = 0
	}
	return append([]VirtualLanguageModelCallEvent{}, languageModel.store.calls[startIndex:]...)
}

func (languageModel *virtualObservedLanguageModel) appendRequest(request llm.StructuredResponseRequest) {
	languageModel.store.mutex.Lock()
	defer languageModel.store.mutex.Unlock()
	languageModel.store.requests = append(languageModel.store.requests, request)
}

func (languageModel *virtualObservedLanguageModel) appendCall(callEvent VirtualLanguageModelCallEvent) {
	languageModel.store.mutex.Lock()
	defer languageModel.store.mutex.Unlock()
	markCorrectedVirtualCalls(languageModel.store.calls, callEvent)
	languageModel.store.calls = append(languageModel.store.calls, callEvent)
}

func markCorrectedVirtualCalls(calls []VirtualLanguageModelCallEvent, successfulCall VirtualLanguageModelCallEvent) {
	if successfulCall.IsError {
		return
	}
	for index := len(calls) - 1; index >= 0; index-- {
		if !virtualCallCorrectsError(calls[index], successfulCall) {
			return
		}
		calls[index].WasCorrected = true
	}
}

func virtualCallCorrectsError(errorCall VirtualLanguageModelCallEvent, successfulCall VirtualLanguageModelCallEvent) bool {
	if !errorCall.IsError || errorCall.StructuredOutputCorrection == nil {
		return false
	}
	if errorCall.SchemaName == "" || errorCall.Kind != successfulCall.Kind || errorCall.SchemaName != successfulCall.SchemaName {
		return false
	}
	return errorCall.Kind != "chat" || successfulCall.FinishReason == "tool_calls"
}

type imageRejectingLanguageModel struct {
	delegate llm.LanguageModelProvider
}

func (model imageRejectingLanguageModel) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	return model.delegate.GenerateResponse(responseContext, prompt)
}

func (model imageRejectingLanguageModel) GenerateStructuredResponse(responseContext context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if part.Type == "image" {
				return llm.StructuredResponse{}, errors.New("text-only coding model received an image part")
			}
		}
	}
	return model.delegate.GenerateStructuredResponse(responseContext, request)
}

func virtualStructuredCallEvent(request llm.StructuredResponseRequest, response llm.StructuredResponse, startedAt time.Time, errorValue error) VirtualLanguageModelCallEvent {
	callEvent := VirtualLanguageModelCallEvent{
		Kind:             "structured",
		SchemaName:       strings.TrimSpace(request.StructuredOutputSchema.Name),
		Provider:         response.ProviderName,
		Model:            response.ModelName,
		ResponseContent:  virtualTruncatedCallContent(response.Content),
		LatencyMS:        time.Since(startedAt).Milliseconds(),
		PromptBytes:      virtualStructuredRequestByteCount(request),
		ContentBytes:     len(response.Content),
		UsedFallback:     response.UsedFallback,
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		TotalTokens:      response.Usage.TotalTokens,
	}
	if errorValue != nil {
		callEvent.IsError = true
		callEvent.Error = virtualTruncatedCallError(errorValue)
		callEvent.IsDeadlineExceeded = errors.Is(errorValue, context.DeadlineExceeded)
		callEvent.StructuredOutputCorrection = virtualStructuredOutputCorrection(errorValue)
	}
	return callEvent
}

func virtualChatCallEvent(kind string, request llm.ChatCompletionRequest, response llm.ChatCompletionResponse, startedAt time.Time, errorValue error) VirtualLanguageModelCallEvent {
	callEvent := VirtualLanguageModelCallEvent{
		Kind:            kind,
		SchemaName:      virtualChatRequestSchemaName(request),
		Provider:        response.ProviderName,
		Model:           response.ModelName,
		SelectedBackend: response.SelectedBackend,
		FinishReason:    response.FinishReason,
		ResponseContent: virtualTruncatedCallContent(response.Message.Content),
		ToolCalls:       append([]llm.ChatCompletionToolCall{}, response.Message.ToolCalls...),
		LatencyMS:       time.Since(startedAt).Milliseconds(),
		PromptBytes:     virtualChatRequestByteCount(request),
		ContentBytes:    len(response.Message.Content),
		UsedFallback:    response.UsedFallback,
	}
	if errorValue != nil {
		callEvent.IsError = true
		callEvent.Error = virtualTruncatedCallError(errorValue)
		callEvent.IsDeadlineExceeded = errors.Is(errorValue, context.DeadlineExceeded)
		callEvent.StructuredOutputCorrection = virtualStructuredOutputCorrection(errorValue)
	}
	return callEvent
}

func virtualStructuredOutputCorrection(errorValue error) *llm.StructuredOutputCorrection {
	correction, isCorrectable := llm.StructuredOutputCorrectionFromError(errorValue)
	if !isCorrectable {
		return nil
	}
	return &correction
}

func virtualChatRequestSchemaName(request llm.ChatCompletionRequest) string {
	return strings.TrimSpace(request.SchemaName)
}

func virtualTextCallEvent(kind string, prompt string, reply string, startedAt time.Time, errorValue error) VirtualLanguageModelCallEvent {
	callEvent := VirtualLanguageModelCallEvent{
		Kind:            kind,
		LatencyMS:       time.Since(startedAt).Milliseconds(),
		PromptBytes:     len(prompt),
		ContentBytes:    len(reply),
		ResponseContent: virtualTruncatedCallContent(reply),
	}
	if errorValue != nil {
		callEvent.IsError = true
		callEvent.Error = virtualTruncatedCallError(errorValue)
	}
	return callEvent
}

func virtualStructuredRequestByteCount(request llm.StructuredResponseRequest) int {
	byteCount := 0
	for _, message := range request.Messages {
		byteCount += len(message.Content)
		for _, part := range message.Parts {
			byteCount += len(part.Text) + len(part.DataBase64)
		}
	}
	return byteCount
}

func virtualChatRequestByteCount(request llm.ChatCompletionRequest) int {
	byteCount := 0
	for _, message := range request.Messages {
		byteCount += len(message.Content)
	}
	return byteCount
}

func virtualTruncatedCallError(errorValue error) string {
	if errorValue == nil {
		return ""
	}
	errorText := strings.Join(strings.Fields(errorValue.Error()), " ")
	if len([]rune(errorText)) <= 300 {
		return errorText
	}
	return string([]rune(errorText)[:300])
}

func virtualTruncatedCallContent(content string) string {
	const maximumVirtualCallContentBytes = 64 * 1024
	if len(content) <= maximumVirtualCallContentBytes {
		return content
	}
	return content[:maximumVirtualCallContentBytes]
}

func BuiltinScenario(name string, artifactDirectoryPath string) (VirtualSessionScenario, error) {
	switch strings.TrimSpace(name) {
	case "", "presentation", "presentation_local_multiturn_success":
		return PresentationLocalMultiturnSuccessScenario(artifactDirectoryPath), nil
	case "memory", "memory_guided_followup":
		return MemoryGuidedFollowupScenario(artifactDirectoryPath), nil
	case "plain_question_acceptance":
		return PlainQuestionAcceptanceScenario(artifactDirectoryPath), nil
	case "web_search_acceptance":
		return WebSearchAcceptanceScenario(artifactDirectoryPath), nil
	case "tool_permission_hides_skill":
		return ToolPermissionHidesSkillScenario(artifactDirectoryPath), nil
	case "file_write_acceptance":
		return FileWriteAcceptanceScenario(artifactDirectoryPath), nil
	case "document_create_acceptance":
		return DocumentCreateAcceptanceScenario(artifactDirectoryPath), nil
	case "gws_disabled":
		return GWSDisabledScenario(artifactDirectoryPath), nil
	case "schedule_create_acceptance":
		return ScheduleCreateAcceptanceScenario(artifactDirectoryPath), nil
	case "schedule_lifecycle_acceptance":
		return ScheduleLifecycleAcceptanceScenario(artifactDirectoryPath), nil
	case "calendar_event_lifecycle_acceptance":
		return CalendarEventLifecycleAcceptanceScenario(artifactDirectoryPath), nil
	case "calendar_false_finish_recovery_acceptance":
		return CalendarFalseFinishRecoveryAcceptanceScenario(artifactDirectoryPath), nil
	case "ambient_duty_calendar_acceptance":
		return AmbientDutyCalendarAcceptanceScenario(artifactDirectoryPath), nil
	case "ambient_task_capture_acceptance":
		return AmbientTaskCaptureAcceptanceScenario(artifactDirectoryPath), nil
	case "skill_lifecycle_acceptance":
		return SkillLifecycleAcceptanceScenario(artifactDirectoryPath), nil
	case "capability_question_acceptance":
		return CapabilityQuestionAcceptanceScenario(artifactDirectoryPath), nil
	case "task_history_question_acceptance":
		return TaskHistoryQuestionAcceptanceScenario(artifactDirectoryPath), nil
	case "memory_explicit_tool_acceptance":
		return MemoryExplicitToolAcceptanceScenario(artifactDirectoryPath), nil
	case "failure_explanation_acceptance":
		return FailureExplanationAcceptanceScenario(artifactDirectoryPath), nil
	case "one_time_schedule_acceptance":
		return OneTimeScheduleAcceptanceScenario(artifactDirectoryPath), nil
	case "site_artifact_acceptance":
		return SitePrototypeAcceptanceScenario(artifactDirectoryPath), nil
	case "site_edit_redeploy_acceptance":
		return SiteEditRedeployAcceptanceScenario(artifactDirectoryPath), nil
	case "site_custom_structure_acceptance":
		return SiteCustomStructureAcceptanceScenario(artifactDirectoryPath), nil
	case "site_lifecycle_acceptance":
		return SiteLifecycleAcceptanceScenario(artifactDirectoryPath), nil
	case "ask_choice_reply_acceptance":
		return AskChoiceReplyAcceptanceScenario(artifactDirectoryPath), nil
	case "dm_send_confirm_acceptance":
		return DirectMessageSendConfirmAcceptanceScenario(artifactDirectoryPath), nil
	case "channel_post_acceptance":
		return ChannelPostAcceptanceScenario(artifactDirectoryPath), nil
	case "platform_message_edit_acceptance":
		return PlatformMessageEditAcceptanceScenario(artifactDirectoryPath), nil
	case "attachment_material_read":
		return AttachmentMaterialReadScenario(artifactDirectoryPath), nil
	case "attachment_html_preview_recovery":
		return AttachmentHTMLPreviewRecoveryScenario(artifactDirectoryPath), nil
	case "attachment_html_previous_preview_recovery":
		return AttachmentHTMLPreviousPreviewRecoveryScenario(artifactDirectoryPath), nil
	case "attachment_current_image_input":
		return AttachmentCurrentImageInputScenario(artifactDirectoryPath), nil
	case "coding_image_vision_fallback":
		return CodingImageVisionFallbackScenario(artifactDirectoryPath), nil
	default:
		return VirtualSessionScenario{}, fmt.Errorf("unknown virtual session scenario: %s", name)
	}
}

func RunVirtualSession(ctx context.Context, scenario VirtualSessionScenario) (VirtualSessionResult, error) {
	harness, errorValue := NewVirtualSessionHarness(scenario)
	if errorValue != nil {
		return VirtualSessionResult{}, errorValue
	}
	return harness.Run(ctx)
}

func NewVirtualSessionHarness(scenario VirtualSessionScenario) (*VirtualSessionHarness, error) {
	if strings.TrimSpace(scenario.Name) == "" {
		return nil, errors.New("scenario name is required")
	}
	artifactPath, errorValue := prepareArtifactDirectory(scenario)
	if errorValue != nil {
		return nil, errorValue
	}
	workspacePath := filepath.Join(artifactPath, "workspace")
	if errorValue := os.MkdirAll(workspacePath, 0700); errorValue != nil {
		return nil, errorValue
	}
	if errorValue := materializeVirtualCapabilityCLI(workspacePath); errorValue != nil {
		return nil, errorValue
	}

	skillInstructions, errorValue := loadVirtualSkillInstructions(scenario, workspacePath)
	if errorValue != nil {
		return nil, errorValue
	}

	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskStepService := task.NewTaskStepService()
	taskArtifactService := task.NewTaskArtifactService()
	scriptedModel := actionScriptedLanguageModelForScenario(scenario)
	baseLanguageModel := scenario.LanguageModel
	if scriptedModel != nil {
		baseLanguageModel = scriptedModel
	}
	if baseLanguageModel == nil {
		return nil, errors.New("virtual session requires a live language model or explicit scripted model responses")
	}
	observationStore := &virtualLanguageModelObservationStore{}
	languageModel := newVirtualObservedLanguageModelWithStore(baseLanguageModel, observationStore)
	lowLanguageModel := observedVirtualLanguageModelOrDefault(scenario.LowLanguageModel, languageModel, observationStore)
	xLowLanguageModel := observedVirtualLanguageModelOrDefault(scenario.XLowLanguageModel, languageModel, observationStore)
	mediumLanguageModel := observedVirtualLanguageModelOrDefault(scenario.MediumLanguageModel, languageModel, observationStore)
	highLanguageModel := observedVirtualLanguageModelOrDefault(scenario.HighLanguageModel, languageModel, observationStore)
	xHighLanguageModel := observedVirtualLanguageModelOrDefault(scenario.XHighLanguageModel, languageModel, observationStore)
	maxLanguageModel := observedVirtualLanguageModelOrDefault(scenario.MaxLanguageModel, languageModel, observationStore)
	codingLanguageModel := observedVirtualLanguageModelOrDefault(scenario.CodingLanguageModel, languageModel, observationStore)
	intakeLanguageModel := observedVirtualLanguageModelOrDefault(scenario.IntakeLanguageModel, languageModel, observationStore)
	agentKernel := agent.NewAgentKernel(taskRunService, taskStepService)
	agentKernel.UseTaskArtifactService(taskArtifactService)
	agentKernel.UseLanguageModelProvider(lowLanguageModel)
	agentKernel.UseTaskTierLanguageModels(maxLanguageModel, xHighLanguageModel, highLanguageModel, mediumLanguageModel, xLowLanguageModel, codingLanguageModel)
	if scenario.CodingTierVisionFallback && scenario.CodingLanguageModel == nil {
		codingTaskLanguageModel := llm.VisionFallbackProvider{
			TextOnlyModel: imageRejectingLanguageModel{delegate: languageModel},
			VisionModel:   languageModel,
		}
		agentKernel.UseTaskTierLanguageModels(languageModel, languageModel, languageModel, languageModel, languageModel, codingTaskLanguageModel)
	}
	agentKernel.UseIntakeLanguageModelProvider(intakeLanguageModel)
	agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true, DefaultTaskLevel: agent.TaskLevelLow})
	agentKernel.UseTurnOptions(virtualTurnOptions(scenario.TurnOptions))
	instructionBundleLoader := virtualInstructionBundleLoader(skillInstructions, workspacePath)
	skillRetriever := agent.NewEmbeddingSkillRetriever(scenario.EmbeddingProvider, "")
	skillRetriever.EmbeddingModel = scenario.EmbeddingModel
	agentKernel.UseInstructionBundleLoader(instructionBundleLoader)
	agentKernel.UseSkillRetriever(skillRetriever)

	identityService := identity.NewIdentityService(testPolicyProjection())
	runtime := connectors.NewConnectorRuntime(identityService, agentKernel, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	adapter := &virtualAdapter{workspacePath: workspacePath}
	runtime.RegisterAdapter(adapter)
	runtime.UseWorkspaceID("e2e")
	runtime.UseWorkspaceRootPath(workspacePath)
	runtime.UseAllowedToolNames(allowedToolsOrDefault(scenario.AllowedTools))
	terminalService := security.NewTerminalSessionService(terminalConfiguration(workspacePath))
	runtime.UseTerminalService(terminalService)
	runtime.UseWorkspaceActorFactory(actortest.NewDirectWorkspaceActorFactory(terminalService))
	runtime.UseTaskRunService(taskRunService)
	scheduleStore := &virtualTaskScheduleRepository{}
	runtime.UseTaskScheduleRepository(scheduleStore)
	cleanup := func() {}
	var capabilityClient capability.Client
	capabilityToolNames := virtualCapabilityToolNames(scenario)
	if len(capabilityToolNames) > 0 {
		var capabilityCleanup func()
		var errorValue error
		capabilityClient, capabilityCleanup, errorValue = startVirtualCapabilityServer(capabilityToolNames, workspacePath, scenario.InitialSite)
		if errorValue != nil {
			return nil, errorValue
		}
		runtime.UseCapabilityToolDescriptors(capabilityClient, virtualCapabilityToolDescriptors(scenario))
		cleanup = capabilityCleanup
	}

	memoryStore := newVirtualMemoryStore(scenario.InitialMemory)
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(memoryStore)
	runtime.UseMemoryService(memoryService)
	toolCatalogBuilder := virtualToolCatalogBuilder(
		scenario,
		workspacePath,
		taskRunService,
		scheduleStore,
		terminalService,
		memoryService,
		capabilityClient,
		skillRetriever,
		instructionBundleLoader,
		agentKernel,
	)
	virtualTaskLauncher := agentruntime.NewTaskLauncher(agentKernel, toolCatalogBuilder)
	virtualTaskLauncher.UseRequesterEmailResolver(identityService)
	runtime.UseTaskLauncher(virtualTaskLauncher)

	return &VirtualSessionHarness{
		scenario:         scenario,
		artifactPath:     artifactPath,
		workspacePath:    workspacePath,
		scriptedModel:    scriptedModel,
		requestRecorder:  virtualRequestRecorder(languageModel),
		callRecorder:     virtualCallRecorder(languageModel),
		taskRunService:   taskRunService,
		taskEventService: taskEventService,
		scheduleStore:    scheduleStore,
		memoryStore:      memoryStore,
		runtime:          runtime,
		adapter:          adapter,
		cleanup:          cleanup,
	}, nil
}

func observedVirtualLanguageModelOrDefault(provider llm.LanguageModelProvider, defaultProvider llm.LanguageModelProvider, store *virtualLanguageModelObservationStore) llm.LanguageModelProvider {
	if provider == nil {
		return defaultProvider
	}
	return newVirtualObservedLanguageModelWithStore(provider, store)
}

func virtualTurnOptions(scenarioOptions agent.TurnOptions) agent.TurnOptions {
	taskLevelProfile := agent.TaskLevelProfileForLevel(scenarioOptions.TaskLevel)
	turnOptions := agent.TurnOptions{
		TaskLevel:         taskLevelProfile.TaskLevel,
		MaxIterationCount: taskLevelProfile.MaxIterationCount,
		MaxToolCallCount:  taskLevelProfile.MaxToolCallCount,
		MaxElapsedSecond:  int(taskLevelProfile.Duration.Seconds()),
	}
	if scenarioOptions.MaxIterationCount > 0 {
		turnOptions.MaxIterationCount = scenarioOptions.MaxIterationCount
	}
	if scenarioOptions.MaxToolCallCount > 0 {
		turnOptions.MaxToolCallCount = scenarioOptions.MaxToolCallCount
	}
	if scenarioOptions.MaxElapsedSecond > 0 {
		turnOptions.MaxElapsedSecond = scenarioOptions.MaxElapsedSecond
	}
	if scenarioOptions.RecoveryAttemptLimit != 0 {
		turnOptions.RecoveryAttemptLimit = scenarioOptions.RecoveryAttemptLimit
	}
	if scenarioOptions.RecoveryBudget != (agent.RecoveryBudget{}) {
		turnOptions.RecoveryBudget = scenarioOptions.RecoveryBudget
	}
	return turnOptions
}

func virtualInstructionBundleLoader(baseSkillInstructions []agent.SkillInstruction, workspacePath string) func() agent.InstructionBundle {
	return func() agent.InstructionBundle {
		skillInstructions := append([]agent.SkillInstruction{}, baseSkillInstructions...)
		skillInstructions = append(skillInstructions, loadVirtualUserManagedSkills(workspacePath)...)
		return agent.InstructionBundle{Skills: skillInstructions}
	}
}

func loadVirtualUserManagedSkills(workspacePath string) []agent.SkillInstruction {
	userManagedSkillRootPath := filepath.Join(workspacePath, ".agents", "skills")
	entries, errorValue := os.ReadDir(userManagedSkillRootPath)
	if errorValue != nil {
		return nil
	}
	skillInstructions := []agent.SkillInstruction{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillBundle, errorValue := (skill.SkillLoader{}).LoadSkillBundle(filepath.Join(userManagedSkillRootPath, entry.Name()))
		if errorValue != nil {
			continue
		}
		skillInstructions = append(skillInstructions, skillInstructionFromBundle(skillBundle))
	}
	return skillInstructions
}

func virtualToolCatalogBuilder(
	scenario VirtualSessionScenario,
	workspacePath string,
	taskRunService *task.TaskRunService,
	scheduleStore *virtualTaskScheduleRepository,
	terminalService *security.TerminalSessionService,
	memoryService *memory.MemoryService,
	capabilityClient capability.Client,
	skillRetriever agent.SkillRetriever,
	instructionBundleLoader func() agent.InstructionBundle,
	agentKernel *agent.AgentKernel,
) *agentruntime.ToolCatalogBuilder {
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, allowedToolsOrDefault(scenario.AllowedTools))
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseTerminalService(terminalService)
	toolCatalogBuilder.UseWorkspaceActorFactory(actortest.NewDirectWorkspaceActorFactory(terminalService))
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseTaskScheduleRepository(scheduleStore)
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UseMemoryUpdateQueue(virtualMemoryUpdateQueue{memoryService: memoryService})
	toolCatalogBuilder.UseSkillSearch(skillRetriever, instructionBundleLoader)
	toolCatalogBuilder.UseSkillChangeHandler(func(contextValue context.Context) {
		agentKernel.RefreshSkillIndex(contextValue, instructionBundleLoader())
	})
	if len(scenario.CapabilityToolNames) > 0 || len(scenario.CapabilityToolDescriptors) > 0 {
		toolCatalogBuilder.UseCapabilityToolDescriptors(capabilityClient, virtualCapabilityToolDescriptors(scenario))
	}
	return toolCatalogBuilder
}

func virtualCapabilityToolNames(scenario VirtualSessionScenario) []string {
	toolNameByName := map[string]bool{}
	toolNames := []string{}
	addToolName := func(toolName string) {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" || toolNameByName[trimmedToolName] {
			return
		}
		toolNameByName[trimmedToolName] = true
		toolNames = append(toolNames, trimmedToolName)
	}
	for _, toolName := range scenario.CapabilityToolNames {
		addToolName(toolName)
	}
	for _, toolDescriptor := range scenario.CapabilityToolDescriptors {
		addToolName(toolDescriptor.Name)
	}
	return toolNames
}

func virtualCapabilityToolDescriptors(scenario VirtualSessionScenario) []agentruntime.CapabilityToolDescriptor {
	descriptorByName := map[string]agentruntime.CapabilityToolDescriptor{}
	for _, descriptor := range scenario.CapabilityToolDescriptors {
		descriptorByName[strings.TrimSpace(descriptor.Name)] = descriptor
	}
	descriptors := []agentruntime.CapabilityToolDescriptor{}
	for _, toolName := range virtualCapabilityToolNames(scenario) {
		descriptor := virtualCapabilityToolDescriptor(toolName)
		if configuredDescriptor, isFound := descriptorByName[toolName]; isFound {
			descriptor = mergeVirtualCapabilityToolDescriptor(descriptor, configuredDescriptor)
		}
		descriptors = append(descriptors, descriptor)
	}
	return descriptors
}

func virtualCapabilityToolDescriptor(toolName string) agentruntime.CapabilityToolDescriptor {
	if descriptor, isFound := virtualGeneratedToolDescriptor(toolName); isFound {
		return descriptor
	}
	sideEffectClass := virtualCapabilitySideEffectClass(toolName)
	descriptor := agentruntime.CapabilityToolDescriptor{
		Name:              toolName,
		CanonicalName:     toolName,
		Namespace:         virtualCapabilityNamespace(toolName),
		ModelName:         toolName,
		ModelVisibility:   agent.ToolVisibilityModel,
		Description:       "Virtual capability " + toolName,
		PrivacyClass:      "test",
		InputSchema:       json.RawMessage(virtualCapabilityInputSchema(toolName)),
		InputIntentSchema: virtualCapabilityInputIntentSchema(toolName),
		OutputSchema:      json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		ResultContract:    virtualCapabilityToolResultContract(toolName),
		PolicyResource:    "tool:" + toolName,
		SideEffectClass:   sideEffectClass,
		RequiresApproval:  toolName == "site.delete",
		Availability:      agentruntime.CapabilityAvailability{State: "ok"},
		Idempotency:       agentruntime.CapabilityIdempotency{Scope: "operation"},
	}
	descriptor.CompletionEvidence = virtualCapabilityCompletionEvidence(toolName, sideEffectClass)
	return descriptor
}

func virtualCanonicalCapabilityToolDescriptor(toolName string) (agentruntime.CapabilityToolDescriptor, bool) {
	descriptor, isFound := virtualCanonicalCapabilityToolDescriptorByName[toolName]
	return descriptor, isFound
}

func virtualGeneratedToolDescriptor(toolName string) (agentruntime.CapabilityToolDescriptor, bool) {
	if !slices.Contains(virtualGeneratedDescriptorToolNames, toolName) {
		return agentruntime.CapabilityToolDescriptor{}, false
	}
	return virtualCanonicalCapabilityToolDescriptor(toolName)
}

func virtualCapabilityCompletionEvidence(toolName string, sideEffectClass string) *agentruntime.CapabilityCompletionEvidence {
	siteActionByToolName := map[string]string{
		"site.create":  "create_site",
		"site.preview": "preview_site",
		"site.publish": "publish_site",
		"site.delete":  "delete_site",
	}
	if action := siteActionByToolName[toolName]; action != "" {
		return &agentruntime.CapabilityCompletionEvidence{Mode: "success", Action: action, TargetKind: "site"}
	}
	if sideEffectClass == agent.ToolSideEffectRead {
		return nil
	}
	return &agentruntime.CapabilityCompletionEvidence{Mode: "success", Action: toolName, TargetKind: virtualCapabilityNamespace(toolName)}
}

func mergeVirtualCapabilityToolDescriptor(base agentruntime.CapabilityToolDescriptor, override agentruntime.CapabilityToolDescriptor) agentruntime.CapabilityToolDescriptor {
	override.Name = base.Name
	override.CanonicalName = firstVirtualString(override.CanonicalName, base.CanonicalName)
	override.Namespace = firstVirtualString(override.Namespace, base.Namespace)
	override.ModelName = firstVirtualString(override.ModelName, base.ModelName)
	override.ModelVisibility = firstVirtualString(override.ModelVisibility, base.ModelVisibility)
	override.Description = firstVirtualString(override.Description, base.Description)
	override.PrivacyClass = firstVirtualString(override.PrivacyClass, base.PrivacyClass)
	override.InputSchema = firstVirtualSchema(override.InputSchema, base.InputSchema)
	override.InputIntentSchema = firstVirtualSchema(override.InputIntentSchema, base.InputIntentSchema)
	override.OutputSchema = firstVirtualSchema(override.OutputSchema, base.OutputSchema)
	if override.ResultContract == nil {
		override.ResultContract = base.ResultContract
	}
	override.PolicyResource = firstVirtualString(override.PolicyResource, base.PolicyResource)
	override.SideEffectClass = firstVirtualString(override.SideEffectClass, base.SideEffectClass)
	override.Availability.State = firstVirtualString(override.Availability.State, base.Availability.State)
	override.Idempotency.Scope = firstVirtualString(override.Idempotency.Scope, base.Idempotency.Scope)
	if override.CompletionEvidence == nil {
		override.CompletionEvidence = base.CompletionEvidence
	}
	return override
}

func virtualCapabilitySideEffectClass(toolName string) string {
	switch toolName {
	case "web.search", "image.read", "document.read", "task.list", "calendar.list", "site.status":
		return agent.ToolSideEffectRead
	case "task.delete", "calendar.delete", "schedule.cancel", "site.delete", "message.delete":
		return agent.ToolSideEffectDestructive
	case "message.send":
		return agent.ToolSideEffectExternalSend
	case "site.preview":
		return agent.ToolSideEffectExternalPublish
	case "site.publish":
		return agent.ToolSideEffectSitePublish
	default:
		return agent.ToolSideEffectWorkspaceWrite
	}
}

func virtualCapabilityNamespace(toolName string) string {
	if separator := strings.IndexByte(toolName, '.'); separator > 0 {
		return toolName[:separator]
	}
	return toolName
}

func firstVirtualString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstVirtualSchema(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func loadVirtualSkillInstructions(scenario VirtualSessionScenario, workspacePath string) ([]agent.SkillInstruction, error) {
	skillInstructions := append([]agent.SkillInstruction{}, scenario.Skills...)
	for _, sourceDirectoryPath := range scenario.SkillDirectoryPaths {
		trimmedSourceDirectoryPath := strings.TrimSpace(sourceDirectoryPath)
		if trimmedSourceDirectoryPath == "" {
			continue
		}
		destinationDirectoryPath := filepath.Join(workspacePath, "skills", filepath.Base(trimmedSourceDirectoryPath))
		if errorValue := copyDirectory(trimmedSourceDirectoryPath, destinationDirectoryPath); errorValue != nil {
			return nil, errorValue
		}
		skillBundle, errorValue := (skill.SkillLoader{}).LoadSkillBundle(destinationDirectoryPath)
		if errorValue != nil {
			return nil, errorValue
		}
		skillInstructions = append(skillInstructions, skillInstructionFromBundle(skillBundle))
	}
	return skillInstructions, nil
}

func skillInstructionFromBundle(skillBundle skill.SkillBundle) agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:           skillBundle.Name,
		Description:    skillBundle.Description,
		Prompt:         skillBundle.Instruction,
		ToolReferences: skillBundle.ReferencedToolNames(),
		Source: agent.InstructionSource{
			Path:      filepath.Join(skillBundle.DirectoryPath, "SKILL.md"),
			SkillName: skillBundle.Name,
			ByteSize:  fileSize(filepath.Join(skillBundle.DirectoryPath, "SKILL.md")),
			SHA256:    fileSHA256(filepath.Join(skillBundle.DirectoryPath, "SKILL.md")),
		},
	}
}

func copyDirectory(sourcePath string, destinationPath string) error {
	sourceInformation, errorValue := os.Stat(sourcePath)
	if errorValue != nil {
		return errorValue
	}
	if !sourceInformation.IsDir() {
		return errors.New("skill source path is not a directory: " + sourcePath)
	}
	return filepath.WalkDir(sourcePath, func(path string, directoryEntry os.DirEntry, walkError error) error {
		if walkError != nil {
			return walkError
		}
		relativePath, errorValue := filepath.Rel(sourcePath, path)
		if errorValue != nil {
			return errorValue
		}
		destination := filepath.Join(destinationPath, relativePath)
		if directoryEntry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		content, errorValue := os.ReadFile(path)
		if errorValue != nil {
			return errorValue
		}
		return os.WriteFile(destination, content, 0600)
	})
}

func fileSize(path string) int {
	information, errorValue := os.Stat(path)
	if errorValue != nil {
		return 0
	}
	return int(information.Size())
}

func fileSHA256(path string) string {
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return ""
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func materializeVirtualCapabilityCLI(workspacePath string) error {
	toolDirectoryPath := filepath.Join(workspacePath, "tools")
	if errorValue := os.MkdirAll(toolDirectoryPath, 0700); errorValue != nil {
		return errorValue
	}
	return os.WriteFile(filepath.Join(toolDirectoryPath, "capability"), []byte(virtualCapabilityCLIDocument), 0700)
}

const virtualCapabilityCLIDocument = `#!/usr/bin/env python3
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

def main():
    command, arguments = parse_arguments(sys.argv[1:])
    endpoint = bridge_endpoint()
    if command == "catalog":
        write_json(get_json(endpoint, "/v1/capabilities"))
        return
    if command == "list":
        for descriptor in sorted(descriptors(endpoint), key=lambda item: item.get("name", "")):
            name = str(descriptor.get("name", "")).strip()
            if name:
                print(name)
        return
    if command == "describe":
        require_argument(arguments, "tool name")
        descriptor = find_descriptor(endpoint, arguments[0])
        if descriptor is None:
            raise SystemExit("capability not found: " + arguments[0])
        write_json(descriptor)
        return
    if command == "invoke":
        require_argument(arguments, "tool name")
        write_json(post_json(endpoint, "/v1/tools/" + urllib.parse.quote(arguments[0], safe="") + "/invoke", invoke_request(arguments[0], arguments[1:])))
        return
    if command == "render":
        require_argument(arguments, "tool name")
        request = invoke_request(arguments[0], arguments[1:])
        request["render"] = True
        write_json(post_json(endpoint, "/v1/tools/" + urllib.parse.quote(arguments[0], safe="") + "/invoke", request))
        return
    raise SystemExit("unknown command: " + command)

def parse_arguments(arguments):
    if not arguments:
        raise SystemExit("usage: capability catalog|list|describe|invoke|render ...")
    return arguments[0], arguments[1:]

def bridge_endpoint():
    endpoint = os.environ.get("CAPABILITY_BRIDGE_URL", "").strip().rstrip("/")
    if not endpoint:
        raise SystemExit("CAPABILITY_BRIDGE_URL is not set")
    return endpoint

def require_argument(arguments, label):
    if not arguments or not arguments[0].strip():
        raise SystemExit(label + " is required")

def descriptors(endpoint):
    document = get_json(endpoint, "/v1/capabilities")
    values = []
    for key in ("capabilities", "deviceCapabilities", "companionCapabilities"):
        candidates = document.get(key)
        if isinstance(candidates, list):
            values.extend(item for item in candidates if isinstance(item, dict))
    return values

def find_descriptor(endpoint, tool_name):
    requested_name = tool_name.strip()
    for descriptor in descriptors(endpoint):
        if str(descriptor.get("name", "")).strip() == requested_name:
            return descriptor
    return None

def invoke_request(tool_name, arguments):
    if not arguments:
        input_document = {}
    else:
        input_document = parse_json_argument(" ".join(arguments))
    if isinstance(input_document, dict) and ("input" in input_document or "context" in input_document or "toolName" in input_document):
        request = dict(input_document)
        request["toolName"] = request.get("toolName") or tool_name
        return request
    return {"toolName": tool_name, "input": input_document}

def parse_json_argument(value):
    try:
        return json.loads(value)
    except json.JSONDecodeError as error:
        raise SystemExit("invalid JSON input: " + str(error)) from error

def get_json(endpoint, path):
    return request_json(endpoint + path, None)

def post_json(endpoint, path, document):
    return request_json(endpoint + path, document)

def request_json(url, document):
    data = None
    headers = {"Accept": "application/json"}
    if document is not None:
        data = json.dumps(document, ensure_ascii=False).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=data, headers=headers)
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as error:
        message = error.read().decode("utf-8", errors="replace").strip()
        raise SystemExit("capability bridge returned " + str(error.code) + ": " + message) from error
    except urllib.error.URLError as error:
        raise SystemExit("capability bridge unavailable: " + str(error.reason)) from error

def write_json(document):
    print(json.dumps(document, ensure_ascii=False, indent=2, sort_keys=True))

if __name__ == "__main__":
    main()
`

type virtualCapabilityRecord struct {
	ID                  string
	Values              map[string]any
	SourceWorkspacePath string
}

type virtualCapabilityService struct {
	mutex            sync.Mutex
	toolNameByName   map[string]bool
	workspacePath    string
	tasks            []virtualCapabilityRecord
	events           []virtualCapabilityRecord
	calendarRevision int
	site             *virtualCapabilityRecord
	sitePublished    bool
}

func startVirtualCapabilityServer(toolNames []string, workspacePath string, initialSite *VirtualSiteFixture) (capability.Client, func(), error) {
	toolNameByName := map[string]bool{}
	for _, toolName := range toolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName != "" {
			toolNameByName[trimmedToolName] = true
		}
	}
	service := &virtualCapabilityService{toolNameByName: toolNameByName, workspacePath: workspacePath}
	if errorValue := service.loadInitialSite(initialSite); errorValue != nil {
		return capability.Client{}, nil, errorValue
	}
	server := httptest.NewServer(http.HandlerFunc(service.handleRequest))
	return capability.Client{
		Endpoint:   server.URL,
		HTTPClient: server.Client(),
	}, server.Close, nil
}

func (service *virtualCapabilityService) loadInitialSite(initialSite *VirtualSiteFixture) error {
	if initialSite == nil {
		return nil
	}
	if initialSite.SiteID == "" || strings.TrimSpace(initialSite.SiteID) != initialSite.SiteID {
		return errors.New("virtual initial site requires an exact site ID")
	}
	if initialSite.Title == "" || strings.TrimSpace(initialSite.Title) != initialSite.Title {
		return errors.New("virtual initial site requires an exact title")
	}
	if !virtualSiteSlugPattern.MatchString(initialSite.Slug) {
		return errors.New("virtual initial site requires a DNS-safe slug")
	}
	sourceWorkspacePath, errorValue := virtualSiteSourcePathForSlug(initialSite.Slug)
	if errorValue != nil {
		return errorValue
	}
	service.site = &virtualCapabilityRecord{
		ID:                  initialSite.SiteID,
		Values:              map[string]any{"slug": initialSite.Slug, "title": initialSite.Title},
		SourceWorkspacePath: sourceWorkspacePath,
	}
	service.sitePublished = initialSite.IsPublished
	return nil
}

func (service *virtualCapabilityService) handleRequest(responseWriter http.ResponseWriter, request *http.Request) {
	responseWriter.Header().Set("Content-Type", "application/json")
	if request.Method == http.MethodGet && request.URL.Path == "/v1/capabilities" {
		_, _ = responseWriter.Write([]byte(virtualCapabilityCatalogResponse(service.toolNameByName)))
		return
	}
	if request.Method != http.MethodPost || !strings.HasPrefix(request.URL.Path, "/v1/tools/") || !strings.HasSuffix(request.URL.Path, "/invoke") {
		http.Error(responseWriter, "unsupported virtual capability endpoint", http.StatusNotFound)
		return
	}
	toolName := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/tools/"), "/invoke")
	if !service.toolNameByName[toolName] {
		http.Error(responseWriter, "unknown virtual capability tool", http.StatusNotFound)
		return
	}
	requestBody, _ := io.ReadAll(request.Body)
	_, _ = responseWriter.Write([]byte(service.response(toolName, requestBody)))
}

func virtualCapabilityCatalogResponse(toolNameByName map[string]bool) string {
	toolNames := make([]string, 0, len(toolNameByName))
	for toolName := range toolNameByName {
		toolNames = append(toolNames, toolName)
	}
	sort.Strings(toolNames)
	descriptors := []string{}
	for _, toolName := range toolNames {
		descriptor := virtualCapabilityToolDescriptor(toolName)
		descriptors = append(
			descriptors,
			`{"name":`+quote(descriptor.Name)+
				`,"description":`+quote(descriptor.Description)+
				`,"inputSchema":`+string(descriptor.InputSchema)+
				virtualCapabilityResultContract(toolName)+`}`,
		)
	}
	return `{"deviceCapabilities":[` + strings.Join(descriptors, ",") + `]}`
}

func (service *virtualCapabilityService) response(toolName string, requestBody []byte) string {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	switch toolName {
	case "task.add", "task.list", "task.update", "task.delete":
		return service.taskResponse(toolName, requestBody)
	case "calendar.add", "calendar.list", "calendar.update", "calendar.delete":
		return service.calendarResponse(toolName, requestBody)
	case "site.create":
		if service.site != nil {
			return virtualCapabilityInvalidInput(toolName, "virtual site already exists")
		}
		input := virtualCapabilityInput(requestBody)
		sourceWorkspacePath, errorValue := virtualSiteSourcePathForSlug(stringValue(input["slug"]))
		if errorValue != nil {
			return virtualCapabilityInvalidInput(toolName, errorValue.Error())
		}
		title := firstVirtualString(stringValue(input["title"]), stringValue(input["slug"]))
		input["title"] = title
		service.site = &virtualCapabilityRecord{ID: "site-1", Values: input, SourceWorkspacePath: sourceWorkspacePath}
		if errorValue := os.MkdirAll(filepath.Join(service.workspacePath, strings.TrimPrefix(sourceWorkspacePath, "/workspace/"), "app", "public"), 0o770); errorValue != nil {
			return virtualCapabilityInvalidInput(toolName, "virtual site workspace creation failed")
		}
		result := map[string]any{
			"siteID":              service.site.ID,
			"slug":                stringValue(input["slug"]),
			"title":               title,
			"status":              "draft",
			"sourceWorkspacePath": sourceWorkspacePath,
			"appWorkspacePath":    filepath.ToSlash(filepath.Join(sourceWorkspacePath, "app")),
			"sourceFiles":         json.RawMessage(virtualSiteCreateSourceFiles(requestBody)),
		}
		return virtualCapabilityWebsiteSuccess(toolName, "created", service.site.ID, result)
	case "site.preview":
		if !service.hasVirtualSiteID(requestBody) {
			return virtualCapabilityNotFound(toolName, "site")
		}
		if _, errorValue := service.virtualSiteSourceMetadata(); errorValue != nil {
			return virtualCapabilityInvalidInput(toolName, errorValue.Error())
		}
		input := virtualCapabilityInput(requestBody)
		previewID := firstVirtualString(stringValue(input["previewID"]), "site-preview-1")
		status := "draft"
		if service.sitePublished {
			status = "published"
		}
		result := map[string]any{
			"siteID":              service.site.ID,
			"status":              status,
			"sourceWorkspacePath": service.site.SourceWorkspacePath,
			"previewID":           previewID,
			"previewURL":          "https://preview-demo.device.example.test",
			"previewExpiresAt":    "2026-07-19T01:00:00Z",
		}
		return virtualCapabilityWebsiteSuccess(toolName, "previewed", service.site.ID, result)
	case "site.publish":
		if !service.hasVirtualSiteID(requestBody) {
			return virtualCapabilityNotFound(toolName, "site")
		}
		sourceMetadata, errorValue := service.virtualSiteSourceMetadata()
		if errorValue != nil {
			return virtualCapabilityInvalidInput(toolName, errorValue.Error())
		}
		service.sitePublished = true
		publishedResult := map[string]any{
			"siteID":              service.site.ID,
			"status":              "published",
			"sourceWorkspacePath": service.site.SourceWorkspacePath,
			"sourceSHA256":        sourceMetadata.SHA256,
			"publishedURL":        "https://demo.device.example.test",
			"currentVersionID":    "site-version-1",
		}
		return virtualCapabilityWebsiteSuccess(toolName, "published", service.site.ID, publishedResult)
	case "site.status":
		if !service.hasVirtualSiteReference(requestBody) {
			return virtualCapabilityNotFound(toolName, "site")
		}
		status := "draft"
		if service.sitePublished {
			status = "published"
		}
		sourceWorkspacePath := service.site.SourceWorkspacePath
		result := map[string]any{
			"siteID":              service.site.ID,
			"slug":                stringValue(service.site.Values["slug"]),
			"title":               firstVirtualString(stringValue(service.site.Values["title"]), stringValue(service.site.Values["slug"])),
			"status":              status,
			"sourceWorkspacePath": sourceWorkspacePath,
			"appWorkspacePath":    filepath.ToSlash(filepath.Join(sourceWorkspacePath, "app")),
		}
		return virtualCapabilitySuccess(toolName, virtualCapabilityJSON(result), result)
	case "site.delete":
		if virtualCapabilityRequestNeedsApproval(requestBody) {
			return virtualCapabilityApprovalRequired(toolName)
		}
		if !service.hasVirtualSiteID(requestBody) {
			return virtualCapabilityNotFound(toolName, "site")
		}
		deletedSiteID := service.site.ID
		sourceWorkspacePath := service.site.SourceWorkspacePath
		service.site = nil
		service.sitePublished = false
		if localPath, errorValue := virtualWorkspacePathToLocalPath(service.workspacePath, sourceWorkspacePath); errorValue == nil {
			_ = os.RemoveAll(filepath.Dir(localPath))
		}
		result := map[string]any{"siteID": deletedSiteID, "deleted": true}
		return virtualCapabilityWebsiteSuccess(toolName, "deleted", deletedSiteID, result)
	case "image.read":
		path := stringValue(virtualCapabilityInput(requestBody)["path"])
		result := map[string]any{"attachments": []map[string]any{{
			"devicePath":    path,
			"filename":      filepath.Base(path),
			"contentType":   "image/png",
			"sizeBytes":     13,
			"contentBase64": "dmlydHVhbC1pbWFnZQ==",
		}}, "path": path, "status": "ok"}
		return virtualCapabilitySuccess(toolName, "image loaded", result)
	case "document.read":
		path := stringValue(virtualCapabilityInput(requestBody)["path"])
		content, errorValue := virtualDocumentContent(service.workspacePath, path)
		if errorValue != nil {
			return virtualCapabilityInvalidInput(toolName, errorValue.Error())
		}
		result := map[string]any{
			"status":    "ok",
			"path":      path,
			"format":    "markdown",
			"content":   content,
			"warnings":  []string{},
			"truncated": false,
		}
		return virtualCapabilitySuccess(toolName, content, result)
	case "web.search":
		query := stringValue(virtualCapabilityInput(requestBody)["query"])
		answer := "BlueclawSearchStubToken virtual search result"
		result := map[string]any{
			"provider":          "virtual",
			"remoteLLMInvolved": false,
			"compatibility":     "native",
			"query":             query,
			"answer":            answer,
			"results": []map[string]any{{
				"title":   "BlueclawSearchStubToken result",
				"url":     "https://example.test/blueclaw-search-stub",
				"snippet": "Deterministic virtual search result for BlueclawSearchStubToken.",
			}},
		}
		return virtualCapabilitySuccess(toolName, answer, result)
	case "message.context":
		return virtualCapabilitySuccess(toolName, "virtual Mattermost conversation context", virtualMessageContextResult())
	case "message.search":
		return virtualCapabilitySuccess(toolName, "found virtual Mattermost message", virtualMessageSearchResult(requestBody))
	case "message.send":
		messageInput := virtualCapabilityInput(requestBody)
		if errorValue := validateVirtualMessageSendInput(messageInput); errorValue != nil {
			return virtualCapabilityInvalidInput(toolName, errorValue.Error())
		}
		if virtualCapabilityRequestNeedsApproval(requestBody) {
			return virtualCapabilityApprovalRequired(toolName)
		}
		messageID := "virtual-platform-message-001"
		result := map[string]any{"messageIDs": []string{messageID}, "deliveryStatus": "sent"}
		return virtualCapabilityMessageSuccess(toolName, "sent", []string{messageID}, "sent virtual platform message "+messageID, result)
	case "message.update":
		if virtualCapabilityRequestNeedsApproval(requestBody) {
			return virtualCapabilityApprovalRequired(toolName)
		}
		input := virtualCapabilityInput(requestBody)
		messageID := stringValue(input["messageID"])
		result := map[string]any{
			"messageID":      messageID,
			"deliveryStatus": "updated",
			"messageUpdated": strings.TrimSpace(stringValue(input["message"])) != "",
		}
		if isPinned, isFound := input["isPinned"].(bool); isFound {
			result["isPinned"] = isPinned
		}
		return virtualCapabilityMessageSuccess(toolName, "updated", []string{messageID}, "updated virtual platform message "+messageID, result)
	case "message.delete":
		if virtualCapabilityRequestNeedsApproval(requestBody) {
			return virtualCapabilityApprovalRequired(toolName)
		}
		messageIDs := stringSliceValue(virtualCapabilityInput(requestBody)["messageIDs"])
		result := map[string]any{"messageIDs": messageIDs, "deliveryStatus": "deleted"}
		return virtualCapabilityMessageSuccess(toolName, "deleted", messageIDs, "deleted virtual platform messages", result)
	case "channel.update":
		if virtualCapabilityRequestNeedsApproval(requestBody) {
			return virtualCapabilityApprovalRequired(toolName)
		}
		input := virtualCapabilityInput(requestBody)
		channelID := firstVirtualString(stringValue(input["channelID"]), "virtual-channel-1")
		result := map[string]any{"channelID": channelID, "updated": true}
		if inviteeHints := stringSliceValue(input["inviteeHints"]); len(inviteeHints) > 0 {
			invitedUserIDs := make([]string, 0, len(inviteeHints))
			for index := range inviteeHints {
				invitedUserIDs = append(invitedUserIDs, fmt.Sprintf("virtual-invitee-%d", index+1))
			}
			result["invitedUserIDs"] = invitedUserIDs
		}
		return virtualCapabilityChannelSuccess(toolName, "updated", channelID, "updated virtual channel "+channelID, result)
	default:
		return virtualCapabilitySuccess(toolName, toolName+" completed", map[string]any{"toolName": toolName, "ok": true, "request": virtualCapabilityInput(requestBody)})
	}
}

func virtualMessageContextResult() map[string]any {
	return map[string]any{
		"platform":                "mattermost",
		"conversationID":          "virtual-conversation-1",
		"conversationType":        "channel",
		"channelID":               "virtual-channel-1",
		"channelName":             "announcements",
		"replyTargetID":           "",
		"rootMessageID":           "",
		"currentMessageID":        "virtual-current-message-001",
		"requesterPersonID":       "virtual-requester-person",
		"requesterPlatformUserID": "virtual-requester-user",
		"botUserID":               "virtual-bot-user",
		"botUsername":             "internkim",
	}
}

func virtualMessageSearchResult(requestBody []byte) map[string]any {
	input := virtualCapabilityInput(requestBody)
	scope := firstVirtualString(stringValue(input["scope"]), "currentChannel")
	authoredBy := firstVirtualString(stringValue(input["authoredBy"]), "anyone")
	messageID := "virtual-platform-message-001"
	return map[string]any{
		"scope":      scope,
		"queries":    stringSliceValue(input["queries"]),
		"authoredBy": authoredBy,
		"messageIDs": []string{messageID},
		"candidates": []map[string]any{{
			"messageID":  messageID,
			"channelID":  "virtual-channel-1",
			"userID":     "virtual-bot-user",
			"authoredBy": "assistant",
			"createdAt":  1,
			"preview":    "virtual Mattermost message",
			"deletable":  true,
		}},
		"hasMore": false,
	}
}

func (service *virtualCapabilityService) hasVirtualSiteID(requestBody []byte) bool {
	if service.site == nil {
		return false
	}
	input := virtualCapabilityInput(requestBody)
	return stringValue(input["siteID"]) == service.site.ID
}

func (service *virtualCapabilityService) hasVirtualSiteReference(requestBody []byte) bool {
	if service.site == nil {
		return false
	}
	siteReference := stringValue(virtualCapabilityInput(requestBody)["siteReference"])
	return siteReference == service.site.ID || siteReference == stringValue(service.site.Values["slug"])
}

func virtualSiteSourcePathForSlug(slug string) (string, error) {
	trimmedSlug := strings.TrimSpace(slug)
	if trimmedSlug == "" {
		return "", errors.New("site slug is required for source workspace")
	}
	cleanSlug := filepath.Clean(trimmedSlug)
	if cleanSlug != trimmedSlug || cleanSlug == "." || cleanSlug == ".." || strings.Contains(cleanSlug, string(os.PathSeparator)) {
		return "", errors.New("site slug cannot contain path separators")
	}
	return filepath.ToSlash(filepath.Join("/workspace/circles/staff/sites", cleanSlug, "draft")), nil
}

type virtualSiteSourceMetadata struct {
	VirtualPath string
	SHA256      string
	SizeBytes   int
}

func (service *virtualCapabilityService) virtualSiteSourceMetadata() (virtualSiteSourceMetadata, error) {
	virtualSourceWorkspacePath := strings.TrimSpace(service.site.SourceWorkspacePath)
	if virtualSourceWorkspacePath == "" {
		return virtualSiteSourceMetadata{}, errors.New("site publish requires a stored source workspace")
	}
	_, errorValue := virtualWorkspacePathToLocalPath(service.workspacePath, virtualSourceWorkspacePath)
	if errorValue != nil {
		return virtualSiteSourceMetadata{}, errorValue
	}
	candidates := []struct {
		virtualPath string
		validate    func([]byte) bool
	}{
		{virtualPath: filepath.ToSlash(filepath.Join(virtualSourceWorkspacePath, "app", "dist", "index.html")), validate: isVirtualHTMLDocument},
		{virtualPath: filepath.ToSlash(filepath.Join(virtualSourceWorkspacePath, "app", "public", "site-content.json")), validate: isVirtualSiteContentDocument},
	}
	for _, candidate := range candidates {
		localPath, errorValue := virtualWorkspacePathToLocalPath(service.workspacePath, candidate.virtualPath)
		if errorValue != nil {
			return virtualSiteSourceMetadata{}, errorValue
		}
		content, errorValue := os.ReadFile(localPath)
		if errors.Is(errorValue, os.ErrNotExist) {
			continue
		}
		if errorValue != nil {
			return virtualSiteSourceMetadata{}, fmt.Errorf("read site source: %w", errorValue)
		}
		if len(content) == 0 || !candidate.validate(content) {
			continue
		}
		digest := sha256.Sum256(content)
		return virtualSiteSourceMetadata{
			VirtualPath: candidate.virtualPath,
			SHA256:      hex.EncodeToString(digest[:]),
			SizeBytes:   len(content),
		}, nil
	}
	return virtualSiteSourceMetadata{}, fmt.Errorf("site publish requires nonempty valid source under %s", filepath.ToSlash(filepath.Join(virtualSourceWorkspacePath, "app")))
}

func virtualWorkspacePathToLocalPath(workspacePath string, virtualPath string) (string, error) {
	trimmedVirtualPath := strings.TrimSpace(virtualPath)
	if !strings.HasPrefix(trimmedVirtualPath, "/workspace/") {
		return "", errors.New("site source path must be rooted at /workspace")
	}
	relativePath := filepath.Clean(strings.TrimPrefix(trimmedVirtualPath, "/workspace/"))
	if relativePath == "." || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) {
		return "", errors.New("site source path escapes the workspace")
	}
	return filepath.Join(workspacePath, relativePath), nil
}

func isVirtualHTMLDocument(content []byte) bool {
	normalizedContent := strings.ToLower(string(content))
	return strings.Contains(normalizedContent, "<html") && strings.Contains(normalizedContent, "</html>")
}

func isVirtualSiteContentDocument(content []byte) bool {
	var document map[string]any
	return json.Unmarshal(content, &document) == nil && len(document) > 0
}

func validateVirtualMessageSendInput(input map[string]any) error {
	if strings.TrimSpace(stringValue(input["message"])) == "" {
		return errors.New("message is required")
	}
	switch stringValue(input["targetType"]) {
	case "directMessage":
		personHints, _ := input["personHints"].([]any)
		if strings.TrimSpace(stringValue(input["personHint"])) == "" && len(personHints) == 0 {
			return errors.New("personHint or personHints is required for directMessage")
		}
	case "channel":
		if strings.TrimSpace(stringValue(input["channelName"])) == "" && strings.TrimSpace(stringValue(input["channelID"])) == "" {
			return errors.New("channelName or channelID is required for channel")
		}
	case "currentThread", "currentChannel":
	default:
		return errors.New("targetType is required")
	}
	return nil
}

func virtualCapabilityInputSchema(toolName string) string {
	if descriptor, isFound := virtualCanonicalCapabilityToolDescriptor(toolName); isFound {
		return string(descriptor.InputSchema)
	}
	return `{"type":"object"}`
}

func virtualCapabilityInputIntentSchema(toolName string) json.RawMessage {
	descriptor, isFound := virtualCanonicalCapabilityToolDescriptor(toolName)
	if !isFound {
		return nil
	}
	return descriptor.InputIntentSchema
}

func virtualCapabilityResultContract(toolName string) string {
	contract := virtualCapabilityToolResultContract(toolName)
	if contract == nil {
		return ""
	}
	document, _ := json.Marshal(contract)
	return `,"resultContract":` + string(document)
}

func virtualCapabilityToolResultContract(toolName string) *agentruntime.CapabilityToolResultContract {
	if descriptor, isFound := virtualGeneratedToolDescriptor(toolName); isFound {
		return descriptor.ResultContract
	}
	if !slices.Contains(virtualGeneratedResultContractToolNames, toolName) {
		return nil
	}
	descriptor, isFound := virtualCanonicalCapabilityToolDescriptor(toolName)
	if !isFound || descriptor.ResultContract == nil {
		panic("generated capability result contract is missing: " + toolName)
	}
	return descriptor.ResultContract
}

func virtualDocumentContent(workspacePath string, path string) (string, error) {
	localPath, errorValue := virtualWorkspacePathToLocalPath(workspacePath, path)
	if errorValue != nil {
		return "", errorValue
	}
	if strings.EqualFold(filepath.Ext(localPath), ".docx") {
		return virtualDOCXContent(localPath)
	}
	content, errorValue := os.ReadFile(localPath)
	if errorValue != nil {
		return "", errorValue
	}
	return string(content), nil
}

func virtualDOCXContent(path string) (string, error) {
	reader, errorValue := zip.OpenReader(path)
	if errorValue != nil {
		return "", errorValue
	}
	defer reader.Close()
	content, errorValue := readDOCXEntry(reader, "word/document.xml")
	if errorValue != nil {
		return "", errorValue
	}
	return extractDOCXText(content)
}

func extractDOCXText(content []byte) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(content)))
	textParts := []string{}
	for {
		token, errorValue := decoder.Token()
		if errorValue == io.EOF {
			return strings.Join(textParts, " "), nil
		}
		if errorValue != nil {
			return "", errorValue
		}
		startElement, isStartElement := token.(xml.StartElement)
		if !isStartElement || startElement.Name.Local != "t" {
			continue
		}
		var text string
		if errorValue := decoder.DecodeElement(&text, &startElement); errorValue != nil {
			return "", errorValue
		}
		textParts = append(textParts, text)
	}
}

func (service *virtualCapabilityService) taskResponse(toolName string, requestBody []byte) string {
	input := virtualCapabilityInput(requestBody)
	switch toolName {
	case "task.add":
		taskID := fmt.Sprintf("task-%d", len(service.tasks)+1)
		record := virtualCapabilityRecord{ID: taskID, Values: virtualTaskResultFromAddInput(taskID, input)}
		service.tasks = append(service.tasks, record)
		return virtualCapabilityTaskSuccess(toolName, "created", record.ID, "created virtual task", record.Values)
	case "task.list":
		tasks := virtualCapabilityRecordValues(service.tasks)
		return virtualCapabilitySuccess(toolName, "listed virtual tasks", map[string]any{"tasks": tasks, "count": len(tasks), "scope": "virtual"})
	case "task.update":
		if len(input) < 2 {
			return virtualCapabilityInvalidInput(toolName, "at least one task field must be updated")
		}
		index := virtualCapabilityRecordIndexByHint(service.tasks, input, "taskHint", "content")
		if index < 0 {
			return virtualCapabilityNotFound(toolName, "task")
		}
		values := copyVirtualCapabilityValues(input)
		if title := strings.TrimSpace(stringValue(values["title"])); title != "" {
			values["content"] = title
		}
		delete(values, "title")
		mergeVirtualCapabilityRecord(service.tasks[index].Values, values, "taskHint")
		return virtualCapabilityTaskSuccess(toolName, "updated", service.tasks[index].ID, "updated virtual task", service.tasks[index].Values)
	default:
		if virtualCapabilityRequestNeedsApproval(requestBody) {
			return virtualCapabilityApprovalRequired(toolName)
		}
		index := virtualCapabilityRecordIndexByHint(service.tasks, input, "taskHint", "content")
		if index < 0 {
			return virtualCapabilityNotFound(toolName, "task")
		}
		deletedRecord := service.tasks[index]
		service.tasks = append(service.tasks[:index], service.tasks[index+1:]...)
		return virtualCapabilityTaskSuccess(toolName, "deleted", deletedRecord.ID, "deleted virtual task", map[string]any{"taskID": deletedRecord.ID, "deleted": true})
	}
}

func virtualTaskResultFromAddInput(taskID string, input map[string]any) map[string]any {
	result := map[string]any{
		"taskID":  taskID,
		"content": strings.TrimSpace(stringValue(input["title"])),
	}
	for _, fieldName := range []string{"goal", "size", "status", "startDate", "endDate"} {
		if value, isFound := input[fieldName]; isFound {
			result[fieldName] = value
		}
	}
	if ownerName := strings.TrimSpace(stringValue(input["targetPersonHint"])); ownerName != "" {
		result["ownerName"] = ownerName
	}
	if participantNames := stringSliceValue(input["participantPersonHints"]); len(participantNames) > 0 {
		result["participantNames"] = participantNames
	}
	return result
}

func (service *virtualCapabilityService) calendarResponse(toolName string, requestBody []byte) string {
	input := virtualCapabilityInput(requestBody)
	requester := virtualCapabilityRequesterFromRequest(requestBody)
	switch toolName {
	case "calendar.add":
		eventID := fmt.Sprintf("calendar-event-%03d", len(service.events)+1)
		record := virtualCapabilityRecord{ID: eventID, Values: service.virtualCalendarEventValues(eventID, input, requester)}
		service.events = append(service.events, record)
		return virtualCapabilityCalendarSuccess(toolName, "created", record.ID, "created virtual calendar event", record.Values)
	case "calendar.list":
		events, errorValue := virtualCalendarEventList(service.events, input)
		if errorValue != nil {
			return virtualCapabilityInvalidInput(toolName, errorValue.Error())
		}
		return virtualCapabilitySuccess(toolName, "listed virtual calendar events", map[string]any{"events": events})
	case "calendar.update":
		if len(input) < 2 {
			return virtualCapabilityInvalidInput(toolName, "at least one calendar event field must be updated")
		}
		index := virtualCapabilityRecordIndexByHint(service.events, input, "eventHint", "title")
		if index < 0 {
			return virtualCapabilityNotFound(toolName, "calendar event")
		}
		mergeVirtualCalendarEvent(service.events[index].Values, input, requester, false)
		service.events[index].Values["updatedAt"] = service.nextVirtualCalendarUpdatedAt()
		return virtualCapabilityCalendarSuccess(toolName, "updated", service.events[index].ID, "updated virtual calendar event", service.events[index].Values)
	default:
		if virtualCapabilityRequestNeedsApproval(requestBody) {
			return virtualCapabilityApprovalRequired(toolName)
		}
		index := virtualCapabilityRecordIndexByHint(service.events, input, "eventHint", "title")
		if index < 0 {
			return virtualCapabilityNotFound(toolName, "calendar event")
		}
		deletedRecord := service.events[index]
		service.events = append(service.events[:index], service.events[index+1:]...)
		result := map[string]any{"eventID": deletedRecord.ID, "deleted": true}
		return virtualCapabilityCalendarSuccess(toolName, "deleted", deletedRecord.ID, "deleted virtual calendar event", result)
	}
}

func (service *virtualCapabilityService) virtualCalendarEventValues(eventID string, input map[string]any, requester virtualCapabilityRequester) map[string]any {
	values := map[string]any{
		"eventID":           eventID,
		"title":             "",
		"description":       "",
		"location":          "",
		"startISO":          "",
		"endISO":            "",
		"timeZone":          "",
		"isAllDay":          false,
		"color":             "",
		"people":            []any{},
		"participants":      []any{},
		"reminderLeadHours": 24,
		"updatedAt":         service.nextVirtualCalendarUpdatedAt(),
	}
	mergeVirtualCalendarEvent(values, input, requester, true)
	return values
}

func (service *virtualCapabilityService) nextVirtualCalendarUpdatedAt() string {
	service.calendarRevision++
	return time.Date(2026, time.January, 1, 0, 0, service.calendarRevision, 0, time.UTC).Format(time.RFC3339)
}

func mergeVirtualCalendarEvent(event map[string]any, input map[string]any, requester virtualCapabilityRequester, includeRequesterDefault bool) {
	for _, fieldName := range []string{"title", "description", "location", "startISO", "endISO", "timeZone", "isAllDay", "color", "people", "reminderLeadHours"} {
		if value, isPresent := input[fieldName]; isPresent {
			event[fieldName] = value
		}
	}
	if _, hasPeople := input["people"]; hasPeople {
		event["participants"] = virtualCalendarParticipants(event["people"], requester, virtualCalendarIncludesRequester(input, includeRequesterDefault))
		return
	}
	if virtualCalendarIncludesRequester(input, includeRequesterDefault) {
		event["participants"] = appendVirtualCalendarRequester(event["participants"], requester)
	}
}

func virtualCalendarEventList(records []virtualCapabilityRecord, input map[string]any) ([]map[string]any, error) {
	query := strings.ToLower(strings.TrimSpace(stringValue(input["query"])))
	start, end, errorValue := virtualCalendarWindow(input)
	if errorValue != nil {
		return nil, errorValue
	}
	limit, errorValue := virtualCalendarLimit(input)
	if errorValue != nil {
		return nil, errorValue
	}
	events := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if query != "" && !virtualCalendarEventMatches(record.Values, query) {
			continue
		}
		if !virtualCalendarEventOverlapsWindow(record.Values, start, end) {
			continue
		}
		events = append(events, record.Values)
		if limit > 0 && float64(len(events)) >= limit {
			break
		}
	}
	return events, nil
}

func virtualCalendarEventMatches(event map[string]any, query string) bool {
	searchText := strings.ToLower(strings.Join([]string{
		stringValue(event["title"]),
		stringValue(event["description"]),
		stringValue(event["location"]),
	}, "\n"))
	return strings.Contains(searchText, query)
}

func virtualCalendarWindow(input map[string]any) (*time.Time, *time.Time, error) {
	startValue := strings.TrimSpace(stringValue(input["startISO"]))
	endValue := strings.TrimSpace(stringValue(input["endISO"]))
	if startValue == "" && endValue == "" {
		return nil, nil, nil
	}
	if startValue == "" || endValue == "" {
		return nil, nil, errors.New("startISO and endISO must be provided together")
	}
	start, errorValue := parseVirtualCalendarTime(startValue)
	if errorValue != nil {
		return nil, nil, fmt.Errorf("startISO is invalid: %w", errorValue)
	}
	end, errorValue := parseVirtualCalendarTime(endValue)
	if errorValue != nil {
		return nil, nil, fmt.Errorf("endISO is invalid: %w", errorValue)
	}
	if !end.After(start) {
		return nil, nil, errors.New("endISO must be after startISO")
	}
	return &start, &end, nil
}

func virtualCalendarLimit(input map[string]any) (float64, error) {
	value, isPresent := input["limit"]
	if !isPresent {
		return 0, nil
	}
	number, isNumber := value.(float64)
	if !isNumber || number <= 0 || math.Trunc(number) != number {
		return 0, errors.New("limit must be a positive whole number")
	}
	return number, nil
}

func virtualCalendarEventOverlapsWindow(event map[string]any, start *time.Time, end *time.Time) bool {
	if start == nil || end == nil {
		return true
	}
	eventStart, startError := parseVirtualCalendarTime(stringValue(event["startISO"]))
	eventEnd, endError := parseVirtualCalendarTime(stringValue(event["endISO"]))
	if startError != nil || endError != nil {
		return false
	}
	return eventStart.Before(*end) && eventEnd.After(*start)
}

func parseVirtualCalendarTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if parsedTime, errorValue := time.Parse(layout, strings.TrimSpace(value)); errorValue == nil {
			return parsedTime, nil
		}
	}
	return time.Time{}, errors.New("expected RFC3339 timestamp or YYYY-MM-DD date")
}

type virtualCapabilityRequester struct {
	PersonID string
	Name     string
	Email    string
}

func virtualCapabilityRequesterFromRequest(requestBody []byte) virtualCapabilityRequester {
	var requestDocument struct {
		Context struct {
			RequesterPersonID string `json:"requesterPersonID"`
			RequesterName     string `json:"requesterName"`
			RequesterEmail    string `json:"requesterEmail"`
		} `json:"context"`
	}
	_ = json.Unmarshal(requestBody, &requestDocument)
	return virtualCapabilityRequester{
		PersonID: strings.TrimSpace(requestDocument.Context.RequesterPersonID),
		Name:     strings.TrimSpace(requestDocument.Context.RequesterName),
		Email:    strings.ToLower(strings.TrimSpace(requestDocument.Context.RequesterEmail)),
	}
}

func virtualCalendarIncludesRequester(input map[string]any, defaultValue bool) bool {
	value, isPresent := input["includeRequester"]
	if !isPresent {
		return defaultValue
	}
	includeRequester, isBoolean := value.(bool)
	return isBoolean && includeRequester
}

func virtualCalendarParticipants(people any, requester virtualCapabilityRequester, includeRequester bool) []any {
	participants := []any{}
	for _, person := range stringSliceValue(people) {
		participants = appendVirtualCalendarParticipant(participants, virtualCapabilityRequester{Name: person})
	}
	if includeRequester {
		participants = appendVirtualCalendarParticipant(participants, requester)
	}
	return participants
}

func appendVirtualCalendarRequester(participants any, requester virtualCapabilityRequester) []any {
	result, _ := participants.([]any)
	return appendVirtualCalendarParticipant(result, requester)
}

func appendVirtualCalendarParticipant(participants []any, requester virtualCapabilityRequester) []any {
	name := firstNonEmptyVirtualValue(requester.Name, requester.Email, requester.PersonID)
	if name == "" {
		return participants
	}
	for _, participant := range participants {
		document, _ := participant.(map[string]any)
		if strings.EqualFold(stringValue(document["name"]), name) {
			return participants
		}
	}
	return append(participants, map[string]any{
		"personID": requester.PersonID,
		"name":     name,
		"email":    requester.Email,
	})
}

func firstNonEmptyVirtualValue(values ...string) string {
	for _, value := range values {
		if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func stringSliceValue(value any) []string {
	values, _ := value.([]any)
	result := make([]string, 0, len(values))
	for _, entry := range values {
		if normalizedValue := strings.TrimSpace(stringValue(entry)); normalizedValue != "" {
			result = append(result, normalizedValue)
		}
	}
	return result
}

func copyVirtualCapabilityValues(values map[string]any) map[string]any {
	copiedValues := make(map[string]any, len(values)+1)
	for fieldName, value := range values {
		copiedValues[fieldName] = value
	}
	return copiedValues
}

func virtualCapabilityInput(requestBody []byte) map[string]any {
	var requestDocument struct {
		Input map[string]any `json:"input"`
	}
	if json.Unmarshal(requestBody, &requestDocument) != nil || requestDocument.Input == nil {
		return map[string]any{}
	}
	return requestDocument.Input
}

func virtualCapabilityRecordIndexByID(records []virtualCapabilityRecord, input map[string]any, idFieldName string) int {
	requestedID := strings.TrimSpace(stringValue(input[idFieldName]))
	for index, record := range records {
		if record.ID == requestedID {
			return index
		}
	}
	return -1
}

func virtualCapabilityRecordIndexByHint(records []virtualCapabilityRecord, input map[string]any, hintFieldName string, titleFieldName string) int {
	hint := strings.TrimSpace(stringValue(input[hintFieldName]))
	if hint == "" {
		return -1
	}
	for index, record := range records {
		if record.ID == hint {
			return index
		}
	}
	matchingIndex := -1
	matchCount := 0
	for index, record := range records {
		if stringValue(record.Values[titleFieldName]) == hint {
			matchingIndex = index
			matchCount++
		}
	}
	if matchCount == 1 {
		return matchingIndex
	}
	return -1
}

func mergeVirtualCapabilityRecord(record map[string]any, input map[string]any, excludedFieldNames ...string) {
	excludedFields := map[string]bool{}
	for _, fieldName := range excludedFieldNames {
		excludedFields[fieldName] = true
	}
	for fieldName, value := range input {
		if !excludedFields[fieldName] {
			record[fieldName] = value
		}
	}
}

func virtualCapabilityRecordValues(records []virtualCapabilityRecord) []map[string]any {
	values := make([]map[string]any, 0, len(records))
	for _, record := range records {
		values = append(values, record.Values)
	}
	return values
}

func virtualCapabilitySuccess(toolName string, content string, result any) string {
	return virtualCapabilityJSON(map[string]any{"provider": "virtual", "selectedBackend": "device", "toolName": toolName, "outcome": "succeeded", "status": "ok", "content": content, "result": result})
}

func virtualCapabilityTaskSuccess(toolName string, effect string, taskID string, content string, result any) string {
	return virtualCapabilityJSON(map[string]any{
		"provider":        "virtual",
		"selectedBackend": "device",
		"toolName":        toolName,
		"outcome":         "succeeded",
		"status":          "ok",
		"content":         content,
		"result":          result,
		"effects":         []map[string]any{{"objectType": "task", "effect": effect, "id": taskID}},
	})
}

func virtualCapabilityCalendarSuccess(toolName string, effect string, eventID string, content string, result any) string {
	return virtualCapabilityJSON(map[string]any{
		"provider":        "virtual",
		"selectedBackend": "device",
		"toolName":        toolName,
		"outcome":         "succeeded",
		"status":          "ok",
		"content":         content,
		"result":          result,
		"effects":         []map[string]any{{"objectType": "calendar", "effect": effect, "id": eventID}},
	})
}

func virtualCapabilityMessageSuccess(toolName string, effect string, messageIDs []string, content string, result any) string {
	effects := make([]map[string]any, 0, len(messageIDs))
	for _, messageID := range messageIDs {
		effects = append(effects, map[string]any{"objectType": "message", "effect": effect, "id": messageID})
	}
	return virtualCapabilityJSON(map[string]any{
		"provider":        "virtual",
		"selectedBackend": "device",
		"toolName":        toolName,
		"outcome":         "succeeded",
		"status":          "ok",
		"content":         content,
		"result":          result,
		"effects":         effects,
	})
}

func virtualCapabilityChannelSuccess(toolName string, effect string, channelID string, content string, result any) string {
	return virtualCapabilityJSON(map[string]any{
		"provider":        "virtual",
		"selectedBackend": "device",
		"toolName":        toolName,
		"outcome":         "succeeded",
		"status":          "ok",
		"content":         content,
		"result":          result,
		"effects":         []map[string]any{{"objectType": "channel", "effect": effect, "id": channelID}},
	})
}

func virtualCapabilityWebsiteSuccess(toolName string, effect string, siteID string, result any) string {
	effects := []map[string]any{{"objectType": "website", "effect": effect, "id": siteID}}
	if resultDocument, isObject := result.(map[string]any); isObject {
		if publishedURL := strings.TrimSpace(stringValue(resultDocument["publishedURL"])); publishedURL != "" {
			effects = append(effects, map[string]any{"objectType": "website", "effect": effect, "url": publishedURL})
		}
	}
	return virtualCapabilityJSON(map[string]any{
		"provider":        "virtual",
		"selectedBackend": "device",
		"toolName":        toolName,
		"outcome":         "succeeded",
		"status":          "ok",
		"content":         virtualCapabilityJSON(result),
		"result":          result,
		"effects":         effects,
	})
}

func virtualCapabilityApprovalRequired(toolName string) string {
	result := map[string]any{"errorCode": "approval_required", "failureStage": "authorization", "message": "requires approval"}
	return virtualCapabilityJSON(map[string]any{"provider": "virtual", "selectedBackend": "device", "toolName": toolName, "outcome": "denied", "status": "denied", "content": "requires approval", "message": "requires approval", "errorCode": "approval_required", "failureStage": "authorization", "result": result})
}

func virtualCapabilityInvalidInput(toolName string, message string) string {
	return virtualCapabilityJSON(map[string]any{
		"provider":        "virtual",
		"selectedBackend": "device",
		"toolName":        toolName,
		"outcome":         "failed",
		"status":          "error",
		"content":         message,
		"message":         message,
		"errorCode":       "invalid_input",
		"failureStage":    "input",
		"result":          map[string]any{"message": message},
	})
}

func virtualCapabilityNotFound(toolName string, resourceName string) string {
	message := "virtual " + resourceName + " not found"
	return virtualCapabilityJSON(map[string]any{"provider": "virtual", "selectedBackend": "device", "toolName": toolName, "outcome": "failed", "status": "error", "content": message, "message": message, "errorCode": "not_found", "failureStage": "lookup", "result": map[string]any{"message": message}})
}

func virtualCapabilityJSON(document any) string {
	encodedDocument, errorValue := json.Marshal(document)
	if errorValue != nil {
		return `{"provider":"virtual","status":"error","message":"virtual response encoding failed"}`
	}
	return string(encodedDocument)
}

func stringValue(value any) string {
	text, isString := value.(string)
	if !isString {
		return ""
	}
	return text
}

func virtualSiteCreateSourceFiles(requestBody []byte) string {
	var request struct {
		Input struct {
			Content json.RawMessage `json:"content"`
		} `json:"input"`
	}
	if json.Unmarshal(requestBody, &request) != nil || len(request.Input.Content) == 0 {
		return `[{"path":"app/public/site-content.json","content":"{}"}]`
	}
	encodedContent, errorValue := json.Marshal(string(request.Input.Content))
	if errorValue != nil {
		return `[{"path":"app/public/site-content.json","content":"{}"}]`
	}
	return `[{"path":"app/public/site-content.json","content":` + string(encodedContent) + `}]`
}

func jsonObjectOrEmpty(document []byte) string {
	trimmedDocument := strings.TrimSpace(string(document))
	if trimmedDocument == "" {
		return "{}"
	}
	var decodedDocument map[string]any
	if errorValue := json.Unmarshal([]byte(trimmedDocument), &decodedDocument); errorValue != nil {
		return "{}"
	}
	return trimmedDocument
}

func virtualCapabilityRequestNeedsApproval(requestBody []byte) bool {
	var requestDocument struct {
		Context struct {
			IsApprovalContinuation bool `json:"isApprovalContinuation"`
		} `json:"context"`
	}
	if len(requestBody) == 0 || json.Unmarshal(requestBody, &requestDocument) != nil {
		return false
	}
	return !requestDocument.Context.IsApprovalContinuation
}

func streamProgressObserver(writer io.Writer) func(task.RawTurnEvent) {
	return func(rawTurnEvent task.RawTurnEvent) {
		switch {
		case rawTurnEvent.Name == "agent.checkpoint.sent":
			fmt.Fprintf(writer, "  ↳ reply: %s\n", agent.CheckpointReplyMessage(rawTurnEvent.Body))
		case strings.HasPrefix(rawTurnEvent.Name, "tool.") && strings.HasSuffix(rawTurnEvent.Name, ".requested"):
			fmt.Fprintf(writer, "  ↳ tool: %s\n", strings.TrimSuffix(strings.TrimPrefix(rawTurnEvent.Name, "tool."), ".requested"))
		}
	}
}

func (harness *VirtualSessionHarness) Run(ctx context.Context) (VirtualSessionResult, error) {
	if harness.cleanup != nil {
		defer harness.cleanup()
	}
	if harness.scenario.ProgressWriter != nil {
		unregisterProgress := harness.taskEventService.RegisterTurnObserver(streamProgressObserver(harness.scenario.ProgressWriter))
		defer unregisterProgress()
	}
	result := VirtualSessionResult{
		ScenarioName:          harness.scenario.Name,
		ArtifactDirectoryPath: harness.artifactPath,
	}
	for index, virtualTurn := range harness.scenario.Turns {
		if harness.scriptedModel != nil {
			if strings.TrimSpace(virtualTurn.RouterApproval) != "" {
				harness.scriptedModel.EnqueueStructuredResponses("blueclaw_turn_router", scenarioApprovalRouterResponse(virtualTurn.RouterApproval))
			} else if len(virtualTurn.RouterRequiredEvidence) > 0 || virtualTurn.RouterTaskShape != "" || strings.TrimSpace(virtualTurn.RouterSiteEvidence) != "" || virtualTurnExpectsEvent(virtualTurn, "ask.requested") || virtualTurnExpectsEvent(virtualTurn, "ask.resolved") {
				harness.scriptedModel.EnqueueStructuredResponses("blueclaw_turn_router", scenarioTurnRouterResponse(harness.scenario, virtualTurn))
			}
			harness.scriptedModel.SetActionResponses(virtualTurn.ActionResponses...)
			if len(virtualTurn.CompletionJudgeResponses) > 0 {
				harness.scriptedModel.EnqueueStructuredResponses("blueclaw_completion_judge", virtualTurn.CompletionJudgeResponses...)
			}
		}
		turnResult, errorValue := harness.runTurn(ctx, index, virtualTurn)
		if errorValue != nil {
			return result, errorValue
		}
		turnResult.InformationalAssertions = informationalAssertionResults(virtualTurn, turnResult)
		result.TurnResults = append(result.TurnResults, turnResult)
		if errorValue := harness.assertTurnResult(virtualTurn, turnResult); errorValue != nil {
			return result, fmt.Errorf("%s turn %d: %w", harness.scenario.Name, index+1, errorValue)
		}
		harness.rememberTurn(virtualTurn, turnResult)
	}
	result.TaskSchedules = harness.scheduleStore.TaskSchedules()
	return result, nil
}

func actionScriptedLanguageModelForScenario(scenario VirtualSessionScenario) *agenttest.ScriptedLanguageModel {
	if scenario.DisableScriptedModel {
		return nil
	}
	for _, virtualTurn := range scenario.Turns {
		if len(virtualTurn.ActionResponses) > 0 {
			return agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
				ProviderName:             "virtual",
				ModelName:                "scripted",
				DefaultResponsesBySchema: scenarioDefaultResponses(scenario),
			})
		}
	}
	return nil
}

func scenarioDefaultResponses(scenario VirtualSessionScenario) map[string]string {
	defaultResponses := map[string]string{}
	defaultResponses["blueclaw_completion_judge"] = `{"satisfied":true,"missingWork":[],"reason":"scripted default"}`
	defaultResponses["blueclaw_addressing_classification"] = `{"target":"anyone","shouldRespond":false,"dutyMatch":false,"dutyName":"","dutyConfidence":0}`
	if strings.TrimSpace(scenario.AddressingResponse) != "" {
		defaultResponses["blueclaw_addressing_classification"] = strings.TrimSpace(scenario.AddressingResponse)
	}
	if virtualEvidenceRequiresExternalSend(scenario.RouterRequiredEvidence) {
		defaultResponses["blueclaw_execution_plan"] = `{"originalInstruction":"scripted external send","summary":"scripted external send","targets":[],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":true,"thirdPartyExternalSend":true,"repeated":false,"highFrequency":false,"destructive":false,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"scripted external send"}`
	}
	if scenario.ScriptedExecutionPlan != nil {
		if document, errorValue := json.Marshal(scenario.ScriptedExecutionPlan); errorValue == nil {
			defaultResponses["blueclaw_execution_plan"] = string(document)
		}
	}
	if reply := strings.TrimSpace(scenario.ScriptedConfirmationReply); reply != "" {
		if document, errorValue := json.Marshal(map[string]string{"reply": reply}); errorValue == nil {
			defaultResponses["blueclaw_confirmation_message"] = string(document)
		}
	}
	if len(scenario.InitialToolNames) == 0 && len(scenario.RouterRequiredEvidence) == 0 && scenario.RouterTaskShape == "" {
		return defaultResponses
	}
	defaultResponses["blueclaw_turn_router"] = scenarioTurnRouterResponse(scenario, VirtualTurn{})
	return defaultResponses
}

func virtualEvidenceRequiresExternalSend(requiredEvidence []string) bool {
	for _, toolName := range requiredEvidence {
		switch strings.TrimSpace(toolName) {
		case "message.send", "mail.message.send", "google.gmail.send", "slack.message.send":
			return true
		}
	}
	return false
}

func scenarioApprovalRouterResponse(approval string) string {
	routerDocument := map[string]any{
		"route":            "continue_task",
		"classification":   "bounded_task",
		"taskShape":        "maintenance_task",
		"level":            "low",
		"estimatedMinutes": 1,
		"approval":         strings.TrimSpace(approval),
		"responseLanguage": "ko",
		"reason":           "scripted approval reply classification",
		"userFacingReply":  "",
	}
	document, errorValue := json.Marshal(routerDocument)
	if errorValue != nil {
		return "{}"
	}
	return string(document)
}

func scenarioTurnRouterResponse(scenario VirtualSessionScenario, virtualTurn VirtualTurn) string {
	taskLevel := agent.NormalizeTaskLevel(scenario.RouterTaskLevel)
	if taskLevel == "" {
		taskLevel = agent.TaskLevelLow
	}
	requiredEvidence := scenario.RouterRequiredEvidence
	if len(virtualTurn.RouterRequiredEvidence) > 0 {
		requiredEvidence = virtualTurn.RouterRequiredEvidence
	}
	taskShape := scenario.RouterTaskShape
	if virtualTurn.RouterTaskShape != "" {
		taskShape = virtualTurn.RouterTaskShape
	}
	if taskShape == "" {
		taskShape = agent.TaskShapeMaintenanceTask
	}
	classification := "bounded_task"
	if taskShape == agent.TaskShapeImmediateReply {
		classification = "quick_reply"
	}
	siteEvidence := scenario.RouterSiteEvidence
	if strings.TrimSpace(virtualTurn.RouterSiteEvidence) != "" {
		siteEvidence = virtualTurn.RouterSiteEvidence
	}
	route := "start_task"
	if virtualTurnExpectsEvent(virtualTurn, "ask.resolved") {
		route = "continue_task"
	}
	routerDocument := map[string]any{
		"route":                  route,
		"classification":         classification,
		"taskShape":              taskShape,
		"level":                  string(taskLevel),
		"estimatedMinutes":       10,
		"requestedOutputFormats": nil,
		"expectedResults":        []any{},
		"siteRequestEvidence":    siteEvidence,
		"responseLanguage":       "ko",
		"reason":                 "scripted scenario default",
		"userFacingReply":        "",
		"initialToolNames":       appendUniqueScenarioToolNames(scenario.InitialToolNames, requiredEvidence),
		"priorTaskReference":     "none",
	}
	if virtualTurnExpectsEvent(virtualTurn, "confirmation.reply_classified") {
		routerDocument["approval"] = "approve"
	}
	encodedDocument, errorValue := json.Marshal(routerDocument)
	if errorValue != nil {
		return `{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":10,"requestedOutputFormats":null,"expectedResults":[],"requiredEvidence":[],"siteRequestEvidence":"","responseLanguage":"ko","reason":"scripted scenario default","userFacingReply":"","initialToolNames":[],"priorTaskReference":"none"}`
	}
	return string(encodedDocument)
}

func appendUniqueScenarioToolNames(toolNames []string, additionalToolNames []string) []string {
	merged := append([]string{}, toolNames...)
	seen := map[string]bool{}
	for _, toolName := range merged {
		seen[toolName] = true
	}
	for _, toolName := range additionalToolNames {
		if toolName == "" || seen[toolName] {
			continue
		}
		seen[toolName] = true
		merged = append(merged, toolName)
	}
	return merged
}

func virtualTurnExpectsEvent(virtualTurn VirtualTurn, eventName string) bool {
	for _, expectedEventName := range virtualTurn.ExpectedEvents {
		if expectedEventName == eventName {
			return true
		}
	}
	return false
}

func (harness *VirtualSessionHarness) runTurn(ctx context.Context, index int, virtualTurn VirtualTurn) (VirtualTurnResult, error) {
	reactionStartIndex := harness.adapter.ReactionCount()
	modelRequestStartIndex := 0
	if harness.requestRecorder != nil {
		modelRequestStartIndex = harness.requestRecorder.RequestCount()
	}
	modelCallStartIndex := 0
	if harness.callRecorder != nil {
		modelCallStartIndex = harness.callRecorder.CallCount()
	}
	messages := harness.adapter.VisibleHistory()
	messages = append(messages, virtualTurn.ContextMessages...)
	conversationID := "virtual-conversation-1"
	historyCursor := ""
	if len(messages) > 0 {
		historyCursor = conversationID
	}
	event := connectors.PlatformInboundEvent{
		Platform:       "virtual",
		Source:         "e2e",
		ConversationID: conversationID,
		MessageID:      fmt.Sprintf("virtual-message-%03d", index+1),
		SenderID:       "user-1",
		ReplyTargetID:  virtualReplyTargetID(index, virtualTurn),
		Prompt:         virtualTurn.Prompt,
		Context: connectors.VisibleContext{
			Messages:      messages,
			HasMoreBefore: len(messages) > 0,
			HistoryCursor: historyCursor,
			InputAttachments: append([]connectors.InputAttachment{},
				virtualTurn.InputAttachments...,
			),
			Materials: append([]connectors.InputAttachment{},
				virtualTurn.ContextMaterials...,
			),
			Sender: connectors.VisibleContextSender{
				Platform:    "virtual",
				SenderID:    "user-1",
				Handle:      "dongha",
				Email:       "dongha@example.com",
				Name:        "샘플",
				CallingName: "샘플 님",
			},
			ConversationType: strings.TrimSpace(virtualTurn.ConversationType),
			ChannelID:        strings.TrimSpace(virtualTurn.ChannelID),
			ChannelName:      strings.TrimSpace(virtualTurn.ChannelName),
			Addressing:       virtualTurn.Addressing,
		},
		RawReceivedAt: time.Now().UTC(),
	}
	runtimeResult, errorValue := harness.runtime.HandleInboundEvent(ctx, harness.adapter, event)
	if errorValue != nil {
		return VirtualTurnResult{}, errorValue
	}
	turnResult := VirtualTurnResult{
		Handled:                 runtimeResult.Handled,
		Ignored:                 runtimeResult.Ignored,
		Reason:                  runtimeResult.Reason,
		Reactions:               harness.adapter.ReactionsSince(reactionStartIndex),
		TaskRunID:               runtimeResult.TaskRunID,
		LanguageModelCallEvents: harness.modelCallsSince(modelCallStartIndex),
		ModelContext:            harness.modelContextSince(modelRequestStartIndex),
		ModelImagePartCount:     harness.modelImagePartCountSince(modelRequestStartIndex),
		UserModelImagePartCount: harness.userModelImagePartCountSince(modelRequestStartIndex),
	}
	if strings.TrimSpace(runtimeResult.TaskRunID) != "" {
		taskRun, isFound := harness.taskRunService.FindTaskRun(runtimeResult.TaskRunID)
		if !isFound {
			return VirtualTurnResult{}, errors.New("virtual turn task run not found")
		}
		turnResult.TaskStatus = taskRun.Status
		turnResult.FailureReason = taskRun.FailureReason
		turnResult.Events = harness.taskEventService.ListTaskEvent(runtimeResult.TaskRunID)
	}
	if strings.TrimSpace(runtimeResult.ReplyDispatchID) == "" {
		return turnResult, nil
	}
	outboundReply, outboundReplyTarget, isFound := harness.adapter.FindReply(runtimeResult.ReplyDispatchID)
	if !isFound {
		return VirtualTurnResult{}, fmt.Errorf("virtual turn reply dispatch %q was not recorded", runtimeResult.ReplyDispatchID)
	}
	turnResult.DidReply = true
	turnResult.FinishMessage = outboundReply.Message
	turnResult.ReplyTargetID = outboundReplyTarget.ReplyTargetID
	turnResult.Attachments = outboundReply.Attachments
	return turnResult, nil
}

func virtualReplyTargetID(index int, virtualTurn VirtualTurn) string {
	if strings.TrimSpace(virtualTurn.ReplyTargetID) != "" {
		return strings.TrimSpace(virtualTurn.ReplyTargetID)
	}
	return fmt.Sprintf("virtual-reply-%03d", index+1)
}

func (harness *VirtualSessionHarness) modelContextSince(startIndex int) string {
	if harness.requestRecorder == nil {
		return ""
	}
	parts := []string{}
	for _, request := range harness.requestRecorder.RequestsSince(startIndex) {
		if request.StructuredOutputSchema.Name != "blueclaw_agent_turn_action" {
			continue
		}
		for _, message := range request.Messages {
			parts = append(parts, message.Role+": "+message.Content)
		}
		parts = append(parts, request.StructuredOutputSchema.Document)
	}
	return strings.Join(parts, "\n")
}

func (harness *VirtualSessionHarness) modelImagePartCountSince(startIndex int) int {
	return harness.modelImagePartCountByRoleSince(startIndex, "")
}

func (harness *VirtualSessionHarness) userModelImagePartCountSince(startIndex int) int {
	return harness.modelImagePartCountByRoleSince(startIndex, "user")
}

func (harness *VirtualSessionHarness) modelImagePartCountByRoleSince(startIndex int, role string) int {
	if harness.requestRecorder == nil {
		return 0
	}
	count := 0
	for _, request := range harness.requestRecorder.RequestsSince(startIndex) {
		if request.StructuredOutputSchema.Name != "blueclaw_agent_turn_action" {
			continue
		}
		for _, message := range request.Messages {
			if role != "" && message.Role != role {
				continue
			}
			for _, part := range message.Parts {
				if part.Type == "image" {
					count++
				}
			}
		}
	}
	return count
}

func (harness *VirtualSessionHarness) rememberTurn(virtualTurn VirtualTurn, turnResult VirtualTurnResult) {
	harness.adapter.RememberMessage(connectors.VisibleContextMessage{Speaker: "user", SpeakerCallingName: "샘플 님", SpeakerHandle: "dongha", Text: virtualTurn.Prompt})
	if !turnResult.DidReply {
		return
	}
	harness.adapter.RememberMessage(connectors.VisibleContextMessage{Speaker: "assistant", SpeakerCallingName: "김인턴", SpeakerHandle: "internkim", Text: turnResult.FinishMessage})
}

func virtualRequestRecorder(languageModel llm.LanguageModelProvider) virtualLanguageModelRequestRecorder {
	recorder, _ := languageModel.(virtualLanguageModelRequestRecorder)
	return recorder
}

func virtualCallRecorder(languageModel llm.LanguageModelProvider) virtualLanguageModelCallRecorder {
	recorder, _ := languageModel.(virtualLanguageModelCallRecorder)
	return recorder
}

func (harness *VirtualSessionHarness) modelCallsSince(startIndex int) []VirtualLanguageModelCallEvent {
	if harness.callRecorder == nil {
		return nil
	}
	return harness.callRecorder.CallsSince(startIndex)
}

func (harness *VirtualSessionHarness) assertTurnResult(virtualTurn VirtualTurn, turnResult VirtualTurnResult) error {
	if harness.scenario.FailOnLanguageModelError {
		if errorValue := assertLanguageModelCallsSucceeded(turnResult); errorValue != nil {
			return errorValue
		}
	}
	if harness.scenario.UseLooseAssertions {
		return assertLooseTurnResult(virtualTurn, turnResult)
	}
	return assertTurnResult(harness.workspacePath, virtualTurn, turnResult)
}

func assertLooseTurnResult(virtualTurn VirtualTurn, turnResult VirtualTurnResult) error {
	if errorValue := assertResponseExpectation(virtualTurn, turnResult); errorValue != nil {
		return errorValue
	}
	if turnResult.TaskRunID != "" {
		switch turnResult.TaskStatus {
		case task.TaskStatusPlanned, task.TaskStatusRunning, task.TaskStatusInterrupted:
			return fmt.Errorf("expected terminal or waiting task status, got %s", turnResult.TaskStatus)
		}
	}
	return assertStructuralTurnExpectations(virtualTurn, turnResult)
}

func informationalAssertionResults(virtualTurn VirtualTurn, turnResult VirtualTurnResult) []VirtualInformationalAssertion {
	results := []VirtualInformationalAssertion{}
	for _, toolName := range virtualTurn.ExpectedToolCalls {
		results = append(results, VirtualInformationalAssertion{
			Name:      "expected tool call " + toolName,
			Satisfied: requestedToolCallPresent(turnResult.Events, toolName),
			Detail:    toolName,
		})
	}
	if len(virtualTurn.ExpectedAnyToolCalls) > 0 {
		foundToolCall := ""
		for _, toolName := range virtualTurn.ExpectedAnyToolCalls {
			if requestedToolCallPresent(turnResult.Events, toolName) {
				foundToolCall = toolName
				break
			}
		}
		results = append(results, VirtualInformationalAssertion{
			Name:      "expected any tool call",
			Satisfied: foundToolCall != "",
			Detail:    foundToolCall,
		})
	}
	for toolName, expectedCount := range virtualTurn.ExpectedToolCallCounts {
		actualCount := countRequestedToolCalls(turnResult.Events, toolName)
		results = append(results, VirtualInformationalAssertion{
			Name:      "expected tool call count " + toolName,
			Satisfied: actualCount == expectedCount,
			Detail:    fmt.Sprintf("expected=%d actual=%d", expectedCount, actualCount),
		})
	}
	for _, fragment := range virtualTurn.ExpectedReplyFragments {
		results = append(results, VirtualInformationalAssertion{
			Name:      "expected reply fragment",
			Satisfied: strings.Contains(turnResult.FinishMessage, fragment),
			Detail:    fragment,
		})
	}
	for _, expectedEventCount := range virtualTurn.ExpectedEventCounts {
		actualCount := countEventsWithFragment(turnResult.Events, expectedEventCount.Name, expectedEventCount.BodyFragment)
		results = append(results, VirtualInformationalAssertion{
			Name:      "expected event count " + expectedEventCount.Name,
			Satisfied: actualCount == expectedEventCount.Count,
			Detail:    fmt.Sprintf("expected=%d actual=%d fragment=%q", expectedEventCount.Count, actualCount, expectedEventCount.BodyFragment),
		})
	}
	return results
}

func assertTurnResult(workspacePath string, virtualTurn VirtualTurn, turnResult VirtualTurnResult) error {
	if errorValue := assertResponseExpectation(virtualTurn, turnResult); errorValue != nil {
		return errorValue
	}
	for _, skillName := range virtualTurn.ExpectedSelectedSkills {
		if !selectedSkillDecisionPresent(turnResult.Events, skillName) {
			return fmt.Errorf("expected selected skill %q; events: %s", skillName, summarizeEvents(turnResult.Events))
		}
	}
	for _, toolName := range virtualTurn.ExpectedToolCalls {
		if !requestedToolCallPresent(turnResult.Events, toolName) {
			return fmt.Errorf("expected requested tool %q; events: %s", toolName, summarizeEvents(turnResult.Events))
		}
	}
	if len(virtualTurn.ExpectedAnyToolCalls) > 0 {
		foundToolCall := false
		for _, toolName := range virtualTurn.ExpectedAnyToolCalls {
			if requestedToolCallPresent(turnResult.Events, toolName) {
				foundToolCall = true
				break
			}
		}
		if !foundToolCall {
			return fmt.Errorf("expected at least one requested tool call from %v; events: %s", virtualTurn.ExpectedAnyToolCalls, summarizeEvents(turnResult.Events))
		}
	}
	for _, eventName := range virtualTurn.ExpectedEvents {
		if !eventsContain(turnResult.Events, eventName, "") {
			return fmt.Errorf("expected event %q; events: %s", eventName, summarizeEvents(turnResult.Events))
		}
	}
	for toolName, expectedCount := range virtualTurn.ExpectedToolCallCounts {
		actualCount := countRequestedToolCalls(turnResult.Events, toolName)
		if expectedCount == 0 && actualCount != 0 {
			return fmt.Errorf("expected no requested %s calls, got %d; events: %s", toolName, actualCount, summarizeEvents(turnResult.Events))
		}
		if expectedCount > 0 && actualCount == 0 {
			return fmt.Errorf("expected a requested %s call; events: %s", toolName, summarizeEvents(turnResult.Events))
		}
	}
	for _, expectedEventCount := range virtualTurn.ExpectedEventCounts {
		actualCount := countEventsWithFragment(turnResult.Events, expectedEventCount.Name, expectedEventCount.BodyFragment)
		if expectedEventCount.Count == 0 && actualCount != 0 {
			return fmt.Errorf("expected no events %s containing %q, got %d; events: %s", expectedEventCount.Name, expectedEventCount.BodyFragment, actualCount, summarizeEvents(turnResult.Events))
		}
		if expectedEventCount.Count > 0 && actualCount == 0 {
			return fmt.Errorf("expected an event %s containing %q; events: %s", expectedEventCount.Name, expectedEventCount.BodyFragment, summarizeEvents(turnResult.Events))
		}
	}
	for _, suffix := range virtualTurn.ExpectedAttachments {
		attachment, isFound := findAttachmentWithSuffix(turnResult.Attachments, suffix)
		if !isFound {
			return fmt.Errorf("expected attachment suffix %q, got %+v; events: %s", suffix, turnResult.Attachments, summarizeEvents(turnResult.Events))
		}
		if errorValue := validateAttachmentContent(workspacePath, attachment, suffix); errorValue != nil {
			return errorValue
		}
	}
	for _, expectedAttachmentFile := range virtualTurn.ExpectedAttachmentFiles {
		if errorValue := validateAttachmentFileExpectation(workspacePath, turnResult.Attachments, expectedAttachmentFile); errorValue != nil {
			return errorValue
		}
	}
	for _, expectedWorkspaceFile := range virtualTurn.ExpectedWorkspaceFiles {
		if errorValue := validateExpectedWorkspaceFile(workspacePath, expectedWorkspaceFile); errorValue != nil {
			return errorValue
		}
	}
	for _, forbiddenWorkspaceFile := range virtualTurn.ForbiddenWorkspaceFiles {
		if errorValue := validateForbiddenWorkspaceFile(workspacePath, forbiddenWorkspaceFile); errorValue != nil {
			return errorValue
		}
	}
	for _, fragment := range virtualTurn.ExpectedModelContexts {
		if !strings.Contains(turnResult.ModelContext, fragment) {
			return fmt.Errorf("expected model context fragment %q", fragment)
		}
	}
	for _, fragment := range virtualTurn.ForbiddenModelContexts {
		if strings.Contains(turnResult.ModelContext, fragment) {
			return fmt.Errorf("forbidden model context fragment %q found", fragment)
		}
	}
	if strings.TrimSpace(virtualTurn.ExpectedReplyTargetID) != "" && turnResult.ReplyTargetID != strings.TrimSpace(virtualTurn.ExpectedReplyTargetID) {
		return fmt.Errorf("expected reply target %q, got %q", strings.TrimSpace(virtualTurn.ExpectedReplyTargetID), turnResult.ReplyTargetID)
	}
	for _, fragment := range virtualTurn.ExpectedReplyFragments {
		if !strings.Contains(turnResult.FinishMessage, fragment) {
			return fmt.Errorf("expected reply fragment %q in %q", fragment, turnResult.FinishMessage)
		}
	}
	for _, fragment := range virtualTurn.ForbiddenReplyFragments {
		if strings.Contains(turnResult.FinishMessage, fragment) {
			return fmt.Errorf("forbidden reply fragment %q found in %q", fragment, turnResult.FinishMessage)
		}
	}
	if virtualTurn.MinimumReplyLength > 0 && len([]rune(turnResult.FinishMessage)) < virtualTurn.MinimumReplyLength {
		return fmt.Errorf("expected reply length >= %d, got %d: %q", virtualTurn.MinimumReplyLength, len([]rune(turnResult.FinishMessage)), turnResult.FinishMessage)
	}
	return assertStructuralTurnExpectations(virtualTurn, turnResult)
}

func assertLanguageModelCallsSucceeded(turnResult VirtualTurnResult) error {
	for _, event := range turnResult.LanguageModelCallEvents {
		if !event.IsError || event.WasCorrected {
			continue
		}
		return fmt.Errorf("language model call failed: %s", strings.TrimSpace(strings.Join([]string{event.Kind, event.SchemaName, event.Error}, " ")))
	}
	return nil
}

func assertResponseExpectation(virtualTurn VirtualTurn, turnResult VirtualTurnResult) error {
	expectation := normalizedResponseExpectation(virtualTurn.ExpectedResponse)
	switch expectation {
	case VirtualResponseReply:
		if !virtualTurnResultDidReply(turnResult) {
			return fmt.Errorf("expected a text reply, got taskRunID=%q ignored=%v reason=%q", turnResult.TaskRunID, turnResult.Ignored, turnResult.Reason)
		}
	case VirtualResponseIgnore:
		if turnResult.TaskRunID != "" || turnResult.DidReply || len(turnResult.Reactions) > 0 {
			return fmt.Errorf("expected silent ignore, got taskRunID=%q reply=%v reactions=%v", turnResult.TaskRunID, turnResult.DidReply, turnResult.Reactions)
		}
	case VirtualResponseIgnoreOrReact:
		if turnResult.TaskRunID != "" || turnResult.DidReply {
			return fmt.Errorf("expected ignore or reaction only, got taskRunID=%q reply=%v", turnResult.TaskRunID, turnResult.DidReply)
		}
	case VirtualResponseReact:
		if turnResult.TaskRunID != "" || turnResult.DidReply || len(turnResult.Reactions) == 0 {
			return fmt.Errorf("expected reaction only, got taskRunID=%q reply=%v reactions=%v", turnResult.TaskRunID, turnResult.DidReply, turnResult.Reactions)
		}
	case VirtualResponseBackgroundAction:
		if turnResult.TaskRunID == "" || turnResult.DidReply {
			return fmt.Errorf("expected background action without reply, got taskRunID=%q reply=%v", turnResult.TaskRunID, turnResult.DidReply)
		}
	default:
		return fmt.Errorf("unknown expected response %q", expectation)
	}
	return nil
}

func virtualTurnResultDidReply(turnResult VirtualTurnResult) bool {
	return turnResult.DidReply || strings.TrimSpace(turnResult.FinishMessage) != ""
}

func normalizedResponseExpectation(expectation VirtualResponseExpectation) VirtualResponseExpectation {
	if strings.TrimSpace(string(expectation)) == "" {
		return VirtualResponseReply
	}
	return VirtualResponseExpectation(strings.TrimSpace(string(expectation)))
}

func assertStructuralTurnExpectations(virtualTurn VirtualTurn, turnResult VirtualTurnResult) error {
	if errorValue := assertEventSubsequence(turnResult.Events, virtualTurn.ExpectedSequence); errorValue != nil {
		return errorValue
	}
	for _, forbiddenEvent := range virtualTurn.ForbiddenEvents {
		if eventsContain(turnResult.Events, forbiddenEvent, "") {
			return fmt.Errorf("forbidden event %q present; events: %s", forbiddenEvent, summarizeEvents(turnResult.Events))
		}
	}
	for _, fragment := range virtualTurn.ExpectedCheckpointReplies {
		if !checkpointRepliesContain(turnResult.Events, fragment) {
			return fmt.Errorf("expected checkpoint reply fragment %q; checkpoint replies: %v", fragment, checkpointReplyMessages(turnResult.Events))
		}
	}
	if strings.TrimSpace(string(virtualTurn.ExpectedTaskStatus)) != "" && turnResult.TaskStatus != virtualTurn.ExpectedTaskStatus {
		return fmt.Errorf("expected task status %q, got %q", virtualTurn.ExpectedTaskStatus, turnResult.TaskStatus)
	}
	return nil
}

func assertEventSubsequence(events []task.TaskEvent, expectedNames []string) error {
	if len(expectedNames) == 0 {
		return nil
	}
	matchIndex := 0
	for _, event := range events {
		if matchIndex < len(expectedNames) && event.Name == expectedNames[matchIndex] {
			matchIndex++
		}
	}
	if matchIndex < len(expectedNames) {
		return fmt.Errorf("expected event subsequence %v, matched %d; events: %s", expectedNames, matchIndex, summarizeEvents(events))
	}
	return nil
}

func checkpointReplyMessages(events []task.TaskEvent) []string {
	messages := []string{}
	for _, event := range events {
		if event.Name != "agent.checkpoint.sent" {
			continue
		}
		message := agent.CheckpointReplyMessage(event.Body)
		if message != "" {
			messages = append(messages, message)
		}
	}
	return messages
}

func checkpointRepliesContain(events []task.TaskEvent, fragment string) bool {
	for _, message := range checkpointReplyMessages(events) {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func validateExpectedWorkspaceFile(workspacePath string, expectation VirtualWorkspaceFileExpectation) error {
	pattern := filepath.Join(workspacePath, expectation.PathGlob)
	matches, errorValue := filepath.Glob(pattern)
	if errorValue != nil {
		return errorValue
	}
	if len(matches) == 0 {
		return fmt.Errorf("expected workspace file matching %q", expectation.PathGlob)
	}
	sort.Strings(matches)
	content, errorValue := os.ReadFile(matches[len(matches)-1])
	if errorValue != nil {
		return errorValue
	}
	document := string(content)
	for _, fragment := range expectation.ContainsFragments {
		if !strings.Contains(document, fragment) {
			return fmt.Errorf("expected %s to contain %q", matches[len(matches)-1], fragment)
		}
	}
	for _, fragment := range expectation.ForbiddenFragments {
		if strings.Contains(document, fragment) {
			return fmt.Errorf("expected %s not to contain %q", matches[len(matches)-1], fragment)
		}
	}
	for fragment, expectedCount := range expectation.FragmentCounts {
		if actualCount := strings.Count(document, fragment); actualCount != expectedCount {
			return fmt.Errorf("expected %s to contain %q %d times, got %d", matches[len(matches)-1], fragment, expectedCount, actualCount)
		}
	}
	return nil
}

func validateForbiddenWorkspaceFile(workspacePath string, pathGlob string) error {
	matches, errorValue := filepath.Glob(filepath.Join(workspacePath, pathGlob))
	if errorValue != nil {
		return errorValue
	}
	if len(matches) > 0 {
		return fmt.Errorf("forbidden workspace file matching %q remains: %s", pathGlob, matches[0])
	}
	return nil
}

func summarizeEvents(events []task.TaskEvent) string {
	parts := []string{}
	for _, event := range events {
		body := event.Body
		if len(body) > 160 {
			body = body[:160] + "..."
		}
		parts = append(parts, event.Name+"="+body)
	}
	return strings.Join(parts, " | ")
}

func eventsContain(events []task.TaskEvent, name string, bodyFragment string) bool {
	for _, event := range events {
		if event.Name == name && strings.Contains(event.Body, bodyFragment) {
			return true
		}
	}
	return false
}

func selectedSkillDecisionPresent(events []task.TaskEvent, skillName string) bool {
	for _, event := range events {
		if event.Name != "agent.instructions_loaded" {
			continue
		}
		var body struct {
			SkillDecisions []agent.SkillSelectionDecision `json:"skillDecisions"`
		}
		if json.Unmarshal([]byte(event.Body), &body) != nil {
			continue
		}
		for _, skillDecision := range body.SkillDecisions {
			if skillDecision.Name == skillName && skillDecision.Status == "selected" {
				return true
			}
		}
	}
	return false
}

func countEvents(events []task.TaskEvent, name string) int {
	count := 0
	for _, event := range events {
		if event.Name == name {
			count++
		}
	}
	return count
}

func requestedToolCallPresent(events []task.TaskEvent, toolName string) bool {
	return eventsContain(events, "tool."+toolName+".requested", toolName)
}

func countRequestedToolCalls(events []task.TaskEvent, toolName string) int {
	return countEvents(events, "tool."+toolName+".requested")
}

func countEventsWithFragment(events []task.TaskEvent, name string, bodyFragment string) int {
	count := 0
	for _, event := range events {
		if event.Name != name {
			continue
		}
		if bodyFragment != "" && !strings.Contains(event.Body, bodyFragment) {
			continue
		}
		count++
	}
	return count
}

func findAttachmentWithSuffix(attachments []agent.FileAttachment, suffix string) (agent.FileAttachment, bool) {
	for _, attachment := range attachments {
		if strings.HasSuffix(attachment.Filename, suffix) || strings.HasSuffix(attachment.DevicePath, suffix) {
			return attachment, true
		}
	}
	return agent.FileAttachment{}, false
}

func validateAttachmentContent(workspacePath string, attachment agent.FileAttachment, suffix string) error {
	path := localAttachmentPath(workspacePath, attachment)
	switch suffix {
	case ".docx":
		return validateDOCXAttachment(path, attachment)
	case ".pptx":
		return validatePPTXAttachment(path, attachment)
	case ".pdf":
		return validateFilePrefix(path, "%PDF")
	case ".html":
		return validateFileContains(path, "<html")
	case "-notes.txt":
		return validateNonEmptyFile(path)
	default:
		return validateNonEmptyFile(path)
	}
}

func validateAttachmentFileExpectation(workspacePath string, attachments []agent.FileAttachment, expectation VirtualAttachmentFileExpectation) error {
	attachment, isFound := findAttachmentWithSuffix(attachments, expectation.Suffix)
	if !isFound {
		return fmt.Errorf("expected attachment suffix %q, got %+v", expectation.Suffix, attachments)
	}
	if errorValue := validateAttachmentContent(workspacePath, attachment, expectation.Suffix); errorValue != nil {
		return errorValue
	}
	path := localAttachmentPath(workspacePath, attachment)
	for _, fragment := range expectation.ContainsFragments {
		if errorValue := validateFileContains(path, fragment); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func localAttachmentPath(workspacePath string, attachment agent.FileAttachment) string {
	devicePath := strings.TrimSpace(attachment.DevicePath)
	if devicePath == "/workspace" {
		return workspacePath
	}
	if strings.HasPrefix(devicePath, "/workspace/") {
		return filepath.Join(workspacePath, strings.TrimPrefix(devicePath, "/workspace/"))
	}
	return devicePath
}

func validatePPTXAttachment(path string, attachment agent.FileAttachment) error {
	reader, errorValue := zip.OpenReader(path)
	if errorValue != nil {
		return fmt.Errorf("attachment %s is not a valid pptx zip: %w", attachment.DevicePath, errorValue)
	}
	defer reader.Close()
	requiredEntries := map[string]bool{
		"[Content_Types].xml":             false,
		"ppt/presentation.xml":            false,
		"ppt/slides/slide1.xml":           false,
		"ppt/_rels/presentation.xml.rels": false,
	}
	for _, file := range reader.File {
		if _, isRequired := requiredEntries[file.Name]; isRequired {
			requiredEntries[file.Name] = true
		}
	}
	for name, isFound := range requiredEntries {
		if !isFound {
			return fmt.Errorf("attachment %s is missing pptx entry %s", attachment.DevicePath, name)
		}
	}
	return nil
}

func validateDOCXAttachment(path string, attachment agent.FileAttachment) error {
	reader, errorValue := zip.OpenReader(path)
	if errorValue != nil {
		return fmt.Errorf("attachment %s is not a valid docx zip: %w", attachment.DevicePath, errorValue)
	}
	defer reader.Close()
	requiredEntries := []struct {
		name             string
		root             string
		requiredChildren []string
	}{
		{name: "[Content_Types].xml", root: "Types", requiredChildren: []string{"Override"}},
		{name: "word/document.xml", root: "document", requiredChildren: []string{"body"}},
		{name: "word/_rels/document.xml.rels", root: "Relationships", requiredChildren: []string{"Relationship"}},
	}
	for _, requiredEntry := range requiredEntries {
		content, errorValue := readDOCXEntry(reader, requiredEntry.name)
		if errorValue != nil {
			return fmt.Errorf("attachment %s is missing docx entry %s", attachment.DevicePath, requiredEntry.name)
		}
		if errorValue := validateDOCXXML(content, requiredEntry.root, requiredEntry.requiredChildren); errorValue != nil {
			return fmt.Errorf("attachment %s has invalid docx entry %s: %w", attachment.DevicePath, requiredEntry.name, errorValue)
		}
	}
	return nil
}

func readDOCXEntry(reader *zip.ReadCloser, name string) ([]byte, error) {
	for _, file := range reader.File {
		if file.Name != name {
			continue
		}
		handle, errorValue := file.Open()
		if errorValue != nil {
			return nil, errorValue
		}
		content, readError := io.ReadAll(handle)
		closeError := handle.Close()
		if readError != nil {
			return nil, readError
		}
		return content, closeError
	}
	return nil, os.ErrNotExist
}

type docxXMLDocument struct {
	XMLName  xml.Name
	Children []docxXMLChild `xml:",any"`
}

type docxXMLChild struct {
	XMLName xml.Name
}

func validateDOCXXML(content []byte, expectedRoot string, requiredChildren []string) error {
	document := docxXMLDocument{}
	if errorValue := xml.Unmarshal(content, &document); errorValue != nil {
		return errorValue
	}
	if document.XMLName.Local != expectedRoot {
		return fmt.Errorf("expected XML root %s, got %s", expectedRoot, document.XMLName.Local)
	}
	childrenSeen := map[string]bool{}
	for _, child := range document.Children {
		for _, requiredChild := range requiredChildren {
			if child.XMLName.Local == requiredChild {
				childrenSeen[requiredChild] = true
			}
		}
	}
	for _, requiredChild := range requiredChildren {
		if childrenSeen[requiredChild] {
			continue
		}
		return fmt.Errorf("XML root %s has no %s child", expectedRoot, requiredChild)
	}
	return nil
}

func validateFilePrefix(path string, prefix string) error {
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return errorValue
	}
	if !strings.HasPrefix(string(content), prefix) {
		return fmt.Errorf("attachment %s does not start with %q", path, prefix)
	}
	return nil
}

func validateFileContains(path string, fragment string) error {
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return errorValue
	}
	if !strings.Contains(strings.ToLower(string(content)), strings.ToLower(fragment)) {
		return fmt.Errorf("attachment %s does not contain %q", path, fragment)
	}
	return nil
}

func validateNonEmptyFile(path string) error {
	information, errorValue := os.Stat(path)
	if errorValue != nil {
		return errorValue
	}
	if information.Size() <= 0 {
		return fmt.Errorf("attachment %s is empty", path)
	}
	return nil
}

func prepareArtifactDirectory(scenario VirtualSessionScenario) (string, error) {
	rootPath := strings.TrimSpace(scenario.ArtifactDirectoryPath)
	if rootPath == "" {
		return os.MkdirTemp("", "blueclaw-e2e-*")
	}
	absoluteRootPath, errorValue := filepath.Abs(rootPath)
	if errorValue != nil {
		return "", errorValue
	}
	artifactPath := filepath.Join(absoluteRootPath, scenario.Name+"-"+time.Now().UTC().Format("20060102T150405.000000000"))
	return artifactPath, os.MkdirAll(artifactPath, 0700)
}

func allowedToolsOrDefault(allowedTools []string) []string {
	if len(allowedTools) > 0 {
		return append([]string{}, allowedTools...)
	}
	return []string{"conversation.history", "memory.search", "terminal.run", "ask.input", "file.read", "file.write", "file.edit", "file.deliver"}
}

func terminalConfiguration(workspacePath string) config.TerminalConfiguration {
	return config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspacePath,
		TimeoutSecond:         120,
		OutputMaxBytes:        32768,
		SessionMaxCount:       2,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
	}
}

func testPolicyProjection() policy.PolicyProjection {
	policyDocument := policy.PolicyDocument{
		People: []policy.PersonPolicy{{
			PersonID:          "person-1",
			DisplayName:       "샘플",
			Emails:            []string{"dongha@example.com"},
			Circles:           []string{"staff"},
			SecurityLevelRank: 0,
			GrantedClasses:    []string{},
		}},
		Circles: []policy.CirclePolicy{{
			CircleID:               "staff",
			DisplayName:            "Staff",
			WorkspaceDirectoryPath: "/workspace/circles/staff",
		}},
		Channels: []policy.ChannelPolicy{{
			Platform:                 "virtual",
			ExternalConversationID:   "virtual-conversation-1",
			ConversationType:         "test",
			DisplayName:              "Virtual Session",
			DefaultSecurityLevelRank: 0,
			DefaultRequiredClasses:   []string{},
			IsCollectEnabled:         true,
			IsReplyEnabled:           true,
		}},
		Retention: policy.RetentionPolicy{RawEventDays: 30},
	}
	return policy.PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(policyDocument)
}

type virtualAdapter struct {
	mutex         sync.Mutex
	workspacePath string
	replies       map[string]virtualReply
	reactions     []connectors.ReactionTarget
	history       []connectors.VisibleContextMessage
}

type virtualReply struct {
	target connectors.ReplyTarget
	reply  connectors.OutboundReply
}

func (adapter *virtualAdapter) Name() string { return "virtual" }

func (adapter *virtualAdapter) ParseHTTPEvent(context.Context, *http.Request) (connectors.HTTPParseResult, error) {
	return connectors.HTTPParseResult{}, errors.New("virtual adapter does not parse http")
}

func (adapter *virtualAdapter) ParseRealtimeEvent(context.Context, []byte, string) (connectors.PlatformInboundEvent, bool, error) {
	return connectors.PlatformInboundEvent{}, false, errors.New("virtual adapter does not parse realtime")
}

func (adapter *virtualAdapter) ResolveIdentity(context.Context, string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{
		Platform:       "virtual",
		ExternalUserID: "user-1",
		Email:          "dongha@example.com",
		DisplayName:    "샘플",
	}, nil
}

func (adapter *virtualAdapter) StartProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter *virtualAdapter) StopProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter *virtualAdapter) SendReply(_ context.Context, target connectors.ReplyTarget, reply connectors.OutboundReply) (string, error) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	if adapter.replies == nil {
		adapter.replies = map[string]virtualReply{}
	}
	dispatchID := fmt.Sprintf("virtual-dispatch-%03d", len(adapter.replies)+1)
	adapter.replies[dispatchID] = virtualReply{target: target, reply: reply}
	return dispatchID, nil
}

func (adapter *virtualAdapter) FindReply(dispatchID string) (connectors.OutboundReply, connectors.ReplyTarget, bool) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	reply, isFound := adapter.replies[dispatchID]
	return reply.reply, reply.target, isFound
}

func (adapter *virtualAdapter) AddReaction(_ context.Context, target connectors.ReactionTarget) error {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	adapter.reactions = append(adapter.reactions, target)
	return nil
}

func (adapter *virtualAdapter) ReactionCount() int {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	return len(adapter.reactions)
}

func (adapter *virtualAdapter) ReactionsSince(startIndex int) []connectors.ReactionTarget {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	if startIndex < 0 || startIndex > len(adapter.reactions) {
		startIndex = 0
	}
	return append([]connectors.ReactionTarget{}, adapter.reactions[startIndex:]...)
}

func (adapter *virtualAdapter) RememberMessage(message connectors.VisibleContextMessage) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	adapter.history = append(adapter.history, message)
}

func (adapter *virtualAdapter) VisibleHistory() []connectors.VisibleContextMessage {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	return append([]connectors.VisibleContextMessage{}, adapter.history...)
}

func (adapter *virtualAdapter) FetchHistory(_ context.Context, historyCursor string, limit int) (connectors.VisibleContext, error) {
	historyCursor = strings.TrimSpace(historyCursor)
	if historyCursor == "" {
		return connectors.VisibleContext{}, nil
	}
	messages := adapter.VisibleHistory()
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	startIndex := max(0, len(messages)-limit)
	return connectors.VisibleContext{
		Messages:      messages[startIndex:],
		HasMoreBefore: startIndex > 0,
		HistoryCursor: historyCursor,
	}, nil
}

func (adapter *virtualAdapter) ImportInputAttachments(_ context.Context, request connectors.InputAttachmentImportRequest) (connectors.InputAttachmentImportResult, error) {
	attachments := []connectors.InputAttachment{}
	for _, attachment := range request.InputAttachments {
		importedAttachment, errorValue := adapter.importInputAttachment(request.TargetDirectoryPath, attachment)
		if errorValue != nil {
			return connectors.InputAttachmentImportResult{}, errorValue
		}
		attachments = append(attachments, importedAttachment)
	}
	return connectors.InputAttachmentImportResult{
		InputAttachments: attachments,
		InputParts:       virtualInputParts(attachments),
	}, nil
}

func (adapter *virtualAdapter) importInputAttachment(targetDirectoryPath string, attachment connectors.InputAttachment) (connectors.InputAttachment, error) {
	filename := firstNonEmptyVirtualString(attachment.Filename, attachment.FileID, "attachment.bin")
	virtualPath := strings.TrimRight(targetDirectoryPath, "/") + "/" + filename
	hostPath := filepath.Join(adapter.workspacePath, strings.TrimPrefix(virtualPath, "/workspace/"))
	content := virtualAttachmentContent(attachment)
	if errorValue := os.MkdirAll(filepath.Dir(hostPath), 0700); errorValue != nil {
		return connectors.InputAttachment{}, errorValue
	}
	if errorValue := os.WriteFile(hostPath, content, 0600); errorValue != nil {
		return connectors.InputAttachment{}, errorValue
	}
	attachment.Path = virtualPath
	attachment.IsAvailable = true
	attachment.SizeBytes = int64(len(content))
	attachment.ContentType = firstNonEmptyVirtualString(attachment.ContentType, "application/octet-stream")
	return attachment, nil
}

func virtualAttachmentContent(attachment connectors.InputAttachment) []byte {
	contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
	if strings.Contains(contentType, "html") || strings.HasSuffix(strings.ToLower(strings.TrimSpace(attachment.Filename)), ".html") {
		return []byte("<!doctype html><html><body><h1>Virtual HTML Title</h1><p>Automation workflow content</p></body></html>")
	}
	if strings.HasPrefix(contentType, "image/") {
		return []byte("virtual-image")
	}
	return []byte("virtual-file")
}

func virtualInputParts(attachments []connectors.InputAttachment) []agent.AgentPart {
	parts := []agent.AgentPart{}
	for _, attachment := range attachments {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.ContentType)), "image/") {
			continue
		}
		parts = append(parts, agent.AgentPart{
			Type: agent.AgentPartTypeImage,
			Image: &agent.AgentImagePart{
				MimeType:   attachment.ContentType,
				DataBase64: "dmlydHVhbC1pbWFnZQ==",
				Path:       attachment.Path,
				Filename:   attachment.Filename,
			},
			Source: agent.AgentPartSource{
				Platform:  attachment.Platform,
				MessageID: attachment.MessageID,
				FileID:    attachment.FileID,
			},
		})
	}
	return parts
}

func firstNonEmptyVirtualString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

type virtualMemoryStore struct {
	mutex sync.Mutex
	facts []memory.MemoryFact
}

type virtualMemoryUpdateQueue struct {
	memoryService *memory.MemoryService
}

type virtualTaskScheduleRepository struct {
	mutex         sync.Mutex
	taskSchedules []task.TaskSchedule
}

func (queue virtualMemoryUpdateQueue) Enqueue(job memory.MemoryUpdateJob) (memory.MemoryUpdateAccepted, error) {
	if queue.memoryService == nil {
		return memory.MemoryUpdateAccepted{}, errors.New("memory update queue is unavailable")
	}
	jobID := strings.TrimSpace(job.JobID)
	if jobID == "" {
		jobID = "virtual-memory-update-" + time.Now().UTC().Format("20060102150405.000000000")
	}
	job.JobID = jobID
	_, errorValue := queue.memoryService.AddEpisode(context.Background(), memory.MemoryEpisode{
		EpisodeID:       job.JobID,
		Platform:        job.Platform,
		ConversationID:  job.ConversationID,
		SenderPersonID:  job.SenderPersonID,
		Prompt:          job.Content,
		OccurredAt:      job.OccurredAt,
		Namespaces:      []memory.MemoryNamespace{job.Namespace},
		Source:          "memory.remember",
		SourceReference: job.SourceReference,
	})
	if errorValue != nil {
		return memory.MemoryUpdateAccepted{}, errorValue
	}
	return memory.MemoryUpdateAccepted{Accepted: true, JobID: job.JobID}, nil
}

func (repository *virtualTaskScheduleRepository) UpsertTaskSchedule(taskSchedule task.TaskSchedule) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	taskSchedule.TaskScheduleID = fmt.Sprintf("virtual-schedule-%03d", len(repository.taskSchedules)+1)
	repository.taskSchedules = append(repository.taskSchedules, taskSchedule)
	return nil
}

func (repository *virtualTaskScheduleRepository) UpdateTaskSchedule(request task.TaskScheduleUpdateRequest) (task.TaskScheduleUpdateResult, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	for index, taskSchedule := range repository.taskSchedules {
		if taskSchedule.TaskScheduleID != request.TaskScheduleID || taskSchedule.CreatorPersonID != request.RequesterPersonID || taskSchedule.NextRunAt == nil {
			continue
		}
		updatedTaskSchedule := taskSchedule
		var errorValue error
		if request.UpdateTaskSchedule != nil {
			updatedTaskSchedule, errorValue = request.UpdateTaskSchedule(taskSchedule)
			if errorValue != nil {
				return task.TaskScheduleUpdateResult{}, errorValue
			}
		}
		repository.taskSchedules[index] = updatedTaskSchedule
		return task.TaskScheduleUpdateResult{TaskSchedule: updatedTaskSchedule, IsFound: true}, nil
	}
	return task.TaskScheduleUpdateResult{}, nil
}

func (repository *virtualTaskScheduleRepository) TaskSchedules() []task.TaskSchedule {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return append([]task.TaskSchedule{}, repository.taskSchedules...)
}

func (repository *virtualTaskScheduleRepository) ListTaskSchedules(request task.TaskScheduleListRequest) (task.TaskScheduleListResult, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	taskSchedules := []task.TaskSchedule{}
	for _, taskSchedule := range repository.taskSchedules {
		if request.CreatorPersonID != "" && taskSchedule.CreatorPersonID != request.CreatorPersonID {
			continue
		}
		if !request.IncludeExpired && taskSchedule.NextRunAt == nil {
			continue
		}
		taskSchedules = append(taskSchedules, taskSchedule)
	}
	pageSize := request.PageSize
	if pageSize <= 0 || pageSize > len(taskSchedules) {
		pageSize = len(taskSchedules)
	}
	return task.TaskScheduleListResult{TaskSchedules: append([]task.TaskSchedule{}, taskSchedules[:pageSize]...), TotalCount: len(taskSchedules), Page: 1, PageSize: pageSize}, nil
}

func (repository *virtualTaskScheduleRepository) ClaimDueTaskSchedules(int, time.Duration, time.Time, string) ([]task.TaskSchedule, error) {
	return nil, nil
}

func (repository *virtualTaskScheduleRepository) MarkTaskScheduleSucceeded(task.TaskSchedule) error {
	return nil
}

func (repository *virtualTaskScheduleRepository) MarkTaskScheduleFailed(task.TaskSchedule, string, time.Time) error {
	return nil
}

func (repository *virtualTaskScheduleRepository) ExpireTaskSchedule(task.TaskSchedule, string, time.Time) error {
	return nil
}

func (repository *virtualTaskScheduleRepository) CancelTaskSchedules(request task.TaskScheduleCancelRequest) (task.TaskScheduleCancelResult, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	cancelledTaskSchedules := []task.TaskSchedule{}
	for index, taskSchedule := range repository.taskSchedules {
		if taskSchedule.CreatorPersonID != request.RequesterPersonID || taskSchedule.NextRunAt == nil {
			continue
		}
		repository.taskSchedules[index].ExpiresAt = &request.CancelledAt
		repository.taskSchedules[index].NextRunAt = nil
		cancelledTaskSchedules = append(cancelledTaskSchedules, repository.taskSchedules[index])
	}
	return task.TaskScheduleCancelResult{TaskSchedules: cancelledTaskSchedules}, nil
}

func newVirtualMemoryStore(initialFacts []memory.MemoryFact) *virtualMemoryStore {
	return &virtualMemoryStore{facts: append([]memory.MemoryFact{}, initialFacts...)}
}

func (store *virtualMemoryStore) AddEpisode(_ context.Context, episode memory.MemoryEpisode) (memory.MemoryIngestionResult, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	validAt := episode.OccurredAt
	if validAt.IsZero() {
		validAt = time.Now().UTC()
	}
	for _, namespace := range episode.Namespaces {
		store.facts = append(store.facts, memory.MemoryFact{
			FactID:            episode.EpisodeID + ":" + namespace.NamespaceID,
			ScopeType:         namespace.ScopeType,
			NamespaceID:       namespace.NamespaceID,
			Content:           episode.Prompt,
			Score:             0.5,
			SourceEpisodeID:   episode.EpisodeID,
			SourceKind:        memory.MemorySourceKindFact,
			ValidAt:           validAt,
			SecurityLevelRank: namespace.SecurityLevelRank,
			RequiredClasses:   append([]string{}, namespace.RequiredClasses...),
		})
	}
	return memory.MemoryIngestionResult{EpisodeID: episode.EpisodeID, NamespaceCount: len(episode.Namespaces)}, nil
}

func (store *virtualMemoryStore) SearchFacts(_ context.Context, request memory.MemorySearchRequest) ([]memory.MemoryFact, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	namespaceByID := map[string]bool{}
	for _, namespace := range request.Namespaces {
		namespaceByID[namespace.NamespaceID] = true
	}
	candidates := []memory.MemoryFact{}
	for _, fact := range store.facts {
		if !namespaceByID[fact.NamespaceID] || request.ReaderSecurityLevelRank < fact.SecurityLevelRank {
			continue
		}
		candidates = append(candidates, fact)
	}
	sort.SliceStable(candidates, func(leftIndex int, rightIndex int) bool {
		leftScore := virtualRelevanceScore(candidates[leftIndex], request.Query)
		rightScore := virtualRelevanceScore(candidates[rightIndex], request.Query)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return candidates[leftIndex].ValidAt.After(candidates[rightIndex].ValidAt)
	})
	if request.Limit > 0 && len(candidates) > request.Limit {
		return append([]memory.MemoryFact{}, candidates[:request.Limit]...), nil
	}
	return append([]memory.MemoryFact{}, candidates...), nil
}

func virtualRelevanceScore(fact memory.MemoryFact, query string) float64 {
	score := fact.Score
	normalizedContent := strings.ToLower(fact.Content)
	for _, queryTerm := range strings.Fields(strings.ToLower(strings.TrimSpace(query))) {
		if strings.Contains(normalizedContent, queryTerm) {
			score += 0.25
		}
	}
	return score
}

func actionFinishMessage(reply string, evidence ...string) string {
	evidenceDocuments := []string{}
	for _, value := range evidence {
		parts := strings.Split(value, ":")
		if len(parts) != 3 {
			continue
		}
		evidenceDocuments = append(evidenceDocuments, `{"observationID":`+quote(parts[0])+`,"toolName":`+quote(parts[1])+`,"attachmentIndex":`+parts[2]+`}`)
	}
	return `{"action":"finish","message":` + quote(reply) + `,"completionSummary":` + quote(reply) + `,"replyParts":[{"type":"text","text":` + quote(reply) + `}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[` + strings.Join(evidenceDocuments, ",") + `]}`
}

func actionFinishWithReplyPart(summary string, replyPart string, evidence ...string) string {
	evidenceDocuments := []string{}
	for _, value := range evidence {
		parts := strings.Split(value, ":")
		if len(parts) != 3 {
			continue
		}
		evidenceDocuments = append(evidenceDocuments, `{"observationID":`+quote(parts[0])+`,"toolName":`+quote(parts[1])+`,"attachmentIndex":`+parts[2]+`}`)
	}
	return `{"action":"finish","message":` + quote(summary) + `,"completionSummary":` + quote(summary) + `,"replyParts":[{"type":"text","text":` + quote(replyPart) + `}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[` + strings.Join(evidenceDocuments, ",") + `]}`
}

func actionNoToolFallbackFinishMessage(reply string) string {
	return `{"action":"finish","message":` + quote(reply) + `,"completionSummary":` + quote(reply) + `,"replyParts":[{"type":"text","text":` + quote(reply) + `}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"failureResolution":"no_tool_fallback"}`
}

func actionFailMessage(reason string) string {
	return `{"action":"fail","reason":` + quote(reason) + `,"goalStatus":"blocked","goalSatisfied":false,"remainingWork":"The requested task could not complete.","failureResolution":"failure_report","usedFailureFacts":{"attempts":[{"toolName":"terminal.run","inputSummary":"printf 'permission denied blocked_by_captcha' >&2; exit 126","errorCode":"operation_failed","failureStage":"terminal_run","message":"errorCode=operation_failed; failureStage=terminal_run; exitCode=126; stderrTail=permission denied blocked_by_captcha"}],"budgetState":"failure_report_required"},"executionStateUpdate":{}}`
}

func actionCallTool(toolName string, input string) string {
	return `{"action":"continue","toolName":` + quote(toolName) + `,"toolInput":` + input + `}`
}

func actionCallToolWithMessage(toolName string, message string, input string) string {
	return `{"action":"continue","toolName":` + quote(toolName) + `,"message":` + quote(message) + `,"toolInput":` + input + `}`
}

func quote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
