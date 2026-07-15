package agentruntime

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

type taskHistoryToolInput struct {
	Limit  int    `json:"limit"`
	Status string `json:"status"`
}

type taskHistoryToolOutput struct {
	Tasks []taskHistoryItem `json:"tasks"`
}

type taskHistoryItem struct {
	TaskRunID     string `json:"taskRunID"`
	Status        string `json:"status"`
	Prompt        string `json:"prompt"`
	Result        string `json:"result,omitempty"`
	FailureReason string `json:"failureReason,omitempty"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerTaskHistoryTool(toolRegistry *agent.ToolSet, request ToolCatalogRequest) {
	if toolCatalogBuilder.taskRunService == nil || strings.TrimSpace(request.RequesterPersonID) == "" {
		return
	}
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[taskHistoryToolInput, taskHistoryToolOutput]{
		Definition: agent.ToolDefinition{
			Name:        "task.history",
			Description: "List the requester's recent task runs with status, result, and failure reason, newest first. Use it to answer what you worked on earlier or why a past task failed.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"number"},"status":{"type":"string","description":"Optional status filter: planned, running, waiting_user_input, waiting_approval, blocked, completed, failed, cancelled"}},"additionalProperties":false}`),
		},
		RejectUnknownInputFields: true,
		Handler: func(toolContext context.Context, input taskHistoryToolInput) (taskHistoryToolOutput, error) {
			return toolCatalogBuilder.listTaskHistory(input, request), nil
		},
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) listTaskHistory(input taskHistoryToolInput, request ToolCatalogRequest) taskHistoryToolOutput {
	limit := input.Limit
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	statusFilter := strings.TrimSpace(input.Status)
	taskRuns := toolCatalogBuilder.taskRunService.ListTaskRunByPersonID(strings.TrimSpace(request.RequesterPersonID))
	sort.Slice(taskRuns, func(left int, right int) bool {
		return taskRuns[left].UpdatedAt.After(taskRuns[right].UpdatedAt)
	})
	items := []taskHistoryItem{}
	for _, taskRun := range taskRuns {
		if statusFilter != "" && string(taskRun.Status) != statusFilter {
			continue
		}
		items = append(items, taskHistoryItemFromRun(taskRun))
		if len(items) == limit {
			break
		}
	}
	return taskHistoryToolOutput{Tasks: items}
}

func taskHistoryItemFromRun(taskRun task.TaskRun) taskHistoryItem {
	return taskHistoryItem{
		TaskRunID:     taskRun.TaskRunID,
		Status:        string(taskRun.Status),
		Prompt:        truncateTaskHistoryText(taskRun.Prompt, 200),
		Result:        truncateTaskHistoryText(taskRun.Result, 400),
		FailureReason: truncateTaskHistoryText(taskRun.FailureReason, 400),
		CreatedAt:     taskRun.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     taskRun.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func truncateTaskHistoryText(text string, maximumLength int) string {
	trimmedText := strings.TrimSpace(text)
	characters := []rune(trimmedText)
	if len(characters) <= maximumLength {
		return trimmedText
	}
	return strings.TrimSpace(string(characters[:maximumLength])) + "…"
}
