package container

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/blueclaw/blueclaw/internal/ipc"
	"github.com/blueclaw/blueclaw/internal/memory"
	"github.com/blueclaw/blueclaw/internal/provider"
	"github.com/blueclaw/blueclaw/internal/tool"
)

type HeartbeatIntervalSetter interface {
	SetInterval(duration time.Duration)
}

type Manager struct {
	runtime                 ContainerRuntime
	image                   string
	networkMode             string
	publicDirectory         string
	dataDirectory           string
	llmProvider             provider.LLMProvider
	embeddingClient         *memory.EmbeddingClient
	jobScheduler            tool.JobScheduler
	heartbeatIntervalSetter HeartbeatIntervalSetter
	debug                   bool
}

func NewManager(runtime ContainerRuntime, image string, networkMode string, publicDirectory string, dataDirectory string, llmProvider provider.LLMProvider, embeddingClient *memory.EmbeddingClient, jobScheduler tool.JobScheduler, heartbeatIntervalSetter HeartbeatIntervalSetter, debug bool) *Manager {
	return &Manager{
		runtime:                 runtime,
		image:                   image,
		networkMode:             networkMode,
		publicDirectory:         publicDirectory,
		dataDirectory:           dataDirectory,
		llmProvider:             llmProvider,
		embeddingClient:         embeddingClient,
		jobScheduler:            jobScheduler,
		heartbeatIntervalSetter: heartbeatIntervalSetter,
		debug:                   debug,
	}
}

func (manager *Manager) debugLog(format string, args ...any) {
	if manager.debug {
		log.Printf("[debug] "+format, args...)
	}
}

func (manager *Manager) RunAgent(executionContext context.Context, sessionID string, request ipc.AgentRequest) (ipc.AgentResponse, error) {
	containerName := fmt.Sprintf("blueclaw-%s-%d", sessionID, time.Now().UnixNano())
	containerID, err := manager.startContainer(executionContext, containerName)
	if err != nil {
		return ipc.AgentResponse{}, err
	}
	defer manager.stopAndRemove(containerID)
	manager.debugLog("starting container-agent in container %s (session %s)", containerID, sessionID)
	session, err := manager.runtime.ExecInteractive(
		executionContext,
		containerID,
		[]string{"/usr/local/bin/blueclaw", "container-agent"},
	)
	if err != nil {
		return ipc.AgentResponse{}, fmt.Errorf("starting container agent: %w", err)
	}
	defer session.Close()
	requestData, err := json.Marshal(request)
	if err != nil {
		return ipc.AgentResponse{}, fmt.Errorf("marshaling agent request: %w", err)
	}
	manager.debugLog("writing initial agent request (%d bytes)", len(requestData))
	if err := session.WriteLine(string(requestData)); err != nil {
		return ipc.AgentResponse{}, fmt.Errorf("writing agent request: %w", err)
	}
	return manager.runProtocol(executionContext, session)
}

func (manager *Manager) startContainer(executionContext context.Context, containerName string) (string, error) {
	configuration := ContainerConfig{
		Image: manager.image,
		Name:  containerName,
		Mounts: []BindMount{
			{Source: manager.publicDirectory, Target: "/workspace"},
			{Source: manager.dataDirectory + "/db", Target: "/data/db"},
		},
		NetworkMode: manager.networkMode,
	}
	containerID, err := manager.runtime.CreateContainer(executionContext, configuration)
	if err != nil {
		return "", fmt.Errorf("creating container %s: %w", containerName, err)
	}
	if err := manager.runtime.StartContainer(executionContext, containerID); err != nil {
		manager.runtime.RemoveContainer(context.Background(), containerID)
		return "", fmt.Errorf("starting container %s: %w", containerName, err)
	}
	return containerID, nil
}

func (manager *Manager) stopAndRemove(containerID string) {
	manager.runtime.StopContainer(context.Background(), containerID)
	manager.runtime.RemoveContainer(context.Background(), containerID)
}

