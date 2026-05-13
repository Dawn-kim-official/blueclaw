package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		"approval.request",
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

	for _, toolName := range []string{"conversation.history", "memory.search", "approval.request", "terminal.run", "site.app.create", "site.app.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	if filteredToolSet.IsAllowed("schedule.create") {
		t.Fatalf("expected unrelated schedule.create to be hidden, got %+v", filteredToolSet.ListToolNames())
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
	for index := 0; index < 5; index++ {
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

func testToolSet(toolNames []string) *ToolSet {
	toolRegistry := newTestToolSet(toolNames)
	for _, toolName := range toolNames {
		toolRegistry.RegisterTool(ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolResult{}, nil
		})
	}
	return toolRegistry
}
