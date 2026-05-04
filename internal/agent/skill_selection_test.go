package agent

import (
	"context"
	"strings"
	"testing"
)

func TestSelectInstructionBundleIncludesSimpleSlidesForKoreanPPTRequest(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{
			{
				Name:          "simple-slides",
				Description:   "Create presentation decks.",
				Category:      "document-generation",
				Tags:          []string{"slides", "pptx"},
				Prompt:        "Generate PPTX with Marp.",
				TriggerHints:  []string{"피피티", "파워포인트", "발표자료", "pptx"},
				RequiredTools: []string{"terminal.run", "file.write", "file.attach"},
				Source:        InstructionSource{Path: "skills/simple-slides/SKILL.md", SkillName: "simple-slides"},
			},
		},
	}

	selectedBundle := selectInstructionBundleForRequest(instructionBundle, AgentRequest{
		Prompt:       "너 뭐 할 수 있는지 피피티 만들어서 보내줘봐",
		ToolRegistry: testToolRegistry([]string{"terminal.run", "file.write", "file.attach"}),
	})

	if !strings.Contains(selectedBundle.Prompt, "Generate PPTX with Marp.") {
		t.Fatalf("expected simple-slides skill prompt for Korean PPT request, got %q", selectedBundle.Prompt)
	}
	if !strings.Contains(selectedBundle.Prompt, "Available skill index") || !strings.Contains(selectedBundle.Prompt, "category=document-generation") {
		t.Fatalf("expected compact skill index, got %q", selectedBundle.Prompt)
	}
	if len(selectedBundle.Sources) != 1 || selectedBundle.Sources[0].SkillName != "simple-slides" {
		t.Fatalf("expected simple-slides instruction source, got %+v", selectedBundle.Sources)
	}
	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected selected skill decision, got %+v", selectedBundle.SkillDecisions)
	}
}

func TestSelectInstructionBundleUsesVisibleContextForFollowUpArtifactRequest(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{
			{
				Name:          "simple-slides",
				Description:   "Create presentation decks.",
				Prompt:        "Generate PPTX with Marp.",
				TriggerHints:  []string{"피피티", "pptx"},
				RequiredTools: []string{"terminal.run", "file.write", "file.attach"},
				Source:        InstructionSource{Path: "skills/simple-slides/SKILL.md", SkillName: "simple-slides"},
			},
		},
	}

	selectedBundle := selectInstructionBundleForRequest(instructionBundle, AgentRequest{
		Prompt: "별로야. 폐기하고 새로 다시 해줘.",
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "user", Text: "너 뭐 할 수 있는지 8장 피피티 만들어서 보내줘봐"},
		}},
		ToolRegistry: testToolRegistry([]string{"terminal.run", "file.write", "file.attach"}),
	})

	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected follow-up context to select simple-slides, got %+v", selectedBundle.SkillDecisions)
	}
	if !strings.Contains(selectedBundle.Prompt, "Generate PPTX with Marp.") {
		t.Fatalf("expected selected skill body, got %q", selectedBundle.Prompt)
	}
}

func TestSkillSelectorIncludesSimpleSlidesInstructionForPresentationAliases(t *testing.T) {
	skillSelector := SkillSelector{}
	skillInstruction := SkillInstruction{
		Name:          "simple-slides",
		TriggerHints:  []string{"피피티", "파워포인트", "발표자료", "pptx"},
		RequiredTools: []string{"terminal.run", "file.write", "file.attach"},
	}
	for _, prompt := range []string{
		"피피티 만들어줘",
		"파워포인트 자료로 정리해줘",
		"발표자료 만들어줘",
		"pptx 파일로 보내줘",
	} {
		request := AgentRequest{Prompt: prompt, ToolRegistry: testToolRegistry([]string{"terminal.run", "file.write", "file.attach"})}
		if !skillSelector.ShouldInclude(skillInstruction, request) {
			t.Fatalf("expected simple-slides for prompt %q", prompt)
		}
	}
}

