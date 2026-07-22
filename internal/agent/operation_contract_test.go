package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
)

type operationContractLanguageModel struct {
	contents     []string
	errorsByCall map[int]error
	requests     []llm.StructuredResponseRequest
	calls        int
}

func (languageModel *operationContractLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *operationContractLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	callIndex := languageModel.calls
	languageModel.calls++
	if errorValue := languageModel.errorsByCall[callIndex]; errorValue != nil {
		return llm.StructuredResponse{}, errorValue
	}
	if callIndex >= len(languageModel.contents) {
		return llm.StructuredResponse{}, context.Canceled
	}
	return llm.StructuredResponse{Content: languageModel.contents[callIndex]}, nil
}

type operationContractCorrectionError struct {
	correction llm.StructuredOutputCorrection
}

func (errorValue operationContractCorrectionError) Error() string {
	return errorValue.correction.Code
}

func (errorValue operationContractCorrectionError) StructuredOutputCorrection() (llm.StructuredOutputCorrection, bool) {
	return errorValue.correction, true
}

func TestCompileOperationRequirementsPreservesPersistedContract(t *testing.T) {
	languageModel := &operationContractLanguageModel{}
	persistedRequirement := OperationRequirement{
		RequirementID: "operation-1",
		ToolID:        "capabilityd:task.add",
		ToolName:      "task.add",
		InputMode:     OperationInputContainsExplicit,
		RequiredInput: json.RawMessage(`{"title":"기존 업무"}`),
	}
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{"task.add"},
		OperationContract: &OperationContract{
			Version:      operationContractVersion,
			Requirements: []OperationRequirement{persistedRequirement},
		},
	}

	compiledContract, errorValue := compileOperationRequirements(context.Background(), languageModel, AgentRequest{}, operationContractTestToolSet(), contract)

	if errorValue != nil {
		t.Fatalf("expected persisted contract to pass: %v", errorValue)
	}
	if languageModel.calls != 0 {
		t.Fatalf("expected persisted contract to remain authoritative, got %d compiler calls", languageModel.calls)
	}
	if string(compiledContract.OperationContract.Requirements[0].RequiredInput) != `{"title":"기존 업무"}` {
		t.Fatalf("expected persisted requirement unchanged, got %+v", compiledContract.OperationContract)
	}
}

func TestCompileOperationRequirementsSkipsModelForEmptyIntents(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{
		{
			ID:                "kernel:terminal.run",
			Name:              TerminalRunToolName,
			InputIntentSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			SideEffectClass:   ToolSideEffectWorkspaceWrite,
		},
		{
			ID:                "kernel:file.deliver",
			Name:              FileDeliverToolName,
			InputIntentSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			SideEffectClass:   ToolSideEffectExternalWrite,
		},
	})
	contract, errorValue := compileOperationRequirements(
		context.Background(),
		nil,
		AgentRequest{},
		toolSet,
		OutcomeContract{RequiredEvidenceTools: []string{TerminalRunToolName, FileDeliverToolName}},
	)
	if errorValue != nil {
		t.Fatalf("expected evidence-only operations without a language model: %v", errorValue)
	}
	if contract.OperationContract != nil {
		t.Fatalf("empty intent tools need exact completion evidence, not an operation input contract: %+v", contract.OperationContract)
	}
	if !slices.Equal(contract.RequiredEvidenceTools, []string{TerminalRunToolName, FileDeliverToolName}) {
		t.Fatalf("expected exact evidence requirements to remain authoritative, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestCompileOperationRequirementsRejectsPersistedContractForEmptyIntents(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{{
		ID:                "kernel:terminal.run",
		Name:              TerminalRunToolName,
		InputIntentSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		SideEffectClass:   ToolSideEffectWorkspaceWrite,
	}})
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{TerminalRunToolName},
		OperationContract: &OperationContract{
			Version: operationContractVersion,
			Requirements: []OperationRequirement{{
				RequirementID: "operation-1",
				ToolID:        "kernel:terminal.run",
				ToolName:      TerminalRunToolName,
				InputMode:     OperationInputContainsExplicit,
				RequiredInput: json.RawMessage(`{}`),
			}},
		},
	}

	_, errorValue := compileOperationRequirements(context.Background(), nil, AgentRequest{}, toolSet, contract)

	if errorValue == nil || errorValue.Error() != "operation contract has no bindable required operation" {
		t.Fatalf("expected stale empty-intent contract rejection, got %v", errorValue)
	}
}

