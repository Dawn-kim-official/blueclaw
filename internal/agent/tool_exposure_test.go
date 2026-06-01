package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestToolExposureKeepsSelectedToolsBeforeCore(t *testing.T) {
	toolSet := testToolSet([]string{"skill.search", "tool.describe", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember", "site.app.status", "site.app.publish"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: []string{"site.app.status", "site.app.publish"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "사이트 고쳐줘"}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{SelectedToolIDs: []string{"site.app.status"}}, ToolExposureEvent{})

	if !filteredToolSet.IsAllowed("site.app.status") {
		t.Fatalf("expected selected tool to be exposed, got %+v", filteredToolSet.ListToolNames())
	}
	if filteredToolSet.IsAllowed("site.app.publish") {
		t.Fatalf("expected unselected fallback skill tool to be hidden when selection is valid, got %+v", filteredToolSet.ListToolNames())
	}
	for _, toolID := range []string{"skill.search", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected core tool %s to remain exposed, got %+v", toolID, filteredToolSet.ListToolNames())
		}
	}
	if filteredToolSet.IsAllowed("tool.describe") {
		t.Fatalf("expected low-priority tool.describe to be dropped under cap pressure, got %+v", filteredToolSet.ListToolNames())
	}
	if event.UsedFallbackGroups {
		t.Fatalf("expected valid model selection to avoid fallback groups: %+v", event)
	}
}

func TestToolExposureFallsBackWhenSelectionIsEmptyOrInvalid(t *testing.T) {
	toolSet := testToolSet([]string{"skill.search", "tool.describe", "ask.confirm", "terminal.run", "file.write", "mail.message.search"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "slides", AllowedTools: []string{"terminal.run", "file.write"}},
			{Name: "mail", AllowedTools: []string{"mail.message.search"}},
		},
		SkillDecisions: []SkillSelectionDecision{{Name: "slides", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "발표자료 만들어줘"}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{SelectedToolIDs: []string{"unknown.tool"}}, ToolExposureEvent{})

	for _, toolID := range []string{"skill.search", "tool.describe", "ask.confirm", "terminal.run", "file.write"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected fallback to expose %s, got %+v", toolID, filteredToolSet.ListToolNames())
		}
	}
	if filteredToolSet.IsAllowed("mail.message.search") {
		t.Fatalf("expected unselected skill tool to stay hidden, got %+v", filteredToolSet.ListToolNames())
	}
	if !event.UsedFallbackGroups {
		t.Fatalf("expected invalid selection to use fallback groups: %+v", event)
	}
}

func TestToolExposureCapTruncatesByGroupOrder(t *testing.T) {
	toolIDs := []string{}
	for index := 0; index < 25; index++ {
		toolIDs = append(toolIDs, "custom.tool"+string(rune('a'+index)))
	}
	toolSet := testToolSet(append([]string{"skill.search", "tool.describe", "ask.confirm"}, toolIDs...))
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "custom",
			AllowedTools: toolIDs,
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "custom", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "run custom workflow"}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{SelectedToolIDs: toolIDs}, ToolExposureEvent{})

	exposedToolIDs := filteredToolSet.ListToolNames()
	if len(exposedToolIDs) != maxSchemaCallableToolCount {
		t.Fatalf("expected exactly %d exposed tools, got %d: %+v", maxSchemaCallableToolCount, len(exposedToolIDs), exposedToolIDs)
	}
	for _, toolID := range toolIDs[:maxSchemaCallableToolCount] {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected selected tool %s to survive cap, got %+v", toolID, exposedToolIDs)
		}
	}
	if filteredToolSet.IsAllowed("skill.search") {
		t.Fatalf("expected core tools to be dropped after selected group consumes cap, got %+v", exposedToolIDs)
	}
	if len(event.DroppedGroups) == 0 || !event.DroppedGroups[0].IsPartial {
		t.Fatalf("expected partial selected group drop event, got %+v", event.DroppedGroups)
	}
}

