package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

func TestDecideAgentActionUsesNativeChatForFinishAndContinue(t *testing.T) {
	testCases := []struct {
		name         string
		toolName     string
		arguments    string
		expectedType string
		check        func(*testing.T, agentAction)
	}{
		{
			name:         "finish",
			toolName:     "finish",
			arguments:    `{"message":"done","goalStatus":"satisfied","goalSatisfied":true,"hasRemainingWork":false,"completionEvidenceIDs":["obs-1"],"qualityReview":[],"executionStateUpdate":{"goal":"done"}}`,
			expectedType: "finish",
			check: func(t *testing.T, action agentAction) {
				if action.Message != "done" || len(action.CompletionEvidenceIDs) != 1 || action.ExecutionStateUpdate.Goal != "done" {
					t.Fatalf("expected finish fields to survive native action parsing, got %+v", action)
				}
			},
		},
		{
			name:         "continue",
			toolName:     "terminal.run",
			arguments:    `{"command":"pwd"}`,
			expectedType: "continue",
			check: func(t *testing.T, action agentAction) {
				if action.ToolName != "terminal.run" || string(action.ToolInput) != `{"command":"pwd"}` {
					t.Fatalf("expected continue tool fields to survive native action parsing, got %+v", action)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := nativeAgentActionLanguageModel{chatResponse: nativeAgentActionChatResponse(testCase.toolName, testCase.arguments)}
			action, errorValue := DecideAgentAction(context.Background(), &provider, nativeAgentActionTestState())
			if errorValue != nil {
				t.Fatalf("expected native action: %v", errorValue)
			}
			if action.Action != testCase.expectedType {
				t.Fatalf("expected %q action, got %+v", testCase.expectedType, action)
			}
			testCase.check(t, action)
			if provider.chatCalls != 1 || provider.structuredCalls != 0 {
				t.Fatalf("expected only one native chat call, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
			}
		})
	}
}

func TestDecideAgentActionNativeChatOmitsTextToolCatalog(t *testing.T) {
	provider := nativeAgentActionLanguageModel{chatResponse: nativeAgentActionChatResponse("finish", `{}`)}

	_, errorValue := DecideAgentAction(context.Background(), &provider, nativeAgentActionTestState())
	if errorValue != nil {
		t.Fatalf("expected native action: %v", errorValue)
	}
	if strings.Contains(chatMessageContent(provider.lastRequest.Messages), "Available tool catalog") {
		t.Fatalf("expected native chat messages to omit textual tool catalog, got %s", chatMessageContent(provider.lastRequest.Messages))
	}
	if nativeChatTool(t, provider.lastRequest.Tools, TerminalRunToolName).Function.Name != TerminalRunToolName {
		t.Fatalf("expected native chat to preserve direct typed tool, got %+v", provider.lastRequest.Tools)
	}
}

func TestBuildAgentActionChatRequestExposesDirectToolsAndTerminalControls(t *testing.T) {
	state := nativeAgentActionTestState()
	seed := int64(77)
	temperature := 0.4
	maxTokens := 321
	state.Options.GenerationOptions = llm.GenerationOptions{
		Seed:        &seed,
		Temperature: &temperature,
		MaxTokens:   &maxTokens,
	}
	structuredRequest := BuildAgentActionRequest(state)
	chatRequest, isRepresentable := buildAgentActionChatCompletionRequest(structuredRequest)
	if !isRepresentable {
		t.Fatal("expected text action request to be representable as chat")
	}
	if len(chatRequest.Tools) != 4 {
		t.Fatalf("expected one callable tool and three terminal controls, got %+v", chatRequest.Tools)
	}
	tool := nativeChatTool(t, chatRequest.Tools, TerminalRunToolName)
	if tool.Type != "function" {
		t.Fatalf("expected function tool, got %+v", tool)
	}
	if string(tool.Function.Parameters) != `{"additionalProperties":false,"properties":{"command":{"type":"string"}},"required":["command"],"type":"object"}` {
		t.Fatalf("expected direct tool parameters to preserve the callable input schema, got %s", tool.Function.Parameters)
	}
	finishTool := nativeChatTool(t, chatRequest.Tools, "finish")
	if strings.Contains(string(finishTool.Function.Parameters), `"action"`) {
		t.Fatalf("expected terminal control schema without redundant action discriminator, got %s", finishTool.Function.Parameters)
	}
	if string(chatRequest.ToolChoice) != `"required"` {
		t.Fatalf("expected required native tool choice, got %s", chatRequest.ToolChoice)
	}
	if chatRequest.ParallelToolCalls {
		t.Fatal("expected parallel native tool calls to be disabled")
	}
	if chatRequest.GenerationOptions != structuredRequest.GenerationOptions {
		t.Fatalf("expected native chat generation options to reuse structured request options, got %+v and %+v", chatRequest.GenerationOptions, structuredRequest.GenerationOptions)
	}
	if chatRequest.GenerationOptions.Seed == nil || *chatRequest.GenerationOptions.Seed != seed {
		t.Fatalf("expected native chat seed to be preserved, got %+v", chatRequest.GenerationOptions)
	}
	if chatRequest.GenerationOptions.Temperature == nil || *chatRequest.GenerationOptions.Temperature != temperature {
		t.Fatalf("expected native chat temperature to be preserved, got %+v", chatRequest.GenerationOptions)
	}
	if chatRequest.GenerationOptions.MaxTokens == nil || *chatRequest.GenerationOptions.MaxTokens != maxTokens {
		t.Fatalf("expected native chat max tokens to be preserved, got %+v", chatRequest.GenerationOptions)
	}
	if chatRequest.SchemaName != agentActionSchemaName {
		t.Fatalf("expected native chat schema provenance, got %q", chatRequest.SchemaName)
	}
}

func TestBuildAgentActionRequestKeepsTextToolCatalogForStructuredFallback(t *testing.T) {
	request := BuildAgentActionRequest(nativeAgentActionTestState())
	if !strings.Contains(joinMessageContent(request.Messages), "Available tool catalog") {
		t.Fatalf("expected structured request to retain textual tool catalog, got %s", joinMessageContent(request.Messages))
	}
	if request.GenerationOptions.MaxTokens == nil || *request.GenerationOptions.MaxTokens != defaultAgentActionMaxTokens {
		t.Fatalf("expected bounded action output, got %+v", request.GenerationOptions)
	}
	chatRequest, isRepresentable := buildAgentActionChatCompletionRequest(request)
	if !isRepresentable {
		t.Fatal("expected action request to be representable as chat")
	}
	if chatRequest.GenerationOptions != request.GenerationOptions {
		t.Fatalf("expected structured and native action output budgets to match, got %+v and %+v", request.GenerationOptions, chatRequest.GenerationOptions)
	}
}

func TestBuildAgentActionRequestIncludesNextPendingOperationRequirement(t *testing.T) {
	request := buildAgentActionRequest(nativeAgentActionContractState(), false)
	messageContent := joinMessageContent(request.Messages)

	if !strings.Contains(messageContent, "Pending typed operation requirements.") {
		t.Fatalf("expected pending operation context, got %s", messageContent)
	}
	if !strings.Contains(messageContent, `"toolName":"file.write"`) || !strings.Contains(messageContent, `"requiredInput":{"path":"report.txt"}`) {
		t.Fatalf("expected exact typed file.write input, got %s", messageContent)
	}
}

func TestCompletionArtifactDeliveryInputUsesPendingOperationInput(t *testing.T) {
	requiredInput := json.RawMessage(`{"filename":"report.json","files":[{"filename":"report.json","path":"report.json","title":"분기 보고"}]}`)
	state := agentTaskState{
		Request: AgentTurnRequest{OutcomeContract: OutcomeContract{OperationContract: &OperationContract{
			Version: operationContractVersion,
			Requirements: []OperationRequirement{{
				RequirementID: "operation-1",
				ToolID:        "kernel/file.deliver",
				ToolName:      FileDeliverToolName,
				InputMode:     OperationInputContainsExplicit,
				RequiredInput: requiredInput,
			}},
		}}},
	}
	completionState := CompletionState{AttachmentPaths: []string{"/workspace/report.json"}}

	input := completionArtifactDeliveryInput(state, completionState)

	if string(input) != string(requiredInput) {
		t.Fatalf("expected pending operation input %s, got %s", requiredInput, input)
	}
	observation := turnObservation{
		ObservationID: "observation-1",
		Action:        "continue",
		Tool:          FileDeliverToolName,
		ToolID:        "kernel/file.deliver",
		ToolInput:     input,
		Output:        ToolOutput{Content: "file attached"},
	}
	if !matchedAllOperationRequirements(state.Request.OutcomeContract.OperationContract, []turnObservation{observation}) {
		t.Fatalf("expected nested delivery input to satisfy the pending operation: %s", input)
	}
}

func TestCompletionArtifactDeliveryInputAddsDiscoveredPath(t *testing.T) {
	state := agentTaskState{
		Request: AgentTurnRequest{OutcomeContract: OutcomeContract{OperationContract: &OperationContract{
			Version: operationContractVersion,
			Requirements: []OperationRequirement{{
				RequirementID: "operation-1",
				ToolID:        "kernel/file.deliver",
				ToolName:      FileDeliverToolName,
				RequiredInput: json.RawMessage(`{"title":"분기 보고"}`),
			}},
		}}},
	}
	completionState := CompletionState{AttachmentPaths: []string{"/workspace/report.json"}}

	input := completionArtifactDeliveryInput(state, completionState)

	if string(input) != `{"path":"/workspace/report.json","title":"분기 보고"}` {
		t.Fatalf("expected discovered path merged into pending input, got %s", input)
	}
}

func TestDecideAgentActionNativeChatRejectsInvalidCallsWithoutStructuredFallback(t *testing.T) {
	blankToolCallIDResponse := nativeAgentActionChatResponse("finish", `{}`)
	blankToolCallIDResponse.Message.ToolCalls[0].ID = " "
	testCases := []struct {
		name     string
		response llm.ChatCompletionResponse
	}{
		{name: "empty calls", response: llm.ChatCompletionResponse{FinishReason: "tool_calls", Message: llm.ChatCompletionMessage{Role: "assistant"}}},
		{name: "unknown tool", response: nativeAgentActionChatResponse("unknown", `{}`)},
		{name: "malformed arguments", response: nativeAgentActionChatResponse(TerminalRunToolName, "{invalid")},
		{name: "non-object arguments", response: nativeAgentActionChatResponse(TerminalRunToolName, `[]`)},
		{name: "empty arguments", response: nativeAgentActionChatResponse(TerminalRunToolName, "")},
		{name: "blank tool call ID", response: blankToolCallIDResponse},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := nativeAgentActionLanguageModel{chatResponse: testCase.response}
			_, errorValue := DecideAgentAction(context.Background(), &provider, nativeAgentActionTestState())
			if errorValue == nil {
				t.Fatal("expected native action error")
			}
			if provider.chatCalls != 1 || provider.structuredCalls != 0 {
				t.Fatalf("expected direct native failure without structured fallback, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
			}
		})
	}
}

func TestDecideAgentActionNativeChatRecoversTaskAddAndInvokesItOnce(t *testing.T) {
	executionCount := 0
	toolSet := NewToolSet([]string{"task.add"})
	registerTestTool(toolSet, ToolDefinition{
		Name:        "task.add",
		Description: "Add a task.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		executionCount++
		return testToolSuccess("added"), nil
	})
	state := agentTaskState{Request: AgentTurnRequest{Prompt: "add a task", ToolSet: toolSet}}
	correctionError := testStructuredOutputCorrectionError{correction: llm.StructuredOutputCorrection{
		Code: "provider_response_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{
			Category:     llm.StructuredOutputDiagnosticJSONParse,
			ToolName:     "task.add",
			RepairStatus: llm.StructuredOutputRepairFailed,
		},
	}}
	provider := nativeAgentActionLanguageModel{
		chatErrors: []error{correctionError, nil},
		chatResponses: []llm.ChatCompletionResponse{
			{},
			nativeAgentActionChatResponse("task.add", `{"title":"plan review"}`),
		},
	}

	action, errorValue := DecideAgentAction(context.Background(), &provider, state)
	if errorValue != nil {
		t.Fatalf("expected corrected native action: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != "task.add" || string(action.ToolInput) != `{"title":"plan review"}` {
		t.Fatalf("expected parsed task.add action, got %+v", action)
	}
	result, invokeError := state.Request.ToolSet.Invoke(context.Background(), ToolInvocation{ToolName: action.ToolName, Input: action.ToolInput})
	if invokeError != nil || result.Failure != nil {
		t.Fatalf("expected task.add invocation, got %+v, %v", result, invokeError)
	}
	if executionCount != 1 || provider.chatCalls != 2 || provider.structuredCalls != 0 {
		t.Fatalf("expected one side effect after two native calls, got executions=%d chat=%d structured=%d", executionCount, provider.chatCalls, provider.structuredCalls)
	}
}

func TestDecideAgentActionNativeChatRetryRequiresExactDiagnosticTool(t *testing.T) {
	correctionError := testStructuredOutputCorrectionError{correction: llm.StructuredOutputCorrection{
		Code: "structured_output_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{
			Category: llm.StructuredOutputDiagnosticSchemaValidation,
			ToolName: "task.add",
			ValidationIssues: []llm.StructuredOutputValidationIssue{{
				FieldPath: "/title",
				Code:      llm.StructuredOutputValidationRequired,
			}},
		},
	}}
	provider := nativeAgentActionLanguageModel{
		chatErrors: []error{correctionError, nil},
		chatResponses: []llm.ChatCompletionResponse{
			{},
			nativeAgentActionChatResponse("task.add", `{"title":"plan review"}`),
		},
	}
	state := nativeAgentActionTestStateWithTools("task.add", TerminalRunToolName)

	_, errorValue := DecideAgentAction(context.Background(), &provider, state)
	if errorValue != nil {
		t.Fatalf("expected corrected native action: %v", errorValue)
	}
	if len(provider.chatRequests) != 2 {
		t.Fatalf("expected two native requests, got %d", len(provider.chatRequests))
	}
	retryRequest := provider.chatRequests[1]
	if len(retryRequest.Tools) != 1 || retryRequest.Tools[0].Function.Name != "task.add" {
		t.Fatalf("expected exact diagnostic tool retry, got %+v", retryRequest.Tools)
	}
	if string(retryRequest.ToolChoice) != `"required"` || retryRequest.ParallelToolCalls {
		t.Fatalf("expected portable single-tool requirement, got choice=%s parallel=%t", retryRequest.ToolChoice, retryRequest.ParallelToolCalls)
	}
	if !strings.Contains(retryRequest.Messages[len(retryRequest.Messages)-1].Content, "schema_validation") || !strings.Contains(retryRequest.Messages[len(retryRequest.Messages)-1].Content, "/title") {
		t.Fatalf("expected typed correction context, got %+v", retryRequest.Messages)
	}
}

