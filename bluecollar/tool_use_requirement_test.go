package bluecollar

import (
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

import "testing"

func TestGoogleWorkspaceAvoidanceDoesNotRequireBrowserEvidence(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser_open", "browser_snapshot", "file_deliver"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:  "do not use Google Workspace; attach local PPTX, PDF, HTML and notes files built with Marp.",
		ToolSet: toolRegistry,
	})

	for _, requirement := range requirements {
		if requirement.ToolName == "browser_screenshot" {
			t.Fatalf("expected no browser requirement, got %+v", requirements)
		}
	}
}

func TestGoogleSearchStillRequiresBrowserEvidence(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser_open", "browser_snapshot"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:                "search Google for the company information",
		ToolSet:               toolRegistry,
		TaskShape:             TaskShapeBrowserHandoffTask,
		RequiredEvidenceTools: []string{"browser_snapshot"},
	})

	if len(requirements) != 1 || requirements[0].ToolName != "browser_snapshot" {
		t.Fatalf("expected browser requirement, got %+v", requirements)
	}
}

func TestExplicitTaskEvidenceIgnoresNoisyBrowserTaskShape(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser_open", "task_update"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		TaskShape:             TaskShapeBrowserHandoffTask,
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"task_update"},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"task_update"}},
	})

	if len(requirements) != 1 || requirements[0].ToolName != "task_update" {
		t.Fatalf("expected only explicit task evidence, got %+v", requirements)
	}
}

func TestDirectMessageUsesOnlyExplicitEvidence(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser_open", "browser_snapshot", "message_send"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:                "DM Dana and ask them to search on Google",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"message_send"},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	})

	if len(requirements) != 1 || requirements[0].ToolName != "message_send" {
		t.Fatalf("expected only DM send evidence, got %+v", requirements)
	}
}

func TestSelectedDirectMessageSkillDoesNotRequireDirectMessageEvidence(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"message_send", "web_fetch"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:         "https://example.com use it to write the business plan",
		ToolSet:        toolRegistry,
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	})

	if len(requirements) != 0 {
		t.Fatalf("expected selected DM skill to stay advisory, got %+v", requirements)
	}
}

func TestBrowserRetryWithVisibleContextRequiresBrowserEvidence(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser_open", "browser_snapshot"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:                "open it again",
		ToolSet:               toolRegistry,
		TaskShape:             TaskShapeBrowserHandoffTask,
		RequiredEvidenceTools: []string{"browser_open"},
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "user", Text: "help me get credential.json from the Google Cloud console"},
			{Speaker: "internkim", Text: "The companion browser connection is required."},
		}},
	})

	if len(requirements) != 1 || requirements[0].ToolName != "browser_open" {
		t.Fatalf("expected browser follow-up requirement, got %+v", requirements)
	}
}

func TestAttachmentRetryWithBrowserFailureContextDoesNotRequireBrowserEvidence(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser_open", "browser_snapshot", "file_preview", "file_read"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:  "let's try again",
		ToolSet: toolRegistry,
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{
				Speaker: "user",
				Text:    "read this file and tell me what to improve",
				Materials: []VisibleContextMaterial{{
					MaterialID:  "mattermost:file-1",
					Path:        "home/inbox/mattermost/direct-1/post-1/page.html",
					IsAvailable: true,
				}},
			},
			{Speaker: "internkim", Text: "The companion browser connection is required."},
		}},
	})

	if len(requirements) != 0 {
		t.Fatalf("expected no browser evidence requirement for attachment follow-up, got %+v", requirements)
	}
}

func TestEvidenceRequirementsSkipReadOnlyTools(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "message_search", SideEffectClass: toolcontract.ToolSideEffectRead},
		{Name: "message_update", SideEffectClass: toolcontract.ToolSideEffectWorkspaceWrite},
	})
	request := AgentTurnRequest{
		ToolSet:               toolSet,
		RequiredEvidenceTools: []string{"message_search", "message_update"},
	}

	requirements := deriveToolUseRequirements(request)

	if len(requirements) != 1 || requirements[0].ToolName != "message_update" {
		t.Fatalf("expected only the side-effect tool to hard-gate completion, got %+v", requirements)
	}
}
