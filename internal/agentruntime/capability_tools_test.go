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
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
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

func TestToolCatalogKeepsCapabilityInputSchemaAuthoritative(t *testing.T) {
	taskAddSchema := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"endDate":{"type":"string"}},"required":["title"]}`)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:        "task.add",
		InputSchema: taskAddSchema,
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"task.add"})

	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	actionSchema := toolSet.ActionSchema(false, nil, false)

	if !strings.Contains(actionSchema, `"title"`) || !strings.Contains(actionSchema, `"endDate"`) {
		t.Fatalf("expected registered task.add schema, got %s", actionSchema)
	}
	if strings.Contains(actionSchema, `"prompt"`) {
		t.Fatalf("expected no inferred legacy task.add fields, got %s", actionSchema)
	}
}

func TestPlatformDMSendAvailabilityDependsOnTrustedContext(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
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
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{
		Name:          "message.send",
		CanonicalName: "message.send",
		PrivacyClass:  "platform_message",
		Idempotency:   CapabilityIdempotency{Supported: true, Scope: "operation"},
	})
	requestDocument := capabilityToolRequest(context.Background(), descriptor, ToolCatalogRequest{
		TaskSource:              TaskLaunchSourceScheduled,
		IsScheduledRun:          true,
		IsApprovalContinuation:  true,
		RequesterPersonID:       "person-1",
		RequesterPlatformUserID: "mattermost-user-1",
		ConversationID:          "conversation-1",
		ConversationChannelID:   "channel-1",
		ReplyTargetID:           "reply-target-1",
		Platform:                "mattermost",
	}, json.RawMessage(`{"targetType":"directMessage","personHint":"샘플","message":"테스트"}`))
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
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
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
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
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

func TestImageGenerateRequiresRequesterWorkspaceWriteAccess(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		circles        []string
		isAllowed      bool
		expectedResult string
	}{
		{
			name:           "other person workspace",
			path:           "/workspace/private/people/person-2/generated.png",
			circles:        []string{"staff"},
			expectedResult: "current account cannot write this file",
		},
		{
			name:           "unauthorized circle workspace",
			path:           "/workspace/circles/finance/generated.png",
			circles:        []string{"staff"},
			expectedResult: "current account cannot write this file",
		},
		{
			name:      "requester workspace",
			path:      "/workspace/private/people/person-1/generated.png",
			circles:   []string{"staff"},
			isAllowed: true,
		},
		{
			name:      "authorized circle workspace",
			path:      "/workspace/circles/finance/generated.png",
			circles:   []string{"staff", "finance"},
			isAllowed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspacePath := t.TempDir()
			httpClient := &recordingHTTPClient{responseBody: `{"content":"generated","status":"ok","result":{"status":"ok"}}`}
			toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
			toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
				Name:           "image.generate",
				PolicyResource: "tool:image.generate",
				InputSchema:    json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"path":{"type":"string"}},"required":["prompt","path"],"additionalProperties":false}`),
			}})
			toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
				"default": {"image.generate"},
			}, nil)
			toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
				ProfileName:       "default",
				RequesterPersonID: "person-1",
				PersonAccess: policy.PersonAccess{
					PersonID: "person-1",
					Circles:  test.circles,
				},
			})

			result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
				ToolName: "image.generate",
				Input: agent.MarshalToolInput(map[string]string{
					"prompt": "a generated test image",
					"path":   test.path,
				}),
			})
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if test.isAllowed {
				if result.Failed() {
					t.Fatalf("expected image.generate success, got %s", result.ContentText())
				}
				if httpClient.requestPath != "/v1/tools/image.generate/invoke" || !strings.Contains(httpClient.requestBody, test.path) {
					t.Fatalf("expected authorized image.generate bridge request, got path=%s body=%s", httpClient.requestPath, httpClient.requestBody)
				}
				return
			}
			if !result.Failed() || result.Failure == nil || result.Failure.Stage != "file_write_access" || !strings.Contains(result.ContentText(), test.expectedResult) {
				t.Fatalf("expected image.generate denial, got %s", result.ContentText())
			}
			if httpClient.requestPath != "" {
				t.Fatalf("expected denied image.generate not to reach capabilityd, got %s", httpClient.requestPath)
			}
		})
	}
}

func TestCapabilityToolIdempotencyKeyOnlyForSendTools(t *testing.T) {
	ctx := agent.WithObservationID(agent.WithTaskRunID(context.Background(), "run-1"), "obs-3")
	sendDescriptor := CapabilityToolDescriptor{CanonicalName: "message.send", Idempotency: CapabilityIdempotency{Supported: true}}
	readDescriptor := CapabilityToolDescriptor{CanonicalName: "web.search"}
	sendKey := capabilityToolIdempotencyKey(ctx, sendDescriptor)
	if sendKey == "" {
		t.Fatal("expected idempotency key for send tool")
	}
	if again := capabilityToolIdempotencyKey(ctx, sendDescriptor); again != sendKey {
		t.Fatalf("idempotency key not deterministic: %q vs %q", sendKey, again)
	}
	differentObservation := agent.WithObservationID(agent.WithTaskRunID(context.Background(), "run-1"), "obs-4")
	if other := capabilityToolIdempotencyKey(differentObservation, sendDescriptor); other == sendKey {
		t.Fatal("expected different observation to produce different key")
	}
	if nonSend := capabilityToolIdempotencyKey(ctx, readDescriptor); nonSend != "" {
		t.Fatalf("expected no key for non-send tool, got %q", nonSend)
	}
	missing := agent.WithTaskRunID(context.Background(), "run-1")
	if noObservation := capabilityToolIdempotencyKey(missing, sendDescriptor); noObservation != "" {
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
