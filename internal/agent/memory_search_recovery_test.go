package agent

import (
	"strings"
	"testing"
)

func TestMemorySearchWebSearchIsAlternateRecoveryRoute(t *testing.T) {
	if !isAlternateRouteToolPair("memory.search", "web.search") {
		t.Fatal("expected web.search to recover memory.search failure as an alternate route")
	}
	if isAlternateRouteToolPair("web.search", "memory.search") {
		t.Fatal("expected memory.search not to recover web.search failure as an alternate route")
	}
}

func TestMemorySearchUnavailableRecoveryGuidanceIncludesWebSearchRoute(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "memory.search", "Persistent memory search is unavailable.", FailureDependencyUnavailable, FailureCodes.Unavailable, "graphiti_search")
	guidance := recoveryGuidanceContent(observation)

	for _, expectedText := range []string{
		"web.search",
		"public, current, or external",
		"private person or circle memory",
	} {
		if !strings.Contains(guidance, expectedText) {
			t.Fatalf("expected recovery guidance to contain %q, got %q", expectedText, guidance)
		}
	}
}

func TestNonMemoryFailureDoesNotIncludeWebSearchRoute(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "terminal.run", "command failed", FailureExternalService, FailureCodes.OperationFailed, "terminal_run")
	guidance := recoveryGuidanceContent(observation)

	if strings.Contains(guidance, "web.search") {
		t.Fatalf("expected non-memory failure not to include web route, got %q", guidance)
	}
}

func TestNonMemoryUnavailableFailureDoesNotIncludeWebSearchRoute(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "terminal.run", "terminal unavailable", FailureDependencyUnavailable, FailureCodes.Unavailable, "terminal_run")
	guidance := recoveryGuidanceContent(observation)

	if strings.Contains(guidance, "web.search") {
		t.Fatalf("expected non-memory unavailable failure not to include web route, got %q", guidance)
	}
}

func TestTerminalPathGuardrailRecoveryGuidanceIncludesCorrectedWorkspaceRetry(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "terminal.run", "command path escapes workspace root", FailureInvalidInput, FailureCodes.InvalidInput, "terminal_path_guardrail")
	guidance := recoveryGuidanceContent(observation)

	for _, expectedText := range []string{
		"retry terminal.run",
		"virtual workspace paths",
		"home/<path>",
		"/workspace/skills/<skill>/scripts/skill_runtime.py",
		"Do not call /opt/blueclaw",
	} {
		if !strings.Contains(guidance, expectedText) {
			t.Fatalf("expected recovery guidance to contain %q, got %q", expectedText, guidance)
		}
	}
}

func TestTerminalCurrentDirectoryRecoveryGuidanceUsesSiteAppWorkspace(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "terminal.run", "CouldntReadCurrentDirectory", FailureExternalService, FailureCodes.OperationFailed, "terminal_run")
	guidance := recoveryGuidanceContent(observation)

	for _, expectedText := range []string{
		"could not read its current working directory",
		"site.app.status",
		"appWorkspacePath",
		"home/sites/<siteID>/app",
		"not source subdirectories like app/src",
	} {
		if !strings.Contains(guidance, expectedText) {
			t.Fatalf("expected recovery guidance to contain %q, got %q", expectedText, guidance)
		}
	}
}

func TestTerminalModuleNotFoundRecoveryGuidanceUsesSkillRuntime(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "terminal.run", "ModuleNotFoundError: No module named 'pptx'", FailureExternalService, FailureCodes.OperationFailed, "terminal_run")
	guidance := recoveryGuidanceContent(observation)

	for _, expectedText := range []string{
		"/workspace/skills/pptx/scripts/skill_runtime.py",
		"do not probe or install python-pptx with system Python",
		"/workspace/skills/simple-slides/scripts/build.sh",
	} {
		if !strings.Contains(guidance, expectedText) {
			t.Fatalf("expected recovery guidance to contain %q, got %q", expectedText, guidance)
		}
	}
}

func TestMemoryInstructionsDescribeWebSearchRecoveryBoundary(t *testing.T) {
	instructions := DefaultSkillInstructions()
	if len(instructions) == 0 {
		t.Fatal("expected default memory skill instruction")
	}
	prompt := instructions[0].Prompt
	for _, expectedText := range []string{
		"memory.search is unavailable",
		"public, current, or external sources",
		"Do not use web.search to replace private person memory",
	} {
		if !strings.Contains(prompt, expectedText) {
			t.Fatalf("expected memory prompt to contain %q, got %q", expectedText, prompt)
		}
	}
}

func TestMemoryInstructionsRequireRememberForDurableUpdates(t *testing.T) {
	instructions := DefaultSkillInstructions()
	if len(instructions) == 0 {
		t.Fatal("expected default memory skill instruction")
	}
	prompt := instructions[0].Prompt
	for _, expectedText := range []string{
		"future conversations",
		"explicitly asks you to remember",
		"durable preference, fact, or context update",
		"non-exhaustive examples",
	} {
		if !strings.Contains(prompt, expectedText) {
			t.Fatalf("expected memory prompt to contain %q, got %q", expectedText, prompt)
		}
	}
}

func TestSystemInstructionAllowsWebSearchAfterMemorySearchUnavailable(t *testing.T) {
	instruction := buildAgentSystemInstruction(AgentTurnRequest{})
	for _, expectedText := range []string{
		"memory.search is unavailable",
		"public, current, or external",
	} {
		if !strings.Contains(instruction, expectedText) {
			t.Fatalf("expected system instruction to contain %q, got %q", expectedText, instruction)
		}
	}
}
