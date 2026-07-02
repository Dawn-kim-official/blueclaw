package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestToolExposureKeepsSelectedToolsBeforeCore(t *testing.T) {
	toolSet := testToolSet([]string{"skill.search", "tool.describe", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember", "site.status", "site.publish"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: []string{"site.status", "site.publish"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "사이트 고쳐줘"}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{SelectedToolIDs: []string{"site.status"}}, ToolExposureEvent{})

	if !filteredToolSet.IsAllowed("site.status") {
		t.Fatalf("expected selected tool to be exposed, got %+v", filteredToolSet.ListToolNames())
	}
	if filteredToolSet.IsAllowed("site.publish") {
		t.Fatalf("expected unselected fallback skill tool to be hidden when selection is valid, got %+v", filteredToolSet.ListToolNames())
	}
	for _, toolID := range []string{"skill.search", "ask.choice", "ask.input", "memory.search", "conversation.history"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected core tool %s to remain exposed, got %+v", toolID, filteredToolSet.ListToolNames())
		}
	}
	if !filteredToolSet.IsAllowed("memory.remember") {
		t.Fatalf("expected selected tool plus core groups to fit within the cap, got %+v", filteredToolSet.ListToolNames())
	}
	if event.UsedFallbackGroups {
		t.Fatalf("expected valid model selection to avoid fallback groups: %+v", event)
	}
}

func TestToolExposureFallsBackWhenSelectionIsEmptyOrInvalid(t *testing.T) {
	toolSet := testToolSet([]string{"skill.search", "memory.remember", "ask.confirm", "terminal.run", "file.write", "mail.message.search"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "slides", AllowedTools: []string{"terminal.run", "file.write"}},
			{Name: "mail", AllowedTools: []string{"mail.message.search"}},
		},
		SkillDecisions: []SkillSelectionDecision{{Name: "slides", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "발표자료 만들어줘", WorkKinds: []string{WorkKindSlidesArtifact}}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{SelectedToolIDs: []string{"unknown.tool"}}, ToolExposureEvent{})

	for _, toolID := range []string{"skill.search", "memory.remember"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected fallback to expose %s, got %+v", toolID, filteredToolSet.ListToolNames())
		}
	}
	for _, toolID := range []string{"terminal.run", "file.write", "mail.message.search"} {
		if filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected unrequested tool %s to stay hidden, got %+v", toolID, filteredToolSet.ListToolNames())
		}
	}
	if !event.UsedFallbackGroups {
		t.Fatalf("expected invalid selection to use fallback groups: %+v", event)
	}
}

func TestPinnedCalendarWorkRequestExposesTaskAndCalendarTools(t *testing.T) {
	toolSet := testToolSet([]string{
		"skill.search",
		"tool.describe",
		"ask.confirm",
		"calendar.add",
		"calendar.list",
		"calendar.update",
		"calendar.delete",
		"task.add",
		"task.list",
		"task.update",
	})
	request := AgentRequest{
		Prompt: "디플랫 코리아 완료",
		PinnedToolNames: []string{
			"calendar.add",
			"calendar.list",
			"calendar.update",
			"calendar.delete",
			"task.add",
			"task.list",
			"task.update",
		},
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		request,
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolSelectionDecision{},
		ToolExposureEvent{},
	)

	for _, toolID := range []string{"task.update", "task.list", "calendar.list", "calendar.update"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected %s for calendar/work request, got %+v", toolID, filteredToolSet.ListToolNames())
		}
	}
}

