package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/blueclaw/blueclaw/internal/configuration"
	"github.com/blueclaw/blueclaw/internal/container"
	"github.com/blueclaw/blueclaw/internal/daemon"
	"github.com/blueclaw/blueclaw/internal/initialize"
)

type CLI struct {
	Init           InitCommand           `cmd:"" help:"Initialize Blueclaw and build the container image."`
	Daemon         DaemonCommand         `cmd:"" help:"Start the Blueclaw daemon process."`
	Chat           ChatCommand           `cmd:"" help:"Send a message or start an interactive chat session."`
	Tasks          TasksCommand          `cmd:"" help:"List active scheduled jobs."`
	Clean          CleanCommand          `cmd:"" help:"Remove all stopped or orphaned Blueclaw containers."`
	ContainerAgent ContainerAgentCommand `cmd:"" hidden:""`
}

type InitCommand struct {
	Reset         bool `help:"Reset all configuration files, re-download models, and rebuild the container image." default:"false"`
	ResetSoul     bool `help:"Overwrite SOUL.md with the default personality." name:"reset-soul" default:"false"`
	ResetHeartbeat bool `help:"Overwrite HEARTBEAT.md with the default prompt." name:"reset-heartbeat" default:"false"`
}

type DaemonCommand struct {
	Debug bool `help:"Enable verbose debug logging for the daemon and container protocol." default:"false"`
}

type ChatCommand struct {
	Message string `arg:"" optional:"" help:"Message to send (omit for interactive REPL)."`
}

type TasksCommand struct{}

type CleanCommand struct{}

func (command *InitCommand) Run() error {
	blueclawDirectory := configuration.BlueclawDirectory()
	if command.ResetSoul {
		if err := initialize.ResetSoul(blueclawDirectory); err != nil {
			return fmt.Errorf("resetting SOUL.md: %w", err)
		}
		fmt.Printf("SOUL.md reset to default at %s\n", blueclawDirectory)
		return nil
	}
	if command.ResetHeartbeat {
		if err := initialize.ResetHeartbeat(blueclawDirectory); err != nil {
			return fmt.Errorf("resetting HEARTBEAT.md: %w", err)
		}
		fmt.Printf("HEARTBEAT.md reset to default at %s\n", blueclawDirectory)
		return nil
	}
	if err := initialize.Run(blueclawDirectory, command.Reset); err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}
	if err := initialize.EnsureEmbeddingModel(blueclawDirectory, command.Reset); err != nil {
		return fmt.Errorf("downloading embedding model: %w", err)
	}
	config, err := configuration.Load(configuration.ConfigFilePath())
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}
	runtimeName := config.ContainerRuntime
	if runtimeName == "" {
		runtimeName = detectContainerRuntimeName()
	}
	if runtimeName == "apple" {
		if err := ensureAppleContainerReady(); err != nil {
			return fmt.Errorf("setting up Apple Container: %w", err)
		}
	}
	imageName := config.ContainerImage
	if imageName == "" {
		imageName = configuration.DefaultContainerImage
	}
	if err := buildContainerImage(runtimeName, imageName); err != nil {
		return fmt.Errorf("building container image: %w", err)
	}
	fmt.Printf("Blueclaw initialized at %s\n", blueclawDirectory)
	return nil
}

func buildContainerImage(runtime string, imageName string) error {
	if _, err := os.Stat("Dockerfile"); err != nil {
		return fmt.Errorf("Dockerfile not found in current directory — run this command from the blueclaw source directory")
	}
	var buildCommand *exec.Cmd
	switch runtime {
	case "apple":
		buildCommand = exec.Command("container", "build", "-t", imageName, ".")
	case "docker":
		buildCommand = exec.Command("docker", "build", "-t", imageName, ".")
	default:
		return fmt.Errorf("unknown container runtime %q — set containerRuntime in config.toml", runtime)
	}
	buildCommand.Stdout = os.Stdout
	buildCommand.Stderr = os.Stderr
	fmt.Printf("Building container image %s using %s runtime...\n", imageName, runtime)
	if err := buildCommand.Run(); err != nil {
		return fmt.Errorf("building container image: %w", err)
	}
	fmt.Printf("Container image %s built successfully.\n", imageName)
	return nil
}