func TestDecideAgentActionNativeChatRetryRequiresSinglePendingContractTool(t *testing.T) {
	correctionError := testStructuredOutputCorrectionError{correction: llm.StructuredOutputCorrection{
		Code: "structured_output_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{
			Category:     llm.StructuredOutputDiagnosticFinishReason,
			FinishReason: "stop",
		},
	}}
	testCases := []struct {
		name             string
		observations     []turnObservation
		expectedToolName string
	}{
		{name: "first operation", expectedToolName: "file.write"},
		{
			name: "next operation",
			observations: []turnObservation{{
				ObservationID: "observation-1",
				Action:        "continue",
				Tool:          "file.write",
				ToolID:        "kernel:file.write",
				ToolInput:     json.RawMessage(`{"path":"report.txt"}`),
				Output:        ToolOutput{Content: "written"},
			}},
			expectedToolName: TerminalRunToolName,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			state := nativeAgentActionContractState()
			state.Observations = testCase.observations
			provider := nativeAgentActionLanguageModel{
				chatErrors: []error{correctionError, nil},
				chatResponses: []llm.ChatCompletionResponse{
					{},
					nativeAgentActionChatResponse(testCase.expectedToolName, `{}`),
				},
			}

			_, errorValue := DecideAgentAction(context.Background(), &provider, state)
			if errorValue != nil {
				t.Fatalf("expected corrected native action: %v", errorValue)
			}
			if string(provider.chatRequests[0].ToolChoice) != `"required"` {
				t.Fatalf("expected initial model choice to remain required, got %s", provider.chatRequests[0].ToolChoice)
			}
			retryRequest := provider.chatRequests[1]
			if len(retryRequest.Tools) != 1 || retryRequest.Tools[0].Function.Name != testCase.expectedToolName {
				t.Fatalf("expected first pending contract operation %q, got %+v", testCase.expectedToolName, retryRequest.Tools)
			}
			if string(retryRequest.ToolChoice) != `"required"` || retryRequest.ParallelToolCalls {
				t.Fatalf("expected portable single-tool requirement, got choice=%s parallel=%t", retryRequest.ToolChoice, retryRequest.ParallelToolCalls)
			}
		})
	}
}

func TestAgentActionFinishCorrectionUsesCompleteTypedState(t *testing.T) {
	testCases := []struct {
		name          string
		updateState   func(*agentTaskState)
		expectsFinish bool
	}{
		{name: "complete contract and effect", expectsFinish: true},
		{
			name:        "missing evidence",
			updateState: func(state *agentTaskState) { state.Observations = nil },
		},
		{
			name:        "missing required effect",
			updateState: func(state *agentTaskState) { state.Observations[0].Effects = nil },
		},
		{
			name:          "message expected result ready for verification",
			expectsFinish: true,
			updateState: func(state *agentTaskState) {
				state.Request.OutcomeContract.ExpectedResults = []ExpectedResult{{
					Type:        ExpectedResultTypeMessage,
					Description: "final reply",
					Required:    true,
				}}
			},
		},
		{
			name: "file expected result missing attachment",
			updateState: func(state *agentTaskState) {
				state.Request.OutcomeContract.ExpectedResults = []ExpectedResult{{
					Type:        ExpectedResultTypeFile,
					Description: "attached report",
					Required:    true,
				}}
			},
		},
		{
			name: "recovery pending",
			updateState: func(state *agentTaskState) {
				state.Observations[0].RecoveryPacket = &RecoveryPacket{AllowedTools: []string{"task.list"}}
			},
		},
		{
			name:        "user input pending",
			updateState: func(state *agentTaskState) { state.PendingWait = &agentPendingWait{Kind: agentPendingWaitUserInput} },
		},
		{
			name: "failed evidence",
			updateState: func(state *agentTaskState) {
				state.Observations[0].Failure = &ToolFailure{Code: FailureCodes.OperationFailed.String()}
			},
		},
		{
			name: "failure debt",
			updateState: func(state *agentTaskState) {
				state.Observations = append(state.Observations, turnObservation{
					ObservationID: "observation-2",
					Action:        "continue",
					Tool:          "task.add",
					ToolInputKey:  "task.add\x00{}",
					Failure:       &ToolFailure{Code: FailureCodes.OperationFailed.String()},
				})
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			state := nativeAgentActionCompletionReadyState()
			if testCase.updateState != nil {
				testCase.updateState(&state)
			}

			retryRequest := finishReasonRetryRequest(t, state)
			isFinishRequired := len(retryRequest.Tools) == 1 && retryRequest.Tools[0].Function.Name == "finish"
			if isFinishRequired != testCase.expectsFinish {
				t.Fatalf("expected finish required=%t, got %+v", testCase.expectsFinish, retryRequest.Tools)
			}
			if isFinishRequired {
				assertRequiredAgentActionTool(t, retryRequest, "finish")
			}
		})
	}
}

