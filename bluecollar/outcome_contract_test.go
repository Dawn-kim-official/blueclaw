package bluecollar

import (
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"reflect"
	"strings"
	"testing"
)

func TestSelectedRequiredAttachmentSuffixesStayAdvisoryForSlides(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "presentation"}},
		SkillDecisions: []SkillSelectionDecision{{Name: "presentation", Status: "selected"}},
	}

	suffixes := selectedRequiredAttachmentSuffixes(instructionBundle, "Hermes Agent make a six-slide pros and cons deck, html is enough")

	if len(suffixes) != 0 {
		t.Fatalf("expected no hard suffix contract, got %+v", suffixes)
	}
}

func TestSelectedEvidenceHintsComeFromSelectedSkills(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "site-prototype"}, {Name: "calendar"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
		RequiredEvidenceTools: []string{"site_serve", "terminal_run", "site_serve"},
	}

	toolNames := selectedEvidenceHintTools(instructionBundle)

	if len(toolNames) != 3 || toolNames[0] != "site_serve" || toolNames[1] != "terminal_run" || toolNames[2] != "site_serve" {
		t.Fatalf("expected selected skill evidence tools, got %+v", toolNames)
	}
}

func TestOutcomeContractDerivesScheduleEvidenceFromSkillHint(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "task_add", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectStateChange},
		{Name: "schedule_create", Namespace: "schedule", SideEffectClass: toolcontract.ToolSideEffectStateChange},
	})
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "send this message tomorrow at 3pm",
			ToolSet: toolSet,
		},
		IntakeDecision{
			Classification: IntakeClassificationBoundedTask,
			TaskShape:      TaskShapeScheduledTask,
		},
		InstructionBundle{RequiredEvidenceTools: []string{"schedule_create"}},
		ExecutionPlan{},
		false,
		nil,
	)

	if !stringSliceContains(contract.RequiredEvidenceTools, "schedule_create") {
		t.Fatalf("expected schedule_create required evidence, got %+v", contract.RequiredEvidenceTools)
	}
	if stringSliceContains(contract.RequiredEvidenceTools, "task_add") {
		t.Fatalf("expected the skill-contract hint not to add fallback evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestAttachmentOutcomeTreatsWorkspaceFileWriteAsIntermediate(t *testing.T) {
	fileWrite := testToolDescriptor(toolcontract.FileWriteToolName)
	fileWrite.SideEffectClass = toolcontract.ToolSideEffectWorkspaceWrite
	fileWrite.Completion = toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation}
	fileWrite.OutputSchema = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
	fileWrite.ResultContract.Schema = fileWrite.OutputSchema
	fileWrite.ResultContract.Effects = []toolcontract.ResourceEffectContract{{
		ObjectType:     "file",
		Effect:         "created",
		ResultField:    "path",
		EffectIdentity: "path",
	}}
	fileDeliver := testToolDescriptor(toolcontract.FileDeliverToolName)
	fileDeliver.SideEffectClass = toolcontract.ToolSideEffectExternalWrite
	fileDeliver.Completion = toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation}
	fileDeliver.InputIntentSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	fileDeliver.OutputSchema = json.RawMessage(`{"type":"object","properties":{"deliveredPaths":{"type":"array","items":{"type":"string"}}},"required":["deliveredPaths"],"additionalProperties":false}`)
	fileDeliver.ResultContract.Schema = fileDeliver.OutputSchema
	fileDeliver.ResultContract.Effects = []toolcontract.ResourceEffectContract{{
		ObjectType:     "file",
		Effect:         "attached",
		ResultField:    "deliveredPaths",
		EffectIdentity: "path",
	}}
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{fileWrite, fileDeliver})
	if !toolProducesIntermediateAttachmentSource(toolSet, toolcontract.FileWriteToolName) {
		t.Fatal("expected file_write descriptor to represent an intermediate attachment source")
	}

	contract := outcomeContractForRequest(
		AgentRequest{ToolSet: toolSet},
		IntakeDecision{
			Classification:         IntakeClassificationBoundedTask,
			TaskShape:              TaskShapeMaintenanceTask,
			RequestedOutputFormats: []string{"docx"},
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		[]string{".docx"},
	)
	if !reflect.DeepEqual(contract.RequiredEvidenceTools, []string{toolcontract.FileDeliverToolName}) {
		t.Fatalf("expected delivery-only attachment evidence, got %v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractPreservesActiveGoalEvidence(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site_unserve"},
		}}},
		IntakeDecision{
			Classification: IntakeClassificationBoundedTask,
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if !stringSliceContains(contract.RequiredEvidenceTools, "site_unserve") || stringSliceContains(contract.RequiredEvidenceTools, "file_delete") {
		t.Fatalf("expected active goal evidence to remain authoritative, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractDoesNotFallbackToScheduleCreateForScheduledTaskShape(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "send this message tomorrow at 3pm",
			ToolSet: newTestToolSet([]string{"schedule_create"}),
		},
		IntakeDecision{
			Classification: IntakeClassificationBoundedTask,
			TaskShape:      TaskShapeScheduledTask,
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if stringSliceContains(contract.RequiredEvidenceTools, "schedule_create") {
		t.Fatalf("expected scheduled task shape not to create fallback evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractCreatesExpectedResultsForSitePublish(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "build and deploy a personal homepage"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
		InstructionBundle{},
		ExecutionPlan{PublicDeploy: true},
		true,
		nil,
	)

	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected site publish contract to require public link result, got %+v", contract.ExpectedResults)
	}
	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeMessage, "final reply") {
		t.Fatalf("expected site publish contract to include final message result, got %+v", contract.ExpectedResults)
	}
}

func TestOutcomeContractDoesNotRequirePublicLinkForSiteDelete(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "delete the test website that was just deployed",
			ToolSet: newTestToolSet([]string{"site_unserve"}),
		},
		IntakeDecision{
			Classification: IntakeClassificationBoundedTask,
			TaskShape:      TaskShapeMaintenanceTask,
		},
		InstructionBundle{RequiredEvidenceTools: []string{"site_unserve"}},
		ExecutionPlan{},
		false,
		nil,
	)

	if expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected site delete not to require public link result, got %+v", contract.ExpectedResults)
	}
	if !evidenceAnyOfContainsTool(contract.RequiredEvidenceAnyOf, "site_unserve") {
		t.Fatalf("expected site_unserve evidence to be derived from the working set, got %+v", contract.RequiredEvidenceAnyOf)
	}
}

