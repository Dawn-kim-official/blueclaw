package bluecollar

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

type TurnEventKind string

const (
	TurnEventReply    TurnEventKind = "reply"
	TurnEventTool     TurnEventKind = "tool"
	TurnEventApproval TurnEventKind = "approval"
)

type TurnEvent struct {
	Kind     TurnEventKind
	ToolName string
	Message  string
	Body     string
}

type TurnStream struct {
	Events   <-chan TurnEvent
	finished chan struct{}
	result   AgentTurnResult
	error    error
}

func (turnStream *TurnStream) Result() (AgentTurnResult, error) {
	<-turnStream.finished
	return turnStream.result, turnStream.error
}

const streamTurnEventBuffer = 64

func (agentTurnRunner *AgentTurnRunner) StreamTurn(ctx context.Context, request AgentTurnRequest) *TurnStream {
	events := make(chan TurnEvent, streamTurnEventBuffer)
	turnStream := &TurnStream{Events: events, finished: make(chan struct{})}
	taskRun := agentTurnRunner.taskRunForRequest(request)
	request.ExistingTaskRunID = taskRun.TaskRunID

	send := func(turnEvent TurnEvent) {
		select {
		case events <- turnEvent:
		default:
		}
	}
	unregisterObserver := agentTurnRunner.taskRunService.RegisterTaskRunObserver(taskRun.TaskRunID, func(rawTurnEvent taskstate.RawTurnEvent) {
		if turnEvent, isStreamable := decodeTurnEvent(rawTurnEvent); isStreamable {
			send(turnEvent)
		}
	})

	go func() {
		defer close(turnStream.finished)
		defer close(events)
		defer unregisterObserver()
		turnStream.result, turnStream.error = agentTurnRunner.RunTurn(ctx, request)
	}()
	return turnStream
}

type heldCallEventBody struct {
	ToolName     string `json:"toolName"`
	Confirmation string `json:"confirmation"`
}

func decodeHeldCallEventBody(body string) heldCallEventBody {
	decodedBody := heldCallEventBody{}
	json.Unmarshal([]byte(body), &decodedBody)
	return decodedBody
}

type checkpointEventBody struct {
	ToolName string `json:"toolName"`
	Message  string `json:"message"`
}

func decodeTurnEvent(rawTurnEvent taskstate.RawTurnEvent) (TurnEvent, bool) {
	if rawTurnEvent.Name == "agent.checkpoint.sent" {
		checkpointBody := decodeCheckpointEventBody(rawTurnEvent.Body)
		return TurnEvent{Kind: TurnEventReply, Message: checkpointBody.Message, ToolName: checkpointBody.ToolName}, true
	}
	if rawTurnEvent.Name == "approval.pending_call" {
		heldCall := decodeHeldCallEventBody(rawTurnEvent.Body)
		return TurnEvent{Kind: TurnEventApproval, ToolName: heldCall.ToolName, Message: heldCall.Confirmation}, true
	}
	if isToolResultEventName(rawTurnEvent.Name) {
		return TurnEvent{Kind: TurnEventTool, ToolName: toolResultEventToolName(rawTurnEvent.Body), Body: rawTurnEvent.Body}, true
	}
	return TurnEvent{}, false
}

func isToolResultEventName(name string) bool {
	trimmedName := strings.TrimSpace(name)
	return strings.HasPrefix(trimmedName, "tool.") && strings.HasSuffix(trimmedName, ".result")
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