func TestAgentActionFinishCorrectionPrecedenceAndFailClosed(t *testing.T) {
	t.Run("pending operation precedes finish", func(t *testing.T) {
		state := nativeAgentActionCompletionReadyState()
		state.Request.OutcomeContract.OperationContract.Requirements = append(
			state.Request.OutcomeContract.OperationContract.Requirements,
			taskAddOperationRequirement("operation-2", "second"),
		)

		assertRequiredAgentActionTool(t, finishReasonRetryRequest(t, state), "task.add")
	})

	t.Run("required next tool without input contract", func(t *testing.T) {
		state := nativeAgentActionContractState()
		state.Request.OutcomeContract.OperationContract = nil
		state.Request.ContractToolWorkingSet.RequiredNextTools = []string{"file.write", TerminalRunToolName}

		assertRequiredAgentActionTool(t, finishReasonRetryRequest(t, state), "file.write")
	})

	t.Run("finish absent from request", func(t *testing.T) {
		state := nativeAgentActionCompletionReadyState()
		request := nativeAgentActionChatCompletionRequest(t, state)
		request.Tools = slices.DeleteFunc(request.Tools, func(tool llm.ChatCompletionTool) bool {
			return tool.Function.Name == "finish"
		})

		_, canRetry := retryAgentActionChatCompletionRequest(request, finishReasonCorrection(), state)
		if canRetry {
			t.Fatal("expected completion-ready correction without finish to fail closed")
		}
	})
}

func TestDecideAgentActionNativeChatRetryPreservesModelChoiceOutsidePendingContract(t *testing.T) {
	correctionError := testStructuredOutputCorrectionError{correction: llm.StructuredOutputCorrection{
		Code: "structured_output_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{
			Category:     llm.StructuredOutputDiagnosticFinishReason,
			FinishReason: "stop",
		},
	}}
	testCases := []struct {
		name         string
		updateState  func(agentTaskState) agentTaskState
		expectedCall string
	}{
		{
			name: "contract satisfied",
			updateState: func(state agentTaskState) agentTaskState {
				state.Observations = []turnObservation{
					successfulContractObservation("observation-1", "file.write", "kernel:file.write", `{"path":"report.txt"}`),
					successfulContractObservation("observation-2", TerminalRunToolName, "kernel:terminal.run", `{"command":"wc report.txt"}`),
				}
				return state
			},
			expectedCall: "finish",
		},
		{
			name: "failure debt",
			updateState: func(state agentTaskState) agentTaskState {
				state.Observations = []turnObservation{{
					ObservationID: "observation-1",
					Action:        "continue",
					Tool:          "file.write",
					ToolInputKey:  "file.write\x00{}",
					Failure:       &ToolFailure{Code: "write_failed"},
				}}
				return state
			},
			expectedCall: "fail",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			state := testCase.updateState(nativeAgentActionContractState())
			provider := nativeAgentActionLanguageModel{
				chatErrors: []error{correctionError, nil},
				chatResponses: []llm.ChatCompletionResponse{
					{},
					nativeAgentActionChatResponse(testCase.expectedCall, `{}`),
				},
			}

			_, errorValue := DecideAgentAction(context.Background(), &provider, state)
			if errorValue != nil {
				t.Fatalf("expected corrected native action: %v", errorValue)
			}
			retryRequest := provider.chatRequests[1]
			if string(retryRequest.ToolChoice) != `"required"` || len(retryRequest.Tools) <= 1 {
				t.Fatalf("expected model choice to remain open, got choice=%s tools=%+v", retryRequest.ToolChoice, retryRequest.Tools)
			}
		})
	}
}

func TestDecideAgentActionNativeChatRetryFailsClosedWhenContractToolIsUnavailable(t *testing.T) {
	correctionError := testStructuredOutputCorrectionError{correction: llm.StructuredOutputCorrection{
		Code:       "structured_output_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{Category: llm.StructuredOutputDiagnosticFinishReason, FinishReason: "stop"},
	}}
	state := nativeAgentActionContractState()
	state.Request.OutcomeContract.OperationContract.Requirements[0].ToolName = "missing.tool"
	provider := nativeAgentActionLanguageModel{chatError: correctionError}

	_, errorValue := DecideAgentAction(context.Background(), &provider, state)

	if errorValue == nil || errorValue.Error() != correctionError.Error() {
		t.Fatalf("expected original correction error, got %v", errorValue)
	}
	if provider.chatCalls != 1 || provider.structuredCalls != 0 {
		t.Fatalf("expected unavailable contract tool to fail closed, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
	}
}

func TestFirstPendingActionToolNameRequiresDistinctObservations(t *testing.T) {
	state := nativeAgentActionContractState()
	state.Request.OutcomeContract.OperationContract.Requirements = []OperationRequirement{
		{
			RequirementID: "operation-1",
			ToolID:        "kernel:file.write",
			ToolName:      "file.write",
			InputMode:     OperationInputContainsExplicit,
			RequiredInput: json.RawMessage(`{"path":"report.txt"}`),
		},
		{
			RequirementID: "operation-2",
			ToolID:        "kernel:file.write",
			ToolName:      "file.write",
			InputMode:     OperationInputContainsExplicit,
			RequiredInput: json.RawMessage(`{"path":"report.txt"}`),
		},
	}
	firstObservation := successfulContractObservation("observation-1", "file.write", "kernel:file.write", `{"path":"report.txt"}`)
	state.Observations = []turnObservation{firstObservation}

	if toolName := firstPendingActionToolName(state); toolName != "file.write" {
		t.Fatalf("expected the repeated occurrence to remain pending, got %q", toolName)
	}

	secondObservation := firstObservation
	secondObservation.ObservationID = "observation-2"
	state.Observations = append(state.Observations, secondObservation)
	if toolName := firstPendingActionToolName(state); toolName != "" {
		t.Fatalf("expected two observations to satisfy two occurrences, got %q", toolName)
	}
}

func TestFirstPendingActionToolNameUsesRequiredNextToolsWithoutInputContract(t *testing.T) {
	state := nativeAgentActionContractState()
	state.Request.OutcomeContract.OperationContract = nil
	state.Request.ContractToolWorkingSet.RequiredNextTools = []string{"file.write", TerminalRunToolName}

	if toolName := firstPendingActionToolName(state); toolName != "file.write" {
		t.Fatalf("expected first required next tool, got %q", toolName)
	}

	state.Observations = []turnObservation{
		successfulContractObservation("observation-1", TerminalRunToolName, "kernel:terminal.run", `{"command":"ls"}`),
		successfulContractObservation("observation-2", "file.write", "kernel:file.write", `{"path":"report.txt"}`),
		{
			ObservationID: "observation-3",
			Action:        "continue",
			Tool:          TerminalRunToolName,
			ToolInputKey:  TerminalRunToolName + "\x00{}",
			Failure:       &ToolFailure{Code: FailureCodes.OperationFailed.String()},
		},
	}
	if toolName := firstPendingRequiredToolName(nil, state.Request.ContractToolWorkingSet.RequiredNextTools, state.Observations); toolName != TerminalRunToolName {
		t.Fatalf("expected out-of-order and failed observations not to advance the sequence, got %q", toolName)
	}
	if toolName := firstPendingActionToolName(state); toolName != "" {
		t.Fatalf("expected failure debt to leave recovery choice open, got %q", toolName)
	}
}

func TestDecideAgentActionNativeChatSucceedsAfterTwoCorrections(t *testing.T) {
	finishReasonError := testStructuredOutputCorrectionError{correction: llm.StructuredOutputCorrection{
		Code: "structured_output_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{
			Category:     llm.StructuredOutputDiagnosticFinishReason,
			FinishReason: llm.StructuredOutputDiagnosticFinishStop,
		},
	}}
	schemaValidationError := testStructuredOutputCorrectionError{correction: llm.StructuredOutputCorrection{
		Code: "provider_response_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{
			Category: llm.StructuredOutputDiagnosticSchemaValidation,
			ToolName: "file.write",
			ValidationIssues: []llm.StructuredOutputValidationIssue{
				{FieldPath: "/content_type", Code: llm.StructuredOutputValidationAdditionalProperty},
				{FieldPath: "/summary", Code: llm.StructuredOutputValidationAdditionalProperty},
			},
			RepairStatus: llm.StructuredOutputRepairFailed,
		},
	}}
	provider := nativeAgentActionLanguageModel{
		chatErrors: []error{finishReasonError, schemaValidationError, nil},
		chatResponses: []llm.ChatCompletionResponse{
			{},
			{},
			nativeAgentActionChatResponse("file.write", `{}`),
		},
	}

	action, errorValue := DecideAgentAction(context.Background(), &provider, nativeAgentActionContractState())

	if errorValue != nil {
		t.Fatalf("expected corrected native action: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != "file.write" {
		t.Fatalf("expected corrected file.write action, got %+v", action)
	}
	if provider.chatCalls != 3 || provider.structuredCalls != 0 {
		t.Fatalf("expected three native calls without structured fallback, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
	}
	for requestIndex := 1; requestIndex < len(provider.chatRequests); requestIndex++ {
		request := provider.chatRequests[requestIndex]
		if len(request.Tools) != 1 || request.Tools[0].Function.Name != "file.write" {
			t.Fatalf("expected retry %d to stay on exact file.write, got %+v", requestIndex, request.Tools)
		}
		if string(request.ToolChoice) != `"required"` || request.ParallelToolCalls {
			t.Fatalf("expected retry %d portable single-tool requirement, got choice=%s parallel=%t", requestIndex, request.ToolChoice, request.ParallelToolCalls)
		}
	}
	lastMessage := provider.chatRequests[2].Messages[len(provider.chatRequests[2].Messages)-1].Content
	if !strings.Contains(lastMessage, "/content_type (additional_property)") || !strings.Contains(lastMessage, "/summary (additional_property)") {
		t.Fatalf("expected exact schema correction fields, got %s", lastMessage)
	}
}

func TestDecideAgentActionNativeChatStopsAfterTwoCorrections(t *testing.T) {
	correctionError := testStructuredOutputCorrectionError{correction: llm.StructuredOutputCorrection{
		Code: "provider_response_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{
			Category: llm.StructuredOutputDiagnosticToolCallContract,
			ToolName: "terminal.run",
		},
	}}
	finalError := testStructuredOutputCorrectionError{correction: llm.StructuredOutputCorrection{
		Code:       "third_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{Category: llm.StructuredOutputDiagnosticToolCallContract, ToolName: "terminal.run"},
	}}
	provider := nativeAgentActionLanguageModel{chatErrors: []error{correctionError, correctionError, finalError}}

	_, errorValue := DecideAgentAction(context.Background(), &provider, nativeAgentActionTestState())

	if errorValue == nil || errorValue.Error() != finalError.Error() {
		t.Fatalf("expected third invalid response to fail closed, got %v", errorValue)
	}
	if provider.chatCalls != 3 || provider.structuredCalls != 0 {
		t.Fatalf("expected exactly three native calls without structured fallback, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
	}
}

func TestDecideAgentActionNativeChatStopsCorrectionLoopOnCancellation(t *testing.T) {
	correctionError := testStructuredOutputCorrectionError{correction: llm.StructuredOutputCorrection{
		Code: "provider_response_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{
			Category: llm.StructuredOutputDiagnosticToolCallContract,
			ToolName: TerminalRunToolName,
		},
	}}
	provider := nativeAgentActionLanguageModel{chatErrors: []error{correctionError, context.Canceled, nil}}

	_, errorValue := DecideAgentAction(context.Background(), &provider, nativeAgentActionTestState())

	if !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", errorValue)
	}
	if provider.chatCalls != 2 || provider.structuredCalls != 0 {
		t.Fatalf("expected cancellation to stop corrections immediately, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
	}
}

