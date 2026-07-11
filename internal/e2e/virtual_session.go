package e2e

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
)

type VirtualSessionScenario struct {
	Name                      string
	ProfileName               string
	ArtifactDirectoryPath     string
	LanguageModel             llm.LanguageModelProvider
	DisableScriptedModel      bool
	UseLooseAssertions        bool
	SkillDirectoryPaths       []string
	Skills                    []agent.SkillInstruction
	AllowedTools              []string
	CapabilityToolNames       []string
	CapabilityToolDescriptors []agentruntime.CapabilityToolDescriptor
	InitialToolNames          []string
	InitialMemory             []memory.MemoryFact
	RouterRequiredEvidence    []string
	RouterTaskLevel           string
	CodingTierVisionFallback  bool
	AddressingResponse        string
	RouterSiteEvidence        string
	TurnOptions               agent.TurnOptions
	ProgressWriter            io.Writer
	Turns                     []VirtualTurn
}

type VirtualTurn struct {
	Prompt                    string
	ConversationType          string
	ChannelID                 string
	ChannelName               string
	ReplyTargetID             string
	Addressing                connectors.AddressingMetadata
	InputAttachments          []connectors.InputAttachment
	ContextMessages           []connectors.VisibleContextMessage
	ContextMaterials          []connectors.InputAttachment
	ActionResponses           []string
	RouterRequiredEvidence    []string
	RouterSiteEvidence        string
	RouterApproval            string
	ExpectedSelectedSkills    []string
	ExpectedToolCalls         []string
	ExpectedEvents            []string
	ExpectedToolCallCounts    map[string]int
	ExpectedEventCounts       []VirtualEventCount
	ExpectedAttachments       []string
	ExpectedWorkspaceFiles    []VirtualWorkspaceFileExpectation
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
}

type VirtualSessionResult struct {
	ScenarioName          string
	ArtifactDirectoryPath string
	TurnResults           []VirtualTurnResult
	TaskSchedules         []task.TaskSchedule
}

