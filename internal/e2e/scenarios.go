package e2e

import (
	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/connectors"
	"blueclaw/internal/task"
)

func actionInvokeCapabilityTool(toolName string, input string) string {
	return actionCallTool(toolName, input)
}

func PresentationLocalMultiturnSuccessScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "presentation_local_multiturn_success",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{presentationSkill()},
		AllowedTools:          []string{"conversation.history", "memory.search", "terminal.run", "file.write", "file.attach"},
		Turns: []VirtualTurn{{
			Prompt:                 "너 뭐 할 수 있는지 8장 피피티 만들어서 보내줘봐",
			ExpectedSelectedSkills: []string{"presentation"},
			ExpectedToolCalls:      []string{"terminal.run", "file.attach"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.terminal.run.requested", BodyFragment: "NAME=", Count: 1},
				{Name: "tool.terminal.run.requested", BodyFragment: "/workspace/skills/presentation/scripts/build.sh", Count: 1},
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
		InitialToolNames:      []string{"web.search"},
		RouterTaskShape:       agent.TaskShapeResearchTask,
		Turns: []VirtualTurn{{
			Prompt:                 "오늘 기준으로 외부 검색이 필요한 정보를 찾아서 핵심만 알려줘",
			RouterRequiredEvidence: []string{"web.search"},
			ActionResponses: []string{
				actionCallTool("web.search", `{"query":"current external information acceptance test","limit":1}`),
				actionFinishMessage("검색 결과 BlueclawSearchStubToken 정보를 확인했습니다.", "obs-001:web.search:0"),
			},
			ExpectedToolCalls:      []string{"web.search"},
			ExpectedSequence:       []string{"tool.web.search.requested", "tool.web.search.result"},
			ForbiddenEvents:        []string{"agent.no_progress_loop_stopped"},
			ExpectedReplyFragments: []string{"BlueclawSearchStubToken"},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func ToolPermissionHidesSkillScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "tool_permission_hides_skill",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{presentationSkill()},
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

func FileWriteAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "file_write_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"file.write", "terminal.run"},
		InitialToolNames:      []string{"file.write", "terminal.run"},
		Turns: []VirtualTurn{{
			Prompt:                 "중간 JSON 파일을 만들고 터미널에서 읽히는지 확인해줘.",
			RouterRequiredEvidence: []string{"file.write"},
			ActionResponses: []string{
				actionCallTool("file.write", `{"path":"tmp/docx-guide/document.json","content":"{\"title\":\"readable\"}\n"}`),
				actionCallTool("terminal.run", `{"workingDirectoryPath":"tmp/docx-guide","command":"cat document.json","timeoutSecond":30}`),
				actionFinishMessage("파일을 생성하고 터미널에서 읽히는 것을 확인했습니다.", "obs-002:terminal.run:0"),
			},
			ExpectedToolCalls: []string{"file.write", "terminal.run"},
			ExpectedWorkspaceFiles: []VirtualWorkspaceFileExpectation{{
				PathGlob:          "private/people/person-1/tmp/docx-guide/document.json",
				ContainsFragments: []string{"readable"},
			}},
			ExpectedReplyFragments: []string{"확인"},
			ForbiddenReplyFragments: []string{
				"permission denied",
				"권한",
				"완료하지 못",
			},
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
				"mascot.png",
			},
			ForbiddenModelContexts: []string{
				"mail.message.search",
				"message.send",
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
				actionCallTool("file.preview", `{"path":"home/inbox/mattermost/thread-1/kim-intern-automation.html"}`),
				actionFinishMessage("첨부 HTML을 확인했습니다. 자동화 섹션의 정보 구조와 CTA를 더 선명하게 다듬으면 좋겠습니다.", "obs-001:file.preview:0"),
			},
			ExpectedToolCalls: []string{"file.preview"},
			ExpectedToolCallCounts: map[string]int{
				"terminal.run": 0,
				"file.read":    0,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.file.preview.requested", BodyFragment: `"path":"home/inbox/mattermost/thread-1/kim-intern-automation.html"`, Count: 1},
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
				actionCallTool("file.preview", `{"path":"home/inbox/mattermost/thread-1/kim-intern-automation.html"}`),
				actionFinishMessage("이전 첨부 HTML을 확인했습니다. 자동화 흐름의 핵심 CTA와 섹션 우선순위를 더 명확히 잡으면 좋겠습니다.", "obs-001:file.preview:0"),
			},
			ExpectedToolCalls: []string{"file.preview"},
			ExpectedToolCallCounts: map[string]int{
				"terminal.run": 0,
				"file.read":    0,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.file.preview.requested", BodyFragment: `"path":"home/inbox/mattermost/thread-1/kim-intern-automation.html"`, Count: 1},
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

func CodingImageVisionFallbackScenario(artifactDirectoryPath string) VirtualSessionScenario {
	attachment := connectors.InputAttachment{
		Platform:    "mattermost",
		FileID:      "file-code-shot",
		MessageID:   "virtual-message-001",
		Filename:    "login_handler.png",
		ContentType: "image/png",
		SizeBytes:   13,
	}
	return VirtualSessionScenario{
		Name:                     "coding_image_vision_fallback",
		ArtifactDirectoryPath:    artifactDirectoryPath,
		RouterTaskLevel:          "medium",
		CodingTierVisionFallback: true,
		AllowedTools:             []string{"conversation.history", "memory.search"},
		Turns: []VirtualTurn{{
			Prompt:           "이 스크린샷에 있는 로그인 핸들러 코드 리뷰하고 리팩터링 방향 알려줘.",
			InputAttachments: []connectors.InputAttachment{attachment},
			ActionResponses: []string{
				actionFinishMessage("스크린샷의 로그인 핸들러는 비밀번호를 평문 비교하고 에러를 한꺼번에 삼키고 있습니다. 비밀번호 검증은 상수 시간 해시 비교로 바꾸고, 인증 실패와 입력 검증 실패를 분리해 각각의 에러로 올려보내며, 토큰 발급 로직을 별도 함수로 추출해 핸들러는 흐름만 조율하도록 리팩터링하시길 권합니다."),
			},
			ExpectedModelContexts: []string{
				"materialID=mattermost:file-code-shot",
				"login_handler.png",
			},
			ExpectedReplyFragments: []string{"리팩터링", "비밀번호"},
			MinimumReplyLength:     80,
			ExpectedTaskStatus:     task.TaskStatusCompleted,
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
		Name:                   "schedule_create_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		Skills:                 []agent.SkillInstruction{scheduledTaskSkill()},
		AllowedTools:           append(agent.KernelToolNames(), "schedule.create", "schedule.cancel"),
		InitialToolNames:       []string{"schedule.create", "schedule.cancel"},
		RouterRequiredEvidence: []string{"schedule.create"},
		Turns: []VirtualTurn{{
			Prompt: "1분마다 \"1분 지났습니다\"라고 보내줘",
			ActionResponses: []string{
				actionInvokeCapabilityTool("schedule.create", `{"name":"1분 알림","taskInstruction":"현재 대화에 \"1분 지났습니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}`),
				actionFinishMessage("1분마다 알림을 보내도록 예약해둘게요.", "obs-001:schedule.create:0"),
			},
			ExpectedSelectedSkills: []string{"scheduled-task"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.schedule.create.requested", BodyFragment: "schedule.create", Count: 1},
				{Name: "tool.schedule.create.result", BodyFragment: "intervalSecond", Count: 1},
			},
			ExpectedModelContexts: []string{"schedule.create", "taskInstruction", "1분마다"},
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
		AllowedTools:          append(agent.KernelToolNames(), "schedule.create", "schedule.update", "schedule.cancel"),
		InitialToolNames:      []string{"schedule.create", "schedule.update", "schedule.cancel"},
		Turns: []VirtualTurn{
			{
				Prompt:                 "30분마다 상태 확인하라고 알려줘. 세 번만 해줘",
				RouterRequiredEvidence: []string{"schedule.create"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("schedule.create", `{"name":"상태 확인 알림","taskInstruction":"현재 대화에 \"상태를 확인하세요\"라고 보낸다.","kind":"interval","intervalSecond":1800,"maxRunCount":3,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}`),
					actionFinishMessage("30분마다 세 번 상태 확인 알림을 보내도록 예약해둘게요.", "obs-001:schedule.create:0"),
				},
				ExpectedSelectedSkills: []string{"scheduled-task"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.schedule.create.requested", BodyFragment: "schedule.create", Count: 1},
					{Name: "tool.schedule.create.result", BodyFragment: "intervalSecond", Count: 1},
				},
				ExpectedModelContexts: []string{"schedule.create", "taskInstruction", "30분마다"},
			},
			{
				Prompt:                 "그 예약을 1시간마다 다섯 번으로 바꿔줘",
				RouterRequiredEvidence: []string{"schedule.update"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("schedule.update", `{"scheduleID":"virtual-schedule-001","intervalSecond":3600,"maxRunCount":5,"repeatPolicy":"finite"}`),
					actionFinishMessage("예약을 1시간마다 다섯 번으로 수정했습니다.", "obs-001:schedule.update:0"),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.schedule.update.requested", BodyFragment: "schedule.update", Count: 1},
					{Name: "tool.schedule.update.result", BodyFragment: "intervalSecond", Count: 1},
				},
			},
			{
				Prompt:                 "그 예약 삭제해줘",
				RouterRequiredEvidence: []string{"schedule.cancel"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("schedule.cancel", `{"scope":"mine"}`),
					actionFinishMessage("예약을 삭제했습니다.", "obs-001:schedule.cancel:0"),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.schedule.cancel.requested", BodyFragment: "schedule.cancel", Count: 1},
				},
			},
		},
	}
}

func CalendarEventLifecycleAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "calendar_event_lifecycle_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{calendarSkill()},
		AllowedTools:          append(agent.KernelToolNames(), "calendar.add", "calendar.update", "calendar.delete"),
		CapabilityToolNames:   []string{"calendar.add", "calendar.update", "calendar.delete"},
		InitialToolNames:      []string{"calendar.add"},
		Turns: []VirtualTurn{
			{
				Prompt:                 "내일 오전 10시에 제품 회고 일정을 캘린더에 추가해줘",
				RouterRequiredEvidence: []string{"calendar.add"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("calendar.add", `{"title":"제품 회고","startISO":"2026-06-13T10:00:00+09:00","endISO":"2026-06-13T11:00:00+09:00","timeZone":"Asia/Seoul"}`),
					actionFinishMessage("내일 오전 10시에 제품 회고 일정을 추가했습니다.", "obs-001:calendar.add:0"),
				},
				ExpectedSelectedSkills: []string{"calendar"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.calendar.add.requested", BodyFragment: "calendar.add", Count: 1},
				},
			},
			{
				Prompt:                 "그 일정을 내일 오후 2시로 바꿔줘",
				RouterRequiredEvidence: []string{"calendar.update"},
				ActionResponses: []string{
					actionCallTool("calendar.update", `{"eventID":"calendar-event-001","title":"제품 회고","startISO":"2026-06-13T14:00:00+09:00","endISO":"2026-06-13T15:00:00+09:00","timeZone":"Asia/Seoul"}`),
					actionFinishMessage("제품 회고 일정을 내일 오후 2시로 변경했습니다.", "obs-001:calendar.update:0"),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.calendar.update.requested", BodyFragment: "calendar.update", Count: 1},
					{Name: "tool.calendar.update.requested", BodyFragment: "2026-06-13T14:00:00+09:00", Count: 1},
					{Name: "tool.calendar.update.result", BodyFragment: "updated virtual calendar event", Count: 1},
				},
			},
			{
				Prompt:                 "그 일정 삭제해줘",
				RouterRequiredEvidence: []string{"calendar.delete"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("calendar.delete", `{"eventID":"calendar-event-001"}`),
					actionFinishMessage("제품 회고 일정을 삭제했습니다.", "obs-001:calendar.delete:0"),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.calendar.delete.requested", BodyFragment: "calendar.delete", Count: 1},
				},
			},
		},
	}
}

func CalendarFalseFinishRecoveryAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "calendar_false_finish_recovery_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agent.SkillInstruction{calendarSkill()},
		AllowedTools:          []string{"conversation.history", "memory.search", "calendar.add"},
		CapabilityToolNames:   []string{"calendar.add"},
		InitialToolNames:      []string{"calendar.add"},
		Turns: []VirtualTurn{{
			Prompt:                 "7월 13일에 샨보장 미팅을 오전 10시부터 11시까지 등록해줘",
			RouterRequiredEvidence: []string{"calendar.add"},
			ActionResponses: []string{
				actionFinishMessage("7월 13일 미팅을 오전 10시~11시로 등록했습니다."),
				actionInvokeCapabilityTool("calendar.add", `{"title":"샨보장 미팅","startISO":"2026-07-13T10:00:00+09:00","endISO":"2026-07-13T11:00:00+09:00","timeZone":"Asia/Seoul"}`),
				actionFinishMessage("7월 13일 미팅을 오전 10시~11시로 등록했습니다.", "obs-002:calendar.add:0"),
			},
			ExpectedSelectedSkills: []string{"calendar"},
			ExpectedToolCalls:      []string{"calendar.add"},
			ExpectedToolCallCounts: map[string]int{
				"calendar.add": 1,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "agent.evidence_missing", BodyFragment: "calendar.add", Count: 1},
				{Name: "agent.completion_required", BodyFragment: "calendar.add", Count: 1},
				{Name: "tool.calendar.add.requested", BodyFragment: "2026-07-13T10:00:00+09:00", Count: 1},
			},
			ExpectedReplyFragments: []string{"등록했습니다"},
			ForbiddenEvents:        []string{"agent.no_progress_loop_stopped"},
		}},
	}
}

func AmbientDutyCalendarAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "ambient_duty_calendar_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"calendar.add"},
		AddressingResponse:     `{"target":"human","shouldRespond":false,"dutyMatch":true,"dutyName":"calendar_upkeep","dutyConfidence":0.93}`,
		Skills:                 []agent.SkillInstruction{calendarSkill()},
		AllowedTools:           []string{"conversation.history", "memory.search", "calendar.add"},
		CapabilityToolNames:    []string{"calendar.add"},
		InitialToolNames:       []string{"calendar.add"},
		Turns: []VirtualTurn{{
			Prompt:           "@박예시 님 오늘 오후 5시 정기회의에 최견본, 이샘플 님도 참석자로 추가해주세요",
			ExpectedResponse: VirtualResponseBackgroundAction,
			ConversationType: "channel",
			ChannelID:        "town-square",
			ChannelName:      "town-square",
			ReplyTargetID:    "virtual-message-001",
			Addressing:       connectors.AddressingMetadata{},
			ActionResponses: []string{
				actionInvokeCapabilityTool("calendar.add", `{"title":"정기회의","startISO":"2026-06-12T17:00:00+09:00","endISO":"2026-06-12T18:00:00+09:00","timeZone":"Asia/Seoul","attendees":["최견본","이샘플"]}`),
				actionFinishMessage("정기회의 일정을 추가했습니다.", "obs-001:calendar.add:0"),
			},
			ExpectedSelectedSkills: []string{"calendar"},
			ExpectedToolCalls:      []string{"calendar.add"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "agent.ambient_duty_launch", BodyFragment: `"dutyName":"calendar_upkeep"`, Count: 1},
				{Name: "tool.calendar.add.requested", BodyFragment: "2026-06-12T17:00:00+09:00", Count: 1},
				{Name: "tool.calendar.add.requested", BodyFragment: "최견본", Count: 1},
				{Name: "tool.calendar.add.requested", BodyFragment: "이샘플", Count: 1},
			},
			ExpectedModelContexts: []string{
				"Ambient duty context",
				"not addressed to you",
				"Never send a text reply",
			},
		}},
	}
}

func flowTaskSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:           "flow",
		Description:    "Add, update, and complete team work tasks, 업무, 할 일, status changes, and deadlines.",
		Prompt:         "Use task.add to add a task for a person, task.list to find an existing task, and task.update to change status, details, or mark it complete.",
		ToolReferences: []string{"task.add", "task.list", "task.update"},
		Source: agent.InstructionSource{
			Path:      "skills/flow/SKILL.md",
			SkillName: "flow",
		},
	}
}

func AmbientTaskCaptureAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "ambient_task_capture_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AddressingResponse:    `{"target":"human","shouldRespond":false,"dutyMatch":true,"dutyName":"team_flow_update","dutyConfidence":0.9}`,
		Skills:                []agent.SkillInstruction{flowTaskSkill()},
		AllowedTools:          []string{"conversation.history", "memory.search", "task.add", "task.list", "task.update"},
		CapabilityToolNames:   []string{"task.add", "task.list", "task.update"},
		InitialToolNames:      []string{"task.add"},
		Turns: []VirtualTurn{{
			Prompt:                 "@박예시 님 월요일까지 신규 가입 플로우 점검 작업 해주세요",
			ExpectedResponse:       VirtualResponseBackgroundAction,
			RouterRequiredEvidence: []string{"task.add"},
			ConversationType:       "channel",
			ChannelID:              "town-square",
			ChannelName:            "town-square",
			ReplyTargetID:          "virtual-message-010",
			Addressing:             connectors.AddressingMetadata{OtherPersonMentioned: true},
			ActionResponses: []string{
				actionInvokeCapabilityTool("task.add", `{"title":"신규 가입 플로우 점검","targetPersonHint":"예시"}`),
				actionFinishMessage("예시 님 업무로 추가했습니다.", "obs-001:task.add:0"),
			},
			ExpectedToolCalls: []string{"task.add"},
			ExpectedToolCallCounts: map[string]int{
				"task.add":     1,
				"terminal.run": 0,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "agent.ambient_duty_launch", BodyFragment: `"dutyName":"team_flow_update"`, Count: 1},
				{Name: "tool.task.add.requested", BodyFragment: "예시", Count: 1},
			},
			ExpectedModelContexts: []string{
				"Ambient duty context",
				"not addressed to you",
				"Never send a text reply",
			},
			ForbiddenEvents: []string{"tool.terminal.run.requested"},
		}, {
			Prompt:                 "@박예시 님 그 작업 마감은 수요일로 변경해주세요",
			ExpectedResponse:       VirtualResponseBackgroundAction,
			RouterRequiredEvidence: []string{"task.update"},
			ConversationType:       "channel",
			ChannelID:              "town-square",
			ChannelName:            "town-square",
			ReplyTargetID:          "virtual-message-011",
			Addressing:             connectors.AddressingMetadata{OtherPersonMentioned: true},
			ActionResponses: []string{
				actionInvokeCapabilityTool("task.list", `{"targetPersonHint":"예시"}`),
				actionInvokeCapabilityTool("task.update", `{"taskID":"task-1","endDate":"2026-06-24"}`),
				actionFinishMessage("예시 님 업무 마감을 수요일로 변경했습니다.", "obs-002:task.update:0"),
			},
			ExpectedToolCalls: []string{"task.list", "task.update"},
			ExpectedToolCallCounts: map[string]int{
				"task.add":    0,
				"task.update": 1,
			},
		}},
	}
}

func SkillLifecycleAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	skillName := "memo-helper"
	skillContent := userManagedSkillDocument(skillName)
	return VirtualSessionScenario{
		Name:                  "skill_lifecycle_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "skill.add", "skill.remove"},
		InitialToolNames:      []string{"skill.add", "skill.remove"},
		Turns: []VirtualTurn{
			{
				Prompt: "간단한 메모 정리 custom skill을 등록해줘",
				ActionResponses: []string{
					actionCallTool("skill.add", skillAddToolInput(skillName, skillContent)),
					actionFinishMessage("memo-helper skill을 등록했습니다.", "obs-001:skill.add:0"),
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
					ContainsFragments: []string{"name: memo-helper", "Organize short notes into concise memos"},
				}},
				ExpectedReplyFragments: []string{"memo-helper", "등록"},
			},
			{
				Prompt: "방금 등록한 memo-helper skill 삭제해줘",
				ActionResponses: []string{
					actionCallTool("skill.remove", `{"name":"memo-helper"}`),
					actionFinishMessage("memo-helper skill을 삭제했습니다.", "obs-001:skill.remove:0"),
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
		Skills:                []agent.SkillInstruction{presentationSkill(), scheduledTaskSkill(), sitePrototypeSkill()},
		AllowedTools:          []string{"conversation.history", "memory.search", "skill.search"},
		Turns: []VirtualTurn{{
			Prompt: "너는 무엇을 할 수 있어?",
			ActionResponses: []string{
				actionCallTool("skill.search", `{}`),
				actionFinishMessage("사용 가능한 skill에는 presentation, scheduled-task, site-prototype이 있습니다.", "obs-001:skill.search:0"),
			},
			ExpectedToolCalls: []string{"skill.search"},
			ExpectedToolCallCounts: map[string]int{
				"skill.search": 1,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.skill.search.result", BodyFragment: "presentation", Count: 1},
			},
			ExpectedReplyFragments: []string{"presentation"},
		}},
	}
}

func TaskHistoryQuestionAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "task_history_question_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "task.list"},
		CapabilityToolNames:   []string{"task.list"},
		InitialToolNames:      []string{"task.list"},
		InitialTaskRuns: []VirtualTaskRunFixture{{
			Prompt: "계약서 확인 요약 작업",
			Result: "계약서 확인 요약 작업을 완료했습니다.",
			Status: task.TaskStatusCompleted,
		}},
		Turns: []VirtualTurn{
			{
				Prompt: "계약서 확인 요약 작업을 완료했다고 답해줘",
				ActionResponses: []string{
					actionFinishMessage("계약서 확인 요약 작업을 완료했습니다."),
				},
				ExpectedToolCallCounts: map[string]int{
					"task.list": 0,
				},
				ExpectedReplyFragments: []string{"계약서 확인 요약", "완료"},
			},
			{
				Prompt: "최근에 어떤 작업을 했는지 알려줘",
				ActionResponses: []string{
					actionCallTool("task.list", `{}`),
					actionFinishMessage("최근에는 계약서 확인 요약 작업을 완료했습니다.", "obs-001:task.list:0"),
				},
				ExpectedToolCalls: []string{"task.list"},
				ExpectedToolCallCounts: map[string]int{
					"task.list": 1,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.task.list.result", BodyFragment: "계약서 확인 요약 작업", Count: 1},
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
		InitialToolNames:      []string{"memory.remember", "memory.search"},
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
					actionCallTool("memory.search", `{"query":"preferred language"}`),
					actionFinishMessage("Your preferred language is Korean.", "obs-001:memory.search:0"),
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

func DatabaseSQLAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "database_sql_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "db.sql"},
		InitialToolNames:      []string{"db.sql"},
		Turns: []VirtualTurn{
			{
				Prompt: "거래처 Acme 를 데이터베이스에 기록해줘.",
				ActionResponses: []string{
					actionCallTool("db.sql", `{"sql":"CREATE TABLE IF NOT EXISTS vendors(id INTEGER PRIMARY KEY, name TEXT)","scope":"me"}`),
					actionCallTool("db.sql", `{"sql":"INSERT INTO vendors(name) VALUES ('Acme')","scope":"me"}`),
					actionFinishMessage("거래처 Acme를 vendors 테이블에 기록했습니다."),
				},
				ExpectedToolCalls: []string{"db.sql"},
				ExpectedToolCallCounts: map[string]int{
					"db.sql": 2,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.db.sql.result", BodyFragment: "vendors", Count: 2},
				},
				ExpectedReplyFragments: []string{"vendors"},
			},
			{
				Prompt: "거래처 Beta 도 추가해줘. 이미 있는 테이블을 다시 써.",
				ActionResponses: []string{
					actionCallTool("db.sql", `{"sql":"SELECT name, sql FROM sqlite_master WHERE type='table'","scope":"me"}`),
					actionCallTool("db.sql", `{"sql":"INSERT INTO vendors(name) VALUES ('Beta')","scope":"me"}`),
					actionFinishMessage("기존 vendors 테이블에 거래처 Beta를 추가했습니다."),
				},
				ExpectedToolCalls: []string{"db.sql"},
				ExpectedToolCallCounts: map[string]int{
					"db.sql": 2,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.db.sql.result", BodyFragment: "vendors", Count: 2},
				},
				ExpectedReplyFragments: []string{"vendors"},
			},
		},
	}
}

func FailureExplanationAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "failure_explanation_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "terminal.run", "task.list"},
		CapabilityToolNames:   []string{"task.list"},
		InitialToolNames:      []string{"terminal.run", "task.list"},
		InitialTaskRuns: []VirtualTaskRunFixture{{
			Prompt:        "Run the analysis.",
			FailureReason: "terminal.run: permission denied",
			Status:        task.TaskStatusFailed,
		}},
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
					actionCallTool("task.list", `{"status":"failed"}`),
					actionFinishMessage("terminal.run 실행이 permission denied 때문에 실패했습니다.", "obs-001:task.list:0"),
				},
				ExpectedToolCalls: []string{"task.list"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.task.list.result", BodyFragment: "terminal.run: permission denied", Count: 1},
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
		InitialToolNames:      []string{"schedule.create", "schedule.cancel"},
		Turns: []VirtualTurn{{
			Prompt: "2027년 1월 15일 오전 9시에 계약서 확인 알림을 한 번만 예약해줘",
			ActionResponses: []string{
				actionInvokeCapabilityTool("schedule.create", `{"name":"계약서 확인 알림","taskInstruction":"현재 대화에 \"계약서를 확인하세요\"라고 보낸다.","kind":"once","runAt":"2027-01-15T00:00:00Z","timeZone":"Asia/Seoul"}`),
				actionFinishMessage("2027년 1월 15일 오전 9시에 한 번 알림을 보내도록 예약해둘게요.", "obs-001:schedule.create:0"),
			},
			ExpectedSelectedSkills: []string{"scheduled-task"},
			ExpectedToolCalls:      []string{"schedule.create"},
			ExpectedModelContexts:  []string{"schedule.create", "runAt", "once"},
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
description: Organize short notes into concise memos and extract action items when the user asks for memo help.
---
Organize notes into concise memos with action items and owners.`
}

func calendarSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:           "calendar",
		Description:    "Create, update, and delete calendar events, 일정, 달력, 캘린더, and meeting time changes.",
		Prompt:         "Use calendar.add to create calendar events, calendar.update to edit event time or details, and calendar.delete to delete events.",
		ToolReferences: []string{"calendar.add", "calendar.update", "calendar.delete"},
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
		Name:           "scheduled-task",
		Description:    "Create or cancel scheduled, recurring, and finite repeated reminders, messages, reports, and follow-up tasks such as 예약, 알림, 리마인드, 분마다, 매일, 매주, or 매월.",
		Prompt:         "Use schedule.create to create schedules and schedule.update to revise active schedules. Put only the run-time work in taskInstruction. Put cadence and stop conditions in structured fields such as runAt, intervalSecond, cronExpression, expiresAt, and maxRunCount. Set repeatPolicy finite with expiresAt or maxRunCount for finite repeats; set repeatPolicy unbounded only when the user explicitly asks for no end. Do not claim background loops are unsupported when schedule.create is available.",
		ToolReferences: []string{"schedule.create", "schedule.update", "schedule.cancel"},
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
		Name:                   "site_prototype_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"site.publish"},
		RouterSiteEvidence:     "Local Fleet Studio",
		Skills:                 []agent.SkillInstruction{sitePrototypeSkill()},
		AllowedTools:           append(agent.KernelToolNames(), sitePrototypeCapabilityToolNames()...),
		CapabilityToolNames:    sitePrototypeCapabilityToolNames(),
		InitialToolNames:       []string{"site.create", "site.publish"},
		Turns: []VirtualTurn{{
			Prompt: "테스트용 'Local Fleet Studio' 단일 페이지 소개 웹사이트를 만들어서 배포해줘. 첫 화면 제목은 'Local Fleet Studio', 보조 문구는 '로컬 플릿 웹사이트 생성 배포 테스트', 섹션은 서비스 소개, 장점 3개, 문의 CTA만 넣어줘. 추가 질문하지 말고 합리적인 기본값으로 진행해줘.",
			ActionResponses: []string{
				actionInvokeCapabilityTool("site.create", `{"slug":"demo","title":"Local Fleet Studio","content":{"siteName":"Local Fleet Studio","tagline":"로컬 플릿 웹사이트 생성 배포 테스트","sections":[{"title":"서비스 소개","body":"Local Fleet Studio는 로컬 플릿 환경에서 웹사이트 생성과 배포 과정을 검증하는 테스트 서비스입니다."},{"title":"장점","body":"빠른 프로토타입 생성, 안전한 배포 검증, 손쉬운 재배포까지 세 가지 장점을 제공합니다."},{"title":"문의","body":"자세한 내용이 궁금하시면 지금 바로 문의해 주세요."}]},"designBrief":"Clean black-on-white validation landing page","prototypeScope":"single static page"}`),
				actionCallTool("site.publish", `{"siteID":"site-1","message":"Initial Local Fleet Studio website"}`),
				actionFinishMessage("Local Fleet Studio 웹사이트 프로토타입을 배포했습니다: https://demo.device.example.test", "obs-002:site.publish:0"),
			},
			ExpectedSelectedSkills: []string{"site-prototype"},
			ExpectedToolCallCounts: map[string]int{"terminal.run": 0},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.site.create.requested", BodyFragment: "site.create", Count: 1},
				{Name: "tool.site.publish.requested", BodyFragment: "site.publish", Count: 1},
				{Name: "tool.site.publish.result", BodyFragment: "device.example.test", Count: 1},
			},
			ExpectedModelContexts:  []string{"site.create", "site.publish", "/workspace/circles/staff/sites/demo/draft", "Local Fleet Studio"},
			ForbiddenModelContexts: []string{"home/sites/site-1"},
			ExpectedReplyFragments: []string{"https://demo.device.example.test"},
			ForbiddenReplyFragments: []string{
				"죄송",
				"완료하지 못",
				"기능은 제공",
				"오류가 발생",
				"다시 한번",
				"어떤 웹사이트",
				"무슨 웹사이트",
			},
		}},
	}
}

func SiteEditRedeployAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "site_edit_redeploy_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"site.publish"},
		RouterSiteEvidence:     "Local Fleet Studio website",
		Skills:                 []agent.SkillInstruction{sitePrototypeSkill()},
		AllowedTools:           append(agent.KernelToolNames(), sitePrototypeCapabilityToolNames()...),
		CapabilityToolNames:    sitePrototypeCapabilityToolNames(),
		InitialToolNames:       []string{"site.create", "site.publish", "site.status", "file.write"},
		Turns: []VirtualTurn{
			{
				Prompt: "Build and deploy a single-page Local Fleet Studio website. Use the heading 'Local Fleet Studio' and subtitle 'Local fleet create deploy test'. Include a short service overview and three feature bullets. Do not ask follow-up questions.",
				ActionResponses: []string{
					actionInvokeCapabilityTool("site.create", `{"slug":"demo","title":"Local Fleet Studio","content":{"siteName":"Local Fleet Studio","tagline":"Local fleet create deploy test","sections":[{"title":"Overview","body":"Local Fleet Studio validates local fleet website creation and deployment."},{"title":"Features","body":"Fast prototyping, safe deploy verification, and easy redeploys."}]},"designBrief":"Clean validation landing page","prototypeScope":"single static page"}`),
					actionCallTool("site.publish", `{"siteID":"site-1","message":"Initial Local Fleet Studio site"}`),
					actionFinishMessage("Deployed the Local Fleet Studio site: https://demo.device.example.test", "obs-002:site.publish:0"),
				},
				ExpectedSelectedSkills: []string{"site-prototype"},
				ExpectedToolCallCounts: map[string]int{"terminal.run": 0},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.site.create.requested", BodyFragment: "site.create", Count: 1},
					{Name: "tool.site.publish.requested", BodyFragment: "site.publish", Count: 1},
					{Name: "tool.site.publish.result", BodyFragment: "device.example.test", Count: 1},
				},
				ExpectedModelContexts:  []string{"/workspace/circles/staff/sites/demo/draft"},
				ForbiddenModelContexts: []string{"home/sites/site-1"},
				ExpectedReplyFragments: []string{"https://demo.device.example.test"},
			},
			{
				Prompt: "Update the same Local Fleet Studio website heading to say 'Local Fleet Studio Updated' and add the subtitle 'Redeploy verification passed', then redeploy the same site. Do not create a new site.",
				ActionResponses: []string{
					actionCallTool("site.status", `{"siteID":"site-1"}`),
					actionCallTool("file.write", `{"path":"/workspace/circles/staff/sites/demo/draft/app/public/site-content.json","content":"{\"siteName\":\"Local Fleet Studio Updated\",\"tagline\":\"Redeploy verification passed\",\"blocks\":[{\"variant\":\"hero\",\"title\":\"Local Fleet Studio Updated\",\"body\":\"Redeploy verification passed\"}]}"}`),
					actionInvokeCapabilityTool("site.publish", `{"siteID":"site-1","message":"Update heading to Local Fleet Studio Updated"}`),
					actionFinishMessage("Updated and redeployed the site: https://demo.device.example.test", "obs-002:file.write:0", "obs-003:site.publish:0"),
				},
				ExpectedToolCallCounts: map[string]int{"terminal.run": 0},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.site.status.requested", BodyFragment: "site.status", Count: 1},
					{Name: "tool.file.write.requested", BodyFragment: "Local Fleet Studio Updated", Count: 1},
					{Name: "tool.file.write.requested", BodyFragment: "blocks", Count: 1},
					{Name: "tool.site.publish.requested", BodyFragment: "site.publish", Count: 1},
					{Name: "tool.site.publish.result", BodyFragment: "device.example.test", Count: 1},
				},
				ExpectedModelContexts:  []string{"/workspace/circles/staff/sites/demo/draft"},
				ForbiddenModelContexts: []string{"home/sites/site-1"},
				ExpectedReplyFragments: []string{"https://demo.device.example.test"},
			},
		},
	}
}

func SiteCustomStructureAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "site_custom_structure_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"site.publish"},
		RouterSiteEvidence:     "Local Fleet Studio",
		Skills:                 []agent.SkillInstruction{sitePrototypeSkill()},
		AllowedTools:           append(agent.KernelToolNames(), sitePrototypeCapabilityToolNames()...),
		CapabilityToolNames:    sitePrototypeCapabilityToolNames(),
		InitialToolNames:       []string{"site.publish", "file.write", "terminal.run"},
		Turns: []VirtualTurn{{
			Prompt: "Local Fleet Studio 웹사이트 레이아웃을 두 칼럼 커스텀 구조로 바꿔서 다시 배포해줘.",
			ActionResponses: []string{
				actionCallTool("file.write", `{"path":"/workspace/circles/staff/sites/demo/draft/app/src/App.tsx","content":"export default function App() {\n  return <main className=\"custom-layout\"><section className=\"column\">Local Fleet Studio</section><section className=\"column\">Two-column custom layout</section></main>;\n}\n"}`),
				actionCallTool("site.publish", `{"siteID":"site-1","message":"Publish custom two-column layout"}`),
				actionCallTool("terminal.run", `{"command":"mkdir -p dist && printf '<!doctype html><html><body><main class=\"custom-layout\"><section>Local Fleet Studio</section><section>Two-column custom layout</section></main></body></html>' > dist/index.html","workingDirectoryPath":"/workspace/circles/staff/sites/demo/draft/app","timeoutSecond":120}`),
				actionCallTool("site.publish", `{"siteID":"site-1","message":"Publish custom two-column layout"}`),
				actionFinishMessage("커스텀 레이아웃을 빌드하고 다시 배포했습니다: https://demo.device.example.test", "obs-004:site.publish:0"),
			},
			ExpectedToolCallCounts: map[string]int{"terminal.run": 1, "file.write": 1, "site.publish": 1},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "agent.site_publish_prerequisite_rejected", BodyFragment: "", Count: 1},
				{Name: "tool.file.write.requested", BodyFragment: "custom-layout", Count: 1},
				{Name: "tool.terminal.run.requested", BodyFragment: "dist/index.html", Count: 1},
				{Name: "tool.site.publish.result", BodyFragment: "device.example.test", Count: 1},
			},
			ExpectedEvents:         []string{"agent.site_publish_prerequisite_rejected"},
			ExpectedModelContexts:  []string{"site.publish requires a fresh build", "app/src", "app/public/site-content.json"},
			ForbiddenModelContexts: []string{"home/sites/site-1"},
			ExpectedReplyFragments: []string{"https://demo.device.example.test"},
			ForbiddenReplyFragments: []string{
				"죄송",
				"완료하지 못",
				"오류가 발생",
			},
		}},
	}
}

func SiteLifecycleAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                      "site_lifecycle_acceptance",
		ArtifactDirectoryPath:     artifactDirectoryPath,
		RouterSiteEvidence:        "Local Fleet Studio",
		Skills:                    []agent.SkillInstruction{sitePrototypeSkill()},
		AllowedTools:              append(agent.KernelToolNames(), sitePrototypeCapabilityToolNames()...),
		CapabilityToolNames:       sitePrototypeCapabilityToolNames(),
		CapabilityToolDescriptors: []agentruntime.CapabilityToolDescriptor{{Name: "site.delete", RequiresApproval: true}},
		InitialToolNames:          []string{"site.create", "site.publish", "site.status", "site.delete", "file.write", "terminal.run"},
		Turns: []VirtualTurn{
			{
				Prompt: "테스트용 'Local Fleet Studio' 단일 페이지 소개 웹사이트를 만들어서 배포해줘. 첫 화면 제목은 'Local Fleet Studio', 보조 문구는 '로컬 플릿 웹사이트 CRUD 테스트', 섹션은 서비스 소개, 장점 3개, 문의 CTA만 넣어줘. 추가 질문하지 말고 합리적인 기본값으로 진행해줘.",
				RouterRequiredEvidence: []string{
					"site.publish",
				},
				ActionResponses: []string{
					actionInvokeCapabilityTool("site.create", `{"slug":"demo","title":"Local Fleet Studio","prompt":"Single-page introduction website with hero title Local Fleet Studio, subtitle 로컬 플릿 웹사이트 CRUD 테스트, service overview, three advantages, and contact CTA.","designBrief":"Clean black-on-white validation landing page","prototypeScope":"single static page"}`),
					actionCallTool("terminal.run", `{"command":"mkdir -p dist && printf '<!doctype html><html><body><main><h1>Local Fleet Studio</h1><p>로컬 플릿 웹사이트 CRUD 테스트</p></main></body></html>' > dist/index.html","workingDirectoryPath":"/workspace/circles/staff/sites/demo/draft/app","timeoutSecond":120}`),
					actionCallTool("site.publish", `{"siteID":"site-1","message":"Initial Local Fleet Studio website"}`),
					actionFinishMessage("Local Fleet Studio 웹사이트를 배포했습니다: https://demo.device.example.test", "obs-003:site.publish:0"),
				},
				ExpectedSelectedSkills: []string{"site-prototype"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.site.create.requested", BodyFragment: "site.create", Count: 1},
					{Name: "tool.site.publish.requested", BodyFragment: "site.publish", Count: 1},
					{Name: "tool.site.publish.result", BodyFragment: "device.example.test", Count: 1},
				},
				ExpectedReplyFragments: []string{"https://demo.device.example.test"},
				ForbiddenReplyFragments: []string{
					"어떤 웹사이트",
					"무슨 웹사이트",
				},
			},
			{
				Prompt: "방금 만든 Local Fleet Studio 웹사이트의 첫 화면 제목을 'Local Fleet Studio Updated'로 바꾸고 보조 문구 '재배포 검증 완료'를 추가한 뒤 같은 사이트를 다시 배포해줘. 새 사이트는 만들지 마.",
				RouterRequiredEvidence: []string{
					"site.publish",
				},
				ActionResponses: []string{
					actionCallTool("site.status", `{"siteID":"site-1"}`),
					actionCallTool("file.write", `{"path":"/workspace/circles/staff/sites/demo/draft/app/src/App.tsx","content":"export default function App() {\n  return <main><h1>Local Fleet Studio Updated</h1><p>재배포 검증 완료</p></main>;\n}\n"}`),
					actionCallTool("terminal.run", `{"command":"mkdir -p dist && printf '<!doctype html><html><body><main><h1>Local Fleet Studio Updated</h1><p>재배포 검증 완료</p></main></body></html>' > dist/index.html","workingDirectoryPath":"/workspace/circles/staff/sites/demo/draft/app","timeoutSecond":120}`),
					actionInvokeCapabilityTool("site.publish", `{"siteID":"site-1","message":"Update Local Fleet Studio heading"}`),
					actionFinishMessage("Local Fleet Studio 웹사이트를 수정하고 다시 배포했습니다: https://demo.device.example.test", "obs-002:file.write:0", "obs-004:site.publish:0"),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.site.status.requested", BodyFragment: "site.status", Count: 1},
					{Name: "tool.file.write.requested", BodyFragment: "Local Fleet Studio Updated", Count: 1},
					{Name: "tool.terminal.run.requested", BodyFragment: "dist/index.html", Count: 1},
					{Name: "tool.site.publish.requested", BodyFragment: "site.publish", Count: 1},
					{Name: "tool.site.create.requested", BodyFragment: "site.create", Count: 0},
				},
				ExpectedReplyFragments: []string{"https://demo.device.example.test"},
			},
			{
				Prompt: "방금 배포한 Local Fleet Studio 테스트 웹사이트를 삭제해줘.",
				RouterRequiredEvidence: []string{
					"site.delete",
				},
				ActionResponses: []string{
					actionCallTool("site.status", `{"siteID":"site-1"}`),
					actionCallToolWithMessage("site.delete", "Local Fleet Studio 테스트 웹사이트를 삭제합니다.", `{"siteID":"site-1"}`),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.site.status.requested", BodyFragment: "site.status", Count: 1},
					{Name: "tool.site.delete.requested", BodyFragment: "site.delete", Count: 0},
					{Name: "tool.site.delete.result", BodyFragment: "approval_required", Count: 0},
					{Name: "approval.pending_call", BodyFragment: `"site.delete"`, Count: 1},
					{Name: "agent.failure_debt_created", BodyFragment: "", Count: 0},
				},
				ExpectedEvents:         []string{"confirmation.requested"},
				ExpectedReplyFragments: []string{"삭제"},
				ExpectedTaskStatus:     task.TaskStatusWaitingApproval,
			},
			{
				Prompt:         "확인",
				RouterApproval: "approve",
				ActionResponses: []string{
					actionFinishMessage("Local Fleet Studio 테스트 웹사이트를 삭제했습니다.", "obs-004:site.delete:0"),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.site.delete.requested", BodyFragment: "site.delete", Count: 1},
					{Name: "tool.site.delete.result", BodyFragment: "deleted", Count: 1},
					{Name: "approval.executed", BodyFragment: `"site.delete"`, Count: 1},
				},
				ExpectedEvents:         []string{"confirmation.reply_classified"},
				ExpectedReplyFragments: []string{"삭제했습니다"},
			},
		},
	}
}

func SiteSuggestedRepairRecoveryScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "site_suggested_repair_recovery",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"site.publish"},
		RouterSiteEvidence:     "웹사이트 퀄리티가 너무 낮잖아",
		Skills:                 []agent.SkillInstruction{sitePrototypeSkill()},
		AllowedTools:           append([]string{"conversation.history", "memory.search", "file.write", "terminal.run"}, sitePrototypeCapabilityToolNames()...),
		CapabilityToolNames:    sitePrototypeCapabilityToolNames(),
		InitialToolNames:       []string{"site.status", "site.repair", "site.publish", "file.write", "terminal.run"},
		Turns: []VirtualTurn{{
			Prompt: "더 예쁘게 해달라구. 웹사이트 퀄리티가 너무 낮잖아.",
			ActionResponses: []string{
				actionCallTool("site.status", `{"siteID":"site-1"}`),
				actionFinishMessage("사이트 수정이 어렵습니다."),
				actionInvokeCapabilityTool("site.status", `{"siteID":"site-1"}`),
				actionInvokeCapabilityTool("site.repair", `{"siteID":"site-1"}`),
				actionCallTool("file.write", `{"path":"/workspace/circles/staff/sites/demo/draft/app/src/App.tsx","content":"export default function App() {\n  return <main><h1>Polished Citrus Studio</h1><p>Fresh, warm, and carefully crafted.</p></main>;\n}\n"}`),
				actionCallTool("terminal.run", `{"command":"mkdir -p dist && printf '<!doctype html><html><body><main><h1>Polished Citrus Studio</h1><p>Fresh, warm, and carefully crafted.</p></main></body></html>' > dist/index.html","workingDirectoryPath":"/workspace/circles/staff/sites/demo/draft/app","timeoutSecond":120}`),
				actionInvokeCapabilityTool("site.publish", `{"siteID":"site-1","message":"Improve visual quality"}`),
				actionFinishMessage("사이트를 더 예쁘게 다듬고 다시 배포했습니다: https://demo.device.example.test", "obs-007:site.publish:0"),
			},
			ExpectedSelectedSkills: []string{"site-prototype"},
			ExpectedToolCalls:      []string{"site.status", "site.repair", "file.write", "terminal.run", "site.publish"},
			ExpectedToolCallCounts: map[string]int{
				"site.status":  2,
				"site.repair":  1,
				"file.write":   1,
				"terminal.run": 1,
				"site.publish": 1,
			},
			ExpectedEvents:         []string{"agent.completion_required"},
			ExpectedModelContexts:  []string{"site.repair", "/workspace/circles/staff/sites/demo/draft"},
			ForbiddenModelContexts: []string{"home/sites/site-1"},
			ExpectedReplyFragments: []string{"https://demo.device.example.test"},
			ForbiddenReplyFragments: []string{
				"권한",
				"제약",
				"어렵",
				"완료하지 못",
			},
		}},
	}
}

func AskChoiceReplyAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "ask_choice_reply_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation.history", "memory.search", "ask.input"},
		InitialToolNames:      []string{agent.AskInputToolName},
		Turns: []VirtualTurn{{
			Prompt: "둘 중 하나 고르게 해줘",
			ActionResponses: []string{
				actionCallToolWithMessage("ask.input", "어느 쪽으로 진행할까요?", `{"question":"어느 쪽으로 진행할까요?","choices":["첫 번째","두 번째"]}`),
			},
			ExpectedToolCalls:      []string{"ask.input"},
			ExpectedEvents:         []string{"ask.requested"},
			ExpectedReplyFragments: []string{"어느 쪽으로 진행할까요?"},
			ExpectedModelContexts:  []string{"choices"},
		}, {
			Prompt: "두 번째",
			ActionResponses: []string{
				actionFinishMessage("두 번째로 진행하겠습니다."),
			},
			ExpectedEvents:         []string{"ask.resolved"},
			ExpectedReplyFragments: []string{"두 번째"},
		}},
	}
}

func DirectMessageSendConfirmAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "dm_send_confirm_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"message.send"},
		ScriptedExecutionPlan: &agent.ExecutionPlan{
			OriginalInstruction:     "테스트이한테 DM으로 오늘 오후 3시에 확인하자고 보내줘",
			Summary:                 "테스트이에게 오늘 오후 3시 확인 요청을 DM으로 보낸다",
			Targets:                 []string{"테스트"},
			ExternalSend:            true,
			ThirdPartyExternalSend:  true,
			MissingInformation:      []string{},
			ContinuationInstruction: "테스트이에게 오늘 오후 3시에 확인하자는 DM을 보낸다",
		},
		ScriptedConfirmationReply: "테스트이에게 ‘오늘 오후 3시에 확인하자’고 DM을 보낼까요?",
		AllowedTools:              append(agent.KernelToolNames(), "message.send"),
		InitialToolNames:          []string{"message.send"},
		CapabilityToolDescriptors: []agentruntime.CapabilityToolDescriptor{{
			Name:             "message.send",
			RequiresApproval: true,
		}},
		Turns: []VirtualTurn{{
			Prompt: "테스트이한테 DM으로 오늘 오후 3시에 확인하자고 보내줘",
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.message.send.requested", BodyFragment: `"targetType":"directMessage"`, Count: 0},
				{Name: "agent.failure_debt_created", BodyFragment: "", Count: 0},
			},
			ExpectedEvents:         []string{"confirmation.requested"},
			ExpectedReplyFragments: []string{"테스트", "오늘 오후 3시에 확인하자"},
			ExpectedTaskStatus:     task.TaskStatusWaitingApproval,
		}, {
			Prompt:         "확인",
			RouterApproval: "approve",
			ActionResponses: []string{
				actionCallTool("message.send", `{"targetType":"directMessage","personHint":"테스트","message":"오늘 오후 3시에 확인하자"}`),
				actionFinishMessage("테스트이에게 DM을 보냈습니다.", "obs-001:message.send:0"),
			},
			ExpectedToolCalls: []string{"message.send"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.message.send.requested", BodyFragment: `"targetType":"directMessage"`, Count: 1},
				{Name: "tool.message.send.result", BodyFragment: "virtual-platform-message-001", Count: 1},
			},
			ExpectedEvents:         []string{"confirmation.reply_classified"},
			ExpectedModelContexts:  []string{"virtual-platform-message-001"},
			ExpectedReplyFragments: []string{"DM", "보냈습니다"},
		}},
	}
}

func ChannelPostAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "channel_post_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"message.send"},
		ScriptedExecutionPlan: &agent.ExecutionPlan{
			OriginalInstruction:     "announcements 채널에 오늘 5시에 전체 공지 회의 있다고 올려줘",
			Summary:                 "announcements 채널에 오늘 5시 전체 공지 회의를 게시한다",
			Targets:                 []string{"announcements"},
			ExternalSend:            true,
			ThirdPartyExternalSend:  true,
			MissingInformation:      []string{},
			ContinuationInstruction: "announcements 채널에 오늘 5시 전체 공지 회의를 게시한다",
		},
		ScriptedConfirmationReply: "announcements 채널에 오늘 5시 전체 공지 회의를 게시할까요?",
		AllowedTools:              append(agent.KernelToolNames(), "message.send"),
		CapabilityToolNames:       []string{"message.send"},
		InitialToolNames:          []string{"message.send"},
		CapabilityToolDescriptors: []agentruntime.CapabilityToolDescriptor{{Name: "message.send", RequiresApproval: true}},
		Turns: []VirtualTurn{{
			Prompt:                 "announcements 채널에 오늘 5시에 전체 공지 회의 있다고 올려줘",
			ExpectedEvents:         []string{"confirmation.requested"},
			ExpectedReplyFragments: []string{"announcements", "게시"},
			ExpectedTaskStatus:     task.TaskStatusWaitingApproval,
		}, {
			Prompt:         "확인",
			RouterApproval: "approve",
			ActionResponses: []string{
				actionCallTool("message.send", `{"targetType":"channel","channelName":"announcements","message":"오늘 5시에 전체 공지 회의가 있습니다."}`),
				actionFinishMessage("announcements 채널에 공지를 올렸습니다.", "obs-001:message.send:0"),
			},
			ExpectedToolCalls: []string{"message.send"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.message.send.requested", BodyFragment: `"targetType":"channel"`, Count: 1},
				{Name: "tool.message.send.requested", BodyFragment: `"channelName":"announcements"`, Count: 1},
				{Name: "tool.message.send.requested", BodyFragment: `"targetType":"directMessage"`, Count: 0},
			},
			ExpectedReplyFragments: []string{"채널", "올렸습니다"},
		}},
	}
}

func PlatformMessageEditAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "platform_message_edit_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"message.update"},
		AllowedTools:           append(agent.KernelToolNames(), "message.update"),
		CapabilityToolNames:    []string{"message.update"},
		InitialToolNames:       []string{"message.update"},
		Turns: []VirtualTurn{{
			Prompt: "방금 올린 공지 message virtual-platform-message-001 문구를 '오늘 오후 6시에 전체 공지 회의가 있습니다.'로 바꿔줘",
			ActionResponses: []string{
				actionCallTool("message.update", `{"messageID":"virtual-platform-message-001","text":"오늘 오후 6시에 전체 공지 회의가 있습니다."}`),
				actionFinishMessage("공지 메시지 문구를 수정했습니다.", "obs-001:message.update:0"),
			},
			ExpectedToolCalls: []string{"message.update"},
			ExpectedToolCallCounts: map[string]int{
				"message.update": 1,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.message.update.requested", BodyFragment: `"messageID":"virtual-platform-message-001"`, Count: 1},
				{Name: "tool.message.update.requested", BodyFragment: `"text":"오늘 오후 6시에 전체 공지 회의가 있습니다."`, Count: 1},
			},
		}},
	}
}

func presentationSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:           "presentation",
		Description:    "Create local presentation decks, 피피티, 파워포인트, 발표자료, PPTX, PDF, HTML, and notes attachments.",
		Prompt:         "Write Stitch-compatible DESIGN.md and Marp presentation.md directly under tmp/<deck-slug> from the user request. Treat presentation.md as the deck source of truth and iterate on it when needed. Use Paperlogy/Freesentation/Pretendard/Noto Sans KR font guidance, choose layouts from the content intent, include design-source: DESIGN.md, run NAME=<deck-slug> /workspace/skills/presentation/scripts/build.sh with workingDirectoryPath tmp/<deck-slug> for a full deck or FORMATS=html NAME=<deck-slug> /workspace/skills/presentation/scripts/build.sh for html-only requests, promote build outputs with file.promote, then file.attach only promoted generated files. Do not use Google Workspace unless a google tool is explicitly available.",
		ToolReferences: []string{"file.write", "terminal.run", "file.promote", "file.attach"},
		Source: agent.InstructionSource{
			Path:      "skills/presentation/SKILL.md",
			SkillName: "presentation",
			ByteSize:  512,
			SHA256:    "virtual-presentation",
		},
	}
}

func sitePrototypeSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:           "site-prototype",
		Description:    "Create, publish, update, take down, restore, or delete React and PocketBase 웹사이트, 사이트, web app, and website prototypes.",
		Prompt:         "Create and publish website prototypes. For a new prototype, call site.create with a DNS-safe slug and title, write or edit source inside the returned site workspace, call terminal.run to build the app with bun, then call site.publish with the siteID and a concise message. Never claim deployment succeeded until site.publish succeeds.",
		ToolReferences: sitePrototypeToolNames(),
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
		"site.create",
		"site.repair",
		"site.publish",
		"site.status",
		"site.logs",
		"site.rollback",
		"site.unpublish",
		"site.restore",
		"site.delete",
		"user.confirm",
	}
}

func sitePrototypeCapabilityToolNames() []string {
	return []string{
		"site.create",
		"site.publish",
		"site.status",
		"site.logs",
		"site.repair",
		"site.rollback",
		"site.unpublish",
		"site.restore",
		"site.delete",
		"user.confirm",
	}
}
