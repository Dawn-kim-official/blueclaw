package e2e

import (
	"blueclaw/internal/agent"
	"blueclaw/internal/connectors"
)

func SlidesLocalMultiturnSuccessScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "slides_local_multiturn_success",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{simpleSlidesSkill()},
		AllowedTools:          []string{"conversation.history", "memory.search", "terminal.run", "file.write", "file.attach"},
		Turns: []VirtualTurn{{
			Prompt:                 "너 뭐 할 수 있는지 8장 피피티 만들어서 보내줘봐",
			ExpectedSelectedSkills: []string{"simple-slides"},
			ExpectedToolCalls:      []string{"terminal.run", "file.attach"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.terminal.run.requested", BodyFragment: "NAME=", Count: 1},
				{Name: "tool.terminal.run.requested", BodyFragment: "/workspace/skills/simple-slides/scripts/build.sh", Count: 1},
				{Name: "tool.terminal.run.result", BodyFragment: "Building requested formats", Count: 1},
				{Name: "tool.terminal.run.result", BodyFragment: "Slide render review", Count: 1},
				{Name: "tool.file.attach.result", BodyFragment: `"output"`, Count: 1},
			},
			ExpectedEvents:      []string{"agent.validity_review"},
			ExpectedAttachments: []string{".pptx", ".pdf", ".html", "-notes.txt"},
			ExpectedWorkspaceFiles: []VirtualWorkspaceFileExpectation{
				{
					PathGlob:          "circles/staff/tmp/*/DESIGN.md",
					ContainsFragments: []string{"colors:", "Visual direction"},
				},
				{
					PathGlob:           "circles/staff/tmp/*/presentation.md",
					ContainsFragments:  []string{"design-source: DESIGN.md", "InternKim capability deck", "너 뭐 할 수 있는지"},
					ForbiddenFragments: []string{"Draft a presentation deck", "user_request:"},
				},
				{
					PathGlob:          "circles/staff/tmp/*/review/slide-review.json",
					ContainsFragments: []string{`"passed": true`, `"safeMargin": true`, `"edgeOverflow": true`, `"contactSheets"`},
				},
				{
					PathGlob:          "circles/staff/tmp/*/*.html",
					ContainsFragments: []string{"Paperlogy", "Freesentation", "--background", "InternKim capability deck"},
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
				ActionResponses: []string{
					actionFinishMessage("기억해둘게요."),
				},
				ExpectedReplyFragments: []string{"기억"},
			},
			{
				Prompt: "아까 말한 선호를 반영해서 다음 발표 스타일을 한 문장으로 정리해줘",
				ActionResponses: []string{
					actionFinishMessage("짧은 문장과 한국어 제목 중심으로 정리하겠습니다."),
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
			ActionResponses: []string{
				actionFinishMessage("현재 profile에서는 필요한 도구가 없어 슬라이드 생성 skill을 실행하지 않았습니다."),
			},
			ExpectedReplyFragments: []string{"필요한 도구"},
		}},
	}
}

func AttachmentMaterialReadScenario(artifactDirectoryPath string) VirtualSessionScenario {
	attachment := connectors.InputAttachment{
		Platform:    "mattermost",
		FileID:      "file-1",
		MessageID:   "root-message",
		Filename:    "mascot.png",
		ContentType: "image/png",
		SizeBytes:   13,
	}
	return VirtualSessionScenario{
		Name:                  "attachment_material_read",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "terminal.run", "image.read", "document.read"},
		CapabilityToolNames:   []string{"image.read", "document.read"},
		Turns: []VirtualTurn{{
			Prompt: "다시 이미지 내가 첨부한 거 봐봐",
			ContextMessages: []connectors.VisibleContextMessage{{
				Speaker:            "샘플",
				SpeakerCallingName: "샘플 님",
				SpeakerHandle:      "dongha",
				Text:               "이거 뭔지 알아?",
				InputAttachments:   []connectors.InputAttachment{attachment},
			}},
			ContextMaterials: []connectors.InputAttachment{attachment},
			ActionResponses: []string{
				actionCallTool("image.read", `{"materialID":"mattermost:file-1"}`),
				actionFinishMessage("이미지를 확인했습니다.", "obs-001:image.read:0"),
			},
			ExpectedToolCalls:      []string{"image.read"},
			ExpectedToolCallCounts: map[string]int{"terminal.run": 0},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "agent.instructions_loaded", BodyFragment: "image.read", Count: 1},
				{Name: "agent.instructions_loaded", BodyFragment: "document.read", Count: 1},
			},
			ExpectedModelContexts: []string{
				"materialID=mattermost:file-1",
				"Use the listed materialID or path",
				"mascot.png",
			},
			ForbiddenModelContexts: []string{
				"mail.message.search",
				"mattermost.channel.post",
			},
			ExpectedReplyFragments: []string{"이미지"},
		}},
	}
}

