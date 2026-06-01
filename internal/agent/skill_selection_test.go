package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
)

func TestSelectInstructionBundleIncludesSimpleSlidesForKoreanPPTRequest(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{
			{
				Name:         "simple-slides",
				Description:  "Create presentation decks, 피피티, 파워포인트, 발표자료, and PPTX files.",
				WhenToUse:    "Use for 피피티, 파워포인트, 발표자료, and PPTX requests.",
				Category:     "document-generation",
				Tags:         []string{"slides", "pptx"},
				Prompt:       "Generate PPTX with Marp.",
				TriggerHints: []string{"피피티", "파워포인트", "발표자료", "pptx"},
				AllowedTools: []string{"terminal.run", "file.write", "file.attach"},
				Source:       InstructionSource{Path: "skills/simple-slides/SKILL.md", SkillName: "simple-slides"},
			},
		},
	}

	selectedBundle := selectInstructionBundleForRequest(instructionBundle, AgentRequest{
		Prompt:  "너 뭐 할 수 있는지 피피티 만들어서 보내줘봐",
		ToolSet: testToolSet([]string{"terminal.run", "file.write", "file.attach"}),
	})

	if !strings.Contains(selectedBundle.Prompt, "Generate PPTX with Marp.") {
		t.Fatalf("expected simple-slides skill prompt for Korean PPT request, got %q", selectedBundle.Prompt)
	}
	if !strings.Contains(selectedBundle.Prompt, "Available skill index") || !strings.Contains(selectedBundle.Prompt, "simple-slides: Create presentation decks, 피피티") {
		t.Fatalf("expected compact skill index, got %q", selectedBundle.Prompt)
	}
	if !strings.Contains(selectedBundle.Prompt, "Available skill references") || !strings.Contains(selectedBundle.Prompt, "They are not mandatory") {
		t.Fatalf("expected selected skill prompt to be framed as references, got %q", selectedBundle.Prompt)
	}
	if strings.Contains(selectedBundle.Prompt, "Selected skill instructions") {
		t.Fatalf("expected no mandatory selected skill framing, got %q", selectedBundle.Prompt)
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
				Name:         "simple-slides",
				Description:  "Create presentation decks, 피피티, 파워포인트, 발표자료, and PPTX files.",
				WhenToUse:    "Use for 피피티 and PPTX requests.",
				Prompt:       "Generate PPTX with Marp.",
				TriggerHints: []string{"피피티", "pptx"},
				AllowedTools: []string{"terminal.run", "file.write", "file.attach"},
				Source:       InstructionSource{Path: "skills/simple-slides/SKILL.md", SkillName: "simple-slides"},
			},
		},
	}

	selectedBundle := selectInstructionBundleForRequest(instructionBundle, AgentRequest{
		Prompt: "별로야. 폐기하고 새로 다시 해줘.",
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "user", Text: "너 뭐 할 수 있는지 8장 피피티 만들어서 보내줘봐"},
		}},
		ToolSet: testToolSet([]string{"terminal.run", "file.write", "file.attach"}),
	})

	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected follow-up context to select simple-slides, got %+v", selectedBundle.SkillDecisions)
	}
	if !strings.Contains(selectedBundle.Prompt, "Generate PPTX with Marp.") {
		t.Fatalf("expected selected skill body, got %q", selectedBundle.Prompt)
	}
}

func TestSelectInstructionBundleDoesNotUseTriggerHintOutsideRetrievalCandidates(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{
			{
				Name:         "site-prototype",
				Description:  "Create and publish web prototypes.",
				WhenToUse:    "Use for website prototype requests.",
				Prompt:       "Use site.app.create, terminal.run, and site.app.publish.",
				TriggerHints: []string{"웹사이트", "배포"},
				AllowedTools: []string{"terminal.run", "site.app.create", "site.app.publish"},
				Source:       InstructionSource{Path: "skills/site-prototype/SKILL.md", SkillName: "site-prototype"},
			},
		},
	}

	selectedBundle := selectInstructionBundleForRequest(instructionBundle, AgentRequest{
		Prompt:  "웹사이트 하나 만들어서 배포해봐",
		ToolSet: testToolSet([]string{"terminal.run", "site.app.create", "site.app.publish"}),
	})

	if strings.Contains(selectedBundle.Prompt, "Use site.app.create") {
		t.Fatalf("expected trigger hint not to load full skill body, got %q", selectedBundle.Prompt)
	}
	for _, skillDecision := range selectedBundle.SkillDecisions {
		if skillDecision.Name == "site-prototype" && skillDecision.Status == "selected" {
			t.Fatalf("expected trigger hint not to select site-prototype, got %+v", selectedBundle.SkillDecisions)
		}
	}
}

