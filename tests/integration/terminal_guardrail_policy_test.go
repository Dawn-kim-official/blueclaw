package integration

import (
	"strings"
	"testing"

	"blueclaw/internal/config"
	"blueclaw/internal/security"
)

func TestTerminalGuardrailAllowsWorkspaceCommand(t *testing.T) {
	workspaceRootPath := t.TempDir()
	terminalConfiguration := config.TerminalConfiguration{
		Mode:                   "native",
		WorkspaceRootPath:      workspaceRootPath,
		AllowedExecutableNames: []string{"echo"},
		DeniedExecutableNames:  []string{"sudo"},
		DeniedPathPrefixes:     []string{"/etc", "/root"},
		TimeoutSecond:          10,
		AllowNetwork:           true,
	}

	terminalSessionService := security.NewTerminalSessionService(terminalConfiguration)
	commandResult, errorValue := terminalSessionService.RunCommand(security.CommandRequest{
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

func TestTerminalGuardrailDeniesSystemModificationExecutable(t *testing.T) {
	workspaceRootPath := t.TempDir()
	commandGuardrailService := security.NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:                   "native",
		WorkspaceRootPath:      workspaceRootPath,
		AllowedExecutableNames: []string{"echo"},
		DeniedExecutableNames:  []string{"sudo"},
		DeniedPathPrefixes:     []string{"/etc", "/root"},
		TimeoutSecond:          10,
		AllowNetwork:           true,
	})

	_, errorValue := commandGuardrailService.BuildCommandPlan(security.CommandRequest{
		ExecutableName:       "sudo",
		Arguments:            []string{"echo", "x"},
		WorkingDirectoryPath: workspaceRootPath,
	})
	if errorValue == nil {
		t.Fatal("expected sudo to be denied")
	}
}

func TestTerminalGuardrailDeniesInlineEval(t *testing.T) {
	workspaceRootPath := t.TempDir()
	commandGuardrailService := security.NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:                   "native",
		WorkspaceRootPath:      workspaceRootPath,
		AllowedExecutableNames: []string{"python3"},
		DeniedExecutableNames:  []string{},
		DeniedPathPrefixes:     []string{"/etc", "/root"},
		TimeoutSecond:          10,
		AllowNetwork:           true,
	})

	_, errorValue := commandGuardrailService.BuildCommandPlan(security.CommandRequest{
		ExecutableName:       "python3",
		Arguments:            []string{"-c", "print('x')"},
		WorkingDirectoryPath: workspaceRootPath,
	})
	if errorValue == nil {
		t.Fatal("expected inline eval to be denied")
	}
}

func TestTerminalGuardrailDeniesWorkspaceEscape(t *testing.T) {
	workspaceRootPath := t.TempDir()
	commandGuardrailService := security.NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:                   "native",
		WorkspaceRootPath:      workspaceRootPath,
		AllowedExecutableNames: []string{"cat"},
		DeniedExecutableNames:  []string{},
		DeniedPathPrefixes:     []string{"/etc", "/root"},
		TimeoutSecond:          10,
		AllowNetwork:           true,
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
		Mode:                   "sandbox",
		SandboxProvider:        "firecracker",
		WorkspaceRootPath:      workspaceRootPath,
		AllowedExecutableNames: []string{"echo"},
		DeniedExecutableNames:  []string{},
		DeniedPathPrefixes:     []string{"/etc", "/root"},
		TimeoutSecond:          10,
		AllowNetwork:           false,
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
