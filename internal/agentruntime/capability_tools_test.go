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

func TestCapabilityToolPreservesValidatedTaskResultEffects(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{
		"provider":"internkim",
		"selectedBackend":"device",
		"toolName":"task.add",
		"outcome":"succeeded",
		"status":"ok",
		"result":{"taskID":"task-1"},
		"effects":[{"objectType":"task","effect":"created","id":"task-1"}]
	}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name: "task.add",
		ResultContract: &CapabilityToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`),
			Effects: []CapabilityResourceEffectContract{{
				ObjectType:     "task",
				Effect:         "created",
				ResultField:    "taskID",
				EffectIdentity: "id",
			}},
		},
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"task.add"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "task.add",
		Input:    json.RawMessage(`{}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() || len(result.Effects) != 1 || result.Effects[0].ID != "task-1" {
		t.Fatalf("expected validated task effect, got %+v", result)
	}
}

func TestCapabilityToolRejectsMismatchedTaskResultIdentity(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{
		"provider":"internkim",
		"selectedBackend":"device",
		"toolName":"task.update",
		"outcome":"succeeded",
		"status":"ok",
		"result":{"taskID":"task-1"},
		"effects":[{"objectType":"task","effect":"created","id":"task-1"}]
	}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "task.add",
		ResultContract: &CapabilityToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`)},
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"task.add"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{ToolName: "task.add", Input: json.RawMessage(`{}`)})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "capability_result_identity" {
		t.Fatalf("expected identity failure, got %+v", result)
	}
}

func TestCapabilityToolRejectsMismatchedIdentityWithoutResultContract(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{
		"provider":"internkim",
		"selectedBackend":"device",
		"toolName":"task.update",
		"outcome":"succeeded",
		"status":"ok",
		"result":{}
	}`}
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "task.add"})
	descriptor.ResultContract = nil
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.capabilityClient = capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}

	result, errorValue := toolCatalogBuilder.invokeCapabilityOperation(
		context.Background(),
		"task.add",
		descriptor,
		ToolCatalogRequest{},
		json.RawMessage(`{}`),
	)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "capability_result_identity" {
		t.Fatalf("expected identity failure without a result contract, got %+v", result)
	}
}

func TestContractedCapabilityPreservesApprovalDenial(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{
		"provider":"internkim",
		"selectedBackend":"device",
		"toolName":"task.delete",
		"outcome":"denied",
		"status":"denied",
		"isError":true,
		"errorCode":"approval_required",
		"failureStage":"authorization",
		"result":{"errorCode":"approval_required"}
	}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name: "task.delete",
		ResultContract: &CapabilityToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"},"deleted":{"const":true}},"required":["taskID","deleted"],"additionalProperties":false}`),
			Effects: []CapabilityResourceEffectContract{{
				ObjectType:     "task",
				Effect:         "deleted",
				ResultField:    "taskID",
				EffectIdentity: "id",
			}},
		},
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"task.delete"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{ToolName: "task.delete", Input: json.RawMessage(`{}`)})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "authorization" || result.Failure == nil || !result.Failure.RequiresApproval {
		t.Fatalf("expected approval denial, got %+v", result)
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
	toolContext := agent.WithToolConflictResolution(context.Background(), agent.ToolConflictResolutionAllowDuplicate)
	requestDocument := capabilityToolRequest(toolContext, descriptor, ToolCatalogRequest{
		TaskSource:              TaskLaunchSourceScheduled,
		IsScheduledRun:          true,
		IsApprovalContinuation:  true,
		RequesterPersonID:       "person-1",
		RequesterPlatformUserID: "mattermost-user-1",
		ConversationID:          "conversation-1",
		ConversationChannelID:   "channel-1",
		ReplyTargetID:           "reply-target-1",
		Platform:                "mattermost",
	}, preparedCapabilityToolPayload{Input: json.RawMessage(`{"targetType":"directMessage","personHint":"샘플","message":"테스트"}`)})
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
	if contextDocument["conflictResolution"] != agent.ToolConflictResolutionAllowDuplicate {
		t.Fatalf("expected typed conflict resolution in context, got %+v", contextDocument)
	}
}

