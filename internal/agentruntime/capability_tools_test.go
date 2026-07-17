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

func TestToolCatalogKeepsCapabilityInputSchemaAuthoritative(t *testing.T) {
	taskAddSchema := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"endDate":{"type":"string"}},"required":["title"]}`)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
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

func TestCapabilityInvokeSchemaListsOperationEnumAndRequiresInput(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local"}, []CapabilityToolDescriptor{{
		Name:        "calendar.add",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
	}, {
		Name:        "task.add",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}},"required":["prompt"]}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolDefinition, isFound := toolRegistry.ToolDefinition(agent.CapabilityInvokeToolName)
	if !isFound {
		t.Fatal("expected capability.invoke tool definition")
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if errorValue := json.Unmarshal(toolDefinition.InputSchema, &schema); errorValue != nil {
		t.Fatalf("expected capability.invoke schema: %v", errorValue)
	}
	if !containsTestString(schema.Properties["operation"].Enum, "calendar.add") || !containsTestString(schema.Properties["operation"].Enum, "task.add") {
		t.Fatalf("expected capability operations in enum, got %+v", schema.Properties["operation"].Enum)
	}
	if !containsTestString(schema.Required, "operation") || !containsTestString(schema.Required, "input") {
		t.Fatalf("expected operation and input to be required, got %+v", schema.Required)
	}
}

func TestCapabilityInvokeMissingRequiredFieldsIncludesDescriptorRecoveryHint(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"content":"unexpected","status":"ok"}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:        "calendar.add",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"startISO":{"type":"string"},"endISO":{"type":"string"},"timeZone":{"type":"string"}},"required":["title","startISO","endISO"]}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeRegistered(context.Background(), agent.ToolInvocation{
		ToolName: agent.CapabilityInvokeToolName,
		Input:    json.RawMessage(`{"operation":"calendar.add","input":{}}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected missing input failure, got %s", result.ContentText())
	}
	if httpClient.requestPath != "" {
		t.Fatalf("missing input must fail before capabilityd call, got path %s", httpClient.requestPath)
	}
	if result.Failure == nil || len(result.Failure.RecoveryHints) != 1 {
		t.Fatalf("expected recovery hint, got %+v", result.Failure)
	}
	recoveryHint := result.Failure.RecoveryHints[0]
	for _, expected := range []string{"calendar.add", "title string", "startISO string", "endISO string"} {
		if !strings.Contains(recoveryHint.Action+" "+recoveryHint.Reason, expected) {
			t.Fatalf("expected recovery hint to contain %q, got %+v", expected, recoveryHint)
		}
	}
	if !containsTestString(recoveryHint.ToolNames, "calendar.add") || containsTestString(recoveryHint.ToolNames, agent.CapabilityInvokeToolName) {
		t.Fatalf("expected direct calendar.add recovery tool, got %+v", recoveryHint.ToolNames)
	}
}

