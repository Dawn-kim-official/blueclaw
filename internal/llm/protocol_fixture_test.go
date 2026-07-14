package llm

import (
	"encoding/json"
	"os"
	"testing"
)

func TestProtocolStructuredRequestFixtureMatchesCapabilityRequest(t *testing.T) {
	documentBytes, errorValue := os.ReadFile("../../protocol/fixtures/valid/structured-response-request.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var request capabilityStructuredResponseRequestDocument
	if errorValue := json.Unmarshal(documentBytes, &request); errorValue != nil {
		t.Fatal(errorValue)
	}
	if request.ExecutionMode != "auto" || request.Model != "openrouter/auto" {
		t.Fatalf("unexpected structured request fixture: %#v", request)
	}
	if request.Context == nil || request.Context.RequesterPersonID != "person-1" {
		t.Fatalf("structured request fixture lost requester context: %#v", request.Context)
	}
	if request.StructuredOutputSchema.Name != "task_list" || len(request.StructuredOutputSchema.Document) == 0 {
		t.Fatalf("structured request fixture lost output schema: %#v", request.StructuredOutputSchema)
	}
}