func TestDecideAgentActionNativeChatRejectsUnknownDiagnosticTool(t *testing.T) {
	correctionError := testStructuredOutputCorrectionError{correction: llm.StructuredOutputCorrection{
		Code: "provider_response_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{
			Category: llm.StructuredOutputDiagnosticJSONParse,
			ToolName: "unknown.tool",
		},
	}}
	provider := nativeAgentActionLanguageModel{chatError: correctionError}

	_, errorValue := DecideAgentAction(context.Background(), &provider, nativeAgentActionTestState())

	if errorValue == nil || errorValue.Error() != correctionError.Error() {
		t.Fatalf("expected original correction error, got %v", errorValue)
	}
	if provider.chatCalls != 1 || provider.structuredCalls != 0 {
		t.Fatalf("expected fail-closed native call, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
	}
}

func TestDecideAgentActionNativeChatUsesFirstProviderOrderedCall(t *testing.T) {
	provider := nativeAgentActionLanguageModel{chatResponse: nativeAgentActionMultipleCallsResponse()}

	action, errorValue := DecideAgentAction(context.Background(), &provider, nativeAgentActionTestState())
	if errorValue != nil {
		t.Fatalf("expected native action: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != TerminalRunToolName || string(action.ToolInput) != `{"command":"pwd"}` {
		t.Fatalf("expected first provider-ordered tool call, got %+v", action)
	}
	if provider.chatCalls != 1 || provider.structuredCalls != 0 {
		t.Fatalf("expected one native call without retry or structured fallback, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
	}
}

func TestDecideAgentActionUsesStructuredProviderForMessageParts(t *testing.T) {
	provider := nativeAgentActionLanguageModel{chatResponse: nativeAgentActionChatResponse("finish", `{}`)}
	state := nativeAgentActionTestState()
	state.Request.InputParts = []AgentPart{{
		Type:  AgentPartTypeImage,
		Image: &AgentImagePart{MimeType: "image/png", DataBase64: "aGVsbG8="},
	}}

	action, errorValue := DecideAgentAction(context.Background(), &provider, state)
	if errorValue != nil || action.Action != "finish" {
		t.Fatalf("expected structured action for message parts, got %+v, %v", action, errorValue)
	}
	if provider.chatCalls != 0 || provider.structuredCalls != 1 {
		t.Fatalf("expected only one structured call for message parts, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
	}
}

func TestDecideAgentActionNativeChatPropagatesProviderErrorAndCancellation(t *testing.T) {
	deadlineContext, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	correctionError := testStructuredOutputCorrectionError{correction: llm.StructuredOutputCorrection{
		Code:       "provider_response_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{Category: llm.StructuredOutputDiagnosticJSONParse},
	}}
	testCases := []struct {
		name      string
		context   context.Context
		chatError error
	}{
		{name: "provider error", context: context.Background(), chatError: errors.New("native provider failed")},
		{name: "cancellation", context: cancelledContext(), chatError: context.Canceled},
		{name: "cancellation during correction", context: cancelledContext(), chatError: correctionError},
		{name: "deadline", context: deadlineContext, chatError: context.DeadlineExceeded},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := nativeAgentActionLanguageModel{chatError: testCase.chatError}
			_, errorValue := DecideAgentAction(testCase.context, &provider, nativeAgentActionTestState())
			if testCase.name != "cancellation during correction" && !errors.Is(errorValue, testCase.chatError) {
				t.Fatalf("expected direct provider error %v, got %v", testCase.chatError, errorValue)
			}
			if provider.chatCalls != 1 || provider.structuredCalls != 0 {
				t.Fatalf("expected no structured fallback, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
			}
		})
	}
}

func TestDecideAgentActionUsesStructuredProviderWithoutChatCapability(t *testing.T) {
	provider := structuredOnlyAgentActionLanguageModel{
		response: llm.StructuredResponse{Content: `{"action":"finish","message":"done"}`},
	}
	action, errorValue := DecideAgentAction(context.Background(), &provider, agentTaskState{})
	if errorValue != nil || action.Action != "finish" {
		t.Fatalf("expected structured action fallback for provider without chat, got %+v, %v", action, errorValue)
	}
	if provider.structuredCalls != 1 {
		t.Fatalf("expected one structured call, got %d", provider.structuredCalls)
	}
}

func nativeAgentActionTestState() agentTaskState {
	toolSet := NewToolSet([]string{TerminalRunToolName})
	registerTestTool(toolSet, ToolDefinition{
		Name:        TerminalRunToolName,
		Description: "Run a command.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ran"), nil
	})
	return agentTaskState{Request: AgentTurnRequest{Prompt: "run command", ToolSet: toolSet}}
}

func nativeAgentActionTestStateWithTools(toolNames ...string) agentTaskState {
	toolSet := NewToolSet(toolNames)
	for _, toolName := range toolNames {
		registerTestTool(toolSet, ToolDefinition{
			Name:        toolName,
			Description: "Test tool.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}
	return agentTaskState{Request: AgentTurnRequest{Prompt: "use a tool", ToolSet: toolSet}}
}

func nativeAgentActionContractState() agentTaskState {
	state := nativeAgentActionTestStateWithTools("file.write", TerminalRunToolName)
	state.Request.OutcomeContract.OperationContract = &OperationContract{
		Version: operationContractVersion,
		Requirements: []OperationRequirement{
			{
				RequirementID: "operation-1",
				ToolID:        "kernel:file.write",
				ToolName:      "file.write",
				InputMode:     OperationInputContainsExplicit,
				RequiredInput: json.RawMessage(`{"path":"report.txt"}`),
			},
			{
				RequirementID: "operation-2",
				ToolID:        "kernel:terminal.run",
				ToolName:      TerminalRunToolName,
				InputMode:     OperationInputNoExplicitValues,
				RequiredInput: json.RawMessage(`{}`),
			},
		},
	}
	return state
}

func nativeAgentActionCompletionReadyState() agentTaskState {
	toolDefinition := testToolDescriptor("task.add")
	toolDefinition.SideEffectClass = ToolSideEffectStateChange
	toolDefinition.Completion = ToolCompletion{Mode: ToolCompletionObservation, Action: "add_task", TargetKind: "task"}
	toolDefinition.ResultContract = &ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"},"created":{"type":"boolean"}},"required":["taskID","created"],"additionalProperties":false}`),
		Effects: []ResourceEffectContract{{
			ObjectType:     "task",
			Effect:         "created",
			ResultField:    "taskID",
			EffectIdentity: "id",
		}},
		EvidenceCondition: &EvidenceCondition{
			ResultField: "created",
			Equals:      json.RawMessage(`true`),
		},
	}
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{toolDefinition})
	return agentTaskState{
		Request: AgentTurnRequest{
			Prompt:                "add task",
			ToolSet:               toolSet,
			RequiredEvidenceTools: []string{"task.add"},
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{"task.add"},
				RequiredEffects: []OutcomeEffect{{
					ObjectType: "task",
					Effect:     "created",
				}},
				OperationContract: &OperationContract{
					Version: operationContractVersion,
					Requirements: []OperationRequirement{
						taskAddOperationRequirement("operation-1", "first"),
					},
				},
			},
		},
		Observations: []turnObservation{{
			ObservationID: "observation-1",
			Action:        "continue",
			Tool:          "task.add",
			ToolID:        toolDefinition.ID,
			ToolInput:     json.RawMessage(`{"title":"first"}`),
			Output:        ToolOutput{Content: "added", Data: json.RawMessage(`{"taskID":"task-1","created":true}`)},
			Effects:       []ResourceEffect{{ObjectType: "task", Effect: "created", ID: "task-1"}},
		}},
	}
}

func taskAddOperationRequirement(requirementID string, title string) OperationRequirement {
	return OperationRequirement{
		RequirementID: requirementID,
		ToolID:        "test:task.add",
		ToolName:      "task.add",
		InputMode:     OperationInputContainsExplicit,
		RequiredInput: json.RawMessage(`{"title":"` + title + `"}`),
	}
}

func finishReasonRetryRequest(t *testing.T, state agentTaskState) llm.ChatCompletionRequest {
	t.Helper()
	request := nativeAgentActionChatCompletionRequest(t, state)
	retryRequest, canRetry := retryAgentActionChatCompletionRequest(request, finishReasonCorrection(), state)
	if !canRetry {
		t.Fatal("expected finish-reason correction")
	}
	return retryRequest
}

func nativeAgentActionChatCompletionRequest(t *testing.T, state agentTaskState) llm.ChatCompletionRequest {
	t.Helper()
	requestSource := buildAgentActionRequest(state, false)
	request, isRepresentable := buildAgentActionChatCompletionRequest(requestSource)
	if !isRepresentable {
		t.Fatal("expected native action request")
	}
	return request
}

func assertRequiredAgentActionTool(t *testing.T, request llm.ChatCompletionRequest, toolName string) {
	t.Helper()
	if len(request.Tools) != 1 || request.Tools[0].Function.Name != toolName {
		t.Fatalf("expected only %q, got %+v", toolName, request.Tools)
	}
	if string(request.ToolChoice) != `"required"` || request.ParallelToolCalls {
		t.Fatalf("expected portable single-tool requirement, got choice=%s parallel=%t", request.ToolChoice, request.ParallelToolCalls)
	}
}

func finishReasonCorrection() llm.StructuredOutputCorrection {
	return llm.StructuredOutputCorrection{
		Code: "structured_output_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{
			Category:     llm.StructuredOutputDiagnosticFinishReason,
			FinishReason: llm.StructuredOutputDiagnosticFinishStop,
		},
	}
}

