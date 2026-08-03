package bluecollar

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Dawn-kim-official/blueclaw/model"
)

type ProbedAgentAction struct {
	Action    string
	ToolName  string
	ToolInput json.RawMessage
}

func ProbeAgentActionSchema(responseContext context.Context, languageModel model.LanguageModelProvider, request AgentTurnRequest) (ProbedAgentAction, error) {
	structuredRequest := BuildAgentActionRequest(agentTaskState{Request: request})
	response, errorValue := languageModel.GenerateStructuredResponse(responseContext, structuredRequest)
	if errorValue != nil {
		return ProbedAgentAction{}, errorValue
	}
	action, errorValue := ParseAgentActionResponse(response)
	if errorValue != nil {
		return ProbedAgentAction{}, fmt.Errorf("agent action response %q is not parsable: %w", response.Content, errorValue)
	}
	return ProbedAgentAction{
		Action:    action.Action,
		ToolName:  action.ToolName,
		ToolInput: action.ToolInput,
	}, nil
}
