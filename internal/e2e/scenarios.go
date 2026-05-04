package e2e

import "blueclaw/internal/agent"

func SlidesLocalMultiturnSuccessScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "slides_local_multiturn_success",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{simpleSlidesSkill()},
		AllowedTools:          []string{"conversation.history", "memory.search", "terminal.run", "file.write", "file.attach"},
		Turns: []VirtualTurn{{
			Prompt:                 "너 뭐 할 수 있는지 피피티 만들어서 보내줘봐",
			ExpectedSelectedSkills: []string{"simple-slides"},
			ExpectedToolCalls:      []string{"terminal.run", "file.attach"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.terminal.run.requested", BodyFragment: "create_deck.py", Count: 1},
				{Name: "tool.terminal.run.result", BodyFragment: "Building HTML + PPTX + PDF", Count: 1},
				{Name: "tool.terminal.run.result", BodyFragment: "Slide render review", Count: 1},
				{Name: "tool.file.attach.result", BodyFragment: `"isError":false`, Count: 1},
			},
			ExpectedEvents:      []string{"agent.validity_review", "agent.quality_review"},
			ExpectedAttachments: []string{".pptx", ".pdf", ".html", "-notes.txt"},
			ExpectedWorkspaceFiles: []VirtualWorkspaceFileExpectation{
				{
					PathGlob:          ".blueclaw/tmp/*/DESIGN.md",
					ContainsFragments: []string{"colors:", "Visual direction"},
				},
				{
					PathGlob:           ".blueclaw/tmp/*/presentation.md",
					ContainsFragments:  []string{"design-source: DESIGN.md", "InternKim capability deck"},
					ForbiddenFragments: []string{"Draft a presentation deck", "user_request:"},
				},
				{
					PathGlob:          ".blueclaw/tmp/*/review/slide-review.json",
					ContainsFragments: []string{`"passed": true`, `"safeMargin": true`, `"edgeOverflow": true`, `"contactSheets"`},
				},
			},
			ForbiddenReplyFragments: []string{
				"PPT 못",
				"PPT 파일을 직접 생성할 수",
				"credentials",
				"자격 증명",
			},
		}},
	}
}

func MemoryGuidedFollowupScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "memory_guided_followup",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Turns: []VirtualTurn{
			{
				Prompt: "내 발표 자료는 항상 짧은 문장과 한국어 제목을 선호한다고 기억해줘",
				ModelResponses: []string{
					actionFinalReply("기억해둘게요."),
				},
				ExpectedReplyFragments: []string{"기억"},
			},
			{
				Prompt: "아까 말한 선호를 반영해서 다음 발표 스타일을 한 문장으로 정리해줘",
				ModelResponses: []string{
					actionFinalReply("짧은 문장과 한국어 제목 중심으로 정리하겠습니다."),
				},
				ExpectedReplyFragments: []string{"짧은 문장", "한국어 제목"},
			},
		},
	}
}

func ToolPermissionHidesSkillScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "tool_permission_hides_skill",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{simpleSlidesSkill()},
		AllowedTools:          []string{"memory.search", "file.write"},
		Turns: []VirtualTurn{{
			Prompt: "피피티 만들어줘",
			ModelResponses: []string{
				actionFinalReply("현재 profile에서는 필요한 도구가 없어 슬라이드 생성 skill을 실행하지 않았습니다."),
			},
			ExpectedReplyFragments: []string{"필요한 도구"},
		}},
	}
}

func GWSDisabledScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "gws_disabled",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"memory.search", "terminal.run", "file.write", "file.attach"},
		Turns: []VirtualTurn{{
			Prompt: "구글 드라이브에 파일 올릴 수 있는지 확인해줘",
			ModelResponses: []string{
				actionCallTool("google.drive.import_pptx", `{"path":"deck.pptx"}`),
				actionFinalReply("Google Workspace 도구는 노출되지 않아 호출이 거부되었습니다. 로컬 첨부 경로만 사용할 수 있습니다."),
			},
			ExpectedToolCalls:      []string{"google.drive.import_pptx"},
			ExpectedReplyFragments: []string{"거부"},
		}},
	}
}

func simpleSlidesSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:        "simple-slides",
		Description: "Create local presentation decks with PPTX, PDF, HTML, and notes attachments.",
		Category:    "document-generation",
		Tags:        []string{"slides", "pptx", "presentation"},
		Prompt:      "Write brief.md, then run python3 /workspace/skills/simple-slides/scripts/create_deck.py --slug <deck-slug> --brief brief.md. The creator must write Stitch-compatible DESIGN.md, read that design source, generate presentation.md, build artifacts, and then file.attach PPTX, PDF, HTML, and notes. Do not hand-write presentation.md before running the creator. Do not use Google Workspace unless a google tool is explicitly available.",
		Activation: agent.SkillActivation{
			Keywords: []string{"피피티", "파워포인트", "발표자료", "pptx", "google slides", "구글 슬라이드"},
		},
		Completion: agent.SkillCompletion{
			RequiredEvidenceTools:      []string{"file.attach"},
			RequiredAttachmentSuffixes: []string{".pptx", ".pdf", ".html", "-notes.txt"},
		},
		Quality: agent.SkillQuality{
			RecommendedChecks: []string{"marp_build_log_success", "parse_pptx", "pdf_page_count", "render_nonblank", "slide_render_images", "max_text_overflow", "slide_count_min", "forbidden_reply_fragments"},
		},
		RequiredTools: []string{"file.write", "terminal.run", "file.attach"},
		TriggerHints:  []string{"피피티", "파워포인트", "발표자료", "pptx", "google slides", "구글 슬라이드"},
		Source: agent.InstructionSource{
			Path:      "skills/simple-slides/SKILL.md",
			SkillName: "simple-slides",
			ByteSize:  512,
			SHA256:    "virtual-simple-slides",
		},
	}
}
