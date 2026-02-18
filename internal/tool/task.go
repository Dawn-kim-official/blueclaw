package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type CreateTaskTool struct {
	tasksDirectory string
}

func NewCreateTaskTool(tasksDirectory string) *CreateTaskTool {
	return &CreateTaskTool{tasksDirectory: tasksDirectory}
}

func (t *CreateTaskTool) Name() string { return "create_task" }
func (t *CreateTaskTool) Description() string {
	return "Create a task to track a multi-step task. Call this before starting work on tasks requiring multiple attempts."
}
func (t *CreateTaskTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "description": "Unique identifier for the task (slug format)"},
			"description": map[string]any{"type": "string", "description": "What needs to be accomplished"},
			"eta_minutes": map[string]any{"type": "integer", "description": "Estimated time in minutes (default 5)"},
			"plan":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Ordered list of steps to accomplish the task"},
		},
		"required": []string{"name", "description"},
	}
}

func (t *CreateTaskTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
	name, _ := arguments["name"].(string)
	if name == "" {
		return Result{Error: "name is required"}, nil
	}
	description, _ := arguments["description"].(string)
	etaMinutes := 5
	if rawETA, ok := arguments["eta_minutes"]; ok {
		switch value := rawETA.(type) {
		case float64:
			etaMinutes = int(value)
		case int:
			etaMinutes = value
		}
	}
	var plan []string
	if rawPlan, ok := arguments["plan"]; ok {
		if planSlice, ok := rawPlan.([]any); ok {
			for _, step := range planSlice {
				if stepStr, ok := step.(string); ok {
					plan = append(plan, stepStr)
				}
			}
		}
	}
	now := time.Now()
	task := Achievement{
		Name:        name,
		Description: description,
		CreatedAt:   now,
		ETA:         now.Add(time.Duration(etaMinutes) * time.Minute),
		Status:      "in_progress",
		Plan:        plan,
		Attempts:    []AchievementAttempt{},
	}
	filePath := filepath.Join(t.tasksDirectory, name+".toml")
	if err := writeAchievementFile(filePath, task); err != nil {
		return Result{Error: err.Error()}, nil
	}
	return Result{Output: fmt.Sprintf("task created: %s", filePath)}, nil
}

type UpdateTaskTool struct {
	tasksDirectory string
}

func NewUpdateTaskTool(tasksDirectory string) *UpdateTaskTool {
	return &UpdateTaskTool{tasksDirectory: tasksDirectory}
}

func (t *UpdateTaskTool) Name() string { return "update_task" }
func (t *UpdateTaskTool) Description() string {
	return "Update a task with a new attempt, status change, or failure details."
}
func (t *UpdateTaskTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":           map[string]any{"type": "string", "description": "Task name to update"},
			"attempt_method": map[string]any{"type": "string", "description": "Method used in this attempt"},
			"attempt_result": map[string]any{"type": "string", "description": "Result of this attempt"},
			"status":         map[string]any{"type": "string", "description": "New status: in_progress, completed, or failed"},
			"failure_reason": map[string]any{"type": "string", "description": "Why the task failed"},
			"user_ask":       map[string]any{"type": "string", "description": "What is needed from the user to proceed"},
		},
		"required": []string{"name"},
	}
}

func (t *UpdateTaskTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
	name, _ := arguments["name"].(string)
	if name == "" {
		return Result{Error: "name is required"}, nil
	}
	filePath := filepath.Join(t.tasksDirectory, name+".toml")
	task, err := readAchievementFile(filePath)
	if err != nil {
		return Result{Error: fmt.Sprintf("task %q not found: %v", name, err)}, nil
	}
	attemptMethod, _ := arguments["attempt_method"].(string)
	attemptResult, _ := arguments["attempt_result"].(string)
	if attemptMethod != "" || attemptResult != "" {
		task.Attempts = append(task.Attempts, AchievementAttempt{
			Method: attemptMethod,
			Result: attemptResult,
			Time:   time.Now(),
		})
	}
	if status, ok := arguments["status"].(string); ok && status != "" {
		task.Status = status
	}
	if failureReason, ok := arguments["failure_reason"].(string); ok && failureReason != "" {
		task.FailureReason = failureReason
	}
	if userAsk, ok := arguments["user_ask"].(string); ok && userAsk != "" {
		task.UserAsk = userAsk
	}
	if err := writeAchievementFile(filePath, task); err != nil {
		return Result{Error: err.Error()}, nil
	}
	return Result{Output: fmt.Sprintf("task updated: %s", filePath)}, nil
}

func PromoteTaskToAchievement(taskName string, tasksDirectory string, achievementsDirectory string) error {
	taskFilePath := filepath.Join(tasksDirectory, taskName+".toml")
	achievement, err := readAchievementFile(taskFilePath)
	if err != nil {
		return fmt.Errorf("reading task file: %w", err)
	}
	achievement.Status = "completed"
	achievementFilePath := filepath.Join(achievementsDirectory, taskName+".toml")
	if err := writeAchievementFile(achievementFilePath, achievement); err != nil {
		return fmt.Errorf("writing achievement file: %w", err)
	}
	if err := os.Remove(taskFilePath); err != nil {
		return fmt.Errorf("removing task file: %w", err)
	}
	return nil
}

func CleanExpiredTasks(directory string, ttl time.Duration) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading tasks directory: %w", err)
	}
	cutoff := time.Now().Add(-ttl)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		filePath := filepath.Join(directory, entry.Name())
		task, err := readAchievementFile(filePath)
		if err != nil {
			continue
		}
		if task.CreatedAt.Before(cutoff) {
			os.Remove(filePath)
		}
	}
	return nil
}
