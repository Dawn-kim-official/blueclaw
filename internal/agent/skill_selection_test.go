package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
)

func TestSelectInstructionBundleIncludesPresentationForKoreanPPTRequest(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{
			{
				Name:           "presentation",
				Description:    "Create presentation decks, 피피티, 파워포인트, 발표자료, and PPTX files.",
				WhenToUse:      "Use for 피피티, 파워포인트, 발표자료, and PPTX requests.",
				Category:       "document-generation",
				Tags:           []string{"slides", "pptx"},
				Prompt:         "Generate PPTX with Marp.",
				TriggerHints:   []string{"피피티", "파워포인트", "발표자료", "pptx"},
				ToolReferences: []string{"terminal.run", "file.write", "file.deliver"},
				Source:         InstructionSource{Path: "skills/presentation/SKILL.md", SkillName: "presentation"},
			},
		},
	}

	selectedBundle := selectInstructionBundleForRequest(instructionBundle, AgentRequest{
		Prompt:  "너 뭐 할 수 있는지 피피티 만들어서 보내줘봐",
		ToolSet: testToolSet([]string{"terminal.run", "file.write", "file.deliver"}),
	})

	if !strings.Contains(selectedBundle.Prompt, "Generate PPTX with Marp.") {
		t.Fatalf("expected presentation skill prompt for Korean PPT request, got %q", selectedBundle.Prompt)
	}
	if !strings.Contains(selectedBundle.Prompt, "Available skill index") || !strings.Contains(selectedBundle.Prompt, "presentation: Create presentation decks, 피피티") {
		t.Fatalf("expected compact skill index, got %q", selectedBundle.Prompt)
	}
	if !strings.Contains(selectedBundle.Prompt, "Available skill references") || !strings.Contains(selectedBundle.Prompt, "They are not mandatory") {
		t.Fatalf("expected selected skill prompt to be framed as references, got %q", selectedBundle.Prompt)
	}
	if !strings.Contains(selectedBundle.Prompt, "Source: /workspace/skills/presentation/SKILL.md") ||
		!strings.Contains(selectedBundle.Prompt, "Resolve relative scripts, references, and assets from the source directory.") {
		t.Fatalf("expected selected skill resources to have a canonical base path, got %q", selectedBundle.Prompt)
	}
	if strings.Contains(selectedBundle.Prompt, "Selected skill instructions") {
		t.Fatalf("expected no mandatory selected skill framing, got %q", selectedBundle.Prompt)
	}
	if len(selectedBundle.Sources) != 1 || selectedBundle.Sources[0].SkillName != "presentation" {
		t.Fatalf("expected presentation instruction source, got %+v", selectedBundle.Sources)
	}
	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected selected skill decision, got %+v", selectedBundle.SkillDecisions)
	}
}

func TestSelectInstructionBundleDoesNotUseStaleVisibleContextForRetrieval(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{
			{
				Name:           "presentation",
				Description:    "Create presentation decks, 피피티, 파워포인트, 발표자료, and PPTX files.",
				WhenToUse:      "Use for 피피티 and PPTX requests.",
				Prompt:         "Generate PPTX with Marp.",
				TriggerHints:   []string{"피피티", "pptx"},
				ToolReferences: []string{"terminal.run", "file.write", "file.deliver"},
				Source:         InstructionSource{Path: "skills/presentation/SKILL.md", SkillName: "presentation"},
			},
		},
	}

	selectedBundle := selectInstructionBundleForRequest(instructionBundle, AgentRequest{
		Prompt: "별로야. 폐기하고 새로 다시 해줘.",
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "user", Text: "너 뭐 할 수 있는지 8장 피피티 만들어서 보내줘봐"},
		}},
		ToolSet: testToolSet([]string{"terminal.run", "file.write", "file.deliver"}),
	})

	if len(selectedBundle.SkillDecisions) != 0 {
		t.Fatalf("expected raw request retrieval not to inherit stale context, got %+v", selectedBundle.SkillDecisions)
	}
	if strings.Contains(selectedBundle.Prompt, "Generate PPTX with Marp.") {
		t.Fatalf("expected stale-context skill body to stay unloaded, got %q", selectedBundle.Prompt)
	}
}

func TestSelectInstructionBundleDoesNotUseTriggerHintOutsideRetrievalCandidates(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{
			{
				Name:           "site-prototype",
				Description:    "Create and publish web prototypes.",
				WhenToUse:      "Use for website prototype requests.",
				Prompt:         "Use site.create, terminal.run, and site.publish.",
				ToolReferences: []string{"terminal.run", "site.create", "site.publish"},
				Source:         InstructionSource{Path: "skills/site-prototype/SKILL.md", SkillName: "site-prototype"},
			},
		},
	}

	selectedBundle := selectInstructionBundleForRequest(instructionBundle, AgentRequest{
		Prompt:  "웹사이트 하나 만들어서 배포해봐",
		ToolSet: testToolSet([]string{"terminal.run", "site.create", "site.publish"}),
	})

	if strings.Contains(selectedBundle.Prompt, "Use site.create") {
		t.Fatalf("expected trigger hint not to load full skill body, got %q", selectedBundle.Prompt)
	}
	for _, skillDecision := range selectedBundle.SkillDecisions {
		if skillDecision.Name == "site-prototype" && skillDecision.Status == "selected" {
			t.Fatalf("expected trigger hint not to select site-prototype, got %+v", selectedBundle.SkillDecisions)
		}
	}
}