func successfulContractObservation(observationID string, toolName string, toolID string, toolInput string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        "continue",
		Tool:          toolName,
		ToolID:        toolID,
		ToolInput:     json.RawMessage(toolInput),
		Output:        ToolOutput{Content: "succeeded"},
	}
}

func nativeAgentActionChatResponse(toolName string, arguments string) llm.ChatCompletionResponse {
	return llm.ChatCompletionResponse{
		FinishReason: "tool_calls",
		Message: llm.ChatCompletionMessage{
			Role:      "assistant",
			ToolCalls: []llm.ChatCompletionToolCall{nativeAgentActionToolCall(toolName, arguments)},
		},
	}
}

func nativeAgentActionMultipleCallsResponse() llm.ChatCompletionResponse {
	return llm.ChatCompletionResponse{
		FinishReason: "tool_calls",
		Message: llm.ChatCompletionMessage{
			Role: "assistant",
			ToolCalls: []llm.ChatCompletionToolCall{
				nativeAgentActionToolCall(TerminalRunToolName, `{"command":"pwd"}`),
				nativeAgentActionToolCall("finish", `{}`),
			},
		},
	}
}

func nativeAgentActionToolCall(toolName string, arguments string) llm.ChatCompletionToolCall {
	return llm.ChatCompletionToolCall{
		ID:   "call-1",
		Type: "function",
		Function: llm.ChatCompletionToolCallFunction{
			Name:      toolName,
			Arguments: arguments,
		},
	}
}

func nativeChatTool(t *testing.T, tools []llm.ChatCompletionTool, toolName string) llm.ChatCompletionTool {
	t.Helper()
	for _, tool := range tools {
		if tool.Function.Name == toolName {
			return tool
		}
	}
	t.Fatalf("expected native tool %q in %+v", toolName, tools)
	return llm.ChatCompletionTool{}
}

func cancelledContext() context.Context {
	contextValue, cancel := context.WithCancel(context.Background())
	cancel()
	return contextValue
}

type testStructuredOutputCorrectionError struct {
	correction llm.StructuredOutputCorrection
}

func (errorValue testStructuredOutputCorrectionError) Error() string {
	return errorValue.correction.Code
}

func (errorValue testStructuredOutputCorrectionError) StructuredOutputCorrection() (llm.StructuredOutputCorrection, bool) {
	return errorValue.correction, true
}

type nativeAgentActionLanguageModel struct {
	chatResponse    llm.ChatCompletionResponse
	chatError       error
	chatResponses   []llm.ChatCompletionResponse
	chatErrors      []error
	chatCalls       int
	structuredCalls int
	lastRequest     llm.ChatCompletionRequest
	chatRequests    []llm.ChatCompletionRequest
}