func TestCompileOperationRequirementsAsksModelOnlyForBindableIntents(t *testing.T) {
	languageModel := &operationContractLanguageModel{contents: []string{
		`{"operations":[{"toolName":"task.add","requiredValues":{"title":"분기 결산"}}]}`,
	}}
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{
		{
			ID:                "kernel:terminal.run",
			Name:              TerminalRunToolName,
			InputIntentSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			SideEffectClass:   ToolSideEffectWorkspaceWrite,
		},
		{
			ID:                "capabilityd:task.add",
			Name:              "task.add",
			InputIntentSchema: operationContractTaskInputIntentSchema(),
			SideEffectClass:   ToolSideEffectStateChange,
		},
	})
	contract, errorValue := compileOperationRequirements(
		context.Background(),
		languageModel,
		AgentRequest{Prompt: "분기 결산 업무를 추가하고 필요한 작업을 해줘"},
		toolSet,
		OutcomeContract{RequiredEvidenceTools: []string{TerminalRunToolName, "task.add"}},
	)
	if errorValue != nil {
		t.Fatalf("expected mixed static and model-bound requirements: %v", errorValue)
	}
	if languageModel.calls != 1 {
		t.Fatalf("expected one model call for the bindable intent, got %d", languageModel.calls)
	}
	modelRequest := joinedMessageContent(languageModel.requests[0].Messages)
	if strings.Contains(modelRequest, `"name":"terminal.run"`) || !strings.Contains(modelRequest, `"name":"task.add"`) {
		t.Fatalf("expected only bindable descriptors in the model request, got %s", modelRequest)
	}
	if len(contract.OperationContract.Requirements) != 1 ||
		contract.OperationContract.Requirements[0].ToolName != "task.add" {
		t.Fatalf("expected only the bindable operation requirement, got %+v", contract.OperationContract)
	}
}

func TestCompileOperationRequirementsDoesNotPromoteExecutionHintsToEvidence(t *testing.T) {
	languageModel := &operationContractLanguageModel{}
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{
		{
			ID:                "kernel:file.read",
			Name:              FileReadToolName,
			InputIntentSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			SideEffectClass:   ToolSideEffectRead,
		},
		{
			ID:                "kernel:file.write",
			Name:              FileWriteToolName,
			InputIntentSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"additionalProperties":false}`),
			SideEffectClass:   ToolSideEffectWorkspaceWrite,
		},
		{
			ID:                "kernel:file.deliver",
			Name:              FileDeliverToolName,
			InputIntentSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			SideEffectClass:   ToolSideEffectExternalWrite,
		},
	})
	contract, errorValue := compileOperationRequirements(
		context.Background(),
		languageModel,
		AgentRequest{
			Prompt:          "FAQ JSON 파일을 만들어서 전달해줘",
			PinnedToolNames: []string{FileReadToolName, FileWriteToolName, FileDeliverToolName},
		},
		toolSet,
		OutcomeContract{RequiredEvidenceTools: []string{FileDeliverToolName}},
	)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !slices.Equal(contract.RequiredEvidenceTools, []string{FileDeliverToolName}) {
		t.Fatalf("expected delivery-only completion evidence, got %v", contract.RequiredEvidenceTools)
	}
	if contract.OperationContract != nil {
		t.Fatalf("expected execution hints to stay out of the completion contract, got %+v", contract.OperationContract)
	}
	if languageModel.calls != 0 {
		t.Fatalf("expected execution hints not to invoke the operation compiler, got %d calls", languageModel.calls)
	}
}

func TestCompileOperationRequirementsUsesPersistedOperationContractOnContinuation(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{{
		ID:                "kernel:file.write",
		Name:              FileWriteToolName,
		InputIntentSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"additionalProperties":false}`),
		SideEffectClass:   ToolSideEffectWorkspaceWrite,
	}})
	operationContract := &OperationContract{
		Version: operationContractVersion,
		Requirements: []OperationRequirement{{
			RequirementID: "operation-1",
			ToolID:        "kernel:file.write",
			ToolName:      FileWriteToolName,
			InputMode:     OperationInputContainsExplicit,
			RequiredInput: json.RawMessage(`{"path":"FAQ.json","content":"{}"}`),
		}},
	}

	contract, errorValue := compileOperationRequirements(
		context.Background(),
		nil,
		AgentRequest{},
		toolSet,
		OutcomeContract{
			RequiredEvidenceTools: []string{FileDeliverToolName},
			OperationContract:     operationContract,
		},
	)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if contract.OperationContract != operationContract {
		t.Fatalf("expected persisted operation contract, got %+v", contract.OperationContract)
	}
}


func TestCompileOperationRequirementsIncludesDirectlyReferencedVisibleContext(t *testing.T) {
	languageModel := &operationContractLanguageModel{contents: []string{
		`{"operations":[{"toolName":"task.add","requiredValues":{}}]}`,
		`{"isComplete":true,"reason":""}`,
	}}
	request := AgentRequest{
		Prompt: "그거 추가해줘",
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{{
			Speaker: "Lee",
			Text:    "고객지원 분기 결산 누락 확인",
		}}},
	}

	_, errorValue := compileOperationRequirements(context.Background(), languageModel, request, operationContractTestToolSet(), OutcomeContract{RequiredEvidenceTools: []string{"task.add"}})

	if errorValue != nil {
		t.Fatalf("expected visible context compilation to pass: %v", errorValue)
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "고객지원 분기 결산 누락 확인") {
		t.Fatalf("expected visible context in compiler request, got %+v", languageModel.requests[0].Messages)
	}
	if strings.Contains(joinedMessageContent(languageModel.requests[0].Messages), "policyResource") {
		t.Fatal("expected compiler descriptors to omit unrelated policy metadata")
	}
}