type VirtualTurnResult struct {
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
	Kind             string `json:"kind"`
	SchemaName       string `json:"schemaName,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	LatencyMS        int64  `json:"latencyMs"`
	PromptBytes      int    `json:"promptBytes"`
	ContentBytes     int    `json:"contentBytes"`
	UsedFallback     bool   `json:"usedFallback,omitempty"`
	PromptTokens     int64  `json:"promptTokens,omitempty"`
	CompletionTokens int64  `json:"completionTokens,omitempty"`
	TotalTokens      int64  `json:"totalTokens,omitempty"`
	IsError          bool   `json:"isError,omitempty"`
	Error            string `json:"error,omitempty"`
}

type VirtualInformationalAssertion struct {
	Name      string
	Satisfied bool
	Detail    string
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
	history          []connectors.VisibleContextMessage
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
	base := &virtualObservedLanguageModel{provider: provider}
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
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	return len(languageModel.requests)
}

func (languageModel *virtualObservedLanguageModel) RequestsSince(startIndex int) []llm.StructuredResponseRequest {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	if startIndex < 0 || startIndex > len(languageModel.requests) {
		startIndex = 0
	}
	return append([]llm.StructuredResponseRequest{}, languageModel.requests[startIndex:]...)
}

func (languageModel *virtualObservedLanguageModel) CallCount() int {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	return len(languageModel.calls)
}

func (languageModel *virtualObservedLanguageModel) CallsSince(startIndex int) []VirtualLanguageModelCallEvent {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	if startIndex < 0 || startIndex > len(languageModel.calls) {
		startIndex = 0
	}
	return append([]VirtualLanguageModelCallEvent{}, languageModel.calls[startIndex:]...)
}

func (languageModel *virtualObservedLanguageModel) appendRequest(request llm.StructuredResponseRequest) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.requests = append(languageModel.requests, request)
}

func (languageModel *virtualObservedLanguageModel) appendCall(callEvent VirtualLanguageModelCallEvent) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.calls = append(languageModel.calls, callEvent)
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
	}
	return callEvent
}

func virtualTextCallEvent(kind string, prompt string, reply string, startedAt time.Time, errorValue error) VirtualLanguageModelCallEvent {
	callEvent := VirtualLanguageModelCallEvent{
		Kind:         kind,
		LatencyMS:    time.Since(startedAt).Milliseconds(),
		PromptBytes:  len(prompt),
		ContentBytes: len(reply),
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
	case "file_write_legacy_mode_acceptance":
		return FileWriteLegacyModeAcceptanceScenario(artifactDirectoryPath), nil
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
	case "database_sql_acceptance":
		return DatabaseSQLAcceptanceScenario(artifactDirectoryPath), nil
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
	case "site_suggested_repair_recovery":
		return SiteSuggestedRepairRecoveryScenario(artifactDirectoryPath), nil
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
	languageModel := newVirtualObservedLanguageModel(baseLanguageModel)
	agentKernel := agent.NewAgentKernel(taskRunService, taskStepService)
	agentKernel.UseTaskArtifactService(taskArtifactService)
	agentKernel.UseLanguageModelProvider(languageModel)
	if scenario.CodingTierVisionFallback {
		codingTaskLanguageModel := llm.VisionFallbackProvider{
			TextOnlyModel: imageRejectingLanguageModel{delegate: languageModel},
			VisionModel:   languageModel,
		}
		agentKernel.UseTaskTierLanguageModels(languageModel, languageModel, languageModel, languageModel, languageModel, codingTaskLanguageModel)
	}
	agentKernel.UseIntakeLanguageModelProvider(languageModel)
	agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true, DefaultTaskLevel: agent.TaskLevelLow})
	agentKernel.UseTurnOptions(virtualTurnOptions(scenario.TurnOptions))
	instructionBundleLoader := virtualInstructionBundleLoader(skillInstructions, workspacePath)
	skillRetriever := agent.NewEmbeddingSkillRetriever(virtualSkillEmbeddingProvider{}, "")
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
		capabilityClient, capabilityCleanup = startVirtualCapabilityServer(capabilityToolNames)
		runtime.UseCapabilityTools(capabilityClient, scenario.CapabilityToolNames)
		runtime.UseCapabilityToolDescriptors(capabilityClient, scenario.CapabilityToolDescriptors)
		cleanup = capabilityCleanup
	}

	memoryStore := newVirtualMemoryStore(scenario.InitialMemory)
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(memoryStore)
	runtime.UseMemoryService(memoryService)
	runtime.UseGraphitiIngestionRouter(memory.NewGraphitiIngestionRouter(languageModel, "e2e"))
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

func virtualTurnOptions(scenarioOptions agent.TurnOptions) agent.TurnOptions {
	turnOptions := agent.TurnOptions{MaxIterationCount: 20, MaxToolCallCount: 16, MaxElapsedSecond: 120}
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
	if len(scenario.CapabilityToolNames) > 0 {
		toolCatalogBuilder.UseCapabilityTools(capabilityClient, scenario.CapabilityToolNames)
	}
	if len(scenario.CapabilityToolDescriptors) > 0 {
		toolCatalogBuilder.UseCapabilityToolDescriptors(capabilityClient, scenario.CapabilityToolDescriptors)
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

type virtualSkillEmbeddingProvider struct{}

func (provider virtualSkillEmbeddingProvider) GenerateEmbedding(_ context.Context, input string) ([]float32, error) {
	normalizedInput := strings.ToLower(input)
	return []float32{
		virtualSkillEmbeddingValue(normalizedInput, []string{"피피티", "pptx", "slides", "presentation", "파워포인트", "발표자료"}),
		virtualSkillEmbeddingValue(normalizedInput, []string{"schedule", "scheduled", "cron", "remind", "reminder", "repeat", "예약", "알림", "리마인드", "마다", "분마다", "한 번씩", "10번"}),
		virtualSkillEmbeddingValue(normalizedInput, []string{"website", "web app", "site", "prototype", "deploy", "웹사이트", "사이트", "프로토타입", "배포"}),
	}, nil
}

func virtualSkillEmbeddingValue(input string, keywords []string) float32 {
	for _, keyword := range keywords {
		if strings.Contains(input, keyword) {
			return 1
		}
	}
	return 0
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
		Name:            skillBundle.Name,
		Description:     skillBundle.Description,
		Category:        skillBundle.Category,
		Tags:            append([]string{}, skillBundle.Tags...),
		Prompt:          skillBundle.Instruction,
		Activation:      agent.SkillActivation(skillBundle.Activation),
		Completion:      agent.SkillCompletion(skillBundle.Completion),
		Quality:         agent.SkillQuality(skillBundle.Quality),
		AllowedTools:    append([]string{}, skillBundle.AllowedTools...),
		AllowedProfiles: append([]string{}, skillBundle.AllowedProfiles...),
		TriggerHints:    append([]string{}, skillBundle.TriggerHints...),
		References:      append([]string{}, skillBundle.References...),
		Scripts:         append([]string{}, skillBundle.Scripts...),
		Assets:          append([]string{}, skillBundle.Assets...),
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

type virtualCapabilityHTTPClient struct {
	toolNameByName map[string]bool
}

func startVirtualCapabilityServer(toolNames []string) (capability.Client, func()) {
	toolNameByName := map[string]bool{}
	for _, toolName := range toolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName != "" {
			toolNameByName[trimmedToolName] = true
		}
	}
	server := httptest.NewServer(http.HandlerFunc(virtualCapabilityHandler(toolNameByName)))
	return capability.Client{
		Endpoint:   server.URL,
		HTTPClient: server.Client(),
	}, server.Close
}

func virtualCapabilityHandler(toolNameByName map[string]bool) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/v1/capabilities" {
			_, _ = responseWriter.Write([]byte(virtualCapabilityCatalogResponse(toolNameByName)))
			return
		}
		if request.Method != http.MethodPost || !strings.HasPrefix(request.URL.Path, "/v1/tools/") || !strings.HasSuffix(request.URL.Path, "/invoke") {
			http.Error(responseWriter, "unsupported virtual capability endpoint", http.StatusNotFound)
			return
		}
		toolName := strings.TrimPrefix(request.URL.Path, "/v1/tools/")
		toolName = strings.TrimSuffix(toolName, "/invoke")
		if !toolNameByName[toolName] {
			http.Error(responseWriter, "unknown virtual capability tool", http.StatusNotFound)
			return
		}
		requestBody, _ := io.ReadAll(request.Body)
		_, _ = responseWriter.Write([]byte(virtualCapabilityResponse(toolName, requestBody)))
	}
}

func virtualCapabilityCatalogResponse(toolNameByName map[string]bool) string {
	toolNames := make([]string, 0, len(toolNameByName))
	for toolName := range toolNameByName {
		toolNames = append(toolNames, toolName)
	}
	sort.Strings(toolNames)
	descriptors := []string{}
	for _, toolName := range toolNames {
		descriptors = append(descriptors, `{"name":`+quote(toolName)+`,"description":"Virtual capability `+toolName+`","inputSchema":{"type":"object"}}`)
	}
	return `{"capabilities":[` + strings.Join(descriptors, ",") + `]}`
}

func (client virtualCapabilityHTTPClient) Do(request *http.Request) (*http.Response, error) {
	toolName := strings.TrimPrefix(request.URL.Path, "/v1/tools/")
	toolName = strings.TrimSuffix(toolName, "/invoke")
	if !client.toolNameByName[toolName] {
		return virtualCapabilityHTTPResponse(http.StatusNotFound, "unknown virtual capability tool"), nil
	}
	requestBody, _ := io.ReadAll(request.Body)
	return virtualCapabilityHTTPResponse(http.StatusOK, virtualCapabilityResponse(toolName, requestBody)), nil
}

func virtualCapabilityHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func virtualCapabilityResponse(toolName string, requestBody []byte) string {
	switch toolName {
	case "site.create":
		return `{"provider":"virtual","toolName":"site.create","status":"ok","result":{"siteID":"site-1","slug":"demo","workspacePath":"/workspace/circles/staff/sites/demo","sourceWorkspacePath":"/workspace/circles/staff/sites/demo/draft","appWorkspacePath":"/workspace/circles/staff/sites/demo/draft/app","sourceFiles":` + virtualSiteCreateSourceFiles(requestBody) + `}}`
	case "site.publish":
		return `{"provider":"virtual","toolName":"site.publish","status":"ok","result":{"siteID":"site-1","status":"published","publishedURL":"https://demo.device.example.test"}}`
	case "site.status":
		return `{"provider":"virtual","toolName":"site.status","status":"ok","result":{"siteID":"site-1","slug":"demo","status":"draft","workspacePath":"/workspace/circles/staff/sites/demo","sourceWorkspacePath":"/workspace/circles/staff/sites/demo/draft","appWorkspacePath":"/workspace/circles/staff/sites/demo/draft/app"}}`
	case "site.logs":
		return `{"provider":"virtual","toolName":"site.logs","status":"ok","result":{"logs":[]}}`
	case "site.delete":
		if virtualCapabilityRequestNeedsApproval(requestBody) {
			return `{"provider":"virtual","toolName":"site.delete","status":"denied","content":"requires approval","message":"requires approval","errorCode":"approval_required","failureStage":"authorization","result":{"errorCode":"approval_required","failureStage":"authorization","message":"requires approval"}}`
		}
		return `{"provider":"virtual","toolName":"site.delete","status":"ok","content":"deleted virtual site","result":{"siteID":"site-1","slug":"demo","status":"deleted"}}`
	case "image.read":
		return `{"provider":"virtual","toolName":"image.read","status":"ok","content":"image loaded","result":{"attachments":[{"devicePath":"/workspace/circles/staff/inbox/virtual/virtual-conversation-1/virtual-message-001/mascot.png","filename":"mascot.png","contentType":"image/png","sizeBytes":13,"contentBase64":"dmlydHVhbC1pbWFnZQ=="}]}}`
	case "web.search":
		return `{"provider":"virtual","toolName":"web.search","status":"ok","content":"BlueclawSearchStubToken virtual search result","result":{"query":"current external information acceptance test","results":[{"title":"BlueclawSearchStubToken result","url":"https://example.test/blueclaw-search-stub","snippet":"Deterministic virtual search result for BlueclawSearchStubToken."}]}}`
	case "message.send":
		if virtualPlatformMessageSendRequiresApproval(requestBody) {
			return `{"provider":"virtual","toolName":"message.send","status":"denied","content":"requires approval","message":"requires approval","errorCode":"approval_required","failureStage":"authorization","result":{"errorCode":"approval_required","failureStage":"authorization","message":"requires approval"}}`
		}
		return `{"provider":"virtual","toolName":"message.send","status":"ok","content":"sent virtual platform message virtual-platform-message-001","result":{"messageID":"virtual-platform-message-001","deliveryStatus":"sent"}}`
	default:
		return `{"provider":"virtual","toolName":` + quote(toolName) + `,"status":"ok","result":{"toolName":` + quote(toolName) + `,"ok":true,"request":` + jsonObjectOrEmpty(requestBody) + `}}`
	}
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

func virtualPlatformMessageSendRequiresApproval(requestBody []byte) bool {
	var requestDocument struct {
		Input struct {
			TargetType string `json:"targetType"`
		} `json:"input"`
		Context struct {
			IsApprovalContinuation bool `json:"isApprovalContinuation"`
		} `json:"context"`
	}
	if len(requestBody) == 0 || json.Unmarshal(requestBody, &requestDocument) != nil {
		return false
	}
	return strings.TrimSpace(requestDocument.Input.TargetType) == "directMessage" && !requestDocument.Context.IsApprovalContinuation
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
			} else if len(virtualTurn.RouterRequiredEvidence) > 0 || strings.TrimSpace(virtualTurn.RouterSiteEvidence) != "" || virtualTurnExpectsEvent(virtualTurn, "ask.resolved") {
				harness.scriptedModel.EnqueueStructuredResponses("blueclaw_turn_router", scenarioTurnRouterResponse(harness.scenario, virtualTurn))
			}
			harness.scriptedModel.SetActionResponses(virtualTurn.ActionResponses...)
		}
		turnResult, errorValue := harness.runTurn(ctx, index, virtualTurn)
		if errorValue != nil {
			return result, errorValue
		}
		turnResult.InformationalAssertions = informationalAssertionResults(virtualTurn, turnResult)
		if errorValue := harness.assertTurnResult(virtualTurn, turnResult); errorValue != nil {
			return result, fmt.Errorf("%s turn %d: %w", harness.scenario.Name, index+1, errorValue)
		}
		result.TurnResults = append(result.TurnResults, turnResult)
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
	defaultResponses["blueclaw_addressing_classification"] = `{"target":"anyone","shouldRespond":false,"dutyMatch":false,"dutyName":"","dutyConfidence":0}`
	if strings.TrimSpace(scenario.AddressingResponse) != "" {
		defaultResponses["blueclaw_addressing_classification"] = strings.TrimSpace(scenario.AddressingResponse)
	}
	if len(scenario.InitialToolNames) == 0 && len(scenario.RouterRequiredEvidence) == 0 {
		return defaultResponses
	}
	defaultResponses["blueclaw_turn_router"] = scenarioTurnRouterResponse(scenario, VirtualTurn{})
	return defaultResponses
}

func scenarioApprovalRouterResponse(approval string) string {
	routerDocument := map[string]any{
		"route":            "continue_task",
		"classification":   "bounded_task",
		"taskShape":        "maintenance_task",
		"level":            "low",
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
		"classification":         "bounded_task",
		"taskShape":              "maintenance_task",
		"level":                  string(taskLevel),
		"requestedOutputFormats": nil,
		"expectedResults":        []any{},
		"requiredEvidence":       requiredEvidence,
		"siteRequestEvidence":    siteEvidence,
		"responseLanguage":       "ko",
		"reason":                 "scripted scenario default",
		"userFacingReply":        "",
		"initialToolNames":       scenario.InitialToolNames,
		"priorTaskReference":     "none",
	}
	if virtualTurnExpectsEvent(virtualTurn, "confirmation.reply_classified") {
		routerDocument["approval"] = "approve"
	}
	encodedDocument, errorValue := json.Marshal(routerDocument)
	if errorValue != nil {
		return `{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","requestedOutputFormats":null,"expectedResults":[],"requiredEvidence":[],"siteRequestEvidence":"","responseLanguage":"ko","reason":"scripted scenario default","userFacingReply":"","initialToolNames":[],"priorTaskReference":"none"}`
	}
	return string(encodedDocument)
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
	modelRequestStartIndex := 0
	if harness.requestRecorder != nil {
		modelRequestStartIndex = harness.requestRecorder.RequestCount()
	}
	modelCallStartIndex := 0
	if harness.callRecorder != nil {
		modelCallStartIndex = harness.callRecorder.CallCount()
	}
	messages := append([]connectors.VisibleContextMessage{}, harness.history...)
	messages = append(messages, virtualTurn.ContextMessages...)
	event := connectors.PlatformInboundEvent{
		Platform:       "virtual",
		Source:         "e2e",
		ConversationID: "virtual-conversation-1",
		MessageID:      fmt.Sprintf("virtual-message-%03d", index+1),
		SenderID:       "user-1",
		ReplyTargetID:  virtualReplyTargetID(index, virtualTurn),
		Prompt:         virtualTurn.Prompt,
		Context: connectors.VisibleContext{
			Messages: messages,
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
	if strings.TrimSpace(runtimeResult.TaskRunID) == "" {
		return VirtualTurnResult{}, errors.New("virtual turn did not create a task run")
	}
	outboundReply, outboundReplyTarget, isFound := harness.adapter.FindReply(runtimeResult.ReplyDispatchID)
	if !isFound {
		events := harness.taskEventService.ListTaskEvent(runtimeResult.TaskRunID)
		return VirtualTurnResult{}, fmt.Errorf("virtual turn did not dispatch a reply; events: %s", summarizeEvents(events))
	}
	taskRun, isFound := harness.taskRunService.FindTaskRun(runtimeResult.TaskRunID)
	if !isFound {
		return VirtualTurnResult{}, errors.New("virtual turn task run not found")
	}
	return VirtualTurnResult{
		TaskRunID:               runtimeResult.TaskRunID,
		TaskStatus:              taskRun.Status,
		FailureReason:           taskRun.FailureReason,
		FinishMessage:           outboundReply.Message,
		ReplyTargetID:           outboundReplyTarget.ReplyTargetID,
		Attachments:             outboundReply.Attachments,
		Events:                  harness.taskEventService.ListTaskEvent(runtimeResult.TaskRunID),
		LanguageModelCallEvents: harness.modelCallsSince(modelCallStartIndex),
		ModelContext:            harness.modelContextSince(modelRequestStartIndex),
		ModelImagePartCount:     harness.modelImagePartCountSince(modelRequestStartIndex),
		UserModelImagePartCount: harness.userModelImagePartCountSince(modelRequestStartIndex),
	}, nil
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
	harness.history = append(harness.history,
		connectors.VisibleContextMessage{Speaker: "user", SpeakerCallingName: "샘플 님", SpeakerHandle: "dongha", Text: virtualTurn.Prompt},
		connectors.VisibleContextMessage{Speaker: "assistant", SpeakerCallingName: "김인턴", SpeakerHandle: "internkim", Text: turnResult.FinishMessage},
	)
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
	if harness.scenario.UseLooseAssertions {
		return assertLooseTurnResult(virtualTurn, turnResult)
	}
	return assertTurnResult(harness.workspacePath, virtualTurn, turnResult)
}

func assertLooseTurnResult(virtualTurn VirtualTurn, turnResult VirtualTurnResult) error {
	if strings.TrimSpace(turnResult.FinishMessage) == "" {
		return errors.New("expected non-empty final reply")
	}
	switch turnResult.TaskStatus {
	case task.TaskStatusPlanned, task.TaskStatusRunning, task.TaskStatusInterrupted:
		return fmt.Errorf("expected terminal or waiting task status, got %s", turnResult.TaskStatus)
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
	for _, fragment := range virtualTurn.ExpectedReplyFragments {
		results = append(results, VirtualInformationalAssertion{
			Name:      "expected reply fragment",
			Satisfied: strings.Contains(turnResult.FinishMessage, fragment),
			Detail:    fragment,
		})
	}
	return results
}

func assertTurnResult(workspacePath string, virtualTurn VirtualTurn, turnResult VirtualTurnResult) error {
	for _, skillName := range virtualTurn.ExpectedSelectedSkills {
		if !eventsContain(turnResult.Events, "agent.instructions_loaded", skillName) {
			return fmt.Errorf("expected selected skill %q; events: %s", skillName, summarizeEvents(turnResult.Events))
		}
	}
	for _, toolName := range virtualTurn.ExpectedToolCalls {
		if !requestedToolCallPresent(turnResult.Events, toolName) {
			return fmt.Errorf("expected requested tool %q; events: %s", toolName, summarizeEvents(turnResult.Events))
		}
	}
	for _, eventName := range virtualTurn.ExpectedEvents {
		if !eventsContain(turnResult.Events, eventName, "") {
			return fmt.Errorf("expected event %q; events: %s", eventName, summarizeEvents(turnResult.Events))
		}
	}
	for toolName, expectedCount := range virtualTurn.ExpectedToolCallCounts {
		actualCount := countRequestedToolCalls(turnResult.Events, toolName)
		if actualCount != expectedCount {
			return fmt.Errorf("expected %d requested %s calls, got %d; events: %s", expectedCount, toolName, actualCount, summarizeEvents(turnResult.Events))
		}
	}
	for _, expectedEventCount := range virtualTurn.ExpectedEventCounts {
		actualCount := countEventsWithFragment(turnResult.Events, expectedEventCount.Name, expectedEventCount.BodyFragment)
		if actualCount != expectedEventCount.Count {
			return fmt.Errorf("expected %d events %s containing %q, got %d; events: %s", expectedEventCount.Count, expectedEventCount.Name, expectedEventCount.BodyFragment, actualCount, summarizeEvents(turnResult.Events))
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
	for _, expectedWorkspaceFile := range virtualTurn.ExpectedWorkspaceFiles {
		if errorValue := validateExpectedWorkspaceFile(workspacePath, expectedWorkspaceFile); errorValue != nil {
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
	if eventsContain(events, "tool."+toolName+".requested", toolName) {
		return true
	}
	return eventsContain(events, "tool.capability.invoke.requested", capabilityOperationFragment(toolName))
}

func countRequestedToolCalls(events []task.TaskEvent, toolName string) int {
	directCount := countEvents(events, "tool."+toolName+".requested")
	verbCount := countEventsWithFragment(events, "tool.capability.invoke.requested", capabilityOperationFragment(toolName))
	return directCount + verbCount
}

func capabilityOperationFragment(toolName string) string {
	return `"operation":"` + toolName + `"`
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
	return []string{"conversation.history", "memory.search", "terminal.run", "terminal.session", "browser_handoff.openURL", "ask.choice", "ask.input", "file.read", "file.write", "file.edit", "file.patch", "file.promote", "file.attach"}
}

func terminalConfiguration(workspacePath string) config.TerminalConfiguration {
	return config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspacePath,

		DeniedPathPrefixes:    []string{"/etc", "/private/etc", "/System", "/Library"},
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

func (adapter *virtualAdapter) FetchHistory(context.Context, string, int) (connectors.VisibleContext, error) {
	return connectors.VisibleContext{}, nil
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

func actionSelectTools(toolNames ...string) string {
	encodedToolNames := []string{}
	for _, toolName := range toolNames {
		encodedToolNames = append(encodedToolNames, quote(toolName))
	}
	return `{"action":"tool.request","toolNames":[` + strings.Join(encodedToolNames, ",") + `],"skillNames":[],"reason":"required for the requested task"}`
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