func TestCapabilityToolRequestSeparatesModelInputFromTransport(t *testing.T) {
	input := json.RawMessage(`{"siteID":"site-1"}`)
	transport := map[string]any{"siteSourceBundle": map[string]any{"workspacePath": "/workspace/site"}}
	requestDocument := capabilityToolRequest(
		context.Background(),
		completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "site.publish", CanonicalName: "site.publish"}),
		ToolCatalogRequest{},
		preparedCapabilityToolPayload{Input: input, Transport: transport},
	)

	if string(requestDocument["input"].(json.RawMessage)) != string(input) {
		t.Fatalf("expected unchanged model input, got %+v", requestDocument["input"])
	}
	if requestDocument["transport"].(map[string]any)["siteSourceBundle"] == nil {
		t.Fatalf("expected trusted transport payload, got %+v", requestDocument)
	}
}

func TestImageReadUsesExactPathInput(t *testing.T) {
	workspacePath := t.TempDir()
	imagePath := filepath.Join(workspacePath, "circles", "staff", "inbox", "mattermost", "thread-1", "post-1", "mascot.png")
	writeTestFile(t, imagePath, "image")
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"image.read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/circles/staff/inbox/mattermost/thread-1/post-1/mascot.png","attachments":[{"devicePath":"/workspace/circles/staff/inbox/mattermost/thread-1/post-1/mascot.png","filename":"mascot.png","contentType":"image/png","sizeBytes":5,"contentBase64":"aW1hZ2U="}]}}`}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{canonicalReadDescriptor("image.read")})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "image.read",
		Input:    agent.MarshalToolInput(map[string]string{"path": "/workspace/circles/staff/inbox/mattermost/thread-1/post-1/mascot.png"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected image.read success, got %s", result.ContentText())
	}
	if !strings.Contains(httpClient.requestBody, `/workspace/circles/staff/inbox/mattermost/thread-1/post-1/mascot.png`) {
		t.Fatalf("expected capability request to use exact path, got %s", httpClient.requestBody)
	}
}

func TestCanonicalReadRejectsMaterialIDInput(t *testing.T) {
	toolCatalogBuilder := newFileToolTestCatalogBuilder(t.TempDir())
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{
		canonicalReadDescriptor("document.read"),
		canonicalReadDescriptor("image.read"),
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	for _, toolName := range []string{"document.read", "image.read"} {
		t.Run(toolName, func(t *testing.T) {
			result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
				ToolName: toolName,
				Input:    agent.MarshalToolInput(map[string]string{"materialID": "mattermost:file-1"}),
			})
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !result.Failed() || result.FailureStage() != "tool_input_schema" {
				t.Fatalf("expected %s materialID rejection, got %+v", toolName, result)
			}
		})
	}
}

func TestCanonicalReadDescriptorsExposePathOnlyInputAndResultContract(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{
		canonicalReadDescriptor("document.read"),
		canonicalReadDescriptor("image.read"),
	})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"document.read", "image.read"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	actionSchema := toolSet.ActionSchema(false, nil, false)
	if strings.Contains(actionSchema, "materialID") || !strings.Contains(actionSchema, "path") {
		t.Fatalf("expected model action schema to expose exact path-only input, got %s", actionSchema)
	}

	for _, toolName := range []string{"document.read", "image.read"} {
		t.Run(toolName, func(t *testing.T) {
			descriptor, isFound := toolSet.ToolDefinition(toolName)
			if !isFound {
				t.Fatal("expected canonical read descriptor")
			}
			var inputSchema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			if errorValue := json.Unmarshal(descriptor.InputSchema, &inputSchema); errorValue != nil {
				t.Fatal(errorValue)
			}
			if _, isMaterialID := inputSchema.Properties["materialID"]; isMaterialID {
				t.Fatal("canonical read input must not expose materialID")
			}
			if len(inputSchema.Required) != 1 || inputSchema.Required[0] != "path" {
				t.Fatalf("expected path-only required input, got %+v", inputSchema.Required)
			}
			if descriptor.ResultContract == nil || len(descriptor.ResultContract.Effects) != 0 {
				t.Fatalf("expected canonical read result contract without effects, got %+v", descriptor.ResultContract)
			}
		})
	}
}

func TestCanonicalReadRejectsIdentityAndResultSchemaDrift(t *testing.T) {
	tests := []struct {
		name         string
		responseBody string
		failureStage string
	}{
		{
			name:         "missing provider",
			responseBody: `{"provider":"","selectedBackend":"device","toolName":"document.read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[],"truncated":false}}`,
			failureStage: "capability_result_identity",
		},
		{
			name:         "missing backend",
			responseBody: `{"provider":"internkim","selectedBackend":"","toolName":"document.read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[],"truncated":false}}`,
			failureStage: "capability_result_identity",
		},
		{
			name:         "wrong tool",
			responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"image.read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[],"truncated":false}}`,
			failureStage: "capability_result_identity",
		},
		{
			name:         "wrong outcome",
			responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"document.read","outcome":"failed","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[],"truncated":false}}`,
			failureStage: "capability_result_identity",
		},
		{
			name:         "missing result field",
			responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"document.read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[]}}`,
			failureStage: "tool_result_contract",
		},
		{
			name:         "missing result",
			responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"document.read","outcome":"succeeded","status":"ok"}`,
			failureStage: "tool_result_contract",
		},
		{
			name:         "generic scalar result",
			responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"document.read","outcome":"succeeded","status":"ok","result":"report"}`,
			failureStage: "tool_result_contract",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			httpClient := &recordingHTTPClient{responseBody: testCase.responseBody}
			toolCatalogBuilder := NewToolCatalogBuilder()
			toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{canonicalReadDescriptor("document.read")})
			toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"document.read"})
			toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

			result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{ToolName: "document.read", Input: json.RawMessage(`{"path":"/workspace/report.md"}`)})

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !result.Failed() || result.FailureStage() != testCase.failureStage {
				t.Fatalf("expected %s, got %+v", testCase.failureStage, result)
			}
		})
	}
}

