package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/acpagent"
	"github.com/Dawn-kim-official/blueclaw/internal/agentruntime"
	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/blueclaw/internal/security"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
	"github.com/Dawn-kim-official/blueclaw/internal/turnstream"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

const requesterPersonID = "bluecollar"

type serveOptions struct {
	ModelName      string
	LLMDSocketPath string
	LLMDEndpoint   string
	LLMDAuthKey    string
}

func main() {
	options, errorValue := parseServeOptions()
	if errorValue != nil {
		fmt.Fprintln(os.Stderr, errorValue)
		os.Exit(2)
	}
	if errorValue := serveOverStandardIO(context.Background(), options); errorValue != nil {
		fmt.Fprintln(os.Stderr, errorValue)
		os.Exit(1)
	}
}

func parseServeOptions() (serveOptions, error) {
	modelName := flag.String("model", "", "model name to pin, for example openai/gpt-5.6-luna")
	socketPath := flag.String("llm-unix-socket", os.Getenv("BLUECLAW_LLMD_SOCKET_PATH"), "llmd unix socket path")
	endpoint := flag.String("llm-endpoint", os.Getenv("BLUECLAW_LLMD_ENDPOINT"), "llmd http endpoint, when not using a socket")
	authKeyPath := flag.String("llm-auth-key-path", os.Getenv("BLUECLAW_LLMD_AUTH_KEY_PATH"), "file holding the llmd auth key")
	flag.Parse()

	if strings.TrimSpace(*socketPath) == "" && strings.TrimSpace(*endpoint) == "" {
		return serveOptions{}, fmt.Errorf("a model is required: pass --llm-unix-socket or --llm-endpoint")
	}
	authKey := ""
	if trimmedPath := strings.TrimSpace(*authKeyPath); trimmedPath != "" {
		keyBytes, readError := os.ReadFile(trimmedPath)
		if readError != nil {
			return serveOptions{}, fmt.Errorf("read llmd auth key: %w", readError)
		}
		authKey = strings.TrimSpace(string(keyBytes))
	}
	return serveOptions{
		ModelName:      strings.TrimSpace(*modelName),
		LLMDSocketPath: strings.TrimSpace(*socketPath),
		LLMDEndpoint:   strings.TrimSpace(*endpoint),
		LLMDAuthKey:    authKey,
	}, nil
}

func serveOverStandardIO(ctx context.Context, options serveOptions) error {
	languageModel := llm.NewLLMDClient(llm.LLMDClientConfiguration{
		Endpoint:       options.LLMDEndpoint,
		UnixSocketPath: options.LLMDSocketPath,
		AuthKey:        options.LLMDAuthKey,
		ModelName:      modelNameOrDefault(options.ModelName),
	})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	turnRunner := bluecollar.NewAgentTurnRunner(
		taskRunService,
		task.NewTaskStepService(),
		task.NewTaskArtifactService(),
		languageModel,
		bluecollar.TurnOptions{},
	)
	return acpagent.Serve(ctx, acpagent.Options{
		TurnStreamer:      turnstream.New(turnRunner, taskRunService),
		BuildToolSet:      buildToolSet,
		BuildInstruction:  workspaceInstruction,
		RequesterPersonID: requesterPersonID,
		AgentName:         "bluecollar",
		AgentVersion:      "0",
	}, os.Stdin, os.Stdout)
}

func buildToolSet(request acpagent.ToolSetRequest) *toolcontract.ToolSet {
	terminalService := security.NewTerminalSessionService(config.TerminalConfiguration{
		Mode:                  "native",
		WorkspaceRootPath:     request.WorkspaceRootPath,
		TimeoutSecond:         600,
		OutputMaxBytes:        32768,
		SessionMaxCount:       2,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
	})
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(request.WorkspaceRootPath)
	toolCatalogBuilder.UseTerminalService(terminalService)
	toolCatalogBuilder.UseWorkspaceActorFactory(security.NewDirectWorkspaceActorFactory(terminalService))
	return toolCatalogBuilder.BuildToolSet(agentruntime.ToolCatalogRequest{
		Prompt:            request.Prompt,
		RequesterPersonID: request.RequesterPersonID,
	})
}

func modelNameOrDefault(modelName string) string {
	if strings.TrimSpace(modelName) != "" {
		return modelName
	}
	return llm.DefaultModelTierNames().Low
}

func workspaceInstruction(workspaceRootPath string) string {
	return strings.Join([]string{
		"You are running as a standalone agent in a single working directory: " + workspaceRootPath + ".",
		"That directory is the entire workspace. Read and write files there with paths relative to it, for example ./notes.txt.",
		"Never create or write into private/, people/, documents/, or any home-like subdirectory, and never use ~ paths.",
		"Run commands from that directory. When the task names an output file, create it at that exact relative path.",
	}, "\n")
}
