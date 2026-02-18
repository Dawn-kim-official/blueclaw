package tool

import (
	"context"
	"fmt"
	"time"
)

type JobScheduler interface {
	AddJob(cronExpression string, prompt string) (ScheduledJobInfo, error)
}

type ScheduledJobInfo struct {
	ID        string
	NextRunAt time.Time
}

type ScheduleTool struct {
	jobScheduler JobScheduler
}

func NewScheduleTool(jobScheduler JobScheduler) *ScheduleTool {
	return &ScheduleTool{jobScheduler: jobScheduler}
}

func (tool *ScheduleTool) Name() string { return "schedule" }

func (tool *ScheduleTool) Description() string {
	return "Create a recurring scheduled task using a cron expression."
}

func (tool *ScheduleTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cronExpression": map[string]any{
				"type":        "string",
				"description": "Standard cron expression (e.g., '0 9 * * 1' for every Monday at 9am)",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "The prompt to execute on schedule",
			},
		},
		"required": []string{"cronExpression", "prompt"},
	}
}

func (tool *ScheduleTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
	cronExpression, _ := arguments["cronExpression"].(string)
	prompt, _ := arguments["prompt"].(string)
	if cronExpression == "" {
		return Result{Error: "cronExpression is required"}, nil
	}
	if prompt == "" {
		return Result{Error: "prompt is required"}, nil
	}
	job, err := tool.jobScheduler.AddJob(cronExpression, prompt)
	if err != nil {
		return Result{Error: fmt.Sprintf("failed to schedule: %v", err)}, nil
	}
	return Result{Output: fmt.Sprintf("scheduled job %s, next run: %s", job.ID, job.NextRunAt.Format("2006-01-02T15:04:05Z"))}, nil
}
