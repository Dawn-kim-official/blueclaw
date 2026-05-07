package e2e

import "blueclaw/internal/agent"

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
				{Name: "tool.terminal.run.requested", BodyFragment: "./build.sh", Count: 1},
				{Name: "tool.terminal.run.result", BodyFragment: "Building requested formats", Count: 1},
				{Name: "tool.terminal.run.result", BodyFragment: "Slide render review", Count: 1},
				{Name: "tool.file.attach.result", BodyFragment: `"isError":false`, Count: 1},
			},
			ExpectedEvents:      []string{"agent.validity_review"},
			ExpectedAttachments: []string{".pptx", ".pdf", ".html", "-notes.txt"},
			ExpectedWorkspaceFiles: []VirtualWorkspaceFileExpectation{
				{
					PathGlob:          ".blueclaw/tmp/*/DESIGN.md",
					ContainsFragments: []string{"colors:", "Visual direction"},
				},
				{
					PathGlob:           ".blueclaw/tmp/*/presentation.md",
					ContainsFragments:  []string{"design-source: DESIGN.md", "InternKim capability deck", "너 뭐 할 수 있는지"},
					ForbiddenFragments: []string{"Draft a presentation deck", "user_request:"},
				},
				{
					PathGlob:          ".blueclaw/tmp/*/review/slide-review.json",
					ContainsFragments: []string{`"passed": true`, `"safeMargin": true`, `"edgeOverflow": true`, `"contactSheets"`},
				},
				{
					PathGlob:          ".blueclaw/tmp/*/*.html",
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

func ScheduleCreateAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "schedule_create_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{scheduledTaskSkill()},
		AllowedTools:          []string{"conversation.history", "memory.search", "schedule.create"},
		Turns: []VirtualTurn{{
			Prompt: "1분마다 \"1분 지났습니다\"라고 보내줘",
			ModelResponses: []string{
				actionCallTool("schedule.create", `{"name":"1분 알림","prompt":"1분 지났습니다라고 보내줘.","kind":"interval","intervalSecond":60,"timeZone":"Asia/Seoul"}`),
				actionFinalReply("1분마다 알림을 보내도록 예약해둘게요."),
			},
			ExpectedSelectedSkills: []string{"scheduled-task"},
			ExpectedToolCalls:      []string{"schedule.create"},
			ExpectedEvents:         []string{"schedule.created"},
			ExpectedModelContexts:  []string{"scheduled-task", "schedule.create", "Create a scheduled agent task", "1분마다"},
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
		Description: "Create recurring reminders, periodic reports, and future follow-up tasks with schedule.create.",
		WhenToUse:   "Use when the user asks to schedule, remind, repeat, send something every minute/hour/day/week/month, or says 예약, 알림, 리마인드, 마다, 분마다, 시간마다, 매일, 매주, or 매월.",
		Category:    "automation",
		Tags:        []string{"schedule", "reminder", "cron"},
		Prompt:      "Use schedule.create to create scheduled agent tasks. For repeated reminders like every minute, use kind interval with intervalSecond. Put the future task instruction in prompt. Do not claim background loops are unsupported when schedule.create is available.",
		Activation: agent.SkillActivation{
			Keywords: []string{"schedule", "scheduled", "cron", "remind", "reminder", "예약", "알림", "리마인드", "마다", "분마다", "시간마다", "매일", "매주", "매월"},
		},
		AllowedTools: []string{"schedule.create"},
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
		AllowedTools:          append([]string{"conversation.history", "memory.search", "schedule.create"}, sitePrototypeToolNames()...),
		CapabilityToolNames:   sitePrototypeCapabilityToolNames(),
		Turns: []VirtualTurn{{
			Prompt: "웹사이트 하나 만들어서 배포해봐",
			ModelResponses: []string{
				actionCallTool("site.app.create", `{"slug":"demo","title":"Demo Website"}`),
				actionCallTool("terminal.run", `{"command":"mkdir -p sites/site-1/app/dist && printf '<!doctype html><html><body>demo site</body></html>' > sites/site-1/app/dist/index.html","workingDirectoryPath":"/workspace","timeoutSecond":30}`),
				actionCallTool("site.app.publish", `{"siteID":"site-1","message":"Initial demo website"}`),
				actionFinalReply("웹사이트 프로토타입을 배포했습니다: https://demo.device.intern.kim"),
			},
			ExpectedSelectedSkills: []string{"site-prototype"},
			ExpectedToolCalls:      []string{"site.app.create", "terminal.run", "site.app.publish"},
			ExpectedModelContexts:  []string{"site-prototype", "site.app.create", "site.app.publish", "웹사이트 하나"},
			ForbiddenModelContexts: []string{"schedule.create"},
			ExpectedReplyFragments: []string{"https://demo.device.intern.kim"},
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
		Prompt:      "Write Stitch-compatible DESIGN.md and Marp presentation.md directly from the user request. Treat presentation.md as the deck source of truth and iterate on it when needed. Use Paperlogy/Freesentation/Pretendard/Noto Sans KR font guidance, choose layouts from the content intent, include design-source: DESIGN.md, copy build.sh/extract_notes.py/render_review.py into the deck directory, run NAME=<deck-slug> ./build.sh for a full deck or FORMATS=html NAME=<deck-slug> ./build.sh for html-only requests, then file.attach only the requested generated files. Do not use Google Workspace unless a google tool is explicitly available.",
		Activation: agent.SkillActivation{
			Keywords: []string{"피피티", "파워포인트", "발표자료", "pptx", "google slides", "구글 슬라이드"},
		},
		AllowedTools: []string{"file.write", "terminal.run", "file.attach"},
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
		"file.write",
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
