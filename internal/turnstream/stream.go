package turnstream

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

type EventKind string

const (
	EventReply    EventKind = "reply"
	EventTool     EventKind = "tool"
	EventApproval EventKind = "approval"
)

type Event struct {
	Kind     EventKind
	ToolName string
	Message  string
	Body     string
}

type Stream struct {
	Events   <-chan Event
	finished chan struct{}
	result   agentcontract.AgentTurnResult
	error    error
}

func (stream *Stream) Result() (agentcontract.AgentTurnResult, error) {
	<-stream.finished
	return stream.result, stream.error
}

const eventBuffer = 64

type Streamer struct {
	harness      agentcontract.Harness
	taskRunStore taskstate.TaskRunStore
}

func New(harness agentcontract.Harness, taskRunStore taskstate.TaskRunStore) *Streamer {
	return &Streamer{harness: harness, taskRunStore: taskRunStore}
}

func (streamer *Streamer) StreamTurn(ctx context.Context, request agentcontract.AgentTurnRequest) *Stream {
	events := make(chan Event, eventBuffer)
	stream := &Stream{Events: events, finished: make(chan struct{})}
	taskRun := streamer.taskRunForRequest(request)
	request.ExistingTaskRunID = taskRun.TaskRunID

	unregisterObserver := streamer.taskRunStore.RegisterTaskRunObserver(taskRun.TaskRunID, func(rawTurnEvent taskstate.RawTurnEvent) {
		event, isStreamable := decodeEvent(rawTurnEvent)
		if !isStreamable {
			return
		}
		select {
		case events <- event:
		default:
		}
	})

	go func() {
		defer close(stream.finished)
		defer close(events)
		defer unregisterObserver()
		stream.result, stream.error = streamer.harness.RunTurn(ctx, request)
	}()
	return stream
}

func (streamer *Streamer) taskRunForRequest(request agentcontract.AgentTurnRequest) taskstate.TaskRun {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := streamer.taskRunStore.FindTaskRun(taskRunID); isFound {
			return taskRun
		}
	}
	return streamer.taskRunStore.CreateTaskRunWithOrigin(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}

func decodeEvent(rawTurnEvent taskstate.RawTurnEvent) (Event, bool) {
	switch {
	case rawTurnEvent.Name == "agent.checkpoint.sent":
		checkpoint := decodeBody[checkpointEventBody](rawTurnEvent.Body)
		return Event{Kind: EventReply, Message: checkpoint.Message, ToolName: checkpoint.ToolName}, true
	case rawTurnEvent.Name == "approval.pending_call":
		heldCall := decodeBody[heldCallEventBody](rawTurnEvent.Body)
		return Event{Kind: EventApproval, ToolName: heldCall.ToolName, Message: heldCall.Confirmation}, true
	case isToolResultEventName(rawTurnEvent.Name):
		return Event{Kind: EventTool, ToolName: toolResultEventToolName(rawTurnEvent.Body), Body: rawTurnEvent.Body}, true
	}
	return Event{}, false
}

type checkpointEventBody struct {
	ToolName string `json:"toolName"`
	Message  string `json:"message"`
}

type heldCallEventBody struct {
	ToolName     string `json:"toolName"`
	Confirmation string `json:"confirmation"`
}

type toolResultEventBody struct {
	Tool string `json:"tool"`
}

func decodeBody[BodyType any](body string) BodyType {
	var decodedBody BodyType
	json.Unmarshal([]byte(body), &decodedBody)
	return decodedBody
}

func toolResultEventToolName(body string) string {
	return strings.TrimSpace(decodeBody[toolResultEventBody](body).Tool)
}

func isToolResultEventName(name string) bool {
	trimmedName := strings.TrimSpace(name)
	return strings.HasPrefix(trimmedName, "tool.") && strings.HasSuffix(trimmedName, ".result")
}