func TestCompileOperationRequirementsNormalizesTemporalValueWithoutDefaults(t *testing.T) {
	languageModel := &operationContractLanguageModel{contents: []string{
		`{"operations":[{"toolName":"task.add","requiredValues":{"title":"분기 결산 누락 확인","endDate":"2026-07-24"}}]}`,
		`{"isComplete":true,"reason":""}`,
	}}
	toolSet := operationContractTestToolSet()
	request := AgentRequest{
		Prompt:        "분기 결산 누락 확인 업무를 다가오는 금요일까지 추가해줘",
		TurnStartedAt: time.Date(2026, time.July, 18, 10, 0, 0, 0, time.FixedZone("KST", 9*60*60)),
	}
	contract := OutcomeContract{RequiredEvidenceTools: []string{"task.add"}}

	compiledContract, errorValue := compileOperationRequirements(context.Background(), languageModel, request, toolSet, contract)

	if errorValue != nil {
		t.Fatalf("expected operation contract compilation to pass: %v", errorValue)
	}
	if len(compiledContract.OperationContract.Requirements) != 1 {
		t.Fatalf("expected one operation requirement, got %+v", compiledContract.OperationContract)
	}
	requiredInput := string(compiledContract.OperationContract.Requirements[0].RequiredInput)
	if !strings.Contains(requiredInput, `"endDate":"2026-07-24"`) {
		t.Fatalf("expected resolved Friday, got %s", requiredInput)
	}
	if strings.Contains(requiredInput, "goal") || strings.Contains(requiredInput, "size") {
		t.Fatalf("expected no inferred defaults, got %s", requiredInput)
	}
	if !strings.Contains(languageModel.requests[0].Messages[1].Content, "Current weekday: Saturday") {
		t.Fatalf("expected temporal context in compiler request, got %+v", languageModel.requests[0].Messages)
	}
}

func TestValidateRequiredOperationInputFailsClosed(t *testing.T) {
	inputSchema := operationContractTaskInputSchema()
	testCases := []struct {
		name     string
		document string
	}{
		{name: "unknown property", document: `{"query":"분기 결산"}`},
		{name: "wrong type", document: `{"title":3}`},
		{name: "unknown enum", document: `{"size":"XL"}`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, errorValue := validateRequiredOperationInput(json.RawMessage(testCase.document), inputSchema); errorValue == nil {
				t.Fatalf("expected %s to fail", testCase.name)
			}
		})
	}
}

func TestValidateRequiredOperationInputAllowsExplicitPartialInput(t *testing.T) {
	requiredInput, errorValue := validateRequiredOperationInput(json.RawMessage(`{"title":"분기 결산 누락 확인"}`), operationContractTaskInputIntentSchema())

	if errorValue != nil {
		t.Fatalf("expected partial input to pass: %v", errorValue)
	}
	if string(requiredInput) != `{"title":"분기 결산 누락 확인"}` {
		t.Fatalf("unexpected normalized input %s", requiredInput)
	}
}

func TestParseOperationRequirementsRejectsJSONEncodedRequiredValues(t *testing.T) {
	_, errorValue := parseOperationRequirements(
		`{"operations":[{"toolName":"task.add","requiredValues":"{\"title\":\"분기 결산 누락 확인\"}"}]}`,
		operationContractTestToolSet(),
		[]string{"task.add"},
	)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "required values must be a JSON object") {
		t.Fatalf("expected JSON-inside-string input to fail closed, got %v", errorValue)
	}
}

func TestOperationContractSchemaUsesDescriptorInputIntentSchema(t *testing.T) {
	schemaDocument := operationContractSchema([]operationDescriptorDocument{
		operationContractSchemaTestDescriptor("task.add", json.RawMessage(`{
			"type":"object",
			"additionalProperties":false,
			"properties":{"title":{"type":"string"},"size":{"type":"string","enum":["S","M"]}}
		}`)),
	})

	if strings.Contains(schemaDocument, "requiredValuesJSON") {
		t.Fatalf("expected no JSON-inside-string field, got %s", schemaDocument)
	}
	if !strings.Contains(schemaDocument, `"requiredValues":{`) ||
		!strings.Contains(schemaDocument, `"additionalProperties":false`) ||
		!strings.Contains(schemaDocument, `"size":`) ||
		!strings.Contains(schemaDocument, `"enum":["S","M"]`) ||
		!strings.Contains(schemaDocument, `"title":{"type":"string"}`) {
		t.Fatalf("expected exact partial descriptor schema, got %s", schemaDocument)
	}
}

