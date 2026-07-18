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

func TestParseToolResultPreservesStructuredContent(t *testing.T) {
	result, errorValue := ParseToolResult(`{"content":[],"structuredContent":{"text":"blueclaw"},"isError":false}`)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(result.StructuredContent) != `{"text":"blueclaw"}` {
		t.Fatalf("expected structured content, got %s", result.StructuredContent)
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