func TestStepWorkingSetPinsCalendarToolsWithoutFlowTaskTools(t *testing.T) {
	request := requestWithStepWorkingSetTools(AgentTurnRequest{
		WorkKinds: []string{WorkKindCalendar},
	}, nil)

	for _, toolName := range []string{"calendar.add", "calendar.list", "calendar.update", "calendar.delete"} {
		if !containsString(request.PinnedToolNames, toolName) {
			t.Fatalf("expected calendar tool %s to be pinned, got %+v", toolName, request.PinnedToolNames)
		}
	}
	for _, toolName := range []string{"task.add", "task.list", "task.update"} {
		if containsString(request.PinnedToolNames, toolName) {
			t.Fatalf("expected flow task tool %s to stay unpinned for calendar work, got %+v", toolName, request.PinnedToolNames)
		}
	}
}

func TestStepWorkingSetPinsFlowTaskToolsWithoutCalendarTools(t *testing.T) {
	request := requestWithStepWorkingSetTools(AgentTurnRequest{
		WorkKinds: []string{WorkKindFlowTask},
	}, nil)

	for _, toolName := range []string{"task.add", "task.list", "task.update"} {
		if !containsString(request.PinnedToolNames, toolName) {
			t.Fatalf("expected flow task tool %s to be pinned, got %+v", toolName, request.PinnedToolNames)
		}
	}
	for _, toolName := range []string{"calendar.add", "calendar.list", "calendar.update", "calendar.delete"} {
		if containsString(request.PinnedToolNames, toolName) {
			t.Fatalf("expected calendar tool %s to stay unpinned for flow task work, got %+v", toolName, request.PinnedToolNames)
		}
	}
}

func TestStepWorkingSetPinsSitePrototypeToolsWithoutCalendarOrFlowTaskTools(t *testing.T) {
	request := requestWithStepWorkingSetTools(AgentTurnRequest{
		WorkKinds: []string{WorkKindSitePrototype},
	}, nil)

	for _, toolName := range []string{"site.status", "site.create", "site.repair", "file.read", "file.write", "file.edit", "file.patch", "terminal.run", "site.build", "artifact.review", "site.publish"} {
		if !containsString(request.PinnedToolNames, toolName) {
			t.Fatalf("expected site prototype tool %s to be pinned, got %+v", toolName, request.PinnedToolNames)
		}
	}
	for _, toolName := range []string{"calendar.add", "calendar.list", "task.add", "task.update"} {
		if containsString(request.PinnedToolNames, toolName) {
			t.Fatalf("expected unrelated workflow tool %s to stay unpinned for site prototype work, got %+v", toolName, request.PinnedToolNames)
		}
	}
}