func TestOutcomeContractRequiresCurrentEffectsForSiteModification(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "the tangerine website looks far too rough, make it prettier.",
			ToolSet: newTestToolSet([]string{"site_list", "file_edit", "site_serve"}),
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if len(contract.RequiredEffects) != 0 {
		t.Fatalf("expected no text-derived site effects without explicit contract, got %+v", contract.RequiredEffects)
	}
}

func TestOutcomeContractCreatesExpectedResultsForRequestedFile(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "pptx make the file"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, RequestedOutputFormats: []string{".pptx"}},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		[]string{".pptx"},
	)

	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeFile, "file in the requested format") {
		t.Fatalf("expected file output contract, got %+v", contract.ExpectedResults)
	}
	if len(contract.ExpectedResults[0].AcceptanceHints) == 0 || contract.ExpectedResults[0].AcceptanceHints[0] != ".pptx" {
		t.Fatalf("expected suffix hint to be preserved, got %+v", contract.ExpectedResults)
	}
}

func TestOutcomeContractKeepsRequestedFileWhenSiteSkillOnlySelected(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "site-prototype"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
		RequiredEvidenceTools: []string{"site_list", "site_serve"},
	}
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt: "make the corporate document guide as a docx",
			ToolSet: newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
				{Name: "site_list", Namespace: "site", SideEffectClass: toolcontract.ToolSideEffectRead},
				{Name: "site_serve", Namespace: "site", SideEffectClass: toolcontract.ToolSideEffectExternalPublish},
			}),
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, RequestedOutputFormats: []string{".docx"}},
		instructionBundle,
		ExecutionPlan{},
		false,
		[]string{".docx"},
	)

	if len(contract.RequiredAttachmentSuffixes) != 1 || contract.RequiredAttachmentSuffixes[0] != ".docx" {
		t.Fatalf("expected selected site skill not to clear requested file suffix, got %+v", contract.RequiredAttachmentSuffixes)
	}
	if !stringSliceContains(contract.RequiredEvidenceTools, "file_deliver") {
		t.Fatalf("expected file_deliver requirement for requested file, got %+v", contract.RequiredEvidenceTools)
	}
	if stringSliceContains(contract.RequiredEvidenceTools, "site_serve") {
		t.Fatalf("expected selected site skill not to require site publish, got %+v", contract.RequiredEvidenceTools)
	}
	if stringSliceContains(contract.SelectedEvidenceHints, "site_serve") {
		t.Fatalf("expected selected site skill not to keep stale site hint, got %+v", contract.SelectedEvidenceHints)
	}
	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeFile, "file in the requested format") {
		t.Fatalf("expected file result for requested attachment, got %+v", contract.ExpectedResults)
	}
	if expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected selected site skill not to require public link, got %+v", contract.ExpectedResults)
	}
}

