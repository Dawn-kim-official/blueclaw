package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueclaw/blueclaw/internal/agent"
	"github.com/blueclaw/blueclaw/internal/configuration"
	"github.com/blueclaw/blueclaw/internal/outbox"
	"github.com/blueclaw/blueclaw/internal/planning"
	"github.com/blueclaw/blueclaw/internal/provider"
	"github.com/blueclaw/blueclaw/internal/scheduler"
	"github.com/blueclaw/blueclaw/internal/tool"
)

type HeartbeatResetter interface {
	ResetToDefault()
}

type Server struct {
	agentRunner       agent.Runner
	llmProvider       provider.LLMProvider
	toolRegistry      *tool.Registry
	blueclawDirectory string
	configuration     configuration.Configuration
	sessions          map[string]*agent.Session
	sessionsMutex     sync.RWMutex
	outbox            *outbox.Outbox
	schedulerService  *scheduler.Service
	heartbeatResetter HeartbeatResetter
}

func NewServer(agentRunner agent.Runner, llmProvider provider.LLMProvider, toolRegistry *tool.Registry, blueclawDirectory string, config configuration.Configuration, messageOutbox *outbox.Outbox, schedulerService *scheduler.Service, heartbeatResetter HeartbeatResetter) *Server {
	return &Server{
		agentRunner:       agentRunner,
		llmProvider:       llmProvider,
		toolRegistry:      toolRegistry,
		blueclawDirectory: blueclawDirectory,
		configuration:     config,
		sessions:          make(map[string]*agent.Session),
		outbox:            messageOutbox,
		schedulerService:  schedulerService,
		heartbeatResetter: heartbeatResetter,
	}
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat", server.handleChat)
	mux.HandleFunc("DELETE /v1/sessions/{sessionID}", server.handleDeleteSession)
	mux.HandleFunc("GET /v1/health", server.handleHealth)
	mux.HandleFunc("GET /v1/tasks", server.handleListTasks)
	mux.HandleFunc("GET /v1/outbox", server.handleGetOutbox)
	mux.HandleFunc("DELETE /v1/outbox", server.handleClearOutbox)
	mux.HandleFunc("POST /tools/remember", server.handleToolExecution("remember"))
	mux.HandleFunc("POST /tools/recall", server.handleToolExecution("recall"))
	mux.HandleFunc("POST /tools/schedule", server.handleToolExecution("schedule"))
	return mux
}

type chatRequest struct {
	Message   string `json:"message"`
	SessionID string `json:"sessionID"`
}

type chatStreamAcknowledgment struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}
type chatStreamDone struct {
	Type      string   `json:"type"`
	SessionID string   `json:"sessionID"`
	Response  string   `json:"response"`
	ToolsUsed []string `json:"toolsUsed,omitempty"`
}
type chatStreamError struct {
	Type  string `json:"type"`
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

type healthResponse struct {
	Status         string `json:"status"`
	ActiveSessions int    `json:"activeSessions"`
}

func startChatStream(writer http.ResponseWriter) func(any) {
	writer.Header().Set("Content-Type", "application/x-ndjson")
	writer.WriteHeader(http.StatusOK)
	flusher, _ := writer.(http.Flusher)
	return func(event any) {
		json.NewEncoder(writer).Encode(event)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (server *Server) handleChat(writer http.ResponseWriter, request *http.Request) {
	var chatRequest chatRequest
	if err := json.NewDecoder(request.Body).Decode(&chatRequest); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
		return
	}
	if chatRequest.Message == "" {
		writeError(writer, http.StatusBadRequest, "message is required", "MISSING_MESSAGE")
		return
	}
	if server.heartbeatResetter != nil {
		server.heartbeatResetter.ResetToDefault()
	}
	session := server.getOrCreateSession(chatRequest.SessionID)
	session.AddMessage(agent.UserMessage(chatRequest.Message))
	promptContext := agent.PromptContext{
		BlueclawDirectory: server.blueclawDirectory,
		ToolRegistry:      server.toolRegistry,
	}
	llmRequest, err := agent.BuildRequest(promptContext, session)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error(), "PROMPT_BUILD_ERROR")
		return
	}
	runContext, cancel := context.WithTimeout(request.Context(), 10*time.Minute)
	defer cancel()
	var plan planning.TaskPlan
	if server.llmProvider != nil {
		var planErr error
		plan, planErr = planning.PlanTask(runContext, server.llmProvider, llmRequest.Messages, llmRequest.Model)
		if planErr != nil {
			fmt.Printf("warning: planning failed: %v\n", planErr)
		}
	}
	tasksDirectory := filepath.Join(server.blueclawDirectory, "public/tasks")
	achievementsDirectory := filepath.Join(server.blueclawDirectory, "public/achievements")
	if plan.Achievement != nil {
		if createErr := planning.CreateTaskFromPlan(*plan.Achievement, tasksDirectory); createErr != nil {
			fmt.Printf("warning: failed to create task: %v\n", createErr)
		} else {
			llmRequest.SystemPrompt += fmt.Sprintf("\n\nYou are working on task: %s. When calling update_task, use name=%q.", plan.Achievement.Name, plan.Achievement.Name)
		}
	}
	writeEvent := startChatStream(writer)
	if acknowledgment := buildAcknowledgment(plan); acknowledgment != "" {
		writeEvent(chatStreamAcknowledgment{Type: "acknowledgment", Content: acknowledgment})
	}
	response, err := server.agentRunner.RunAgent(runContext, llmRequest, session.ID)
	if err != nil {
		writeEvent(chatStreamError{Type: "error", Error: err.Error(), Code: "LLM_ERROR"})
		return
	}
	if plan.Achievement != nil {
		if promoteErr := tool.PromoteTaskToAchievement(plan.Achievement.Name, tasksDirectory, achievementsDirectory); promoteErr != nil {
			fmt.Printf("warning: failed to promote task to achievement: %v\n", promoteErr)
		}
	}
	session.AddMessage(response.Message)
	sessionsDirectory := server.blueclawDirectory + "/sessions"
	if saveError := session.Save(sessionsDirectory); saveError != nil {
		fmt.Printf("warning: failed to save session %s: %v\n", session.ID, saveError)
	}
	toolsUsed := extractToolNames(response.ToolCalls)
	responseContent := response.Message.Content
	if len(response.IntermediateContent) > 0 {
		responseContent = strings.Join(response.IntermediateContent, "\n\n") + "\n\n" + responseContent
	}
	writeEvent(chatStreamDone{
		Type:      "done",
		SessionID: session.ID,
		Response:  responseContent,
		ToolsUsed: toolsUsed,
	})
}