func TestToolSetForAgentTurnExposesSelectedSkillToolsAlongsideKernel(t *testing.T) {
	fullToolSet := testToolSet([]string{
		"conversation.history",
		"memory.search",
		"terminal.run",
		"site.create",
		"site.publish",
		"schedule.create",
	})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:           "site-prototype",
				ToolReferences: []string{"terminal.run", "site.create", "site.publish"},
			},
			{
				Name:           "scheduled-task",
				ToolReferences: []string{"schedule.create"},
			},
		},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}

	filteredToolSet := toolSetForAgentTurn(fullToolSet, instructionBundle, AgentRequest{Prompt: "사이트 만들어줘"}, ExecutionPlan{}, false, OutcomeContract{})

	// terminal.run and conversation.history are kernel tools in this fixture; memory.search is not.
	for _, toolName := range []string{"terminal.run", "conversation.history"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected kernel tool %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	for _, toolName := range []string{"memory.search", "schedule.create"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected unselected tool %s to stay hidden, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	for _, toolName := range []string{"site.create", "site.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill tool %s to be directly callable, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestSelectedFlowSkillExposesRegisteredDirectToolsFromKernelPalette(t *testing.T) {
	toolSet := NewToolSet(KernelToolNames())
	for _, toolName := range append(KernelToolNames(), "task.add", "task.list", "task.update", "task.delete") {
		registerTestTool(toolSet, ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}
	flowSkill := SkillInstruction{
		Name:           "internkim-flow",
		ToolReferences: []string{"task.add", "task.list", "task.update", "task.delete"},
	}
	request := AgentRequest{ToolSet: toolSet}

	availability := skillAvailabilityDecision(flowSkill, request, "default")
	if availability.Reason == "missing_tool_references" {
		t.Fatalf("expected registered direct tools to make internkim-flow reachable, got %+v", availability)
	}

	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{flowSkill},
		SkillDecisions: []SkillSelectionDecision{{Name: flowSkill.Name, Status: "selected"}},
	}
	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, OutcomeContract{})
	for _, toolName := range flowSkill.ToolReferences {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected flow tool %s to be directly exposed, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestToolSetForAgentTurnExposesOnlyPinnedNonKernelTools(t *testing.T) {
	fullToolSet := testToolSet([]string{
		"conversation.history",
		"memory.search",
		"math.calculate",
		"terminal.run",
		"file.write",
		"schedule.create",
		"mail.message.search",
	})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "scheduled-task", ToolReferences: []string{"schedule.create"}},
			{Name: "mail", ToolReferences: []string{"mail.message.search"}},
		},
		SkillDecisions: []SkillSelectionDecision{{Name: "scheduled-task", Status: "selected"}},
	}

	filteredToolSet := toolSetForAgentTurn(fullToolSet, instructionBundle, AgentRequest{
		Prompt:          "내일 알려줘",
		PinnedToolNames: []string{"schedule.create"},
	}, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"terminal.run", "file.write", "schedule.create", "conversation.history"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected tool %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	for _, toolName := range []string{"memory.search", "math.calculate", "mail.message.search"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected unpinned non-kernel tool %s to be hidden, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestToolSetForAgentTurnHidesSendToolButKeepsKernelToolForNonSendOutcome(t *testing.T) {
	fullToolSet := testToolSet([]string{"message.send", "file.write"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           "direct-message",
			ToolReferences: []string{"message.send"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	contract := OutcomeContract{SelectedEvidenceHints: []string{"message.send"}}

	filteredToolSet := toolSetForAgentTurn(fullToolSet, instructionBundle, AgentRequest{Prompt: "사업계획서 작성해줘"}, ExecutionPlan{}, false, contract)

	if !filteredToolSet.IsAllowed("message.send") {
		t.Fatalf("expected selected send tool to be directly callable, got %+v", filteredToolSet.ListToolNames())
	}
	if !filteredToolSet.IsAllowed("file.write") {
		t.Fatalf("expected kernel tool file.write to remain available, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestAgentKernelActionSchemaExposesTypedInitialTools(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"requiredEvidence":["schedule.create"],"initialToolNames":["schedule.create"],"reason":"schedule request","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("done"),
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	services.kernel.UseSkillRetriever(NewEmbeddingSkillRetriever(nil, ""))
	services.kernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{
			{
				Name:           "scheduled-task",
				Description:    "Create schedule, scheduled, reminder, repeat, and repeated tasks.",
				WhenToUse:      "Use for reminders, scheduled tasks, repeat requests, 1분에 한 번씩, and 10번 repeated work.",
				Prompt:         "Use schedule.create for reminders.",
				TriggerHints:   []string{"schedule", "reminder", "repeat", "10번"},
				ToolReferences: []string{"schedule.create"},
				Source:         InstructionSource{Path: "skills/scheduled-task/SKILL.md", SkillName: "scheduled-task"},
			},
			{
				Name:           "mail",
				Description:    "Search mail.",
				Prompt:         "Use mail.message.search.",
				ToolReferences: []string{"mail.message.search"},
				Source:         InstructionSource{Path: "skills/mail/SKILL.md", SkillName: "mail"},
			},
		}}
	})
	// The initial tool list contains direct typed tools; skill selection can add more direct tools later.
	toolRegistry := newTestCapabilityToolSet([]string{"schedule.create", "mail.message.search", "math.calculate"})
	registerTestTool(toolRegistry, ToolDefinition{Name: AskInputToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{}, nil
	})

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
		t.Fatalf("expected direct initial schedule.create in action schema, got %s", actionSchema)
	}
	if strings.Contains(actionSchema, AskInputToolName) {
		t.Fatalf("expected ask.input to stay hidden without a typed interaction requirement, got %s", actionSchema)
	}
	for _, domainToolName := range []string{"mail.message.search", "math.calculate"} {
		if strings.Contains(actionSchema, `"toolName":{"enum":["`+domainToolName+`"`) {
			t.Fatalf("expected unselected tool %s not to be directly callable, got %s", domainToolName, actionSchema)
		}
	}
}

func TestSkillSelectorOnlyChecksSkillAvailability(t *testing.T) {
	skillSelector := SkillSelector{}
	skillInstruction := SkillInstruction{
		Name:           "presentation",
		TriggerHints:   []string{"피피티", "파워포인트", "발표자료", "pptx"},
		ToolReferences: []string{"terminal.run", "file.write", "file.deliver"},
	}
	request := AgentRequest{Prompt: "피피티 만들어줘", ToolSet: testToolSet([]string{"terminal.run", "file.write", "file.deliver"})}

	if skillSelector.ShouldInclude(skillInstruction, request) {
		t.Fatal("expected prompt hints not to select skills outside retrieval")
	}
}

func TestSkillSelectorSkipsSkillWhenAllowedToolIsMissing(t *testing.T) {
	skillSelector := SkillSelector{}
	skillInstruction := SkillInstruction{
		Name:           "presentation",
		TriggerHints:   []string{"피피티"},
		ToolReferences: []string{"terminal.run", "file.write", "file.deliver"},
	}
	request := AgentRequest{
		Prompt:  "피피티 만들어줘",
		ToolSet: testToolSet([]string{"terminal.run", "file.write"}),
	}

	decision := skillSelector.Evaluate(skillInstruction, request, "default")
	if decision.Status == "selected" {
		t.Fatal("expected presentation to be skipped without file.deliver")
	}
	if decision.Reason != "missing_tool_references" || len(decision.MissingToolReferences) != 1 || decision.MissingToolReferences[0] != "file.deliver" {
		t.Fatalf("expected missing tool reason, got %+v", decision)
	}
}

func TestSelectInstructionBundleKeepsSkillWhenDirectToolsAreAvailable(t *testing.T) {
	toolSet := NewToolSet([]string{"terminal.run", "site.create", "site.publish"})
	for _, toolName := range []string{"terminal.run", "site.create", "site.publish"} {
		currentToolName := toolName
		registerTestTool(toolSet, ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}
	instructionBundle := InstructionBundle{Skills: []SkillInstruction{{
		Name:           "site-prototype",
		Description:    "Create and publish website prototypes.",
		Prompt:         "SITE BODY",
		ToolReferences: []string{"terminal.run", "site.create", "site.publish"},
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

	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected directly callable site skill to be selected, got %+v", selectedBundle.SkillDecisions)
	}
	filteredToolSet := toolSetForAgentTurn(toolSet, selectedBundle, AgentRequest{Prompt: "김인턴 소개 웹사이트 만들어줘"}, ExecutionPlan{}, false, OutcomeContract{})
	if !filteredToolSet.IsAllowed("site.create") || !filteredToolSet.IsAllowed("site.publish") {
		t.Fatalf("expected selected skill tools to be directly callable, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestSelectInstructionBundleSkipsSkillWhenDirectToolIsUnavailable(t *testing.T) {
	toolSet := NewToolSet([]string{"terminal.run"})
	registerTestTool(toolSet, ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ok"), nil
	})
	for _, toolName := range []string{"site.create", "site.publish"} {
		toolSet.RegisterBoundTool(BoundTool{
			Definition:   ToolDefinition{Name: toolName},
			Availability: ToolAvailability{Status: ToolAvailabilityUnavailable},
			Handler: func(context.Context, ToolInvocation) (ToolResult, error) {
				return testToolSuccess("ok"), nil
			},
		})
	}
	instructionBundle := InstructionBundle{Skills: []SkillInstruction{{
		Name:           "site-prototype",
		Description:    "Create and publish website prototypes.",
		Prompt:         "SITE BODY",
		ToolReferences: []string{"terminal.run", "site.create", "site.publish"},
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

	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Status == "selected" {
		t.Fatalf("expected skill to be skipped without directly callable tools, got %+v", selectedBundle.SkillDecisions)
	}
}

func TestSelectInstructionBundleKeepsUnselectedFullSkillBodyOutOfPrompt(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{
			{
				Name:           "presentation",
				Description:    "Create decks.",
				Prompt:         "Generate PPTX with Marp.",
				TriggerHints:   []string{"피피티"},
				ToolReferences: []string{"terminal.run"},
			},
			{
				Name:           "create-gws-file",
				Description:    "Create spreadsheets.",
				Prompt:         "SECRET FULL BODY",
				TriggerHints:   []string{"spreadsheet"},
				ToolReferences: []string{"terminal.run"},
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

func TestBM25RetrieverSelectsStandardSkill(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:        "presentation",
				Description: "Create presentation slides, 피피티, and PPTX decks.",
				WhenToUse:   "Use for pitch decks, 발표자료, 피피티, and PowerPoint requests.",
				Prompt:      "Generate slides.",
				Source:      InstructionSource{Path: "skills/presentation/SKILL.md", SHA256: "one", SkillName: "presentation"},
			},
			{
				Name:        "calendar",
				Description: "Create or list calendar events.",
				Prompt:      "Follow calendar workflow.",
				Source:      InstructionSource{Path: "skills/calendar/SKILL.md", SHA256: "two", SkillName: "calendar"},
			},
		},
	}
	retriever := NewEmbeddingSkillRetriever(nil, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "피피티 만들어줘",
	}, retriever)

	if selectedBundle.RetrievalMode != "bm25_fallback" || selectedBundle.IndexStatus != "embedding_unavailable" {
		t.Fatalf("expected BM25 retrieval, got mode=%q status=%q", selectedBundle.RetrievalMode, selectedBundle.IndexStatus)
	}
	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Name != "presentation" || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected presentation selected, got %+v", selectedBundle.SkillDecisions)
	}
	if !strings.Contains(selectedBundle.Prompt, "Generate slides.") {
		t.Fatalf("expected selected skill body, got %q", selectedBundle.Prompt)
	}
	if strings.Contains(selectedBundle.Prompt, "Follow calendar workflow.") {
		t.Fatalf("expected unselected skill body to stay out of prompt, got %q", selectedBundle.Prompt)
	}
}

func TestSiteArtifactRequestAllowsContentDomainSkillsButGuidesPromptToTheActualTask(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:        "site-prototype",
				Description: "Create, publish, and update website prototypes, homepages, web apps, landing pages, and deployed sites.",
				WhenToUse:   "Use for website, homepage, web app, site, publish, deploy, 홈페이지, 웹사이트, 사이트, and 배포 requests.",
				Prompt:      "Follow site prototype workflow.",
				Source:      InstructionSource{Path: "skills/site-prototype/SKILL.md", SkillName: "site-prototype"},
			},
			{
				Name:        "mail",
				Description: "Search, read, and send mail messages.",
				WhenToUse:   "Use when the user wants to operate on real email.",
				Prompt:      "Follow mail workflow.",
				Source:      InstructionSource{Path: "skills/mail/SKILL.md", SkillName: "mail"},
			},
			{
				Name:        "calendar",
				Description: "Create, list, and update calendar events and schedules.",
				WhenToUse:   "Use when the user wants to operate on real calendar data.",
				Prompt:      "Follow calendar workflow.",
				Source:      InstructionSource{Path: "skills/calendar/SKILL.md", SkillName: "calendar"},
			},
			{
				Name:        "browser",
				Description: "Control the browser and inspect web pages.",
				WhenToUse:   "Use when the user wants interactive browser control.",
				Prompt:      "Follow browser workflow.",
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
	languageModel := &schemaStructuredLanguageModel{contentBySchema: map[string]string{
		"blueclaw_skill_search_queries":       `{"queries":[]}`,
		"blueclaw_contract_skill_arbitration": `{"selectedSkillNames":["site-prototype","mail","calendar","browser"],"rejectedSkillNames":[],"requiredNextToolNames":[],"expectedEvidence":[],"unmetPreconditions":[],"reason":"Use the website workflow and the referenced capability descriptions as content."}`,
	}}
	selectedBundle := selectInstructionBundleForRequestWithRetrieverAndRouter(
		context.Background(),
		instructionBundle,
		AgentRequest{
			Prompt: "메일, 일정, 브라우저 제어 능력을 소개하는 세련된 개인 홈페이지 하나 만들어서 배포해줘",
			ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{ExpectedResults: []ExpectedResult{
				{ID: "site-public-link", Type: "link", Description: "public website URL", Required: true},
			}}},
		},
		retriever,
		NewSkillSearchQueryRouter(languageModel),
	)

	if !skillDecisionHasStatus(selectedBundle.SkillDecisions, "site-prototype", "selected") {
		t.Fatalf("expected site-prototype selected, got %+v", selectedBundle.SkillDecisions)
	}
	// Mentioning mail/calendar/browser as content for the site is a legitimate reason to
	// select those skills too (the model may need their descriptions to write accurate
	// copy). Selection is not narrowed deterministically; instead the prompt tells the
	// model to only act on what the request actually needs.
	if !strings.Contains(selectedBundle.Prompt, "only use the ones this specific request actually needs") {
		t.Fatalf("expected selected-skill prompt to guide the model toward the actual task, got %q", selectedBundle.Prompt)
	}
}

func TestNonArtifactFlowTaskRequestIsNotDominatedByPresentation(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:           "presentation",
				Description:    "Generate clean presentation slides with Marp and attach the requested files.",
				WhenToUse:      "Use for slides, slide decks, presentations, PPTX, PowerPoint, 발표자료, 파워포인트, 피피티.",
				Prompt:         "Follow slides workflow.",
				ToolReferences: []string{"terminal.run", "file.write", "file.deliver"},
				Source:         InstructionSource{Path: "skills/presentation/SKILL.md", SkillName: "presentation"},
			},
			{
				Name:           "internkim-flow",
				Description:    "Manage InternKim todo tasks with flow.task capability operations.",
				WhenToUse:      "Use for 업무, 할 일, todo, task 등록, 목록, 완료, 수정 requests.",
				Prompt:         "Use flow.task capability operations.",
				ToolReferences: []string{"task.add", "task.list", "task.update"},
				Source:         InstructionSource{Path: "skills/internkim-flow/SKILL.md", SkillName: "internkim-flow"},
			},
		},
	}
	retriever := staticSkillRetriever{result: SkillRetrievalResult{
		SelectedCandidates: []SkillCandidate{
			{Name: "presentation", Score: 30, Reason: "test"},
			{Name: "internkim-flow", Score: 8, Reason: "test"},
		},
		RetrievalMode: "test",
		IndexStatus:   "ready",
	}}

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "업무 등록해줘\n- 메일 페이지 앱 비밀번호, 다양한 사이트 관련 링크로 이동으로 개선하기",
		ToolSet: testToolSet([]string{
			"terminal.run",
			"file.write",
			"file.deliver",
			"task.add",
			"task.list",
			"task.update",
		}),
	}, retriever)

	if !skillDecisionHasStatus(selectedBundle.SkillDecisions, "internkim-flow", "selected") {
		t.Fatalf("expected internkim-flow selected for flow task work, got %+v", selectedBundle.SkillDecisions)
	}
	if skillDecisionHasStatus(selectedBundle.SkillDecisions, "internkim-flow", "skipped") {
		t.Fatalf("expected internkim-flow not to be skipped by presentation dominance, got %+v", selectedBundle.SkillDecisions)
	}
	if !strings.Contains(selectedBundle.Prompt, "Use flow.task capability operations.") {
		t.Fatalf("expected internkim-flow instructions in prompt, got %q", selectedBundle.Prompt)
	}
}

func TestBM25RetrieverSelectsSkillManagement(t *testing.T) {
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
				Prompt:      "Follow calendar workflow.",
				Source:      InstructionSource{Path: "skills/calendar/SKILL.md", SHA256: "two", SkillName: "calendar"},
			},
		},
	}
	retriever := NewEmbeddingSkillRetriever(nil, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt: "새 스킬 만들어서 추가해줘",
	}, retriever)

	if selectedBundle.RetrievalMode != "bm25_fallback" {
		t.Fatalf("expected BM25 retrieval, got %q", selectedBundle.RetrievalMode)
	}
	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Name != "skill-management" || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected skill-management selected, got %+v", selectedBundle.SkillDecisions)
	}
	if !strings.Contains(selectedBundle.Prompt, "Use skill.add and skill.remove.") {
		t.Fatalf("expected selected skill-management body, got %q", selectedBundle.Prompt)
	}
	if strings.Contains(selectedBundle.Prompt, "Follow calendar workflow.") {
		t.Fatalf("expected unselected skill body to stay out of prompt, got %q", selectedBundle.Prompt)
	}
}

