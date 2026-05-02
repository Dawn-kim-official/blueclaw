package agent

import "testing"

func TestGoogleWorkspaceAvoidanceDoesNotRequireBrowserEvidence(t *testing.T) {
	toolRegistry := NewToolRegistry([]string{"browser.open", "browser.snapshot", "file.attach"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:       "구글 워크스페이스는 쓰지 말고 Marp로 로컬 PPTX PDF HTML notes 파일을 첨부해줘.",
		ToolRegistry: toolRegistry,
	})

	for _, requirement := range requirements {
		if requirement.ToolPrefix == "browser." || requirement.ToolName == "browser.screenshot" {
			t.Fatalf("expected no browser requirement, got %+v", requirements)
		}
	}
}

func TestGoogleSearchStillRequiresBrowserEvidence(t *testing.T) {
	toolRegistry := NewToolRegistry([]string{"browser.open", "browser.snapshot"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:       "구글에서 회사 정보를 검색해줘",
		ToolRegistry: toolRegistry,
	})

	if len(requirements) != 1 || requirements[0].ToolPrefix != "browser." {
		t.Fatalf("expected browser requirement, got %+v", requirements)
	}
}