func (server *Server) handleDeleteSession(writer http.ResponseWriter, request *http.Request) {
	sessionID := request.PathValue("sessionID")
	server.sessionsMutex.Lock()
	_, exists := server.sessions[sessionID]
	if exists {
		delete(server.sessions, sessionID)
	}
	server.sessionsMutex.Unlock()
	if !exists {
		writeError(writer, http.StatusNotFound, "session not found", "SESSION_NOT_FOUND")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) handleHealth(writer http.ResponseWriter, _ *http.Request) {
	server.sessionsMutex.RLock()
	activeSessionCount := len(server.sessions)
	server.sessionsMutex.RUnlock()
	writeJSON(writer, http.StatusOK, healthResponse{
		Status:         "healthy",
		ActiveSessions: activeSessionCount,
	})
}

func (server *Server) handleListTasks(writer http.ResponseWriter, _ *http.Request) {
	jobs := server.schedulerService.ListJobs()
	writeJSON(writer, http.StatusOK, map[string]any{"tasks": jobs})
}

func (server *Server) handleGetOutbox(writer http.ResponseWriter, _ *http.Request) {
	messages, err := server.outbox.List()
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error(), "OUTBOX_ERROR")
		return
	}
	if messages == nil {
		messages = []outbox.ProactiveMessage{}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"messages": messages})
}

func (server *Server) handleClearOutbox(writer http.ResponseWriter, _ *http.Request) {
	if err := server.outbox.Clear(); err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error(), "OUTBOX_ERROR")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) getOrCreateSession(sessionID string) *agent.Session {
	server.sessionsMutex.Lock()
	defer server.sessionsMutex.Unlock()
	if sessionID != "" {
		if session, exists := server.sessions[sessionID]; exists {
			return session
		}
	}
	newID := sessionID
	if newID == "" {
		newID = generateSessionID()
	}
	session := agent.NewSession(newID)
	server.sessions[newID] = session
	return session
}

func generateSessionID() string {
	return fmt.Sprintf("ses_%d", time.Now().UnixNano())
}

func extractToolNames(toolCalls []provider.ToolCall) []string {
	if len(toolCalls) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	names := make([]string, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		if !seen[toolCall.Name] {
			seen[toolCall.Name] = true
			names = append(names, toolCall.Name)
		}
	}
	return names
}

func buildAcknowledgment(plan planning.TaskPlan) string {
	if plan.Achievement == nil {
		return ""
	}
	if plan.Achievement.EtaMinutes > 3 {
		return fmt.Sprintf("%s (this will take about %d minutes)", plan.Acknowledgment, plan.Achievement.EtaMinutes)
	}
	return plan.Acknowledgment
}

func writeJSON(writer http.ResponseWriter, statusCode int, data any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	json.NewEncoder(writer).Encode(data)
}

func writeError(writer http.ResponseWriter, statusCode int, message string, code string) {
	writeJSON(writer, statusCode, errorResponse{Error: message, Code: code})
}

func (server *Server) handleToolExecution(toolName string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		var arguments map[string]any
		if err := json.NewDecoder(request.Body).Decode(&arguments); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
			return
		}
		registeredTool, err := server.toolRegistry.Get(toolName)
		if err != nil {
			writeError(writer, http.StatusNotFound, fmt.Sprintf("tool %q not found", toolName), "TOOL_NOT_FOUND")
			return
		}
		result, err := registeredTool.Execute(request.Context(), arguments)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error(), "TOOL_ERROR")
			return
		}
		if result.Error != "" {
			writeError(writer, http.StatusBadRequest, result.Error, "TOOL_VALIDATION_ERROR")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok", "output": result.Output})
	}
}
