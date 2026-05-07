package security

import (
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
	if commandPlan.EnvironmentVariables["HOME"] != workspaceRootPath {
		t.Fatalf("expected POSIX HOME environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.Timeout != 3*time.Second {
		t.Fatalf("expected timeout to survive POSIX wrapping, got %+v", commandPlan.Timeout)
	}
}