func TestResolvedInputDischargesAskInputContract(t *testing.T) {
	request := AgentRequest{
		ExistingTaskRunID: "task-1",
		ActiveGoal: ActiveGoal{
			TaskRunID: "task-1",
			Status:    ActiveGoalStatusWaitingUserInput,
		},
	}
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{toolcontract.AskInputToolName, "task_update"},
		RequiredEvidenceAnyOf: [][]string{{toolcontract.AskInputToolName, "task_update"}},
		SelectedEvidenceHints: []string{toolcontract.AskInputToolName},
		ExpectedResults: []ExpectedResult{{
			ID:              "choice",
			Type:            ExpectedResultTypeMessage,
			Description:     "user choice",
			Required:        true,
			AcceptanceHints: []string{toolcontract.AskInputToolName},
		}, {
			ID:              "choice-update",
			Type:            ExpectedResultTypeMessage,
			Description:     "user choice applied",
			Required:        true,
			AcceptanceHints: []string{toolcontract.AskInputToolName, "task_update"},
		}},
	}

	resolvedContract := dischargeResolvedInputContract(request, TurnDecision{Route: TurnRouteContinueTask}, contract)

	if stringSliceContains(resolvedContract.RequiredEvidenceTools, toolcontract.AskInputToolName) ||
		stringSliceContains(resolvedContract.SelectedEvidenceHints, toolcontract.AskInputToolName) {
		t.Fatalf("expected resolved ask_input requirements to be discharged, got %+v", resolvedContract)
	}
	if len(resolvedContract.ExpectedResults) != 1 ||
		len(resolvedContract.ExpectedResults[0].AcceptanceHints) != 1 ||
		resolvedContract.ExpectedResults[0].AcceptanceHints[0] != "task_update" {
		t.Fatalf("expected mixed expected result to retain its remaining hint, got %+v", resolvedContract.ExpectedResults)
	}
	if len(resolvedContract.RequiredEvidenceAnyOf) != 1 || !stringSliceContains(resolvedContract.RequiredEvidenceAnyOf[0], "task_update") {
		t.Fatalf("expected unrelated evidence alternative to remain, got %+v", resolvedContract.RequiredEvidenceAnyOf)
	}
}