func TestToolSetForSelectedSkillsKeepsCoreAndSelectedSkillTools(t *testing.T) {
	fullToolSet := testToolSet([]string{
		"conversation.history",
		"memory.search",
		"ask.confirm",
		"terminal.run",
		"site.app.create",
		"site.app.publish",
		"schedule.create",
	})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:         "site-prototype",
				AllowedTools: []string{"terminal.run", "site.app.create", "site.app.publish"},
			},
			{
				Name:         "scheduled-task",
				AllowedTools: []string{"schedule.create"},
			},
		},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}

	filteredToolSet := toolSetForSelectedSkills(fullToolSet, instructionBundle)

	for _, toolName := range []string{"conversation.history", "memory.search", "ask.confirm", "terminal.run", "site.app.create", "site.app.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	if !filteredToolSet.IsAllowed("schedule.create") {
		t.Fatalf("expected default schedule.create to remain available, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestToolSetForAgentTurnUsesSelectedSkillAllowedTools(t *testing.T) {
	fullToolSet := testToolSet([]string{
		"conversation.history",
		"memory.search",
		"ask.confirm",
		"math.calculate",
		"terminal.run",
		"file.write",
		"schedule.create",
		"mail.message.search",
	})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "scheduled-task", AllowedTools: []string{"schedule.create"}},
			{Name: "mail", AllowedTools: []string{"mail.message.search"}},
		},
		SkillDecisions: []SkillSelectionDecision{{Name: "scheduled-task", Status: "selected"}},
	}

	filteredToolSet := toolSetForAgentTurn(fullToolSet, instructionBundle, AgentRequest{Prompt: "내일 알려줘"}, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"conversation.history", "memory.search", "ask.confirm", "math.calculate", "terminal.run", "file.write", "schedule.create"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	if filteredToolSet.IsAllowed("mail.message.search") {
		t.Fatalf("expected unselected skill tool to be hidden, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestToolSetForAgentTurnHidesSelectedSendToolForNonSendOutcome(t *testing.T) {
	fullToolSet := testToolSet([]string{"ask.confirm", "platform.dm.send", "file.write"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "direct-message",
			AllowedTools: []string{"ask.confirm", "platform.dm.send"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	contract := OutcomeContract{SelectedEvidenceHints: []string{"platform.dm.send"}}

	filteredToolSet := toolSetForAgentTurn(fullToolSet, instructionBundle, AgentRequest{Prompt: "사업계획서 작성해줘"}, ExecutionPlan{}, false, contract)

	if !filteredToolSet.IsAllowed("ask.confirm") || !filteredToolSet.IsAllowed("file.write") {
		t.Fatalf("expected universal tools to remain available, got %+v", filteredToolSet.ListToolNames())
	}
	if filteredToolSet.IsAllowed("platform.dm.send") {
		t.Fatalf("expected send tool to be hidden for non-send outcome, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestAgentKernelActionSchemaUsesSelectedSkillAllowedTools(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"reason":"schedule request","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("done"),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, ""))
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{
			{
				Name:         "scheduled-task",
				Description:  "Create schedule, scheduled, reminder, repeat, and repeated tasks.",
				WhenToUse:    "Use for reminders, scheduled tasks, repeat requests, 1분에 한 번씩, and 10번 repeated work.",
				Prompt:       "Use schedule.create for reminders.",
				TriggerHints: []string{"schedule", "reminder", "repeat", "10번"},
				AllowedTools: []string{"schedule.create"},
				Source:       InstructionSource{Path: "skills/scheduled-task/SKILL.md", SkillName: "scheduled-task"},
			},
			{
				Name:         "mail",
				Description:  "Search mail.",
				Prompt:       "Use mail.message.search.",
				AllowedTools: []string{"mail.message.search"},
				Source:       InstructionSource{Path: "skills/mail/SKILL.md", SkillName: "mail"},
			},
		}}
	})
	toolRegistry := testToolSet([]string{"schedule.create", "mail.message.search", "math.calculate", "ask.input"})

	_, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "repeat this 10번",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to complete: %v", errorValue)
	}
	if len(replyLanguageModel.requests) == 0 {
		t.Fatal("expected action request")
	}
	actionSchema := replyLanguageModel.requests[0].StructuredOutputSchema.Document
	if !strings.Contains(actionSchema, `"toolName":{"enum":["schedule.create"]`) {
		t.Fatalf("expected selected skill allowed tool in action schema, got %s", actionSchema)
	}
	if strings.Contains(actionSchema, "mail.message.search") {
		t.Fatalf("expected unselected skill tool to be hidden from action schema, got %s", actionSchema)
	}
	if !strings.Contains(actionSchema, "math.calculate") || !strings.Contains(actionSchema, "ask.input") {
		t.Fatalf("expected universal tools in action schema, got %s", actionSchema)
	}
}

func TestSkillSelectorOnlyChecksSkillAvailability(t *testing.T) {
	skillSelector := SkillSelector{}
	skillInstruction := SkillInstruction{
		Name:         "simple-slides",
		TriggerHints: []string{"피피티", "파워포인트", "발표자료", "pptx"},
		AllowedTools: []string{"terminal.run", "file.write", "file.attach"},
	}
	request := AgentRequest{Prompt: "피피티 만들어줘", ToolSet: testToolSet([]string{"terminal.run", "file.write", "file.attach"})}

	if skillSelector.ShouldInclude(skillInstruction, request) {
		t.Fatal("expected prompt hints not to select skills outside retrieval")
	}
}

func TestSkillSelectorSkipsSkillWhenAllowedToolIsMissing(t *testing.T) {
	skillSelector := SkillSelector{}
	skillInstruction := SkillInstruction{
		Name:         "simple-slides",
		TriggerHints: []string{"피피티"},
		AllowedTools: []string{"terminal.run", "file.write", "file.attach"},
	}
	request := AgentRequest{
		Prompt:  "피피티 만들어줘",
		ToolSet: testToolSet([]string{"terminal.run", "file.write"}),
	}

	decision := skillSelector.Evaluate(skillInstruction, request, "default")
	if decision.Status == "selected" {
		t.Fatal("expected simple-slides to be skipped without file.attach")
	}
	if decision.Reason != "missing_allowed_tools" || len(decision.MissingTools) != 1 || decision.MissingTools[0] != "file.attach" {
		t.Fatalf("expected missing tool reason, got %+v", decision)
	}
}

func TestSelectInstructionBundleKeepsSkillWhenAllowedToolIsRegisteredButHidden(t *testing.T) {
	toolSet := NewToolSet([]string{"terminal.run"})
	for _, toolName := range []string{"terminal.run", "site.app.create", "site.app.publish"} {
		currentToolName := toolName
		toolSet.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}
	instructionBundle := InstructionBundle{Skills: []SkillInstruction{{
		Name:         "site-prototype",
		Description:  "Create and publish website prototypes.",
		Prompt:       "SITE BODY",
		AllowedTools: []string{"terminal.run", "site.app.create", "site.app.publish"},
	}}}
	retriever := staticSkillRetriever{result: SkillRetrievalResult{
		RetrievalMode: "test",
		SelectedCandidates: []SkillCandidate{{
			Name:   "site-prototype",
			Score:  1,
			Reason: "test",
		}},
	}}

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt:  "김인턴 소개 웹사이트 만들어줘",
		ToolSet: toolSet,
	}, retriever)
	filteredToolSet := toolSetForSelectedSkills(toolSet, selectedBundle)

	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected hidden registered site skill to be selected, got %+v", selectedBundle.SkillDecisions)
	}
	for _, toolName := range []string{"site.app.create", "site.app.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill to expose %s, got %+v", toolName, filteredToolSet.ListToolNames())
		}
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
				Name:         "simple-slides",
				Description:  "Create decks.",
				Prompt:       "Generate PPTX with Marp.",
				TriggerHints: []string{"피피티"},
				AllowedTools: []string{"terminal.run"},
			},
			{
				Name:         "create-gws-file",
				Description:  "Create spreadsheets.",
				Prompt:       "SECRET FULL BODY",
				TriggerHints: []string{"spreadsheet"},
				AllowedTools: []string{"terminal.run"},
			},
		},
	}

	selectedBundle := selectInstructionBundleForRequest(instructionBundle, AgentRequest{
		Prompt:  "피피티 만들어줘",
		ToolSet: testToolSet([]string{"terminal.run"}),
	})

	if strings.Contains(selectedBundle.Prompt, "SECRET FULL BODY") {
		t.Fatalf("expected unselected full body to be omitted, got %q", selectedBundle.Prompt)
	}
}