func TestToolExposureSelectsAttachmentReadToolForVisibleMaterial(t *testing.T) {
	toolSet := testToolSet([]string{"skill.search", "tool.describe", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember", "terminal.run", "image.read", "file.preview", "document.read", "mail.message.search", "message.send"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "mail", AllowedTools: []string{"mail.message.search"}},
			{Name: "mattermost", AllowedTools: []string{"message.send"}},
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
	for _, toolName := range []string{"mail.message.search", "message.send"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill tool %s to stay hidden for attachment read selection, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestToolExposureDropsStaleSiteToolsForMessageDeleteOutcome(t *testing.T) {
	toolSet := testToolSet([]string{
		"skill.search",
		"tool.describe",
		"ask.confirm",
		"message.context",
		"message.search",
		"message.send",
		"message.delete",
		"site.status",
		"site.build",
		"artifact.review",
		"site.publish",
	})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "mattermost", AllowedTools: []string{"message.context", "message.search", "message.send", "message.delete"}},
			{Name: "site-prototype", AllowedTools: []string{"site.status", "site.build", "artifact.review", "site.publish"}},
		},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "mattermost", Status: "selected"},
			{Name: "site-prototype", Status: "selected"},
		},
	}
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{"message.delete"},
		SelectedEvidenceHints: []string{"message.delete"},
		ExpectedResults: []ExpectedResult{{
			ID:          "final-message",
			Type:        ExpectedResultTypeMessage,
			Description: "삭제 결과를 알려주는 최종 답변",
			Required:    true,
		}},
	}

	request := AgentRequest{
		Prompt:          "네가 보낸 메시지 삭제해줘",
		PinnedToolNames: []string{"message.delete"},
	}
	filteredToolSet, _ := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract, ToolSelectionDecision{}, ToolExposureEvent{})

	for _, toolName := range []string{"site.status", "site.build", "artifact.review", "site.publish"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected stale site tool %s to stay hidden, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	if !filteredToolSet.IsAllowed("message.delete") {
		t.Fatalf("expected message.delete to remain exposed, got %+v", filteredToolSet.ListToolNames())
	}
	if filteredToolSet.IsAllowed("message.send") {
		t.Fatalf("expected message.send to stay hidden for delete completion reporting, got %+v", filteredToolSet.ListToolNames())
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

func TestPinnedPaletteTruncatesToolsByOrder(t *testing.T) {
	siteToolIDs := []string{
		"site.status",
		"site.create",
		"file.read",
		"file.write",
		"terminal.run",
		"site.build",
		"browser.open",
		"browser.snapshot",
		"browser.screenshot",
		"artifact.review",
		"site.publish",
		"site.repair",
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
	request := AgentRequest{
		Prompt:          "개인 홈페이지 만들고 배포해줘",
		WorkKinds:       []string{WorkKindSitePrototype},
		PinnedToolNames: siteToolIDs,
	}
	selectionRequest := buildToolSelectionRequest(toolSet, instructionBundle, request, ExecutionPlan{}, false, OutcomeContract{})
	selection, event, isDeterministic := deterministicToolSelectionDecision(selectionRequest)
	if !isDeterministic {
		t.Fatal("expected pinned site operations to select deterministically")
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, request, ExecutionPlan{}, false, OutcomeContract{}, selection, event)

	if len(event.ExposedToolIDs) > maxSchemaCallableToolCount {
		t.Fatalf("expected exposed tools to stay within cap, got %+v", event.ExposedToolIDs)
	}
	for _, toolID := range []string{"site.status", "site.create", "file.read", "file.write", "terminal.run", "site.build", "browser.open", "browser.snapshot", "browser.screenshot", "artifact.review", "site.publish", "site.repair"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected deterministic skill tool %s to be exposed, got %+v", toolID, event.ExposedToolIDs)
		}
	}
	for _, toolID := range []string{"site.extra.03", "site.extra.09", "site.extra.14"} {
		if filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected lower-priority site tool %s to stay out of the capped palette, got %+v", toolID, event.ExposedToolIDs)
		}
	}
}

func TestPinnedPaletteKeepsRequestedFileWorkflowAheadOfOtherSkills(t *testing.T) {
	toolIDs := []string{
		"terminal.run",
		"file.write",
		"file.promote",
		"file.deliver",
		"artifact.review",
		"site.status",
		"site.create",
		"site.build",
		"site.publish",
	}
	toolSet := testToolSet(toolIDs)
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "simple-slides", AllowedTools: []string{"terminal.run", "file.write", "file.promote", "file.deliver", "artifact.review"}},
			{Name: "site-prototype", AllowedTools: []string{"site.status", "site.create", "site.build", "site.publish", "terminal.run", "file.write", "artifact.review"}},
		},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "simple-slides", Status: "selected"},
			{Name: "site-prototype", Status: "selected"},
		},
	}
	contract := OutcomeContract{
		ArtifactRequirement:        ArtifactRequirementRequired,
		RequiredAttachmentSuffixes: []string{".pptx"},
		RequiredEvidenceTools:      []string{"file.deliver"},
	}
	request := AgentRequest{
		Prompt:          "PPTX 발표자료 만들어 첨부해줘",
		PinnedToolNames: []string{"terminal.run", "file.write", "file.promote", "file.deliver", "artifact.review"},
	}
	selectionRequest := buildToolSelectionRequest(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)
	selection, event, isDeterministic := deterministicToolSelectionDecision(selectionRequest)
	if !isDeterministic {
		t.Fatal("expected requested file workflow tools to select deterministically")
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract, selection, event)

	for _, toolID := range []string{"terminal.run", "file.write", "file.promote", "file.deliver", "artifact.review"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected selected file workflow tool %s to be exposed, got %+v", toolID, event.ExposedToolIDs)
		}
	}
	if filteredToolSet.IsAllowed("site.publish") {
		t.Fatalf("expected lower-priority site publish tool to stay out of PPTX palette, got %+v", event.ExposedToolIDs)
	}
}

