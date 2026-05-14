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
	observation := newFailureObservation("obs-001", "call_tool", "memory.search", "Persistent memory search is unavailable.", FailureDependencyUnavailable, FailureCodes.MemorySearchUnavailable, "graphiti_search")
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
	observation := newFailureObservation("obs-001", "call_tool", "terminal.run", "command failed", FailureExternalService, FailureCodeLiteral("terminal.command.failed"), "terminal_run")
	guidance := recoveryGuidanceContent(observation)

	if strings.Contains(guidance, "web.search") {
		t.Fatalf("expected non-memory failure not to include web route, got %q", guidance)
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
