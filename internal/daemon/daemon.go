package daemon

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"

	"os/signal"

	"github.com/blueclaw/blueclaw/internal/configuration"
	"github.com/blueclaw/blueclaw/internal/container"
	"github.com/blueclaw/blueclaw/internal/heartbeat"
	"github.com/blueclaw/blueclaw/internal/initialize"
	"github.com/blueclaw/blueclaw/internal/memory"
	"github.com/blueclaw/blueclaw/internal/outbox"
	"github.com/blueclaw/blueclaw/internal/provider"
	"github.com/blueclaw/blueclaw/internal/scheduler"
	"github.com/blueclaw/blueclaw/internal/tool"
)

const achievementsSubdirectory = "public/achievements"
const tasksSubdirectory = "public/tasks"

type Daemon struct {
	configuration    configuration.Configuration
	llmProvider      provider.LLMProvider
	toolRegistry     *tool.Registry
	agentRunner      *container.AgentRunner
	containerManager *container.Manager
	embeddingClient  *memory.EmbeddingClient
	sidecar          *memory.SidecarProcess
	searchIndex      *memory.SearchIndex
	heartbeatService *heartbeat.Service
	schedulerService *scheduler.Service
	outbox           *outbox.Outbox
	socketPath       string
	tcpAddress       string
}

func Start(config configuration.Configuration) error {
	apiKey := config.ActiveAPIKey()
	if apiKey == "" {
		return fmt.Errorf("no API key configured for provider %q; set it in config.yaml or via environment variable", config.LLMProvider)
	}
	llmProvider, err := provider.NewProvider(config.LLMProvider, apiKey, config.Model, config.ProviderEndpoint, config.Debug)
	if err != nil {
		return fmt.Errorf("creating LLM provider: %w", err)
	}
	blueclawDirectory := configuration.BlueclawDirectory()
	containerRuntime, err := createContainerRuntime(config.ContainerRuntime)
	if err != nil {
		return fmt.Errorf("creating container runtime: %w", err)
	}
	if err := containerRuntime.IsAvailable(context.Background()); err != nil {
		return fmt.Errorf("container runtime not available: %w", err)
	}
	publicDirectory := filepath.Join(blueclawDirectory, "public")
	if err := ensureDirectories(publicDirectory); err != nil {
		return err
	}
	container.CleanOrphanedContainers(context.Background(), containerRuntime)
	achievementsDirectory := filepath.Join(blueclawDirectory, achievementsSubdirectory)
	if cleanErr := tool.CleanExpiredAchievements(achievementsDirectory, config.ParsedAchievementTTL()); cleanErr != nil {
		log.Printf("warning: failed to clean expired achievements: %v", cleanErr)
	}
	tasksDirectory := filepath.Join(blueclawDirectory, tasksSubdirectory)
	if cleanErr := tool.CleanExpiredTasks(tasksDirectory, config.ParsedAchievementTTL()); cleanErr != nil {
		log.Printf("warning: failed to clean expired tasks: %v", cleanErr)
	}
	embeddingClient := memory.NewEmbeddingClient(config.EmbeddingPort)
	databasePath := filepath.Join(blueclawDirectory, "db", "memory.db")
	searchIndex, err := memory.NewSearchIndex(databasePath)
	if err != nil {
		return fmt.Errorf("opening search index: %w", err)
	}
	store := memory.NewStore(blueclawDirectory)
	toolRegistry := tool.NewRegistry()
	toolRegistry.Register(tool.NewRememberTool(store, searchIndex, embeddingClient))
	toolRegistry.Register(tool.NewRecallTool(store, searchIndex, embeddingClient, config.MemoryTopK))
	toolRegistry.Register(tool.NewReadFileTool(publicDirectory))
	toolRegistry.Register(tool.NewWriteFileTool(publicDirectory))
	toolRegistry.Register(tool.NewEditFileTool(publicDirectory))
	toolRegistry.Register(tool.NewListDirTool(publicDirectory))
	toolRegistry.Register(tool.NewAppendFileTool(publicDirectory))
	messageOutbox := outbox.NewOutbox(blueclawDirectory)
	schedulerService, err := scheduler.NewService(blueclawDirectory, nil, messageOutbox)
	if err != nil {
		return fmt.Errorf("creating scheduler: %w", err)
	}
	toolRegistry.Register(tool.NewScheduleTool(schedulerService))
	heartbeatService := heartbeat.NewService(
		config.ParsedHeartbeatInterval(),
		config.ParsedMinHeartbeatInterval(),
		config.ParsedMaxHeartbeatInterval(),
		blueclawDirectory, nil, messageOutbox,
	)
	containerManager := container.NewManager(
		containerRuntime, config.ContainerImage, config.ContainerNetworkMode,
		publicDirectory, blueclawDirectory,
		llmProvider, embeddingClient, schedulerService, heartbeatService, config.Debug,
	)
	agentRunner := container.NewAgentRunner(containerManager)
	schedulerService.SetAgentRunner(agentRunner)
	heartbeatService.SetAgentRunner(agentRunner)
	if err := initialize.EnsureEmbeddingModel(blueclawDirectory, false); err != nil {
		log.Printf("warning: failed to ensure embedding model: %v (memory search will be degraded)", err)
	}
	socketPath := filepath.Join(blueclawDirectory, "daemon.sock")
	modelPath := filepath.Join(blueclawDirectory, "models", "embedding.gguf")
	sidecar := memory.NewSidecarProcess(config.EmbeddingPort, modelPath)
	daemon := &Daemon{
		configuration:    config,
		llmProvider:      llmProvider,
		toolRegistry:     toolRegistry,
		agentRunner:      agentRunner,
		containerManager: containerManager,
		embeddingClient:  embeddingClient,
		sidecar:          sidecar,
		searchIndex:      searchIndex,
		heartbeatService: heartbeatService,
		schedulerService: schedulerService,
		outbox:           messageOutbox,
		socketPath:       socketPath,
		tcpAddress:       fmt.Sprintf(":%d", config.APIPort),
	}
	return daemon.run(blueclawDirectory)
}