func (provider *nativeAgentActionLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (provider *nativeAgentActionLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	provider.structuredCalls++
	return provider.chatResponseAsStructured(), nil
}

func (provider *nativeAgentActionLanguageModel) GenerateChatCompletion(_ context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	callIndex := provider.chatCalls
	provider.chatCalls++
	provider.lastRequest = request
	provider.chatRequests = append(provider.chatRequests, request)
	response := provider.chatResponse
	if callIndex < len(provider.chatResponses) {
		response = provider.chatResponses[callIndex]
	}
	errorValue := provider.chatError
	if callIndex < len(provider.chatErrors) {
		errorValue = provider.chatErrors[callIndex]
	}
	return response, errorValue
}

func (provider *nativeAgentActionLanguageModel) chatResponseAsStructured() llm.StructuredResponse {
	return llm.StructuredResponse{Content: `{"action":"finish","message":"done"}`}
}

func chatMessageContent(messages []llm.ChatCompletionMessage) string {
	contents := make([]string, 0, len(messages))
	for _, message := range messages {
		contents = append(contents, message.Content)
	}
	return strings.Join(contents, "\n")
}

type structuredOnlyAgentActionLanguageModel struct {
	response        llm.StructuredResponse
	structuredCalls int
}

func (provider *structuredOnlyAgentActionLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (provider *structuredOnlyAgentActionLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	provider.structuredCalls++
	return provider.response, nil
}

func TestBuildAgentActionRequestPreservesNativeToolCallingWireShape(t *testing.T) {
	seed := int64(77)
	temperature := 0.4
	toolSet := NewToolSet([]string{TerminalRunToolName, "site.publish"})
	registerTestTool(toolSet, ToolDefinition{
		Name:        TerminalRunToolName,
		Description: "Run a command.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ran"), nil
	})
	registerTestTool(toolSet, ToolDefinition{
		Name:        "site.publish",
		Description: "Publish a site.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"required":["siteID"],"additionalProperties":false}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("published"), nil
	})
	state := agentTaskState{
		Request: AgentTurnRequest{
			RequesterPersonID: "person-1",
			ConversationID:    "conversation-1",
			Prompt:            "publish it",
			VisibleContext: VisibleContext{Messages: []VisibleContextMessage{{
				Speaker: "Lee",
				Text:    "Please publish the site.",
			}}},
			ToolSet: toolSet,
		},
		Options: TurnOptions{GenerationOptions: llm.GenerationOptions{
			Seed:        &seed,
			Temperature: &temperature,
		}},
	}

	request := BuildAgentActionRequest(state)

	if request.StructuredOutputSchema.Name != "blueclaw_agent_turn_action" {
		t.Fatalf("expected agent action schema name, got %q", request.StructuredOutputSchema.Name)
	}
	if !request.StructuredOutputSchema.IsStrictlyEnforced {
		t.Fatal("expected agent action schema to be strictly enforced")
	}
	if request.GenerationOptions.Seed == nil || *request.GenerationOptions.Seed != seed {
		t.Fatalf("expected seed to be preserved, got %+v", request.GenerationOptions)
	}
	if request.GenerationOptions.Temperature == nil || *request.GenerationOptions.Temperature != temperature {
		t.Fatalf("expected temperature to be preserved, got %+v", request.GenerationOptions)
	}
	var schemaDocument struct {
		OneOf []map[string]any `json:"oneOf"`
	}
	if errorValue := json.Unmarshal([]byte(request.StructuredOutputSchema.Document), &schemaDocument); errorValue != nil {
		t.Fatalf("expected action schema JSON: %v", errorValue)
	}
	if len(schemaDocument.OneOf) == 0 {
		t.Fatal("expected action schema oneOf variants")
	}
	for _, variant := range schemaDocument.OneOf {
		properties := mapFromAny(variant["properties"])
		actionValues := stringSliceFromAny(mapFromAny(properties["action"])["enum"])
		if len(actionValues) != 1 {
			t.Fatalf("expected one action discriminator per variant, got %+v", actionValues)
		}
		if actionValues[0] != "continue" {
			continue
		}
		toolNameValues := stringSliceFromAny(mapFromAny(properties["toolName"])["enum"])
		if len(toolNameValues) != 1 {
			t.Fatalf("expected one toolName discriminator per continue variant, got %+v", toolNameValues)
		}
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"action":{"enum":["continue"]`) {
		t.Fatalf("expected continue action variant, got %s", request.StructuredOutputSchema.Document)
	}
	if strings.Contains(request.StructuredOutputSchema.Document, `"call_tool"`) || strings.Contains(request.StructuredOutputSchema.Document, `"final_reply"`) || strings.Contains(request.StructuredOutputSchema.Document, `"finalReply"`) {
		t.Fatalf("expected model-facing schema to omit legacy action aliases, got %s", request.StructuredOutputSchema.Document)
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolName":{"enum":["terminal.run"]`) {
		t.Fatalf("expected kernel toolName enum to be preserved, got %s", request.StructuredOutputSchema.Document)
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolName":{"enum":["site.publish"]`) {
		t.Fatalf("expected selected domain operation to remain in the model-facing schema, got %s", request.StructuredOutputSchema.Document)
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolInput"`) {
		t.Fatalf("expected toolInput to be preserved, got %s", request.StructuredOutputSchema.Document)
	}
	if strings.Contains(request.StructuredOutputSchema.Document, `"nextStepPlan"`) {
		t.Fatalf("expected continue action to omit nextStepPlan, got %s", request.StructuredOutputSchema.Document)
	}
	if strings.Contains(request.StructuredOutputSchema.Document, `"requestTools"`) {
		t.Fatalf("expected continue action to omit requestTools, got %s", request.StructuredOutputSchema.Document)
	}
	finishVariant := actionSchemaVariant(t, request.StructuredOutputSchema.Document, "finish")
	requiredFields := stringSliceFromAny(finishVariant["required"])
	for _, fieldName := range []string{"message", "completionEvidenceIDs", "qualityReview"} {
		if !containsString(requiredFields, fieldName) {
			t.Fatalf("expected finish schema to require %s, got %+v", fieldName, requiredFields)
		}
	}
	if containsString(requiredFields, "executionStateUpdate") {
		t.Fatalf("expected terminal execution state update to remain optional, got %+v", requiredFields)
	}
	finishProperties := mapFromAny(finishVariant["properties"])
	qualityReviewItems := mapFromAny(mapFromAny(finishProperties["qualityReview"])["items"])
	if qualityReviewItems["additionalProperties"] != false {
		t.Fatalf("expected quality review items to reject undeclared fields, got %+v", qualityReviewItems)
	}
	qualityReviewProperties := mapFromAny(qualityReviewItems["properties"])
	if _, isPresent := qualityReviewProperties["evidenceIDs"]; !isPresent {
		t.Fatalf("expected quality review items to expose evidenceIDs, got %+v", qualityReviewProperties)
	}
	if _, isPresent := qualityReviewProperties["evidence"]; isPresent {
		t.Fatalf("expected quality review items to omit legacy evidence, got %+v", qualityReviewProperties)
	}
	if strings.Contains(request.StructuredOutputSchema.Document, `"finishMessage"`) {
		t.Fatalf("expected model-facing schema to omit legacy finishMessage, got %s", request.StructuredOutputSchema.Document)
	}
	if strings.Contains(request.StructuredOutputSchema.Document, `"action":{"enum":["tool.request"]`) {
		t.Fatalf("expected tool.request action to stay hidden, got %s", request.StructuredOutputSchema.Document)
	}
	if strings.Contains(request.StructuredOutputSchema.Document, "require_capabilities") {
		t.Fatalf("expected model-facing schema to omit require_capabilities, got %s", request.StructuredOutputSchema.Document)
	}
	if !messagesContain(request.Messages, "Recent visible conversation context") {
		t.Fatalf("expected visible context in model messages, got %+v", request.Messages)
	}
}

func TestBuildAgentActionRequestPreservesTypedInteractionTool(t *testing.T) {
	toolSet := NewToolSet([]string{AskInputToolName})
	registerTestTool(toolSet, ToolDefinition{
		Name:        AskInputToolName,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}},"required":["question"]}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("waiting"), nil
	})

	request := BuildAgentActionRequest(agentTaskState{Request: AgentTurnRequest{ToolSet: toolSet}})

	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolName":{"enum":["ask.input"]`) {
		t.Fatalf("expected typed ask.input exposure to remain in the action schema, got %s", request.StructuredOutputSchema.Document)
	}
}

func TestDirectActionSchemaPreservesToolRequiredFields(t *testing.T) {
	schemaDocument := buildActionSchemaFromToolDefinitions([]ToolDefinition{{
		Name:        "calendar.add",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
	}}, false, nil, false, false)

	continueVariant := actionSchemaVariant(t, schemaDocument, "continue")
	properties := mapFromAny(continueVariant["properties"])
	toolInput := mapFromAny(properties["toolInput"])
	requiredFields := stringSliceFromAny(toolInput["required"])
	if !containsString(requiredFields, "title") {
		t.Fatalf("expected direct tool input fields to stay required, got %+v in %s", requiredFields, schemaDocument)
	}
}