func TestBM25RetrieverSelectsScheduledTaskForFiniteRepeat(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name:           "scheduled-task",
				Description:    "Create scheduled, recurring, and finite repeated messages, 1분에 한 번씩, 10번, reminders, and repeats.",
				WhenToUse:      "Use for reminders, interval messages, 1분에 한 번씩, 10번, finite repeated message, and repeat N times requests.",
				Prompt:         "Use schedule.create with taskInstruction for the run-time work, intervalSecond, and maxRunCount.",
				ToolReferences: []string{"schedule.create"},
				Source:         InstructionSource{Path: "skills/scheduled-task/SKILL.md", SHA256: "schedule", SkillName: "scheduled-task"},
			},
			{
				Name:        "presentation",
				Description: "Create presentation slides and PPTX decks.",
				Prompt:      "Generate slides.",
				Source:      InstructionSource{Path: "skills/presentation/SKILL.md", SHA256: "slides", SkillName: "presentation"},
			},
		},
	}
	retriever := NewEmbeddingSkillRetriever(nil, "")

	selectedBundle := selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, AgentRequest{
		Prompt:  `1분에 한 번씩 나한테 "죄송합니다" 10번 해봐`,
		ToolSet: testToolSet([]string{"schedule.create"}),
	}, retriever)

	if selectedBundle.RetrievalMode != "bm25_fallback" || selectedBundle.IndexStatus != "embedding_unavailable" {
		t.Fatalf("expected BM25 retrieval, got mode=%q status=%q", selectedBundle.RetrievalMode, selectedBundle.IndexStatus)
	}
	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Name != "scheduled-task" || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected scheduled-task selected, got %+v", selectedBundle.SkillDecisions)
	}
	if !strings.Contains(selectedBundle.Prompt, "maxRunCount") {
		t.Fatalf("expected scheduled-task body, got %q", selectedBundle.Prompt)
	}
}

