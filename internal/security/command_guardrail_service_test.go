package security

import (
	"errors"
	"os"
	"os/user"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/config"
)

func TestCommandPlanUsesPOSIXHelperForExecutionIdentity(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root cannot build terminal command plans")
	}

	currentUser, errorValue := user.Current()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	currentGroup, errorValue := user.LookupGroupId(currentUser.Gid)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	workspaceRootPath := t.TempDir()
	commandGuardrailService := NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspaceRootPath,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
		POSIXHelperPath:       "/usr/local/bin/blueclaw-posix-helper",
		TimeoutSecond:         3,
	})

	commandPlan, errorValue := commandGuardrailService.BuildCommandPlan(CommandRequest{
		Command: "printf ready",
		ExecutionIdentity: ExecutionIdentity{
			UserName:          currentUser.Username,
			GroupName:         currentGroup.Name,
			HomeDirectoryPath: workspaceRootPath,
		},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if commandPlan.ExecutablePath != "/usr/local/bin/blueclaw-posix-helper" {
		t.Fatalf("expected POSIX helper executable, got %+v", commandPlan)
	}
	if len(commandPlan.Arguments) < 12 || commandPlan.Arguments[0] != "exec" {
		t.Fatalf("expected POSIX helper arguments, got %+v", commandPlan.Arguments)
	}
	if !strings.Contains(strings.Join(commandPlan.Arguments, " "), "--cwd "+workspaceRootPath) {
		t.Fatalf("expected helper cwd argument, got %+v", commandPlan.Arguments)
	}
	if commandPlan.WorkingDirectoryPath != workspaceRootPath {
		t.Fatalf("expected helper process to start from workspace root, got %+v", commandPlan)
	}
	if commandPlan.EnvironmentVariables["HOME"] != workspaceRootPath {
		t.Fatalf("expected POSIX HOME environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.EnvironmentVariables["BLUECLAW_REQUESTER_TMP"] != workspaceRootPath+"/tmp" {
		t.Fatalf("expected requester tmp environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.EnvironmentVariables["BLUECLAW_TASK_TMP"] != workspaceRootPath+"/tmp" {
		t.Fatalf("expected task tmp environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.EnvironmentVariables["BLUECLAW_REQUESTER_ARTIFACTS"] != workspaceRootPath+"/artifacts" {
		t.Fatalf("expected requester artifacts environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.EnvironmentVariables["TMPDIR"] != workspaceRootPath+"/tmp/.runtime/tmp" {
		t.Fatalf("expected requester runtime tmp environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.EnvironmentVariables["BUN_TMPDIR"] != workspaceRootPath+"/tmp/.runtime/bun/tmp" {
		t.Fatalf("expected requester Bun tmp environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.EnvironmentVariables["BUN_INSTALL"] != workspaceRootPath+"/tmp/.runtime/bun/install" {
		t.Fatalf("expected requester Bun install environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.Timeout != 3*time.Second {
		t.Fatalf("expected timeout to survive POSIX wrapping, got %+v", commandPlan.Timeout)
	}
}

func TestCommandPlanKeepsPrivateCWDInsideHelperArguments(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root cannot build terminal command plans")
	}

	currentUser, errorValue := user.Current()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	currentGroup, errorValue := user.LookupGroupId(currentUser.Gid)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	workspaceRootPath := t.TempDir()
	privateWorkingDirectoryPath := workspaceRootPath + "/private/people/person-1/tmp/task/deck"
	commandGuardrailService := NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspaceRootPath,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
		POSIXHelperPath:       "/usr/local/bin/blueclaw-posix-helper",
		TimeoutSecond:         3,
	})

	commandPlan, errorValue := commandGuardrailService.BuildCommandPlan(CommandRequest{
		Command:              "pwd",
		WorkingDirectoryPath: privateWorkingDirectoryPath,
		ExecutionIdentity: ExecutionIdentity{
			UserName:          currentUser.Username,
			GroupName:         currentGroup.Name,
			HomeDirectoryPath: workspaceRootPath,
		},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if commandPlan.WorkingDirectoryPath != workspaceRootPath {
		t.Fatalf("expected helper process cwd to avoid requester-private path, got %+v", commandPlan)
	}
	if !strings.Contains(strings.Join(commandPlan.Arguments, " "), "--cwd "+privateWorkingDirectoryPath) {
		t.Fatalf("expected requester cwd only in helper arguments, got %+v", commandPlan.Arguments)
	}
}

func TestCommandPathGuardrailErrorIncludesRecoveryDetails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root cannot build terminal command plans")
	}

	workspaceRootPath := t.TempDir()
	commandGuardrailService := NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspaceRootPath,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
		TimeoutSecond:         3,
	})

	_, errorValue := commandGuardrailService.BuildCommandPlan(CommandRequest{
		Command:              "/opt/blueclaw/builtin-skills-venv/bin/python --version",
		WorkingDirectoryPath: workspaceRootPath,
	})

	var commandGuardrailError CommandGuardrailError
	if !errors.As(errorValue, &commandGuardrailError) {
		t.Fatalf("expected command guardrail error, got %v", errorValue)
	}
	for _, expectedText := range []string{
		"command path escapes workspace root",
		"/opt/blueclaw/builtin-skills-venv/bin/python",
		workspaceRootPath,
		"/workspace/skills/<skill>/scripts/skill_runtime.py",
	} {
		if !strings.Contains(commandGuardrailError.Error(), expectedText) {
			t.Fatalf("expected guardrail error to contain %q, got %q", expectedText, commandGuardrailError.Error())
		}
	}
}

func TestCommandPathGuardrailIgnoresHereDocumentContentPaths(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root cannot build terminal command plans")
	}

	workspaceRootPath := t.TempDir()
	commandGuardrailService := NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspaceRootPath,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
		TimeoutSecond:         3,
	})

	_, errorValue := commandGuardrailService.BuildCommandPlan(CommandRequest{
		Command: strings.Join([]string{
			"cat <<'EOF' > index.html",
			`<script type="module" src="/src/main.tsx"></script>`,
			`<a href="/">Home</a>`,
			"EOF",
			"bun run build",
		}, "\n"),
		WorkingDirectoryPath: workspaceRootPath,
	})

	if errorValue != nil {
		t.Fatalf("expected heredoc content paths to be ignored, got %v", errorValue)
	}
}

func TestCommandPathGuardrailRejectsEscapingPathBeforeHereDocument(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root cannot build terminal command plans")
	}

	workspaceRootPath := t.TempDir()
	commandGuardrailService := NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspaceRootPath,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
		TimeoutSecond:         3,
	})

	_, errorValue := commandGuardrailService.BuildCommandPlan(CommandRequest{
		Command: strings.Join([]string{
			"cat /opt/blueclaw/state <<EOF",
			"hello",
			"EOF",
		}, "\n"),
		WorkingDirectoryPath: workspaceRootPath,
	})

	var commandGuardrailError CommandGuardrailError
	if !errors.As(errorValue, &commandGuardrailError) {
		t.Fatalf("expected command guardrail error, got %v", errorValue)
	}
	if !strings.Contains(commandGuardrailError.Error(), "/opt/blueclaw/state") {
		t.Fatalf("expected escaping command path in error, got %q", commandGuardrailError.Error())
	}
}
