package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCapabilityCatalogParametersExposesNestedRequired(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["content"],"properties":{"content":{"type":"object","required":["title"],"properties":{"title":{"type":"string"},"blocks":{"type":"array","items":{"type":"string"}}}}}}`)
	rendered := capabilityCatalogParameters(schema)
	for _, fragment := range []string{"content object{", "title string (required)", "blocks array[string]"} {
		if !strings.Contains(rendered, fragment) {
			t.Fatalf("expected nested render to contain %q, got %q", fragment, rendered)
		}
	}
}

func TestCapabilityCatalogParametersRendersEnum(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["targetType"],"properties":{"targetType":{"type":"string","enum":["directMessage","channel"]}}}`)
	rendered := capabilityCatalogParameters(schema)
	if !strings.Contains(rendered, "targetType enum(directMessage|channel) (required)") {
		t.Fatalf("expected enum render, got %q", rendered)
	}
}
