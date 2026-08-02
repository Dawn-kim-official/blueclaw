package agentcontract

import (
	"encoding/json"
	"strings"
)

func PriorTaskContextDescription(context PriorTaskContext) string {
	if !priorTaskContextHasContent(context) {
		return ""
	}
	document, errorValue := json.Marshal(context)
	if errorValue != nil {
		return ""
	}
	return strings.Join([]string{
		"Prior task context:",
		string(document),
		"This is context for interpreting the latest user message, not permission to finish from old text. If the latest user message asks to deliver, retry, continue, or revise this prior task's outcome, set priorTaskReference=outcome_recovery. If it is unrelated or self-contained, set priorTaskReference=none. When recovering an outcome, infer the needed structured output formats from the prior task prompt, prior result, known contract, and latest user message. A file deliverable reaches the user only through successful file_deliver completionEvidence in the current task; a prepared file, generated path, task link, or prior result text is not delivery.",
	}, "\n")
}

func priorTaskContextHasContent(context PriorTaskContext) bool {
	return strings.TrimSpace(context.TaskRunID) != "" ||
		strings.TrimSpace(context.Prompt) != "" ||
		strings.TrimSpace(context.Result) != ""
}