func TestEmbeddingRetrieverSelectsStandardSkill(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:        "simple-slides",
				Description: "Create presentation slides, 피피티, and PPTX decks.",
				WhenToUse:   "Use for pitch decks, 발표자료, 피피티, and PowerPoint requests.",
				Prompt:      "Generate slides.",
				Source:      InstructionSource{Path: "skills/simple-slides/SKILL.md", SHA256: "one", SkillName: "simple-slides"},
			},
			{
				Name:        "calendar",
				Description: "Create or list calendar events.",
				Prompt:      "Use calendar tools.",
				Source:      InstructionSource{Path: "skills/calendar/SKILL.md", SHA256: "two", SkillName: "calendar"},
			},
		},
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "피피티 만들어줘",
	}, retriever)

	if selectedBundle.RetrievalMode != "embedding" || selectedBundle.IndexStatus != "ready" {
		t.Fatalf("expected embedding retrieval, got mode=%q status=%q", selectedBundle.RetrievalMode, selectedBundle.IndexStatus)
	}
	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Name != "simple-slides" || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected simple-slides selected, got %+v", selectedBundle.SkillDecisions)
	}
	if !strings.Contains(selectedBundle.Prompt, "Generate slides.") {
		t.Fatalf("expected selected skill body, got %q", selectedBundle.Prompt)
	}
	if strings.Contains(selectedBundle.Prompt, "Use calendar tools.") {
		t.Fatalf("expected unselected skill body to stay out of prompt, got %q", selectedBundle.Prompt)
	}
}

func TestSiteArtifactRequestDoesNotSelectContentDomainSkills(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:        "site-prototype",
				Description: "Create, publish, and update website prototypes, homepages, web apps, landing pages, and deployed sites.",
				WhenToUse:   "Use for website, homepage, web app, site, publish, deploy, 홈페이지, 웹사이트, 사이트, and 배포 requests.",
				Prompt:      "Use site tools.",
				Source:      InstructionSource{Path: "skills/site-prototype/SKILL.md", SkillName: "site-prototype"},
			},
			{
				Name:        "mail",
				Description: "Search, read, and send mail messages.",
				WhenToUse:   "Use when the user wants to operate on real email.",
				Prompt:      "Use mail tools.",
				Source:      InstructionSource{Path: "skills/mail/SKILL.md", SkillName: "mail"},
			},
			{
				Name:        "calendar",
				Description: "Create, list, and update calendar events and schedules.",
				WhenToUse:   "Use when the user wants to operate on real calendar data.",
				Prompt:      "Use calendar tools.",
				Source:      InstructionSource{Path: "skills/calendar/SKILL.md", SkillName: "calendar"},
			},
			{
				Name:        "browser",
				Description: "Control the browser and inspect web pages.",
				WhenToUse:   "Use when the user wants interactive browser control.",
				Prompt:      "Use browser tools.",
				Source:      InstructionSource{Path: "skills/browser/SKILL.md", SkillName: "browser"},
			},
		},
	}

	retriever := staticSkillRetriever{result: SkillRetrievalResult{
		SelectedCandidates: []SkillCandidate{
			{Name: "mail", Score: 30, Reason: "bm25_fallback"},
			{Name: "calendar", Score: 29, Reason: "bm25_fallback"},
			{Name: "browser", Score: 28, Reason: "bm25_fallback"},
			{Name: "site-prototype", Score: 8, Reason: "bm25_fallback"},
		},
		RetrievalMode: "bm25_fallback",
		IndexStatus:   "ready",
	}}
	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "메일, 일정, 브라우저 제어 능력을 소개하는 세련된 개인 홈페이지 하나 만들어서 배포해줘",
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{ExpectedResults: []ExpectedResult{
			{ID: "site-public-link", Type: "link", Description: "public website URL", Required: true},
		}}},
	}, retriever)

	if !skillDecisionHasStatus(selectedBundle.SkillDecisions, "site-prototype", "selected") {
		t.Fatalf("expected site-prototype selected, got %+v", selectedBundle.SkillDecisions)
	}
	for _, skillName := range []string{"mail", "calendar", "browser"} {
		if skillDecisionHasStatus(selectedBundle.SkillDecisions, skillName, "selected") {
			t.Fatalf("expected %s not to be selected for site content mentions, got %+v", skillName, selectedBundle.SkillDecisions)
		}
	}
	if strings.Contains(selectedBundle.Prompt, "Use mail tools.") || strings.Contains(selectedBundle.Prompt, "Use calendar tools.") || strings.Contains(selectedBundle.Prompt, "Use browser tools.") {
		t.Fatalf("expected content-domain skill bodies to be omitted, got %q", selectedBundle.Prompt)
	}
}