func TestOperationContractSchemaValidatesToolSpecificValuesAndRepeatedOperations(t *testing.T) {
	schemaDocument := operationContractSchema([]operationDescriptorDocument{
		operationContractSchemaTestDescriptor("task.add", json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"title":{"type":"string"},"size":{"type":"string","enum":["S","M"]}}}`)),
		operationContractSchemaTestDescriptor("calendar.add", json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"startTime":{"type":"string"},"attendees":{"type":"array","items":{"type":"string"}}}}`)),
	})
	schema, errorValue := decodeOperationInputSchema(json.RawMessage(schemaDocument))
	if errorValue != nil {
		t.Fatalf("expected generated schema to decode: %v", errorValue)
	}
	resolvedSchema, errorValue := schema.Resolve(nil)
	if errorValue != nil {
		t.Fatalf("expected generated schema to resolve: %v", errorValue)
	}

	validDocument := map[string]any{"operations": []any{
		map[string]any{"toolName": "task.add", "requiredValues": map[string]any{"title": "첫 번째 업무"}},
		map[string]any{"toolName": "task.add", "requiredValues": map[string]any{"size": "M"}},
		map[string]any{"toolName": "calendar.add", "requiredValues": map[string]any{"startTime": "2026-07-24T09:00:00+09:00"}},
	}}
	if errorValue := resolvedSchema.Validate(validDocument); errorValue != nil {
		t.Fatalf("expected repeated typed operations to pass: %v", errorValue)
	}

	unknownToolDocument := map[string]any{"operations": []any{
		map[string]any{"toolName": "message.send", "requiredValues": map[string]any{}},
	}}
	if errorValue := resolvedSchema.Validate(unknownToolDocument); errorValue == nil {
		t.Fatalf("expected an unknown tool name to fail schema validation: %+v", unknownToolDocument)
	}
	if strings.Contains(operationContractSchema([]operationDescriptorDocument{
		operationContractSchemaTestDescriptor("task.add", json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"additionalProperties":false}`)),
		operationContractSchemaTestDescriptor("calendar.add", json.RawMessage(`{"type":"object","properties":{"startTime":{"type":"string"}},"additionalProperties":false}`)),
	}), "oneOf") {
		t.Fatal("expected the multi-descriptor operation schema to stay portable without oneOf")
	}
}

func operationContractSchemaTestDescriptor(name string, inputIntentSchema json.RawMessage) operationDescriptorDocument {
	return operationDescriptorDocument{
		Name:              name,
		InputIntentSchema: inputIntentSchema,
	}
}

func TestOperationContractSeparatesDescriptorValidityFromInvocationCompleteness(t *testing.T) {
	inputSchema := json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["eventHint"],
		"minProperties":2,
		"properties":{
			"eventHint":{"type":"string"},
			"title":{"type":"string"}
		}
	}`)
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{{
		ID:                "capabilityd:calendar.update",
		Name:              "calendar.update",
		InputSchema:       inputSchema,
		InputIntentSchema: operationContractCalendarUpdateIntentSchema(),
		OutputSchema:      json.RawMessage(`{"type":"object","properties":{}}`),
		SideEffectClass:   ToolSideEffectStateChange,
	}})

	if _, errorValue := operationDescriptorDocuments(toolSet, []string{"calendar.update"}); errorValue != nil {
		t.Fatalf("expected descriptor schema to resolve without a fake invocation: %v", errorValue)
	}
	for _, requiredValues := range []string{`{}`, `{"eventHint":"event-1"}`, `{"title":"변경"}`} {
		if _, errorValue := validateRequiredOperationInput(json.RawMessage(requiredValues), operationContractCalendarUpdateIntentSchema()); errorValue != nil {
			t.Fatalf("expected partial explicit values %s to pass: %v", requiredValues, errorValue)
		}
	}
	if _, errorValue := validateRequiredOperationInput(json.RawMessage(`{"query":"변경"}`), operationContractCalendarUpdateIntentSchema()); errorValue == nil {
		t.Fatal("expected unknown partial value to fail")
	}
}

