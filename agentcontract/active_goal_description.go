package agentcontract

import (
	"encoding/json"
	"strings"
)

func ActiveGoalDescription(activeGoal ActiveGoal) string {
	if strings.TrimSpace(activeGoal.GoalID) == "" &&
		strings.TrimSpace(activeGoal.TaskRunID) == "" &&
		strings.TrimSpace(activeGoal.OriginalInstruction) == "" &&
		strings.TrimSpace(activeGoal.CurrentObjective) == "" {
		return ""
	}
	document, errorValue := json.Marshal(activeGoal)
	if errorValue != nil {
		return ""
	}
	return "Active conversation goal:\n" + string(document) + "\nTreat the current user message as input to this goal unless it clearly starts a new unrelated request. Preserve the current user message as the latest user input; do not rewrite it.\nrequiredNextTools and selectedToolNames are intake suggestions, not commands: when a listed tool contradicts what the user visibly asked for, use the tool that actually fulfills the request (load it with request_tools if it is not exposed)."
}
