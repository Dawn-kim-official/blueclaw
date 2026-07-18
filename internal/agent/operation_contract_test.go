package agent

import (
	"context"
	"encoding/json"
	"errors"
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

func TestCompileOperationRequirementsIncludesDirectlyReferencedVisibleContext(t *testing.T) {
	languageModel := &operationContractLanguageModel{contents: []string{
		`{"operations":[{"toolName":"task.add","requiredValuesJSON":"{}"}]}`,
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
		`{"operations":[{"toolName":"task.add","requiredValuesJSON":"{\"title\":\"분기 결산 누락 확인\",\"endDate\":\"2026-07-24\"}"}]}`,
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
			if _, errorValue := validateRequiredOperationInput(testCase.document, inputSchema); errorValue == nil {
				t.Fatalf("expected %s to fail", testCase.name)
			}
		})
	}
}

func TestValidateRequiredOperationInputAllowsExplicitPartialInput(t *testing.T) {
	requiredInput, errorValue := validateRequiredOperationInput(`{"title":"분기 결산 누락 확인"}`, operationContractTaskInputSchema())

	if errorValue != nil {
		t.Fatalf("expected partial input to pass: %v", errorValue)
	}
	if string(requiredInput) != `{"title":"분기 결산 누락 확인"}` {
		t.Fatalf("unexpected normalized input %s", requiredInput)
	}
}

func TestOperationContractSchemaRejectsEmptyRequiredValues(t *testing.T) {
	schemaDocument := operationContractSchema([]string{"terminal.run"})

	if !strings.Contains(schemaDocument, `"requiredValuesJSON":{"maxLength":4096,"minLength":2,"type":"string"}`) {
		t.Fatalf("expected required values to require at least an empty JSON object, got %s", schemaDocument)
	}
}

func TestOperationContractSeparatesDescriptorValidityFromInvocationCompleteness(t *testing.T) {
	inputSchema := json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["eventID"],
		"minProperties":2,
		"properties":{
			"eventID":{"type":"string"},
			"title":{"type":"string"}
		}
	}`)
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{{
		ID:              "capabilityd:calendar.update",
		Name:            "calendar.update",
		InputSchema:     inputSchema,
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{}}`),
		SideEffectClass: ToolSideEffectStateChange,
	}})

	if _, errorValue := operationDescriptorDocuments(toolSet, []string{"calendar.update"}); errorValue != nil {
		t.Fatalf("expected descriptor schema to resolve without a fake invocation: %v", errorValue)
	}
	for _, requiredValues := range []string{`{}`, `{"eventID":"event-1"}`, `{"title":"변경"}`} {
		if _, errorValue := validateRequiredOperationInput(requiredValues, inputSchema); errorValue != nil {
			t.Fatalf("expected partial explicit values %s to pass: %v", requiredValues, errorValue)
		}
	}
	if _, errorValue := validateRequiredOperationInput(`{"query":"변경"}`, inputSchema); errorValue == nil {
		t.Fatal("expected unknown partial value to fail")
	}
}