func TestEmptyStructuredSkillQueryStillSearchesRawRequest(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{{
			Name:        "mail",
			Description: "Read, search, summarize, reply to, and send email messages.",
			Prompt:      "Follow mail workflow.",
		}},
	}
	retriever := NewEmbeddingSkillRetriever(nil, "")
	router := NewSkillSearchQueryRouter(staticStructuredLanguageModel{content: `{"queries":[]}`})

	selectedBundle := selectInstructionBundleForRequestWithRetrieverAndRouter(context.Background(), instructionBundle, AgentRequest{
		Prompt: "Read and summarize recent email messages.",
	}, retriever, router)

	if selectedBundle.RetrievalMode != "bm25_fallback" || selectedBundle.IndexStatus != "embedding_unavailable" {
		t.Fatalf("expected raw request retrieval, got mode=%q status=%q", selectedBundle.RetrievalMode, selectedBundle.IndexStatus)
	}
	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Name != "mail" || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected raw request to select mail skill, got decisions=%+v prompt=%q", selectedBundle.SkillDecisions, selectedBundle.Prompt)
	}
}

func TestStructuredSkillQuerySelectsMailSkill(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{
			{
				Name:           "mail",
				Description:    "Read, search, summarize, reply to, and send email messages.",
				Prompt:         "Use mail.message.search and mail.message.read.",
				ToolReferences: []string{"mail.message.search", "mail.message.read"},
				Source:         InstructionSource{Path: "skills/mail/SKILL.md", SkillName: "mail"},
			},
			{
				Name:        "calendar",
				Description: "Create and list calendar events.",
				Prompt:      "Follow calendar workflow.",
			},
		},
	}
	retriever := NewEmbeddingSkillRetriever(nil, "")
	router := NewSkillSearchQueryRouter(staticStructuredLanguageModel{content: `{"queries":[{"description":"Search and read recent email messages from GitHub."}]}`})

	selectedBundle := selectInstructionBundleForRequestWithRetrieverAndRouter(context.Background(), instructionBundle, AgentRequest{
		Prompt:  "나 최근 github한테 온 메일 있어?",
		ToolSet: testToolSet([]string{"mail.message.search", "mail.message.read"}),
	}, retriever, router)

	if len(selectedBundle.SkillDecisions) != 1 || selectedBundle.SkillDecisions[0].Name != "mail" || selectedBundle.SkillDecisions[0].Status != "selected" {
		t.Fatalf("expected mail selected, got %+v", selectedBundle.SkillDecisions)
	}
	if len(selectedBundle.SkillQueries) != 2 || selectedBundle.SkillQueries[0] != "나 최근 github한테 온 메일 있어?" || !strings.Contains(selectedBundle.SkillQueries[1], "email messages") {
		t.Fatalf("expected raw request and supplemental query to be recorded, got %+v", selectedBundle.SkillQueries)
	}
	if !strings.Contains(selectedBundle.Prompt, "mail.message.search") {
		t.Fatalf("expected selected mail instructions, got %q", selectedBundle.Prompt)
	}
}