func AttachmentHTMLPreviewRecoveryScenario(artifactDirectoryPath string) VirtualSessionScenario {
	attachment := connectors.InputAttachment{
		Platform:    "mattermost",
		FileID:      "file-html",
		MessageID:   "message-html",
		Filename:    "kim-intern-automation.html",
		ContentType: "text/html",
		SizeBytes:   691000,
	}
	return VirtualSessionScenario{
		Name:                  "attachment_html_preview_recovery",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "terminal.run", "file.preview", "file.read", "image.read"},
		Turns: []VirtualTurn{{
			Prompt:           "이거 파일 내용 보고 어떻게 개선하면 좋을지 말해줘봐",
			InputAttachments: []connectors.InputAttachment{attachment},
			ActionResponses: []string{
				actionCallTool("file.preview", `{"path":"home/inbox/mattermost/thread-1/message-html/kim-intern-automation.html"}`),
				actionFinishMessage("첨부 HTML을 확인했습니다. 자동화 섹션의 정보 구조와 CTA를 더 선명하게 다듬으면 좋겠습니다.", "obs-001:file.preview:0"),
			},
			ExpectedToolCalls: []string{"file.preview"},
			ExpectedToolCallCounts: map[string]int{
				"terminal.run": 0,
				"file.read":    0,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.file.preview.requested", BodyFragment: `"path":"home/inbox/mattermost/thread-1/message-html/kim-intern-automation.html"`, Count: 1},
				{Name: "tool.file.preview.result", BodyFragment: "Virtual HTML Title", Count: 1},
			},
			ExpectedModelContexts: []string{
				"materialID=mattermost:file-html",
				"availableTools=file.preview,file.read",
			},
			ExpectedReplyFragments: []string{"첨부 HTML", "정보 구조"},
		}},
	}
}

func AttachmentHTMLPreviousPreviewRecoveryScenario(artifactDirectoryPath string) VirtualSessionScenario {
	attachment := connectors.InputAttachment{
		Platform:    "mattermost",
		FileID:      "file-html",
		MessageID:   "root-message",
		Filename:    "kim-intern-automation.html",
		ContentType: "text/html",
		SizeBytes:   691000,
	}
	return VirtualSessionScenario{
		Name:                  "attachment_html_previous_preview_recovery",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "terminal.run", "file.preview", "file.read", "image.read"},
		Turns: []VirtualTurn{{
			Prompt: "다시",
			ContextMessages: []connectors.VisibleContextMessage{{
				Speaker:            "샘플",
				SpeakerCallingName: "샘플 님",
				SpeakerHandle:      "dongha",
				Text:               "이거 파일 내용 보고 어떻게 개선하면 좋을지 말해줘봐",
				InputAttachments:   []connectors.InputAttachment{attachment},
			}},
			ContextMaterials: []connectors.InputAttachment{attachment},
			ActionResponses: []string{
				actionCallTool("file.preview", `{"path":"home/inbox/mattermost/thread-1/root-message/kim-intern-automation.html"}`),
				actionFinishMessage("이전 첨부 HTML을 확인했습니다. 자동화 흐름의 핵심 CTA와 섹션 우선순위를 더 명확히 잡으면 좋겠습니다.", "obs-001:file.preview:0"),
			},
			ExpectedToolCalls: []string{"file.preview"},
			ExpectedToolCallCounts: map[string]int{
				"terminal.run": 0,
				"file.read":    0,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.file.preview.requested", BodyFragment: `"path":"home/inbox/mattermost/thread-1/root-message/kim-intern-automation.html"`, Count: 1},
				{Name: "tool.file.preview.result", BodyFragment: "Virtual HTML Title", Count: 1},
			},
			ExpectedModelContexts: []string{
				"Previous attachments:",
				"materialID=mattermost:file-html",
				"availableTools=file.preview,file.read",
			},
			ForbiddenReplyFragments: []string{"파일을 찾을 수", "다시 확인", "직접 공유"},
			ExpectedReplyFragments:  []string{"이전 첨부 HTML", "CTA"},
		}},
	}
}