func (manager *Manager) runProtocol(executionContext context.Context, session InteractiveSession) (ipc.AgentResponse, error) {
	var notifications []string
	for {
		line, err := session.ReadLine()
		if err != nil {
			return ipc.AgentResponse{}, fmt.Errorf("reading from container: %w", err)
		}
		var message ipc.StdioOutbound
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			return ipc.AgentResponse{}, fmt.Errorf("parsing container message: %w", err)
		}
		manager.debugLog("received message from container: type=%s", message.Type)
		switch message.Type {
		case "notify":
			if message.Notification != "" {
				notifications = append(notifications, message.Notification)
			}
		case "llm_request":
			if err := manager.handleLLMRequest(executionContext, session, message); err != nil {
				return ipc.AgentResponse{}, err
			}
		case "embedding_request":
			if err := manager.handleEmbeddingRequest(executionContext, session, message); err != nil {
				return ipc.AgentResponse{}, err
			}
		case "schedule_request":
			if err := manager.handleScheduleRequest(session, message); err != nil {
				return ipc.AgentResponse{}, err
			}
		case "heartbeat_interval_request":
			if err := manager.handleHeartbeatIntervalRequest(session, message); err != nil {
				return ipc.AgentResponse{}, err
			}
		case "done":
			if message.DoneResponse == nil {
				return ipc.AgentResponse{}, fmt.Errorf("done message missing response payload")
			}
			manager.debugLog("container agent finished successfully")
			intermediateContent := append(notifications, message.DoneResponse.IntermediateContent...)
			return ipc.AgentResponse{
				Message:             message.DoneResponse.Message,
				ToolCalls:           message.DoneResponse.ToolCalls,
				IntermediateContent: intermediateContent,
			}, nil
		case "error":
			return ipc.AgentResponse{}, fmt.Errorf("container agent error: %s", message.ErrorMessage)
		default:
			return ipc.AgentResponse{}, fmt.Errorf("unknown message type from container: %s", message.Type)
		}
	}
}

func (manager *Manager) handleLLMRequest(executionContext context.Context, session InteractiveSession, message ipc.StdioOutbound) error {
	if message.LLMRequest == nil {
		return writeSessionInbound(session, ipc.StdioInbound{Type: "llm_response", ErrorMessage: "missing LLM request payload"})
	}
	response, err := manager.llmProvider.SendMessage(executionContext, *message.LLMRequest)
	if err != nil {
		return writeSessionInbound(session, ipc.StdioInbound{Type: "llm_response", ErrorMessage: err.Error()})
	}
	return writeSessionInbound(session, ipc.StdioInbound{Type: "llm_response", LLMResponse: &response})
}

func (manager *Manager) handleEmbeddingRequest(executionContext context.Context, session InteractiveSession, message ipc.StdioOutbound) error {
	if message.EmbeddingRequest == nil {
		return writeSessionInbound(session, ipc.StdioInbound{Type: "embedding_response", ErrorMessage: "missing embedding request payload"})
	}
	if manager.embeddingClient == nil || !manager.embeddingClient.IsAvailable() {
		return writeSessionInbound(session, ipc.StdioInbound{Type: "embedding_response", ErrorMessage: "embedding sidecar unavailable"})
	}
	vector, err := manager.embeddingClient.Generate(executionContext, message.EmbeddingRequest.Text)
	if err != nil {
		return writeSessionInbound(session, ipc.StdioInbound{Type: "embedding_response", ErrorMessage: err.Error()})
	}
	return writeSessionInbound(session, ipc.StdioInbound{Type: "embedding_response", EmbeddingVector: vector})
}

func (manager *Manager) handleScheduleRequest(session InteractiveSession, message ipc.StdioOutbound) error {
	if message.ScheduleRequest == nil {
		return writeSessionInbound(session, ipc.StdioInbound{Type: "schedule_response", ErrorMessage: "missing schedule request payload"})
	}
	job, err := manager.jobScheduler.AddJob(message.ScheduleRequest.CronExpression, message.ScheduleRequest.Prompt)
	if err != nil {
		return writeSessionInbound(session, ipc.StdioInbound{Type: "schedule_response", ErrorMessage: err.Error()})
	}
	return writeSessionInbound(session, ipc.StdioInbound{
		Type:         "schedule_response",
		ScheduleID:   job.ID,
		ScheduleNext: job.NextRunAt.Format("2006-01-02T15:04:05Z"),
	})
}

func (manager *Manager) handleHeartbeatIntervalRequest(session InteractiveSession, message ipc.StdioOutbound) error {
	if message.HeartbeatIntervalRequest == nil {
		return writeSessionInbound(session, ipc.StdioInbound{Type: "heartbeat_interval_response", ErrorMessage: "missing heartbeat interval request payload"})
	}
	duration, err := time.ParseDuration(message.HeartbeatIntervalRequest.Duration)
	if err != nil {
		return writeSessionInbound(session, ipc.StdioInbound{Type: "heartbeat_interval_response", ErrorMessage: fmt.Sprintf("invalid duration: %v", err)})
	}
	if manager.heartbeatIntervalSetter != nil {
		manager.heartbeatIntervalSetter.SetInterval(duration)
	}
	return writeSessionInbound(session, ipc.StdioInbound{Type: "heartbeat_interval_response"})
}

func writeSessionInbound(session InteractiveSession, inbound ipc.StdioInbound) error {
	data, err := json.Marshal(inbound)
	if err != nil {
		return fmt.Errorf("marshaling inbound: %w", err)
	}
	return session.WriteLine(string(data))
}