func TestCanonicalReadRejectsEffects(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"document.read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[],"truncated":false},"effects":[{"objectType":"file","effect":"read","path":"/workspace/report.md"}]}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{canonicalReadDescriptor("document.read")})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"document.read"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{ToolName: "document.read", Input: json.RawMessage(`{"path":"/workspace/report.md"}`)})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "tool_result_contract" {
		t.Fatalf("expected read effects rejection, got %+v", result)
	}
}

func TestCanonicalWebSearchAcceptsNormalizedResultContract(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"openrouter","selectedBackend":"remote","toolName":"web.search","outcome":"succeeded","status":"ok","result":{"provider":"openrouter","remoteLLMInvolved":true,"compatibility":"openrouter_server_tool_auto","query":"internkim","answer":"result","results":[{"title":"InternKim","url":"https://internkim.example","snippet":"An agent platform"}]}}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{canonicalWebSearchDescriptor()})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"web.search"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{ToolName: "web.search", Input: json.RawMessage(`{"query":"internkim"}`)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected normalized web search result, got %+v", result)
	}
}

func TestCanonicalWebSearchRejectsReadEffects(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"openrouter","selectedBackend":"remote","toolName":"web.search","outcome":"succeeded","status":"ok","result":{"provider":"openrouter","remoteLLMInvolved":true,"compatibility":"openrouter_server_tool_auto","query":"internkim","answer":"result","results":[]},"effects":[{"objectType":"web","effect":"read","url":"https://internkim.example"}]}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{canonicalWebSearchDescriptor()})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"web.search"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{ToolName: "web.search", Input: json.RawMessage(`{"query":"internkim"}`)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "tool_result_contract" {
		t.Fatalf("expected web search effects rejection, got %+v", result)
	}
}