func TestCapabilityInvokeRejectsUnexpectedFieldsBeforeCapabilityCall(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"content":"unexpected","status":"ok"}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:        "calendar.delete",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"eventID":{"type":"string"},"query":{"type":"string"}},"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeRegistered(context.Background(), agent.ToolInvocation{
		ToolName: agent.CapabilityInvokeToolName,
		Input:    json.RawMessage(`{"operation":"calendar.delete","input":"{\"path\":\"/calendar/events/비용 테스트 일정\"}"}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "does not accept these input fields: path") {
		t.Fatalf("expected unexpected input failure, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), "eventID, query") {
		t.Fatalf("expected allowed fields in failure, got %s", result.ContentText())
	}
	if httpClient.requestPath != "" {
		t.Fatalf("unexpected input must fail before capabilityd call, got path %s", httpClient.requestPath)
	}
	if result.Failure == nil || !result.Failure.Retryable || result.Failure.RetryPolicy != "different_input" {
		t.Fatalf("expected retryable schema failure, got %+v", result.Failure)
	}
	if len(result.Failure.RecoveryHints) != 1 || containsTestString(result.Failure.RecoveryHints[0].ToolNames, agent.CapabilityInvokeToolName) || !containsTestString(result.Failure.RecoveryHints[0].ToolNames, "calendar.delete") {
		t.Fatalf("expected direct calendar.delete recovery tool, got %+v", result.Failure.RecoveryHints)
	}
}

func TestCapabilityInvokeMissingInputIncludesInputSkeleton(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"content":"unexpected","status":"ok"}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:        "site.create",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string","description":"URL identifier"},"prompt":{"type":"string"}},"required":["slug","prompt"]}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeRegistered(context.Background(), agent.ToolInvocation{
		ToolName: agent.CapabilityInvokeToolName,
		Input:    json.RawMessage(`{"operation":"site.create","input":{}}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected missing input failure, got %s", result.ContentText())
	}
	if httpClient.requestPath != "" {
		t.Fatalf("missing input must fail before capabilityd call, got path %s", httpClient.requestPath)
	}
	if !strings.Contains(result.ContentText(), "inputSkeleton") {
		t.Fatalf("expected failure message to point at inputSkeleton, got %s", result.ContentText())
	}
	var payload struct {
		InputSkeleton map[string]string `json:"inputSkeleton"`
	}
	if errorValue := json.Unmarshal(result.Output.Data, &payload); errorValue != nil {
		t.Fatalf("expected structured inputSkeleton data, got %s: %v", result.Output.Data, errorValue)
	}
	if payload.InputSkeleton["slug"] != "<string: URL identifier>" {
		t.Fatalf("expected described placeholder for slug, got %+v", payload.InputSkeleton)
	}
	if payload.InputSkeleton["prompt"] != "<string>" {
		t.Fatalf("expected typed placeholder for prompt, got %+v", payload.InputSkeleton)
	}
}

