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

func TestToolExposureSelectsAttachmentReadToolForVisibleMaterial(t *testing.T) {
	toolSet := testToolSet([]string{"skill.search", "tool.describe", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember", "terminal.run", "image.read", "file.preview", "document.read", "mail.message.search", "mattermost.channel.post"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "mail", AllowedTools: []string{"mail.message.search"}},
			{Name: "mattermost", AllowedTools: []string{"mattermost.channel.post"}},
		},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "mail", Status: "selected"},
			{Name: "mattermost", Status: "selected"},
		},
	}
	request := AgentRequest{
		Prompt: "다시 이미지 봐봐",
		VisibleContext: VisibleContext{
			Materials: []VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Filename:    "mascot.png",
				ContentType: "image/png",
			}},
		},
	}
	selectionRequest := buildToolSelectionRequest(toolSet, instructionBundle, request, ExecutionPlan{}, false, OutcomeContract{})
	selection, event, isDeterministic := deterministicToolSelectionDecision(selectionRequest)
	if !isDeterministic || !containsString(selection.SelectedToolIDs, "image.read") {
		t.Fatalf("expected deterministic image.read selection, selection=%+v event=%+v", selection, event)
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, request, ExecutionPlan{}, false, OutcomeContract{}, selection, event)

	if !filteredToolSet.IsAllowed("image.read") {
		t.Fatalf("expected image.read to be exposed, got %+v", filteredToolSet.ListToolNames())
	}
	if filteredToolSet.IsAllowed("terminal.run") {
		t.Fatalf("expected terminal.run to stay hidden for attachment read selection, got %+v", filteredToolSet.ListToolNames())
	}
	for _, toolName := range []string{"mail.message.search", "mattermost.channel.post"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill tool %s to stay hidden for attachment read selection, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestToolExposureSelectsFilePreviewForVisibleDocumentMaterial(t *testing.T) {
	toolSet := testToolSet([]string{"skill.search", "tool.describe", "ask.confirm", "file.preview", "document.read", "terminal.run"})
	request := AgentRequest{
		Prompt: "첨부 파일 다시 봐봐",
		VisibleContext: VisibleContext{
			CurrentMaterials: []VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Filename:    "deck.html",
				ContentType: "text/html",
			}},
		},
	}

	selectionRequest := buildToolSelectionRequest(toolSet, InstructionBundle{}, request, ExecutionPlan{}, false, OutcomeContract{})
	selection, event, isDeterministic := deterministicToolSelectionDecision(selectionRequest)
	if !isDeterministic || !containsString(selection.SelectedToolIDs, "file.preview") {
		t.Fatalf("expected deterministic file.preview selection, selection=%+v event=%+v", selection, event)
	}
	if containsString(selection.SelectedToolIDs, "document.read") {
		t.Fatalf("expected document.read not to be selected for attachment catalog, selection=%+v", selection)
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

func TestDeterministicPaletteTruncatesSelectedSkillToolsByOrder(t *testing.T) {
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
	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "개인 홈페이지 만들고 배포해줘"}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{}, ToolExposureEvent{})

	if len(event.ExposedToolIDs) > maxSchemaCallableToolCount {
		t.Fatalf("expected exposed tools to stay within cap, got %+v", event.ExposedToolIDs)
	}
	for _, toolID := range []string{"site.app.status", "site.app.create", "file.read", "file.write", "terminal.run", "site.app.build", "browser.open", "browser.snapshot"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected deterministic skill tool %s to be exposed, got %+v", toolID, event.ExposedToolIDs)
		}
	}
	for _, toolID := range []string{"browser.screenshot", "artifact.review", "site.app.publish", "site.app.repair"} {
		if filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected lower-priority site tool %s to stay out of the capped palette, got %+v", toolID, event.ExposedToolIDs)
		}
	}
}

func TestDeterministicPaletteKeepsSelectedFileWorkflowAheadOfOtherSkills(t *testing.T) {
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
	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "PPTX 발표자료 만들어 첨부해줘"}, ExecutionPlan{}, false, contract, ToolSelectionDecision{}, ToolExposureEvent{})

	for _, toolID := range []string{"terminal.run", "file.write", "file.promote", "file.attach", "artifact.review"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected selected file workflow tool %s to be exposed, got %+v", toolID, event.ExposedToolIDs)
		}
	}
	if filteredToolSet.IsAllowed("site.app.publish") {
		t.Fatalf("expected lower-priority site publish tool to stay out of PPTX palette, got %+v", event.ExposedToolIDs)
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
		SelectedEvidenceHints:      []string{"file.promote", "file.attach"},
		ExpectedResults: []ExpectedResult{{
			ID:          "attached-file",
			Type:        ExpectedResultTypeFile,
			Description: "수정 가능한 PPTX 파일 한 개",
			Required:    true,
		}},
	}
	observation := turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "artifact.review",
		Failure: &ToolFailure{
			Kind:            FailureInvalidInput,
			Code:            FailureCodes.InvalidInput.String(),
			Stage:           "artifact_review",
			UserSafeSummary: "artifact still needs delivery",
		},
		ToolInputKey: "artifact.review\x00{\"path\":\"tmp/deck\"}",
		RecoveryPacket: &RecoveryPacket{
			AllowedTools: []string{"site.app.status", "site.app.repair", "terminal.run", "artifact.review"},
		},
	}
	selectionRequest := buildToolSelectionRequest(toolSet, instructionBundle, AgentRequest{Prompt: "PPTX 파일 첨부해줘"}, ExecutionPlan{}, false, contract, []turnObservation{observation})
	selection, event, isDeterministic := deterministicToolSelectionDecision(selectionRequest)
	if !isDeterministic {
		t.Fatal("expected recovery and outcome tools to select deterministically")
	}

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

