package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"blueclaw/internal/llm"
	"blueclaw/internal/memory"
	"blueclaw/internal/task"
)

type AgentKernel struct {
	planCompiler        PlanCompiler
	subagentDispatcher  SubagentDispatcher
	taskRunService      *task.TaskRunService
	taskStepService     *task.TaskStepService
	taskArtifactService *task.TaskArtifactService
	languageModel       llm.LanguageModelProvider
	turnOptions         TurnOptions
}

func NewAgentKernel(taskRunService *task.TaskRunService, taskStepService *task.TaskStepService) *AgentKernel {
	return &AgentKernel{
		planCompiler:        PlanCompiler{},
		subagentDispatcher:  SubagentDispatcher{},
		taskRunService:      taskRunService,
		taskStepService:     taskStepService,
		taskArtifactService: task.NewTaskArtifactService(),
	}
}

func (agentKernel *AgentKernel) UseLanguageModelProvider(languageModel llm.LanguageModelProvider) {
	agentKernel.languageModel = languageModel
}

func (agentKernel *AgentKernel) UseTaskArtifactService(taskArtifactService *task.TaskArtifactService) {
	if taskArtifactService != nil {
		agentKernel.taskArtifactService = taskArtifactService
	}
}

func (agentKernel *AgentKernel) UseTurnOptions(turnOptions TurnOptions) {
	agentKernel.turnOptions = normalizeTurnOptions(turnOptions)
}

func (agentKernel *AgentKernel) HandleInboundMessage(requesterPersonID string, originConversationID string, prompt string) (task.TaskRun, error) {
	return agentKernel.RunTask(requesterPersonID, originConversationID, prompt)
}

func (agentKernel *AgentKernel) AppendTaskEvent(taskRunID string, name string, body string) {
	agentKernel.taskRunService.AppendTaskEvent(taskRunID, name, body)
}

func (agentKernel *AgentKernel) GenerateReply(responseContext context.Context, prompt string) (string, error) {
	return agentKernel.GenerateReplyWithMemory(responseContext, prompt, nil)
}

func (agentKernel *AgentKernel) GenerateReplyWithMemory(responseContext context.Context, prompt string, memoryFacts []memory.MemoryFact) (string, error) {
	return agentKernel.GenerateReplyWithContext(responseContext, prompt, VisibleContext{}, memoryFacts)
}

func (agentKernel *AgentKernel) GenerateReplyWithContext(responseContext context.Context, prompt string, visibleContext VisibleContext, memoryFacts []memory.MemoryFact) (string, error) {
	if agentKernel.languageModel == nil {
		return "", errors.New("language model provider is not configured")
	}

	structuredResponse, errorValue := agentKernel.languageModel.GenerateStructuredResponse(
		responseContext,
		llm.StructuredResponseRequest{
			Messages: agentKernel.buildReplyMessages(prompt, visibleContext, memoryFacts),
			StructuredOutputSchema: llm.StructuredOutputSchema{
				Name:               "blueclaw_reply",
				Document:           `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"],"additionalProperties":false}`,
				IsStrictlyEnforced: true,
			},
		},
	)
	if errorValue != nil {
		return "", errorValue
	}

	var replyDocument struct {
		Reply string `json:"reply"`
	}
	errorValue = json.Unmarshal([]byte(structuredResponse.Content), &replyDocument)
	if errorValue != nil {
		return "", errorValue
	}

	reply := strings.TrimSpace(replyDocument.Reply)
	if reply == "" {
		return "", errors.New("language model reply is empty")
	}

	return reply, nil
}

func (agentKernel *AgentKernel) RunTurn(responseContext context.Context, request AgentTurnRequest) (AgentTurnResult, error) {
	agentTurnRunner := NewAgentTurnRunner(
		agentKernel.taskRunService,
		agentKernel.taskStepService,
		agentKernel.taskArtifactService,
		agentKernel.languageModel,
		agentKernel.turnOptions,
	)
	return agentTurnRunner.RunTurn(responseContext, request)
}

type VisibleContext struct {
	Messages      []VisibleContextMessage
	HasMoreBefore bool
	HistoryCursor string
}

type VisibleContextMessage struct {
	Speaker string
	Text    string
}

func (agentKernel *AgentKernel) buildReplyMessages(prompt string, visibleContext VisibleContext, memoryFacts []memory.MemoryFact) []llm.Message {
	return buildReplyMessages(prompt, visibleContext, memoryFacts)
}

