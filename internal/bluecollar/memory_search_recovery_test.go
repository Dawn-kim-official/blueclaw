package bluecollar

import (
	"strings"
	"testing"
)

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