func TestValidateRequiredOperationInputValidatesNestedObjectsAndArrays(t *testing.T) {
	inputIntentSchema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"updates":{
				"type":"object",
				"additionalProperties":false,
				"properties":{"status":{"type":"string","enum":["open","done"]}}
			},
			"labels":{"type":"array","items":{"type":"string"}},
			"owner":{"type":"string"}
		}
	}`)
	validDocument := `{"updates":{"status":"done"},"labels":["quarterly"],"owner":"Lee"}`
	if _, errorValue := validateRequiredOperationInput(json.RawMessage(validDocument), inputIntentSchema); errorValue != nil {
		t.Fatalf("expected nested input to pass: %v", errorValue)
	}
	for _, invalidDocument := range []string{
		`{"updates":{"status":"closed"}}`,
		`{"updates":{"unexpected":"done"}}`,
		`{"labels":[3]}`,
		`{"owner":false}`,
	} {
		if _, errorValue := validateRequiredOperationInput(json.RawMessage(invalidDocument), inputIntentSchema); errorValue == nil {
			t.Fatalf("expected nested input to fail: %s", invalidDocument)
		}
	}
}

func TestValidateRequiredOperationInputAllowsPartialNestedArrayObjects(t *testing.T) {
	inputIntentSchema := json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"properties":{
			"edits":{
				"type":"array",
				"minItems":1,
				"items":{
					"type":"object",
					"additionalProperties":false,
					"properties":{
						"path":{"type":"string"},
						"oldText":{"type":"string"},
						"newText":{"type":"string"}
					}
				}
			}
		}
	}`)

	requiredInput, errorValue := validateRequiredOperationInput(
		json.RawMessage(`{"edits":[{"newText":"완료"}]}`),
		inputIntentSchema,
	)

	if errorValue != nil {
		t.Fatalf("expected explicitly requested nested value to pass: %v", errorValue)
	}
	if string(requiredInput) != `{"edits":[{"newText":"완료"}]}` {
		t.Fatalf("unexpected normalized nested input %s", requiredInput)
	}
	for _, invalidInput := range []json.RawMessage{
		json.RawMessage(`{"edits":[]}`),
		json.RawMessage(`{"edits":[{"newText":3}]}`),
		json.RawMessage(`{"edits":[{"unknown":"완료"}]}`),
	} {
		if _, errorValue := validateRequiredOperationInput(invalidInput, inputIntentSchema); errorValue == nil {
			t.Fatalf("expected invalid nested input to fail: %s", invalidInput)
		}
	}
}

func TestInputIntentSchemaSupportsCanonicalSiteContent(t *testing.T) {
	inputIntentSchema := json.RawMessage(`{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"type":"object",
		"additionalProperties":false,
		"properties":{
			"slug":{"type":"string","minLength":1,"pattern":"^[a-z0-9]+(?:-[a-z0-9]+)*$"},
			"content":{
				"type":"object",
				"additionalProperties":false,
				"properties":{
					"siteName":{"type":"string"},
					"sections":{
						"type":"array",
						"minItems":1,
						"items":{
							"type":"object",
							"additionalProperties":false,
							"properties":{"title":{"type":"string"},"body":{"type":"string"}}
						}
					}
				}
			}
		}
	}`)

	schema, errorValue := decodeOperationInputSchema(inputIntentSchema)
	if errorValue != nil {
		t.Fatalf("decode site input intent schema: %v", errorValue)
	}
	resolvedSchema, errorValue := schema.Resolve(nil)
	if errorValue != nil {
		t.Fatalf("resolve projected site schema: %v", errorValue)
	}
	partialContent := map[string]any{
		"content": map[string]any{
			"sections": []any{map[string]any{"body": "고객지원 운영 현황"}},
		},
	}
	if errorValue := resolvedSchema.Validate(partialContent); errorValue != nil {
		t.Fatalf("expected partial nested site content to pass: %v", errorValue)
	}
}

func TestOperationDescriptorDocumentsRejectMissingOrMalformedInputIntentSchema(t *testing.T) {
	for _, inputIntentSchema := range []json.RawMessage{
		nil,
		json.RawMessage(`{"type":"string"}`),
		json.RawMessage(`{"type":"object","properties":{"title":{"$ref":"#/$defs/missing"}}}`),
	} {
		toolSet := NewToolSet([]string{"task.add"})
		toolSet.RegisterBoundTool(BoundTool{Definition: ToolDefinition{
			ID:                "capabilityd:task.add",
			Name:              "task.add",
			InputSchema:       operationContractTaskInputSchema(),
			InputIntentSchema: inputIntentSchema,
			OutputSchema:      json.RawMessage(`{"type":"object","properties":{}}`),
			SideEffectClass:   ToolSideEffectStateChange,
		}})

		_, errorValue := operationDescriptorDocuments(toolSet, []string{"task.add"})

		if errorValue == nil {
			t.Fatalf("expected invalid input intent schema to fail before generation: %s", inputIntentSchema)
		}
	}
}

func TestNormalizeOperationContractPreservesRepeatedOperations(t *testing.T) {
	contract := normalizeOutcomeContract(OutcomeContract{OperationContract: &OperationContract{
		Version: operationContractVersion,
		Requirements: []OperationRequirement{
			{RequirementID: " operation-1 ", ToolID: " capabilityd:task.add ", ToolName: " task.add ", InputMode: OperationInputContainsExplicit, RequiredInput: json.RawMessage(`{"title":"업무","endDate":"2026-07-24"}`)},
			{RequirementID: "operation-2", ToolID: "capabilityd:task.add", ToolName: "task.add", InputMode: OperationInputContainsExplicit, RequiredInput: json.RawMessage(`{"endDate":"2026-07-24","title":"업무"}`)},
		},
	}})

	if len(contract.OperationContract.Requirements) != 2 {
		t.Fatalf("expected repeated operations to remain distinct, got %+v", contract.OperationContract)
	}
	if string(contract.OperationContract.Requirements[0].RequiredInput) != `{"endDate":"2026-07-24","title":"업무"}` {
		t.Fatalf("expected canonical JSON, got %s", contract.OperationContract.Requirements[0].RequiredInput)
	}
}

