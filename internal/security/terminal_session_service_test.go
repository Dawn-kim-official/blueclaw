package security

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/config"
	"blueclaw/internal/hooks"
)

func TestRunCommandUsesBashStdinInFirecrackerGuestMode(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))

	commandResult, errorValue := terminalSessionService.RunCommand(CommandRequest{
		Command: "printf 'blueclaw\\n'",
	})

	if errorValue != nil {
		t.Fatalf("expected command to succeed: %v", errorValue)
	}
	if commandResult.Stdout != "blueclaw\n" || commandResult.ExitCode != 0 {
		t.Fatalf("expected bash stdout and exit code, got %+v", commandResult)
	}
}

func TestRunCommandUsesBuiltInRTKHook(t *testing.T) {
	terminalConfiguration := testTerminalConfiguration(t)
	terminalSessionService := newTerminalSessionService(
		terminalConfiguration,
		fakeRewriteExecutable(t, "printf original", "printf rewritten"),
	)

	commandResult, errorValue := terminalSessionService.RunCommand(CommandRequest{
		Command: "printf original",
	})

	if errorValue != nil {
		t.Fatalf("expected rewritten command to succeed: %v", errorValue)
	}
	if commandResult.Stdout != "rewritten" {
		t.Fatalf("expected rewritten output, got %+v", commandResult)
	}
}

func TestRunCommandFallsBackWhenRTKHookFails(t *testing.T) {
	terminalConfiguration := testTerminalConfiguration(t)
	terminalSessionService := newTerminalSessionService(
		terminalConfiguration,
		fakeRewriteExecutable(t, "git status", "rtk git status"),
	)

	commandResult, errorValue := terminalSessionService.RunCommand(CommandRequest{
		Command: "printf original",
	})

	if errorValue != nil {
		t.Fatalf("expected fallback command to succeed: %v", errorValue)
	}
	if commandResult.Stdout != "original" {
		t.Fatalf("expected original output after rewrite fallback, got %+v", commandResult)
	}
}

func TestRunCommandFallsBackWhenRTKHookIsMissing(t *testing.T) {
	terminalSessionService := newTerminalSessionService(testTerminalConfiguration(t), "/missing/rtk")

	commandResult, errorValue := terminalSessionService.RunCommand(CommandRequest{
		Command: "printf original",
	})

	if errorValue != nil {
		t.Fatalf("expected fallback command to succeed: %v", errorValue)
	}
	if commandResult.Stdout != "original" {
		t.Fatalf("expected original output after missing RTK fallback, got %+v", commandResult)
	}
}

func TestRunCommandFallsBackWhenRTKHookReturnsUnchangedCommand(t *testing.T) {
	terminalSessionService := newTerminalSessionService(
		testTerminalConfiguration(t),
		fakeRewriteExecutable(t, "printf original", "printf original"),
	)

	commandResult, errorValue := terminalSessionService.RunCommand(CommandRequest{
		Command: "printf original",
	})

	if errorValue != nil {
		t.Fatalf("expected unchanged command to succeed: %v", errorValue)
	}
	if commandResult.Stdout != "original" {
		t.Fatalf("expected original output after unchanged RTK fallback, got %+v", commandResult)
	}
}

func TestRunCommandFallsBackWhenRTKHookTimesOut(t *testing.T) {
	terminalSessionService := newTerminalSessionService(testTerminalConfiguration(t), fakeSlowRewriteExecutable(t))

	commandResult, errorValue := terminalSessionService.RunCommand(CommandRequest{
		Command: "printf original",
	})

	if errorValue != nil {
		t.Fatalf("expected timeout fallback command to succeed: %v", errorValue)
	}
	if commandResult.Stdout != "original" {
		t.Fatalf("expected original output after RTK timeout, got %+v", commandResult)
	}
}

func TestRTKHookSkipsPTYCommand(t *testing.T) {
	markerPath := filepath.Join(t.TempDir(), "called")
	rtkHook := newRTKRewriteHook(fakeMarkedRewriteExecutable(t, markerPath))

	result, errorValue := rtkHook(context.Background(), hooks.HookRequest{
		EventName: hooks.EventPreToolUse,
		ToolName:  terminalRunToolName,
		ToolInput: CommandRequest{
			Command: "printf original",
			IsPTY:   true,
		},
	})

	if errorValue != nil {
		t.Fatalf("expected PTY hook skip to succeed: %v", errorValue)
	}
	if result.UpdatedToolInput != nil {
		t.Fatalf("expected PTY command to skip RTK, got %+v", result)
	}
	if _, errorValue := os.Stat(markerPath); errorValue == nil {
		t.Fatal("expected RTK executable not to be called for PTY command")
	}
}