func (daemon *Daemon) run(blueclawDirectory string) error {
	if err := removeStaleSocket(daemon.socketPath); err != nil {
		return err
	}
	if err := daemon.sidecar.Start(); err != nil {
		log.Printf("warning: embedding sidecar failed to start: %v (memory search will be degraded)", err)
	} else {
		go daemon.sidecar.HealthCheckLoop(daemon.embeddingClient)
	}
	defer daemon.sidecar.Stop()
	defer daemon.searchIndex.Close()
	daemon.heartbeatService.Start()
	defer daemon.heartbeatService.Stop()
	daemon.schedulerService.Start()
	defer daemon.schedulerService.Stop()
	server := NewServer(daemon.agentRunner, daemon.llmProvider, daemon.toolRegistry, blueclawDirectory, daemon.configuration, daemon.outbox, daemon.schedulerService, daemon.heartbeatService)
	handler := server.Handler()
	unixListener, err := net.Listen("unix", daemon.socketPath)
	if err != nil {
		return fmt.Errorf("listening on Unix socket %s: %w", daemon.socketPath, err)
	}
	defer unixListener.Close()
	if err := os.Chmod(daemon.socketPath, 0600); err != nil {
		return fmt.Errorf("setting socket permissions: %w", err)
	}
	tcpListener, err := net.Listen("tcp", daemon.tcpAddress)
	if err != nil {
		unixListener.Close()
		return fmt.Errorf("listening on TCP %s: %w", daemon.tcpAddress, err)
	}
	defer tcpListener.Close()
	unixServer := &http.Server{Handler: handler}
	tcpServer := &http.Server{Handler: handler}
	shutdownContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errChannel := make(chan error, 2)
	go func() { errChannel <- unixServer.Serve(unixListener) }()
	go func() { errChannel <- tcpServer.Serve(tcpListener) }()
	log.Printf("daemon started: unix=%s tcp=%s provider=%s image=%s", daemon.socketPath, daemon.tcpAddress, daemon.llmProvider.Name(), daemon.configuration.ContainerImage)
	select {
	case <-shutdownContext.Done():
		log.Println("shutting down daemon...")
		unixServer.Shutdown(context.Background())
		tcpServer.Shutdown(context.Background())
		return nil
	case err := <-errChannel:
		return fmt.Errorf("server error: %w", err)
	}
}

func createContainerRuntime(runtimeName string) (container.ContainerRuntime, error) {
	if runtimeName == "" {
		return detectContainerRuntime()
	}
	switch runtimeName {
	case "docker":
		return container.NewDockerRuntime()
	case "apple":
		return container.NewAppleContainerRuntime(), nil
	default:
		return nil, fmt.Errorf("unknown container runtime: %s", runtimeName)
	}
}

func detectContainerRuntime() (container.ContainerRuntime, error) {
	appleRuntime := container.NewAppleContainerRuntime()
	if err := appleRuntime.IsAvailable(context.Background()); err == nil {
		log.Println("auto-detected container runtime: apple")
		return appleRuntime, nil
	}
	dockerRuntime, err := container.NewDockerRuntime()
	if err == nil {
		if dockerErr := dockerRuntime.IsAvailable(context.Background()); dockerErr == nil {
			log.Println("auto-detected container runtime: docker")
			return dockerRuntime, nil
		}
	}
	return nil, fmt.Errorf("no container runtime available: install Docker or Apple Container CLI")
}

func ensureDirectories(paths ...string) error {
	for _, path := range paths {
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("creating directory %s: %w", path, err)
		}
	}
	return nil
}

func removeStaleSocket(socketPath string) error {
	if _, err := os.Stat(socketPath); err == nil {
		if err := os.Remove(socketPath); err != nil {
			return fmt.Errorf("removing stale socket %s: %w", socketPath, err)
		}
	}
	return nil
}