func TestOperationContractInstructionsUseInvocationValueBoundary(t *testing.T) {
	instruction := operationContractInstruction()
	for _, fragment := range []string{
		"invocation field whose value the user supplied for that same field",
		"Do not turn requested artifact facts into a generated content string",
		"the completed file requires delivery but contributes no requiredValues",
		"Do not copy an execution choice or an output reference",
	} {
		if !strings.Contains(instruction, fragment) {
			t.Fatalf("expected operation instruction to contain %q, got %s", fragment, instruction)
		}
	}
}

func TestCompileOperationRequirementsCorrectsInvalidCandidate(t *testing.T) {
	languageModel := &operationContractLanguageModel{contents: []string{
		`{"operations":[{"toolName":"task.add","requiredValues":{"title":3}}]}`,
		`{"operations":[{"toolName":"task.add","requiredValues":{"title":"분기 결산 누락 확인"}}]}`,
	}}

	contract, errorValue := compileOperationRequirements(
		context.Background(),
		languageModel,
		AgentRequest{Prompt: "분기 결산 누락 확인 업무를 추가해줘"},
		operationContractTestToolSet(),
		OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
	)

	if errorValue != nil {
		t.Fatalf("expected invalid candidate to receive typed correction: %v", errorValue)
	}
	if languageModel.calls != 2 {
		t.Fatalf("expected generation and correction calls, got %d", languageModel.calls)
	}
	correctionMessages := joinedMessageContent(languageModel.requests[1].Messages)
	if !strings.Contains(correctionMessages, `want "string"`) {
		t.Fatalf("expected validation diagnostics in correction request, got %s", correctionMessages)
	}
	if string(contract.OperationContract.Requirements[0].RequiredInput) != `{"title":"분기 결산 누락 확인"}` {
		t.Fatalf("unexpected corrected operation contract %+v", contract.OperationContract)
	}
}

func TestCompileOperationRequirementsCorrectsAuthoritativeStructuredOutputError(t *testing.T) {
	correctionError := operationContractCorrectionError{correction: llm.StructuredOutputCorrection{
		Code: "structured_output_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{
			Category: llm.StructuredOutputDiagnosticSchemaValidation,
			ValidationIssues: []llm.StructuredOutputValidationIssue{{
				FieldPath: "operations[0].requiredValues.title",
				Code:      llm.StructuredOutputValidationOther,
			}},
		},
	}}
	languageModel := &operationContractLanguageModel{
		contents: []string{
			"",
			`{"operations":[{"toolName":"task.add","requiredValues":{"title":"분기 결산 누락 확인"}}]}`,
		},
		errorsByCall: map[int]error{0: correctionError},
	}

	contract, errorValue := compileOperationRequirements(
		context.Background(),
		languageModel,
		AgentRequest{Prompt: "분기 결산 누락 확인 업무를 추가해줘"},
		operationContractTestToolSet(),
		OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
	)

	if errorValue != nil {
		t.Fatalf("expected authoritative structured error to receive typed correction: %v", errorValue)
	}
	if languageModel.calls != 2 {
		t.Fatalf("expected failed generation and correction calls, got %d", languageModel.calls)
	}
	correctionMessages := joinedMessageContent(languageModel.requests[1].Messages)
	if !strings.Contains(correctionMessages, "schema_validation") || !strings.Contains(correctionMessages, "operations[0].requiredValues.title") {
		t.Fatalf("expected typed LLMD diagnostic in correction request, got %s", correctionMessages)
	}
	if string(contract.OperationContract.Requirements[0].RequiredInput) != `{"title":"분기 결산 누락 확인"}` {
		t.Fatalf("unexpected corrected operation contract %+v", contract.OperationContract)
	}
}

func TestCompileOperationRequirementsFailsClosedWhenCorrectionIsInvalid(t *testing.T) {
	languageModel := &operationContractLanguageModel{contents: []string{
		`{"operations":[{"toolName":"task.add","requiredValues":{"title":3}}]}`,
		`{"operations":[{"toolName":"task.add","requiredValues":{"title":false}}]}`,
	}}

	_, errorValue := compileOperationRequirements(
		context.Background(),
		languageModel,
		AgentRequest{Prompt: "분기 결산 누락 확인 업무를 추가해줘"},
		operationContractTestToolSet(),
		OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
	)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "correct operation contract") {
		t.Fatalf("expected invalid correction to fail closed, got %v", errorValue)
	}
	if languageModel.calls != 2 {
		t.Fatalf("expected one correction attempt, got %d calls", languageModel.calls)
	}
}