func ensureAppleContainerReady() error {
	fmt.Println("Starting Apple Container services...")
	startCommand := exec.Command("container", "system", "start")
	startCommand.Stdout = os.Stdout
	startCommand.Stderr = os.Stderr
	startCommand.Run()
	if isAppleContainerKernelInstalled() {
		fmt.Println("Apple Container kernel already installed, skipping.")
		return nil
	}
	fmt.Println("Configuring default kernel (this downloads ~300MB on first run)...")
	kernelCommand := exec.Command("container", "system", "kernel", "set", "--recommended")
	kernelCommand.Stdout = os.Stdout
	kernelCommand.Stderr = os.Stderr
	kernelCommand.Run()
	return nil
}

func isAppleContainerKernelInstalled() bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	kernelsDir := filepath.Join(homeDir, "Library", "Application Support", "com.apple.container", "kernels")
	entries, err := os.ReadDir(kernelsDir)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

func detectContainerRuntimeName() string {
	if _, err := exec.LookPath("container"); err == nil {
		return "apple"
	}
	return "docker"
}

func (command *DaemonCommand) Run() error {
	config, err := configuration.Load(configuration.ConfigFilePath())
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}
	config.Debug = command.Debug
	return daemon.Start(config)
}

func (command *ChatCommand) Run() error {
	if command.Message != "" {
		return sendOneShot(command.Message)
	}
	config, err := configuration.Load(configuration.ConfigFilePath())
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}
	return runREPL(config.ParsedOutboxPollInterval())
}

