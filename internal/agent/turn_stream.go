package agent

import (
	"context"
	"encoding/json"

	"blueclaw/internal/task"
)

type TurnEventKind string

const (
	TurnEventReply TurnEventKind = "reply"
	TurnEventTool  TurnEventKind = "tool"
	TurnEventFinal TurnEventKind = "final"
)

type TurnEvent struct {
	Kind     TurnEventKind
	ToolName string
	Message  string
	Body     string
	Result   AgentTurnResult
	Error    error
}

const streamTurnEventBuffer = 64

func (agentTurnRunner *AgentTurnRunner) StreamTurn(ctx context.Context, request AgentTurnRequest) <-chan TurnEvent {
	events := make(chan TurnEvent, streamTurnEventBuffer)
	taskRun := agentTurnRunner.taskRunForRequest(request)
	request.ExistingTaskRunID = taskRun.TaskRunID

	send := func(turnEvent TurnEvent) {
		select {
		case events <- turnEvent:
		default:
		}
	}
	unregisterObserver := agentTurnRunner.taskRunService.RegisterTaskRunObserver(taskRun.TaskRunID, func(rawTurnEvent task.RawTurnEvent) {
		if turnEvent, isStreamable := decodeTurnEvent(rawTurnEvent); isStreamable {
			send(turnEvent)
		}
	})

	go func() {
		defer close(events)
		defer unregisterObserver()
		result, errorValue := agentTurnRunner.RunTurn(ctx, request)
		send(TurnEvent{Kind: TurnEventFinal, Result: result, Error: errorValue})
	}()
	return events
}

type checkpointEventBody struct {
	ToolName string `json:"toolName"`
	Message  string `json:"message"`
}

func decodeTurnEvent(rawTurnEvent task.RawTurnEvent) (TurnEvent, bool) {
	if rawTurnEvent.Name == "agent.checkpoint.sent" {
		checkpointBody := decodeCheckpointEventBody(rawTurnEvent.Body)
		return TurnEvent{Kind: TurnEventReply, Message: checkpointBody.Message, ToolName: checkpointBody.ToolName}, true
	}
	if artifactManifestToolNameFromEvent(rawTurnEvent.Name) != "" {
		return TurnEvent{Kind: TurnEventTool, ToolName: toolResultEventToolName(rawTurnEvent.Body), Body: rawTurnEvent.Body}, true
	}
	return TurnEvent{}, false
}

func toolResultEventToolName(body string) string {
	var document struct {
		Tool string `json:"tool"`
	}
	if json.Unmarshal([]byte(body), &document) != nil {
		return ""
	}
	return document.Tool
}

func decodeCheckpointEventBody(body string) checkpointEventBody {
	parsed := checkpointEventBody{}
	_ = json.Unmarshal([]byte(body), &parsed)
	return parsed
}

func CheckpointReplyMessage(body string) string {
	return decodeCheckpointEventBody(body).Message
}
