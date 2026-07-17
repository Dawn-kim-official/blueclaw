package mcp

import (
	"strings"
	"testing"
)

func TestParseToolResultPreservesErrorState(t *testing.T) {
	result, errorValue := ParseToolResult(`{"content":[],"isError":true}`)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.IsError {
		t.Fatal("expected MCP error state")
	}
}

func TestParseToolResultRejectsInvalidOutput(t *testing.T) {
	for _, output := range []string{"invalid", `{"content":[]}`} {
		_, errorValue := ParseToolResult(output)
		if errorValue == nil || !strings.Contains(errorValue.Error(), "invalid normalized result") {
			t.Fatalf("expected invalid result error, got %v", errorValue)
		}
	}
}