func TestSlidesArtifactRequestDoesNotSelectContentDomainSkills(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:        "simple-slides",
				Description: "Generate clean presentation slides with Marp and attach the requested files.",
				WhenToUse:   "Use for slides, slide decks, presentations, PPTX, PowerPoint, 발표자료, 파워포인트, 피피티.",
				Prompt:      "Use simple-slides tools.",
				Source:      InstructionSource{Path: "skills/simple-slides/SKILL.md", SkillName: "simple-slides"},
			},
			{
				Name:        "mail",
				Description: "Search, read, and send mail messages.",
				WhenToUse:   "Use when the user wants to operate on real email.",
				Prompt:      "Use mail tools.",
				Source:      InstructionSource{Path: "skills/mail/SKILL.md", SkillName: "mail"},
			},
			{
				Name:        "calendar",
				Description: "Create, list, and update calendar events and schedules.",
				WhenToUse:   "Use when the user wants to operate on real calendar data.",
				Prompt:      "Use calendar tools.",
				Source:      InstructionSource{Path: "skills/calendar/SKILL.md", SkillName: "calendar"},
			},
			{
				Name:        "browser",
				Description: "Control the browser and inspect web pages.",
				WhenToUse:   "Use when the user wants interactive browser control.",
				Prompt:      "Use browser tools.",
				Source:      InstructionSource{Path: "skills/browser/SKILL.md", SkillName: "browser"},
			},
		},
	}

	retriever := staticSkillRetriever{result: SkillRetrievalResult{
		SelectedCandidates: []SkillCandidate{
			{Name: "mail", Score: 30, Reason: "bm25_fallback"},
			{Name: "calendar", Score: 29, Reason: "bm25_fallback"},
			{Name: "browser", Score: 28, Reason: "bm25_fallback"},
			{Name: "simple-slides", Score: 8, Reason: "bm25_fallback"},
		},
		RetrievalMode: "bm25_fallback",
		IndexStatus:   "ready",
	}}
	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "메일, 일정, 브라우저 제어 능력을 소개하는 5장짜리 발표자료를 PPTX로 첨부해줘",
	}, retriever)

	if !skillDecisionHasStatus(selectedBundle.SkillDecisions, "simple-slides", "selected") {
		t.Fatalf("expected simple-slides selected, got %+v", selectedBundle.SkillDecisions)
	}
	for _, skillName := range []string{"mail", "calendar", "browser"} {
		if skillDecisionHasStatus(selectedBundle.SkillDecisions, skillName, "selected") {
			t.Fatalf("expected %s not to be selected for slides content mentions, got %+v", skillName, selectedBundle.SkillDecisions)
		}
	}
	if strings.Contains(selectedBundle.Prompt, "Use mail tools.") || strings.Contains(selectedBundle.Prompt, "Use calendar tools.") || strings.Contains(selectedBundle.Prompt, "Use browser tools.") {
		t.Fatalf("expected content-domain skill bodies to be omitted, got %q", selectedBundle.Prompt)
	}
}

func TestEmbeddingRetrieverSelectsSkillManagement(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:        "skill-management",
				Description: "Create, add, update, or remove user-managed Blueclaw skills, 스킬, and SKILL.md files.",
				WhenToUse:   "Use for skill 만들기, 스킬 추가, 스킬 삭제, SKILL.md 작성, and /skill-management.",
				Prompt:      "Use skill.add and skill.remove.",
				Source:      InstructionSource{Path: "skills/skill-management/SKILL.md", SHA256: "one", SkillName: "skill-management"},
			},
			{
				Name:        "calendar",
				Description: "Create or list calendar events.",
				Prompt:      "Use calendar tools.",
				Source:      InstructionSource{Path: "skills/calendar/SKILL.md", SHA256: "two", SkillName: "calendar"},
			},
		},
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "새 스킬 만들어서 추가해줘",
	}, retriever)

	if selectedBundle.RetrievalMode != "embedding" {
		t.Fatalf("expected embedding retrieval, got %q", selectedBundle.RetrievalMode)
	}
	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Name != "skill-management" || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected skill-management selected, got %+v", selectedBundle.SkillDecisions)
	}
	if !strings.Contains(selectedBundle.Prompt, "Use skill.add and skill.remove.") {
		t.Fatalf("expected selected skill-management body, got %q", selectedBundle.Prompt)
	}
	if strings.Contains(selectedBundle.Prompt, "Use calendar tools.") {
		t.Fatalf("expected unselected skill body to stay out of prompt, got %q", selectedBundle.Prompt)
	}
}

func TestEmbeddingRetrieverSelectsScheduledTaskForFiniteRepeat(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:         "scheduled-task",
				Description:  "Create scheduled, recurring, and finite repeated messages, 1분에 한 번씩, 10번, reminders, and repeats.",
				WhenToUse:    "Use for reminders, interval messages, 1분에 한 번씩, 10번, finite repeated message, and repeat N times requests.",
				Prompt:       "Use schedule.create with executionMode message for exact repeated messages, intervalSecond, and maxRunCount.",
				AllowedTools: []string{"schedule.create"},
				Source:       InstructionSource{Path: "skills/scheduled-task/SKILL.md", SHA256: "schedule", SkillName: "scheduled-task"},
			},
			{
				Name:        "simple-slides",
				Description: "Create presentation slides and PPTX decks.",
				Prompt:      "Generate slides.",
				Source:      InstructionSource{Path: "skills/simple-slides/SKILL.md", SHA256: "slides", SkillName: "simple-slides"},
			},
		},
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt:  `1분에 한 번씩 나한테 "죄송합니다" 10번 해봐`,
		ToolSet: testToolSet([]string{"schedule.create"}),
	}, retriever)

	if selectedBundle.RetrievalMode != "embedding" || selectedBundle.IndexStatus != "ready" {
		t.Fatalf("expected embedding retrieval, got mode=%q status=%q", selectedBundle.RetrievalMode, selectedBundle.IndexStatus)
	}
	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Name != "scheduled-task" || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected scheduled-task selected, got %+v", selectedBundle.SkillDecisions)
	}
	if !strings.Contains(selectedBundle.Prompt, "maxRunCount") {
		t.Fatalf("expected scheduled-task body, got %q", selectedBundle.Prompt)
	}
}