func TestRecoveryWorkingSetDoesNotLetPinnedSkillToolsCrowdOutRecoveryHints(t *testing.T) {
	siteToolNames := []string{
		"site.app.build",
		"site.app.publish",
		"site.app.status",
		"terminal.run",
		"terminal.session",
		"browser.open",
		"browser.snapshot",
		"browser.screenshot",
		"file.read",
		"file.edit",
		"file.patch",
		"file.write",
		"site.app.create",
		"site.app.repair",
		"site.app.preview",
		"site.app.history",
		"site.app.diff",
		"site.app.logs",
		"site.app.rollback",
		"site.app.unpublish",
		"site.app.restore",
		"site.app.delete",
		"artifact.review",
		"user.confirm",
	}
	toolSet := testToolSet(append(siteToolNames, "skill.search", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember", "tool.describe"))
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: siteToolNames,
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	observation := turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "site.app.build",
		Failure: &ToolFailure{
			Kind:                  FailureInvalidInput,
			Code:                  FailureCodes.InvalidInput.String(),
			Stage:                 "site_build_source",
			UserSafeSummary:       "source failed to compile",
			RequiredPreconditions: []string{"source_changed"},
			RecoveryHints: []RecoveryHint{{
				Action:    "edit_resource",
				ToolNames: []string{"file.read", "file.edit", "file.patch", "file.write"},
			}},
		},
		ToolInputKey:       "site.app.build\x00{\"siteID\":\"site-1\"}",
		AttemptFingerprint: "site.app.build\x00{\"siteID\":\"site-1\"}\x00invalid_input",
	}
	request := AgentRequest{
		Prompt:           "개인 홈페이지 배포해줘",
		PinnedSkillNames: []string{"site-prototype"},
	}
	selectionRequest := buildToolSelectionRequest(toolSet, instructionBundle, request, ExecutionPlan{}, false, OutcomeContract{}, []turnObservation{observation})
	selection, event, isDeterministic := deterministicToolSelectionDecision(selectionRequest)
	if !isDeterministic {
		t.Fatal("expected active recovery hints to select deterministically")
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, request, ExecutionPlan{}, false, OutcomeContract{}, selection, event, []turnObservation{observation})

	for _, toolID := range []string{"file.read", "file.edit", "file.patch", "file.write"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected recovery hint tool %s to survive selected skill pressure, got %+v", toolID, event.ExposedToolIDs)
		}
	}
	if filteredToolSet.IsAllowed("browser.screenshot") || filteredToolSet.IsAllowed("site.app.rollback") {
		t.Fatalf("expected broad pinned skill tools to stay out of G4 recovery set, got %+v", event.ExposedToolIDs)
	}
}

func TestDeterministicPinnedStepExposesOnlyPinnedTools(t *testing.T) {
	toolSet := testToolSet([]string{"site.app.build", "skill.search", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember"})
	request := AgentRequest{Prompt: "빌드해줘", PinnedToolNames: []string{"site.app.build"}}
	selection, event := deterministicToolSelection([]string{"site.app.build"}, "pinned tools are required for the next step")

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, InstructionBundle{}, request, ExecutionPlan{}, false, OutcomeContract{}, selection, event)

	if !filteredToolSet.IsAllowed("site.app.build") {
		t.Fatalf("expected pinned build tool to be exposed, got %+v", event.ExposedToolIDs)
	}
	for _, toolID := range []string{"skill.search", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember"} {
		if filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected deterministic pinned step to hide %s, got %+v", toolID, event.ExposedToolIDs)
		}
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

func TestFallbackKeepsSelectedSkillToolsBeforeCoreTools(t *testing.T) {
	slideToolNames := []string{"terminal.run", "file.read", "file.write", "file.edit", "file.patch", "file.promote", "file.attach", "artifact.review"}
	toolSet := testToolSet(append(slideToolNames, "skill.search", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember", "tool.describe"))
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "simple-slides",
			AllowedTools: slideToolNames,
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "simple-slides", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "발표자료 만들어줘"}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{}, ToolExposureEvent{})

	for _, toolName := range slideToolNames {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill tool %s to survive fallback, got %+v", toolName, event.ExposedToolIDs)
		}
	}
	if filteredToolSet.IsAllowed("tool.describe") {
		t.Fatalf("expected low-priority core tool to yield to selected skill tools, got %+v", event.ExposedToolIDs)
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