func TestActionSchemaOmitsToolsWithoutAnObjectInputSchema(t *testing.T) {
	schemaDocument := buildActionSchemaFromToolDefinitions([]ToolDefinition{
		{Name: "missing.schema"},
		{Name: "invalid.schema", InputSchema: json.RawMessage(`{"type":`)},
		{Name: "scalar.schema", InputSchema: json.RawMessage(`{"type":"string"}`)},
		{Name: "valid.schema", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
	}, false, nil, false, false)

	if strings.Contains(schemaDocument, "missing.schema") {
		t.Fatalf("expected missing schema tool to be omitted, got %s", schemaDocument)
	}
	if strings.Contains(schemaDocument, "invalid.schema") {
		t.Fatalf("expected invalid schema tool to be omitted, got %s", schemaDocument)
	}
	if strings.Contains(schemaDocument, "scalar.schema") {
		t.Fatalf("expected non-object schema tool to be omitted, got %s", schemaDocument)
	}
	if !strings.Contains(schemaDocument, "valid.schema") {
		t.Fatalf("expected valid object schema tool to remain, got %s", schemaDocument)
	}
}

func TestActionSchemaPreservesRequiredFieldsOnArrayOfNestedObjects(t *testing.T) {
	schemaDocument := buildActionSchemaFromToolDefinitions([]ToolDefinition{{
		Name:        "calendar.add",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}}},"required":["items"]}`),
	}}, false, nil, false, false)

	continueVariant := actionSchemaVariant(t, schemaDocument, "continue")
	properties := mapFromAny(continueVariant["properties"])
	toolInput := mapFromAny(properties["toolInput"])
	topLevelRequired := stringSliceFromAny(toolInput["required"])
	if !containsString(topLevelRequired, "items") {
		t.Fatalf("expected top-level required to include items, got %+v in %s", topLevelRequired, schemaDocument)
	}
	toolInputProperties := mapFromAny(toolInput["properties"])
	itemsProperty := mapFromAny(toolInputProperties["items"])
	arrayItemSchema := mapFromAny(itemsProperty["items"])
	nestedRequired := stringSliceFromAny(arrayItemSchema["required"])
	if !containsString(nestedRequired, "name") {
		t.Fatalf("expected required to be preserved two levels deep on array item objects, got %+v in %s", nestedRequired, schemaDocument)
	}
}

func TestBuildAgentActionRequestGenerationOptionsDoNotChangeSchema(t *testing.T) {
	seed := int64(88)
	temperature := 0.5
	toolSet := NewToolSet([]string{"browser.open"})
	registerTestTool(toolSet, ToolDefinition{Name: "browser.open"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("opened"), nil
	})
	state := agentTaskState{
		Request: AgentTurnRequest{
			Prompt:  "open browser",
			ToolSet: toolSet,
		},
	}
	seededState := state
	seededState.Options.GenerationOptions = llm.GenerationOptions{Seed: &seed, Temperature: &temperature}

	request := BuildAgentActionRequest(state)
	seededRequest := BuildAgentActionRequest(seededState)

	if request.StructuredOutputSchema.Document != seededRequest.StructuredOutputSchema.Document {
		t.Fatalf("expected generation options not to change schema document\nwithout=%s\nwith=%s", request.StructuredOutputSchema.Document, seededRequest.StructuredOutputSchema.Document)
	}
	if request.StructuredOutputSchema.Name != seededRequest.StructuredOutputSchema.Name {
		t.Fatalf("expected generation options not to change schema name")
	}
}

func TestRestoreAgentTaskStateRestoresTaskContextSummary(t *testing.T) {
	events := []task.TaskEvent{{
		Name: taskContextSummaryEventName,
		Body: marshalEventBody(TaskContextSummary{
			ObservationID:                 "context-summary-001",
			CompactedThroughObservationID: "obs-007",
			Goal:                          "finish the site",
			CompletedSteps:                []string{"created the app"},
			Artifacts:                     []string{"/workspace/site/index.html"},
			NextPlan:                      []string{"run verification"},
		}),
	}}

	state, errorValue := restoreAgentTaskState(AgentTurnRequest{Prompt: "continue"}, TurnOptions{}, task.TaskRun{
		TaskRunID: "task-1",
		Status:    task.TaskStatusRunning,
	}, events)

	if errorValue != nil {
		t.Fatalf("expected restore to succeed: %v", errorValue)
	}
	if state.ContextSummary.CompactedThroughObservationID != "obs-007" {
		t.Fatalf("expected context summary to be restored, got %+v", state.ContextSummary)
	}
	if len(state.ContextSummary.Artifacts) != 1 || state.ContextSummary.Artifacts[0] != "/workspace/site/index.html" {
		t.Fatalf("expected artifact path to be preserved, got %+v", state.ContextSummary.Artifacts)
	}
}

func TestParseAgentActionResponseUsesReplyPartsForFinishMessage(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"finish","message":"summary","replyParts":[{"type":"text","text":"done"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":[],"qualityReview":[]}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "finish" || finishActionMessage(action) != "done" {
		t.Fatalf("expected replyParts to provide finish message, got %+v", action)
	}
}

func TestParseAgentActionResponseNormalizesUntypedFinishReplyParts(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"finish","message":"직접 전달합니다.","replyParts":[{"type":"","text":"대기 중 입니다. 차후 첨부로 전달해드리겠습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":[],"qualityReview":[]}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if finishActionMessage(action) != "대기 중 입니다. 차후 첨부로 전달해드리겠습니다." {
		t.Fatalf("expected untyped replyParts text to stay visible, got %+v", action)
	}
}

func TestParseAgentActionResponseCoercesStringCompletionEvidenceIDs(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"finish","message":"완료했습니다.","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":"obs-005, obs-008","qualityReview":[]}`})
	if errorValue != nil {
		t.Fatalf("expected string completionEvidenceIDs to parse: %v", errorValue)
	}
	if len(action.CompletionEvidenceIDs) != 2 || action.CompletionEvidenceIDs[0] != "obs-005" || action.CompletionEvidenceIDs[1] != "obs-008" {
		t.Fatalf("expected coerced evidence IDs, got %+v", action.CompletionEvidenceIDs)
	}
}

