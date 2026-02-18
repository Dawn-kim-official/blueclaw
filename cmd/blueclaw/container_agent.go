package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/blueclaw/blueclaw/internal/agent"
	"github.com/blueclaw/blueclaw/internal/ipc"
	"github.com/blueclaw/blueclaw/internal/memory"
	"github.com/blueclaw/blueclaw/internal/provider"
	"github.com/blueclaw/blueclaw/internal/tool"
)

const (
	containerWorkspaceDirectory = "/workspace"
	containerDataDirectory      = "/data"
	containerMemoryTopK         = 5
)

type ContainerAgentCommand struct{}

func (command *ContainerAgentCommand) Run() error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)
	if !scanner.Scan() {
		return writeContainerAgentError("reading initial request: no data on stdin")
	}
	var agentRequest ipc.AgentRequest
	if err := json.Unmarshal(scanner.Bytes(), &agentRequest); err != nil {
		return writeContainerAgentError(fmt.Sprintf("parsing request: %v", err))
	}
	transport := ipc.NewStdioTransportFromScanner(os.Stdout, scanner)
	llmProvider := ipc.NewStdioProvider(transport)
	registry := buildLocalToolRegistry(transport)
	toolDefinitions := buildToolDefinitions(registry)
	agentLoop := agent.NewLoop(llmProvider, registry)
	providerRequest := provider.Request{
		SystemPrompt:    agentRequest.SystemPrompt,
		Messages:        agentRequest.Messages,
		ToolDefinitions: toolDefinitions,
		Model:           agentRequest.Model,
	}
	response, err := agentLoop.Run(context.Background(), providerRequest)
	if err != nil {
		return writeContainerAgentError(err.Error())
	}
	return transport.WriteOutbound(ipc.StdioOutbound{
		Type:         "done",
		DoneResponse: &response,
	})
}

func buildLocalToolRegistry(transport *ipc.StdioTransport) *tool.Registry {
	tasksDirectory := containerWorkspaceDirectory + "/tasks"
	registry := tool.NewRegistry()
	registry.Register(tool.NewReadFileTool(containerWorkspaceDirectory))
	registry.Register(tool.NewWriteFileTool(containerWorkspaceDirectory))
	registry.Register(tool.NewEditFileTool(containerWorkspaceDirectory))
	registry.Register(tool.NewListDirTool(containerWorkspaceDirectory))
	registry.Register(tool.NewAppendFileTool(containerWorkspaceDirectory))
	registry.Register(tool.NewShellTool())
	registerMemoryTools(registry, transport)
	registry.Register(tool.NewScheduleTool(ipc.NewStdioJobScheduler(transport)))
	registry.Register(tool.NewSetHeartbeatIntervalTool(ipc.NewStdioHeartbeatIntervalSetter(transport)))
	registry.Register(tool.NewUpdateTaskTool(tasksDirectory))
	return registry
}

func registerMemoryTools(registry *tool.Registry, transport *ipc.StdioTransport) {
	embeddingClient := ipc.NewStdioEmbeddingClient(transport)
	graphStore, err := memory.NewGraphStore(containerDataDirectory + "/db/memory.db")
	if err != nil {
		log.Printf("warning: could not open graph store: %v (memory tools unavailable)", err)
		return
	}
	registry.Register(tool.NewRememberTool(graphStore, embeddingClient))
	registry.Register(tool.NewRecallTool(graphStore, embeddingClient, containerMemoryTopK))
	registry.Register(tool.NewConnectTool(graphStore))
}

func buildToolDefinitions(registry *tool.Registry) []provider.ToolDefinition {
	definitions := registry.ListDefinitions()
	toolDefinitions := make([]provider.ToolDefinition, len(definitions))
	for index, definition := range definitions {
		toolDefinitions[index] = provider.ToolDefinition{
			Name:        definition.Name,
			Description: definition.Description,
			Parameters:  definition.Parameters,
		}
	}
	return toolDefinitions
}

func writeContainerAgentError(message string) error {
	outbound := ipc.StdioOutbound{Type: "error", ErrorMessage: message}
	data, _ := json.Marshal(outbound)
	fmt.Fprintln(os.Stdout, string(data))
	return nil
}
