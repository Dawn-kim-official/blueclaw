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
		Prompt:    "구글에서 회사 정보를 검색해줘",
		ToolSet:   toolRegistry,
		WorkKinds: []string{WorkKindBrowserSession},
	})

	if len(requirements) != 1 || requirements[0].ToolPrefix != "browser." {
		t.Fatalf("expected browser requirement, got %+v", requirements)
	}
}

func TestDirectMessageEvidenceSuppressesImplicitBrowserRequirement(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.snapshot", "message.send"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:                "동하에게 구글에서 검색해보라고 DM 보내줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"message.send"},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	})

	if len(requirements) != 1 || requirements[0].ToolName != "message.send" {
		t.Fatalf("expected only DM send evidence, got %+v", requirements)
	}
}

func TestSelectedDirectMessageSkillDoesNotRequireDirectMessageEvidence(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"message.send", "web.fetch"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:         "https://dawn.kim 참고해서 사업계획서 작성해줘",
		ToolSet:        toolRegistry,
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	})

	if len(requirements) != 0 {
		t.Fatalf("expected selected DM skill to stay advisory, got %+v", requirements)
	}
}

func TestBrowserRetryWithVisibleContextRequiresBrowserEvidence(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.snapshot"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:    "다시 열어봐",
		ToolSet:   toolRegistry,
		WorkKinds: []string{WorkKindUserBrowser, WorkKindBrowserSession},
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "사용자", Text: "구글 클라우드 콘솔에서 credential.json 받는 거 도와줘"},
			{Speaker: "김인턴", Text: "Companion 브라우저 연결이 필요합니다."},
		}},
	})

	if len(requirements) != 1 || requirements[0].ToolPrefix != "browser." {
		t.Fatalf("expected browser follow-up requirement, got %+v", requirements)
	}
}

func TestAttachmentRetryWithBrowserFailureContextDoesNotRequireBrowserEvidence(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.snapshot", "file.preview", "file.read"})

	requirements := deriveToolUseRequirements(AgentTurnRequest{
		Prompt:  "다시 시도해보자",
		ToolSet: toolRegistry,
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{
				Speaker: "사용자",
				Text:    "이 파일 내용 보고 개선점 말해줘",
				Materials: []VisibleContextMaterial{{
					MaterialID:  "mattermost:file-1",
					Path:        "home/inbox/mattermost/direct-1/post-1/page.html",
					IsAvailable: true,
				}},
			},
			{Speaker: "김인턴", Text: "Companion 브라우저 연결이 필요합니다."},
		}},
	})

	if len(requirements) != 0 {
		t.Fatalf("expected no browser evidence requirement for attachment follow-up, got %+v", requirements)
	}
}
