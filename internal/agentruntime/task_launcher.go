package agentruntime

import (
	"context"

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
	Source                    TaskLaunchSource
	SourceReference           string
	RequesterPersonID         string
	RequesterName             string
	RequesterCallingName      string
	RequesterHandle           string
	RequesterEmail            string
	RequesterPlatformUserID   string
	ProfileName               string
	Platform                  string
	ConversationID            string
	ConversationType          string
	ConversationChannelID     string
	ConversationChannelName   string
	ReplyTargetID             string
	Prompt                    string
	VisibleContext            agent.VisibleContext
	HistoryProvider           HistoryProvider
	PersonAccess              policy.PersonAccess
	MemoryNamespaces          []memory.MemoryNamespace
	AccessibleConversationIDs []string
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
	normalizedProfileName := normalizeProfileName(request.ProfileName)
	toolRegistry := taskLauncher.toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{
		ProfileName:               normalizedProfileName,
		Prompt:                    request.Prompt,
		VisibleContext:            request.VisibleContext,
		RequesterPersonID:         request.RequesterPersonID,
		RequesterName:             request.RequesterName,
		RequesterEmail:            request.RequesterEmail,
		RequesterPlatformUserID:   request.RequesterPlatformUserID,
		ConversationID:            request.ConversationID,
		ConversationType:          request.ConversationType,
		ConversationChannelID:     request.ConversationChannelID,
		ConversationChannelName:   request.ConversationChannelName,
		ReplyTargetID:             request.ReplyTargetID,
		Platform:                  request.Platform,
		HistoryCursor:             request.VisibleContext.HistoryCursor,
		HistoryProvider:           request.HistoryProvider,
		PersonAccess:              request.PersonAccess,
		MemoryNamespaces:          request.MemoryNamespaces,
		AccessibleConversationIDs: request.AccessibleConversationIDs,
	})
	toolNames := toolRegistry.ListToolNames()
	memoryFacts, errorValue := taskLauncher.toolCatalogBuilder.SearchMemory(ctx, TaskMemoryRequest{
		Query:                     request.Prompt,
		RequesterPersonID:         request.RequesterPersonID,
		ConversationID:            request.ConversationID,
		PersonAccess:              request.PersonAccess,
		MemoryNamespaces:          request.MemoryNamespaces,
		AccessibleConversationIDs: request.AccessibleConversationIDs,
	})
	memorySearchError := ""
	if errorValue != nil {
		memorySearchError = errorValue.Error()
		memoryFacts = nil
	}
	turnResult, errorValue := taskLauncher.agentKernel.RunTurn(ctx, agent.AgentTurnRequest{
		RequesterPersonID:       request.RequesterPersonID,
		RequesterEmail:          request.RequesterEmail,
		RequesterName:           request.RequesterName,
		RequesterPlatformUserID: request.RequesterPlatformUserID,
		Platform:                request.Platform,
		RequesterCallingName:    request.RequesterCallingName,
		RequesterHandle:         request.RequesterHandle,
		RequesterCircles:        append([]string{}, request.PersonAccess.Circles...),
		ProfileName:             normalizedProfileName,
		ConversationID:          request.ConversationID,
		Prompt:                  request.Prompt,
		VisibleContext:          request.VisibleContext,
		MemoryFacts:             memoryFacts,
		ToolRegistry:            toolRegistry,
		WorkspaceRootPath:       taskLauncher.toolCatalogBuilder.WorkspaceRootPath(),
	})
	if errorValue != nil {
		return TaskLaunchResult{}, errorValue
	}
	if turnResult.TaskRun.TaskRunID != "" {
		if memorySearchError != "" {
			taskLauncher.agentKernel.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "memory.search_failed", memorySearchError)
		} else {
			taskLauncher.agentKernel.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "memory.search_succeeded", marshalToolResult(map[string]any{"factCount": len(memoryFacts)}))
		}
		taskLauncher.agentKernel.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "agent.task_launched", marshalTaskLaunchEvent(request, normalizedProfileName, toolNames, len(memoryFacts)))
		conversationScope := ConversationScopeForRequest(taskLauncher.toolCatalogBuilder.WorkspaceRootPath(), ToolCatalogRequest{
			RequesterPersonID:       request.RequesterPersonID,
			ConversationID:          request.ConversationID,
			ConversationType:        request.ConversationType,
			ConversationChannelID:   request.ConversationChannelID,
			ConversationChannelName: request.ConversationChannelName,
		})
		taskLauncher.agentKernel.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "agent.conversation_scope", marshalToolResult(conversationScope))
	}
	return TaskLaunchResult{
		TurnResult:            turnResult,
		MemoryFacts:           memoryFacts,
		ToolNames:             toolNames,
		NormalizedProfileName: normalizedProfileName,
	}, nil
}