func TestToolSelectionModelChoosesSiteWorkingSetFromCards(t *testing.T) {
	siteToolIDs := []string{
		"site.app.status",
		"site.app.create",
		"file.read",
		"file.write",
		"terminal.run",
		"site.app.build",
		"browser.open",
		"browser.snapshot",
		"browser.screenshot",
		"artifact.review",
		"site.app.publish",
		"site.app.repair",
	}
	for index := 0; index < maxSchemaCallableToolCount; index++ {
		siteToolIDs = append(siteToolIDs, fmt.Sprintf("site.extra.%02d", index))
	}
	toolSet := testToolSet(siteToolIDs)
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: siteToolIDs,
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	selectionRequest := buildToolSelectionRequest(toolSet, instructionBundle, AgentRequest{Prompt: "개인 홈페이지 만들고 배포해줘"}, ExecutionPlan{}, false, OutcomeContract{})
	if toolSelectionFallbackFitsCap(selectionRequest) {
		t.Fatal("expected oversized site candidates to require model selection")
	}
	languageModel := &sequenceLanguageModel{toolSelections: []string{
		`{"selectedToolIDs":["site.app.status","site.app.create","file.write","site.app.build","artifact.review","site.app.publish"],"reason":"create, build, review, and publish satisfy the link result"}`,
	}}
	selection, event := NewToolSelectionRouter(languageModel).Select(context.Background(), selectionRequest)
	if len(languageModel.selectionRequests) != 1 {
		t.Fatalf("expected one tool selection request, got %d", len(languageModel.selectionRequests))
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "개인 홈페이지 만들고 배포해줘"}, ExecutionPlan{}, false, OutcomeContract{}, selection, event)

	if len(event.ExposedToolIDs) > maxSchemaCallableToolCount {
		t.Fatalf("expected exposed tools to stay within cap, got %+v", event.ExposedToolIDs)
	}
	for _, toolID := range []string{"site.app.status", "site.app.create", "file.write", "site.app.build", "artifact.review", "site.app.publish"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected model-selected site tool %s to be exposed, got %+v", toolID, event.ExposedToolIDs)
		}
	}
	for _, toolID := range []string{"browser.open", "browser.snapshot", "browser.screenshot", "site.app.repair"} {
		if filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected unselected site tool %s to stay out of the working set, got %+v", toolID, event.ExposedToolIDs)
		}
	}
}

func TestToolSelectionModelChoosesFileWorkingSetFromCards(t *testing.T) {
	toolIDs := []string{
		"terminal.run",
		"file.write",
		"file.promote",
		"file.attach",
		"artifact.review",
		"site.app.status",
		"site.app.create",
		"site.app.build",
		"site.app.publish",
	}
	toolSet := testToolSet(toolIDs)
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "simple-slides", AllowedTools: []string{"terminal.run", "file.write", "file.promote", "file.attach", "artifact.review"}},
			{Name: "site-prototype", AllowedTools: []string{"site.app.status", "site.app.create", "site.app.build", "site.app.publish", "terminal.run", "file.write", "artifact.review"}},
		},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "simple-slides", Status: "selected"},
			{Name: "site-prototype", Status: "selected"},
		},
	}
	contract := OutcomeContract{
		ArtifactRequirement:        ArtifactRequirementRequired,
		RequiredAttachmentSuffixes: []string{".pptx"},
		RequiredEvidenceTools:      []string{"file.attach"},
	}
	selectionRequest := buildToolSelectionRequest(toolSet, instructionBundle, AgentRequest{Prompt: "PPTX 발표자료 만들어 첨부해줘"}, ExecutionPlan{}, false, contract)
	languageModel := &sequenceLanguageModel{toolSelections: []string{
		`{"selectedToolIDs":["terminal.run","file.write","file.promote","file.attach","artifact.review"],"reason":"create, inspect, promote, and attach the requested PPTX file"}`,
	}}
	selection, event := NewToolSelectionRouter(languageModel).Select(context.Background(), selectionRequest)

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "PPTX 발표자료 만들어 첨부해줘"}, ExecutionPlan{}, false, contract, selection, event)

	for _, toolID := range []string{"terminal.run", "file.write", "file.promote", "file.attach", "artifact.review"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected model-selected file tool %s to be exposed, got %+v", toolID, event.ExposedToolIDs)
		}
	}
	if filteredToolSet.IsAllowed("site.app.create") || filteredToolSet.IsAllowed("site.app.publish") {
		t.Fatalf("expected unselected site tools to stay out of PPTX working set, got %+v", event.ExposedToolIDs)
	}
}

