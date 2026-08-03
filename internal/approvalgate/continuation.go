package approvalgate

import (
	"strings"

	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

func ApprovalContinuationNote(taskEvents []taskstate.TaskEvent) string {
	heldToolName := ""
	decision := ""
	for _, taskEvent := range taskEvents {
		switch taskEvent.Name {
		case "approval.pending_call":
			heldToolName = decodeHeldCallEventBody(taskEvent.Body).ToolName
		case "approval.decided":
			decision = decodedDecision(taskEvent.Body)
		case "approval.executed":
			if executedToolName(taskEvent.Body) == heldToolName {
				heldToolName = ""
				decision = ""
			}
		}
	}
	if heldToolName == "" || decision == "" {
		return ""
	}
	if decision == "cancel" {
		return "The requester declined the " + heldToolName + " call you asked about. Do not attempt it again; continue without it or stop and say why you cannot."
	}
	return "The requester approved the " + heldToolName + " call you asked about, and it has not run yet. Issue that exact call again now to carry it out."
}

func decodedDecision(body string) string {
	decidedBody := struct {
		Decision string `json:"decision"`
	}{}
	unmarshalEventBody(body, &decidedBody)
	return strings.TrimSpace(decidedBody.Decision)
}