func TestUnresolvedInputKeepsAskInputContract(t *testing.T) {
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{toolcontract.AskInputToolName},
		ExpectedResults: []ExpectedResult{{
			ID:              "choice",
			Type:            ExpectedResultTypeMessage,
			Description:     "user choice",
			Required:        true,
			AcceptanceHints: []string{toolcontract.AskInputToolName},
		}},
	}
	request := AgentRequest{
		ExistingTaskRunID: "task-1",
		ActiveGoal: ActiveGoal{
			TaskRunID: "task-1",
			Status:    ActiveGoalStatusWaitingUserInput,
		},
	}

	for _, turnDecision := range []TurnDecision{{Route: TurnRouteStartTask}, {Route: TurnRouteAnswerQuestion}} {
		unresolvedContract := dischargeResolvedInputContract(request, turnDecision, contract)
		if !stringSliceContains(unresolvedContract.RequiredEvidenceTools, toolcontract.AskInputToolName) || len(unresolvedContract.ExpectedResults) != 1 {
			t.Fatalf("expected route %s to preserve ask_input contract, got %+v", turnDecision.Route, unresolvedContract)
		}
	}
	request.ExistingTaskRunID = "task-2"
	mismatchedContract := dischargeResolvedInputContract(request, TurnDecision{Route: TurnRouteContinueTask}, contract)
	if !stringSliceContains(mismatchedContract.RequiredEvidenceTools, toolcontract.AskInputToolName) {
		t.Fatal("expected another task's continuation not to discharge ask_input")
	}
}

func TestOutcomeContractDoesNotTreatReplyInstructionAsExternalSend(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{Name: "direct-message"}, {Name: "site-prototype"}},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "direct-message", Status: "selected"},
			{Name: "site-prototype", Status: "selected"},
		},
		RequiredEvidenceTools: []string{"message_send", "site_list", "site_serve"},
	}
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "message_send", Namespace: "message", SideEffectClass: toolcontract.ToolSideEffectExternalSend},
		{Name: "site_list", Namespace: "site", SideEffectClass: toolcontract.ToolSideEffectRead},
		{Name: "site_serve", Namespace: "site", SideEffectClass: toolcontract.ToolSideEffectExternalPublish},
	})

	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "build and deploy a personal homepage and just give me the URL", ToolSet: toolSet},
		IntakeDecision{Classification: IntakeClassificationBoundedTask},
		instructionBundle,
		ExecutionPlan{PublicDeploy: true},
		true,
		nil,
	)

	if stringSliceContains(contract.RequiredEvidenceTools, "message_send") {
		t.Fatalf("expected reply instruction not to require external send evidence, got %+v", contract.RequiredEvidenceTools)
	}
	if !stringSliceContains(contract.RequiredEvidenceTools, "site_serve") {
		t.Fatalf("expected site publish evidence to remain required, got %+v", contract.RequiredEvidenceTools)
	}
}

func expectedResultsContain(results []ExpectedResult, resultType string, descriptionFragment string) bool {
	for _, result := range results {
		if result.Type == resultType && strings.Contains(result.Description, descriptionFragment) {
			return true
		}
	}
	return false
}

func TestOutcomeContractIgnoresSelectedDirectMessageForNonSendGoal(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message_send"},
	}
	intakeDecision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeResearchTask}

	contract := outcomeContractForRequest(AgentRequest{Prompt: "https://example.com use it to write the business plan"}, intakeDecision, instructionBundle, ExecutionPlan{}, false, nil)

	if len(contract.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected no DM hard gate for non-send goal, got %+v", contract.RequiredEvidenceTools)
	}
	if len(contract.SelectedEvidenceHints) != 1 || contract.SelectedEvidenceHints[0] != "message_send" {
		t.Fatalf("expected selected evidence hint to be retained, got %+v", contract.SelectedEvidenceHints)
	}
}