func TestRecoveryWorkingSetKeepsPendingFileDeliveryTools(t *testing.T) {
	toolSet := testToolSet([]string{
		"terminal.run",
		"site.app.status",
		"site.app.repair",
		"artifact.review",
		"file.attach",
		"file.promote",
		"file.read",
		"skill.search",
	})
	instructionBundle := InstructionBundle{}
	contract := OutcomeContract{
		ArtifactRequirement:        ArtifactRequirementRequired,
		RequiredAttachmentSuffixes: []string{".pptx"},
		ExpectedResults: []ExpectedResult{{
			ID:          "attached-file",
			Type:        ExpectedResultTypeFile,
			Description: "수정 가능한 PPTX 파일 한 개",
			Required:    true,
		}},
	}
	observation := turnObservation{
		ObservationID: "obs-001",
		Action:        "recovery_guidance",
		Tool:          "artifact.review",
		RecoveryPacket: &RecoveryPacket{
			AllowedTools: []string{"site.app.status", "site.app.repair", "terminal.run", "artifact.review"},
		},
	}
	selectionRequest := buildToolSelectionRequest(toolSet, instructionBundle, AgentRequest{Prompt: "PPTX 파일 첨부해줘"}, ExecutionPlan{}, false, contract, []turnObservation{observation})
	languageModel := &sequenceLanguageModel{toolSelections: []string{
		`{"selectedToolIDs":["terminal.run","artifact.review","file.promote","file.attach"],"reason":"recover by creating, reviewing, promoting, and attaching the requested file"}`,
	}}
	selection, event := NewToolSelectionRouter(languageModel).Select(context.Background(), selectionRequest)

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "PPTX 파일 첨부해줘"}, ExecutionPlan{}, false, contract, selection, event)

	for _, toolID := range []string{"terminal.run", "artifact.review", "file.attach", "file.promote"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected recovery working set to expose %s, got %+v", toolID, event.ExposedToolIDs)
		}
	}
}

func TestRecoveryWorkingSetUsesActiveFailureHints(t *testing.T) {
	toolSet := testToolSet([]string{"file.read", "file.write", "site.app.build", "site.app.publish", "skill.search", "tool.describe", "ask.confirm"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: []string{"file.read", "file.write", "site.app.build", "site.app.publish"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	observation := turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "site.app.build",
		Output:        ToolOutput{Content: "starter scaffold remains"},
		Failure: &ToolFailure{
			Kind:                  FailureInvalidInput,
			Code:                  FailureCodes.InvalidInput.String(),
			Stage:                 "site_build_delivery",
			UserSafeSummary:       "starter scaffold remains",
			RequiredPreconditions: []string{"source_changed"},
			RecoveryHints: []RecoveryHint{{
				Action:    "edit_resource",
				ToolNames: []string{"file.read", "file.write"},
			}},
		},
		ToolInputKey:       "site.app.build\x00{\"siteID\":\"site-1\"}",
		AttemptFingerprint: "site.app.build\x00{\"siteID\":\"site-1\"}\x00invalid_input",
	}
	selectionRequest := buildToolSelectionRequest(toolSet, instructionBundle, AgentRequest{Prompt: "개인 홈페이지 배포해줘"}, ExecutionPlan{}, false, OutcomeContract{}, []turnObservation{observation})
	selection, event, isDeterministic := deterministicToolSelectionDecision(selectionRequest)
	if !isDeterministic {
		t.Fatal("expected active recovery hints to select deterministically")
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "개인 홈페이지 배포해줘"}, ExecutionPlan{}, false, OutcomeContract{}, selection, event, []turnObservation{observation})

	for _, toolID := range []string{"file.read", "file.write"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected recovery hint tool %s to be exposed, got %+v", toolID, event.ExposedToolIDs)
		}
	}
	if filteredToolSet.IsAllowed("site.app.publish") {
		t.Fatalf("expected publish to stay hidden until source changes and rebuild succeeds, got %+v", event.ExposedToolIDs)
	}
}

func TestRecoveryWorkingSetDropsExhaustedTool(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "file.edit",
		Failure: &ToolFailure{
			Kind:            FailureInvalidInput,
			Code:            FailureCodes.InvalidInput.String(),
			Stage:           "file_edit",
			UserSafeSummary: "oldText must match exactly once",
			RecoveryHints: []RecoveryHint{{
				Action:    "inspect_or_edit_text",
				ToolNames: []string{"file.read", "file.edit", "file.patch", "file.write"},
			}},
		},
		ToolInputKey: "file.edit\x00{\"path\":\"App.tsx\"}",
	}, {
		ObservationID: "obs-002",
		Action:        "policy",
		Tool:          "file.edit",
		Output:        ToolOutput{Content: "The recovery budget for corrected_retry is exhausted."},
		RecoveryStep:  recoveryStepCorrectedRetry,
		Summary:       "The recovery budget for corrected_retry is exhausted.",
	}}

	toolNames := recoveryPinnedToolNames(InstructionBundle{}, AgentRequest{PinnedToolNames: []string{"file.edit", "file.write"}}, observations)

	if stringSliceContains(toolNames, "file.edit") {
		t.Fatalf("expected exhausted file.edit to be removed, got %+v", toolNames)
	}
	if !stringSliceContains(toolNames, "file.write") || !stringSliceContains(toolNames, "file.patch") {
		t.Fatalf("expected alternate edit tools to remain, got %+v", toolNames)
	}
}