func AttachmentCurrentImageInputScenario(artifactDirectoryPath string) VirtualSessionScenario {
	attachment := connectors.InputAttachment{
		Platform:    "mattermost",
		FileID:      "file-current",
		MessageID:   "virtual-message-001",
		Filename:    "mascot.png",
		ContentType: "image/png",
		SizeBytes:   13,
	}
	return VirtualSessionScenario{
		Name:                  "attachment_current_image_input",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "terminal.run", "image.read", "document.read"},
		CapabilityToolNames:   []string{"image.read", "document.read"},
		Turns: []VirtualTurn{{
			Prompt:           "이거 보여? 묘사 좀 자세히 해봐.",
			InputAttachments: []connectors.InputAttachment{attachment},
			ActionResponses: []string{
				actionFinishWithReplyPart(
					"이미지를 상세하게 설명드렸습니다.",
					"이미지에는 흰색 고양이 형태의 김인턴 마스코트 인형이 서 있습니다. 얼굴에는 검은색으로 윙크하는 눈과 동그란 눈, 작은 입 모양이 붙어 있고, 목에는 '김인턴'이라고 적힌 이름표가 걸려 있습니다. 흰 셔츠와 청바지, 운동화를 착용했고 검은 가방끈과 꼬리가 보여 캐릭터 상품처럼 연출된 사진입니다.",
				),
			},
			ExpectedToolCallCounts: map[string]int{
				"image.read":    0,
				"terminal.run":  0,
				"document.read": 0,
			},
			ExpectedModelContexts: []string{
				"materialID=mattermost:file-current",
				"mascot.png",
			},
			ExpectedReplyFragments: []string{"흰색 고양이", "김인턴", "이름표"},
			ForbiddenReplyFragments: []string{
				"상세하게 설명드렸습니다",
			},
			MinimumReplyLength: 80,
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
			ActionResponses: []string{
				actionCallTool("google.drive.import_pptx", `{"path":"deck.pptx"}`),
				actionNoToolFallbackFinishMessage("Google Workspace 도구는 노출되지 않아 호출이 거부되었습니다. 로컬 첨부 경로만 사용할 수 있습니다."),
			},
			ExpectedToolCalls:      []string{"google.drive.import_pptx"},
			ExpectedReplyFragments: []string{"거부"},
		}},
	}
}

func ScheduleCreateAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "schedule_create_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{scheduledTaskSkill()},
		AllowedTools:          []string{"conversation.history", "memory.search", "schedule.create", "schedule.cancel"},
		Turns: []VirtualTurn{{
			Prompt: "1분마다 \"1분 지났습니다\"라고 보내줘",
			ActionResponses: []string{
				actionCallTool("schedule.create", `{"name":"1분 알림","prompt":"1분 지났습니다","executionMode":"message","kind":"interval","intervalSecond":60,"maxRunCount":10,"timeZone":"Asia/Seoul"}`),
				actionFinishMessage("1분마다 알림을 보내도록 예약해둘게요.", "obs-001:schedule.create:0"),
			},
			ExpectedSelectedSkills: []string{"scheduled-task"},
			ExpectedToolCalls:      []string{"schedule.create"},
			ExpectedEvents:         []string{"schedule.created"},
			ExpectedModelContexts:  []string{"scheduled-task", "schedule.create", "executionMode message", "1분마다"},
			ForbiddenReplyFragments: []string{
				"죄송",
				"제공하고 있지",
				"기능은 제공",
				"못합니다",
			},
		}},
	}
}

func scheduledTaskSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:        "scheduled-task",
		Description: "Create or cancel scheduled, recurring, and finite repeated reminders, messages, reports, and follow-up tasks with schedule tools.",
		WhenToUse:   "Use when the user asks to schedule, remind, repeat, cancel schedules, stop reminders, send something every minute/hour/day/week/month, repeat N times, send a finite repeated message, or says 예약, 알림, 리마인드, 취소, 중지, 마다, 분마다, 시간마다, 한 번씩, 1분에 한 번씩, 10번, 매일, 매주, or 매월.",
		Category:    "automation",
		Tags:        []string{"schedule", "reminder", "cron"},
		Prompt:      "Use schedule.create to create schedules. Use executionMode message when the scheduled run should send the prompt verbatim, such as reminders, repeated messages, or say/send this exact text requests. Use executionMode agent only for schedules that need reasoning, research, checks, summaries, or tool work at run time. For repeated reminders like every minute, use kind interval with intervalSecond. Set maxRunCount for finite repeats like 10번 or repeat N times. Do not claim background loops are unsupported when schedule.create is available.",
		Activation: agent.SkillActivation{
			Keywords: []string{"schedule", "scheduled", "cron", "remind", "reminder", "예약", "알림", "리마인드", "마다", "분마다", "시간마다", "매일", "매주", "매월"},
		},
		Completion: agent.SkillCompletion{
			RequiredEvidenceTools: []string{"schedule.create"},
		},
		AllowedTools: []string{"schedule.create", "schedule.cancel"},
		TriggerHints: []string{"schedule", "scheduled", "cron", "remind", "reminder", "예약", "알림", "리마인드", "마다", "분마다", "시간마다", "매일", "매주", "매월"},
		Source: agent.InstructionSource{
			Path:      "skills/scheduled-task/SKILL.md",
			SkillName: "scheduled-task",
			ByteSize:  512,
			SHA256:    "virtual-scheduled-task",
		},
	}
}

func SitePrototypeAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "site_prototype_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{sitePrototypeSkill()},
		AllowedTools:          append([]string{"conversation.history", "memory.search"}, sitePrototypeToolNames()...),
		CapabilityToolNames:   sitePrototypeCapabilityToolNames(),
		Turns: []VirtualTurn{{
			Prompt: "웹사이트 하나 만들어서 배포해봐",
			ActionResponses: []string{
				actionCallTool("site.app.create", `{"slug":"demo","title":"Demo Website"}`),
				actionCallTool("terminal.run", `{"command":"mkdir -p dist && printf 'demo site' > dist/index.html","workingDirectoryPath":"home/sites/site-1/app","timeoutSecond":30}`),
				actionCallTool("site.app.publish", `{"siteID":"site-1","message":"Initial demo website"}`),
				actionFinishMessage("웹사이트 프로토타입을 배포했습니다: https://demo.device.example.test", "obs-003:site.app.publish:0"),
			},
			ExpectedSelectedSkills: []string{"site-prototype"},
			ExpectedToolCalls:      []string{"site.app.create", "terminal.run", "site.app.publish"},
			ExpectedModelContexts:  []string{"site-prototype", "site.app.create", "site.app.publish", "appWorkspacePath", "웹사이트 하나"},
			ExpectedReplyFragments: []string{"https://demo.device.example.test"},
			ForbiddenReplyFragments: []string{
				"죄송",
				"완료하지 못",
				"기능은 제공",
				"오류가 발생",
				"다시 한번",
			},
		}},
	}
}

func simpleSlidesSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:        "simple-slides",
		Description: "Create local presentation decks with PPTX, PDF, HTML, and notes attachments.",
		Category:    "document-generation",
		Tags:        []string{"slides", "pptx", "presentation"},
		Prompt:      "Write Stitch-compatible DESIGN.md and Marp presentation.md directly under tmp/<deck-slug> from the user request. Treat presentation.md as the deck source of truth and iterate on it when needed. Use Paperlogy/Freesentation/Pretendard/Noto Sans KR font guidance, choose layouts from the content intent, include design-source: DESIGN.md, run NAME=<deck-slug> /workspace/skills/simple-slides/scripts/build.sh with workingDirectoryPath tmp/<deck-slug> for a full deck or FORMATS=html NAME=<deck-slug> /workspace/skills/simple-slides/scripts/build.sh for html-only requests, promote build outputs with file.promote, then file.attach only promoted generated files. Do not use Google Workspace unless a google tool is explicitly available.",
		Activation: agent.SkillActivation{
			Keywords: []string{"피피티", "파워포인트", "발표자료", "pptx", "google slides", "구글 슬라이드"},
		},
		AllowedTools: []string{"file.write", "terminal.run", "file.promote", "file.attach"},
		TriggerHints: []string{"피피티", "파워포인트", "발표자료", "pptx", "google slides", "구글 슬라이드"},
		Source: agent.InstructionSource{
			Path:      "skills/simple-slides/SKILL.md",
			SkillName: "simple-slides",
			ByteSize:  512,
			SHA256:    "virtual-simple-slides",
		},
	}
}

func sitePrototypeSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:        "site-prototype",
		Description: "Create, publish, update, take down, restore, or delete free React and PocketBase website prototypes through InternKim site.app tools.",
		Category:    "site-prototype",
		Tags:        []string{"website", "prototype", "deploy"},
		Prompt:      "Create and publish website prototypes. For a new prototype, call site.app.create with a DNS-safe slug and title, build the frontend inside the returned site workspace, call terminal.run to build or verify the app, then call site.app.publish with the siteID and a concise message. Never claim deployment succeeded until site.app.publish succeeds.",
		Activation: agent.SkillActivation{
			Keywords: []string{"웹사이트", "배포", "사이트", "web app", "website", "prototype"},
		},
		AllowedTools: sitePrototypeToolNames(),
		TriggerHints: []string{"웹사이트", "배포", "사이트", "web app", "website", "prototype"},
		Source: agent.InstructionSource{
			Path:      "skills/site-prototype/SKILL.md",
			SkillName: "site-prototype",
			ByteSize:  512,
			SHA256:    "virtual-site-prototype",
		},
	}
}

func sitePrototypeToolNames() []string {
	return []string{
		"terminal.run",
		"file.read",
		"file.write",
		"file.edit",
		"file.patch",
		"site.app.create",
		"site.app.publish",
		"site.app.status",
		"site.app.logs",
		"site.app.rollback",
		"site.app.unpublish",
		"site.app.restore",
		"site.app.delete",
		"user.confirm",
	}
}

func sitePrototypeCapabilityToolNames() []string {
	return []string{
		"site.app.create",
		"site.app.publish",
		"site.app.status",
		"site.app.logs",
		"site.app.rollback",
		"site.app.unpublish",
		"site.app.restore",
		"site.app.delete",
		"user.confirm",
	}
}
