package bluecollar

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"strings"
	"testing"
)

func newHybridKernelCapabilityToolSet(kernelToolNames []string, operationNames []string) *toolcontract.ToolSet {
	toolNames := append(append([]string{}, kernelToolNames...), operationNames...)
	toolSet := toolcontract.NewToolSet(toolNames)
	toolSet.AllowTestReplacement()
	for _, toolName := range toolNames {
		registerTestTool(toolSet, toolcontract.ToolDefinition{Name: toolName}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}
	return toolSet
}

func TestPlannedToolsDropRepeatedFileRead(t *testing.T) {
	observations := []turnObservation{
		newFailureObservation("obs-001", "policy", "file_read", "Already read tmp/deck/presentation.md lines 1-400.", toolcontract.FailurePolicyBlocked, toolcontract.FailureCodes.PolicyBlocked, "file_read_repeat"),
	}

	toolNames := filterExhaustedRecoveryToolNames([]string{"file_read", "terminal_run", "file_deliver"}, observations)

	if stringSliceContains(toolNames, "file_read") {
		t.Fatalf("expected repeated file_read to be removed, got %+v", toolNames)
	}
	for _, toolName := range []string{"terminal_run", "file_deliver"} {
		if !stringSliceContains(toolNames, toolName) {
			t.Fatalf("expected %s to remain available, got %+v", toolName, toolNames)
		}
	}
}

func TestSelectedSkillExposesDirectTools(t *testing.T) {
	toolSet := testToolSet(append(toolcontract.KernelToolNames(), "task_add", "task_list"))
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "internkim-flow", ToolReferences: []string{"task_add", "task_list"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{}, ExecutionPlan{}, false, OutcomeContract{}, ToolExposureEvent{})

	for _, toolName := range []string{"task_add", "task_list"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill tool %s, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	if filteredToolSet.IsAllowed(toolcontract.SkillSearchToolName) {
		t.Fatalf("expected loaded skill instructions to hide skill_search, got %+v", filteredToolSet.ListToolNames())
	}
	if !sameStringSet(event.SelectedSkillToolIDs, []string{"task_add", "task_list"}) {
		t.Fatalf("expected selected skill event, got %+v", event)
	}
	if event.SelectionSource != "selected_skills" {
		t.Fatalf("expected selected skill source, got %+v", event)
	}
}

func TestAuthoritativeContractExposesWorkingSetWithSkillTools(t *testing.T) {
	flowToolNames := []string{"task_add", "task_list", "task_update", "task_delete"}
	toolSet := testToolSet(append(toolcontract.KernelToolNames(), flowToolNames...))
	instructionBundle := InstructionBundle{
		Skills:                      []SkillInstruction{{Name: "internkim-flow", ToolReferences: flowToolNames}},
		SkillDecisions:              []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
		RequiredNextTools:           []string{"task_add"},
		RequiredEvidenceTools:       []string{"task_add"},
		HasContractSkillArbitration: true,
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{PinnedToolNames: []string{"task_add"}},
		ExecutionPlan{},
		false,
		OutcomeContract{RequiredEvidenceTools: []string{"task_add"}},
		ToolExposureEvent{},
	)

	expectedToolNames := append(kernelToolNamesForInstructionBundle(instructionBundle), flowToolNames...)
	if !sameStringSet(filteredToolSet.ListToolNames(), expectedToolNames) {
		t.Fatalf("expected task contract working set with skill tools, got %+v", filteredToolSet.ListToolNames())
	}
	if event.SelectionSource != "contract_arbitration" {
		t.Fatalf("expected contract arbitration source, got %+v", event)
	}
	if !sameStringSet(event.SelectedSkillToolIDs, flowToolNames) {
		t.Fatalf("expected selected skill tools exposed, got %+v", event)
	}
}

func TestAuthoritativeContractPreservesCompoundWorkflow(t *testing.T) {
	flowToolNames := []string{"task_add", "task_list", "task_update", "task_delete"}
	calendarToolNames := []string{"calendar_add", "calendar_list", "calendar_update", "calendar_delete"}
	toolSet := testToolSet(append(append(toolcontract.KernelToolNames(), flowToolNames...), calendarToolNames...))
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "internkim-flow", ToolReferences: flowToolNames},
			{Name: "calendar", ToolReferences: calendarToolNames},
		},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "internkim-flow", Status: "selected"},
			{Name: "calendar", Status: "selected"},
		},
		RequiredNextTools:           []string{"task_add", "calendar_add"},
		RequiredEvidenceTools:       []string{"task_add", "calendar_add"},
		HasContractSkillArbitration: true,
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{RequiredEvidenceTools: []string{"task_add", "calendar_add"}},
		ToolExposureEvent{},
	)

	expectedToolNames := append(append(kernelToolNamesForInstructionBundle(instructionBundle), flowToolNames...), calendarToolNames...)
	if !sameStringSet(filteredToolSet.ListToolNames(), expectedToolNames) {
		t.Fatalf("expected compound contract working set with skill tools, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestAuthoritativeContractPreservesTypedRecoveryTool(t *testing.T) {
	flowToolNames := []string{"task_add", "task_update"}
	toolSet := testToolSet(append(toolcontract.KernelToolNames(), flowToolNames...))
	instructionBundle := InstructionBundle{
		Skills:                      []SkillInstruction{{Name: "internkim-flow", ToolReferences: flowToolNames}},
		SkillDecisions:              []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
		RequiredNextTools:           []string{"task_add"},
		HasContractSkillArbitration: true,
	}
	observation := newFailureObservation("obs-001", "continue", "task_add", "retry with an existing task", toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "invoke")
	observation.ToolInputKey = "task_add\x00{}"
	observation.Failure.RecoveryHints = []toolcontract.RecoveryHint{{ToolNames: []string{"task_update"}}}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{RequiredEvidenceTools: []string{"task_add"}},
		ToolExposureEvent{},
		[]turnObservation{observation},
	)

	expectedToolNames := append(kernelToolNamesForInstructionBundle(instructionBundle), "task_add", "task_update")
	if !sameStringSet(filteredToolSet.ListToolNames(), expectedToolNames) {
		t.Fatalf("expected contract and recovery working set, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestImmediateReplyWithoutToolIntentExposesNoTools(t *testing.T) {
	toolSet := testToolSet(toolcontract.KernelToolNames())

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		AgentRequest{TaskShape: TaskShapeImmediateReply},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)

	if len(filteredToolSet.ListToolNames()) != 0 {
		t.Fatalf("expected pure reply to expose no tools, got %+v", filteredToolSet.ListToolNames())
	}
	if len(event.ExposedToolIDs) != 0 {
		t.Fatalf("expected pure reply event to contain no tools, got %+v", event)
	}
}

func TestImmediateReplyWithPinnedToolExposesFullKernel(t *testing.T) {
	toolSet := testToolSet(append(toolcontract.KernelToolNames(), "schedule_list"))

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		AgentRequest{TaskShape: TaskShapeImmediateReply, PinnedToolNames: []string{"schedule_list"}},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)

	expectedToolNames := append(append([]string{}, toolcontract.KernelToolNames()...), "schedule_list")
	if !sameStringSet(filteredToolSet.ListToolNames(), expectedToolNames) {
		t.Fatalf("expected full kernel with the pinned tool, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestEmptyArbitrationWorkingSetPreservesDocumentKernel(t *testing.T) {
	toolSet := testToolSet(toolcontract.KernelToolNames())
	instructionBundle := InstructionBundle{
		Skills:                      []SkillInstruction{{Name: "document"}},
		SkillDecisions:              []SkillSelectionDecision{{Name: "document", Status: "selected"}},
		HasContractSkillArbitration: true,
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{RequiredEvidenceTools: []string{toolcontract.FileDeliverToolName}},
		ToolExposureEvent{},
	)

	if !sameStringSet(filteredToolSet.ListToolNames(), kernelToolNamesForInstructionBundle(instructionBundle)) {
		t.Fatalf("expected document kernel fallback, got %+v", filteredToolSet.ListToolNames())
	}
	if event.SelectionSource != "fixed_kernel" {
		t.Fatalf("expected fixed kernel source, got %+v", event)
	}
}

func TestSelectedSkillRankingControlsToolBudget(t *testing.T) {
	secondaryToolNames := []string{
		"calendar_add", "calendar_list", "calendar_update", "calendar_delete",
		"company_info_get", "company_info_set", "company_metric_list", "company_metric_record",
		"company_record_list", "company_record_add", "company_record_update", "company_record_delete",
		"company_document_list", "company_document_search", "company_document_register",
	}
	flowToolNames := []string{"task_add", "task_list", "task_update", "task_delete"}
	toolSet := testToolSet(append(append(toolcontract.KernelToolNames(), secondaryToolNames...), flowToolNames...))
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "secondary", ToolReferences: secondaryToolNames},
			{Name: "internkim-flow", ToolReferences: flowToolNames},
		},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "internkim-flow", Status: "selected"},
			{Name: "secondary", Status: "selected"},
		},
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{}, ExecutionPlan{}, false, OutcomeContract{}, ToolExposureEvent{})

	for _, toolName := range flowToolNames {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected first-ranked skill tool %s, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestPinnedDirectToolWinsSelectedSkillBudget(t *testing.T) {
	selectedToolNames := []string{
		"site_serve", "site.audit", "artifact_review", "site.snapshot",
		"site_list", "site.history", "site.diff", "site.logs",
		"site.rollback", "site.unpublish", "site.restore", "site_unserve",
		"site.metrics", "site.backup", "site.scan", "site.verify", "site.export",
		"file_read", "file_write", "file_edit", "terminal_run",
	}
	toolSet := testToolSet(append(toolcontract.KernelToolNames(), selectedToolNames...))
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "website", ToolReferences: selectedToolNames}},
		SkillDecisions: []SkillSelectionDecision{{Name: "website", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{PinnedToolNames: []string{"terminal_run"}},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)

	if !filteredToolSet.IsAllowed("terminal_run") {
		t.Fatalf("expected pinned direct tool inside budget, got %+v", filteredToolSet.ListToolNames())
	}
	expectedToolCount := len(kernelToolNamesForInstructionBundle(instructionBundle)) + maxExtensionCallableToolCount
	if len(filteredToolSet.ListToolNames()) != expectedToolCount {
		t.Fatalf("expected %d tools, got %+v", expectedToolCount, filteredToolSet.ListToolNames())
	}
	if len(event.DroppedGroups) == 0 {
		t.Fatalf("expected oversized selected skill to report dropped tools, got %+v", event)
	}
	for _, toolName := range event.SelectedSkillToolIDs {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill metadata to contain only exposed tools, got %+v", event)
		}
	}
}

