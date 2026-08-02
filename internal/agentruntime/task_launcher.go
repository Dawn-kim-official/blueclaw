package agentruntime

import (
	"context"
	"errors"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"path/filepath"
	"strings"
	"time"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/memory"
	"github.com/Dawn-kim-official/blueclaw/internal/policy"
)

type TaskLaunchSource string

const (
	TaskLaunchSourceConnector TaskLaunchSource = "connector"
	TaskLaunchSourceAdmin     TaskLaunchSource = "admin"
	TaskLaunchSourceScheduled TaskLaunchSource = "scheduled"
)

type TaskLauncher struct {
	harness                       agentcontract.Harness
	launchFailureCompleter        LaunchFailureCompleter
	turnRouter                    TurnRouter
	taskRunService                *taskstate.TaskRunService
	toolCatalogBuilder            *ToolCatalogBuilder
	requesterWorkspaceProvisioner RequesterWorkspaceProvisioner
	requesterEmailResolver        RequesterEmailResolver
}

type RequesterWorkspaceProvisioner interface {
	ProvisionRequesterWorkspace(context.Context, policy.PersonAccess, string) error
}

type RequesterEmailResolver interface {
	ResolvePersonPrimaryEmail(personID string) string
}

type TaskLaunchRequest struct {
	Source                     TaskLaunchSource
	SourceReference            string
	RequesterPersonID          string
	RequesterName              string
	RequesterCallingName       string
	RequesterHandle            string
	RequesterEmail             string
	RequesterPlatformUserID    string
	IsApprovalContinuation     bool
	IsRuntimeRestartResume     bool
	ExistingTaskRunID          string
	OriginReplyTargetID        string
	OriginIsThread             bool
	ProfileName                string
	Platform                   string
	ConversationID             string
	ConversationType           string
	ConversationChannelID      string
	ConversationChannelName    string
	ActiveCircleID             string
	ActiveCircleConflict       bool
	ReplyTargetID              string
	Prompt                     string
	InputParts                 []agentcontract.AgentPart
	ResponseLanguage           string
	VisibleContext             agentcontract.VisibleContext
	ActiveGoal                 agentcontract.ActiveGoal
	PriorTask                  agentcontract.PriorTaskContext
	ScheduledRun               agentcontract.ScheduledRunContext
	PrecomputedTurnDecision    *agentcontract.TurnDecision
	IsPrecomputedDecisionExact bool
	SkipSkillSelection         bool
	UseEmptyToolCatalog        bool
	AmbientDuty                agentcontract.AmbientDutyContext
	PinnedToolNames            []string
	PinnedSkillNames           []string
	HistoryProvider            HistoryProvider
	AttachmentMaterialResolver AttachmentMaterialResolver
	PersonAccess               policy.PersonAccess
	MemoryNamespaces           []memory.MemoryNamespace
	AccessibleConversationIDs  []string
	CheckpointSender           agentcontract.AgentCheckpointSender
	ArtifactManifest           []agentcontract.ArtifactManifestEntry
}

type TaskLaunchResult struct {
	TurnResult            agentcontract.AgentTurnResult
	MemoryFacts           []memory.MemoryFact
	ToolNames             []string
	NormalizedProfileName string
}

type TaskMemoryRequest struct {
	Query                     string
	RequesterPersonID         string
	ConversationID            string
	PersonAccess              policy.PersonAccess
	MemoryNamespaces          []memory.MemoryNamespace
	AccessibleConversationIDs []string
}

type TaskPinnedMemoryRequest struct {
	RequesterPersonID string
}

type taskLaunchStep[T any] interface {
	Name() string
	Run(context.Context, *taskLaunchExecution) (T, error)
}

type taskLaunchExecution struct {
	Launcher              *TaskLauncher
	Request               TaskLaunchRequest
	NormalizedProfileName string
}