func TestRTKHookSkipsInteractiveCommand(t *testing.T) {
	markerPath := filepath.Join(t.TempDir(), "called")
	rtkHook := newRTKRewriteHook(fakeMarkedRewriteExecutable(t, markerPath))

	result, errorValue := rtkHook(context.Background(), hooks.HookRequest{
		EventName: hooks.EventPreToolUse,
		ToolName:  terminalRunToolName,
		ToolInput: CommandRequest{
			Command:       "printf original",
			IsInteractive: true,
		},
	})

	if errorValue != nil {
		t.Fatalf("expected interactive hook skip to succeed: %v", errorValue)
	}
	if result.UpdatedToolInput != nil {
		t.Fatalf("expected interactive command to skip RTK, got %+v", result)
	}
	if _, errorValue := os.Stat(markerPath); errorValue == nil {
		t.Fatal("expected RTK executable not to be called for interactive command")
	}
}

func TestRunCommandValidatesRewrittenCommandWithGuardrails(t *testing.T) {
	terminalSessionService := newTerminalSessionService(
		testTerminalConfiguration(t),
		fakeRewriteExecutable(t, "printf original", "cat /etc/passwd"),
	)

	commandResult, errorValue := terminalSessionService.RunCommand(CommandRequest{
		Command: "printf original",
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "escapes workspace root") {
		t.Fatalf("expected rewritten command guardrail denial, got %v", errorValue)
	}
	if !strings.Contains(commandResult.Stderr, "escapes workspace root") {
		t.Fatalf("expected command result to include guardrail error, got %+v", commandResult)
	}
}

func TestRunCommandPreparesMissingWorkspaceWorkingDirectoryInFirecrackerGuestMode(t *testing.T) {
	terminalConfiguration := testTerminalConfiguration(t)
	terminalSessionService := NewTerminalSessionService(terminalConfiguration)
	workingDirectoryPath := terminalConfiguration.WorkspaceRootPath + "/.blueclaw/tmp/slides"

	commandResult, errorValue := terminalSessionService.RunCommand(CommandRequest{
		Command:              "printf 'ready' > result.txt",
		WorkingDirectoryPath: workingDirectoryPath,
	})

	if errorValue != nil {
		t.Fatalf("expected command to succeed: %v result=%+v", errorValue, commandResult)
	}
	if commandResult.ExitCode != 0 {
		t.Fatalf("expected successful exit code, got %+v", commandResult)
	}
	content, errorValue := os.ReadFile(workingDirectoryPath + "/result.txt")
	if errorValue != nil || string(content) != "ready" {
		t.Fatalf("expected command to write inside prepared working directory, content=%q error=%v", string(content), errorValue)
	}
}

func TestRunCommandDeniesWorkspaceEscape(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))

	commandResult, errorValue := terminalSessionService.RunCommand(CommandRequest{
		Command: "cat /etc/passwd",
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "escapes workspace root") {
		t.Fatalf("expected workspace escape denial, got %v", errorValue)
	}
	if !strings.Contains(commandResult.Stderr, "escapes workspace root") {
		t.Fatalf("expected command result to include guardrail error, got %+v", commandResult)
	}
}

func TestRunCommandDeniesExecutableFloor(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))

	_, errorValue := terminalSessionService.RunCommand(CommandRequest{
		Command: "sudo echo nope",
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "denied executable") {
		t.Fatalf("expected denied executable, got %v", errorValue)
	}
}

func TestRunCommandReportsTimeout(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))

	commandResult, errorValue := terminalSessionService.RunCommand(CommandRequest{
		Command:       "sleep 2",
		TimeoutSecond: 1,
	})

	if errorValue == nil || !commandResult.TimedOut {
		t.Fatalf("expected timed out command result, got result=%+v error=%v", commandResult, errorValue)
	}
}

func TestRunCommandIncludesProcessErrorWhenStderrIsEmpty(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))

	commandResult, errorValue := terminalSessionService.RunCommand(CommandRequest{
		Command: "definitely_missing_blueclaw_binary",
	})

	if errorValue == nil {
		t.Fatal("expected command error")
	}
	if strings.TrimSpace(commandResult.Stderr) == "" {
		t.Fatalf("expected stderr to explain the process error, got %+v", commandResult)
	}
}

