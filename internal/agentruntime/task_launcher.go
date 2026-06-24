package agentruntime

import (
	"context"
	"errors"
	"strings"

	"blueclaw/internal/agent"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
)

type TaskLaunchSource string

const (
	TaskLaunchSourceConnector TaskLaunchSource = "connector"
	TaskLaunchSourceAdmin     TaskLaunchSource = "admin"
	TaskLaunchSourceScheduled TaskLaunchSource = "scheduled"
)

type TaskLauncher struct {
	agentKernel                   *agent.AgentKernel
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
	InputParts                 []agent.AgentPart
	ResponseLanguage           string
	VisibleContext             agent.VisibleContext
	ActiveGoal                 agent.ActiveGoal
	ScheduledRun               agent.ScheduledRunContext
	PrecomputedTurnDecision    *agent.TurnDecision
	AmbientDuty                agent.AmbientDutyContext
	PinnedToolNames            []string
	PinnedSkillNames           []string
	HistoryProvider            HistoryProvider
	AttachmentMaterialResolver AttachmentMaterialResolver
	PersonAccess               policy.PersonAccess
	MemoryNamespaces           []memory.MemoryNamespace
	AccessibleConversationIDs  []string
	CheckpointSender           agent.AgentCheckpointSender
}

type TaskLaunchResult struct {
	TurnResult            agent.AgentTurnResult
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
	Facts []memory.MemoryFact
	Error string
}

func NewTaskLauncher(agentKernel *agent.AgentKernel, toolCatalogBuilder *ToolCatalogBuilder) *TaskLauncher {
	if toolCatalogBuilder == nil {
		toolCatalogBuilder = NewToolCatalogBuilder()
	}
	return &TaskLauncher{
		agentKernel:        agentKernel,
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
		if strings.TrimSpace(turnResult.TaskRun.TaskRunID) == "" {
			return taskLauncher.completeLaunchFailure(ctx, request, normalizedProfileName, toolNames, record.StepName, launchRecords, errorFromStepRecord(record)), nil
		}
		return TaskLaunchResult{}, errorFromStepRecord(record)
	}
	launchedToolNames := turnResult.ToolNames
	if len(launchedToolNames) == 0 {
		launchedToolNames = toolNames
	}
	if turnResult.TaskRun.TaskRunID != "" {
		taskLauncher.appendLaunchStepRecords(turnResult.TaskRun.TaskRunID, launchRecords)
		if memoryResult.Error != "" {
			taskLauncher.agentKernel.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "memory.pinned_load_failed", memoryResult.Error)
		} else {
			taskLauncher.agentKernel.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "memory.pinned_load_succeeded", marshalToolResult(map[string]any{"factCount": len(memoryResult.Facts)}))
		}
		taskLauncher.agentKernel.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "agent.task_launched", marshalTaskLaunchEvent(request, normalizedProfileName, launchedToolNames, registryAudit, len(memoryResult.Facts)))
		taskLauncher.appendAmbientDutyLaunchEvent(turnResult.TaskRun.TaskRunID, request)
		taskLauncher.agentKernel.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "agent.conversation_scope", marshalToolResult(conversationScope))
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

func (buildToolSetLaunchStep) Run(_ context.Context, execution *taskLaunchExecution) (*agent.ToolSet, error) {
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
		"flow.task.add", "flow.task.list", "flow.task.update",
		"calendar.event.add", "calendar.event.update", "calendar.event.list",
		"ask.choice", "ask.input",
		"conversation.history",
	}
}

type auditToolRegistryLaunchStep struct {
	ToolSet *agent.ToolSet
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
	memoryFacts, errorValue := execution.Launcher.toolCatalogBuilder.LoadPinnedMemory(ctx, TaskPinnedMemoryRequest{
		RequesterPersonID: execution.Request.RequesterPersonID,
	})
	if errorValue != nil {
		return launchMemoryResult{Error: errorValue.Error()}, nil
	}
	return launchMemoryResult{Facts: memoryFacts}, nil
}