func TestCompileOperationRequirementsDoesNotRetryNonCorrectableError(t *testing.T) {
	generationError := errors.New("provider unavailable")
	languageModel := &operationContractLanguageModel{
		contents:     []string{""},
		errorsByCall: map[int]error{0: generationError},
	}

	_, errorValue := compileOperationRequirements(
		context.Background(),
		languageModel,
		AgentRequest{Prompt: "분기 결산 누락 확인 업무를 추가해줘"},
		operationContractTestToolSet(),
		OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
	)

	if !errors.Is(errorValue, generationError) {
		t.Fatalf("expected non-correctable provider error, got %v", errorValue)
	}
	if languageModel.calls != 1 {
		t.Fatalf("expected no retry, got %d calls", languageModel.calls)
	}
}

func TestCompileOperationRequirementsPreservesRepeatedRequestedOperations(t *testing.T) {
	languageModel := &operationContractLanguageModel{contents: []string{
		`{"operations":[{"toolName":"task.add","requiredValues":{"title":"첫 번째 업무"}},{"toolName":"task.add","requiredValues":{"title":"두 번째 업무"}}]}`,
	}}

	contract, errorValue := compileOperationRequirements(
		context.Background(),
		languageModel,
		AgentRequest{Prompt: "첫 번째 업무와 두 번째 업무를 각각 추가해줘"},
		operationContractTestToolSet(),
		OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
	)

	if errorValue != nil {
		t.Fatalf("expected repeated operation contract to pass: %v", errorValue)
	}
	if len(contract.OperationContract.Requirements) != 2 {
		t.Fatalf("expected two distinct operation requirements, got %+v", contract.OperationContract)
	}
}

func TestOperationRequirementsRequireDistinctSuccessfulObservations(t *testing.T) {
	contract := &OperationContract{
		Version: operationContractVersion,
		Requirements: []OperationRequirement{
			{RequirementID: "operation-1", ToolID: "capabilityd:task.add", ToolName: "task.add", InputMode: OperationInputContainsExplicit, RequiredInput: json.RawMessage(`{"title":"첫 번째 업무"}`)},
			{RequirementID: "operation-2", ToolID: "capabilityd:task.add", ToolName: "task.add", InputMode: OperationInputContainsExplicit, RequiredInput: json.RawMessage(`{"title":"두 번째 업무"}`)},
		},
	}
	firstObservation := successfulOperationObservation(`{"title":"첫 번째 업무"}`)
	secondObservation := successfulOperationObservation(`{"title":"두 번째 업무"}`)
	secondObservation.ObservationID = "observation-2"

	if matchedAllOperationRequirements(contract, []turnObservation{firstObservation}) {
		t.Fatal("expected one observation to satisfy at most one requirement")
	}
	if !matchedAllOperationRequirements(contract, []turnObservation{firstObservation, secondObservation}) {
		t.Fatal("expected two matching observations to satisfy two requirements")
	}
}

func TestOperationWithoutExplicitValuesStillRequiresMatchingToolObservation(t *testing.T) {
	requirement := OperationRequirement{
		RequirementID: "operation-1",
		ToolID:        "capabilityd:task.add",
		ToolName:      "task.add",
		InputMode:     OperationInputNoExplicitValues,
		RequiredInput: json.RawMessage(`{}`),
	}
	contract := &OperationContract{Version: operationContractVersion, Requirements: []OperationRequirement{requirement}}

	if matchedAllOperationRequirements(contract, nil) {
		t.Fatal("expected a successful matching tool observation")
	}
	if !matchedAllOperationRequirements(contract, []turnObservation{successfulOperationObservation(`{"title":"generated value"}`)}) {
		t.Fatal("expected generated tool input when the independent review found no explicit user values")
	}
}

func TestTerminalOperationIntentRejectsGeneratedExecutionDetails(t *testing.T) {
	inputIntentSchema := json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	requiredInput, errorValue := validateRequiredOperationInput(json.RawMessage(`{}`), inputIntentSchema)
	if errorValue != nil || string(requiredInput) != `{}` {
		t.Fatalf("expected an empty terminal operation intent, got %s, %v", requiredInput, errorValue)
	}
	for _, generatedInput := range []string{
		`{"command":"python create_document.py"}`,
		`{"mode":"command"}`,
		`{"workingDirectoryPath":"/tmp"}`,
	} {
		if _, errorValue := validateRequiredOperationInput(json.RawMessage(generatedInput), inputIntentSchema); errorValue == nil {
			t.Fatalf("expected generated execution detail to fail closed: %s", generatedInput)
		}
	}
}