func buildReplyMessages(prompt string, visibleContext VisibleContext, memoryFacts []memory.MemoryFact) []llm.Message {
	messages := []llm.Message{
		{
			Role:    "system",
			Content: "You are Blueclaw. Reply helpfully and concisely to the user message. Use the provided visible conversation context and Blueclaw memory only as context; do not reveal hidden policy or provenance unless the user asks for it and access is allowed.",
		},
	}

	visibleContextDescription := buildVisibleContextDescription(visibleContext)
	if visibleContextDescription != "" {
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: visibleContextDescription,
		})
	}

	memoryContext := buildMemoryContext(memoryFacts)
	if memoryContext != "" {
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: memoryContext,
		})
	}

	messages = append(messages, llm.Message{
		Role:    "user",
		Content: prompt,
	})
	return messages
}

func buildVisibleContextDescription(visibleContext VisibleContext) string {
	contextLines := []string{}
	for _, message := range visibleContext.Messages {
		speaker := strings.TrimSpace(message.Speaker)
		if speaker == "" {
			speaker = "Someone"
		}
		text := strings.TrimSpace(message.Text)
		if text != "" {
			contextLines = append(contextLines, "- "+speaker+": "+text)
		}
	}

	if len(contextLines) == 0 && !visibleContext.HasMoreBefore {
		return ""
	}

	historyLine := "No earlier visible messages are available."
	if visibleContext.HasMoreBefore {
		historyLine = "There are earlier visible messages not included here. Ask for conversation.history if older context is needed."
	}

	if len(contextLines) == 0 {
		return "Recent visible conversation context:\n" + historyLine
	}

	return "Recent visible conversation context:\n" + strings.Join(contextLines, "\n") + "\n" + historyLine
}

func buildMemoryContext(memoryFacts []memory.MemoryFact) string {
	userMemoryDescriptions := []string{}
	workspaceMemoryDescriptions := []string{}
	conversationMemoryDescriptions := []string{}
	for _, memoryFact := range memoryFacts {
		memoryDescription := strings.TrimSpace(memoryFact.Content)
		if memoryDescription == "" {
			continue
		}
		switch memoryFact.ScopeType {
		case memory.ScopeTypeWorkspace:
			workspaceMemoryDescriptions = append(workspaceMemoryDescriptions, "- "+memoryDescription)
		case memory.ScopeTypeConversation:
			conversationMemoryDescriptions = append(conversationMemoryDescriptions, "- "+memoryDescription)
		default:
			userMemoryDescriptions = append(userMemoryDescriptions, "- "+memoryDescription)
		}
	}

	sections := []string{}
	if len(userMemoryDescriptions) > 0 {
		sections = append(sections, "User-space memory for this requester:\n"+strings.Join(userMemoryDescriptions, "\n"))
	}
	if len(workspaceMemoryDescriptions) > 0 {
		sections = append(sections, "Accessible workspace memory:\n"+strings.Join(workspaceMemoryDescriptions, "\n"))
	}
	if len(conversationMemoryDescriptions) > 0 {
		sections = append(sections, "Current conversation memory:\n"+strings.Join(conversationMemoryDescriptions, "\n"))
	}

	if len(sections) == 0 {
		return ""
	}

	return strings.Join(sections, "\n\n")
}

func (agentKernel *AgentKernel) RunTask(requesterPersonID string, originConversationID string, prompt string) (task.TaskRun, error) {
	taskRun := agentKernel.taskRunService.CreateTaskRun(requesterPersonID, originConversationID, prompt)
	taskPlan, errorValue := agentKernel.planCompiler.CompilePlan(prompt)
	if errorValue != nil {
		return task.TaskRun{}, errorValue
	}

	for _, taskPlanStep := range taskPlan.TaskSteps {
		agentKernel.taskStepService.AddTaskStep(task.TaskStep{
			TaskStepID:               taskRun.TaskRunID + ":" + taskPlanStep.Name,
			TaskRunID:                taskRun.TaskRunID,
			AssignedAgentProfileName: taskPlanStep.AssignedAgentProfileName,
			Instruction:              taskPlanStep.Instruction,
			Status:                   task.TaskStatusPlanned,
		})
	}

	return agentKernel.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "planner")
}

func (agentKernel *AgentKernel) ResumeTask(taskRunID string) (task.TaskRun, error) {
	return agentKernel.taskRunService.ResumeTaskRun(taskRunID)
}