func TestOutcomeContractDoesNotPromoteDirectMessageHintForAttachmentFollowUp(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message_send"},
	}
	request := AgentRequest{
		Prompt: "let's try again",
		VisibleContext: VisibleContext{
			Materials: []VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Path:        "home/inbox/mattermost/direct/post/kim-intern-automation.html",
				ContentType: "text/html",
			}},
		},
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"message_send"},
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	if stringSliceContains(contract.RequiredEvidenceTools, "message_send") {
		t.Fatalf("expected attachment follow-up not to require DM send evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractIgnoresMailKeywordForArtifactAttachmentGoal(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message_send"},
	}
	intakeDecision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeResearchTask}

	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "attach a five-slide PPTX introducing the mail, calendar, and browser control features"},
		intakeDecision,
		instructionBundle,
		ExecutionPlan{},
		false,
		[]string{".pptx"},
	)

	if stringSliceContains(contract.RequiredEvidenceTools, "message_send") {
		t.Fatalf("expected artifact attachment request not to require DM send evidence, got %+v", contract.RequiredEvidenceTools)
	}
	if !stringSliceContains(contract.RequiredEvidenceTools, "file_deliver") {
		t.Fatalf("expected artifact attachment request to require file_deliver, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractRequiresSendEvidenceForExternalSendPlan(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message_send"},
	}
	intakeDecision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}

	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{{
		Name:            "message_send",
		Namespace:       "message",
		SideEffectClass: toolcontract.ToolSideEffectExternalSend,
	}})
	contract := outcomeContractForRequest(AgentRequest{Prompt: "send Dana a DM saying test", ToolSet: toolSet}, intakeDecision, instructionBundle, ExecutionPlan{ExternalSend: true, ThirdPartyExternalSend: true}, true, nil)

	if len(contract.RequiredEvidenceTools) != 1 || contract.RequiredEvidenceTools[0] != "message_send" {
		t.Fatalf("expected send hard gate for external send goal, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractIgnoresIntakeSendEvidenceForCurrentConversationReply(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{{
		Name:            "message_send",
		Namespace:       "message",
		SideEffectClass: toolcontract.ToolSideEffectExternalSend,
	}})
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "hi, answer with a short greeting.",
			ToolSet: toolSet,
		},
		IntakeDecision{
			Classification: IntakeClassificationBoundedTask,
			TaskShape:      TaskShapeMaintenanceTask,
		},
		InstructionBundle{},
		ExecutionPlan{},
		true,
		nil,
	)

	if stringSliceContains(contract.RequiredEvidenceTools, "message_send") {
		t.Fatalf("expected current-conversation reply not to require message_send, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractKeepsSendEvidenceForExternalSendContinuation(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt: "do it",
			ActiveGoal: ActiveGoal{
				OriginalInstruction: "send Dana a DM saying test",
				OutcomeContract: OutcomeContract{
					RequiredEvidenceTools: []string{"message_send"},
				},
			},
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if !stringSliceContains(contract.RequiredEvidenceTools, "message_send") {
		t.Fatalf("expected external send continuation to keep message_send, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractDoesNotDeriveEvidenceFromPromptAndAvailableTools(t *testing.T) {
	tests := []struct {
		name      string
		prompt    string
		toolNames []string
	}{
		{
			name:      "flow task",
			prompt:    "register the task",
			toolNames: []string{"task_add"},
		},
		{
			name:      "external send",
			prompt:    "send Dana a DM saying test",
			toolNames: []string{"message_send"},
		},
	}

	for _, test := range tests {
		contract := outcomeContractForRequest(
			AgentRequest{
				Prompt:  test.prompt,
				ToolSet: testToolSet(test.toolNames),
			},
			IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
			InstructionBundle{},
			ExecutionPlan{},
			false,
			nil,
		)
		if len(contract.RequiredEvidenceTools) != 0 {
			t.Fatalf("expected %s prompt and available tools not to derive evidence, got %+v", test.name, contract.RequiredEvidenceTools)
		}
	}
}

func TestOutcomeContractDemotesIntakeInitialToolsToEvidenceHints(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "memory_remember", Namespace: "memory", SideEffectClass: toolcontract.ToolSideEffectStateChange},
	})
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "remember what was said in this conversation",
			ToolSet: toolSet,
		},
		IntakeDecision{
			Classification:   IntakeClassificationBoundedTask,
			TaskShape:        TaskShapeMaintenanceTask,
			InitialToolNames: []string{"memory_remember"},
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if len(contract.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected intake initial tools not to become required evidence, got %+v", contract.RequiredEvidenceTools)
	}
	if len(contract.RequiredEvidenceAnyOf) != 0 {
		t.Fatalf("expected intake initial tools not to become required any-of evidence, got %+v", contract.RequiredEvidenceAnyOf)
	}
	if !stringSliceContains(contract.SelectedEvidenceHints, "memory_remember") {
		t.Fatalf("expected intake initial tools to be recorded as evidence hints, got %+v", contract.SelectedEvidenceHints)
	}
}