func TestOperationRequirementsUseRecursiveSubsetAndExactLargeNumbers(t *testing.T) {
	requirement := OperationRequirement{
		RequirementID: "operation-1",
		ToolID:        "capabilityd:task.add",
		ToolName:      "task.add",
		InputMode:     OperationInputContainsExplicit,
		RequiredInput: json.RawMessage(`{"details":{"owner":"Lee"},"sequence":9007199254740993}`),
	}
	observation := successfulOperationObservation(`{"details":{"owner":"Lee","team":"Support"},"sequence":9007199254740993}`)

	if !matchedAllOperationRequirements(&OperationContract{Version: operationContractVersion, Requirements: []OperationRequirement{requirement}}, []turnObservation{observation}) {
		t.Fatal("expected nested extras and exact large number to match")
	}
	observation.ToolInput = json.RawMessage(`{"details":{"owner":"Lee","team":"Support"},"sequence":9007199254740992}`)
	if matchedAllOperationRequirements(&OperationContract{Version: operationContractVersion, Requirements: []OperationRequirement{requirement}}, []turnObservation{observation}) {
		t.Fatal("expected a distinct large number to fail")
	}
}

func TestOperationRequirementsMatchPartialArrayObjectsByPosition(t *testing.T) {
	requirement := OperationRequirement{
		RequirementID: "operation-1",
		ToolID:        "kernel/file.edit",
		ToolName:      "file.edit",
		InputMode:     OperationInputContainsExplicit,
		RequiredInput: json.RawMessage(`{"edits":[{"newText":"완료"}]}`),
	}
	contract := &OperationContract{Version: operationContractVersion, Requirements: []OperationRequirement{requirement}}
	observation := successfulOperationObservation(`{"edits":[{"path":"memo/status.md","oldText":"진행 중","newText":"완료"}]}`)
	observation.Tool = "file.edit"
	observation.ToolID = "kernel/file.edit"

	if !matchedAllOperationRequirements(contract, []turnObservation{observation}) {
		t.Fatal("expected runtime-supplied edit mechanics to preserve the explicit replacement")
	}
	for _, mismatchedInput := range []json.RawMessage{
		json.RawMessage(`{"edits":[{"path":"memo/status.md","oldText":"진행 중","newText":"보류"}]}`),
		json.RawMessage(`{"edits":[{"path":"memo/status.md","oldText":"진행 중","newText":"완료"},{"path":"memo/other.md","oldText":"진행 중","newText":"완료"}]}`),
	} {
		observation.ToolInput = mismatchedInput
		if matchedAllOperationRequirements(contract, []turnObservation{observation}) {
			t.Fatalf("expected array value or length mismatch to fail: %s", mismatchedInput)
		}
	}
}

func TestCompileOperationRequirementsRejectsCorruptedPersistedContract(t *testing.T) {
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{"task.add"},
		OperationContract: &OperationContract{
			Version: operationContractVersion,
			Requirements: []OperationRequirement{{
				RequirementID: "operation-1",
				ToolName:      "task.add",
				InputMode:     OperationInputContainsExplicit,
				RequiredInput: json.RawMessage(`{"title":"업무"}`),
			}},
		},
	}

	_, errorValue := compileOperationRequirements(context.Background(), &operationContractLanguageModel{}, AgentRequest{}, operationContractTestToolSet(), contract)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "descriptor identity") {
		t.Fatalf("expected corrupted persisted metadata to fail closed, got %v", errorValue)
	}
}

func operationContractTestToolSet() *ToolSet {
	return newTestToolSetWithDefinitions([]ToolDefinition{{
		ID:                "capabilityd:task.add",
		Name:              "task.add",
		InputSchema:       operationContractTaskInputSchema(),
		InputIntentSchema: operationContractTaskInputIntentSchema(),
		OutputSchema:      json.RawMessage(`{"type":"object","properties":{}}`),
		SideEffectClass:   ToolSideEffectStateChange,
	}})
}

func operationContractTaskInputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["title","goal","endDate"],
		"properties":{
			"title":{"type":"string"},
			"goal":{"type":"string"},
			"endDate":{"type":"string"},
			"size":{"type":"string","enum":["S","M","L"]}
		}
	}`)
}

func operationContractTaskInputIntentSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"properties":{
			"title":{"type":"string"},
			"goal":{"type":"string"},
			"endDate":{"type":"string"},
			"size":{"type":"string","enum":["S","M","L"]}
		}
	}`)
}

func operationContractCalendarUpdateIntentSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"properties":{
			"eventHint":{"type":"string"},
			"title":{"type":"string"}
		}
	}`)
}

func matchedAllOperationRequirements(contract *OperationContract, observations []turnObservation) bool {
	if contract == nil {
		return true
	}
	if contract.Version != operationContractVersion || len(contract.Requirements) == 0 {
		return false
	}
	return matchedOperationRequirementCount(contract, observations) == len(contract.Requirements)
}

func successfulOperationObservation(toolInput string) turnObservation {
	return turnObservation{
		ObservationID: "observation-1",
		Action:        "continue",
		Tool:          "task.add",
		ToolID:        "capabilityd:task.add",
		ToolInput:     json.RawMessage(toolInput),
		Output:        ToolOutput{Content: "ok"},
	}
}
