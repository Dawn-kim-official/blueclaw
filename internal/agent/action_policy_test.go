package agent

import (
	"encoding/json"
	"testing"
)

func TestActionPolicyHidesFinishUntilRequiredAttachmentExists(t *testing.T) {
	contract := normalizeExecutionContract(ExecutionContract{
		ActionPolicy: ActionPolicy{CanSelectTools: true, CanSetQualityCriteria: true, CanFail: true, FinishExposure: FinishExposureWhenReady},
		FinishPolicy: FinishPolicy{
			RequiresAttachment:         true,
			RequiredAttachmentSuffixes: []string{".pptx"},
		},
	})

	request := BuildAgentActionRequest(agentTaskState{
		Request: AgentTurnRequest{
			Prompt:            "PPTX 만들어줘",
			ExecutionContract: contract,
			ToolSet:           newTestToolSet([]string{"file.attach"}),
		},
		Options: TurnOptions{},
	})

	if actionSchemaContainsAction(t, request.StructuredOutputSchema.Document, "finish") {
		t.Fatalf("expected finish to be hidden before required attachment exists, got %s", request.StructuredOutputSchema.Document)
	}
}

func TestActionPolicyShowsFinishAfterRequiredAttachmentExists(t *testing.T) {
	contract := normalizeExecutionContract(ExecutionContract{
		ActionPolicy: ActionPolicy{CanSelectTools: true, CanSetQualityCriteria: true, CanFail: true, FinishExposure: FinishExposureWhenReady},
		FinishPolicy: FinishPolicy{
			RequiresAttachment:         true,
			RequiredAttachmentSuffixes: []string{".pptx"},
		},
	})

	request := BuildAgentActionRequest(agentTaskState{
		Request: AgentTurnRequest{
			Prompt:            "PPTX 만들어줘",
			ExecutionContract: contract,
			ToolSet:           newTestToolSet([]string{"file.attach"}),
		},
		Attachments: []FileAttachment{{Filename: "deck.pptx", DevicePath: "artifacts/deck/deck.pptx"}},
		Options:     TurnOptions{},
	})

	if !actionSchemaContainsAction(t, request.StructuredOutputSchema.Document, "finish") {
		t.Fatalf("expected finish after required attachment exists, got %s", request.StructuredOutputSchema.Document)
	}
}

func TestActionPolicyAllowsImmediateFinishForQuickReply(t *testing.T) {
	request := BuildAgentActionRequest(agentTaskState{
		Request: AgentTurnRequest{
			Prompt:  "안녕",
			ToolSet: newTestToolSet(nil),
		},
		Options: TurnOptions{},
	})

	if !actionSchemaContainsAction(t, request.StructuredOutputSchema.Document, "finish") {
		t.Fatalf("expected quick reply schema to allow finish, got %s", request.StructuredOutputSchema.Document)
	}
}

func actionSchemaContainsAction(t *testing.T, schemaDocument string, actionName string) bool {
	t.Helper()
	var document struct {
		OneOf []struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		} `json:"oneOf"`
	}
	if errorValue := json.Unmarshal([]byte(schemaDocument), &document); errorValue != nil {
		t.Fatalf("invalid action schema: %v", errorValue)
	}
	for _, variant := range document.OneOf {
		actionProperty, isFound := variant.Properties["action"]
		if !isFound {
			continue
		}
		if containsString(actionProperty.Enum, actionName) {
			return true
		}
	}
	return false
}
