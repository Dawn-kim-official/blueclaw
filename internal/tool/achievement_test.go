package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestCreateAchievement(t *testing.T) {
	directory := t.TempDir()
	tool := NewCreateAchievementTool(directory)
	result, err := tool.Execute(context.Background(), map[string]any{
		"name":        "weather_mumbai",
		"description": "Find current weather in Mumbai",
		"eta_minutes": float64(3),
		"plan":        []any{"try curl", "try wget", "try python"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected tool error: %s", result.Error)
	}
	filePath := filepath.Join(directory, "weather_mumbai.toml")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatal("expected achievement file to be created")
	}
	var achievement Achievement
	if _, err := toml.DecodeFile(filePath, &achievement); err != nil {
		t.Fatalf("failed to decode achievement file: %v", err)
	}
	if achievement.Name != "weather_mumbai" {
		t.Errorf("expected name %q, got %q", "weather_mumbai", achievement.Name)
	}
	if achievement.Status != "in_progress" {
		t.Errorf("expected status %q, got %q", "in_progress", achievement.Status)
	}
	if len(achievement.Plan) != 3 {
		t.Errorf("expected 3 plan steps, got %d", len(achievement.Plan))
	}
	expectedETA := achievement.CreatedAt.Add(3 * time.Minute)
	if achievement.ETA.Sub(expectedETA) > time.Second {
		t.Errorf("ETA not approximately created_at + 3 minutes")
	}
}

func TestCreateAchievementMissingName(t *testing.T) {
	directory := t.TempDir()
	tool := NewCreateAchievementTool(directory)
	result, err := tool.Execute(context.Background(), map[string]any{
		"description": "some task",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected error for missing name")
	}
}

func TestUpdateAchievementAppendAttempt(t *testing.T) {
	directory := t.TempDir()
	createTool := NewCreateAchievementTool(directory)
	_, err := createTool.Execute(context.Background(), map[string]any{
		"name":        "test_task",
		"description": "A test task",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	updateTool := NewUpdateAchievementTool(directory)
	result, err := updateTool.Execute(context.Background(), map[string]any{
		"name":           "test_task",
		"attempt_method": "curl wttr.in",
		"attempt_result": "connection refused",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected tool error: %s", result.Error)
	}
	filePath := filepath.Join(directory, "test_task.toml")
	var achievement Achievement
	if _, err := toml.DecodeFile(filePath, &achievement); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(achievement.Attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(achievement.Attempts))
	}
	if achievement.Attempts[0].Method != "curl wttr.in" {
		t.Errorf("expected method %q, got %q", "curl wttr.in", achievement.Attempts[0].Method)
	}
}

func TestUpdateAchievementStatusChange(t *testing.T) {
	directory := t.TempDir()
	createTool := NewCreateAchievementTool(directory)
	_, err := createTool.Execute(context.Background(), map[string]any{
		"name":        "status_task",
		"description": "Testing status update",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	updateTool := NewUpdateAchievementTool(directory)
	result, err := updateTool.Execute(context.Background(), map[string]any{
		"name":           "status_task",
		"status":         "failed",
		"failure_reason": "no network tools available",
		"user_ask":       "please install curl",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected tool error: %s", result.Error)
	}
	filePath := filepath.Join(directory, "status_task.toml")
	var achievement Achievement
	if _, err := toml.DecodeFile(filePath, &achievement); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if achievement.Status != "failed" {
		t.Errorf("expected status %q, got %q", "failed", achievement.Status)
	}
	if achievement.FailureReason != "no network tools available" {
		t.Errorf("unexpected failure_reason: %q", achievement.FailureReason)
	}
	if achievement.UserAsk != "please install curl" {
		t.Errorf("unexpected user_ask: %q", achievement.UserAsk)
	}
}

func TestUpdateAchievementNonExistentReturnsError(t *testing.T) {
	directory := t.TempDir()
	updateTool := NewUpdateAchievementTool(directory)
	result, err := updateTool.Execute(context.Background(), map[string]any{
		"name": "nonexistent",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected tool error for non-existent achievement")
	}
}

func TestCleanExpiredAchievements(t *testing.T) {
	directory := t.TempDir()
	oldAchievement := Achievement{
		Name:        "old",
		Description: "old task",
		CreatedAt:   time.Now().Add(-8 * 24 * time.Hour),
		ETA:         time.Now().Add(-8 * 24 * time.Hour),
		Status:      "completed",
	}
	recentAchievement := Achievement{
		Name:        "recent",
		Description: "recent task",
		CreatedAt:   time.Now().Add(-1 * time.Hour),
		ETA:         time.Now().Add(1 * time.Hour),
		Status:      "in_progress",
	}
	if err := writeAchievementFile(filepath.Join(directory, "old.toml"), oldAchievement); err != nil {
		t.Fatalf("failed to write old: %v", err)
	}
	if err := writeAchievementFile(filepath.Join(directory, "recent.toml"), recentAchievement); err != nil {
		t.Fatalf("failed to write recent: %v", err)
	}
	if err := CleanExpiredAchievements(directory, 7*24*time.Hour); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "old.toml")); !os.IsNotExist(err) {
		t.Error("expected old.toml to be deleted")
	}
	if _, err := os.Stat(filepath.Join(directory, "recent.toml")); os.IsNotExist(err) {
		t.Error("expected recent.toml to still exist")
	}
}

func TestCleanExpiredAchievementsNonExistentDirectory(t *testing.T) {
	err := CleanExpiredAchievements("/nonexistent/path", 7*24*time.Hour)
	if err != nil {
		t.Errorf("expected no error for non-existent directory, got: %v", err)
	}
}
