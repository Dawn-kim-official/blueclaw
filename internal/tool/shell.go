package tool

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

const (
	defaultShellTimeout   = 30 * time.Second
	maxShellOutputBytes   = 10000
)

type ShellTool struct{}

func NewShellTool() *ShellTool { return &ShellTool{} }

func (tool *ShellTool) Name() string { return "shell" }
func (tool *ShellTool) Description() string {
	return "Execute a shell command in the workspace environment. Working directory is /workspace."
}
func (tool *ShellTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (default 30, max 120)",
			},
		},
		"required": []string{"command"},
	}
}

func (tool *ShellTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
	command, _ := arguments["command"].(string)
	if command == "" {
		return Result{Error: "command is required"}, nil
	}
	timeout := defaultShellTimeout
	if seconds, ok := arguments["timeout_seconds"].(float64); ok && seconds > 0 {
		if seconds > 120 {
			seconds = 120
		}
		timeout = time.Duration(seconds) * time.Second
	}
	timeoutContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(timeoutContext, "bash", "-c", command)
	cmd.Dir = "/workspace"
	var outputBuffer bytes.Buffer
	cmd.Stdout = &outputBuffer
	cmd.Stderr = &outputBuffer
	runError := cmd.Run()
	output := outputBuffer.String()
	if len(output) > maxShellOutputBytes {
		output = output[:maxShellOutputBytes] + fmt.Sprintf("\n... (truncated at %d bytes)", maxShellOutputBytes)
	}
	if runError != nil {
		if timeoutContext.Err() != nil {
			return Result{Error: fmt.Sprintf("command timed out after %s\n%s", timeout, output)}, nil
		}
		if output != "" {
			return Result{Error: fmt.Sprintf("exit error: %v\n%s", runError, output)}, nil
		}
		return Result{Error: fmt.Sprintf("exit error: %v", runError)}, nil
	}
	return Result{Output: output}, nil
}
