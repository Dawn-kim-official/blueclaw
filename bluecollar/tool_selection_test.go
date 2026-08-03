package bluecollar

import ()

import "testing"

func TestApplyToolRequestNormalizesContinueActionToolNames(t *testing.T) {
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"file_deliver", "file.promote", "terminal_run"})}

	updatedRequest, result := applyToolRequest(request, requestToolsArguments{
		ToolNames: []string{"continue__file_deliver", "continue__file_promote", "terminal_run"},
	})

	if len(result.UnknownToolNames) != 0 {
		t.Fatalf("expected no unknown tools, got %+v", result.UnknownToolNames)
	}
	for _, toolName := range []string{"file_deliver", "file.promote", "terminal_run"} {
		if !containsString(result.PinnedToolNames, toolName) {
			t.Fatalf("expected result to pin %s, got %+v", toolName, result.PinnedToolNames)
		}
		if !containsString(updatedRequest.PinnedToolNames, toolName) {
			t.Fatalf("expected request to pin %s, got %+v", toolName, updatedRequest.PinnedToolNames)
		}
	}
}

func TestApplyToolRequestKeepsLegacyContinueActionToolNameUnknown(t *testing.T) {
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"file_deliver"})}

	_, result := applyToolRequest(request, requestToolsArguments{
		ToolNames: []string{"continue__file_attach"},
	})

	if !containsString(result.UnknownToolNames, "continue__file_attach") {
		t.Fatalf("expected legacy synthetic tool to remain unknown, got %+v", result.UnknownToolNames)
	}
}

func TestApplyToolRequestKeepsUnknownSyntheticToolNameUnknown(t *testing.T) {
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"file_deliver"})}

	_, result := applyToolRequest(request, requestToolsArguments{
		ToolNames: []string{"continue__spreadsheet_export"},
	})

	if !containsString(result.UnknownToolNames, "continue__spreadsheet_export") {
		t.Fatalf("expected unknown synthetic tool to remain unknown, got %+v", result.UnknownToolNames)
	}
}
