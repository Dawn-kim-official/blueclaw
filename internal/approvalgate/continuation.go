package approvalgate

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type ApprovedCall struct {
	ToolName  string
	ToolInput json.RawMessage
}

func ApprovedPendingCall(taskEvents []taskstate.TaskEvent) (ApprovedCall, bool) {
	heldCall, decision := undecidedHeldCall(taskEvents)
	if heldCall.ToolName == "" || !isApprovingDecision(decision) {
		return ApprovedCall{}, false
	}
	return ApprovedCall{ToolName: heldCall.ToolName, ToolInput: heldCall.ToolInput}, true
}

func DeclinedCallNote(taskEvents []taskstate.TaskEvent) string {
	heldCall, decision := undecidedHeldCall(taskEvents)
	if heldCall.ToolName == "" || decision != "cancel" {
		return ""
	}
	return "The requester declined the " + heldCall.ToolName + " call you asked about. Do not attempt it again; continue without it or stop and say why you cannot."
}

func undecidedHeldCall(taskEvents []taskstate.TaskEvent) (heldCallRecord, string) {
	heldCall := heldCallRecord{}
	decision := ""
	for _, taskEvent := range taskEvents {
		switch taskEvent.Name {
		case "approval.pending_call":
			heldCall = decodeHeldCallEventBody(taskEvent.Body)
			decision = ""
		case "approval.decided":
			decision = decodedDecision(taskEvent.Body)
		case "approval.executed":
			if executedToolName(taskEvent.Body) == heldCall.ToolName {
				heldCall = heldCallRecord{}
				decision = ""
			}
		}
	}
	return heldCall, decision
}

func isApprovingDecision(decision string) bool {
	return decision == "confirm" || decision == "confirm_task"
}

func decodedDecision(body string) string {
	decidedBody := struct {
		Decision string `json:"decision"`
	}{}
	unmarshalEventBody(body, &decidedBody)
	return strings.TrimSpace(decidedBody.Decision)
}