type launchStepRecord struct {
	StepName string `json:"stepName"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

type launchMemoryResult struct {
	Facts           []memory.MemoryFact
	PinnedFactCount int
	GraphFactCount  int
	Error           string
}

type TurnRouter interface {
	Plan(context.Context, agentcontract.AgentRequest) (agentcontract.TurnDecision, error)
}

func (taskLauncher *TaskLauncher) UseTurnRouter(turnRouter TurnRouter) {
	taskLauncher.turnRouter = turnRouter
}

type LaunchFailureCompleter interface {
	CompleteLaunchFailure(context.Context, agentcontract.AgentTurnRequest, string, string, error) agentcontract.AgentTurnResult
}

func (taskLauncher *TaskLauncher) UseLaunchFailureCompleter(launchFailureCompleter LaunchFailureCompleter) {
	taskLauncher.launchFailureCompleter = launchFailureCompleter
}

func NewTaskLauncher(harness agentcontract.Harness, taskRunService *taskstate.TaskRunService, toolCatalogBuilder *ToolCatalogBuilder) *TaskLauncher {
	if toolCatalogBuilder == nil {
		toolCatalogBuilder = NewToolCatalogBuilder()
	}
	return &TaskLauncher{
		harness:            harness,
		taskRunService:     taskRunService,
		toolCatalogBuilder: toolCatalogBuilder,
	}
}

func (taskLauncher *TaskLauncher) UseRequesterWorkspaceProvisioner(provisioner RequesterWorkspaceProvisioner) {
	taskLauncher.requesterWorkspaceProvisioner = provisioner
}

func (taskLauncher *TaskLauncher) UseRequesterEmailResolver(resolver RequesterEmailResolver) {
	taskLauncher.requesterEmailResolver = resolver
}

func (taskLauncher *TaskLauncher) resolveRequesterEmail(request TaskLaunchRequest) string {
	personID := strings.TrimSpace(request.RequesterPersonID)
	if taskLauncher.requesterEmailResolver != nil && personID != "" {
		if resolvedEmail := strings.TrimSpace(taskLauncher.requesterEmailResolver.ResolvePersonPrimaryEmail(personID)); resolvedEmail != "" {
			return resolvedEmail
		}
	}
	return request.RequesterEmail
}

func (taskLauncher *TaskLauncher) Launch(ctx context.Context, request TaskLaunchRequest) (TaskLaunchResult, error) {
	request.RequesterEmail = taskLauncher.resolveRequesterEmail(request)
	request.PersonAccess = requesterPersonAccessForTaskLaunch(request)
	normalizedProfileName := normalizeProfileName(request.ProfileName)
	activeCircleRequest := withResolvedActiveCircle(ToolCatalogRequest{
		Prompt:                  request.Prompt,
		ConversationChannelName: request.ConversationChannelName,
		PersonAccess:            request.PersonAccess,
		ActiveCircleID:          request.ActiveCircleID,
		ActiveCircleConflict:    request.ActiveCircleConflict,
	})
	request.ActiveCircleID = activeCircleRequest.ActiveCircleID
	request.ActiveCircleConflict = activeCircleRequest.ActiveCircleConflict
	request.ArtifactManifest = taskLauncher.conversationArtifactManifest(request, normalizedProfileName)
	request.PrecomputedTurnDecision = taskLauncher.routedTurnDecision(ctx, request, normalizedProfileName)
	request.VisibleContext = taskLauncher.visibleContextWithArtifactManifest(request.VisibleContext, request.ArtifactManifest)
	execution := &taskLaunchExecution{
		Launcher:              taskLauncher,
		Request:               request,
		NormalizedProfileName: normalizedProfileName,
	}
	launchRecords := []launchStepRecord{}
	_, record := runLaunchStep(ctx, execution, provisionRequesterWorkspaceLaunchStep{})
	launchRecords = append(launchRecords, record)
	if record.Error != "" {
		return taskLauncher.completeLaunchFailure(ctx, request, normalizedProfileName, nil, record.StepName, launchRecords, errorFromStepRecord(record)), nil
	}
	toolSet, record := runLaunchStep(ctx, execution, buildToolSetLaunchStep{})
	launchRecords = append(launchRecords, record)
	toolNames := toolSet.ListToolNames()
	registryAudit, record := runLaunchStep(ctx, execution, auditToolRegistryLaunchStep{ToolSet: toolSet})
	launchRecords = append(launchRecords, record)
	if record.Error != "" {
		return taskLauncher.completeLaunchFailure(ctx, request, normalizedProfileName, toolNames, record.StepName, launchRecords, errorFromStepRecord(record)), nil
	}
	conversationScope := ConversationScopeForRequest(taskLauncher.toolCatalogBuilder.WorkspaceRootPath(), ToolCatalogRequest{
		RequesterPersonID:       request.RequesterPersonID,
		ConversationID:          request.ConversationID,
		ConversationType:        request.ConversationType,
		ConversationChannelID:   request.ConversationChannelID,
		ConversationChannelName: request.ConversationChannelName,
	})
	memoryResult, record := runLaunchStep(ctx, execution, loadMemoryLaunchStep{})
	launchRecords = append(launchRecords, record)
	turnResult, record := runLaunchStep(ctx, execution, runTurnLaunchStep{
		MemoryFacts:       memoryResult.Facts,
		ToolSet:           toolSet,
		ConversationScope: conversationScope,
	})
	launchRecords = append(launchRecords, record)
	if record.Error != "" {
		if taskRunID := strings.TrimSpace(turnResult.TaskRun.TaskRunID); taskRunID != "" {
			request.ExistingTaskRunID = taskRunID
		}
		return taskLauncher.completeLaunchFailure(ctx, request, normalizedProfileName, toolNames, record.StepName, launchRecords, errorFromStepRecord(record)), nil
	}
	launchedToolNames := turnResult.ToolNames
	if len(launchedToolNames) == 0 {
		launchedToolNames = toolNames
	}
	if turnResult.TaskRun.TaskRunID != "" {
		taskLauncher.appendLaunchStepRecords(turnResult.TaskRun.TaskRunID, launchRecords)
		if memoryResult.Error != "" {
			taskLauncher.taskRunService.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "memory.pinned_load_failed", memoryResult.Error)
		} else {
			taskLauncher.taskRunService.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "memory.pinned_load_succeeded", marshalToolResult(map[string]any{
				"factCount":       len(memoryResult.Facts),
				"pinnedFactCount": memoryResult.PinnedFactCount,
				"graphFactCount":  memoryResult.GraphFactCount,
			}))
		}
		taskLauncher.taskRunService.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "agent.task_launched", marshalTaskLaunchEvent(request, normalizedProfileName, launchedToolNames, registryAudit, len(memoryResult.Facts)))
		taskLauncher.appendAmbientDutyLaunchEvent(turnResult.TaskRun.TaskRunID, request)
		taskLauncher.taskRunService.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "agent.conversation_scope", marshalToolResult(conversationScope))
	}
	return TaskLaunchResult{
		TurnResult:            turnResult,
		MemoryFacts:           memoryResult.Facts,
		ToolNames:             launchedToolNames,
		NormalizedProfileName: normalizedProfileName,
	}, nil
}

type provisionRequesterWorkspaceLaunchStep struct{}

func (provisionRequesterWorkspaceLaunchStep) Name() string {
	return "provision_requester_workspace"
}

func (provisionRequesterWorkspaceLaunchStep) Run(ctx context.Context, execution *taskLaunchExecution) (struct{}, error) {
	provisioner := execution.Launcher.requesterWorkspaceProvisioner
	if provisioner == nil {
		return struct{}{}, nil
	}
	return struct{}{}, provisioner.ProvisionRequesterWorkspace(ctx, execution.Request.PersonAccess, execution.Launcher.toolCatalogBuilder.WorkspaceRootPath())
}

type buildToolSetLaunchStep struct{}

func (buildToolSetLaunchStep) Name() string {
	return "build_tool_set"
}

func (buildToolSetLaunchStep) Run(_ context.Context, execution *taskLaunchExecution) (*toolcontract.ToolSet, error) {
	if execution.Request.UseEmptyToolCatalog {
		return toolcontract.NewToolSet(nil), nil
	}
	toolSet := execution.Launcher.toolCatalogBuilder.BuildToolSet(
		execution.Launcher.toolCatalogRequestForLaunch(execution.Request, execution.NormalizedProfileName),
	)
	if execution.Request.AmbientDuty.IsMatch {
		return toolSet.WithAllowedToolNames(ambientCaptureAllowedToolNames()), nil
	}
	return toolSet, nil
}

func ambientCaptureAllowedToolNames() []string {
	return []string{
		"task_add", "task_list", "task_update",
		"calendar_add", "calendar_update", "calendar_list",
		"ask_input",
		"conversation_history",
	}
}

type auditToolRegistryLaunchStep struct {
	ToolSet *toolcontract.ToolSet
}

func (auditToolRegistryLaunchStep) Name() string {
	return "audit_tool_registry"
}

func (step auditToolRegistryLaunchStep) Run(ctx context.Context, execution *taskLaunchExecution) (ToolRegistryAudit, error) {
	return execution.Launcher.toolCatalogBuilder.BuildToolRegistryAudit(ctx, step.ToolSet)
}

type loadMemoryLaunchStep struct{}

func (loadMemoryLaunchStep) Name() string {
	return "load_memory"
}

func (loadMemoryLaunchStep) Run(ctx context.Context, execution *taskLaunchExecution) (launchMemoryResult, error) {
	pinnedMemoryFacts, errorValue := execution.Launcher.toolCatalogBuilder.LoadPinnedMemory(ctx, TaskPinnedMemoryRequest{
		RequesterPersonID: execution.Request.RequesterPersonID,
	})
	if errorValue != nil {
		return launchMemoryResult{Error: errorValue.Error()}, nil
	}
	graphMemoryFacts := searchLaunchGraphMemory(ctx, execution)
	return launchMemoryResult{
		Facts:           appendMemoryFacts(pinnedMemoryFacts, graphMemoryFacts),
		PinnedFactCount: len(pinnedMemoryFacts),
		GraphFactCount:  len(graphMemoryFacts),
	}, nil
}

const launchGraphMemorySearchTimeout = 8 * time.Second

func searchLaunchGraphMemory(ctx context.Context, execution *taskLaunchExecution) []memory.MemoryFact {
	toolCatalogBuilder := execution.Launcher.toolCatalogBuilder
	request := execution.Request
	if !toolCatalogBuilder.canSearchGraphMemory() || strings.TrimSpace(request.Prompt) == "" {
		return nil
	}
	catalogRequest := execution.Launcher.toolCatalogRequestForLaunch(request, execution.NormalizedProfileName)
	searchContext, cancelSearch := context.WithTimeout(ctx, launchGraphMemorySearchTimeout)
	defer cancelSearch()
	graphMemoryFacts, errorValue := toolCatalogBuilder.SearchMemory(searchContext, TaskMemoryRequest{
		Query:                     request.Prompt,
		RequesterPersonID:         request.RequesterPersonID,
		ConversationID:            request.ConversationID,
		PersonAccess:              request.PersonAccess,
		MemoryNamespaces:          searchMemoryNamespaces(catalogRequest),
		AccessibleConversationIDs: request.AccessibleConversationIDs,
	})
	if errorValue != nil {
		return nil
	}
	return graphMemoryFacts
}

type runTurnLaunchStep struct {
	MemoryFacts       []memory.MemoryFact
	ToolSet           *toolcontract.ToolSet
	ConversationScope ConversationResourceScope
}

func (runTurnLaunchStep) Name() string {
	return "run_turn"
}

func (step runTurnLaunchStep) Run(ctx context.Context, execution *taskLaunchExecution) (agentcontract.AgentTurnResult, error) {
	return execution.Launcher.harness.RunTurn(ctx, execution.Launcher.agentTurnRequestForLaunch(
		execution.Request,
		execution.NormalizedProfileName,
		step.MemoryFacts,
		step.ToolSet,
		step.ConversationScope,
	))
}

func runLaunchStep[T any](ctx context.Context, execution *taskLaunchExecution, step taskLaunchStep[T]) (T, launchStepRecord) {
	result, errorValue := step.Run(ctx, execution)
	if errorValue != nil {
		return result, launchStepRecord{StepName: step.Name(), Status: "error", Error: errorValue.Error()}
	}
	return result, launchStepRecord{StepName: step.Name(), Status: "result"}
}

func errorFromStepRecord(record launchStepRecord) error {
	return errors.New(record.Error)
}

func (taskLauncher *TaskLauncher) completeLaunchFailure(ctx context.Context, request TaskLaunchRequest, profileName string, toolNames []string, stepName string, records []launchStepRecord, errorValue error) TaskLaunchResult {
	turnRequest := taskLauncher.agentTurnRequestForLaunch(request, profileName, nil, nil, ConversationResourceScope{})
	turnResult := taskLauncher.launchFailureCompleter.CompleteLaunchFailure(ctx, turnRequest, "launch", stepName, errorValue)
	turnResult.ToolNames = append([]string{}, toolNames...)
	taskLauncher.appendLaunchStepRecords(turnResult.TaskRun.TaskRunID, records)
	taskLauncher.appendAmbientDutyLaunchEvent(turnResult.TaskRun.TaskRunID, request)
	return TaskLaunchResult{
		TurnResult:            turnResult,
		ToolNames:             append([]string{}, toolNames...),
		NormalizedProfileName: profileName,
	}
}

func (taskLauncher *TaskLauncher) agentTurnRequestForLaunch(request TaskLaunchRequest, profileName string, memoryFacts []memory.MemoryFact, toolSet *toolcontract.ToolSet, conversationScope ConversationResourceScope) agentcontract.AgentTurnRequest {
	return agentcontract.AgentTurnRequest{
		ArtifactManifest:           request.ArtifactManifest,
		RequesterPersonID:          request.RequesterPersonID,
		RequesterEmail:             request.RequesterEmail,
		RequesterName:              request.RequesterName,
		RequesterPlatformUserID:    request.RequesterPlatformUserID,
		SourceReference:            request.SourceReference,
		IsApprovalContinuation:     request.IsApprovalContinuation,
		IsRuntimeRestartResume:     request.IsRuntimeRestartResume,
		ExistingTaskRunID:          request.ExistingTaskRunID,
		OriginReplyTargetID:        request.OriginReplyTargetID,
		OriginIsThread:             request.OriginIsThread,
		Platform:                   request.Platform,
		RequesterCallingName:       request.RequesterCallingName,
		RequesterHandle:            request.RequesterHandle,
		RequesterCircles:           append([]string{}, request.PersonAccess.Circles...),
		ProfileName:                profileName,
		ConversationID:             request.ConversationID,
		ConversationType:           request.ConversationType,
		Prompt:                     request.Prompt,
		InputParts:                 append([]agentcontract.AgentPart{}, request.InputParts...),
		ResponseLanguage:           request.ResponseLanguage,
		VisibleContext:             request.VisibleContext,
		ActiveGoal:                 request.ActiveGoal,
		PriorTask:                  request.PriorTask,
		ScheduledRun:               request.ScheduledRun,
		PrecomputedTurnDecision:    request.PrecomputedTurnDecision,
		IsPrecomputedDecisionExact: request.IsPrecomputedDecisionExact,
		SkipSkillSelection:         request.SkipSkillSelection,
		AmbientDuty:                request.AmbientDuty,
		MemoryFacts:                bluecollarMemoryFacts(memoryFacts),
		ToolSet:                    toolSet,
		PinnedToolNames:            append([]string{}, request.PinnedToolNames...),
		PinnedSkillNames:           append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:          taskLauncher.toolCatalogBuilder.WorkspaceRootPath(),
		WorkspaceDefaultPath:       conversationScope.DefaultDirectoryPath,
		CheckpointSender:           request.CheckpointSender,
	}
}

// A missing artifact service must reach the harness as an absent store, not as a
// non-nil port holding a nil pointer.
func conversationArtifactStore(taskArtifactService *task.TaskArtifactService) taskstate.TaskArtifactStore {
	if taskArtifactService == nil {
		return nil
	}
	return taskArtifactService
}

func (taskLauncher *TaskLauncher) conversationArtifactManifest(request TaskLaunchRequest, profileName string) []agentcontract.ArtifactManifestEntry {
	if taskLauncher.toolCatalogBuilder.taskRunService == nil {
		return nil
	}
	conversationScope := ConversationScopeForRequest(taskLauncher.toolCatalogBuilder.WorkspaceRootPath(), taskLauncher.toolCatalogRequestForLaunch(request, profileName))
	return buildConversationArtifactManifest(agentcontract.AgentTurnRequest{
		ConversationID:       request.ConversationID,
		ExistingTaskRunID:    request.ExistingTaskRunID,
		WorkspaceRootPath:    taskLauncher.toolCatalogBuilder.WorkspaceRootPath(),
		WorkspaceDefaultPath: conversationScope.DefaultDirectoryPath,
	}, taskLauncher.toolCatalogBuilder.taskRunService, conversationArtifactStore(taskLauncher.toolCatalogBuilder.taskArtifactService))
}

func (taskLauncher *TaskLauncher) visibleContextWithArtifactManifest(visibleContext agentcontract.VisibleContext, manifest []agentcontract.ArtifactManifestEntry) agentcontract.VisibleContext {
	for _, artifact := range manifest {
		visibleContext.Materials = append(visibleContext.Materials, agentcontract.VisibleContextMaterial{
			FileHint:    artifact.FileHint,
			Filename:    filepath.Base(artifact.RelativePath),
			Path:        filepath.ToSlash(filepath.Join(taskLauncher.toolCatalogBuilder.WorkspaceRootPath(), artifact.RelativePath)),
			IsAvailable: true,
		})
	}
	return visibleContext
}

func (taskLauncher *TaskLauncher) appendAmbientDutyLaunchEvent(taskRunID string, request TaskLaunchRequest) {
	ambientDuty := request.AmbientDuty.Normalized()
	if !ambientDuty.IsMatch {
		return
	}
	taskLauncher.taskRunService.AppendTaskEvent(taskRunID, "agent.ambient_duty_launch", marshalToolResult(map[string]any{
		"dutyName":   ambientDuty.Name,
		"confidence": ambientDuty.Confidence,
	}))
}

func (taskLauncher *TaskLauncher) appendLaunchStepRecords(taskRunID string, records []launchStepRecord) {
	for _, record := range records {
		eventName := "agent.launch_step." + record.Status
		taskLauncher.taskRunService.AppendTaskEvent(taskRunID, eventName, marshalToolResult(record))
	}
}

func (taskLauncher *TaskLauncher) toolCatalogRequestForLaunch(request TaskLaunchRequest, profileName string) ToolCatalogRequest {
	return ToolCatalogRequest{
		ProfileName:                profileName,
		Prompt:                     request.Prompt,
		VisibleContext:             request.VisibleContext,
		RequesterPersonID:          request.RequesterPersonID,
		RequesterName:              request.RequesterName,
		RequesterEmail:             request.RequesterEmail,
		RequesterPlatformUserID:    request.RequesterPlatformUserID,
		TaskSource:                 request.Source,
		IsScheduledRun:             request.Source == TaskLaunchSourceScheduled,
		IsApprovalContinuation:     request.IsApprovalContinuation,
		ConversationID:             request.ConversationID,
		ConversationType:           request.ConversationType,
		ConversationChannelID:      request.ConversationChannelID,
		ConversationChannelName:    request.ConversationChannelName,
		ActiveCircleID:             request.ActiveCircleID,
		ActiveCircleConflict:       request.ActiveCircleConflict,
		ReplyTargetID:              request.ReplyTargetID,
		Platform:                   request.Platform,
		HistoryCursor:              request.VisibleContext.HistoryCursor,
		HistoryProvider:            request.HistoryProvider,
		AttachmentMaterialResolver: request.AttachmentMaterialResolver,
		PersonAccess:               request.PersonAccess,
		MemoryNamespaces:           request.MemoryNamespaces,
		AccessibleConversationIDs:  request.AccessibleConversationIDs,
		InputParts:                 append([]agentcontract.AgentPart{}, request.InputParts...),
		ScheduledRun:               request.ScheduledRun,
	}
}

func requesterPersonAccessForTaskLaunch(request TaskLaunchRequest) policy.PersonAccess {
	return requesterPersonAccess(request.RequesterPersonID, request.PersonAccess)
}

func requesterPersonAccess(requesterPersonID string, personAccess policy.PersonAccess) policy.PersonAccess {
	if strings.TrimSpace(personAccess.PersonID) == "" {
		personAccess.PersonID = strings.TrimSpace(requesterPersonID)
	}
	return policy.EnsureRequesterDefaults(personAccess)
}

// bluecollarMemoryFacts converts recalled facts into the loop's own shape. The
// loop carries its own type so it never depends on the service that stores them;
// this single call is where the two meet.
func bluecollarMemoryFacts(facts []memory.MemoryFact) []agentcontract.MemoryFact {
	converted := make([]agentcontract.MemoryFact, 0, len(facts))
	for _, fact := range facts {
		converted = append(converted, agentcontract.MemoryFact{
			FactID:            fact.FactID,
			ScopeType:         fact.ScopeType,
			NamespaceID:       fact.NamespaceID,
			Content:           fact.Content,
			Score:             fact.Score,
			SourceEpisodeID:   fact.SourceEpisodeID,
			SourceKind:        fact.SourceKind,
			ValidAt:           fact.ValidAt,
			SecurityLevelRank: fact.SecurityLevelRank,
			RequiredClasses:   append([]string{}, fact.RequiredClasses...),
		})
	}
	return converted
}

func (taskLauncher *TaskLauncher) routedTurnDecision(ctx context.Context, request TaskLaunchRequest, profileName string) *agentcontract.TurnDecision {
	if request.PrecomputedTurnDecision != nil || taskLauncher.turnRouter == nil {
		return request.PrecomputedTurnDecision
	}
	turnDecision, errorValue := taskLauncher.turnRouter.Plan(ctx, agentcontract.AgentRequest{
		RequesterPersonID: request.RequesterPersonID,
		ConversationID:    request.ConversationID,
		Prompt:            request.Prompt,
		ResponseLanguage:  request.ResponseLanguage,
		VisibleContext:    request.VisibleContext,
		ScheduledRun:      request.ScheduledRun,
		ActiveGoal:        request.ActiveGoal,
		PriorTask:         request.PriorTask,
		ToolSet:           taskLauncher.toolCatalogBuilder.BuildToolSet(taskLauncher.toolCatalogRequestForLaunch(request, profileName)),
	})
	if errorValue != nil {
		return nil
	}
	return &turnDecision
}