func TestOutcomeContractDerivesSideEffectEvidenceAnyOfGroupForMaintenanceTask(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "task_add", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectStateChange},
		{Name: "task_list", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectRead},
		{Name: "task_update", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectStateChange},
	})
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "internkim-flow"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
		RequiredEvidenceTools: []string{"task_add", "task_list", "task_update"},
	}
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "register one new task",
			ToolSet: toolSet,
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
		instructionBundle,
		ExecutionPlan{},
		false,
		nil,
	)

	if len(contract.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected no AND-required evidence from the working set derivation, got %+v", contract.RequiredEvidenceTools)
	}
	if len(contract.RequiredEvidenceAnyOf) != 1 {
		t.Fatalf("expected exactly one derived any-of group, got %+v", contract.RequiredEvidenceAnyOf)
	}
	if !stringSliceContains(contract.RequiredEvidenceAnyOf[0], "task_add") || !stringSliceContains(contract.RequiredEvidenceAnyOf[0], "task_update") {
		t.Fatalf("expected the side-effect working set tools in the derived group, got %+v", contract.RequiredEvidenceAnyOf[0])
	}
	if !stringSliceContains(contract.RequiredEvidenceAnyOf[0], "task_list") {
		t.Fatalf("expected the read tool to stay satisfiable so verification asks can finish on read evidence, got %+v", contract.RequiredEvidenceAnyOf[0])
	}
}