func TestParseAgentActionResponseNormalizesNestedFinishBlock(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"executionStateUpdate":{"goal":"answer user"},"finish":{"message":"done","replyParts":[{"type":"text","text":"done"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"],"qualityReview":[{"id":"complete","passed":true,"evidenceIDs":["obs-001"]}]}}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "finish" || finishActionMessage(action) != "done" {
		t.Fatalf("expected nested finish block to normalize, got %+v", action)
	}
	if action.GoalSatisfied == nil || !*action.GoalSatisfied {
		t.Fatalf("expected goalSatisfied to be parsed, got %+v", action.GoalSatisfied)
	}
	if action.ExecutionStateUpdate.Goal != "answer user" {
		t.Fatalf("expected top-level execution state to be preserved, got %+v", action.ExecutionStateUpdate)
	}
	if len(action.CompletionEvidence) != 1 || action.CompletionEvidence[0].ObservationID != "obs-001" {
		t.Fatalf("expected nested completion evidence to expand, got %+v", action.CompletionEvidence)
	}
}

func TestParseAgentActionResponseNormalizesStringGoalSatisfied(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"finish","message":"done","goalStatus":"satisfied","goalSatisfied":"true","completionEvidenceIDs":[],"qualityReview":[]}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.GoalSatisfied == nil || !*action.GoalSatisfied {
		t.Fatalf("expected string boolean to normalize, got %+v", action.GoalSatisfied)
	}
}

func TestParseAgentActionResponseRejectsAmbiguousNestedActionBlocks(t *testing.T) {
	_, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"finish":{"message":"done"},"continue":{"toolName":"browser.open","toolInput":{}}}`})
	if errorValue == nil {
		t.Fatal("expected ambiguous action blocks to be rejected")
	}
}

func TestParseAgentActionResponseExpandsShallowEvidenceIDs(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"finish","message":"done","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"],"qualityReview":[{"id":"done","passed":true,"evidenceIDs":["obs-001"]}]}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if len(action.CompletionEvidence) != 1 || action.CompletionEvidence[0].ObservationID != "obs-001" {
		t.Fatalf("expected completion evidence IDs to expand, got %+v", action.CompletionEvidence)
	}
	if len(action.QualityReview) != 1 || len(action.QualityReview[0].Evidence) != 1 || action.QualityReview[0].Evidence[0].ObservationID != "obs-001" {
		t.Fatalf("expected quality review evidence IDs to expand, got %+v", action.QualityReview)
	}
}

func TestParseAgentActionResponseParsesToolCall(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"continue","toolName":"browser.open","toolInput":{"url":"https://example.com"}}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != "browser.open" {
		t.Fatalf("expected tool call action, got %+v", action)
	}
	if string(action.ToolInput) != `{"url":"https://example.com"}` {
		t.Fatalf("expected tool input to be preserved, got %s", string(action.ToolInput))
	}
}

func TestParseAgentActionResponseNormalizesContinueToolCall(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"continue","toolName":"browser.open","message":"opening it","toolInput":{"url":"https://example.com"}}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != "browser.open" || action.Message != "opening it" {
		t.Fatalf("expected continue action to normalize, got %+v", action)
	}
	if string(action.ToolInput) != `{"url":"https://example.com"}` {
		t.Fatalf("expected tool input to be preserved, got %s", string(action.ToolInput))
	}
}

func TestParseAgentActionResponsePreservesDirectToolName(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":"continue","toolName":"task.add","toolInput":{"title":"분기 결산"}}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.ToolName != "task.add" {
		t.Fatalf("expected direct tool name to stay task.add, got %+v", action)
	}
	if string(action.ToolInput) != `{"title":"분기 결산"}` {
		t.Fatalf("expected direct tool input to be preserved, got %s", action.ToolInput)
	}
}

func TestParseAgentActionResponseRejectsMalformedJSON(t *testing.T) {
	_, errorValue := ParseAgentActionResponse(llm.StructuredResponse{Content: `{"action":`})
	if errorValue == nil {
		t.Fatal("expected malformed JSON error")
	}
}

func TestApplyToolResultAppendsObservationDeterministically(t *testing.T) {
	state := agentTaskState{}
	result := ToolResult{
		Output: ToolOutput{Content: "attached"},
		Attachments: []FileAttachment{{
			DevicePath:  "/tmp/file.html",
			Filename:    "file.html",
			ContentType: "text/html",
		}},
	}

	nextState := applyToolResult(state, ToolInvocation{ToolName: "file.deliver", Input: json.RawMessage(`{"path":"file.html"}`)}, result)

	if len(nextState.Observations) != 1 {
		t.Fatalf("expected one observation, got %+v", nextState.Observations)
	}
	observation := nextState.Observations[0]
	if observation.ObservationID != "obs-001" || observation.Tool != "file.deliver" || observation.ContentText() != "attached" {
		t.Fatalf("unexpected observation: %+v", observation)
	}
	if len(nextState.Attachments) != 1 || nextState.Attachments[0].Filename != "file.html" {
		t.Fatalf("expected attachment to be appended, got %+v", nextState.Attachments)
	}
}

func TestAdvanceAgentTaskReturnsModelCallEffectByDefault(t *testing.T) {
	state := buildInitialAgentTaskState(AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "hello",
	}, TurnOptions{}, "task-1")

	transition := advanceAgentTask(state)

	if transition.Effect.Kind != agentEffectCallModel {
		t.Fatalf("expected model call effect, got %+v", transition.Effect)
	}
	if transition.Effect.ModelCall == nil {
		t.Fatal("expected model call request")
	}
}

func TestAdvanceAgentTaskReturnsAttachExistingArtifactEffect(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactPath := filepath.Join(workspaceRootPath, "report.html")
	if errorValue := os.WriteFile(artifactPath, []byte("<html></html>"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolSet := NewToolSet([]string{FileDeliverToolName})
	registerTestTool(toolSet, ToolDefinition{Name: FileDeliverToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("delivered"), nil
	})
	state := agentTaskState{
		Request: AgentTurnRequest{
			Prompt:                     "HTML 파일 만들어줘",
			ToolSet:                    toolSet,
			WorkspaceRootPath:          workspaceRootPath,
			RequiredEvidenceTools:      []string{FileDeliverToolName},
			RequiredAttachmentSuffixes: []string{".html"},
			TurnStartedAt:              time.Now().Add(-time.Second),
		},
		Requirements: []toolUseRequirement{{
			ToolName:           FileDeliverToolName,
			RequiresAttachment: true,
			AttachmentSuffixes: []string{".html"},
		}},
	}

	transition := advanceAgentTask(state)

	if transition.Effect.Kind != agentEffectContinue {
		t.Fatalf("expected file delivery effect, got %+v", transition.Effect)
	}
	if transition.Effect.ToolCall == nil || transition.Effect.ToolCall.ToolName != FileDeliverToolName {
		t.Fatalf("expected file.deliver tool call, got %+v", transition.Effect.ToolCall)
	}
	if !strings.Contains(string(transition.Effect.ToolCall.Input), artifactPath) {
		t.Fatalf("expected artifact path in tool input, got %s", string(transition.Effect.ToolCall.Input))
	}
}

func TestAdvanceAgentTaskReturnsFinishMessageEffectForSatisfiedBrowserOpen(t *testing.T) {
	state := agentTaskState{
		Request: AgentTurnRequest{
			Prompt:    "open browser",
			TaskShape: TaskShapeBrowserHandoffTask,
			TaskLevel: TaskLevelXLow,
			ToolSet: newTestToolSetWithDefinitions([]ToolDefinition{{
				Name:            "browser.open",
				Namespace:       "browser",
				SideEffectClass: ToolSideEffectConnect,
				Completion:      ToolCompletion{Mode: ToolCompletionObservation, Action: "open_browser", TargetKind: "browser"},
			}}),
			RequiredEvidenceTools: []string{"browser.open"},
		},
		Requirements: []toolUseRequirement{{
			ToolName: "browser.open",
		}},
		Observations: []turnObservation{{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "browser.open",
			Output:        ToolOutput{Content: "opened"},
		}},
	}

	transition := advanceAgentTask(state)

	if transition.Effect.Kind != agentEffectFinish {
		t.Fatalf("expected final reply effect, got %+v", transition.Effect)
	}
	if transition.Effect.Finish == nil {
		t.Fatalf("expected completion finish effect, got %+v", transition.Effect.Finish)
	}
}

func TestRestoreAgentTaskStateRestoresToolProgressOnly(t *testing.T) {
	events := []task.TaskEvent{{
		Name: "tool.browser.open.result",
		Body: `{"observationID":"obs-001","action":"continue","tool":"browser.open","content":"opened","isError":false}`,
	}}

	state, errorValue := restoreAgentTaskState(AgentTurnRequest{Prompt: "continue"}, TurnOptions{}, task.TaskRun{
		TaskRunID: "task-1",
		Status:    task.TaskStatusWaitingUserInput,
	}, events)

	if errorValue != nil {
		t.Fatalf("expected restored state: %v", errorValue)
	}
	if state.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected restored status, got %s", state.Status)
	}
	if len(state.Observations) != 1 || state.Observations[0].Tool != "browser.open" {
		t.Fatalf("expected restored observation, got %+v", state.Observations)
	}
}

func TestBlockedResumeRestoresPriorObservations(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "build site")
	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	taskRunService.AppendTaskEvent(runningTaskRun.TaskRunID, "tool.file.write.result", `{"observationID":"obs-001","action":"continue","tool":"file.write","content":"wrote app","isError":false}`)
	taskRunService.AppendTaskEvent(runningTaskRun.TaskRunID, "tool.file.read.result", `{"observationID":"obs-003","action":"continue","tool":"file.read","content":"read app","isError":false}`)
	blockedTaskRun, errorValue := taskRunService.PauseTaskRun(runningTaskRun.TaskRunID, task.TaskStatusBlocked, "max_iterations")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	resumedTaskRun, errorValue := taskRunService.AdvanceTaskRun(blockedTaskRun.TaskRunID, "assistant")
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	state, errorValue := agentTaskStateForTurn(AgentTurnRequest{
		Prompt:                 "continue",
		ExistingTaskRunID:      resumedTaskRun.TaskRunID,
		IsRuntimeRestartResume: true,
	}, TurnOptions{}, resumedTaskRun, taskRunService.ListTaskEvent(resumedTaskRun.TaskRunID))

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(state.Observations) != 2 || state.Observations[0].Tool != "file.write" || state.Observations[1].Tool != "file.read" {
		t.Fatalf("expected prior observations to be restored, got %+v", state.Observations)
	}
	if state.ToolCallCount != 2 {
		t.Fatalf("expected restored tool call count, got %d", state.ToolCallCount)
	}
	state = applyToolResult(state, ToolInvocation{ToolName: "file.write", Input: json.RawMessage(`{"path":"app.txt","content":"next"}`)}, testToolSuccess("wrote next"))
	if state.Observations[2].ObservationID != "obs-004" {
		t.Fatalf("expected observation IDs to continue after the highest restored ID, got %+v", state.Observations)
	}
}

func TestDecodeLegacyObservationNormalizesMemorySearchFailureCode(t *testing.T) {
	observation, errorValue := decodeTurnObservation([]byte(`{"observationID":"obs-001","action":"continue","tool":"memory.search","content":"memory failed","isError":true,"errorCode":"memory_search_unavailable","failureStage":"graphiti_search","message":"memory failed"}`))
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if !observation.Failed() || observation.FailureCode() != FailureCodes.Unavailable.String() {
		t.Fatalf("expected canonical memory search failure, got %+v", observation)
	}
}

func TestUserResumeClearsInheritedFailureDebt(t *testing.T) {
	observations := []turnObservation{
		{ObservationID: "obs-001", Action: "continue", Tool: "site.create", Output: ToolOutput{Content: `{"siteID":"site-1"}`}},
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          "site.publish",
			Failure:       &ToolFailure{Code: FailureCodes.OperationFailed.String()},
			ToolInputKey:  "site.publish\x00{\"siteID\":\"site-1\"}",
		},
	}
	if _, hasDebt := activeFailureDebt(observations); !hasDebt {
		t.Fatal("setup expected active failure debt before resume")
	}

	userResume := AgentTurnRequest{IsRuntimeRestartResume: true, IsApprovalContinuation: false}
	if !userResumeClearsInheritedFailureDebt(userResume, observations) {
		t.Fatal("expected user-driven resume to clear inherited failure debt")
	}
	approvalResume := AgentTurnRequest{IsRuntimeRestartResume: true, IsApprovalContinuation: true}
	if userResumeClearsInheritedFailureDebt(approvalResume, observations) {
		t.Fatal("expected approval continuation to retain failure debt")
	}
	autoStart := AgentTurnRequest{IsRuntimeRestartResume: false}
	if userResumeClearsInheritedFailureDebt(autoStart, observations) {
		t.Fatal("expected non-resume turn to be unaffected")
	}

	cleared := observationsWithoutFailures(observations)
	if _, hasDebt := activeFailureDebt(cleared); hasDebt {
		t.Fatal("expected failure debt cleared after dropping failed observations")
	}
	if len(cleared) != 1 || cleared[0].ObservationID != "obs-001" {
		t.Fatalf("expected successful observation retained, got %+v", cleared)
	}
}

func TestProducedSourcePathsRecoversSourceFilesFromDurableResults(t *testing.T) {
	events := []task.TaskEvent{
		toolResultTestEvent("tool.file.write.result", "obs-1", "file.write", `{"path":"tmp/deck/slides.html","sizeBytes":20}`, false),
		toolResultTestEvent("tool.file.edit.result", "obs-2", "file.edit", `{"editedFiles":["tmp/deck/slides.html","tmp/deck/DESIGN.md"]}`, false),
		toolResultTestEvent("tool.file.write.result", "obs-3", "file.write", `{"path":"tmp/deck/notes.md"}`, true),
	}
	paths := producedSourcePaths(events)
	if len(paths) != 2 || paths[0] != "tmp/deck/slides.html" || paths[1] != "tmp/deck/DESIGN.md" {
		t.Fatalf("expected deduped non-failed source paths, got %+v", paths)
	}
}

func TestPruneOrphanRequiredFieldsDropsUndefinedNames(t *testing.T) {
	schemaDocument := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title","missingField"],"nested":{"type":"object","required":["ghost"]}}`)
	pruned := pruneOrphanRequiredFields(schemaDocument)
	var schema map[string]any
	if errorValue := json.Unmarshal(pruned, &schema); errorValue != nil {
		t.Fatal(errorValue)
	}
	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "title" {
		t.Fatalf("expected only defined required fields, got %v", required)
	}
	nested, _ := schema["nested"].(map[string]any)
	if _, hasRequired := nested["required"]; hasRequired {
		t.Fatalf("expected orphan nested required removed, got %v", nested)
	}
}