func TestContractSkillArbitrationSelectsUsefulCandidateFromTopK(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{
			{
				Name:           "public-web-builder",
				Description:    "Create, update, build, and publish website prototypes with public URLs.",
				WhenToUse:      "Use for website, homepage, web app, landing page, deploy, and publish requests.",
				Prompt:         "Follow website build and publish workflow.",
				ToolReferences: []string{"file.write", "terminal.run", "site.create", "site.build", "site.publish"},
				Source:         InstructionSource{Path: "skills/public-web-builder/SKILL.md", SkillName: "public-web-builder"},
			},
			{
				Name:           "enterprise-document-maker",
				Description:    "Create, verify, promote, and attach Word documents in .docx format.",
				WhenToUse:      "Use for Word documents, .docx files, memos, reports, and enterprise document deliverables.",
				Prompt:         "Create the document, validate it, promote it, then attach it.",
				ToolReferences: []string{"file.write", "terminal.run", "file.promote", "file.deliver"},
				Source:         InstructionSource{Path: "skills/enterprise-document-maker/SKILL.md", SkillName: "enterprise-document-maker"},
			},
		},
	}
	retriever := staticSkillRetriever{result: SkillRetrievalResult{
		SelectedCandidates: []SkillCandidate{
			{Name: "public-web-builder", Score: 30, Reason: "embedding_similarity"},
			{Name: "enterprise-document-maker", Score: 8, Reason: "embedding_similarity"},
		},
		RetrievalMode: "embedding",
		IndexStatus:   "ready",
	}}
	languageModel := &schemaStructuredLanguageModel{contentBySchema: map[string]string{
		"blueclaw_skill_search_queries":       `{"queries":[{"description":"Recover and attach the requested .docx enterprise guide file."}]}`,
		"blueclaw_contract_skill_arbitration": `{"selectedSkillNames":["enterprise-document-maker"],"rejectedSkillNames":["public-web-builder"],"requiredNextToolNames":["file.write","terminal.run","file.promote","file.deliver"],"expectedEvidence":["file.deliver"],"unmetPreconditions":[],"reason":"The outcome contract requires a .docx attachment, not a public website."}`,
	}}

	selectedBundle := selectInstructionBundleForRequestWithRetrieverAndRouter(context.Background(), instructionBundle, AgentRequest{
		Prompt: "링크로 전달된 적 없어. 첨부파일로 줘야지 그리고.",
		ToolSet: testToolSet([]string{
			"file.write",
			"terminal.run",
			"file.promote",
			"file.deliver",
			"site.create",
			"site.build",
			"site.publish",
		}),
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools:      []string{"file.deliver"},
			RequiredAttachmentSuffixes: []string{".docx"},
			ArtifactRequirement:        ArtifactRequirementRequired,
			ExpectedResults: []ExpectedResult{{
				ID:       "docx-guide",
				Type:     ExpectedResultTypeFile,
				Required: true,
			}},
		}},
	}, retriever, NewSkillSearchQueryRouter(languageModel))

	if !structuredRequestHasSchema(languageModel.requests, "blueclaw_contract_skill_arbitration") {
		t.Fatalf("expected contract arbitration request, got %+v", structuredRequestSchemaNames(languageModel.requests))
	}
	if !skillDecisionHasReason(selectedBundle.SkillDecisions, "enterprise-document-maker", "contract_arbitration") {
		t.Fatalf("expected enterprise document skill selected by arbitration, got %+v", selectedBundle.SkillDecisions)
	}
	if skillDecisionHasStatus(selectedBundle.SkillDecisions, "public-web-builder", "selected") {
		t.Fatalf("expected website skill not to be selected by artifact contract arbitration, got %+v", selectedBundle.SkillDecisions)
	}
	if !strings.Contains(selectedBundle.Prompt, "Create the document") || strings.Contains(selectedBundle.Prompt, "Use website build") {
		t.Fatalf("expected only arbitrated document instructions, got %q", selectedBundle.Prompt)
	}
	if !selectedBundle.HasContractSkillArbitration || !reflect.DeepEqual(selectedBundle.RequiredEvidenceTools, []string{"file.deliver"}) {
		t.Fatalf("expected exact arbitrated evidence, got %+v", selectedBundle.RequiredEvidenceTools)
	}
}

func TestContractSkillArbitrationDoesNotRunWithoutOutcomeContract(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{{
			Name:           "mail",
			Description:    "Read mail.",
			Prompt:         "Follow mail workflow.",
			ToolReferences: []string{"mail.message.search"},
		}},
	}
	languageModel := &schemaStructuredLanguageModel{contentBySchema: map[string]string{
		"blueclaw_skill_search_queries":       `{"queries":[{"description":"Read recent email."}]}`,
		"blueclaw_contract_skill_arbitration": `{"selectedSkillNames":[],"rejectedSkillNames":["mail"],"requiredNextToolNames":[],"expectedEvidence":[],"unmetPreconditions":[],"reason":"should not be called"}`,
	}}

	_ = selectInstructionBundleForRequestWithRetrieverAndRouter(context.Background(), instructionBundle, AgentRequest{
		Prompt:  "메일 확인해줘",
		ToolSet: testToolSet([]string{"mail.message.search"}),
	}, NewEmbeddingSkillRetriever(nil, ""), NewSkillSearchQueryRouter(languageModel))

	if structuredRequestHasSchema(languageModel.requests, "blueclaw_contract_skill_arbitration") {
		t.Fatalf("expected no contract arbitration without an outcome contract, got %+v", structuredRequestSchemaNames(languageModel.requests))
	}
}