func TestRequiredEvidenceWinsToolBudget(t *testing.T) {
	selectedToolNames := []string{
		"site_serve", "site_serve", "artifact_review", "site_serve",
		"site_list", "site.history", "site.diff", "site.logs",
		"site.rollback", "site.unpublish", "site.restore", "site_unserve",
		"file_read", "file_write", "file_edit", "terminal_run",
	}
	toolSet := testToolSet(append(append(toolcontract.KernelToolNames(), selectedToolNames...), "task_update"))
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "website", ToolReferences: selectedToolNames}},
		SkillDecisions: []SkillSelectionDecision{{Name: "website", Status: "selected"}},
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{RequiredEvidenceTools: []string{"task_update"}},
		ToolExposureEvent{},
	)

	if !filteredToolSet.IsAllowed("task_update") {
		t.Fatalf("expected required evidence inside budget, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestPendingRequiredToolWinsExtensionToolBudget(t *testing.T) {
	selectedToolNames := []string{
		"tool.01", "tool.02", "tool.03", "tool.04", "tool.05",
		"tool.06", "tool.07", "tool.08", "tool.09", "tool.10", "tool.11",
		"tool.12", "tool.13", "tool.14", "tool.15", "tool.16",
	}
	toolSet := testToolSet(append(toolcontract.KernelToolNames(), selectedToolNames...))
	instructionBundle := InstructionBundle{
		Skills:            []SkillInstruction{{Name: "extension", ToolReferences: selectedToolNames}},
		SkillDecisions:    []SkillSelectionDecision{{Name: "extension", Status: "selected"}},
		RequiredNextTools: []string{"tool.16"},
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)

	if !filteredToolSet.IsAllowed("tool.16") {
		t.Fatalf("expected pending operation inside budget, got %+v", filteredToolSet.ListToolNames())
	}
	if filteredToolSet.IsAllowed("tool.15") {
		t.Fatalf("expected a non-pending extension to leave the budget, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestEachRequiredEvidenceAlternativeGroupKeepsOneTool(t *testing.T) {
	firstGroup := []string{
		"tool.01", "tool.02", "tool.03", "tool.04", "tool.05",
		"tool.06", "tool.07", "tool.08", "tool.09", "tool.10",
		"tool.11", "tool.12", "tool.13", "tool.14", "tool.15",
	}
	secondGroup := []string{"task_update"}
	toolSet := testToolSet(append(append(toolcontract.KernelToolNames(), firstGroup...), secondGroup...))

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{RequiredEvidenceAnyOf: [][]string{firstGroup, secondGroup}},
		ToolExposureEvent{},
	)

	for _, toolName := range []string{firstGroup[0], secondGroup[0]} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected one tool from every evidence group, got %+v", filteredToolSet.ListToolNames())
		}
	}
}