func TestStructuredSkillQueryCanSkipSkillSearch(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{{
			Name:        "mail",
			Description: "Read, search, summarize, reply to, and send email messages.",
			Prompt:      "Use mail tools.",
		}},
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")
	router := NewSkillSearchQueryRouter(staticStructuredLanguageModel{content: `{"queries":[]}`})

	selectedBundle := selectInstructionBundleForRequestWithRetrieverAndRouter(context.Background(), instructionBundle, AgentRequest{
		Prompt: "고마워",
	}, retriever, router)

	if selectedBundle.RetrievalMode != "structured_query" || selectedBundle.IndexStatus != "empty_query" {
		t.Fatalf("expected empty structured query, got mode=%q status=%q", selectedBundle.RetrievalMode, selectedBundle.IndexStatus)
	}
	if len(selectedBundle.SkillDecisions) != 0 || strings.Contains(selectedBundle.Prompt, "Use mail tools.") {
		t.Fatalf("expected no selected skills, got decisions=%+v prompt=%q", selectedBundle.SkillDecisions, selectedBundle.Prompt)
	}
}

func TestStructuredSkillQuerySelectsMailSkill(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{
			{
				Name:         "mail",
				Description:  "Read, search, summarize, reply to, and send email messages.",
				Prompt:       "Use mail.message.search and mail.message.read.",
				AllowedTools: []string{"mail.message.search", "mail.message.read"},
				Source:       InstructionSource{Path: "skills/mail/SKILL.md", SkillName: "mail"},
			},
			{
				Name:        "calendar",
				Description: "Create and list calendar events.",
				Prompt:      "Use calendar tools.",
			},
		},
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")
	router := NewSkillSearchQueryRouter(staticStructuredLanguageModel{content: `{"queries":[{"description":"Search and read recent email messages from GitHub."}]}`})

	selectedBundle := selectInstructionBundleForRequestWithRetrieverAndRouter(context.Background(), instructionBundle, AgentRequest{
		Prompt:  "나 최근 github한테 온 메일 있어?",
		ToolSet: testToolSet([]string{"mail.message.search", "mail.message.read"}),
	}, retriever, router)

	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Name != "mail" || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected mail selected, got %+v", selectedBundle.SkillDecisions)
	}
	if len(selectedBundle.SkillQueries) != 1 || !strings.Contains(selectedBundle.SkillQueries[0], "email messages") {
		t.Fatalf("expected structured skill query to be recorded, got %+v", selectedBundle.SkillQueries)
	}
	if !strings.Contains(selectedBundle.Prompt, "mail.message.search") {
		t.Fatalf("expected selected mail instructions, got %q", selectedBundle.Prompt)
	}
}

func TestSkillQueryRouterMessagesPrioritizeLatestRequest(t *testing.T) {
	router := NewSkillSearchQueryRouter(staticStructuredLanguageModel{content: `{"queries":[]}`})

	messages := router.buildMessages(AgentRequest{
		Prompt: "김인턴의 구조에 대해 웹사이트 하나 소개 형식으로 만들어줘.",
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "user", Text: "example.com 스타일로 사업계획서 PPT 만들어줘."},
		}},
		ActiveGoal:    ActiveGoal{CurrentObjective: "example.com 발표 자료 생성"},
		ToolSet:       testToolSet([]string{"site.app.create", "site.app.publish", "terminal.run"}),
		TurnStartedAt: time.Date(2026, time.May, 17, 1, 2, 3, 0, time.UTC),
	})

	if len(messages) == 0 || !strings.Contains(messages[0].Content, "latest user request is authoritative") {
		t.Fatalf("expected latest-request priority instruction, got %+v", messages)
	}
	if !strings.Contains(messages[0].Content, "Use prior conversation only when it is needed") {
		t.Fatalf("expected prior-context limitation instruction, got %q", messages[0].Content)
	}
	if !strings.Contains(messages[0].Content, "do not carry forward stale subjects") {
		t.Fatalf("expected stale-context instruction, got %q", messages[0].Content)
	}
	if !strings.Contains(joinMessageContent(messages), "Current date: 2026-05-17") {
		t.Fatalf("expected skill query temporal context, got %+v", messages)
	}
	if messages[len(messages)-1].Role != "user" || !strings.Contains(messages[len(messages)-1].Content, "김인턴") {
		t.Fatalf("expected latest prompt to remain the user message, got %+v", messages[len(messages)-1])
	}
}

func TestStructuredSkillQueryRecordsLatestRequestWebsiteQueryWithStaleContext(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{{
			Name:         "site-prototype",
			Description:  "Create and publish website prototypes.",
			Prompt:       "Use site.app.create and site.app.publish.",
			AllowedTools: []string{"site.app.create", "site.app.publish"},
			Source:       InstructionSource{Path: "skills/site-prototype/SKILL.md", SkillName: "site-prototype"},
		}},
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")
	router := NewSkillSearchQueryRouter(staticStructuredLanguageModel{content: `{"queries":[{"description":"Create a website introducing InternKim's structure."}]}`})

	selectedBundle := selectInstructionBundleForRequestWithRetrieverAndRouter(context.Background(), instructionBundle, AgentRequest{
		Prompt: "김인턴의 구조에 대해 웹사이트 하나 소개 형식으로 만들어줘.",
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "user", Text: "https://example.com 내용으로 사업계획서 PPT 만들어줘."},
		}},
		ToolSet: testToolSet([]string{"site.app.create", "site.app.publish"}),
	}, retriever, router)

	if len(selectedBundle.SkillQueries) != 1 || !strings.Contains(selectedBundle.SkillQueries[0], "InternKim") {
		t.Fatalf("expected latest-request website query to be recorded, got %+v", selectedBundle.SkillQueries)
	}
	if strings.Contains(strings.Join(selectedBundle.SkillQueries, "\n"), "example.com") || strings.Contains(strings.ToLower(strings.Join(selectedBundle.SkillQueries, "\n")), "ppt") {
		t.Fatalf("expected stale visible context to stay out of structured query, got %+v", selectedBundle.SkillQueries)
	}
}

func TestStructuredSkillQueryUsesAtMostFiveQueries(t *testing.T) {
	querySet := normalizeSkillSearchQuerySet(SkillSearchQuerySet{Queries: []SkillSearchQuery{
		{Description: "one"},
		{Description: "two"},
		{Description: "three"},
		{Description: "four"},
		{Description: "five"},
		{Description: "six"},
	}})

	if len(querySet.Queries) != 5 {
		t.Fatalf("expected five queries, got %+v", querySet.Queries)
	}
}

