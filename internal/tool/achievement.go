package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Achievement struct {
	Name          string               `toml:"name"`
	Description   string               `toml:"description"`
	CreatedAt     time.Time            `toml:"createdAt"`
	ETA           time.Time            `toml:"eta"`
	Status        string               `toml:"status"`
	Plan          []string             `toml:"plan"`
	Attempts      []AchievementAttempt `toml:"attempts"`
	FailureReason string               `toml:"failureReason,omitempty"`
	UserAsk       string               `toml:"userAsk,omitempty"`
}

type AchievementAttempt struct {
	Method string    `toml:"method"`
	Result string    `toml:"result"`
	Time   time.Time `toml:"time"`
}

type CreateAchievementTool struct {
	achievementsDirectory string
}

func NewCreateAchievementTool(achievementsDirectory string) *CreateAchievementTool {
	return &CreateAchievementTool{achievementsDirectory: achievementsDirectory}
}

func (t *CreateAchievementTool) Name() string        { return "create_achievement" }
func (t *CreateAchievementTool) Description() string {
	return "Create an achievement to track a multi-step task. Call this before starting work on tasks requiring multiple attempts."
}
func (t *CreateAchievementTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "description": "Unique identifier for the achievement (slug format)"},
			"description": map[string]any{"type": "string", "description": "What needs to be accomplished"},
			"eta_minutes": map[string]any{"type": "integer", "description": "Estimated time in minutes (default 5)"},
			"plan":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Ordered list of steps to accomplish the task"},
		},
		"required": []string{"name", "description"},
	}
}

func (t *CreateAchievementTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
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
	achievement := Achievement{
		Name:        name,
		Description: description,
		CreatedAt:   now,
		ETA:         now.Add(time.Duration(etaMinutes) * time.Minute),
		Status:      "in_progress",
		Plan:        plan,
		Attempts:    []AchievementAttempt{},
	}
	filePath := filepath.Join(t.achievementsDirectory, name+".toml")
	if err := writeAchievementFile(filePath, achievement); err != nil {
		return Result{Error: err.Error()}, nil
	}
	return Result{Output: fmt.Sprintf("achievement created: %s", filePath)}, nil
}

type UpdateAchievementTool struct {
	achievementsDirectory string
}

func NewUpdateAchievementTool(achievementsDirectory string) *UpdateAchievementTool {
	return &UpdateAchievementTool{achievementsDirectory: achievementsDirectory}
}

func (t *UpdateAchievementTool) Name() string        { return "update_achievement" }
func (t *UpdateAchievementTool) Description() string {
	return "Update an achievement with a new attempt, status change, or failure details."
}
func (t *UpdateAchievementTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":           map[string]any{"type": "string", "description": "Achievement name to update"},
			"attempt_method": map[string]any{"type": "string", "description": "Method used in this attempt"},
			"attempt_result": map[string]any{"type": "string", "description": "Result of this attempt"},
			"status":         map[string]any{"type": "string", "description": "New status: in_progress, completed, or failed"},
			"failure_reason": map[string]any{"type": "string", "description": "Why the achievement failed"},
			"user_ask":       map[string]any{"type": "string", "description": "What is needed from the user to proceed"},
		},
		"required": []string{"name"},
	}
}

func (t *UpdateAchievementTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
	name, _ := arguments["name"].(string)
	if name == "" {
		return Result{Error: "name is required"}, nil
	}
	filePath := filepath.Join(t.achievementsDirectory, name+".toml")
	achievement, err := readAchievementFile(filePath)
	if err != nil {
		return Result{Error: fmt.Sprintf("achievement %q not found: %v", name, err)}, nil
	}
	attemptMethod, _ := arguments["attempt_method"].(string)
	attemptResult, _ := arguments["attempt_result"].(string)
	if attemptMethod != "" || attemptResult != "" {
		achievement.Attempts = append(achievement.Attempts, AchievementAttempt{
			Method: attemptMethod,
			Result: attemptResult,
			Time:   time.Now(),
		})
	}
	if status, ok := arguments["status"].(string); ok && status != "" {
		achievement.Status = status
	}
	if failureReason, ok := arguments["failure_reason"].(string); ok && failureReason != "" {
		achievement.FailureReason = failureReason
	}
	if userAsk, ok := arguments["user_ask"].(string); ok && userAsk != "" {
		achievement.UserAsk = userAsk
	}
	if err := writeAchievementFile(filePath, achievement); err != nil {
		return Result{Error: err.Error()}, nil
	}
	return Result{Output: fmt.Sprintf("achievement updated: %s", filePath)}, nil
}

func CleanExpiredAchievements(directory string, ttl time.Duration) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading achievements directory: %w", err)
	}
	cutoff := time.Now().Add(-ttl)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		filePath := filepath.Join(directory, entry.Name())
		achievement, err := readAchievementFile(filePath)
		if err != nil {
			continue
		}
		if achievement.CreatedAt.Before(cutoff) {
			os.Remove(filePath)
		}
	}
	return nil
}

func writeAchievementFile(filePath string, achievement Achievement) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("creating achievements directory: %w", err)
	}
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("creating achievement file: %w", err)
	}
	defer file.Close()
	if err := toml.NewEncoder(file).Encode(achievement); err != nil {
		return fmt.Errorf("encoding achievement: %w", err)
	}
	return nil
}

func readAchievementFile(filePath string) (Achievement, error) {
	var achievement Achievement
	if _, err := toml.DecodeFile(filePath, &achievement); err != nil {
		return Achievement{}, fmt.Errorf("decoding achievement file: %w", err)
	}
	return achievement, nil
}