func TestPlannedToolsDropRepeatedFileRead(t *testing.T) {
	observations := []turnObservation{
		newFailureObservation("obs-001", "policy", "file.read", "Already read tmp/deck/presentation.md lines 1-400.", FailurePolicyBlocked, FailureCodes.PolicyBlocked, "file_read_repeat"),
	}

	toolNames := filterExhaustedRecoveryToolNames([]string{"file.read", "terminal.run", "file.attach"}, observations)

	if stringSliceContains(toolNames, "file.read") {
		t.Fatalf("expected repeated file.read to be removed, got %+v", toolNames)
	}
	for _, toolName := range []string{"terminal.run", "file.attach"} {
		if !stringSliceContains(toolNames, toolName) {
			t.Fatalf("expected %s to remain available, got %+v", toolName, toolNames)
		}
	}
}

func TestGenericFallbackKeepsExplicitCapabilityToolsBeforeBuiltIns(t *testing.T) {
	toolSet := testToolSet([]string{"conversation.history", "memory.search", "browser.snapshot", "file.write", "terminal.run"})
	instructionBundle := InstructionBundle{}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "open browser and observe"}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{}, ToolExposureEvent{})

	if !filteredToolSet.IsAllowed("browser.snapshot") {
		t.Fatalf("expected explicit capability tool to survive generic fallback, got %+v", event.ExposedToolIDs)
	}
}

func TestToolSelectionContextUsesCompactCards(t *testing.T) {
	toolSet := testToolSet([]string{"site.app.status"})
	cards := renderCompactToolCards(toolSet, []toolExposureGroup{{Name: "G5 selected-skill candidates", ToolIDs: []string{"site.app.status"}}})
	summary := renderCoreGroupSummary(collectCoreGroups(testToolSet([]string{"skill.search", "tool.describe", "ask.confirm"})))

	if !strings.Contains(cards, "- site.app.status:") {
		t.Fatalf("expected compact card to include tool id, got %s", cards)
	}
	if strings.Contains(cards, "inputSchema") || strings.Contains(cards, "properties") {
		t.Fatalf("expected compact card to omit full schema, got %s", cards)
	}
	if !strings.Contains(summary, "G1 control-core: skill.search, ask.confirm") || !strings.Contains(summary, "G3 memory-context-core: tool.describe") {
		t.Fatalf("expected compact core summary, got %s", summary)
	}
}

func TestFileToolCardsSeparateWriteEditAndPatchRoles(t *testing.T) {
	toolSet := NewToolSet([]string{"file.write", "file.edit", "file.patch"})
	handler := func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("ok"), nil
	}
	toolSet.RegisterTool(ToolDefinition{
		Name: "file.write",
		RecoveryCard: ToolRecoveryCard{
			Does:      "Overwrites one workspace text file with the exact content string.",
			UseWhen:   "A new file or full rewrite is needed.",
			AvoidWhen: "A small targeted source change is needed.",
		},
	}, handler)
	toolSet.RegisterTool(ToolDefinition{
		Name: "file.edit",
		RecoveryCard: ToolRecoveryCard{
			Does:      "Replaces one exact oldText occurrence with newText.",
			UseWhen:   "A small targeted source change is needed.",
			AvoidWhen: "The oldText is missing or ambiguous.",
		},
	}, handler)
	toolSet.RegisterTool(ToolDefinition{
		Name: "file.patch",
		RecoveryCard: ToolRecoveryCard{
			Does:      "Applies structured exact replacements across files.",
			UseWhen:   "Several targeted edits should be applied together.",
			AvoidWhen: "A broad file rewrite is needed.",
		},
	}, handler)

	cards := renderCompactToolCards(toolSet, []toolExposureGroup{{Name: "file tools", ToolIDs: []string{"file.write", "file.edit", "file.patch"}}})

	for _, expectedText := range []string{"file.write", "full rewrite", "file.edit", "oldText", "file.patch", "Several targeted edits"} {
		if !strings.Contains(cards, expectedText) {
			t.Fatalf("expected file tool card text %q in %s", expectedText, cards)
		}
	}
}

func TestToolSelectionRouterReturnsEmptyOnModelFailure(t *testing.T) {
	router := NewToolSelectionRouter(failingRecoveryLanguageModel{})
	decision, event := router.Select(context.Background(), toolSelectionRequest{
		Prompt:          "사이트 고쳐줘",
		ToolSet:         testToolSet([]string{"site.app.status"}),
		CandidateGroups: []toolExposureGroup{{Name: "G5 selected-skill candidates", ToolIDs: []string{"site.app.status"}}},
	})

	if len(decision.SelectedToolIDs) != 0 {
		t.Fatalf("expected failed selection to return empty decision, got %+v", decision)
	}
	if !event.SelectionFailed {
		t.Fatalf("expected failed selection event, got %+v", event)
	}
}
