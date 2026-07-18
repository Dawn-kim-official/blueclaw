package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"blueclaw/internal/config"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var mcpInputSchema = json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)
var mcpOutputSchema = json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)

func TestMcpRegistryBuildsSchemaAwareToolCatalog(t *testing.T) {
	setStdioServerEnvironment(t)
	mcpRegistry := NewMcpRegistry()
	loadReport := mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{canonicalServerConfiguration(os.Args[0], "echo")})
	defer mcpRegistry.Close()
	if len(loadReport.Quarantined) != 0 {
		t.Fatalf("expected trusted stdio server, got %+v", loadReport.Quarantined)
	}

	toolDefinitions := mcpRegistry.ListTool()
	if len(toolDefinitions) != 1 {
		t.Fatalf("expected one tool definition, got %+v", toolDefinitions)
	}
	toolDefinition := toolDefinitions[0]
	if toolDefinition.Name != "workspace.echo" || toolDefinition.Namespace != "workspace" || toolDefinition.ServerName != "local-tools" {
		t.Fatalf("unexpected tool identity: %+v", toolDefinition)
	}
	if string(toolDefinition.InputSchema) != string(mcpInputSchema) {
		t.Fatalf("expected input schema, got %s", string(toolDefinition.InputSchema))
	}
	if string(toolDefinition.OutputSchema) != string(mcpOutputSchema) ||
		toolDefinition.ResultContract == nil ||
		string(toolDefinition.ResultContract.Schema) != string(mcpOutputSchema) {
		t.Fatalf("expected exact output contract, got %+v", toolDefinition)
	}
	if toolDefinition.ResultContract.EvidenceCondition == nil ||
		string(toolDefinition.ResultContract.EvidenceCondition.Equals) != `"ready"` {
		t.Fatalf("expected evidence condition to survive MCP registration, got %+v", toolDefinition.ResultContract)
	}
	if toolDefinition.Policy.SideEffectClass != "read" {
		t.Fatalf("expected local policy overlay, got %+v", toolDefinition.Policy)
	}

	output, errorValue := mcpRegistry.InvokeTool(context.Background(), Invocation{ServerName: "local-tools", ToolName: "workspace.echo", Input: `{"text":"blueclaw"}`})
	if errorValue != nil {
		t.Fatalf("expected tool call, got %v", errorValue)
	}
	if output != `{"content":[{"type":"text","text":"blueclaw"}],"structuredContent":{"text":"blueclaw"},"isError":false}` {
		t.Fatalf("expected normalized result, got %s", output)
	}
}

func TestMcpRegistryQuarantinesLegacyAndUnreachableServers(t *testing.T) {
	mcpRegistry := NewMcpRegistry()
	loadReport := mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{
		{Name: "legacy", Transport: TransportStdio, Command: "cat"},
		{Name: "schema-less", Transport: TransportStdio, Command: "cat", Tools: []config.MCPToolConfiguration{{Name: "echo", Namespace: "workspace", Policy: &config.MCPToolPolicyMetadata{}}}},
		canonicalServerConfiguration("/path/does/not/exist", "echo"),
	})
	if !reflect.DeepEqual(loadReport.Quarantined, []QuarantinedServer{
		{Name: "legacy", Reason: "invalid configuration"},
		{Name: "schema-less", Reason: "invalid configuration"},
		{Name: "local-tools", Reason: "server unavailable"},
	}) {
		t.Fatalf("expected quarantine report, got %+v", loadReport.Quarantined)
	}
	if len(mcpRegistry.ListTool()) != 0 {
		t.Fatalf("expected quarantined tools to remain hidden, got %+v", mcpRegistry.ListTool())
	}
}