func TestValidateRequiredOperationInputValidatesNestedAlternativesAndArrays(t *testing.T) {
	inputSchema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"updates":{
				"type":"object",
				"additionalProperties":false,
				"required":["status"],
				"properties":{"status":{"type":"string","enum":["open","done"]}}
			},
			"labels":{"type":"array","items":{"type":"string"}},
			"owner":{"anyOf":[{"type":"string"},{"type":"null"}]}
		}
	}`)
	validDocument := `{"updates":{"status":"done"},"labels":["quarterly"],"owner":null}`
	if _, errorValue := validateRequiredOperationInput(validDocument, inputSchema); errorValue != nil {
		t.Fatalf("expected nested input to pass: %v", errorValue)
	}
	for _, invalidDocument := range []string{
		`{"updates":{"status":"closed"}}`,
		`{"updates":{"unexpected":"done"}}`,
		`{"labels":[3]}`,
		`{"owner":false}`,
	} {
		if _, errorValue := validateRequiredOperationInput(invalidDocument, inputSchema); errorValue == nil {
			t.Fatalf("expected nested input to fail: %s", invalidDocument)
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

func TestOperationRequirementsRejectPartialExecutionForNormalAndElapsedCompletion(t *testing.T) {
	requirement := OperationRequirement{
		RequirementID: "operation-1",
		ToolID:        "capabilityd:task.add",
		ToolName:      "task.add",
		InputMode:     OperationInputContainsExplicit,
		RequiredInput: json.RawMessage(`{"title":"분기 결산 누락 확인","endDate":"2026-07-24"}`),
	}
	observation := successfulOperationObservation(`{"title":"분기 결산 누락 확인"}`)
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{"task.add"},
		OperationContract: &OperationContract{
			Version:      operationContractVersion,
			Requirements: []OperationRequirement{requirement},
		},
	}

	normalResult := validateOutcomeContractRequirements(contract, []turnObservation{observation}, nil)
	elapsedResult := elapsedTurnCanComplete(
		AgentTurnRequest{OutcomeContract: contract},
		[]toolUseRequirement{{ToolName: "task.add"}},
		[]turnObservation{observation},
		nil,
	)

	if normalResult.IsSatisfied || elapsedResult {
		t.Fatalf("expected partial execution to fail normal and elapsed completion")
	}
}

func TestOperationRequirementsAcceptExactRequestedInputForNormalAndElapsedCompletion(t *testing.T) {
	requirement := OperationRequirement{
		RequirementID: "operation-1",
		ToolID:        "capabilityd:task.add",
		ToolName:      "task.add",
		InputMode:     OperationInputContainsExplicit,
		RequiredInput: json.RawMessage(`{"title":"분기 결산 누락 확인","endDate":"2026-07-24"}`),
	}
	observation := successfulOperationObservation(`{"title":"분기 결산 누락 확인","endDate":"2026-07-24","goal":"누락 확인"}`)
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{"task.add"},
		OperationContract: &OperationContract{
			Version:      operationContractVersion,
			Requirements: []OperationRequirement{requirement},
		},
	}

	normalResult := validateOutcomeContractRequirements(contract, []turnObservation{observation}, nil)
	elapsedResult := elapsedTurnCanComplete(
		AgentTurnRequest{OutcomeContract: contract},
		[]toolUseRequirement{{ToolName: "task.add"}},
		[]turnObservation{observation},
		nil,
	)

	if !normalResult.IsSatisfied || !elapsedResult {
		t.Fatalf("expected exact requested input to pass normal and elapsed completion")
	}
}

func TestCompileOperationRequirementsRepairsRejectedCandidateOnce(t *testing.T) {
	languageModel := &operationContractLanguageModel{contents: []string{
		`{"operations":[{"toolName":"task.add","requiredValuesJSON":"{}"}]}`,
		`{"isComplete":false,"reason":"missing explicit title"}`,
		`{"operations":[{"toolName":"task.add","requiredValuesJSON":"{\"title\":\"분기 결산 누락 확인\"}"}]}`,
		`{"isComplete":true,"reason":""}`,
	}}

	contract, errorValue := compileOperationRequirements(
		context.Background(),
		languageModel,
		AgentRequest{Prompt: "분기 결산 누락 확인 업무를 추가해줘"},
		operationContractTestToolSet(),
		OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
	)

	if errorValue != nil {
		t.Fatalf("expected reviewed correction to pass: %v", errorValue)
	}
	if languageModel.calls != 4 {
		t.Fatalf("expected compile, review, correction, review calls, got %d", languageModel.calls)
	}
	if !strings.Contains(joinedMessageContent(languageModel.requests[2].Messages), "missing explicit title") {
		t.Fatal("expected independent review reason in the correction request")
	}
	if string(contract.OperationContract.Requirements[0].RequiredInput) != `{"title":"분기 결산 누락 확인"}` {
		t.Fatalf("unexpected corrected operation contract %+v", contract.OperationContract)
	}
}

func TestCompileOperationRequirementsCorrectsInvalidReviewedCandidate(t *testing.T) {
	languageModel := &operationContractLanguageModel{contents: []string{
		`{"operations":[{"toolName":"task.add","requiredValuesJSON":"{}"}]}`,
		`{"isComplete":false,"reason":"missing explicit title"}`,
		`{"operations":[{"toolName":"task.add","requiredValuesJSON":"{"}]}`,
		`{"operations":[{"toolName":"task.add","requiredValuesJSON":"{\"title\":\"분기 결산 누락 확인\"}"}]}`,
		`{"isComplete":true,"reason":""}`,
	}}

	contract, errorValue := compileOperationRequirements(
		context.Background(),
		languageModel,
		AgentRequest{Prompt: "분기 결산 누락 확인 업무를 추가해줘"},
		operationContractTestToolSet(),
		OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
	)

	if errorValue != nil {
		t.Fatalf("expected invalid correction candidate to receive typed correction: %v", errorValue)
	}
	if languageModel.calls != 5 {
		t.Fatalf("expected compile, review, invalid correction, typed correction, and review calls, got %d", languageModel.calls)
	}
	correctionMessages := joinedMessageContent(languageModel.requests[3].Messages)
	if !strings.Contains(correctionMessages, "missing explicit title") || !strings.Contains(correctionMessages, "unexpected EOF") {
		t.Fatalf("expected review and validation diagnostics in correction request, got %s", correctionMessages)
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
				FieldPath: "operations[0].requiredValuesJSON",
				Code:      llm.StructuredOutputValidationOther,
			}},
		},
	}}
	languageModel := &operationContractLanguageModel{
		contents: []string{
			"",
			`{"operations":[{"toolName":"task.add","requiredValuesJSON":"{\"title\":\"분기 결산 누락 확인\"}"}]}`,
			`{"isComplete":true,"reason":""}`,
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
	if languageModel.calls != 3 {
		t.Fatalf("expected failed generation, correction, and review calls, got %d", languageModel.calls)
	}
	correctionMessages := joinedMessageContent(languageModel.requests[1].Messages)
	if !strings.Contains(correctionMessages, "schema_validation") || !strings.Contains(correctionMessages, "operations[0].requiredValuesJSON") {
		t.Fatalf("expected typed SDKD diagnostic in correction request, got %s", correctionMessages)
	}
	if string(contract.OperationContract.Requirements[0].RequiredInput) != `{"title":"분기 결산 누락 확인"}` {
		t.Fatalf("unexpected corrected operation contract %+v", contract.OperationContract)
	}
}

func TestCompileOperationRequirementsFailsClosedWhenCorrectionIsInvalid(t *testing.T) {
	languageModel := &operationContractLanguageModel{contents: []string{
		`{"operations":[{"toolName":"task.add","requiredValuesJSON":"{"}]}`,
		`{"operations":[{"toolName":"task.add","requiredValuesJSON":"["}]}`,
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

func TestCompileOperationRequirementsFailsAfterRejectedCorrection(t *testing.T) {
	languageModel := &operationContractLanguageModel{contents: []string{
		`{"operations":[{"toolName":"task.add","requiredValuesJSON":"{}"}]}`,
		`{"isComplete":false,"reason":"missing explicit title"}`,
		`{"operations":[{"toolName":"task.add","requiredValuesJSON":"{}"}]}`,
		`{"isComplete":false,"reason":"still missing explicit title"}`,
	}}

	_, errorValue := compileOperationRequirements(
		context.Background(),
		languageModel,
		AgentRequest{Prompt: "분기 결산 누락 확인 업무를 추가해줘"},
		operationContractTestToolSet(),
		OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
	)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "still missing explicit title") {
		t.Fatalf("expected rejected correction to fail closed, got %v", errorValue)
	}
}

func TestCompileOperationRequirementsPreservesRepeatedRequestedOperations(t *testing.T) {
	languageModel := &operationContractLanguageModel{contents: []string{
		`{"operations":[{"toolName":"task.add","requiredValuesJSON":"{\"title\":\"첫 번째 업무\"}"},{"toolName":"task.add","requiredValuesJSON":"{\"title\":\"두 번째 업무\"}"}]}`,
		`{"isComplete":true,"reason":""}`,
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

	if operationRequirementsSatisfied(contract, []turnObservation{firstObservation}) {
		t.Fatal("expected one observation to satisfy at most one requirement")
	}
	if !operationRequirementsSatisfied(contract, []turnObservation{firstObservation, secondObservation}) {
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

	if operationRequirementsSatisfied(contract, nil) {
		t.Fatal("expected a successful matching tool observation")
	}
	if !operationRequirementsSatisfied(contract, []turnObservation{successfulOperationObservation(`{"title":"generated value"}`)}) {
		t.Fatal("expected generated tool input when the independent review found no explicit user values")
	}
}

func TestPendingOperationInputMismatchDoesNotBlockReadTools(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{
		{ID: "kernel:file.read", Name: "file.read", SideEffectClass: ToolSideEffectRead},
		{ID: "capabilityd:task.add", Name: "task.add", SideEffectClass: ToolSideEffectStateChange},
	})
	contract := &OperationContract{
		Version: operationContractVersion,
		Requirements: []OperationRequirement{{
			RequirementID: "operation-1",
			ToolID:        "capabilityd:task.add",
			ToolName:      "task.add",
			InputMode:     OperationInputContainsExplicit,
			RequiredInput: json.RawMessage(`{"title":"업무"}`),
		}},
	}

	_, isMismatch := pendingOperationInputMismatch(toolSet, contract, nil, "file.read", json.RawMessage(`{"path":"other.txt"}`))

	if isMismatch {
		t.Fatal("expected read and discovery tools to remain available outside the mutation guard")
	}
}

func TestPendingOperationInputMismatchIgnoresDescriptorSideEffectDrift(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{{
		ID:              "capabilityd:task.add",
		Name:            "task.add",
		SideEffectClass: ToolSideEffectRead,
	}})
	contract := &OperationContract{
		Version: operationContractVersion,
		Requirements: []OperationRequirement{{
			RequirementID: "operation-1",
			ToolID:        "capabilityd:task.add",
			ToolName:      "task.add",
			InputMode:     OperationInputContainsExplicit,
			RequiredInput: json.RawMessage(`{"title":"업무"}`),
		}},
	}

	pendingRequirements, isMismatch := pendingOperationInputMismatch(toolSet, contract, nil, "task.add", json.RawMessage(`{"title":"다른 업무"}`))

	if !isMismatch || len(pendingRequirements) != 1 {
		t.Fatalf("expected the validated contract to remain authoritative after descriptor metadata drift, got mismatch=%t pending=%+v", isMismatch, pendingRequirements)
	}
}

func TestPendingOperationInputMismatchAllowsBipartiteReassignment(t *testing.T) {
	toolSet := operationContractTestToolSet()
	contract := &OperationContract{
		Version: operationContractVersion,
		Requirements: []OperationRequirement{
			{
				RequirementID: "operation-1",
				ToolID:        "capabilityd:task.add",
				ToolName:      "task.add",
				InputMode:     OperationInputContainsExplicit,
				RequiredInput: json.RawMessage(`{"title":"업무"}`),
			},
			{
				RequirementID: "operation-2",
				ToolID:        "capabilityd:task.add",
				ToolName:      "task.add",
				InputMode:     OperationInputContainsExplicit,
				RequiredInput: json.RawMessage(`{"title":"업무","endDate":"2026-07-24"}`),
			},
		},
	}
	observations := []turnObservation{successfulOperationObservation(`{"title":"업무","goal":"보고","endDate":"2026-07-24"}`)}

	_, isMismatch := pendingOperationInputMismatch(toolSet, contract, observations, "task.add", json.RawMessage(`{"title":"업무","goal":"보고","endDate":"2026-07-25"}`))
	if isMismatch {
		t.Fatal("expected the candidate to satisfy the shorter occurrence after maximum matching reassignment")
	}

	pendingRequirements, isMismatch := pendingOperationInputMismatch(toolSet, contract, observations, "task.add", json.RawMessage(`{"title":"다른 업무","goal":"보고","endDate":"2026-07-25"}`))
	if !isMismatch || len(pendingRequirements) != 1 {
		t.Fatalf("expected unrelated input to leave one unmatched occurrence, got mismatch=%t pending=%+v", isMismatch, pendingRequirements)
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

	if !operationRequirementsSatisfied(&OperationContract{Version: operationContractVersion, Requirements: []OperationRequirement{requirement}}, []turnObservation{observation}) {
		t.Fatal("expected nested extras and exact large number to match")
	}
	observation.ToolInput = json.RawMessage(`{"details":{"owner":"Lee","team":"Support"},"sequence":9007199254740992}`)
	if operationRequirementsSatisfied(&OperationContract{Version: operationContractVersion, Requirements: []OperationRequirement{requirement}}, []turnObservation{observation}) {
		t.Fatal("expected a distinct large number to fail")
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
		ID:              "capabilityd:task.add",
		Name:            "task.add",
		InputSchema:     operationContractTaskInputSchema(),
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{}}`),
		SideEffectClass: ToolSideEffectStateChange,
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
