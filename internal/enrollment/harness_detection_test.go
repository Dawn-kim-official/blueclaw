package enrollment

import (
	"os"
	"path/filepath"
	"testing"
)

func pathWithAgentCommands(t *testing.T, commandNames ...string) string {
	t.Helper()
	directoryPath := t.TempDir()
	for _, commandName := range commandNames {
		commandPath := filepath.Join(directoryPath, commandName)
		if errorValue := os.WriteFile(commandPath, []byte("#!/bin/sh\n"), 0o755); errorValue != nil {
			t.Fatalf("expected to place a fake %s on PATH: %v", commandName, errorValue)
		}
	}
	return directoryPath
}

func TestAFreshInstallSelectsCodexWhenItIsTheOnlyAgentOnPath(t *testing.T) {
	t.Setenv("PATH", pathWithAgentCommands(t, "codex"))

	detected := detectedHarness()

	if detected.Name != "codex" {
		t.Fatalf("expected a fresh install to select the codex harness it can actually run, got %q", detected.Name)
	}
	if detected.AgentCommandPath == "" {
		t.Fatal("expected the detected harness to carry the executable path blueclaw found")
	}
}

func TestAFreshInstallSelectsClaudeCodeWhenBothAgentsAreOnPath(t *testing.T) {
	t.Setenv("PATH", pathWithAgentCommands(t, "claude", "codex"))

	detected := detectedHarness()

	if detected.Name != "claude-code" {
		t.Fatalf("expected claude-code to win when both agents are installed, got %q", detected.Name)
	}
}

func TestAFreshInstallSelectsNoHarnessWhenNoAgentIsInstalled(t *testing.T) {
	t.Setenv("PATH", pathWithAgentCommands(t))

	if detected := detectedHarness(); detected.Name != "" {
		t.Fatalf("expected no harness when nothing is installed rather than a name blueclaw cannot run, got %q", detected.Name)
	}
	if available := AvailableHarnesses(); len(available) != 0 {
		t.Fatalf("expected blueclaw to offer only harnesses it found on this machine, got %+v", available)
	}
}

func TestEveryOfferedHarnessCarriesTheExecutableItRuns(t *testing.T) {
	t.Setenv("PATH", pathWithAgentCommands(t, "claude", "codex"))

	available := AvailableHarnesses()

	if len(available) != 2 {
		t.Fatalf("expected both installed agents to be offered, got %+v", available)
	}
	for _, candidate := range available {
		if candidate.AgentCommandPath == "" {
			t.Fatalf("blueclaw hosts an agent rather than shipping one, so %q must name the executable it runs", candidate.Name)
		}
	}
}

func TestPreflightRefusesAnInstallWithNoHarnessAttached(t *testing.T) {
	checkResult := checkHarness(HarnessChoice{})

	if checkResult.IsReady {
		t.Fatal("expected an install with no attached harness to fail preflight rather than claim a built-in loop")
	}
	if checkResult.Guidance == "" {
		t.Fatal("expected preflight to tell the person which agent to install")
	}
}