func TestContractSkillArbitrationReportsExplicitStatuses(t *testing.T) {
	skillInstruction := SkillInstruction{
		Name:           "internkim-flow",
		Description:    "Manage tasks.",
		ToolReferences: []string{"task.add"},
	}
	candidates := []SkillInstruction{skillInstruction}
	candidateByName := map[string]SkillCandidate{
		skillInstruction.Name: {Name: skillInstruction.Name, Score: 1, Reason: "required_evidence_tool"},
	}
	request := AgentRequest{
		ToolSet: testToolSet([]string{"task.add"}),
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"task.add"},
		}},
	}

	testCases := []struct {
		name           string
		router         SkillSearchQueryRouter
		request        AgentRequest
		expectedStatus contractSkillArbitrationStatus
	}{
		{
			name:           "not applicable",
			router:         NewSkillSearchQueryRouter(staticStructuredLanguageModel{}),
			request:        AgentRequest{ToolSet: request.ToolSet},
			expectedStatus: contractSkillArbitrationNotApplicable,
		},
		{
			name:           "missing language model",
			router:         SkillSearchQueryRouter{},
			request:        request,
			expectedStatus: contractSkillArbitrationFailed,
		},
		{
			name: "invalid response",
			router: NewSkillSearchQueryRouter(staticStructuredLanguageModel{
				content: `{}`,
			}),
			request:        request,
			expectedStatus: contractSkillArbitrationFailed,
		},
		{
			name: "missing side effect evidence",
			router: NewSkillSearchQueryRouter(staticStructuredLanguageModel{
				content: `{"selectedSkillNames":["internkim-flow"],"rejectedSkillNames":[],"requiredNextToolNames":["task.add"],"expectedEvidence":[],"unmetPreconditions":[],"reason":"create task"}`,
			}),
			request:        request,
			expectedStatus: contractSkillArbitrationFailed,
		},
		{
			name: "succeeded",
			router: NewSkillSearchQueryRouter(staticStructuredLanguageModel{
				content: `{"selectedSkillNames":["internkim-flow"],"rejectedSkillNames":[],"requiredNextToolNames":["task.add"],"expectedEvidence":["task.add"],"unmetPreconditions":[],"reason":"The task contract requires task creation."}`,
			}),
			request:        request,
			expectedStatus: contractSkillArbitrationSucceeded,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result := testCase.router.ArbitrateContractSkills(context.Background(), testCase.request, candidates, candidateByName)
			if result.Status != testCase.expectedStatus {
				t.Fatalf("expected status %q, got %+v", testCase.expectedStatus, result)
			}
		})
	}
}

func TestContractSkillArbitrationCorrectsProseToExactCanonicalNames(t *testing.T) {
	candidates := []SkillInstruction{{
		Name:           "document",
		ToolReferences: []string{"document.read"},
	}}
	request := AgentRequest{
		ToolSet: testToolSet([]string{"document.read", TerminalRunToolName, FileWriteToolName, FileDeliverToolName}),
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{FileWriteToolName, FileDeliverToolName},
		}},
	}
	languageModel := &contractArbitrationSequenceLanguageModel{contents: []string{
		`{"selectedSkillNames":["document"],"rejectedSkillNames":[],"requiredNextToolNames":["file.write","terminal.run","file.deliver"],"expectedEvidence":["file.deliver attaches the DOCX"],"unmetPreconditions":[],"reason":"document workflow"}`,
		`{"selectedSkillNames":["document"],"rejectedSkillNames":[],"requiredNextToolNames":["file.write","terminal.run","file.deliver"],"expectedEvidence":["file.deliver"],"unmetPreconditions":[],"reason":"document workflow"}`,
	}}

	result := NewSkillSearchQueryRouter(languageModel).ArbitrateContractSkills(
		context.Background(),
		request,
		candidates,
		map[string]SkillCandidate{"document": {Name: "document"}},
	)

	if result.Status != contractSkillArbitrationSucceeded {
		t.Fatalf("expected corrected arbitration, got %+v", result)
	}
	if !reflect.DeepEqual(result.Arbitration.RequiredNextTools, []string{"file.write", "terminal.run", "file.deliver"}) {
		t.Fatalf("expected exact kernel workflow, got %v", result.Arbitration.RequiredNextTools)
	}
	if !reflect.DeepEqual(result.Arbitration.ExpectedEvidence, []string{"file.deliver"}) {
		t.Fatalf("expected exact delivery evidence, got %v", result.Arbitration.ExpectedEvidence)
	}
	if len(languageModel.requests) != 2 || !strings.Contains(joinedMessageContent(languageModel.requests[1].Messages), "previous candidate was invalid") {
		t.Fatalf("expected one correction request, got %+v", languageModel.requests)
	}
	assertContractArbitrationSchemaEnums(t, languageModel.requests[0].StructuredOutputSchema.Document, map[string][]string{
		"selectedSkillNames":    {"document"},
		"rejectedSkillNames":    {"document"},
		"requiredNextToolNames": {"document.read", "terminal.run", "file.deliver", "file.write"},
		"expectedEvidence":      {"file.write", "file.deliver", "document.read"},
	})
}

func TestContractSkillArbitrationFailureKeepsOnlyExplicitEvidenceAndKernelTools(t *testing.T) {
	instructionBundle := InstructionBundle{
		Prompt: "base",
		Skills: []SkillInstruction{{
			Name:           "internkim-flow",
			Description:    "Manage company tasks.",
			Prompt:         "Task workflow instructions must not load after arbitration failure.",
			ToolReferences: []string{"task.add", "task.list"},
		}},
	}
	languageModel := &schemaStructuredLanguageModel{contentBySchema: map[string]string{
		"blueclaw_skill_search_queries":       `{"queries":[{"description":"Create a company task."}]}`,
		"blueclaw_contract_skill_arbitration": `{}`,
	}}
	toolSet := testToolSet([]string{TerminalRunToolName, "task.add", "task.list"})
	outcomeContract := OutcomeContract{RequiredEvidenceTools: []string{"task.add"}}
	request := AgentRequest{
		Prompt:     "고객지원 분기 결산 누락 항목 확인 업무를 추가해줘",
		ToolSet:    toolSet,
		ActiveGoal: ActiveGoal{OutcomeContract: outcomeContract},
	}
	retriever := staticSkillRetriever{result: SkillRetrievalResult{
		SelectedCandidates: []SkillCandidate{{
			Name:   "internkim-flow",
			Score:  1,
			Reason: "required_evidence_tool",
		}},
		RetrievalMode: "embedding",
		IndexStatus:   "ready",
	}}

	selectedBundle := selectInstructionBundleForRequestWithRetrieverAndRouter(
		context.Background(),
		instructionBundle,
		request,
		retriever,
		NewSkillSearchQueryRouter(languageModel),
	)

	if !skillDecisionHasReason(selectedBundle.SkillDecisions, "internkim-flow", "contract_skill_arbitration_failed") {
		t.Fatalf("expected failed arbitration decision, got %+v", selectedBundle.SkillDecisions)
	}
	if !selectedBundle.ContractSkillArbitrationFailed {
		t.Fatal("expected failed arbitration to remain explicit")
	}
	if skillDecisionHasStatus(selectedBundle.SkillDecisions, "internkim-flow", "selected") {
		t.Fatalf("expected no selected skill after arbitration failure, got %+v", selectedBundle.SkillDecisions)
	}
	if strings.Contains(selectedBundle.Prompt, "Task workflow instructions") {
		t.Fatalf("expected failed skill body to stay unloaded, got %q", selectedBundle.Prompt)
	}

	exposedToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		selectedBundle,
		request,
		ExecutionPlan{},
		false,
		outcomeContract,
		ToolExposureEvent{},
	)
	if !exposedToolSet.IsAllowed(TerminalRunToolName) || !exposedToolSet.IsAllowed("task.add") {
		t.Fatalf("expected kernel and explicit evidence tools, got %+v", exposedToolSet.ListToolNames())
	}
	if exposedToolSet.IsAllowed("task.list") {
		t.Fatalf("expected unrelated skill tool to stay hidden, got %+v", exposedToolSet.ListToolNames())
	}
}