func TestRecoveryWorkingSetKeepsPendingFileDeliveryTools(t *testing.T) {
	toolSet := testToolSet([]string{
		"terminal.run",
		"site.status",
		"site.repair",
		"artifact.review",
		"file.deliver",
		"file.promote",
		"file.read",
		"skill.search",
	})
	instructionBundle := InstructionBundle{}
	contract := OutcomeContract{
		ArtifactRequirement:        ArtifactRequirementRequired,
		RequiredAttachmentSuffixes: []string{".pptx"},
		SelectedEvidenceHints:      []string{"file.promote", "file.deliver"},
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
			AllowedTools: []string{"site.status", "site.repair", "terminal.run", "artifact.review"},
		},
	}
	request := AgentRequest{
		Prompt:          "PPTX 파일 첨부해줘",
		PinnedToolNames: []string{"file.promote", "file.deliver"},
	}
	selectionRequest := buildToolSelectionRequest(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract, []turnObservation{observation})
	selection, event, isDeterministic := deterministicToolSelectionDecision(selectionRequest)
	if !isDeterministic {
		t.Fatal("expected recovery and outcome tools to select deterministically")
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract, selection, event, []turnObservation{observation})

	for _, toolID := range []string{"terminal.run", "artifact.review", "file.deliver", "file.promote"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected recovery working set to expose %s, got %+v", toolID, event.ExposedToolIDs)
		}
	}
}

func TestStepWorkingSetKeepsFileCreationToolsAfterAttachPathFailure(t *testing.T) {
	toolSet := testToolSet([]string{
		"file.preview",
		"file.read",
		"file.write",
		"file.edit",
		"file.patch",
		"terminal.run",
		"artifact.review",
		"file.promote",
		"file.deliver",
	})
	request := requestWithStepWorkingSetTools(AgentTurnRequest{
		Prompt:  "첨부파일로 줘",
		ToolSet: toolSet,
		AvailableSkills: []SkillInstruction{{
			Name:         "enterprise-document-maker",
			AllowedTools: []string{"file.write", "terminal.run", "file.promote", "file.deliver"},
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"file.promote", "file.deliver"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "enterprise-document-maker", Status: "selected"}},
		PinnedToolNames: []string{
			"file.deliver",
		},
		OutcomeContract: OutcomeContract{
			ArtifactRequirement:        ArtifactRequirementRequired,
			RequiredAttachmentSuffixes: []string{".docx"},
			RequiredEvidenceTools:      []string{"file.deliver"},
			ExpectedResults: []ExpectedResult{{
				ID:       "docx-guide",
				Type:     ExpectedResultTypeFile,
				Required: true,
			}},
		},
	}, []turnObservation{
		newFailureObservation("obs-001", "continue", "file.deliver", "stat artifacts/guide.docx: no such file or directory", FailureInvalidInput, FailureCodes.InvalidInput, "file_attach"),
	})
	selectionRequest := buildToolSelectionRequest(toolSet, instructionBundleFromTurnRequest(request), agentRequestFromTurnRequest(request), ExecutionPlan{}, false, request.OutcomeContract)
	selection, event, isDeterministic := deterministicToolSelectionDecision(selectionRequest)
	if !isDeterministic {
		t.Fatal("expected pending file delivery tools to select deterministically")
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundleFromTurnRequest(request), agentRequestFromTurnRequest(request), ExecutionPlan{}, false, request.OutcomeContract, selection, event)

	for _, toolID := range []string{"file.write", "terminal.run", "file.promote", "file.deliver"} {
		if !containsString(request.PinnedToolNames, toolID) {
			t.Fatalf("expected pending delivery pin %s, got %+v", toolID, request.PinnedToolNames)
		}
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected pending delivery tool %s to be exposed, got %+v", toolID, event.ExposedToolIDs)
		}
	}
}