func TestSkillSelectorSkipsSkillWhenRequiredToolIsMissing(t *testing.T) {
	skillSelector := SkillSelector{}
	skillInstruction := SkillInstruction{
		Name:          "simple-slides",
		TriggerHints:  []string{"피피티"},
		RequiredTools: []string{"terminal.run", "file.write", "file.attach"},
	}
	request := AgentRequest{
		Prompt:       "피피티 만들어줘",
		ToolRegistry: testToolRegistry([]string{"terminal.run", "file.write"}),
	}

	decision := skillSelector.Evaluate(skillInstruction, request, "default")
	if decision.Status == "selected" {
		t.Fatal("expected simple-slides to be skipped without file.attach")
	}
	if decision.Reason != "missing_required_tools" || len(decision.MissingTools) != 1 || decision.MissingTools[0] != "file.attach" {
		t.Fatalf("expected missing tool reason, got %+v", decision)
	}
}

func TestSkillSelectorSkipsSkillWhenDefaultProfileIsNotAllowed(t *testing.T) {
	skillSelector := SkillSelector{}
	skillInstruction := SkillInstruction{
		Name:            "admin-only",
		AllowedProfiles: []string{"admin"},
		TriggerHints:    []string{"admin"},
	}

	decision := skillSelector.Evaluate(skillInstruction, AgentRequest{Prompt: "admin"}, "default")

	if decision.Status == "selected" {
		t.Fatal("expected profile-disallowed skill to be skipped")
	}
	if decision.Reason != "profile_not_allowed" {
		t.Fatalf("expected profile reason, got %+v", decision)
	}
}

func TestSelectInstructionBundleUsesRequestProfileForSkillSelection(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:            "admin-only",
				Prompt:          "ADMIN FULL BODY",
				AllowedProfiles: []string{"admin"},
				TriggerHints:    []string{"admin"},
			},
		},
	}

	selectedBundle := selectInstructionBundleForRequest(instructionBundle, AgentRequest{
		ProfileName: "admin",
		Prompt:      "admin",
	})

	if !strings.Contains(selectedBundle.Prompt, "ADMIN FULL BODY") {
		t.Fatalf("expected admin profile to select admin skill, got %q", selectedBundle.Prompt)
	}
	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].ProfileName != "admin" {
		t.Fatalf("expected admin profile decision, got %+v", selectedBundle.SkillDecisions)
	}
}

func TestSelectInstructionBundleKeepsUnselectedFullSkillBodyOutOfPrompt(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{
			{
				Name:          "simple-slides",
				Description:   "Create decks.",
				Prompt:        "Generate PPTX with Marp.",
				TriggerHints:  []string{"피피티"},
				RequiredTools: []string{"terminal.run"},
			},
			{
				Name:          "create-gws-file",
				Description:   "Create spreadsheets.",
				Prompt:        "SECRET FULL BODY",
				TriggerHints:  []string{"spreadsheet"},
				RequiredTools: []string{"terminal.run"},
			},
		},
	}

	selectedBundle := selectInstructionBundleForRequest(instructionBundle, AgentRequest{
		Prompt:       "피피티 만들어줘",
		ToolRegistry: testToolRegistry([]string{"terminal.run"}),
	})

	if !strings.Contains(selectedBundle.Prompt, "create-gws-file") {
		t.Fatalf("expected unselected eligible skill in compact index, got %q", selectedBundle.Prompt)
	}
	if strings.Contains(selectedBundle.Prompt, "SECRET FULL BODY") {
		t.Fatalf("expected unselected full body to be omitted, got %q", selectedBundle.Prompt)
	}
}

func testToolRegistry(toolNames []string) *ToolRegistry {
	toolRegistry := NewToolRegistry(toolNames)
	for _, toolName := range toolNames {
		toolRegistry.RegisterTool(ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolResult{}, nil
		})
	}
	return toolRegistry
}