type runTurnLaunchStep struct {
	MemoryFacts       []memory.MemoryFact
	ToolSet           *agent.ToolSet
	ConversationScope ConversationResourceScope
}

func (runTurnLaunchStep) Name() string {
	return "run_turn"
}

func (step runTurnLaunchStep) Run(ctx context.Context, execution *taskLaunchExecution) (agent.AgentTurnResult, error) {
	return execution.Launcher.agentKernel.RunTurn(ctx, execution.Launcher.agentTurnRequestForLaunch(
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
	turnResult := taskLauncher.agentKernel.CompleteLaunchFailure(ctx, turnRequest, "launch", stepName, errorValue)
	turnResult.ToolNames = append([]string{}, toolNames...)
	taskLauncher.appendLaunchStepRecords(turnResult.TaskRun.TaskRunID, records)
	taskLauncher.appendAmbientDutyLaunchEvent(turnResult.TaskRun.TaskRunID, request)
	return TaskLaunchResult{
		TurnResult:            turnResult,
		ToolNames:             append([]string{}, toolNames...),
		NormalizedProfileName: profileName,
	}
}

func (taskLauncher *TaskLauncher) agentTurnRequestForLaunch(request TaskLaunchRequest, profileName string, memoryFacts []memory.MemoryFact, toolSet *agent.ToolSet, conversationScope ConversationResourceScope) agent.AgentTurnRequest {
	return agent.AgentTurnRequest{
		RequesterPersonID:       request.RequesterPersonID,
		RequesterEmail:          request.RequesterEmail,
		RequesterName:           request.RequesterName,
		RequesterPlatformUserID: request.RequesterPlatformUserID,
		SourceReference:         request.SourceReference,
		IsApprovalContinuation:  request.IsApprovalContinuation,
		IsRuntimeRestartResume:  request.IsRuntimeRestartResume,
		ExistingTaskRunID:       request.ExistingTaskRunID,
		OriginReplyTargetID:     request.OriginReplyTargetID,
		OriginIsThread:          request.OriginIsThread,
		Platform:                request.Platform,
		RequesterCallingName:    request.RequesterCallingName,
		RequesterHandle:         request.RequesterHandle,
		RequesterCircles:        append([]string{}, request.PersonAccess.Circles...),
		ProfileName:             profileName,
		ConversationID:          request.ConversationID,
		Prompt:                  request.Prompt,
		InputParts:              append([]agent.AgentPart{}, request.InputParts...),
		ResponseLanguage:        request.ResponseLanguage,
		VisibleContext:          request.VisibleContext,
		ActiveGoal:              request.ActiveGoal,
		ScheduledRun:            request.ScheduledRun,
		PrecomputedTurnDecision: request.PrecomputedTurnDecision,
		AmbientDuty:             request.AmbientDuty,
		MemoryFacts:             memoryFacts,
		ToolSet:                 toolSet,
		PinnedToolNames:         append([]string{}, request.PinnedToolNames...),
		PinnedSkillNames:        append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:       taskLauncher.toolCatalogBuilder.WorkspaceRootPath(),
		WorkspaceDefaultPath:    conversationScope.DefaultDirectoryPath,
		CheckpointSender:        request.CheckpointSender,
	}
}

func (taskLauncher *TaskLauncher) appendAmbientDutyLaunchEvent(taskRunID string, request TaskLaunchRequest) {
	ambientDuty := request.AmbientDuty.Normalized()
	if !ambientDuty.IsMatch {
		return
	}
	taskLauncher.agentKernel.AppendTaskEvent(taskRunID, "agent.ambient_duty_launch", marshalToolResult(map[string]any{
		"dutyName":   ambientDuty.Name,
		"confidence": ambientDuty.Confidence,
	}))
}

func (taskLauncher *TaskLauncher) appendLaunchStepRecords(taskRunID string, records []launchStepRecord) {
	for _, record := range records {
		eventName := "agent.launch_step." + record.Status
		taskLauncher.agentKernel.AppendTaskEvent(taskRunID, eventName, marshalToolResult(record))
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
		InputParts:                 append([]agent.AgentPart{}, request.InputParts...),
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