func canonicalReadDescriptor(toolName string) CapabilityToolDescriptor {
	resultSchema := `{"type":"object","additionalProperties":false,"properties":{"status":{"const":"ok","type":"string"},"path":{"minLength":1,"type":"string"},"format":{"const":"markdown","type":"string"},"content":{"type":"string"},"warnings":{"type":"array","items":{"type":"string"}},"truncated":{"type":"boolean"}},"required":["status","path","format","content","warnings","truncated"]}`
	inputSchema := `{"type":"object","additionalProperties":false,"properties":{"path":{"minLength":1,"type":"string"}},"required":["path"]}`
	if toolName == "image.read" {
		resultSchema = `{"type":"object","additionalProperties":false,"properties":{"status":{"const":"ok","type":"string"},"path":{"minLength":1,"type":"string"},"attachments":{"type":"array","minItems":1,"items":{"type":"object","additionalProperties":false,"properties":{"devicePath":{"minLength":1,"type":"string"},"filename":{"minLength":1,"type":"string"},"contentType":{"minLength":1,"type":"string"},"sizeBytes":{"type":"integer","minimum":0},"contentBase64":{"minLength":1,"type":"string"}},"required":["devicePath","filename","contentType","sizeBytes","contentBase64"]}}},"required":["status","path","attachments"]}`
	}
	return CapabilityToolDescriptor{
		Name:            toolName,
		CanonicalName:   toolName,
		Namespace:       strings.SplitN(toolName, ".", 2)[0],
		ModelName:       toolName,
		ModelVisibility: agent.ToolVisibilityModel,
		Description:     "Canonical read test descriptor.",
		PrivacyClass:    "workspace_document",
		InputSchema:     json.RawMessage(inputSchema),
		OutputSchema:    json.RawMessage(`{"type":"object","additionalProperties":false}`),
		ResultContract:  &CapabilityToolResultContract{Schema: json.RawMessage(resultSchema)},
		PolicyResource:  "tool:" + toolName,
		SideEffectClass: "read",
		Availability:    CapabilityAvailability{State: "ok"},
		Idempotency:     CapabilityIdempotency{Scope: "operation"},
	}
}

func canonicalWebSearchDescriptor() CapabilityToolDescriptor {
	inputSchema := `{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string"},"location":{"type":"string"},"language":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":10},"allowedDomains":{"type":"array","items":{"type":"string"}},"excludedDomains":{"type":"array","items":{"type":"string"}}},"required":["query"]}`
	resultSchema := `{"type":"object","additionalProperties":false,"properties":{"provider":{"type":"string"},"remoteLLMInvolved":{"type":"boolean"},"compatibility":{"type":"string"},"query":{"type":"string"},"answer":{"type":"string"},"results":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"title":{"type":"string"},"url":{"type":"string"},"snippet":{"type":"string"},"source":{"type":"string"}},"required":["title","url","snippet"]}}},"required":["provider","remoteLLMInvolved","compatibility","query","answer","results"]}`
	return CapabilityToolDescriptor{
		Name:            "web.search",
		CanonicalName:   "web.search",
		Namespace:       "web",
		ModelName:       "web.search",
		ModelVisibility: agent.ToolVisibilityModel,
		Description:     "Canonical web search test descriptor.",
		PrivacyClass:    "public_web",
		InputSchema:     json.RawMessage(inputSchema),
		OutputSchema:    json.RawMessage(`{"type":"object","additionalProperties":false}`),
		ResultContract:  &CapabilityToolResultContract{Schema: json.RawMessage(resultSchema)},
		PolicyResource:  "tool:web.search",
		SideEffectClass: "read",
		Availability:    CapabilityAvailability{State: "ok"},
		Idempotency:     CapabilityIdempotency{Scope: "operation"},
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
			httpClient := &recordingHTTPClient{responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"image.generate","outcome":"succeeded","content":"generated","status":"ok","result":{}}`}
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
