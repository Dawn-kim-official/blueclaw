package support

import (
	"context"
	"encoding/json"
	"os"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	FakeMCPServerArgument  = "-blueclaw-fake-mcp-server"
	FakeMCPEchoDescription = "Returns the text it was given."
	FakeMCPEchoSchema      = `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`
)

func FakeMCPCommand() (string, []string) {
	return os.Args[0], []string{FakeMCPServerArgument}
}

func IsFakeMCPServerRequested(arguments []string) bool {
	for _, argument := range arguments {
		if argument == FakeMCPServerArgument {
			return true
		}
	}
	return false
}

func RunFakeMCPServer() {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fake-mcp-server", Version: "1"}, nil)
	server.AddTool(
		&sdkmcp.Tool{
			Name:         "echo",
			Description:  FakeMCPEchoDescription,
			InputSchema:  json.RawMessage(FakeMCPEchoSchema),
			OutputSchema: json.RawMessage(FakeMCPEchoSchema),
		},
		func(_ context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			var arguments struct {
				Text string `json:"text"`
			}
			if errorValue := json.Unmarshal(request.Params.Arguments, &arguments); errorValue != nil {
				return nil, errorValue
			}
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: arguments.Text}}}, nil
		},
	)
	if errorValue := server.Run(context.Background(), &sdkmcp.StdioTransport{}); errorValue != nil {
		os.Exit(1)
	}
}
