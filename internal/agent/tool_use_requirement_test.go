package agent

import "testing"

func TestGoogleWorkspaceAvoidanceDoesNotRequireBrowserEvidence(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.snapshot", "file.attach"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:  "구글 워크스페이스는 쓰지 말고 Marp로 로컬 PPTX PDF HTML notes 파일을 첨부해줘.",
		ToolSet: toolRegistry,
	})

	for _, requirement := range requirements {
		if requirement.ToolPrefix == "browser." || requirement.ToolName == "browser.screenshot" {
			t.Fatalf("expected no browser requirement, got %+v", requirements)
		}
	}
}

func TestGoogleSearchStillRequiresBrowserEvidence(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.snapshot"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:  "구글에서 회사 정보를 검색해줘",
		ToolSet: toolRegistry,
	})

	if len(requirements) != 1 || requirements[0].ToolPrefix != "browser." {
		t.Fatalf("expected browser requirement, got %+v", requirements)
	}
}

func TestDirectMessageEvidenceSuppressesImplicitBrowserRequirement(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.snapshot", "platform.dm.send"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:                "동하에게 구글에서 검색해보라고 DM 보내줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"platform.dm.send"},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	})

	if len(requirements) != 1 || requirements[0].ToolName != "platform.dm.send" {
		t.Fatalf("expected only DM send evidence, got %+v", requirements)
	}
}

func TestBrowserRetryWithVisibleContextRequiresBrowserEvidence(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.snapshot"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:  "다시 열어봐",
		ToolSet: toolRegistry,
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "사용자", Text: "구글 클라우드 콘솔에서 credential.json 받는 거 도와줘"},
			{Speaker: "김인턴", Text: "Companion 브라우저 연결이 필요합니다."},
		}},
	})

	if len(requirements) != 1 || requirements[0].ToolPrefix != "browser." {
		t.Fatalf("expected browser follow-up requirement, got %+v", requirements)
	}
}