func TestTerminalSessionUsesPTYLifecycle(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))
	workspaceRootPath := terminalSessionService.commandGuardrailService.terminalConfiguration.WorkspaceRootPath

	sessionID, errorValue := terminalSessionService.StartInteractiveSession(CommandRequest{
		WorkingDirectoryPath: workspaceRootPath,
		IsPTY:                true,
	})
	if errorValue != nil {
		t.Fatalf("expected PTY session to start: %v", errorValue)
	}
	defer terminalSessionService.CloseSession(sessionID)

	_, errorValue = terminalSessionService.WriteSessionInput(sessionID, "printf 'hello-pty\\n'\n")
	if errorValue != nil {
		t.Fatalf("expected PTY write to succeed: %v", errorValue)
	}

	var sessionStatus TerminalSessionStatus
	for range 20 {
		sessionStatus, errorValue = terminalSessionService.StatusSession(sessionID)
		if errorValue != nil {
			t.Fatalf("expected PTY status: %v", errorValue)
		}
		if strings.Contains(sessionStatus.RecentOutput, "hello-pty") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if sessionStatus.SessionID != sessionID || sessionStatus.Status != "running" {
		t.Fatalf("expected running PTY session, got %+v", sessionStatus)
	}
	if !strings.Contains(sessionStatus.RecentOutput, "hello-pty") {
		t.Fatalf("expected recent PTY output, got %+v", sessionStatus)
	}
}

func TestTerminalSessionLimit(t *testing.T) {
	terminalConfiguration := testTerminalConfiguration(t)
	terminalConfiguration.SessionMaxCount = 1
	terminalSessionService := NewTerminalSessionService(terminalConfiguration)
	workspaceRootPath := terminalConfiguration.WorkspaceRootPath

	sessionID, errorValue := terminalSessionService.StartInteractiveSession(CommandRequest{
		WorkingDirectoryPath: workspaceRootPath,
		IsPTY:                true,
	})
	if errorValue != nil {
		t.Fatalf("expected first session to start: %v", errorValue)
	}
	defer terminalSessionService.CloseSession(sessionID)

	_, errorValue = terminalSessionService.StartInteractiveSession(CommandRequest{
		WorkingDirectoryPath: workspaceRootPath,
		IsPTY:                true,
	})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "session limit") {
		t.Fatalf("expected session cap error, got %v", errorValue)
	}
}

func fakeRewriteExecutable(t *testing.T, originalCommand string, rewrittenCommand string) string {
	t.Helper()
	executablePath := t.TempDir() + "/fake-rtk"
	script := "#!/bin/sh\nif [ \"$1\" = \"rewrite\" ] && [ \"$2\" = " + shellSingleQuote(originalCommand) + " ]; then\n  printf '%s\\n' " + shellSingleQuote(rewrittenCommand) + "\n  exit 0\nfi\nexit 1\n"
	if errorValue := os.WriteFile(executablePath, []byte(script), 0o755); errorValue != nil {
		t.Fatalf("expected fake rewrite executable to be written: %v", errorValue)
	}
	return executablePath
}

func fakeSlowRewriteExecutable(t *testing.T) string {
	t.Helper()
	executablePath := t.TempDir() + "/fake-slow-rtk"
	script := "#!/bin/sh\nsleep 2\nprintf '%s\\n' 'printf rewritten'\n"
	if errorValue := os.WriteFile(executablePath, []byte(script), 0o755); errorValue != nil {
		t.Fatalf("expected fake slow rewrite executable to be written: %v", errorValue)
	}
	return executablePath
}

func fakeMarkedRewriteExecutable(t *testing.T, markerPath string) string {
	t.Helper()
	executablePath := t.TempDir() + "/fake-marked-rtk"
	script := "#!/bin/sh\ntouch " + shellSingleQuote(markerPath) + "\nprintf '%s\\n' 'printf rewritten'\n"
	if errorValue := os.WriteFile(executablePath, []byte(script), 0o755); errorValue != nil {
		t.Fatalf("expected fake marked rewrite executable to be written: %v", errorValue)
	}
	return executablePath
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func testTerminalConfiguration(t *testing.T) config.TerminalConfiguration {
	t.Helper()
	workspaceRootPath := t.TempDir()
	return config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspaceRootPath,
		DeniedExecutableNames: []string{"sudo", "su", "mount"},
		DeniedPathPrefixes:    []string{"/etc", "/root", "/var/run/docker.sock"},
		AllowNetwork:          true,
		AllowInteractiveShell: true,
		TimeoutSecond:         3,
		OutputMaxBytes:        1024,
		SessionMaxCount:       4,
	}
}
