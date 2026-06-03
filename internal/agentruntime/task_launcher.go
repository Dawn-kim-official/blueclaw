package agentruntime

import (
	"context"
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
	agentKernel        *agent.AgentKernel
	toolCatalogBuilder *ToolCatalogBuilder
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
	ExistingTaskRunID          string
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
	PrecomputedTurnDecision    *agent.TurnDecision
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

func NewTaskLauncher(agentKernel *agent.AgentKernel, toolCatalogBuilder *ToolCatalogBuilder) *TaskLauncher {
	if toolCatalogBuilder == nil {
		toolCatalogBuilder = NewToolCatalogBuilder()
	}
	return &TaskLauncher{
		agentKernel:        agentKernel,
		toolCatalogBuilder: toolCatalogBuilder,
	}
}

func (taskLauncher *TaskLauncher) Launch(ctx context.Context, request TaskLaunchRequest) (TaskLaunchResult, error) {
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
	toolSet := taskLauncher.toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:                normalizedProfileName,
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
	})
	toolNames := toolSet.ListToolNames()
	conversationScope := ConversationScopeForRequest(taskLauncher.toolCatalogBuilder.WorkspaceRootPath(), ToolCatalogRequest{
		RequesterPersonID:       request.RequesterPersonID,
		ConversationID:          request.ConversationID,
		ConversationType:        request.ConversationType,
		ConversationChannelID:   request.ConversationChannelID,
		ConversationChannelName: request.ConversationChannelName,
	})
	memoryFacts, errorValue := taskLauncher.toolCatalogBuilder.LoadPinnedMemory(ctx, TaskPinnedMemoryRequest{
		RequesterPersonID: request.RequesterPersonID,
	})
	pinnedMemoryError := ""
	if errorValue != nil {
		pinnedMemoryError = errorValue.Error()
		memoryFacts = nil
	}
	turnResult, errorValue := taskLauncher.agentKernel.RunTurn(ctx, agent.AgentTurnRequest{
		RequesterPersonID:       request.RequesterPersonID,
		RequesterEmail:          request.RequesterEmail,
		RequesterName:           request.RequesterName,
		RequesterPlatformUserID: request.RequesterPlatformUserID,
		IsApprovalContinuation:  request.IsApprovalContinuation,
		ExistingTaskRunID:       request.ExistingTaskRunID,
		Platform:                request.Platform,
		RequesterCallingName:    request.RequesterCallingName,
		RequesterHandle:         request.RequesterHandle,
		RequesterCircles:        append([]string{}, request.PersonAccess.Circles...),
		ProfileName:             normalizedProfileName,
		ConversationID:          request.ConversationID,
		Prompt:                  request.Prompt,
		InputParts:              append([]agent.AgentPart{}, request.InputParts...),
		ResponseLanguage:        request.ResponseLanguage,
		VisibleContext:          request.VisibleContext,
		ActiveGoal:              request.ActiveGoal,
		PrecomputedTurnDecision: request.PrecomputedTurnDecision,
		MemoryFacts:             memoryFacts,
		ToolSet:                 toolSet,
		PinnedToolNames:         append([]string{}, request.PinnedToolNames...),
		PinnedSkillNames:        append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:       taskLauncher.toolCatalogBuilder.WorkspaceRootPath(),
		WorkspaceDefaultPath:    conversationScope.DefaultDirectoryPath,
		CheckpointSender:        request.CheckpointSender,
	})
	if errorValue != nil {
		return TaskLaunchResult{}, errorValue
	}
	launchedToolNames := turnResult.ToolNames
	if len(launchedToolNames) == 0 {
		launchedToolNames = toolNames
	}
	if turnResult.TaskRun.TaskRunID != "" {
		if pinnedMemoryError != "" {
			taskLauncher.agentKernel.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "memory.pinned_load_failed", pinnedMemoryError)
		} else {
			taskLauncher.agentKernel.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "memory.pinned_load_succeeded", marshalToolResult(map[string]any{"factCount": len(memoryFacts)}))
		}
		taskLauncher.agentKernel.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "agent.task_launched", marshalTaskLaunchEvent(request, normalizedProfileName, launchedToolNames, len(memoryFacts)))
		taskLauncher.agentKernel.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "agent.conversation_scope", marshalToolResult(conversationScope))
	}
	return TaskLaunchResult{
		TurnResult:            turnResult,
		MemoryFacts:           memoryFacts,
		ToolNames:             launchedToolNames,
		NormalizedProfileName: normalizedProfileName,
	}, nil
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