func TestCapabilityInvokeRejectsMissingInputObject(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"content":"unexpected","status":"ok"}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:        "calendar.add",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeRegistered(context.Background(), agent.ToolInvocation{
		ToolName: agent.CapabilityInvokeToolName,
		Input:    json.RawMessage(`{"operation":"calendar.add"}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "calendar.add requires input to be an object") || strings.Contains(result.ContentText(), "Call capability.invoke") {
		t.Fatalf("expected missing input object failure, got %s", result.ContentText())
	}
	if httpClient.requestPath != "" {
		t.Fatalf("missing input object must fail before capabilityd call, got path %s", httpClient.requestPath)
	}
}

func TestNormalizeCapabilityInvokeInputUnwrapsStringifiedObject(t *testing.T) {
	objectInput := json.RawMessage(`{"title":"x"}`)
	if got := normalizeCapabilityInvokeInput(objectInput); string(got) != string(objectInput) {
		t.Fatalf("expected object input unchanged, got %s", got)
	}
	stringifiedObjectInput := json.RawMessage(`"{\"title\":\"x\"}"`)
	if got := normalizeCapabilityInvokeInput(stringifiedObjectInput); string(got) != `{"title":"x"}` {
		t.Fatalf("expected stringified object unwrapped, got %s", got)
	}
	plainStringInput := json.RawMessage(`"not an object"`)
	if got := normalizeCapabilityInvokeInput(plainStringInput); string(got) != string(plainStringInput) {
		t.Fatalf("expected plain string unchanged, got %s", got)
	}
	numberInput := json.RawMessage(`42`)
	if got := normalizeCapabilityInvokeInput(numberInput); string(got) != string(numberInput) {
		t.Fatalf("expected number input unchanged, got %s", got)
	}
}

func TestCapabilityInvokeAcceptsStringifiedJSONObjectInput(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"content":"created","status":"ok"}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:        "calendar.add",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeRegistered(context.Background(), agent.ToolInvocation{
		ToolName: agent.CapabilityInvokeToolName,
		Input:    json.RawMessage(`{"operation":"calendar.add","input":"{\"title\":\"party\"}"}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected stringified object input to succeed, got %s", result.ContentText())
	}
	if httpClient.requestPath != "/v1/tools/calendar.add/invoke" {
		t.Fatalf("expected capability call to reach calendar.add, got path=%s", httpClient.requestPath)
	}
}

func TestCapabilityInvokeRejectsNonObjectInputWithHardenedFailure(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"content":"unexpected","status":"ok"}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:        "calendar.add",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeRegistered(context.Background(), agent.ToolInvocation{
		ToolName: agent.CapabilityInvokeToolName,
		Input:    json.RawMessage(`{"operation":"calendar.add","input":42}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected non-object input failure, got %s", result.ContentText())
	}
	if httpClient.requestPath != "" {
		t.Fatalf("non-object input must fail before capabilityd call, got path %s", httpClient.requestPath)
	}
	if result.Failure == nil || !result.Failure.Retryable {
		t.Fatalf("expected retryable failure, got %+v", result.Failure)
	}
	if len(result.Failure.RecoveryHints) == 0 {
		t.Fatalf("expected recovery hints, got %+v", result.Failure)
	}
	recoveryHint := result.Failure.RecoveryHints[0]
	if !strings.Contains(recoveryHint.Action+" "+recoveryHint.Reason, "calendar.add") || containsTestString(recoveryHint.ToolNames, agent.CapabilityInvokeToolName) {
		t.Fatalf("expected recovery hint to mention operation, got %+v", recoveryHint)
	}
}

func TestCapabilityInvokeUnknownOperationTakesPriorityOverInputShape(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local"}, []CapabilityToolDescriptor{{
		Name:        "calendar.add",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeRegistered(context.Background(), agent.ToolInvocation{
		ToolName: agent.CapabilityInvokeToolName,
		Input:    json.RawMessage(`{"operation":"calendar.remove","input":42}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "not a valid capability operation") {
		t.Fatalf("expected unknown operation failure, got %s", result.ContentText())
	}
}

func TestCapabilityInvokeSchemaOmitsPolicyDeniedOperation(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local"}, []CapabilityToolDescriptor{{
		Name:           "site.create",
		PolicyResource: "tool:site.create",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
	}, {
		Name:        "task.add",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}},"required":["prompt"]}`),
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

	toolDefinition, isFound := toolRegistry.ToolDefinition(agent.CapabilityInvokeToolName)
	if !isFound {
		t.Fatal("expected capability.invoke tool definition")
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if errorValue := json.Unmarshal(toolDefinition.InputSchema, &schema); errorValue != nil {
		t.Fatalf("expected capability.invoke schema: %v", errorValue)
	}
	if containsTestString(schema.Properties["operation"].Enum, "site.create") {
		t.Fatalf("expected policy-denied operation to be absent from enum, got %+v", schema.Properties["operation"].Enum)
	}
	if !containsTestString(schema.Properties["operation"].Enum, "task.add") {
		t.Fatalf("expected allowed operation present in enum, got %+v", schema.Properties["operation"].Enum)
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

func TestCapabilityInvokeSchemaDeclaresInputAsJSONEncodedString(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local"}, []CapabilityToolDescriptor{{
		Name:        "site.create",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolDefinition, isFound := toolRegistry.ToolDefinition(agent.CapabilityInvokeToolName)
	if !isFound {
		t.Fatal("expected capability.invoke tool definition")
	}
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if errorValue := json.Unmarshal(toolDefinition.InputSchema, &schema); errorValue != nil {
		t.Fatalf("expected capability.invoke schema: %v", errorValue)
	}
	if schema.Properties["input"].Type != "string" {
		t.Fatalf("expected input declared as a JSON-encoded string so strict structured output can fill it, got %q", schema.Properties["input"].Type)
	}
}

func TestCapabilityInvokeWrapperExampleEncodesInputAsString(t *testing.T) {
	example := capabilityInvokeWrapperExample("site.create", json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`), []string{"slug"})
	var document struct {
		Operation string `json:"operation"`
		Input     string `json:"input"`
	}
	if errorValue := json.Unmarshal([]byte(example), &document); errorValue != nil {
		t.Fatalf("expected wrapper example with string input, got %s: %v", example, errorValue)
	}
	if document.Operation != "site.create" {
		t.Fatalf("expected operation site.create, got %q", document.Operation)
	}
	var input map[string]string
	if errorValue := json.Unmarshal([]byte(document.Input), &input); errorValue != nil {
		t.Fatalf("expected input to hold a JSON object, got %q: %v", document.Input, errorValue)
	}
	if input["slug"] == "" {
		t.Fatalf("expected slug placeholder in example input, got %+v", input)
	}
}
