package bluecollar

import (
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"strings"
	"testing"
)

func TestMemorySearchWebSearchIsAlternateRecoveryRoute(t *testing.T) {
	if !isAlternateRouteToolPair("memory_search", "web_search") {
		t.Fatal("expected web_search to recover memory_search failure as an alternate route")
	}
	if isAlternateRouteToolPair("web_search", "memory_search") {
		t.Fatal("expected memory_search not to recover web_search failure as an alternate route")
	}
}

func TestMemorySearchUnavailableRecoveryGuidanceIncludesWebSearchRoute(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "memory_search", "Persistent memory search is unavailable.", toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, "graphiti_search")
	guidance := recoveryGuidanceContent(observation, "")

	for _, expectedText := range []string{
		"web_search",
		"public, current, or external",
		"private person or circle memory",
	} {
		if !strings.Contains(guidance, expectedText) {
			t.Fatalf("expected recovery guidance to contain %q, got %q", expectedText, guidance)
		}
	}
}

func TestNonMemoryFailureDoesNotIncludeWebSearchRoute(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "terminal_run", "command failed", toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "terminal_run")
	guidance := recoveryGuidanceContent(observation, "")

	if strings.Contains(guidance, "web_search") {
		t.Fatalf("expected non-memory failure not to include web route, got %q", guidance)
	}
}

func TestNonMemoryUnavailableFailureDoesNotIncludeWebSearchRoute(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "terminal_run", "terminal unavailable", toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, "terminal_run")
	guidance := recoveryGuidanceContent(observation, "")

	if strings.Contains(guidance, "web_search") {
		t.Fatalf("expected non-memory unavailable failure not to include web route, got %q", guidance)
	}
}

func TestMemoryInstructionsDescribeWebSearchRecoveryBoundary(t *testing.T) {
	instructions := DefaultSkillInstructions()
	if len(instructions) == 0 {
		t.Fatal("expected default memory skill instruction")
	}
	prompt := instructions[0].Prompt
	for _, expectedText := range []string{
		"selected public web tool",
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
		"memory_remember is the only path to durable storage",
		"explicitly asks you to remember",
		"durable preference, fact, or context update",
		"call memory_remember with one compact standalone fact per call",
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
		"selected direct tool",
		"action schema contains the exact tools callable",
	} {
		if !strings.Contains(instruction, expectedText) {
			t.Fatalf("expected system instruction to contain %q, got %q", expectedText, instruction)
		}
	}
}