func TestDisableModelInvocationBlocksAutomaticRetrieval(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:                   "manual-only",
			Description:            "Create slides.",
			Prompt:                 "MANUAL BODY",
			DisableModelInvocation: true,
			Source:                 InstructionSource{Path: "skills/manual-only/SKILL.md", SHA256: "one", SkillName: "manual-only"},
		}},
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "create slides",
	}, retriever)

	if strings.Contains(selectedBundle.Prompt, "MANUAL BODY") {
		t.Fatalf("expected manual-only skill to stay out of automatic prompt, got %q", selectedBundle.Prompt)
	}
}

func TestDirectSkillNameBypassesDisableModelInvocation(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:                   "manual-only",
			Description:            "Create slides.",
			Prompt:                 "MANUAL BODY",
			DisableModelInvocation: true,
			Source:                 InstructionSource{Path: "skills/manual-only/SKILL.md", SHA256: "one", SkillName: "manual-only"},
		}},
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "/manual-only create slides",
	}, retriever)

	if !strings.Contains(selectedBundle.Prompt, "MANUAL BODY") {
		t.Fatalf("expected direct skill body, got %q", selectedBundle.Prompt)
	}
	if selectedBundle.RetrievalMode != "direct" {
		t.Fatalf("expected direct retrieval, got %q", selectedBundle.RetrievalMode)
	}
}

func TestHiddenFromCirclesSkipsAutomaticSkillRetrieval(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:              "finance-helper",
			Description:       "Handle finance report workflows.",
			WhenToUse:         "Use for finance report requests.",
			Prompt:            "FINANCE BODY",
			HiddenFromCircles: []string{"staff"},
		}},
	}

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt:           "finance report 만들어줘",
		RequesterCircles: []string{"staff"},
	}, nil)

	if strings.Contains(selectedBundle.Prompt, "finance-helper") || strings.Contains(selectedBundle.Prompt, "FINANCE BODY") {
		t.Fatalf("expected hidden skill to stay out of automatic prompt, got %q", selectedBundle.Prompt)
	}
}

func TestDirectSkillNameBypassesHiddenFromCirclesHint(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:              "finance-helper",
			Description:       "Handle finance report workflows.",
			Prompt:            "FINANCE BODY",
			HiddenFromCircles: []string{"staff"},
		}},
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt:           "/finance-helper 공개 자료로 보고서 만들어줘",
		RequesterCircles: []string{"staff"},
	}, retriever)

	if !strings.Contains(selectedBundle.Prompt, "FINANCE BODY") {
		t.Fatalf("expected direct hidden skill request to load body, got %q", selectedBundle.Prompt)
	}
	if selectedBundle.RetrievalMode != "direct" {
		t.Fatalf("expected direct retrieval, got %q", selectedBundle.RetrievalMode)
	}
}

func TestPathsPreventAutomaticRetrievalOutsideMatchingFiles(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:        "swiftui-pro",
			Description: "Review SwiftUI files.",
			WhenToUse:   "Use for SwiftUI code.",
			Prompt:      "SWIFT BODY",
			Paths:       []string{"*.swift"},
			Source:      InstructionSource{Path: "skills/swiftui-pro/SKILL.md", SHA256: "one", SkillName: "swiftui-pro"},
		}},
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt:      "review SwiftUI code",
		ActivePaths: []string{"README.md"},
	}, retriever)

	if strings.Contains(selectedBundle.Prompt, "SWIFT BODY") {
		t.Fatalf("expected paths to block automatic retrieval, got %q", selectedBundle.Prompt)
	}
}

func TestDirectSkillNameBypassesPathFilter(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:        "swiftui-pro",
			Description: "Review SwiftUI files.",
			Prompt:      "SWIFT BODY",
			Paths:       []string{"*.swift"},
			Source:      InstructionSource{Path: "skills/swiftui-pro/SKILL.md", SHA256: "one", SkillName: "swiftui-pro"},
		}},
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt:      "/swiftui-pro review",
		ActivePaths: []string{"README.md"},
	}, retriever)

	if !strings.Contains(selectedBundle.Prompt, "SWIFT BODY") {
		t.Fatalf("expected direct retrieval to bypass paths, got %q", selectedBundle.Prompt)
	}
}

func TestDirectSkillNameRequiresExactSlashToken(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:        "git",
				Description: "Use git.",
				Prompt:      "GIT BODY",
			},
			{
				Name:        "git-review",
				Description: "Review git changes.",
				Prompt:      "GIT REVIEW BODY",
			},
		},
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "/git-review please",
	}, retriever)

	if strings.Contains(selectedBundle.Prompt, "GIT BODY") {
		t.Fatalf("expected /git-review not to select /git, got %q", selectedBundle.Prompt)
	}
	if !strings.Contains(selectedBundle.Prompt, "GIT REVIEW BODY") {
		t.Fatalf("expected exact direct skill match, got %q", selectedBundle.Prompt)
	}
}

func TestSelectedFullSkillBodiesAreLimited(t *testing.T) {
	skills := []SkillInstruction{}
	for index := 0; index < 10; index++ {
		skills = append(skills, SkillInstruction{
			Name:        fmt.Sprintf("slides-%d", index),
			Description: "Create presentation slides and 피피티.",
			WhenToUse:   "Use for 피피티.",
			Prompt:      fmt.Sprintf("BODY %d", index),
			Source:      InstructionSource{Path: fmt.Sprintf("skills/slides-%d/SKILL.md", index), SHA256: fmt.Sprintf("sha-%d", index)},
		})
	}
	instructionBundle := InstructionBundle{Skills: skills}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "피피티",
	}, retriever)

	if strings.Count(selectedBundle.Prompt, "BODY ") != maxSelectedSkillInstructionCount {
		t.Fatalf("expected selected full bodies to be limited, got %q", selectedBundle.Prompt)
	}
}