func TestAuthoritativeWorkingSetKeepsSelectedSkillTools(t *testing.T) {
	toolSet := testToolSet(append(toolcontract.KernelToolNames(), "site_serve", "site_list"))
	instructionBundle := InstructionBundle{
		HasContractSkillArbitration: true,
		RequiredNextTools:           []string{"file_write"},
		Skills:                      []SkillInstruction{{Name: "website", ToolReferences: []string{"site_serve", "site_list"}}},
		SkillDecisions:              []SkillSelectionDecision{{Name: "website", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{}, ExecutionPlan{}, false, OutcomeContract{}, ToolExposureEvent{})

	for _, toolName := range []string{"site_serve", "site_list"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill tool %s in authoritative working set, got %+v", toolName, event.ExposedToolIDs)
		}
	}
}

func TestInterleaveToolNameListsKeepsEverySkillRepresented(t *testing.T) {
	interleaved := interleaveToolNameLists([][]string{
		{"task_add", "task_list", "task_update"},
		{"calendar_add", "calendar_list"},
		{"message_send"},
	})

	expected := []string{"task_add", "calendar_add", "message_send", "task_list", "calendar_list", "task_update"}
	if len(interleaved) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, interleaved)
	}
	for index, toolName := range expected {
		if interleaved[index] != toolName {
			t.Fatalf("expected %v, got %v", expected, interleaved)
		}
	}
}

