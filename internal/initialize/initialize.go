package initialize

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func defaultConfigTOML() string {
	return fmt.Sprintf(`llmProvider = "anthropic"
containerRuntime = "%s"
containerImage = "blueclaw:latest"
containerNetwork = "bridge"
apiPort = 8080
heartbeatInterval = "30m"
minHeartbeatInterval = "1m"
maxHeartbeatInterval = "4h"
memoryTopK = 5
embeddingPort = 8990
achievementTTL = "168h"
`, detectContainerRuntime())
}

func detectContainerRuntime() string {
	if _, err := exec.LookPath("container"); err == nil {
		return "apple"
	}
	if _, err := exec.LookPath("docker"); err == nil {
		return "docker"
	}
	return "docker"
}

const defaultSoulMarkdown = `You are Blueclaw — a personal AI assistant running on this machine.

Always respond in the same language the user writes in.

Be direct. Skip filler phrases like "Great question!" or "Happy to help!" — just help. Have opinions and share them. If something is a bad idea, say so. If you don't know, say so clearly without hedging.

Use the remember and recall tools to retain important things across sessions. When you notice something worth saving, save it without being asked. When you schedule something, follow through.

During heartbeat check-ins, be brief and specific. Surface only what's genuinely useful. Don't generate filler updates.

When working on a multi-step task (a task file will be created for you before you start):
1. Before each attempt, check available tools: ` + "`" + `which curl wget python3 python node nc ncat` + "`" + `
2. After each attempt, call update_task with the method tried and result.
3. Never give up after 1–3 failed attempts — exhaust all reasonable alternatives.
4. On success: call update_task with status="completed".
5. If all approaches fail: call update_task with status="failed", failure_reason, and user_ask. Then explain what you tried and what you need from the user.
`

const defaultHeartbeatMarkdown = `Use recall to check for anything with upcoming deadlines or pending follow-ups. If there's something specific and actionable, surface it in 1-2 sentences.
`

var requiredDirectories = []string{
	"sessions",
	"cron",
	"outbox",
	"public",
	"public/achievements",
	"public/tasks",
	"models",
}

func Run(blueclawDirectory string, reset bool) error {
	if err := createDirectories(blueclawDirectory); err != nil {
		return err
	}
	writeFile := writeFileIfNotExists
	if reset {
		writeFile = writeFileAlways
	}
	if err := writeFile(filepath.Join(blueclawDirectory, "config.toml"), defaultConfigTOML()); err != nil {
		return fmt.Errorf("writing config.toml: %w", err)
	}
	if err := writeFile(filepath.Join(blueclawDirectory, "SOUL.md"), defaultSoulMarkdown); err != nil {
		return fmt.Errorf("writing SOUL.md: %w", err)
	}
	if err := writeFile(filepath.Join(blueclawDirectory, "HEARTBEAT.md"), defaultHeartbeatMarkdown); err != nil {
		return fmt.Errorf("writing HEARTBEAT.md: %w", err)
	}
	return nil
}

func ResetSoul(blueclawDirectory string) error {
	if err := createDirectories(blueclawDirectory); err != nil {
		return err
	}
	return writeFileAlways(filepath.Join(blueclawDirectory, "SOUL.md"), defaultSoulMarkdown)
}

func ResetHeartbeat(blueclawDirectory string) error {
	if err := createDirectories(blueclawDirectory); err != nil {
		return err
	}
	return writeFileAlways(filepath.Join(blueclawDirectory, "HEARTBEAT.md"), defaultHeartbeatMarkdown)
}

func createDirectories(blueclawDirectory string) error {
	for _, directory := range requiredDirectories {
		fullPath := filepath.Join(blueclawDirectory, directory)
		if err := os.MkdirAll(fullPath, 0755); err != nil {
			return fmt.Errorf("creating directory %s: %w", fullPath, err)
		}
	}
	return nil
}

func writeFileIfNotExists(filePath string, content string) error {
	if _, err := os.Stat(filePath); err == nil {
		return nil
	}
	return os.WriteFile(filePath, []byte(content), 0644)
}

func writeFileAlways(filePath string, content string) error {
	return os.WriteFile(filePath, []byte(content), 0644)
}