func TestMcpRegistryQuarantinesDuplicateServerNamesWithoutConnecting(t *testing.T) {
	mcpRegistry := NewMcpRegistry()
	loadReport := mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{
		canonicalServerConfiguration(os.Args[0], "echo"),
		canonicalServerConfiguration(os.Args[0], "other"),
	})

	if !reflect.DeepEqual(loadReport.Quarantined, []QuarantinedServer{{
		Name:   "local-tools",
		Reason: "duplicate server name",
	}}) {
		t.Fatalf("expected duplicate server quarantine, got %+v", loadReport.Quarantined)
	}
	if len(mcpRegistry.ListTool()) != 0 {
		t.Fatalf("expected duplicate server tools to remain hidden, got %+v", mcpRegistry.ListTool())
	}
}

func TestMcpRegistryInvalidReloadRemovesTrimmedServerName(t *testing.T) {
	setStdioServerEnvironment(t)
	mcpRegistry := NewMcpRegistry()
	loadReport := mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{canonicalServerConfiguration(os.Args[0], "echo")})
	defer mcpRegistry.Close()
	if len(loadReport.Quarantined) != 0 || len(mcpRegistry.ListTool()) != 1 {
		t.Fatalf("expected initial server registration, got %+v", loadReport)
	}

	invalidConfiguration := canonicalServerConfiguration(os.Args[0], "echo")
	invalidConfiguration.Name = " local-tools "
	invalidConfiguration.Tools = nil
	loadReport = mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{invalidConfiguration})

	if !reflect.DeepEqual(loadReport.Quarantined, []QuarantinedServer{{Name: "local-tools", Reason: "invalid configuration"}}) {
		t.Fatalf("expected invalid reload quarantine, got %+v", loadReport.Quarantined)
	}
	if len(mcpRegistry.ListTool()) != 0 {
		t.Fatalf("expected invalid reload to remove previous tools, got %+v", mcpRegistry.ListTool())
	}
}

func TestMcpServerDefinitionRejectsDuplicateRemoteToolNames(t *testing.T) {
	configuration := canonicalServerConfiguration(os.Args[0], "echo")
	secondTool := configuration.Tools[0]
	secondTool.Namespace = "other"
	configuration.Tools = append(configuration.Tools, secondTool)

	if _, errorValue := buildServerDefinition(configuration); errorValue == nil {
		t.Fatal("expected duplicate remote tool name to fail")
	}
}

func TestMcpServerDefinitionRejectsNonObjectInputSchema(t *testing.T) {
	configuration := canonicalServerConfiguration(os.Args[0], "echo")
	configuration.Tools[0].InputSchema = json.RawMessage(`{"type":"string"}`)

	if _, errorValue := buildServerDefinition(configuration); errorValue == nil {
		t.Fatal("expected non-object input schema to fail")
	}
}

func TestMcpServerDefinitionRequiresExactOutputContract(t *testing.T) {
	testCases := []struct {
		name   string
		update func(*config.MCPToolConfiguration)
	}{
		{
			name: "missing output schema",
			update: func(configuration *config.MCPToolConfiguration) {
				configuration.OutputSchema = nil
			},
		},
		{
			name: "missing result contract",
			update: func(configuration *config.MCPToolConfiguration) {
				configuration.ResultContract = nil
			},
		},
		{
			name: "mismatched result contract",
			update: func(configuration *config.MCPToolConfiguration) {
				configuration.ResultContract.Schema = json.RawMessage(`{"type":"object","properties":{"value":{"type":"number"}},"required":["value"],"additionalProperties":false}`)
			},
		},
		{
			name: "missing idempotency scope",
			update: func(configuration *config.MCPToolConfiguration) {
				configuration.Policy.IdempotencyScope = ""
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			configuration := canonicalServerConfiguration(os.Args[0], "echo")
			testCase.update(&configuration.Tools[0])

			if _, errorValue := buildServerDefinition(configuration); errorValue == nil {
				t.Fatal("expected incomplete output contract rejection")
			}
		})
	}
}

func TestMcpServerDefinitionRejectsIncompleteObservationMetadata(t *testing.T) {
	configuration := canonicalServerConfiguration(os.Args[0], "echo")
	configuration.Tools[0].Policy.CompletionMode = "observation"

	if _, errorValue := buildServerDefinition(configuration); errorValue == nil {
		t.Fatal("expected incomplete observation metadata to fail")
	}
}