func TestFifthRetrievedSkillIsSelectedBeforeLimit(t *testing.T) {
	skills := []SkillInstruction{}
	candidates := []SkillCandidate{}
	for index := 1; index <= 6; index++ {
		name := fmt.Sprintf("skill-%d", index)
		skills = append(skills, SkillInstruction{
			Name:        name,
			Description: fmt.Sprintf("Skill %d.", index),
			Prompt:      fmt.Sprintf("BODY %d", index),
			Source:      InstructionSource{Path: fmt.Sprintf("skills/%s/SKILL.md", name), SkillName: name},
		})
		candidates = append(candidates, SkillCandidate{Name: name, Score: 1, Reason: "test"})
	}
	instructionBundle := InstructionBundle{Skills: skills}
	retriever := staticSkillRetriever{result: SkillRetrievalResult{
		RetrievalMode:      "test",
		SelectedCandidates: candidates,
	}}

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "select several skills",
	}, retriever)

	if !strings.Contains(selectedBundle.Prompt, "BODY 5") {
		t.Fatalf("expected fifth skill body to be selected, got %q", selectedBundle.Prompt)
	}
	if strings.Contains(selectedBundle.Prompt, "BODY 6") {
		t.Fatalf("expected sixth skill body to be limited out, got %q", selectedBundle.Prompt)
	}
	for _, skillDecision := range selectedBundle.SkillDecisions {
		if skillDecision.Name == "skill-5" && skillDecision.Status != "selected" {
			t.Fatalf("expected fifth skill selected, got %+v", selectedBundle.SkillDecisions)
		}
		if skillDecision.Name == "skill-6" && skillDecision.Reason != "selected_skill_limit_reached" {
			t.Fatalf("expected sixth skill to hit selected limit, got %+v", selectedBundle.SkillDecisions)
		}
	}
}

func TestWebsiteSkillToolsSurviveWhenSkillIsFifthCandidate(t *testing.T) {
	skills := []SkillInstruction{
		{Name: "simple-slides", Description: "Create slides.", Prompt: "SLIDES BODY"},
		{Name: "pptx", Description: "Create PowerPoint files.", Prompt: "PPTX BODY"},
		{Name: "direct-message", Description: "Send direct messages.", Prompt: "DM BODY"},
		{Name: "report", Description: "Write reports.", Prompt: "REPORT BODY"},
		{
			Name:         "site-prototype",
			Description:  "Create and publish website prototypes.",
			Prompt:       "SITE BODY",
			AllowedTools: []string{"terminal.run", "site.app.create", "site.app.publish"},
			Source:       InstructionSource{Path: "skills/site-prototype/SKILL.md", SkillName: "site-prototype"},
		},
		{Name: "extra", Description: "Extra skill.", Prompt: "EXTRA BODY"},
	}
	candidates := []SkillCandidate{}
	for _, skill := range skills {
		candidates = append(candidates, SkillCandidate{Name: skill.Name, Score: 1, Reason: "test"})
	}
	instructionBundle := InstructionBundle{Skills: skills}
	retriever := staticSkillRetriever{result: SkillRetrievalResult{
		RetrievalMode:      "test",
		SelectedCandidates: candidates,
	}}

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "김인턴의 구조에 대해 웹사이트 하나 소개 형식으로 만들어줘.",
		ToolSet: testToolSet([]string{
			"terminal.run",
			"site.app.create",
			"site.app.publish",
		}),
	}, retriever)
	filteredToolSet := toolSetForSelectedSkills(testToolSet([]string{
		"terminal.run",
		"site.app.create",
		"site.app.publish",
	}), selectedBundle)

	for _, toolName := range []string{"terminal.run", "site.app.create", "site.app.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected fifth candidate site tool %s to be allowed, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	if strings.Contains(selectedBundle.Prompt, "EXTRA BODY") {
		t.Fatalf("expected sixth candidate body to stay out, got %q", selectedBundle.Prompt)
	}
}

func TestSkillIndexPromptStaysBoundedForManySkills(t *testing.T) {
	skills := []SkillInstruction{{
		Name:        "simple-slides",
		Description: "Create presentation slides and 피피티.",
		WhenToUse:   "Use for 피피티.",
		Prompt:      "Generate slides.",
		Source:      InstructionSource{Path: "skills/simple-slides/SKILL.md", SHA256: "match", SkillName: "simple-slides"},
	}}
	for index := 0; index < 1000; index++ {
		skills = append(skills, SkillInstruction{
			Name:        fmt.Sprintf("unrelated-%d", index),
			Description: "Archive unrelated data.",
			Prompt:      "UNRELATED BODY",
			Source:      InstructionSource{Path: fmt.Sprintf("skills/unrelated-%d/SKILL.md", index), SHA256: fmt.Sprintf("sha-%d", index)},
		})
	}
	instructionBundle := InstructionBundle{Skills: skills}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "피피티",
	}, retriever)

	if strings.Count(selectedBundle.Prompt, "\n- ") > maxSkillIndexCandidateCount {
		t.Fatalf("expected bounded skill index, got %q", selectedBundle.Prompt)
	}
	if strings.Contains(selectedBundle.Prompt, "UNRELATED BODY") {
		t.Fatalf("expected unrelated full bodies to stay out of prompt")
	}
}

func TestBM25FallbackIsObservableWhenEmbeddingUnavailable(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:        "simple-slides",
			Description: "Create presentation slides and 피피티.",
			WhenToUse:   "Use for 피피티.",
			Prompt:      "Generate slides.",
		}},
	}

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "피피티",
	}, NewEmbeddingSkillRetriever(nil, ""))

	if selectedBundle.RetrievalMode != "bm25_fallback" {
		t.Fatalf("expected BM25 fallback, got %q", selectedBundle.RetrievalMode)
	}
}