func TestRecoveryWorkingSetUsesActiveFailureHints(t *testing.T) {
	toolSet := testToolSet([]string{"file.read", "file.write", "site.build", "site.publish", "skill.search", "tool.describe", "ask.confirm"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: []string{"file.read", "file.write", "site.build", "site.publish"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	observation := turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "site.build",
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
		ToolInputKey:       "site.build\x00{\"siteID\":\"site-1\"}",
		AttemptFingerprint: "site.build\x00{\"siteID\":\"site-1\"}\x00invalid_input",
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
	if filteredToolSet.IsAllowed("site.publish") {
		t.Fatalf("expected publish to stay hidden until source changes and rebuild succeeds, got %+v", event.ExposedToolIDs)
	}
}

func TestRecoveryWorkingSetDoesNotLetPinnedSkillToolsCrowdOutRecoveryHints(t *testing.T) {
	siteToolNames := []string{
		"site.build",
		"site.publish",
		"site.status",
		"terminal.run",
		"terminal.session",
		"browser.open",
		"browser.snapshot",
		"browser.screenshot",
		"file.read",
		"file.edit",
		"file.patch",
		"file.write",
		"site.create",
		"site.repair",
		"site.preview",
		"site.history",
		"site.diff",
		"site.logs",
		"site.rollback",
		"site.unpublish",
		"site.restore",
		"site.delete",
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
		Tool:          "site.build",
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
		ToolInputKey:       "site.build\x00{\"siteID\":\"site-1\"}",
		AttemptFingerprint: "site.build\x00{\"siteID\":\"site-1\"}\x00invalid_input",
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
	if filteredToolSet.IsAllowed("browser.screenshot") || filteredToolSet.IsAllowed("site.rollback") {
		t.Fatalf("expected broad pinned skill tools to stay out of G4 recovery set, got %+v", event.ExposedToolIDs)
	}
}

func TestDeterministicPinnedStepExposesOnlyPinnedTools(t *testing.T) {
	toolSet := testToolSet([]string{"site.build", "skill.search", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember"})
	request := AgentRequest{Prompt: "빌드해줘", PinnedToolNames: []string{"site.build"}}
	selection, event := deterministicToolSelection([]string{"site.build"}, "pinned tools are required for the next step")

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, InstructionBundle{}, request, ExecutionPlan{}, false, OutcomeContract{}, selection, event)

	if !filteredToolSet.IsAllowed("site.build") {
		t.Fatalf("expected pinned build tool to be exposed, got %+v", event.ExposedToolIDs)
	}
	for _, toolID := range []string{"skill.search", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember"} {
		if filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected deterministic pinned step to hide %s, got %+v", toolID, event.ExposedToolIDs)
		}
	}
}

func TestPinnedToolGroupBypassesOutcomeFilter(t *testing.T) {
	siteToolNames := []string{"site.status", "site.create", "site.publish", "browser.open", "browser.snapshot"}
	toolSet := testToolSet(append(siteToolNames, "skill.search", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember"))
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: siteToolNames,
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt:          "이어 해줘",
		PinnedToolNames: []string{"site.status", "site.create"},
	}
	selectionRequest := buildToolSelectionRequest(toolSet, instructionBundle, request, ExecutionPlan{}, false, OutcomeContract{})
	selection, event, isDeterministic := deterministicToolSelectionDecision(selectionRequest)
	if !isDeterministic {
		t.Fatal("expected pinned site operations to select deterministically")
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, request, ExecutionPlan{}, false, OutcomeContract{}, selection, event)

	for _, toolID := range []string{"site.status", "site.create"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected pinned selected-skill tool %s to be exposed, got %+v", toolID, event.ExposedToolIDs)
		}
	}
	if filteredToolSet.IsAllowed("site.publish") {
		t.Fatalf("expected unpinned site skill tool to keep outcome filtering, got %+v", event.ExposedToolIDs)
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

	toolNames := filterExhaustedRecoveryToolNames([]string{"file.read", "terminal.run", "file.deliver"}, observations)

	if stringSliceContains(toolNames, "file.read") {
		t.Fatalf("expected repeated file.read to be removed, got %+v", toolNames)
	}
	for _, toolName := range []string{"terminal.run", "file.deliver"} {
		if !stringSliceContains(toolNames, toolName) {
			t.Fatalf("expected %s to remain available, got %+v", toolName, toolNames)
		}
	}
}

func TestFallbackHidesUnrequestedCapabilityTools(t *testing.T) {
	toolSet := testToolSet([]string{"conversation.history", "memory.search", "browser.snapshot", "file.write", "terminal.run"})
	instructionBundle := InstructionBundle{}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "open browser and observe"}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{}, ToolExposureEvent{})

	if filteredToolSet.IsAllowed("browser.snapshot") {
		t.Fatalf("expected unrequested capability tool to stay hidden, got %+v", event.ExposedToolIDs)
	}
	for _, toolID := range []string{"conversation.history", "memory.search"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected core tool %s to remain exposed, got %+v", toolID, event.ExposedToolIDs)
		}
	}
}

func TestFallbackHidesSelectedSkillToolsUntilRequested(t *testing.T) {
	slideToolNames := []string{"terminal.run", "file.read", "file.write", "file.edit", "file.patch", "file.promote", "file.deliver", "artifact.review"}
	toolSet := testToolSet(append(slideToolNames, "skill.search", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember", "tool.describe"))
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "simple-slides",
			AllowedTools: slideToolNames,
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "simple-slides", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "발표자료 만들어줘", WorkKinds: []string{WorkKindSlidesArtifact}}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{}, ToolExposureEvent{})

	for _, toolName := range slideToolNames {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill tool %s to stay hidden until requested, got %+v", toolName, event.ExposedToolIDs)
		}
	}
	if !filteredToolSet.IsAllowed("memory.remember") {
		t.Fatalf("expected remaining core capacity to include memory.remember, got %+v", event.ExposedToolIDs)
	}
}

func TestToolSelectionContextUsesCompactCards(t *testing.T) {
	toolSet := testToolSet([]string{"site.status"})
	cards := renderCompactToolCards(toolSet, []toolExposureGroup{{Name: "G5 selected-skill candidates", ToolIDs: []string{"site.status"}}})
	summary := renderCoreGroupSummary(collectCoreGroups(testToolSet([]string{"skill.search", "memory.remember"})))

	if !strings.Contains(cards, "- site.status:") {
		t.Fatalf("expected compact card to include tool id, got %s", cards)
	}
	if strings.Contains(cards, "inputSchema") || strings.Contains(cards, "properties") {
		t.Fatalf("expected compact card to omit full schema, got %s", cards)
	}
	if !strings.Contains(summary, "G1 control-core: skill.search") || !strings.Contains(summary, "G3 memory-context-core: memory.remember") {
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

	cards := renderCompactToolCards(toolSet, []toolExposureGroup{{Name: "file operations", ToolIDs: []string{"file.write", "file.edit", "file.patch"}}})

	for _, expectedText := range []string{"file.write", "full rewrite", "file.edit", "oldText", "file.patch", "Several targeted edits"} {
		if !strings.Contains(cards, expectedText) {
			t.Fatalf("expected file tool card text %q in %s", expectedText, cards)
		}
	}
}
