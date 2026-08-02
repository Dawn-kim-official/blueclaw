package acpagent

import (
	"context"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

func sessionUpdateForTurnEvent(turnEvent bluecollar.TurnEvent, toolSet *toolcontract.ToolSet, toolCallIdentity acp.ToolCallId) acp.SessionUpdate {
	if turnEvent.Kind == bluecollar.TurnEventTool {
		return acp.StartToolCall(
			toolCallIdentity,
			toolCallTitle(turnEvent),
			acp.WithStartKind(toolKindForTool(toolSet, turnEvent.ToolName)),
			acp.WithStartStatus(acp.ToolCallStatusCompleted),
			acp.WithStartRawOutput(turnEvent.Body),
		)
	}
	return acp.UpdateAgentMessageText(turnEvent.Message)
}

func toolCallTitle(turnEvent bluecollar.TurnEvent) string {
	toolName := strings.TrimSpace(turnEvent.ToolName)
	if toolName == "" {
		return "tool call"
	}
	return toolName
}

func toolKindForTool(toolSet *toolcontract.ToolSet, toolName string) acp.ToolKind {
	if toolSet == nil {
		return acp.ToolKindOther
	}
	toolDefinition, isRegistered := toolSet.ToolDefinition(strings.TrimSpace(toolName))
	if !isRegistered {
		return acp.ToolKindOther
	}
	switch toolcontract.ToolDefinitionSideEffectClass(toolDefinition) {
	case toolcontract.ToolSideEffectRead:
		return acp.ToolKindRead
	case toolcontract.ToolSideEffectComputation:
		return acp.ToolKindThink
	case toolcontract.ToolSideEffectStateChange, toolcontract.ToolSideEffectWorkspaceWrite, toolcontract.ToolSideEffectLocalFile:
		return acp.ToolKindEdit
	case toolcontract.ToolSideEffectDestructive:
		return acp.ToolKindDelete
	}
	return acp.ToolKindOther
}

func stopReasonForTurn(ctx context.Context, turnResult agentcontract.AgentTurnResult) acp.StopReason {
	if ctx.Err() != nil {
		return acp.StopReasonCancelled
	}
	switch turnResult.TaskRun.Status {
	case taskstate.TaskStatusCancelled, taskstate.TaskStatusInterrupted:
		return acp.StopReasonCancelled
	case taskstate.TaskStatusBlocked:
		return acp.StopReasonMaxTurnRequests
	}
	return acp.StopReasonEndTurn
}