func TestMcpServerDefinitionRejectsUnknownCompletionMode(t *testing.T) {
	configuration := canonicalServerConfiguration(os.Args[0], "echo")
	configuration.Tools[0].Policy.CompletionMode = "unknown"

	if _, errorValue := buildServerDefinition(configuration); errorValue == nil {
		t.Fatal("expected unknown completion mode to fail")
	}
}

func TestMCPResultContractAcceptsOnlyCanonicalArrayEffectIdentities(t *testing.T) {
	effect := config.MCPResourceEffectContract{
		ObjectType:     "file",
		Effect:         "updated",
		ResultField:    "paths",
		EffectIdentity: "path",
	}
	validSchema := json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"},"minItems":1,"uniqueItems":true}},"required":["paths"],"additionalProperties":false}`)
	if _, errorValue := buildResourceEffectContract(validSchema, effect); errorValue != nil {
		t.Fatalf("expected canonical string array identity, got %v", errorValue)
	}
	for _, schema := range []json.RawMessage{
		json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"},"uniqueItems":true}},"required":["paths"],"additionalProperties":false}`),
		json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"},"minItems":1}},"required":["paths"],"additionalProperties":false}`),
		json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"number"},"minItems":1,"uniqueItems":true}},"required":["paths"],"additionalProperties":false}`),
	} {
		if _, errorValue := buildResourceEffectContract(schema, effect); errorValue == nil {
			t.Fatalf("expected noncanonical array identity rejection for %s", schema)
		}
	}
}

func TestMcpRegistrySupportsStreamableHTTP(t *testing.T) {
	server := newMCPServer(func(_ context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return textToolResult("http:" + request.Params.Name), nil
	})
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	mcpRegistry := NewMcpRegistry()
	loadReport := mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{canonicalHTTPServerConfiguration(httpServer.URL)})
	defer mcpRegistry.Close()
	if len(loadReport.Quarantined) != 0 {
		t.Fatalf("expected trusted streamable http server, got %+v", loadReport.Quarantined)
	}
	output, errorValue := mcpRegistry.InvokeTool(context.Background(), Invocation{ServerName: "http-tools", ToolName: "workspace.echo", Input: `{"text":"blueclaw"}`})
	if errorValue != nil {
		t.Fatalf("expected streamable http tool call, got %v", errorValue)
	}
	if output != `{"content":[{"type":"text","text":"http:echo"}],"structuredContent":{"text":"http:echo"},"isError":false}` {
		t.Fatalf("expected normalized streamable result, got %s", output)
	}
}

func TestMcpRegistryQuarantinesSchemaMismatch(t *testing.T) {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test-server", Version: "1"}, nil)
	server.AddTool(&sdkmcp.Tool{
		Name:        "echo",
		Description: "Echo",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"number"}},"required":["value"],"additionalProperties":false}`),
	}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return textToolResult("unused"), nil
	})
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	mcpRegistry := NewMcpRegistry()
	loadReport := mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{canonicalHTTPServerConfiguration(httpServer.URL)})
	defer mcpRegistry.Close()

	if !reflect.DeepEqual(loadReport.Quarantined, []QuarantinedServer{{Name: "http-tools", Reason: "server unavailable"}}) {
		t.Fatalf("expected schema mismatch quarantine, got %+v", loadReport.Quarantined)
	}
	if len(mcpRegistry.ListTool()) != 0 {
		t.Fatalf("expected mismatched tools to remain hidden, got %+v", mcpRegistry.ListTool())
	}
}