func TestOutcomeReferenceToolSetHidesSendAndSiteToolsForDocumentGoal(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "web_fetch", Namespace: "web", SideEffectClass: toolcontract.ToolSideEffectRead},
		{Name: "file_write", Namespace: "file", SideEffectClass: toolcontract.ToolSideEffectWorkspaceWrite},
		{Name: "file_deliver", Namespace: "file", SideEffectClass: toolcontract.ToolSideEffectExternalWrite},
		{Name: "site_serve", Namespace: "site", SideEffectClass: toolcontract.ToolSideEffectExternalWrite},
		{Name: "site_serve", Namespace: "site", SideEffectClass: toolcontract.ToolSideEffectExternalPublish},
		{Name: "message_send", Namespace: "message", SideEffectClass: toolcontract.ToolSideEffectExternalSend},
		{Name: "mail_message_send", Namespace: "mail", SideEffectClass: toolcontract.ToolSideEffectExternalSend},
	})
	contract := OutcomeContract{
		SelectedEvidenceHints: []string{"site_serve", "site_serve", "message_send", "mail_message_send"},
	}

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "https://example.com use it to write the business plan"}, ExecutionPlan{}, false, contract)

	for _, toolName := range []string{"web_fetch", "file_write", "file_deliver"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	for _, toolName := range []string{"site_serve", "site_serve", "message_send", "mail_message_send"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to be hidden for document goal, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestAgentTurnToolSetExposesPinnedNonKernelTools(t *testing.T) {
	toolSet := testToolSet([]string{"web_search", "web_fetch", "terminal_run", "file_write"})
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "presentation", ToolReferences: []string{"terminal_run", "file_write"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "presentation", Status: "selected"}},
	}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, AgentRequest{
		Prompt:          "https://example.com use it to make the deck",
		PinnedToolNames: []string{"web_search", "web_fetch", "terminal_run", "file_write"},
	}, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"terminal_run", "file_write", "web_search", "web_fetch"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected pinned tool %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeReferenceToolSetKeepsSiteToolsForSiteGoal(t *testing.T) {
	toolSet := testToolSet([]string{"web_fetch", "site_serve", "site_serve"})

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "build and deploy a website"}, ExecutionPlan{}, false, OutcomeContract{
		RequiredEvidenceTools: []string{"site_serve", "site_serve"},
	})

	for _, toolName := range []string{"site_serve", "site_serve"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available for site goal, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeReferenceToolSetKeepsActiveGoalEvidenceToolsForContinuation(t *testing.T) {
	toolSet := testToolSet([]string{"web_fetch", "terminal_run", "site_serve", "site_serve"})
	request := AgentRequest{
		Prompt: "try again, it should work",
		ActiveGoal: ActiveGoal{OriginalInstruction: "build and deploy a website", OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"site_serve", "terminal_run", "site_serve"},
		}},
	}

	filteredToolSet := toolSetForOutcomeReference(toolSet, request, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"site_serve", "site_serve"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available for active site continuation, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestAgentTurnToolSetHidesSiteToolsForActiveGoalContinuation(t *testing.T) {
	toolSet := testToolSet([]string{"web_fetch", "terminal_run", "site_serve", "site_serve"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           "site-prototype",
			ToolReferences: []string{"terminal_run", "site_serve", "site_serve"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt:          "try again, it should work",
		PinnedToolNames: []string{"terminal_run", "site_serve", "site_serve"},
		ActiveGoal: ActiveGoal{OriginalInstruction: "build and deploy a website", OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"site_serve", "terminal_run", "site_serve"},
		}},
	}
	contract := OutcomeContract{SelectedEvidenceHints: []string{"site_serve", "terminal_run", "site_serve"}}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	// Continuation of an active site goal keeps site.* exposed because it is still pinned.
	for _, toolName := range []string{"terminal_run", "site_serve", "site_serve"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected pinned tool %s to remain available for an active site continuation, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestAgentTurnToolSetHidesSelectedSiteSkillToolsWhenActiveGoalWasAttachmentFallback(t *testing.T) {
	toolSet := testToolSet([]string{"web_fetch", "terminal_run", "file_deliver", "site_serve", "site_serve"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           "site-prototype",
			ToolReferences: []string{"terminal_run", "site_serve", "site_serve"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt:          "try again",
		PinnedToolNames: []string{"terminal_run", "site_serve", "site_serve"},
		ActiveGoal: ActiveGoal{OriginalInstruction: "build and deploy a personal homepage", OutcomeContract: OutcomeContract{
			RequiredEvidenceTools:      []string{"file_deliver"},
			RequiredAttachmentSuffixes: []string{".html"},
			SelectedEvidenceHints:      []string{"site_serve", "terminal_run", "site_serve"},
			ArtifactRequirement:        ArtifactRequirementRequired,
		}},
	}
	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeImmediateReply}, instructionBundle, ExecutionPlan{}, false, nil)

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	// A selected site skill keeps site.* exposed because it is still pinned, alongside the kernel tools.
	for _, toolName := range []string{"terminal_run", "file_deliver", "site_serve", "site_serve"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected pinned tool %s to remain available after selected site skill, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeContractRequiresActiveGoalRequiredEvidenceForContinuation(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "site-prototype"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
		RequiredEvidenceTools: []string{"site_serve", "terminal_run", "site_serve"},
	}
	request := AgentRequest{
		Prompt: "try again, it should work",
		ActiveGoal: ActiveGoal{OriginalInstruction: "build and deploy a website", OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site_serve", "site_serve"},
			SelectedEvidenceHints: []string{"site_serve", "terminal_run", "site_serve"},
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	for _, toolName := range []string{"site_serve", "site_serve"} {
		if !stringSliceContains(contract.RequiredEvidenceTools, toolName) {
			t.Fatalf("expected active site continuation to require %s evidence, got %+v", toolName, contract.RequiredEvidenceTools)
		}
	}
}

func TestOutcomeContractPreservesSiteGoalDuringApprovalContinuation(t *testing.T) {
	request := AgentRequest{
		Prompt:                 "check",
		IsApprovalContinuation: true,
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site_unserve"},
			SelectedEvidenceHints: []string{"site_unserve"},
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, InstructionBundle{}, ExecutionPlan{}, false, nil)

	if !stringSliceContains(contract.RequiredEvidenceTools, "site_unserve") {
		t.Fatalf("expected site_unserve evidence to remain, got %+v", contract)
	}
}

func TestAgentTurnToolSetExposesSendToolForActiveSendContinuation(t *testing.T) {
	toolSet := testToolSet([]string{"message_send", "file_write"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           "direct-message",
			ToolReferences: []string{"message_send"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt:          "do it again",
		PinnedToolNames: []string{"message_send"},
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "send Dana a DM saying test",
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{"message_send"},
				SelectedEvidenceHints: []string{"message_send"},
			},
		},
	}
	contract := OutcomeContract{RequiredEvidenceTools: []string{"message_send"}, SelectedEvidenceHints: []string{"message_send"}}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	if !filteredToolSet.IsAllowed("message_send") {
		t.Fatalf("expected pinned send tool to remain available for continuation, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestAgentTurnToolSetHidesUnrequestedSendToolForAttachmentFollowUp(t *testing.T) {
	toolSet := testToolSet([]string{"message_send", "file_preview", "file_read"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           "direct-message",
			ToolReferences: []string{"message_send"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt:          "let's try again",
		PinnedToolNames: []string{"file_preview"},
		VisibleContext: VisibleContext{
			Materials: []VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Path:        "home/inbox/mattermost/direct/post/kim-intern-automation.html",
				ContentType: "text/html",
			}},
		},
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"message_send"},
		}},
	}
	contract := OutcomeContract{SelectedEvidenceHints: []string{"message_send"}}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	if !filteredToolSet.IsAllowed("message_send") {
		t.Fatalf("expected selected direct send tool to remain available, got %+v", filteredToolSet.ListToolNames())
	}
	if !filteredToolSet.IsAllowed("file_preview") {
		t.Fatalf("expected attachment preview to remain available, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestOutcomeReferenceToolSetKeepsSendToolsForExplicitSendGoal(t *testing.T) {
	toolSet := testToolSet([]string{"web_fetch", "message_send", "mail_message_send"})

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "send Dana a DM"}, ExecutionPlan{}, false, OutcomeContract{RequiredEvidenceTools: []string{"message_send"}})

	if !filteredToolSet.IsAllowed("message_send") {
		t.Fatalf("expected DM send to remain available, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestConfirmationHintsIgnoreUnrelatedSelectedSkillEvidence(t *testing.T) {
	hints := confirmationEvidenceHintsForRequest(
		AgentRequest{Prompt: "https://example.com use it to write the business plan"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeResearchTask},
		[]string{"site_serve", "message_send"},
	)

	if len(hints) != 0 {
		t.Fatalf("expected unrelated skill evidence not to force confirmation planning, got %+v", hints)
	}
}

func TestAttachmentSuffixesComeFromStructuredOutputFormats(t *testing.T) {
	suffixes := attachmentSuffixesForRequestedOutputFormats([]string{"html", "pdf", "html"})

	if len(suffixes) != 2 || suffixes[0] != ".html" || suffixes[1] != ".pdf" {
		t.Fatalf("expected structured output format suffixes, got %+v", suffixes)
	}
}