func TestBM25FallbackIsObservableWhenEmbeddingDimensionMismatches(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:        "simple-slides",
			Description: "Create presentation slides and 피피티.",
			WhenToUse:   "Use for 피피티.",
			Prompt:      "Generate slides.",
			Source:      InstructionSource{Path: "skills/simple-slides/SKILL.md", SHA256: "one", SkillName: "simple-slides"},
		}},
	}
	retriever := NewEmbeddingSkillRetriever(&dimensionChangingEmbeddingProvider{}, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "피피티",
	}, retriever)

	if selectedBundle.RetrievalMode != "bm25_fallback" || selectedBundle.IndexStatus != "embedding_dimension_mismatch" {
		t.Fatalf("expected dimension mismatch BM25 fallback, got mode=%q status=%q", selectedBundle.RetrievalMode, selectedBundle.IndexStatus)
	}
	if !strings.Contains(selectedBundle.Prompt, "Generate slides.") {
		t.Fatalf("expected BM25 fallback to select skill, got %q", selectedBundle.Prompt)
	}
}

func TestSkillIndexRefreshesWhenSourceHashChanges(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "skill-index.json")
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, indexPath)
	firstBundle := []SkillInstruction{{
		Name:        "simple-slides",
		Description: "Create presentation slides and 피피티.",
		Source:      InstructionSource{Path: "skills/simple-slides/SKILL.md", SHA256: "one", SkillName: "simple-slides"},
	}}
	secondBundle := []SkillInstruction{{
		Name:        "simple-slides",
		Description: "Create presentation slides.",
		Source:      InstructionSource{Path: "skills/simple-slides/SKILL.md", SHA256: "two", SkillName: "simple-slides"},
	}}

	retriever.Refresh(context.Background(), firstBundle)
	retriever.Refresh(context.Background(), secondBundle)

	document, errorValue := os.ReadFile(indexPath)
	if errorValue != nil {
		t.Fatalf("expected materialized skill index: %v", errorValue)
	}
	if !strings.Contains(string(document), `"sourceSHA256": "two"`) || strings.Contains(string(document), `"sourceSHA256": "one"`) {
		t.Fatalf("expected index to refresh by source hash, got %s", string(document))
	}
}

func TestSkillIndexRefreshesWhenSearchDocumentVersionChanges(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "skill-index.json")
	legacyDocument := `[{"skillName":"simple-slides","sourcePath":"skills/simple-slides/SKILL.md","sourceSHA256":"one","searchText":"Create presentation slides.","embeddingModel":"embedding.create","embedding":[1],"indexedAt":"2026-01-01T00:00:00Z"}]`
	if errorValue := os.WriteFile(indexPath, []byte(legacyDocument), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	retriever := NewEmbeddingSkillRetriever(keywordEmbeddingProvider{}, indexPath)

	retriever.Refresh(context.Background(), []SkillInstruction{{
		Name:        "simple-slides",
		Description: "Create presentation slides.",
		Source:      InstructionSource{Path: "skills/simple-slides/SKILL.md", SHA256: "one", SkillName: "simple-slides"},
	}})

	document, errorValue := os.ReadFile(indexPath)
	if errorValue != nil {
		t.Fatalf("expected materialized skill index: %v", errorValue)
	}
	if !strings.Contains(string(document), skillSearchDocumentVersion) {
		t.Fatalf("expected versioned skill index, got %s", string(document))
	}
	if strings.Contains(string(document), `"embeddingModel": "embedding.create"`) {
		t.Fatalf("expected legacy embedding model key to be replaced, got %s", string(document))
	}
}

type keywordEmbeddingProvider struct{}

func (provider keywordEmbeddingProvider) GenerateEmbedding(_ context.Context, input string) ([]float32, error) {
	normalizedInput := normalizeSkillSearchText(input)
	return []float32{
		keywordEmbeddingValue(normalizedInput, []string{"피피티", "pptx", "slides", "presentation"}),
		keywordEmbeddingValue(normalizedInput, []string{"calendar", "event", "일정", "캘린더"}),
		keywordEmbeddingValue(normalizedInput, []string{"archive", "unrelated"}),
		keywordEmbeddingValue(normalizedInput, []string{"skill", "skills", "스킬", "skill.md"}),
		keywordEmbeddingValue(normalizedInput, []string{"schedule", "scheduled", "reminder", "repeat", "finite", "repeated", "1분에", "한", "번씩", "10번"}),
		keywordEmbeddingValue(normalizedInput, []string{"html"}),
		keywordEmbeddingValue(normalizedInput, []string{"mail", "email", "inbox", "message", "messages", "github"}),
	}, nil
}

type dimensionChangingEmbeddingProvider struct {
	callCount int
}

func (provider *dimensionChangingEmbeddingProvider) GenerateEmbedding(_ context.Context, input string) ([]float32, error) {
	provider.callCount++
	normalizedInput := normalizeSkillSearchText(input)
	if provider.callCount == 1 {
		return []float32{keywordEmbeddingValue(normalizedInput, []string{"피피티", "slides", "presentation"})}, nil
	}
	return []float32{keywordEmbeddingValue(normalizedInput, []string{"피피티", "slides", "presentation"}), 0}, nil
}

func keywordEmbeddingValue(input string, keywords []string) float32 {
	for _, keyword := range keywords {
		if strings.Contains(input, keyword) {
			return 1
		}
	}
	return 0
}

type staticStructuredLanguageModel struct {
	content string
}

func (languageModel staticStructuredLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticStructuredLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{Content: languageModel.content}, nil
}

type staticSkillRetriever struct {
	result SkillRetrievalResult
}

func (retriever staticSkillRetriever) Retrieve(context.Context, AgentRequest, []SkillInstruction, int) SkillRetrievalResult {
	return retriever.result
}

func (retriever staticSkillRetriever) Search(context.Context, AgentRequest, []SkillInstruction, SkillSearchQuerySet, int) SkillRetrievalResult {
	return retriever.result
}

func (retriever staticSkillRetriever) Refresh(context.Context, []SkillInstruction) {}

func skillDecisionHasStatus(skillDecisions []SkillSelectionDecision, skillName string, status string) bool {
	for _, skillDecision := range skillDecisions {
		if skillDecision.Name == skillName && skillDecision.Status == status {
			return true
		}
	}
	return false
}

func testToolSet(toolNames []string) *ToolSet {
	toolRegistry := newTestToolSet(toolNames)
	for _, toolName := range toolNames {
		toolRegistry.RegisterTool(ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolResult{}, nil
		})
	}
	return toolRegistry
}
