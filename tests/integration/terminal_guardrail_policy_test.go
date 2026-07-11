package integration

import (
	"context"
	"strings"
	"testing"

	"blueclaw/internal/config"
	"blueclaw/internal/security"
)

func TestTerminalGuardrailAllowsWorkspaceCommand(t *testing.T) {
	workspaceRootPath := t.TempDir()
	terminalConfiguration := config.TerminalConfiguration{
		Mode:               "native",
		WorkspaceRootPath:  workspaceRootPath,
		DeniedPathPrefixes: []string{"/etc", "/root"},
		TimeoutSecond:      10,
		AllowNetwork:       true,
	}

	terminalSessionService := security.NewTerminalSessionService(terminalConfiguration)
	commandResult, errorValue := terminalSessionService.RunCommand(context.Background(), security.CommandRequest{
		ExecutableName:       "echo",
		Arguments:            []string{"blueclaw"},
		WorkingDirectoryPath: workspaceRootPath,
	})
	if errorValue != nil {
		t.Fatalf("expected terminal command to succeed: %v", errorValue)
	}
	if strings.TrimSpace(commandResult.Stdout) != "blueclaw" {
		t.Fatalf("expected stdout to match, got %q", commandResult.Stdout)
	}
}

func TestTerminalGuardrailAllowsInlineEval(t *testing.T) {
	workspaceRootPath := t.TempDir()
	commandGuardrailService := security.NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:               "native",
		WorkspaceRootPath:  workspaceRootPath,
		DeniedPathPrefixes: []string{"/etc", "/root"},
		TimeoutSecond:      10,
		AllowNetwork:       true,
	})

	_, errorValue := commandGuardrailService.BuildCommandPlan(security.CommandRequest{
		ExecutableName:       "python3",
		Arguments:            []string{"-c", "print('x')"},
		WorkingDirectoryPath: workspaceRootPath,
	})
	if errorValue != nil {
		t.Fatalf("expected inline eval to use the requester POSIX boundary: %v", errorValue)
	}
}

func TestTerminalGuardrailDeniesWorkspaceEscape(t *testing.T) {
	workspaceRootPath := t.TempDir()
	commandGuardrailService := security.NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:               "native",
		WorkspaceRootPath:  workspaceRootPath,
		DeniedPathPrefixes: []string{"/etc", "/root"},
		TimeoutSecond:      10,
		AllowNetwork:       true,
	})

	_, errorValue := commandGuardrailService.BuildCommandPlan(security.CommandRequest{
		ExecutableName:       "cat",
		Arguments:            []string{"/etc/passwd"},
		WorkingDirectoryPath: workspaceRootPath,
	})
	if errorValue == nil {
		t.Fatal("expected workspace escape to be denied")
	}
}

func TestTerminalGuardrailDeniesUnsupportedSandboxProvider(t *testing.T) {
	workspaceRootPath := t.TempDir()
	commandGuardrailService := security.NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:               "sandbox",
		SandboxProvider:    "firecracker",
		WorkspaceRootPath:  workspaceRootPath,
		DeniedPathPrefixes: []string{"/etc", "/root"},
		TimeoutSecond:      10,
		AllowNetwork:       false,
	})

	_, errorValue := commandGuardrailService.BuildCommandPlan(security.CommandRequest{
		ExecutableName:       "echo",
		Arguments:            []string{"blueclaw"},
		WorkingDirectoryPath: workspaceRootPath,
	})
	if errorValue == nil {
		t.Fatal("expected unsupported sandbox provider to be denied")
	}
}
