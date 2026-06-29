package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/policy"
)

func TestCapabilityInvokeUnknownOperationGivesRetryableSuggestion(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"content":"unexpected","status":"ok"}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{
		{Name: "task.add", Description: "Create a task.", PolicyResource: "tool:task.add", InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}}}`)},
		{Name: "task.list", Description: "List tasks.", PolicyResource: "tool:task.list"},
		{Name: "task.update", Description: "Update a task.", PolicyResource: "tool:task.update"},
		{Name: "calendar.add", Description: "Add a calendar event.", PolicyResource: "tool:calendar.add"},
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:  "default",
		PersonAccess: policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: agent.CapabilityInvokeToolName,
		Input:    json.RawMessage(`{"operation":"flow.task.add","input":{"prompt":"add a task"}}`),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if strings.Contains(httpClient.requestBody, "flow.task.add") {
		t.Fatalf("unknown op must NOT be forwarded to capabilityd, got request %q", httpClient.requestBody)
	}
	if !result.Failed() {
		t.Fatalf("expected unknown op to fail, got success")
	}
	if result.Failure.Kind != agent.FailureInvalidInput {
		t.Fatalf("expected invalid_input failure kind, got %s", result.Failure.Kind)
	}
	content := result.ContentText()
	if !strings.Contains(content, "task.add") || !strings.Contains(content, "Did you mean") {
		t.Fatalf("expected a task.add suggestion, got %q", content)
	}
	if strings.Contains(content, "calendar.add") {
		t.Fatalf("calendar.add shares no segment with flow.task.add and must not be suggested, got %q", content)
	}
}

func TestCapabilityInvokeKnownOperationStillForwards(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"content":"task created","status":"ok","result":{"taskID":"task-1"}}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{
		{Name: "task.add", Description: "Create a task.", PolicyResource: "tool:task.add", InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}}}`)},
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:  "default",
		PersonAccess: policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: agent.CapabilityInvokeToolName,
		Input:    json.RawMessage(`{"operation":"task.add","input":{"prompt":"add a task"}}`),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected task.add success, got %s", result.ContentText())
	}
	if !strings.Contains(httpClient.requestBody, "add a task") {
		t.Fatalf("expected task.add to forward to capabilityd, got request %q", httpClient.requestBody)
	}
}