func assertContractArbitrationSchemaEnums(t *testing.T, schemaDocument string, expectedValues map[string][]string) {
	t.Helper()
	var schema struct {
		Properties map[string]struct {
			Items struct {
				Enum []string `json:"enum"`
			} `json:"items"`
		} `json:"properties"`
	}
	if errorValue := json.Unmarshal([]byte(schemaDocument), &schema); errorValue != nil {
		t.Fatalf("decode arbitration schema: %v", errorValue)
	}
	for propertyName, values := range expectedValues {
		if !reflect.DeepEqual(schema.Properties[propertyName].Items.Enum, values) {
			t.Fatalf("expected %s enum %v, got %v", propertyName, values, schema.Properties[propertyName].Items.Enum)
		}
	}
}

func TestRequiredEvidenceAddsOwningSkillCandidate(t *testing.T) {
	skillInstructions := []SkillInstruction{{
		Name:           "internkim-flow",
		ToolReferences: []string{"task.add", "task.list", "task.update", "task.delete"},
	}}
	request := AgentRequest{
		ToolSet: newTestToolSet([]string{"task.add", "task.list", "task.update", "task.delete"}),
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"task.add"},
		}},
	}

	result := retrieveSkillCandidates(context.Background(), request, skillInstructions, staticSkillRetriever{}, SkillSearchQuerySet{}, false)

	if len(result.SelectedCandidates) != 1 || result.SelectedCandidates[0].Name != "internkim-flow" || result.SelectedCandidates[0].Reason != "required_evidence_tool" {
		t.Fatalf("expected exact tool ownership candidate, got %+v", result.SelectedCandidates)
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
		ToolSet:       testToolSet([]string{"site.create", "site.publish", "terminal.run"}),
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
			Name:           "site-prototype",
			Description:    "Create and publish website prototypes.",
			Prompt:         "Use site.create and site.publish.",
			ToolReferences: []string{"site.create", "site.publish"},
			Source:         InstructionSource{Path: "skills/site-prototype/SKILL.md", SkillName: "site-prototype"},
		}},
	}
	retriever := NewEmbeddingSkillRetriever(nil, "")
	router := NewSkillSearchQueryRouter(staticStructuredLanguageModel{content: `{"queries":[{"description":"Create a website introducing InternKim's structure."}]}`})

	selectedBundle := selectInstructionBundleForRequestWithRetrieverAndRouter(context.Background(), instructionBundle, AgentRequest{
		Prompt: "김인턴의 구조에 대해 웹사이트 하나 소개 형식으로 만들어줘.",
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "user", Text: "https://example.com 내용으로 사업계획서 PPT 만들어줘."},
		}},
		ToolSet: testToolSet([]string{"site.create", "site.publish"}),
	}, retriever, router)

	if len(selectedBundle.SkillQueries) != 2 || selectedBundle.SkillQueries[0] != "김인턴의 구조에 대해 웹사이트 하나 소개 형식으로 만들어줘." || !strings.Contains(selectedBundle.SkillQueries[1], "InternKim") {
		t.Fatalf("expected raw request first and router query second, got %+v", selectedBundle.SkillQueries)
	}
	if strings.Contains(strings.Join(selectedBundle.SkillQueries, "\n"), "example.com") || strings.Contains(strings.ToLower(strings.Join(selectedBundle.SkillQueries, "\n")), "ppt") {
		t.Fatalf("expected stale visible context to stay out of structured query, got %+v", selectedBundle.SkillQueries)
	}
}

func TestSkillRetrievalUsesRawRequestBeforeSupplementalQueries(t *testing.T) {
	retriever := &recordingSkillRetriever{result: SkillRetrievalResult{RetrievalMode: "recording", IndexStatus: "ready"}}
	router := NewSkillSearchQueryRouter(staticStructuredLanguageModel{content: `{"queries":[{"description":"Supplemental task description."}]}`})

	_ = selectInstructionBundleForRequestWithRetrieverAndRouter(context.Background(), InstructionBundle{}, AgentRequest{
		Prompt: "  실제 사용자 요청  ",
	}, retriever, router)

	if len(retriever.querySets) != 1 {
		t.Fatalf("expected one retrieval, got %d", len(retriever.querySets))
	}
	descriptions := skillSearchQueryDescriptions(retriever.querySets[0])
	if len(descriptions) != 2 || descriptions[0] != "실제 사용자 요청" || descriptions[1] != "Supplemental task description." {
		t.Fatalf("expected raw request before supplemental query, got %+v", descriptions)
	}
}

func TestSkillRetrievalDoesNotSynthesizeArtifactContractQueries(t *testing.T) {
	retriever := &recordingSkillRetriever{result: SkillRetrievalResult{RetrievalMode: "recording", IndexStatus: "ready"}}
	router := NewSkillSearchQueryRouter(staticStructuredLanguageModel{content: `{"queries":[]}`})

	_ = selectInstructionBundleForRequestWithRetrieverAndRouter(context.Background(), InstructionBundle{}, AgentRequest{
		Prompt: "이어서 수정해줘",
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			ArtifactRequirement:        ArtifactRequirementRequired,
			RequiredEvidenceTools:      []string{"file.deliver"},
			RequiredAttachmentSuffixes: []string{".docx"},
		}},
	}, retriever, router)

	descriptions := skillSearchQueryDescriptions(retriever.querySets[0])
	if len(descriptions) != 1 || descriptions[0] != "이어서 수정해줘" {
		t.Fatalf("expected only the raw request, got %+v", descriptions)
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
	retriever := NewEmbeddingSkillRetriever(nil, "")

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
	candidates := []SkillCandidate{}
	for _, skillInstruction := range skills {
		candidates = append(candidates, SkillCandidate{Name: skillInstruction.Name, Score: 1, Reason: "test"})
	}
	retriever := staticSkillRetriever{result: SkillRetrievalResult{SelectedCandidates: candidates}}

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

func TestWebsiteSkillSurvivesWhenSkillIsFifthCandidate(t *testing.T) {
	skills := []SkillInstruction{
		{Name: "presentation", Description: "Create slides.", Prompt: "SLIDES BODY"},
		{Name: "handout", Description: "Create printable handouts.", Prompt: "HANDOUT BODY"},
		{Name: "direct-message", Description: "Send direct messages.", Prompt: "DM BODY"},
		{Name: "report", Description: "Write reports.", Prompt: "REPORT BODY"},
		{
			Name:           "site-prototype",
			Description:    "Create and publish website prototypes.",
			Prompt:         "SITE BODY",
			ToolReferences: []string{"terminal.run", "site.create", "site.publish"},
			Source:         InstructionSource{Path: "skills/site-prototype/SKILL.md", SkillName: "site-prototype"},
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
			"site.create",
			"site.publish",
		}),
	}, retriever)

	if !strings.Contains(selectedBundle.Prompt, "SITE BODY") {
		t.Fatalf("expected fifth candidate site skill body to be selected, got %q", selectedBundle.Prompt)
	}
	if strings.Contains(selectedBundle.Prompt, "EXTRA BODY") {
		t.Fatalf("expected sixth candidate body to stay out, got %q", selectedBundle.Prompt)
	}
}