func TestRequestedToolNamesFromObservationsPinsSuccessfulRequests(t *testing.T) {
	successful := newContentObservation("obs-001", "continue", toolcontract.RequestToolsToolName, "")
	successful.Output = toolcontract.ToolOutput{Data: json.RawMessage(`{"requestedToolNames":["calendar_update","message_delete"]}`)}
	failed := newContentObservation("obs-002", "continue", toolcontract.RequestToolsToolName, "")
	failed.Output = toolcontract.ToolOutput{Data: json.RawMessage(`{"requestedToolNames":["task_delete"]}`)}
	failed.Failure = &toolcontract.ToolFailure{Kind: toolcontract.FailureInvalidInput}

	toolNames := requestedToolNamesFromObservations([]turnObservation{successful, failed})

	if !sameStringSet(toolNames, []string{"calendar_update", "message_delete"}) {
		t.Fatalf("expected only successful requests to pin, got %+v", toolNames)
	}
}

func TestDroppedExposureToolNamesFlattenGroups(t *testing.T) {
	exposure := ToolExposureEvent{DroppedGroups: []droppedToolGroup{
		{Name: "selected skills", ToolIDs: []string{"calendar_update", "task_update"}},
		{Name: "evidence alternatives", ToolIDs: []string{"task_update", "message_delete"}},
	}}

	toolNames := droppedExposureToolNames(exposure)

	if !sameStringSet(toolNames, []string{"calendar_update", "task_update", "message_delete"}) {
		t.Fatalf("expected flattened unique dropped names, got %+v", toolNames)
	}
}

func TestAdditionalToolsContextListsPageWithSummaries(t *testing.T) {
	toolSet := toolcontract.NewToolSet([]string{"calendar_update"})
	registerTestTool(toolSet, toolcontract.ToolDefinition{Name: "calendar_update", Description: "Update a calendar event. Provide an eventHint."}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("ok"), nil
	})
	toolNames := []string{"calendar_update"}
	for index := 0; index < additionalToolsContextPageSize; index++ {
		toolNames = append(toolNames, fmt.Sprintf("filler.tool%02d", index))
	}

	rendered := (LLMContextBuilder{}).additionalToolsContext(LLMContextInput{AdditionalToolNames: toolNames, ToolSet: toolSet})

	if !strings.Contains(rendered, "calendar_update — Update a calendar event") {
		t.Fatalf("expected name with summary, got %s", rendered)
	}
	if !strings.Contains(rendered, "and 1 more") {
		t.Fatalf("expected overflow page note, got %s", rendered)
	}
}

func TestRequestedToolsAttachOwningSkillInstructions(t *testing.T) {
	calendarSkill := SkillInstruction{Name: "calendar", ToolReferences: []string{"calendar_add", "calendar_update"}}
	request := AgentTurnRequest{
		AvailableSkills: []SkillInstruction{calendarSkill},
		SkillDecisions:  []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
	}
	observation := newContentObservation("obs-001", "continue", toolcontract.RequestToolsToolName, "")
	observation.Output = toolcontract.ToolOutput{Data: json.RawMessage(`{"requestedToolNames":["calendar_update"]}`)}

	amendedRequest := requestWithStepWorkingSetTools(request, []turnObservation{observation})

	hasCalendarDecision := false
	for _, decision := range amendedRequest.SkillDecisions {
		if decision.Name == "calendar" && decision.Status == "selected" {
			hasCalendarDecision = true
		}
	}
	if !hasCalendarDecision {
		t.Fatalf("expected the owning skill to be selected for its requested tool, got %+v", amendedRequest.SkillDecisions)
	}
	if len(request.SkillDecisions) != 1 {
		t.Fatalf("expected the caller's decisions to stay untouched, got %+v", request.SkillDecisions)
	}
}
