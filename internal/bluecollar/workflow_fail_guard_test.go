package bluecollar

import (
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
)

import "testing"

func TestRecoverableWorkflowNextToolsSuggestsFileDeliveryAfterSourceProgress(t *testing.T) {
	toolSet := newTestToolSet([]string{"terminal.run", toolcontract.FileDeliverToolName})
	request := AgentTurnRequest{
		RequiredAttachmentSuffixes: []string{".docx"},
		ToolSet:                    toolSet,
	}
	observations := []turnObservation{successfulWorkflowObservation("file.write")}

	nextTools := recoverableWorkflowNextTools(request, observations)

	for _, toolName := range []string{"terminal.run", toolcontract.FileDeliverToolName} {
		if !containsString(nextTools, toolName) {
			t.Fatalf("expected file delivery recovery tools to include %s, got %+v", toolName, nextTools)
		}
	}
}

func TestRecoverableWorkflowNextToolsStopsAfterDeliver(t *testing.T) {
	toolSet := newTestToolSet([]string{"terminal.run", toolcontract.FileDeliverToolName})
	request := AgentTurnRequest{
		RequiredAttachmentSuffixes: []string{".docx"},
		ToolSet:                    toolSet,
	}
	observations := []turnObservation{successfulWorkflowObservation(toolcontract.FileDeliverToolName)}

	nextTools := recoverableWorkflowNextTools(request, observations)

	if len(nextTools) != 0 {
		t.Fatalf("expected no file delivery recovery after attach, got %+v", nextTools)
	}
}

func successfulWorkflowObservation(toolName string) turnObservation {
	return turnObservation{
		Action: "continue",
		Tool:   toolName,
	}
}
