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

func PlainQuestionAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "plain_question_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Turns: []VirtualTurn{{
			Prompt: "도구 없이 짧게 답해줘. 좋은 회의록의 핵심은 뭐야?",
			ActionResponses: []string{
				actionFinishMessage("좋은 회의록의 핵심은 결정사항, 담당자, 기한을 분명히 남기는 것입니다."),
			},
			ExpectedReplyFragments: []string{"결정사항", "담당자", "기한"},
		}},
	}
}

func WebSearchAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "web_search_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "web.search"},
		CapabilityToolNames:   []string{"web.search"},
		Turns: []VirtualTurn{{
			Prompt: "오늘 기준으로 외부 검색이 필요한 정보를 찾아서 핵심만 알려줘",
			ActionResponses: []string{
				`{"action":"select_tools","toolNames":["web.search"],"skillNames":[],"reason":"외부 최신 정보 검색이 필요합니다."}`,
				actionCallTool("web.search", `{"query":"current external information acceptance test","limit":1}`),
				actionFinishMessage("검색 결과 BlueclawSearchStubToken 정보를 확인했습니다.", "obs-002:web.search:0"),
			},
			ExpectedToolCalls:      []string{"web.search"},
			ExpectedReplyFragments: []string{"BlueclawSearchStubToken"},
		}},
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
				Speaker:            "동하",
				SpeakerCallingName: "동하 님",
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
				"platform.message.send",
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
				Speaker:            "동하",
				SpeakerCallingName: "동하 님",
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
				actionCallTool("schedule.create", `{"name":"1분 알림","taskInstruction":"현재 대화에 \"1분 지났습니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}`),
				actionFinishMessage("1분마다 알림을 보내도록 예약해둘게요.", "obs-001:schedule.create:0"),
			},
			ExpectedSelectedSkills: []string{"scheduled-task"},
			ExpectedToolCalls:      []string{"schedule.create"},
			ExpectedEvents:         []string{"schedule.created"},
			ExpectedModelContexts:  []string{"scheduled-task", "schedule.create", "taskInstruction", "1분마다"},
			ForbiddenReplyFragments: []string{
				"죄송",
				"제공하고 있지",
				"기능은 제공",
				"못합니다",
			},
		}},
	}
}

func ScheduleLifecycleAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "schedule_lifecycle_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{scheduledTaskSkill()},
		AllowedTools:          []string{"conversation.history", "memory.search", "schedule.create", "schedule.cancel"},
		Turns: []VirtualTurn{
			{
				Prompt: "30분마다 상태 확인하라고 알려줘. 세 번만 해줘",
				ActionResponses: []string{
					actionCallTool("schedule.create", `{"name":"상태 확인 알림","taskInstruction":"현재 대화에 \"상태를 확인하세요\"라고 보낸다.","kind":"interval","intervalSecond":1800,"maxRunCount":3,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}`),
					actionFinishMessage("30분마다 세 번 상태 확인 알림을 보내도록 예약해둘게요.", "obs-001:schedule.create:0"),
				},
				ExpectedSelectedSkills: []string{"scheduled-task"},
				ExpectedToolCalls:      []string{"schedule.create"},
				ExpectedEvents:         []string{"schedule.created"},
				ExpectedModelContexts:  []string{"scheduled-task", "schedule.create", "taskInstruction", "30분마다"},
			},
			{
				Prompt: "그 예약을 1시간마다 다섯 번으로 바꿔줘",
				ActionResponses: []string{
					actionCallTool("schedule.cancel", `{"scope":"mine"}`),
					actionCallTool("schedule.create", `{"name":"상태 확인 알림","taskInstruction":"현재 대화에 \"상태를 확인하세요\"라고 보낸다.","kind":"interval","intervalSecond":3600,"maxRunCount":5,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}`),
					actionFinishMessage("기존 예약을 취소하고 1시간마다 다섯 번으로 다시 예약했습니다.", "obs-001:schedule.cancel:0", "obs-002:schedule.create:0"),
				},
				ExpectedToolCalls: []string{"schedule.cancel", "schedule.create"},
				ExpectedEvents:    []string{"schedule.cancelled", "schedule.created"},
			},
			{
				Prompt: "그 예약 삭제해줘",
				ActionResponses: []string{
					actionCallTool("schedule.cancel", `{"scope":"mine"}`),
					actionFinishMessage("예약을 삭제했습니다.", "obs-001:schedule.cancel:0"),
				},
				ExpectedToolCalls: []string{"schedule.cancel"},
				ExpectedEvents:    []string{"schedule.cancelled"},
			},
		},
	}
}

func CalendarEventLifecycleAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "calendar_event_lifecycle_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		RouterWorkKinds:       []string{agent.WorkKindCalendar},
		Skills:                []agent.SkillInstruction{calendarSkill()},
		AllowedTools:          []string{"conversation.history", "memory.search", "calendar.event.add", "calendar.event.update", "calendar.event.delete"},
		CapabilityToolNames:   []string{"calendar.event.add", "calendar.event.update", "calendar.event.delete"},
		Turns: []VirtualTurn{
			{
				Prompt: "내일 오전 10시에 제품 회고 일정을 캘린더에 추가해줘",
				ActionResponses: []string{
					actionCallTool("calendar.event.add", `{"title":"제품 회고","startISO":"2026-06-13T10:00:00+09:00","endISO":"2026-06-13T11:00:00+09:00","timeZone":"Asia/Seoul"}`),
					actionFinishMessage("내일 오전 10시에 제품 회고 일정을 추가했습니다.", "obs-001:calendar.event.add:0"),
				},
				ExpectedSelectedSkills: []string{"calendar"},
				ExpectedToolCalls:      []string{"calendar.event.add"},
				ExpectedToolCallCounts: map[string]int{
					"calendar.event.add":    1,
					"calendar.event.update": 0,
					"calendar.event.delete": 0,
				},
			},
			{
				Prompt: "그 일정을 내일 오후 2시로 바꿔줘",
				ActionResponses: []string{
					actionCallTool("calendar.event.update", `{"eventID":"calendar-event-001","startISO":"2026-06-13T14:00:00+09:00","endISO":"2026-06-13T15:00:00+09:00","timeZone":"Asia/Seoul"}`),
					actionFinishMessage("제품 회고 일정을 내일 오후 2시로 변경했습니다.", "obs-001:calendar.event.update:0"),
				},
				ExpectedToolCalls: []string{"calendar.event.update"},
				ExpectedToolCallCounts: map[string]int{
					"calendar.event.add":    0,
					"calendar.event.update": 1,
					"calendar.event.delete": 0,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.calendar.event.update.requested", BodyFragment: "2026-06-13T14:00:00+09:00", Count: 1},
				},
			},
			{
				Prompt: "그 일정 삭제해줘",
				ActionResponses: []string{
					actionCallTool("calendar.event.delete", `{"eventID":"calendar-event-001"}`),
					actionFinishMessage("제품 회고 일정을 삭제했습니다.", "obs-001:calendar.event.delete:0"),
				},
				ExpectedToolCalls: []string{"calendar.event.delete"},
				ExpectedToolCallCounts: map[string]int{
					"calendar.event.add":    0,
					"calendar.event.update": 0,
					"calendar.event.delete": 1,
				},
				ExpectedReplyFragments: []string{"삭제했습니다"},
			},
		},
	}
}

func SkillLifecycleAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	skillName := "memo-helper"
	skillContent := userManagedSkillDocument(skillName)
	return VirtualSessionScenario{
		Name:                  "skill_lifecycle_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "skill.add", "skill.remove"},
		Turns: []VirtualTurn{
			{
				Prompt: "간단한 메모 정리 custom skill을 등록해줘",
				ActionResponses: []string{
					actionSelectTools("skill.add"),
					actionCallTool("skill.add", skillAddToolInput(skillName, skillContent)),
					actionFinishMessage("memo-helper skill을 등록했습니다.", "obs-002:skill.add:0"),
				},
				ExpectedToolCalls: []string{"skill.add"},
				ExpectedToolCallCounts: map[string]int{
					"skill.add":    1,
					"skill.remove": 0,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.skill.add.result", BodyFragment: "created", Count: 1},
				},
				ExpectedWorkspaceFiles: []VirtualWorkspaceFileExpectation{{
					PathGlob:          ".agents/skills/memo-helper/SKILL.md",
					ContainsFragments: []string{"name: memo-helper", "Memo helper organizes short notes"},
				}},
				ExpectedReplyFragments: []string{"memo-helper", "등록"},
			},
			{
				Prompt: "방금 등록한 memo-helper skill 삭제해줘",
				ActionResponses: []string{
					actionSelectTools("skill.remove"),
					actionCallTool("skill.remove", `{"name":"memo-helper"}`),
					actionFinishMessage("memo-helper skill을 삭제했습니다.", "obs-002:skill.remove:0"),
				},
				ExpectedToolCalls: []string{"skill.remove"},
				ExpectedToolCallCounts: map[string]int{
					"skill.add":    0,
					"skill.remove": 1,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.skill.remove.result", BodyFragment: "removed", Count: 1},
				},
				ExpectedReplyFragments: []string{"memo-helper", "삭제"},
			},
		},
	}
}

func CapabilityQuestionAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "capability_question_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{simpleSlidesSkill(), scheduledTaskSkill(), sitePrototypeSkill()},
		AllowedTools:          []string{"conversation.history", "memory.search", "skill.search"},
		Turns: []VirtualTurn{{
			Prompt: "너는 무엇을 할 수 있어?",
			ActionResponses: []string{
				actionCallTool("skill.search", `{}`),
				actionFinishMessage("사용 가능한 skill에는 simple-slides, scheduled-task, site-prototype이 있습니다.", "obs-001:skill.search:0"),
			},
			ExpectedToolCalls: []string{"skill.search"},
			ExpectedToolCallCounts: map[string]int{
				"skill.search": 1,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.skill.search.result", BodyFragment: "simple-slides", Count: 1},
			},
			ExpectedReplyFragments: []string{"simple-slides"},
		}},
	}
}

func TaskHistoryQuestionAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "task_history_question_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "task.history"},
		Turns: []VirtualTurn{
			{
				Prompt: "계약서 확인 요약 작업을 완료했다고 답해줘",
				ActionResponses: []string{
					actionFinishMessage("계약서 확인 요약 작업을 완료했습니다."),
				},
				ExpectedToolCallCounts: map[string]int{
					"task.history": 0,
				},
				ExpectedReplyFragments: []string{"계약서 확인 요약", "완료"},
			},
			{
				Prompt: "최근에 어떤 작업을 했는지 알려줘",
				ActionResponses: []string{
					actionSelectTools("task.history"),
					actionCallTool("task.history", `{}`),
					actionFinishMessage("최근에는 계약서 확인 요약 작업을 완료했습니다.", "obs-002:task.history:0"),
				},
				ExpectedToolCalls: []string{"task.history"},
				ExpectedToolCallCounts: map[string]int{
					"task.history": 1,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.task.history.result", BodyFragment: "계약서 확인 요약 작업", Count: 1},
				},
				ExpectedReplyFragments: []string{"계약서 확인 요약"},
			},
		},
	}
}

func MemoryExplicitToolAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "memory_explicit_tool_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "memory.remember"},
		Turns: []VirtualTurn{
			{
				Prompt: "Please remember that my preferred language is Korean.",
				ActionResponses: []string{
					actionCallTool("memory.remember", `{"content":"preferred language is Korean"}`),
					actionFinishMessage("Remembered: your preferred language is Korean.", "obs-001:memory.remember:0"),
				},
				ExpectedToolCalls: []string{"memory.remember"},
				ExpectedToolCallCounts: map[string]int{
					"memory.remember": 1,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.memory.remember.requested", BodyFragment: "Korean", Count: 1},
				},
				ExpectedReplyFragments: []string{"Korean"},
			},
			{
				Prompt: "What language do I prefer?",
				ActionResponses: []string{
					actionSelectTools("memory.search"),
					actionCallTool("memory.search", `{"query":"preferred language"}`),
					actionFinishMessage("Your preferred language is Korean.", "obs-002:memory.search:0"),
				},
				ExpectedToolCalls: []string{"memory.search"},
				ExpectedToolCallCounts: map[string]int{
					"memory.search": 1,
				},
				ExpectedReplyFragments: []string{"Korean"},
			},
		},
	}
}

func FailureExplanationAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "failure_explanation_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "terminal.run", "task.history"},
		TurnOptions: agent.TurnOptions{
			RecoveryBudget: agent.RecoveryBudget{
				CorrectedRetry: -1,
				AlternateRoute: -1,
				AdjacentTool:   -1,
				NoToolFallback: -1,
			},
		},
		Turns: []VirtualTurn{
			{
				Prompt: "Run the analysis.",
				ActionResponses: []string{
					actionSelectTools("terminal.run"),
					actionCallTool("terminal.run", `{"command":"printf 'permission denied blocked_by_captcha' >&2; exit 126","workingDirectoryPath":"home","timeoutSecond":30}`),
					actionFailMessage("terminal.run: permission denied"),
				},
				ExpectedToolCalls: []string{"terminal.run"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.terminal.run.result", BodyFragment: "permission denied", Count: 1},
				},
			},
			{
				Prompt: "왜 실패했어?",
				ActionResponses: []string{
					actionSelectTools("task.history"),
					actionCallTool("task.history", `{"status":"failed"}`),
					actionFinishMessage("terminal.run 실행이 permission denied 때문에 실패했습니다.", "obs-002:task.history:0"),
				},
				ExpectedToolCalls: []string{"task.history"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.task.history.result", BodyFragment: "terminal.run: permission denied", Count: 1},
				},
				ExpectedReplyFragments: []string{"permission denied"},
			},
		},
	}
}

func OneTimeScheduleAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "one_time_schedule_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{scheduledTaskSkill()},
		AllowedTools:          []string{"conversation.history", "memory.search", "schedule.create", "schedule.cancel"},
		Turns: []VirtualTurn{{
			Prompt: "2027년 1월 15일 오전 9시에 계약서 확인 알림을 한 번만 예약해줘",
			ActionResponses: []string{
				actionCallTool("schedule.create", `{"name":"계약서 확인 알림","taskInstruction":"현재 대화에 \"계약서를 확인하세요\"라고 보낸다.","kind":"once","runAt":"2027-01-15T00:00:00Z","timeZone":"Asia/Seoul"}`),
				actionFinishMessage("2027년 1월 15일 오전 9시에 한 번 알림을 보내도록 예약해둘게요.", "obs-001:schedule.create:0"),
			},
			ExpectedSelectedSkills: []string{"scheduled-task"},
			ExpectedToolCalls:      []string{"schedule.create"},
			ExpectedEvents:         []string{"schedule.created"},
			ExpectedModelContexts:  []string{"scheduled-task", "schedule.create", "runAt", "once"},
			ExpectedReplyFragments: []string{"2027년 1월 15일", "한 번"},
		}},
	}
}

func skillAddToolInput(skillName string, skillContent string) string {
	return `{"name":` + quote(skillName) + `,"content":` + quote(skillContent) + `}`
}

func userManagedSkillDocument(skillName string) string {
	return `---
name: ` + skillName + `
description: Memo helper organizes short notes and extracts action items from source text.
when_to_use: Use when the user wants notes organized into concise memos.
---
Organize notes into concise memos with action items and owners.`
}

func calendarSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:         "calendar",
		Description:  "Create, update, and delete calendar events.",
		WhenToUse:    "Use for calendar event creation, updates, deletion, 일정, 달력, 캘린더, and meeting time changes.",
		Prompt:       "Use calendar.event.add to create calendar events, calendar.event.update to edit event time or details, and calendar.event.delete to delete events.",
		Category:     "calendar",
		Tags:         []string{"calendar", "event"},
		AllowedTools: []string{"calendar.event.add", "calendar.event.update", "calendar.event.delete"},
		TriggerHints: []string{"calendar", "event", "일정", "달력", "캘린더", "meeting"},
		Source: agent.InstructionSource{
			Path:      "skills/calendar/SKILL.md",
			SkillName: "calendar",
			ByteSize:  512,
			SHA256:    "virtual-calendar",
		},
	}
}

func scheduledTaskSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:        "scheduled-task",
		Description: "Create or cancel scheduled, recurring, and finite repeated reminders, messages, reports, and follow-up tasks with schedule tools.",
		WhenToUse:   "Use when the user asks to schedule, remind, repeat, cancel schedules, stop reminders, send something every minute/hour/day/week/month, repeat N times, send a finite repeated message, or says 예약, 알림, 리마인드, 취소, 중지, 마다, 분마다, 시간마다, 한 번씩, 1분에 한 번씩, 10번, 매일, 매주, or 매월.",
		Category:    "automation",
		Tags:        []string{"schedule", "reminder", "cron"},
		Prompt:      "Use schedule.create to create schedules. Put only the run-time work in taskInstruction. Put cadence and stop conditions in structured fields such as runAt, intervalSecond, cronExpression, expiresAt, and maxRunCount. Set repeatPolicy finite with expiresAt or maxRunCount for finite repeats; set repeatPolicy unbounded only when the user explicitly asks for no end. Do not claim background loops are unsupported when schedule.create is available.",
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
		RouterWorkKinds:       []string{agent.WorkKindSitePrototype},
		Skills:                []agent.SkillInstruction{sitePrototypeSkill()},
		AllowedTools:          append([]string{"conversation.history", "memory.search"}, sitePrototypeToolNames()...),
		CapabilityToolNames:   sitePrototypeCapabilityToolNames(),
		Turns: []VirtualTurn{{
			Prompt: "웹사이트 하나 만들어서 배포해봐",
			ActionResponses: []string{
				actionCallTool("site.app.create", `{"slug":"demo","title":"Demo Website"}`),
				actionCallTool("terminal.run", `{"command":"mkdir -p dist && printf 'demo site' > dist/index.html","workingDirectoryPath":"home/sites/site-1/app","timeoutSecond":30}`),
				actionCallTool("site.app.publish", `{"siteID":"site-1","message":"Initial demo website"}`),
				actionFinishMessage("웹사이트 프로토타입을 배포했습니다: https://demo.device.intern.kim", "obs-003:site.app.publish:0"),
			},
			ExpectedSelectedSkills: []string{"site-prototype"},
			ExpectedToolCalls:      []string{"site.app.create", "terminal.run", "site.app.publish"},
			ExpectedModelContexts:  []string{"site-prototype", "site.app.create", "site.app.publish", "appWorkspacePath", "웹사이트 하나"},
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

func SiteEditRedeployAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "site_edit_redeploy_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		RouterWorkKinds:       []string{agent.WorkKindSitePrototype},
		Skills:                []agent.SkillInstruction{sitePrototypeSkill()},
		AllowedTools:          append([]string{"conversation.history", "memory.search"}, sitePrototypeToolNames()...),
		CapabilityToolNames:   sitePrototypeCapabilityToolNames(),
		Turns: []VirtualTurn{
			{
				Prompt: "Build and deploy a simple site.",
				ActionResponses: []string{
					actionCallTool("site.app.create", `{"slug":"demo","title":"Demo Website"}`),
					actionCallTool("file.write", `{"path":"home/sites/site-1/app/index.html","content":"<!doctype html>\n<html>\n<body>\n<h1>Simple Site</h1>\n</body>\n</html>\n"}`),
					actionCallTool("terminal.run", `{"command":"mkdir -p dist && cp index.html dist/index.html","workingDirectoryPath":"home/sites/site-1/app","timeoutSecond":30}`),
					actionCallTool("site.app.publish", `{"siteID":"site-1","message":"Initial simple site"}`),
					actionFinishMessage("Deployed the simple site: https://demo.device.intern.kim", "obs-004:site.app.publish:0"),
				},
				ExpectedSelectedSkills: []string{"site-prototype"},
				ExpectedToolCalls:      []string{"file.write", "terminal.run", "site.app.publish"},
				ExpectedReplyFragments: []string{"https://demo.device.intern.kim"},
			},
			{
				Prompt: "Update the heading to say Hello World.",
				ActionResponses: []string{
					actionSelectTools("file.edit", "terminal.run", "site.app.publish"),
					actionCallTool("file.edit", `{"path":"home/sites/site-1/app/index.html","oldText":"Simple Site","newText":"Hello World"}`),
					actionCallTool("terminal.run", `{"command":"mkdir -p dist && cp index.html dist/index.html","workingDirectoryPath":"home/sites/site-1/app","timeoutSecond":30}`),
					actionCallTool("site.app.publish", `{"siteID":"site-1","message":"Update heading to Hello World"}`),
					actionFinishMessage("Updated and redeployed the site: https://demo.device.intern.kim", "obs-004:site.app.publish:0"),
				},
				ExpectedToolCalls: []string{"file.edit", "terminal.run", "site.app.publish"},
				ExpectedToolCallCounts: map[string]int{
					"file.edit":        1,
					"terminal.run":     1,
					"site.app.publish": 1,
				},
				ExpectedReplyFragments: []string{"https://demo.device.intern.kim"},
			},
		},
	}
}

func AskChoiceReplyAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "ask_choice_reply_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "ask.choice"},
		Turns: []VirtualTurn{{
			Prompt: "둘 중 하나 고르게 해줘",
			ActionResponses: []string{
				actionCallTool("ask.choice", `{"question":"어느 쪽으로 진행할까요?","options":["첫 번째","두 번째"],"recommendedOptionKey":"1","selectionMode":"single"}`),
			},
			ExpectedToolCalls:      []string{"ask.choice"},
			ExpectedEvents:         []string{"ask.requested"},
			ExpectedReplyFragments: []string{"어느 쪽으로 진행할까요?"},
			ExpectedModelContexts:  []string{`"options":{"items":{"type":"string"},"type":"array"}`, `"recommendedOptionKey"`},
		}, {
			Prompt: "2",
			ActionResponses: []string{
				actionFinishMessage("두 번째로 진행하겠습니다."),
			},
			ExpectedEvents:         []string{"ask.resolved"},
			ExpectedReplyFragments: []string{"두 번째"},
			ExpectedModelContexts:  []string{"User selected: 2 / 두 번째"},
		}},
	}
}

func AskConfirmReplyAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "ask_confirm_reply_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "ask.confirm"},
		Turns: []VirtualTurn{{
			Prompt: "진행 전에 확인 받아줘",
			ActionResponses: []string{
				actionCallTool("ask.confirm", `{"userFacingMessage":"이대로 진행할까요?","reasonCode":"external_send","reasonDetail":"confirmation acceptance test"}`),
			},
			ExpectedToolCalls:      []string{"ask.confirm"},
			ExpectedEvents:         []string{"confirmation.requested"},
			ExpectedReplyFragments: []string{"이대로 진행할까요?"},
		}, {
			Prompt: "확인",
			ActionResponses: []string{
				actionFinishMessage("확인되어 계속 진행하겠습니다."),
			},
			ExpectedEvents:         []string{"confirmation.reply_classified"},
			ExpectedReplyFragments: []string{"확인되어"},
		}},
	}
}

func DirectMessageSendConfirmAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "dm_send_confirm_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "ask.confirm", "platform.message.send"},
		CapabilityToolNames:   []string{"platform.message.send"},
		Turns: []VirtualTurn{{
			Prompt: "우경이한테 DM으로 오늘 오후 3시에 확인하자고 보내줘",
			ActionResponses: []string{
				`{"action":"select_tools","toolNames":["ask.confirm"],"skillNames":[],"reason":"외부 DM 전송에는 확인이 필요합니다."}`,
				actionCallTool("ask.confirm", `{"userFacingMessage":"우경이에게 DM을 보낼까요?","reasonCode":"external_send","reasonDetail":"우경이에게 오늘 오후 3시에 확인하자는 DM을 보냅니다."}`),
			},
			ExpectedToolCalls: []string{"ask.confirm"},
			ExpectedToolCallCounts: map[string]int{
				"platform.message.send": 0,
			},
			ExpectedEvents:         []string{"confirmation.requested"},
			ExpectedReplyFragments: []string{"우경이에게 DM을 보낼까요?"},
		}, {
			Prompt: "확인",
			ActionResponses: []string{
				`{"action":"select_tools","toolNames":["platform.message.send"],"skillNames":[],"reason":"승인 후 DM 전송 도구를 호출합니다."}`,
				actionCallTool("platform.message.send", `{"deliveryTarget":{"type":"directMessage","personHint":"우경"},"message":"오늘 오후 3시에 확인하자"}`),
				actionFinishMessage("우경이에게 DM을 보냈습니다.", "obs-002:platform.message.send:0"),
			},
			ExpectedToolCalls: []string{"platform.message.send"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.platform.message.send.requested", BodyFragment: `"type":"directMessage"`, Count: 1},
				{Name: "tool.platform.message.send.result", BodyFragment: "virtual-platform-message-001", Count: 1},
			},
			ExpectedEvents:         []string{"confirmation.reply_classified"},
			ExpectedModelContexts:  []string{"virtual-platform-message-001"},
			ExpectedReplyFragments: []string{"DM", "보냈습니다"},
		}},
	}
}

func ChannelPostAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "channel_post_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "platform.message.send"},
		CapabilityToolNames:   []string{"platform.message.send"},
		Turns: []VirtualTurn{{
			Prompt: "announcements 채널에 오늘 5시에 전체 공지 회의 있다고 올려줘",
			ActionResponses: []string{
				`{"action":"select_tools","toolNames":["platform.message.send"],"skillNames":[],"reason":"채널 공지를 게시하려면 플랫폼 메시지 전송 도구가 필요합니다."}`,
				actionCallTool("platform.message.send", `{"deliveryTarget":{"type":"channel","channelName":"announcements"},"message":"오늘 5시에 전체 공지 회의가 있습니다."}`),
				actionFinishMessage("announcements 채널에 공지를 올렸습니다.", "obs-002:platform.message.send:0"),
			},
			ExpectedToolCalls: []string{"platform.message.send"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.platform.message.send.requested", BodyFragment: `"type":"channel"`, Count: 1},
				{Name: "tool.platform.message.send.requested", BodyFragment: `"channelName":"announcements"`, Count: 1},
				{Name: "tool.platform.message.send.requested", BodyFragment: `"type":"directMessage"`, Count: 0},
			},
			ExpectedReplyFragments: []string{"채널", "올렸습니다"},
		}},
	}
}

func PlatformMessageEditAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "platform_message_edit_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "platform.message.update"},
		CapabilityToolNames:   []string{"platform.message.update"},
		Turns: []VirtualTurn{{
			Prompt: "방금 올린 공지 message virtual-platform-message-001 문구를 '오늘 오후 6시에 전체 공지 회의가 있습니다.'로 바꿔줘",
			ActionResponses: []string{
				`{"action":"select_tools","toolNames":["platform.message.update"],"skillNames":[],"reason":"최근 공지 메시지를 수정하려면 플랫폼 메시지 업데이트 도구가 필요합니다."}`,
				actionCallTool("platform.message.update", `{"messageID":"virtual-platform-message-001","text":"오늘 오후 6시에 전체 공지 회의가 있습니다."}`),
				actionFinishMessage("공지 메시지 문구를 수정했습니다.", "obs-002:platform.message.update:0"),
			},
			ExpectedToolCalls: []string{"platform.message.update"},
			ExpectedToolCallCounts: map[string]int{
				"platform.message.update": 1,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.platform.message.update.requested", BodyFragment: `"messageID":"virtual-platform-message-001"`, Count: 1},
				{Name: "tool.platform.message.update.requested", BodyFragment: `"text":"오늘 오후 6시에 전체 공지 회의가 있습니다."`, Count: 1},
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