func (command *TasksCommand) Run() error {
	client := daemonClient()
	response, err := client.Get("http://blueclaw/v1/tasks")
	if err != nil {
		return fmt.Errorf("connecting to daemon (is 'blueclaw daemon' running?): %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	var tasksPayload struct {
		Tasks []struct {
			ID             string `json:"id"`
			CronExpression string `json:"cronExpression"`
			Prompt         string `json:"prompt"`
			LastRunAt      string `json:"lastRunAt"`
			NextRunAt      string `json:"nextRunAt"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(responseBody, &tasksPayload); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}
	if len(tasksPayload.Tasks) == 0 {
		fmt.Println("No scheduled tasks.")
		return nil
	}
	for _, task := range tasksPayload.Tasks {
		fmt.Printf("[%s] %s  cron=%s  next=%s\n", task.ID, task.Prompt, task.CronExpression, task.NextRunAt)
	}
	return nil
}

func (command *CleanCommand) Run() error {
	config, err := configuration.Load(configuration.ConfigFilePath())
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}
	runtime, err := createRuntimeFromConfig(config)
	if err != nil {
		return fmt.Errorf("creating container runtime: %w", err)
	}
	containers, err := runtime.ListContainers(context.Background(), "")
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}
	removed := 0
	for _, containerInfo := range containers {
		if !strings.HasPrefix(containerInfo.Name, "blueclaw-") {
			continue
		}
		fmt.Printf("removing %s...\n", containerInfo.Name)
		if removeError := runtime.RemoveContainer(context.Background(), containerInfo.ID); removeError != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove %s: %v\n", containerInfo.Name, removeError)
		} else {
			removed++
		}
	}
	fmt.Printf("removed %d container(s)\n", removed)
	return nil
}

func createRuntimeFromConfig(config configuration.Configuration) (container.ContainerRuntime, error) {
	runtimeName := config.ContainerRuntime
	if runtimeName == "" {
		runtimeName = detectContainerRuntimeName()
	}
	switch runtimeName {
	case "apple":
		return container.NewAppleContainerRuntime(), nil
	case "docker":
		return container.NewDockerRuntime()
	default:
		return nil, fmt.Errorf("unknown container runtime %q", runtimeName)
	}
}

func sendOneShot(message string) error {
	response, err := sendChatMessage(message, "")
	if err != nil {
		return err
	}
	fmt.Println(response.Response)
	return nil
}

func runREPL(outboxPollInterval time.Duration) error {
	deliverOutboxMessages()
	done := make(chan struct{})
	go pollOutboxUntilDone(done, outboxPollInterval)
	defer close(done)
	scanner := bufio.NewScanner(os.Stdin)
	var sessionID string
	fmt.Println("Blueclaw REPL (type 'exit' or Ctrl+D to quit)")
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}
		response, err := sendChatMessage(input, sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		sessionID = response.SessionID
		fmt.Println(response.Response)
	}
	fmt.Println()
	return nil
}

func pollOutboxUntilDone(done <-chan struct{}, outboxPollInterval time.Duration) {
	ticker := time.NewTicker(outboxPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			deliverOutboxMessages()
		}
	}
}

type chatResponsePayload struct {
	SessionID string   `json:"sessionID"`
	Response  string   `json:"response"`
	ToolsUsed []string `json:"toolsUsed,omitempty"`
}

type chatRequestPayload struct {
	Message   string `json:"message"`
	SessionID string `json:"sessionID,omitempty"`
}

type errorResponsePayload struct {
	Error string `json:"error"`
}

func readChatStream(body io.Reader) (chatResponsePayload, error) {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		var event struct {
			Type string `json:"type"`
		}
		line := scanner.Bytes()
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		switch event.Type {
		case "acknowledgment":
			var acknowledgment struct {
				Content string `json:"content"`
			}
			if json.Unmarshal(line, &acknowledgment) == nil {
				fmt.Println(acknowledgment.Content)
			}
		case "done":
			var done chatResponsePayload
			if err := json.Unmarshal(line, &done); err != nil {
				return chatResponsePayload{}, fmt.Errorf("parsing done event: %w", err)
			}
			return done, nil
		case "error":
			var streamError struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(line, &streamError) == nil {
				return chatResponsePayload{}, fmt.Errorf("daemon error: %s", streamError.Error)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return chatResponsePayload{}, fmt.Errorf("reading stream: %w", err)
	}
	return chatResponsePayload{}, fmt.Errorf("stream ended without done event")
}

func sendChatMessage(message string, sessionID string) (chatResponsePayload, error) {
	payload := chatRequestPayload{Message: message, SessionID: sessionID}
	body, err := json.Marshal(payload)
	if err != nil {
		return chatResponsePayload{}, fmt.Errorf("marshaling request: %w", err)
	}
	client := daemonClient()
	response, err := client.Post("http://blueclaw/v1/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return chatResponsePayload{}, fmt.Errorf("daemon is not running. Start it with: blueclaw daemon")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(response.Body)
		var errorPayload errorResponsePayload
		if json.Unmarshal(responseBody, &errorPayload) == nil && errorPayload.Error != "" {
			return chatResponsePayload{}, fmt.Errorf("daemon error: %s", errorPayload.Error)
		}
		return chatResponsePayload{}, fmt.Errorf("daemon error (status %d): %s", response.StatusCode, string(responseBody))
	}
	return readChatStream(response.Body)
}

func deliverOutboxMessages() {
	client := daemonClient()
	response, err := client.Get("http://blueclaw/v1/outbox")
	if err != nil {
		return
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(response.Body)
	var outboxPayload struct {
		Messages []struct {
			Source  string `json:"source"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(responseBody, &outboxPayload) != nil {
		return
	}
	if len(outboxPayload.Messages) == 0 {
		return
	}
	fmt.Println("--- Pending messages ---")
	for _, message := range outboxPayload.Messages {
		fmt.Printf("[%s] %s\n", message.Source, message.Content)
	}
	fmt.Println("------------------------")
	deleteRequest, _ := http.NewRequest(http.MethodDelete, "http://blueclaw/v1/outbox", nil)
	client.Do(deleteRequest)
}

func daemonClient() *http.Client {
	socketPath := filepath.Join(configuration.BlueclawDirectory(), "daemon.sock")
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}
}

func main() {
	cli := CLI{}
	kongContext := kong.Parse(&cli,
		kong.Name("blueclaw"),
		kong.Description("Ultra-lightweight personal AI assistant."),
		kong.UsageOnError(),
	)
	if err := kongContext.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
