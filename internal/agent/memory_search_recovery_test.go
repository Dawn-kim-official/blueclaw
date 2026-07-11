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
	guidance := recoveryGuidanceContent(observation, "")

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
	guidance := recoveryGuidanceContent(observation, "")

	if strings.Contains(guidance, "web.search") {
		t.Fatalf("expected non-memory failure not to include web route, got %q", guidance)
	}
}

func TestNonMemoryUnavailableFailureDoesNotIncludeWebSearchRoute(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "terminal.run", "terminal unavailable", FailureDependencyUnavailable, FailureCodes.Unavailable, "terminal_run")
	guidance := recoveryGuidanceContent(observation, "")

	if strings.Contains(guidance, "web.search") {
		t.Fatalf("expected non-memory unavailable failure not to include web route, got %q", guidance)
	}
}

func TestTerminalPathGuardrailRecoveryGuidanceIncludesCorrectedWorkspaceRetry(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "terminal.run", "command path escapes workspace root", FailureInvalidInput, FailureCodes.InvalidInput, "terminal_path_guardrail")
	guidance := recoveryGuidanceContent(observation, "")

	for _, expectedText := range []string{
		"retry terminal.run",
		"~/documents",
		"/workspace/skills/<skill>/scripts/skill_runtime.py",
		"Do not call /opt/blueclaw",
	} {
		if !strings.Contains(guidance, expectedText) {
			t.Fatalf("expected recovery guidance to contain %q, got %q", expectedText, guidance)
		}
	}
}

func TestTerminalCurrentDirectoryRecoveryGuidanceUsesSiteAppWorkspace(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "terminal.run", "current workspace is unavailable", FailureExternalService, FailureCodes.OperationFailed, "terminal_working_directory_access")
	guidance := recoveryGuidanceContent(observation, "")

	for _, expectedText := range []string{
		"workingDirectoryPath",
		"~/documents",
		"relative paths",
	} {
		if !strings.Contains(guidance, expectedText) {
			t.Fatalf("expected recovery guidance to contain %q, got %q", expectedText, guidance)
		}
	}
}

func TestTerminalDependencyRecoveryUsesStructuredFailureClass(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "terminal.run", "python runtime dependency unavailable", FailureDependencyUnavailable, FailureCodes.Unavailable, "terminal_dependency")
	observation.Failure.FailureClass = failureClassDependency
	packet := buildRecoveryPacket(observation)

	if packet.FailureClass != failureClassDependency {
		t.Fatalf("expected structured dependency class, got %+v", packet)
	}
	if !strings.Contains(strings.Join(packet.MustDoNext, " "), "dependency") {
		t.Fatalf("expected generic dependency recovery steps, got %+v", packet.MustDoNext)
	}
}

func TestMemoryInstructionsDescribeWebSearchRecoveryBoundary(t *testing.T) {
	instructions := DefaultSkillInstructions()
	if len(instructions) == 0 {
		t.Fatal("expected default memory skill instruction")
	}
	prompt := instructions[0].Prompt
	for _, expectedText := range []string{
		"selected web-search skill for public web information",
		"public, current, or external",
		"Do not use public web lookup to replace private person memory",
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
		"memory.remember is the only path to durable storage",
		"explicitly asks you to remember",
		"durable preference, fact, or context update",
		"call memory.remember with one compact standalone fact per call",
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
		"call the exact named operation exposed in the current action schema",
		"The current action schema contains the always-available base tools plus the selected skills' allowed-tools",
	} {
		if !strings.Contains(instruction, expectedText) {
			t.Fatalf("expected system instruction to contain %q, got %q", expectedText, instruction)
		}
	}
}
