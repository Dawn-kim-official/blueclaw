package agentruntime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/policy"
)

func TestToolCatalogHidesPolicyDeniedCapabilityTools(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:           "site.create",
		Description:    "Create a site.",
		PolicyResource: "tool:site.create",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
			ResourceAccessRules: []policy.ResourceAccessPolicy{{
				Resource: "tool:site.create",
				Actions:  []string{"execute"},
				Circles:  []string{"admin"},
			}},
		},
	})

	if strings.Contains(toolRegistry.Descriptions(), "site.create") {
		t.Fatalf("expected denied site tool to be omitted from catalog, got %s", toolRegistry.Descriptions())
	}
}

func TestPlatformDMSendAvailabilityDependsOnTrustedContext(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:             "message.send",
		Description:      "Send a direct message",
		RequiresApproval: true,
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"message.send"})

	immediateToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	if strings.Contains(immediateToolSet.Descriptions(), "ask approval before invoking") {
		t.Fatalf("expected immediate DM to be available for runtime gating, got %s", immediateToolSet.Descriptions())
	}
	toolDefinition, isFound := immediateToolSet.ToolDefinition("message.send")
	if !isFound || !toolDefinition.RequiresApproval {
		t.Fatalf("expected immediate DM definition to require approval, got found=%v definition=%+v", isFound, toolDefinition)
	}
	scheduledToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", IsScheduledRun: true})
	if strings.Contains(scheduledToolSet.Descriptions(), "ask approval before invoking") {
		t.Fatalf("expected scheduled DM to be available, got %s", scheduledToolSet.Descriptions())
	}
	approvedToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", IsApprovalContinuation: true})
	if strings.Contains(approvedToolSet.Descriptions(), "ask approval before invoking") {
		t.Fatalf("expected approved continuation DM to be available, got %s", approvedToolSet.Descriptions())
	}
}

func TestCapabilityToolRequestIncludesTrustedExecutionContext(t *testing.T) {
	requestDocument := capabilityToolRequest(context.Background(), "message.send", ToolCatalogRequest{
		TaskSource:              TaskLaunchSourceScheduled,
		IsScheduledRun:          true,
		IsApprovalContinuation:  true,
		RequesterPersonID:       "person-1",
		RequesterPlatformUserID: "mattermost-user-1",
		ConversationID:          "conversation-1",
		ConversationChannelID:   "channel-1",
		ReplyTargetID:           "reply-target-1",
		Platform:                "mattermost",
	}, json.RawMessage(`{"deliveryTarget":{"type":"directMessage","personHint":"동하"},"message":"테스트"}`))
	contextDocument, isFound := requestDocument["context"].(map[string]any)
	if !isFound {
		t.Fatalf("expected context document, got %+v", requestDocument)
	}
	if contextDocument["taskSource"] != string(TaskLaunchSourceScheduled) || contextDocument["isScheduledRun"] != true || contextDocument["isApprovalContinuation"] != true {
		t.Fatalf("expected trusted execution context, got %+v", contextDocument)
	}
	if contextDocument["replyTargetID"] != "reply-target-1" {
		t.Fatalf("expected reply target in context, got %+v", contextDocument)
	}
}

func TestImageReadResolvesAttachmentMaterialID(t *testing.T) {
	workspacePath := t.TempDir()
	imagePath := filepath.Join(workspacePath, "circles", "staff", "inbox", "mattermost", "thread-1", "post-1", "mascot.png")
	writeTestFile(t, imagePath, "image")
	httpClient := &recordingHTTPClient{responseBody: `{"content":"image loaded","status":"ok","result":{"status":"ok"}}`}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "image.read",
		PolicyResource: "tool:image.read",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"materialID":{"type":"string"},"path":{"type":"string"}}}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agent.VisibleContextMaterial{
				MaterialID:  "mattermost:file-1",
				Filename:    "mascot.png",
				ContentType: "image/png",
				Path:        "/workspace/circles/staff/inbox/mattermost/thread-1/post-1/mascot.png",
			},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "image.read",
		Input:    agent.MarshalToolInput(map[string]string{"materialID": "mattermost:file-1"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected image.read success, got %s", result.ContentText())
	}
	if strings.Contains(httpClient.requestBody, "materialID") {
		t.Fatalf("expected materialID to be resolved before capability call, got %s", httpClient.requestBody)
	}
	if !strings.Contains(httpClient.requestBody, `/workspace/circles/staff/inbox/mattermost/thread-1/post-1/mascot.png`) {
		t.Fatalf("expected capability request to use material path, got %s", httpClient.requestBody)
	}
}

func TestDocumentReadRejectsImageMaterialID(t *testing.T) {
	toolCatalogBuilder := newFileToolTestCatalogBuilder(t.TempDir())
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:           "document.read",
		PolicyResource: "tool:document.read",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"materialID":{"type":"string"},"path":{"type":"string"}}}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agent.VisibleContextMaterial{
				MaterialID:  "mattermost:file-1",
				Filename:    "mascot.png",
				ContentType: "image/png",
				Path:        "/workspace/circles/staff/mascot.png",
			},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "document.read",
		Input:    agent.MarshalToolInput(map[string]string{"materialID": "mattermost:file-1"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "use image.read") {
		t.Fatalf("expected document.read material type error, got %s", result.ContentText())
	}
}

func TestCapabilityToolIdempotencyKeyOnlyForSendTools(t *testing.T) {
	ctx := agent.WithObservationID(agent.WithTaskRunID(context.Background(), "run-1"), "obs-3")
	sendKey := capabilityToolIdempotencyKey(ctx, "message.send")
	if sendKey == "" {
		t.Fatal("expected idempotency key for send tool")
	}
	if again := capabilityToolIdempotencyKey(ctx, "message.send"); again != sendKey {
		t.Fatalf("idempotency key not deterministic: %q vs %q", sendKey, again)
	}
	differentObservation := agent.WithObservationID(agent.WithTaskRunID(context.Background(), "run-1"), "obs-4")
	if other := capabilityToolIdempotencyKey(differentObservation, "message.send"); other == sendKey {
		t.Fatal("expected different observation to produce different key")
	}
	if nonSend := capabilityToolIdempotencyKey(ctx, "web.search"); nonSend != "" {
		t.Fatalf("expected no key for non-send tool, got %q", nonSend)
	}
	missing := agent.WithTaskRunID(context.Background(), "run-1")
	if noObservation := capabilityToolIdempotencyKey(missing, "message.send"); noObservation != "" {
		t.Fatalf("expected no key without observation id, got %q", noObservation)
	}
}

func TestCapabilityCatalogParametersListsRequiredAndOptional(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"status":{"type":"string"},"startDate":{"type":"string"}},"required":["prompt"]}`)
	got := capabilityCatalogParameters(schema)
	want := "{ prompt string (required), startDate string, status string }"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if capabilityCatalogParameters(nil) != "" {
		t.Fatal("nil schema should yield no parameters")
	}
}
