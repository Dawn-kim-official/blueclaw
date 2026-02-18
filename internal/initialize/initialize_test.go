package initialize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCreatesAllDirectories(t *testing.T) {
	temporaryDirectory := t.TempDir()
	if err := Run(temporaryDirectory, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, directory := range requiredDirectories {
		fullPath := filepath.Join(temporaryDirectory, directory)
		info, err := os.Stat(fullPath)
		if err != nil {
			t.Errorf("directory %s was not created: %v", directory, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s exists but is not a directory", directory)
		}
	}
}

func TestRunCreatesConfigTOML(t *testing.T) {
	temporaryDirectory := t.TempDir()
	if err := Run(temporaryDirectory, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	configPath := filepath.Join(temporaryDirectory, "config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.toml was not created: %v", err)
	}
	if !strings.Contains(string(content), `llmProvider = "anthropic"`) {
		t.Error("config.toml missing llmProvider default")
	}
	if !strings.Contains(string(content), "containerRuntime") {
		t.Error("config.toml missing containerRuntime")
	}
	if !strings.Contains(string(content), "apiPort = 8080") {
		t.Error("config.toml missing apiPort default")
	}
	if !strings.Contains(string(content), `achievementTTL = "168h"`) {
		t.Error("config.toml missing achievementTTL")
	}
}

func TestRunCreatesSoulMarkdown(t *testing.T) {
	temporaryDirectory := t.TempDir()
	if err := Run(temporaryDirectory, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	soulPath := filepath.Join(temporaryDirectory, "SOUL.md")
	content, err := os.ReadFile(soulPath)
	if err != nil {
		t.Fatalf("SOUL.md was not created: %v", err)
	}
	if !strings.Contains(string(content), "Blueclaw") {
		t.Error("SOUL.md missing Blueclaw name")
	}
	if !strings.Contains(string(content), "update_task") {
		t.Error("SOUL.md missing update_task instruction")
	}
}

func TestRunCreatesHeartbeatMarkdown(t *testing.T) {
	temporaryDirectory := t.TempDir()
	if err := Run(temporaryDirectory, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	heartbeatPath := filepath.Join(temporaryDirectory, "HEARTBEAT.md")
	if _, err := os.Stat(heartbeatPath); err != nil {
		t.Fatalf("HEARTBEAT.md was not created: %v", err)
	}
}

func TestRunIsIdempotent(t *testing.T) {
	temporaryDirectory := t.TempDir()
	if err := Run(temporaryDirectory, false); err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	configPath := filepath.Join(temporaryDirectory, "config.toml")
	customContent := `llmProvider = "openai"` + "\n"
	if err := os.WriteFile(configPath, []byte(customContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Run(temporaryDirectory, false); err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != customContent {
		t.Error("second run overwrote existing config.toml")
	}
}

func TestRunResetOverwritesFiles(t *testing.T) {
	temporaryDirectory := t.TempDir()
	if err := Run(temporaryDirectory, false); err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	configPath := filepath.Join(temporaryDirectory, "config.toml")
	customContent := `llmProvider = "openai"` + "\n"
	if err := os.WriteFile(configPath, []byte(customContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Run(temporaryDirectory, true); err != nil {
		t.Fatalf("reset run failed: %v", err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) == customContent {
		t.Error("reset run did not overwrite config.toml")
	}
	if !strings.Contains(string(content), `llmProvider = "anthropic"`) {
		t.Error("reset config.toml missing default llmProvider")
	}
}