func TestSkillIndexPromptStaysBoundedForManySkills(t *testing.T) {
	skills := []SkillInstruction{{
		Name:        "presentation",
		Description: "Create presentation slides and 피피티.",
		WhenToUse:   "Use for 피피티.",
		Prompt:      "Generate slides.",
		Source:      InstructionSource{Path: "skills/presentation/SKILL.md", SHA256: "match", SkillName: "presentation"},
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
	retriever := NewEmbeddingSkillRetriever(nil, "")

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
			Name:        "presentation",
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
			Name:        "presentation",
			Description: "Create presentation slides and 피피티.",
			WhenToUse:   "Use for 피피티.",
			Prompt:      "Generate slides.",
			Source:      InstructionSource{Path: "skills/presentation/SKILL.md", SHA256: "one", SkillName: "presentation"},
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
	retriever := NewEmbeddingSkillRetriever(constantEmbeddingProvider{}, indexPath)
	firstBundle := []SkillInstruction{{
		Name:        "presentation",
		Description: "Create presentation slides and 피피티.",
		Source:      InstructionSource{Path: "skills/presentation/SKILL.md", SHA256: "one", SkillName: "presentation"},
	}}
	secondBundle := []SkillInstruction{{
		Name:        "presentation",
		Description: "Create presentation slides.",
		Source:      InstructionSource{Path: "skills/presentation/SKILL.md", SHA256: "two", SkillName: "presentation"},
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
	legacyDocument := `[{"skillName":"presentation","sourcePath":"skills/presentation/SKILL.md","sourceSHA256":"one","searchText":"Create presentation slides.","embeddingModel":"embedding.create","embedding":[1],"indexedAt":"2026-01-01T00:00:00Z"}]`
	if errorValue := os.WriteFile(indexPath, []byte(legacyDocument), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	retriever := NewEmbeddingSkillRetriever(constantEmbeddingProvider{}, indexPath)

	retriever.Refresh(context.Background(), []SkillInstruction{{
		Name:        "presentation",
		Description: "Create presentation slides.",
		Source:      InstructionSource{Path: "skills/presentation/SKILL.md", SHA256: "one", SkillName: "presentation"},
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

func TestSkillIndexIncludesConfiguredEmbeddingModel(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "skill-index.json")
	retriever := NewEmbeddingSkillRetriever(constantEmbeddingProvider{}, indexPath)
	retriever.EmbeddingModel = "baai/bge-m3"

	retriever.Refresh(context.Background(), []SkillInstruction{{
		Name:        "presentation",
		Description: "Create presentation slides.",
		Source:      InstructionSource{Path: "skills/presentation/SKILL.md", SHA256: "one", SkillName: "presentation"},
	}})

	document, errorValue := os.ReadFile(indexPath)
	if errorValue != nil {
		t.Fatalf("expected materialized skill index: %v", errorValue)
	}
	if !strings.Contains(string(document), `"embeddingModel": "baai/bge-m3:`+skillSearchDocumentVersion+`"`) {
		t.Fatalf("expected configured embedding model in index, got %s", string(document))
	}
}

type constantEmbeddingProvider struct{}

func (provider constantEmbeddingProvider) GenerateEmbedding(context.Context, string) ([]float32, error) {
	return []float32{1, 0}, nil
}

type dimensionChangingEmbeddingProvider struct {
	callCount int
}

func (provider *dimensionChangingEmbeddingProvider) GenerateEmbedding(context.Context, string) ([]float32, error) {
	provider.callCount++
	if provider.callCount == 1 {
		return []float32{1}, nil
	}
	return []float32{1, 0}, nil
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

type schemaStructuredLanguageModel struct {
	contentBySchema map[string]string
	requests        []llm.StructuredResponseRequest
}

type contractArbitrationSequenceLanguageModel struct {
	contents []string
	requests []llm.StructuredResponseRequest
}

func (languageModel *contractArbitrationSequenceLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *contractArbitrationSequenceLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	index := len(languageModel.requests) - 1
	if index >= len(languageModel.contents) {
		index = len(languageModel.contents) - 1
	}
	return llm.StructuredResponse{Content: languageModel.contents[index]}, nil
}

func (languageModel *schemaStructuredLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *schemaStructuredLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	content := languageModel.contentBySchema[request.StructuredOutputSchema.Name]
	if strings.TrimSpace(content) == "" {
		content = `{}`
	}
	return llm.StructuredResponse{Content: content}, nil
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

type recordingSkillRetriever struct {
	result    SkillRetrievalResult
	querySets []SkillSearchQuerySet
}

func (retriever *recordingSkillRetriever) Retrieve(context.Context, AgentRequest, []SkillInstruction, int) SkillRetrievalResult {
	return retriever.result
}

func (retriever *recordingSkillRetriever) Search(_ context.Context, _ AgentRequest, _ []SkillInstruction, querySet SkillSearchQuerySet, _ int) SkillRetrievalResult {
	retriever.querySets = append(retriever.querySets, querySet)
	return retriever.result
}

func (retriever *recordingSkillRetriever) Refresh(context.Context, []SkillInstruction) {}

func skillDecisionHasStatus(skillDecisions []SkillSelectionDecision, skillName string, status string) bool {
	for _, skillDecision := range skillDecisions {
		if skillDecision.Name == skillName && skillDecision.Status == status {
			return true
		}
	}
	return false
}

func skillDecisionHasReason(skillDecisions []SkillSelectionDecision, skillName string, reason string) bool {
	for _, skillDecision := range skillDecisions {
		if skillDecision.Name == skillName && skillDecision.Reason == reason {
			return true
		}
	}
	return false
}

func structuredRequestHasSchema(requests []llm.StructuredResponseRequest, schemaName string) bool {
	for _, request := range requests {
		if request.StructuredOutputSchema.Name == schemaName {
			return true
		}
	}
	return false
}

func structuredRequestSchemaNames(requests []llm.StructuredResponseRequest) []string {
	names := []string{}
	for _, request := range requests {
		names = append(names, request.StructuredOutputSchema.Name)
	}
	return names
}

func testToolSet(toolNames []string) *ToolSet {
	toolRegistry := newTestToolSet(toolNames)
	for _, toolName := range toolNames {
		registerTestTool(toolRegistry, ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolResult{}, nil
		})
	}
	return toolRegistry
}
