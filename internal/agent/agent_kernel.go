package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

type AgentKernel struct {
	planCompiler       PlanCompiler
	subagentDispatcher SubagentDispatcher
	taskRunService     *task.TaskRunService
	taskStepService    *task.TaskStepService
	languageModel      llm.LanguageModelProvider
}

func NewAgentKernel(taskRunService *task.TaskRunService, taskStepService *task.TaskStepService) *AgentKernel {
	return &AgentKernel{
		planCompiler:       PlanCompiler{},
		subagentDispatcher: SubagentDispatcher{},
		taskRunService:     taskRunService,
		taskStepService:    taskStepService,
	}
}

func (agentKernel *AgentKernel) UseLanguageModelProvider(languageModel llm.LanguageModelProvider) {
	agentKernel.languageModel = languageModel
}

func (agentKernel *AgentKernel) HandleInboundMessage(requesterPersonID string, originConversationID string, prompt string) (task.TaskRun, error) {
	return agentKernel.RunTask(requesterPersonID, originConversationID, prompt)
}

func (agentKernel *AgentKernel) GenerateReply(responseContext context.Context, prompt string) (string, error) {
	if agentKernel.languageModel == nil {
		return "", errors.New("language model provider is not configured")
	}

	structuredResponse, errorValue := agentKernel.languageModel.GenerateStructuredResponse(
		responseContext,
		llm.StructuredResponseRequest{
			Messages: []llm.Message{
				{
					Role:    "system",
					Content: "You are Blueclaw. Reply helpfully and concisely to the user message.",
				},
				{
					Role:    "user",
					Content: prompt,
				},
			},
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