func TestMcpRegistryQuarantinesOutputSchemaMismatch(t *testing.T) {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test-server", Version: "1"}, nil)
	server.AddTool(&sdkmcp.Tool{
		Name:         "echo",
		Description:  "Echo",
		InputSchema:  mcpInputSchema,
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"number"}},"required":["value"],"additionalProperties":false}`),
	}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return textToolResult("unused"), nil
	})
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	mcpRegistry := NewMcpRegistry()
	loadReport := mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{canonicalHTTPServerConfiguration(httpServer.URL)})
	defer mcpRegistry.Close()

	if !reflect.DeepEqual(loadReport.Quarantined, []QuarantinedServer{{Name: "http-tools", Reason: "server unavailable"}}) {
		t.Fatalf("expected output schema mismatch quarantine, got %+v", loadReport.Quarantined)
	}
}

func TestMcpRegistryPropagatesCallCancellation(t *testing.T) {
	server := newMCPServer(func(ctx context.Context, _ *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	mcpRegistry := NewMcpRegistry()
	loadReport := mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{canonicalHTTPServerConfiguration(httpServer.URL)})
	defer mcpRegistry.Close()
	if len(loadReport.Quarantined) != 0 {
		t.Fatalf("expected trusted streamable http server, got %+v", loadReport.Quarantined)
	}
	callContext, cancelCall := context.WithCancel(context.Background())
	cancelCall()
	_, errorValue := mcpRegistry.InvokeTool(callContext, Invocation{ServerName: "http-tools", ToolName: "workspace.echo", Input: `{}`})
	if errorValue == nil {
		t.Fatal("expected cancelled tool call")
	}
}

func TestMCPStdioServerProcess(t *testing.T) {
	if os.Getenv("BLUECLAW_MCP_STDIO_SERVER") != "1" {
		return
	}
	server := newMCPServer(func(_ context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		arguments := map[string]string{}
		if errorValue := json.Unmarshal(request.Params.Arguments, &arguments); errorValue != nil {
			return nil, errorValue
		}
		return textToolResult(arguments["text"]), nil
	})
	if errorValue := server.Run(context.Background(), &sdkmcp.StdioTransport{}); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func canonicalServerConfiguration(command string, toolName string) config.MCPServerConfiguration {
	return config.MCPServerConfiguration{
		Name:      "local-tools",
		Transport: TransportStdio,
		Command:   command,
		Arguments: []string{"-test.run=TestMCPStdioServerProcess", "--"},
		Tools: []config.MCPToolConfiguration{{
			Name:         toolName,
			Namespace:    "workspace",
			Description:  "Echo text",
			InputSchema:  mcpInputSchema,
			OutputSchema: mcpOutputSchema,
			ResultContract: &config.MCPToolResultContract{
				Schema: mcpOutputSchema,
				EvidenceCondition: &config.EvidenceCondition{
					ResultField: "text",
					Equals:      json.RawMessage(`"ready"`),
				},
			},
			Policy: &config.MCPToolPolicyMetadata{
				PrivacyClass:     "workspace",
				WorksOffline:     true,
				ModelVisibility:  "visible",
				PolicyResource:   "tool:workspace.echo",
				SideEffectClass:  "read",
				CompletionMode:   "none",
				Idempotency:      "supported",
				IdempotencyScope: "operation",
			},
		}},
	}
}

func canonicalHTTPServerConfiguration(endpoint string) config.MCPServerConfiguration {
	configuration := canonicalServerConfiguration("", "echo")
	configuration.Name = "http-tools"
	configuration.Transport = TransportStreamableHTTP
	configuration.Endpoint = endpoint
	configuration.Command = ""
	configuration.Arguments = nil
	return configuration
}

func newMCPServer(handler sdkmcp.ToolHandler) *sdkmcp.Server {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test-server", Version: "1"}, nil)
	server.AddTool(&sdkmcp.Tool{Name: "echo", Description: "Echo", InputSchema: mcpInputSchema, OutputSchema: mcpOutputSchema}, handler)
	return server
}

func textToolResult(text string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content:           []sdkmcp.Content{&sdkmcp.TextContent{Text: text}},
		StructuredContent: map[string]any{"text": text},
	}
}

func setStdioServerEnvironment(t *testing.T) {
	t.Helper()
	if errorValue := os.Setenv("BLUECLAW_MCP_STDIO_SERVER", "1"); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = os.Unsetenv("BLUECLAW_MCP_STDIO_SERVER") })
}
