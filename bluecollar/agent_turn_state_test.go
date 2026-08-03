package bluecollar

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/model"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
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
			toolName:     "terminal_run",
			arguments:    `{"command":"pwd"}`,
			expectedType: "continue",
			check: func(t *testing.T, action agentAction) {
				if action.ToolName != "terminal_run" || string(action.ToolInput) != `{"command":"pwd"}` {
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
	if nativeChatTool(t, provider.lastRequest.Tools, toolcontract.TerminalRunToolName).Function.Name != toolcontract.TerminalRunToolName {
		t.Fatalf("expected native chat to preserve direct typed tool, got %+v", provider.lastRequest.Tools)
	}
}

func TestBuildAgentActionChatRequestExposesDirectToolsAndTerminalControls(t *testing.T) {
	state := nativeAgentActionTestState()
	seed := int64(77)
	temperature := 0.4
	maxTokens := 321
	state.Options.GenerationOptions = model.GenerationOptions{
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
	tool := nativeChatTool(t, chatRequest.Tools, toolcontract.TerminalRunToolName)
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

func TestDecideAgentActionNativeChatRejectsInvalidCallsWithoutStructuredFallback(t *testing.T) {
	blankToolCallIDResponse := nativeAgentActionChatResponse("finish", `{}`)
	blankToolCallIDResponse.Message.ToolCalls[0].ID = " "
	testCases := []struct {
		name     string
		response model.ChatCompletionResponse
	}{
		{name: "empty calls", response: model.ChatCompletionResponse{FinishReason: "tool_calls", Message: model.ChatCompletionMessage{Role: "assistant"}}},
		{name: "unknown tool", response: nativeAgentActionChatResponse("unknown", `{}`)},
		{name: "malformed arguments", response: nativeAgentActionChatResponse(toolcontract.TerminalRunToolName, "{invalid")},
		{name: "non-object arguments", response: nativeAgentActionChatResponse(toolcontract.TerminalRunToolName, `[]`)},
		{name: "empty arguments", response: nativeAgentActionChatResponse(toolcontract.TerminalRunToolName, "")},
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
	toolSet := toolcontract.NewToolSet([]string{"task_add"})
	registerTestTool(toolSet, toolcontract.ToolDefinition{
		Name:        "task_add",
		Description: "Add a task.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		executionCount++
		return testToolSuccess("added"), nil
	})
	state := agentTaskState{Request: AgentTurnRequest{Prompt: "add a task", ToolSet: toolSet}}
	correctionError := testStructuredOutputCorrectionError{correction: model.StructuredOutputCorrection{
		Code: "provider_response_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{
			Category:     model.StructuredOutputDiagnosticJSONParse,
			ToolName:     "task_add",
			RepairStatus: model.StructuredOutputRepairFailed,
		},
	}}
	provider := nativeAgentActionLanguageModel{
		chatErrors: []error{correctionError, nil},
		chatResponses: []model.ChatCompletionResponse{
			{},
			nativeAgentActionChatResponse("task_add", `{"title":"plan review"}`),
		},
	}

	action, errorValue := DecideAgentAction(context.Background(), &provider, state)
	if errorValue != nil {
		t.Fatalf("expected corrected native action: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != "task_add" || string(action.ToolInput) != `{"title":"plan review"}` {
		t.Fatalf("expected parsed task_add action, got %+v", action)
	}
	result, invokeError := state.Request.ToolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: action.ToolName, Input: action.ToolInput})
	if invokeError != nil || result.Failure != nil {
		t.Fatalf("expected task_add invocation, got %+v, %v", result, invokeError)
	}
	if executionCount != 1 || provider.chatCalls != 2 || provider.structuredCalls != 0 {
		t.Fatalf("expected one side effect after two native calls, got executions=%d chat=%d structured=%d", executionCount, provider.chatCalls, provider.structuredCalls)
	}
}

func TestDecideAgentActionNativeChatRetryRequiresExactDiagnosticTool(t *testing.T) {
	correctionError := testStructuredOutputCorrectionError{correction: model.StructuredOutputCorrection{
		Code: "structured_output_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{
			Category: model.StructuredOutputDiagnosticSchemaValidation,
			ToolName: "task_add",
			ValidationIssues: []model.StructuredOutputValidationIssue{{
				FieldPath: "/title",
				Code:      model.StructuredOutputValidationRequired,
			}},
		},
	}}
	provider := nativeAgentActionLanguageModel{
		chatErrors: []error{correctionError, nil},
		chatResponses: []model.ChatCompletionResponse{
			{},
			nativeAgentActionChatResponse("task_add", `{"title":"plan review"}`),
		},
	}
	state := nativeAgentActionTestStateWithTools("task_add", toolcontract.TerminalRunToolName)

	_, errorValue := DecideAgentAction(context.Background(), &provider, state)
	if errorValue != nil {
		t.Fatalf("expected corrected native action: %v", errorValue)
	}
	if len(provider.chatRequests) != 2 {
		t.Fatalf("expected two native requests, got %d", len(provider.chatRequests))
	}
	retryRequest := provider.chatRequests[1]
	if len(retryRequest.Tools) != 1 || retryRequest.Tools[0].Function.Name != "task_add" {
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
	correctionError := testStructuredOutputCorrectionError{correction: model.StructuredOutputCorrection{
		Code: "structured_output_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{
			Category:     model.StructuredOutputDiagnosticFinishReason,
			FinishReason: "stop",
		},
	}}
	testCases := []struct {
		name             string
		observations     []turnObservation
		expectedToolName string
	}{
		{name: "first operation", expectedToolName: "file_write"},
		{
			name: "next operation",
			observations: []turnObservation{{
				ObservationID: "observation-1",
				Action:        "continue",
				Tool:          "file_write",
				ToolID:        "kernel:file_write",
				ToolInput:     json.RawMessage(`{"path":"report.txt"}`),
				Output:        toolcontract.ToolOutput{Content: "written"},
			}},
			expectedToolName: toolcontract.TerminalRunToolName,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			state := nativeAgentActionContractState()
			state.Observations = testCase.observations
			provider := nativeAgentActionLanguageModel{
				chatErrors: []error{correctionError, nil},
				chatResponses: []model.ChatCompletionResponse{
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
				state.Observations[0].RecoveryPacket = &RecoveryPacket{AllowedTools: []string{"task_list"}}
			},
		},
		{
			name:        "user input pending",
			updateState: func(state *agentTaskState) { state.PendingWait = &agentPendingWait{Kind: agentPendingWaitUserInput} },
		},
		{
			name: "failed evidence",
			updateState: func(state *agentTaskState) {
				state.Observations[0].Failure = &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.OperationFailed.String()}
			},
		},
		{
			name: "failure debt",
			updateState: func(state *agentTaskState) {
				state.Observations = append(state.Observations, turnObservation{
					ObservationID: "observation-2",
					Action:        "continue",
					Tool:          "task_add",
					ToolInputKey:  "task_add\x00{}",
					Failure:       &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.OperationFailed.String()},
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
	t.Run("required next tool precedes finish", func(t *testing.T) {
		state := nativeAgentActionContractState()

		assertRequiredAgentActionTool(t, finishReasonRetryRequest(t, state), "file_write")
	})

	t.Run("finish absent from request", func(t *testing.T) {
		state := nativeAgentActionCompletionReadyState()
		request := nativeAgentActionChatCompletionRequest(t, state)
		request.Tools = slices.DeleteFunc(request.Tools, func(tool model.ChatCompletionTool) bool {
			return tool.Function.Name == "finish"
		})

		_, canRetry := retryAgentActionChatCompletionRequest(request, finishReasonCorrection(), state)
		if canRetry {
			t.Fatal("expected completion-ready correction without finish to fail closed")
		}
	})
}

func TestDecideAgentActionNativeChatRetryPreservesModelChoiceOutsidePendingContract(t *testing.T) {
	correctionError := testStructuredOutputCorrectionError{correction: model.StructuredOutputCorrection{
		Code: "structured_output_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{
			Category:     model.StructuredOutputDiagnosticFinishReason,
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
					successfulContractObservation("observation-1", "file_write", "kernel:file_write", `{"path":"report.txt"}`),
					successfulContractObservation("observation-2", toolcontract.TerminalRunToolName, "kernel:terminal_run", `{"command":"wc report.txt"}`),
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
					Tool:          "file_write",
					ToolInputKey:  "file_write\x00{}",
					Failure:       &toolcontract.ToolFailure{Code: "write_failed"},
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
				chatResponses: []model.ChatCompletionResponse{
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
	correctionError := testStructuredOutputCorrectionError{correction: model.StructuredOutputCorrection{
		Code:       "structured_output_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{Category: model.StructuredOutputDiagnosticFinishReason, FinishReason: "stop"},
	}}
	state := nativeAgentActionContractState()
	state.Request.ContractToolWorkingSet.RequiredNextTools[0] = "missing.tool"
	provider := nativeAgentActionLanguageModel{chatError: correctionError}

	_, errorValue := DecideAgentAction(context.Background(), &provider, state)

	if errorValue == nil || errorValue.Error() != correctionError.Error() {
		t.Fatalf("expected original correction error, got %v", errorValue)
	}
	if provider.chatCalls != 1 || provider.structuredCalls != 0 {
		t.Fatalf("expected unavailable contract tool to fail closed, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
	}
}

func TestFirstPendingActionToolNameUsesRequiredNextTools(t *testing.T) {
	state := nativeAgentActionContractState()

	if toolName := firstPendingActionToolName(state); toolName != "file_write" {
		t.Fatalf("expected first required next tool, got %q", toolName)
	}

	state.Observations = []turnObservation{
		successfulContractObservation("observation-1", toolcontract.TerminalRunToolName, "kernel:terminal_run", `{"command":"ls"}`),
		successfulContractObservation("observation-2", "file_write", "kernel:file_write", `{"path":"report.txt"}`),
		{
			ObservationID: "observation-3",
			Action:        "continue",
			Tool:          toolcontract.TerminalRunToolName,
			ToolInputKey:  toolcontract.TerminalRunToolName + "\x00{}",
			Failure:       &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.OperationFailed.String()},
		},
	}
	if toolName := firstPendingRequiredToolName(state.Request.ContractToolWorkingSet.RequiredNextTools, state.Observations); toolName != toolcontract.TerminalRunToolName {
		t.Fatalf("expected out-of-order and failed observations not to advance the sequence, got %q", toolName)
	}
	if toolName := firstPendingActionToolName(state); toolName != "" {
		t.Fatalf("expected failure debt to leave recovery choice open, got %q", toolName)
	}
}

func TestDecideAgentActionNativeChatSucceedsAfterTwoCorrections(t *testing.T) {
	finishReasonError := testStructuredOutputCorrectionError{correction: model.StructuredOutputCorrection{
		Code: "structured_output_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{
			Category:     model.StructuredOutputDiagnosticFinishReason,
			FinishReason: model.StructuredOutputDiagnosticFinishStop,
		},
	}}
	schemaValidationError := testStructuredOutputCorrectionError{correction: model.StructuredOutputCorrection{
		Code: "provider_response_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{
			Category: model.StructuredOutputDiagnosticSchemaValidation,
			ToolName: "file_write",
			ValidationIssues: []model.StructuredOutputValidationIssue{
				{FieldPath: "/content_type", Code: model.StructuredOutputValidationAdditionalProperty},
				{FieldPath: "/summary", Code: model.StructuredOutputValidationAdditionalProperty},
			},
			RepairStatus: model.StructuredOutputRepairFailed,
		},
	}}
	provider := nativeAgentActionLanguageModel{
		chatErrors: []error{finishReasonError, schemaValidationError, nil},
		chatResponses: []model.ChatCompletionResponse{
			{},
			{},
			nativeAgentActionChatResponse("file_write", `{}`),
		},
	}

	action, errorValue := DecideAgentAction(context.Background(), &provider, nativeAgentActionContractState())

	if errorValue != nil {
		t.Fatalf("expected corrected native action: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != "file_write" {
		t.Fatalf("expected corrected file_write action, got %+v", action)
	}
	if provider.chatCalls != 3 || provider.structuredCalls != 0 {
		t.Fatalf("expected three native calls without structured fallback, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
	}
	for requestIndex := 1; requestIndex < len(provider.chatRequests); requestIndex++ {
		request := provider.chatRequests[requestIndex]
		if len(request.Tools) != 1 || request.Tools[0].Function.Name != "file_write" {
			t.Fatalf("expected retry %d to stay on exact file_write, got %+v", requestIndex, request.Tools)
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
	correctionError := testStructuredOutputCorrectionError{correction: model.StructuredOutputCorrection{
		Code: "provider_response_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{
			Category: model.StructuredOutputDiagnosticToolCallContract,
			ToolName: "terminal_run",
		},
	}}
	finalError := testStructuredOutputCorrectionError{correction: model.StructuredOutputCorrection{
		Code:       "third_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{Category: model.StructuredOutputDiagnosticToolCallContract, ToolName: "terminal_run"},
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
	correctionError := testStructuredOutputCorrectionError{correction: model.StructuredOutputCorrection{
		Code: "provider_response_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{
			Category: model.StructuredOutputDiagnosticToolCallContract,
			ToolName: toolcontract.TerminalRunToolName,
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
	correctionError := testStructuredOutputCorrectionError{correction: model.StructuredOutputCorrection{
		Code: "provider_response_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{
			Category: model.StructuredOutputDiagnosticJSONParse,
			ToolName: "unknown_tool",
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
	if action.Action != "continue" || action.ToolName != toolcontract.TerminalRunToolName || string(action.ToolInput) != `{"command":"pwd"}` {
		t.Fatalf("expected first provider-ordered tool call, got %+v", action)
	}
	if provider.chatCalls != 1 || provider.structuredCalls != 0 {
		t.Fatalf("expected one native call without retry or structured fallback, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
	}
}

func TestDecideAgentActionKeepsImagePartsOnNativeChatPath(t *testing.T) {
	provider := nativeAgentActionLanguageModel{chatResponse: nativeAgentActionChatResponse("finish", `{}`)}
	state := nativeAgentActionTestState()
	state.Request.InputParts = []AgentPart{{
		Type:  AgentPartTypeImage,
		Image: &AgentImagePart{MimeType: "image/png", DataBase64: "aGVsbG8="},
	}}

	action, errorValue := DecideAgentAction(context.Background(), &provider, state)
	if errorValue != nil || action.Action != "finish" {
		t.Fatalf("expected native action for message parts, got %+v, %v", action, errorValue)
	}
	if provider.chatCalls != 1 || provider.structuredCalls != 0 {
		t.Fatalf("expected one native call for message parts, got chat=%d structured=%d", provider.chatCalls, provider.structuredCalls)
	}
	imagePartCount := 0
	for _, message := range provider.lastRequest.Messages {
		for _, part := range message.Parts {
			if part.Type == "image" && part.DataBase64 == "aGVsbG8=" && part.MimeType == "image/png" {
				imagePartCount++
			}
		}
	}
	if imagePartCount != 1 {
		t.Fatalf("expected the image part to reach the native chat request, got %d", imagePartCount)
	}
}

func TestDecideAgentActionNativeChatPropagatesProviderErrorAndCancellation(t *testing.T) {
	deadlineContext, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	correctionError := testStructuredOutputCorrectionError{correction: model.StructuredOutputCorrection{
		Code:       "provider_response_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{Category: model.StructuredOutputDiagnosticJSONParse},
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
		response: model.StructuredResponse{Content: `{"action":"finish","message":"done"}`},
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
	toolSet := toolcontract.NewToolSet([]string{toolcontract.TerminalRunToolName})
	registerTestTool(toolSet, toolcontract.ToolDefinition{
		Name:        toolcontract.TerminalRunToolName,
		Description: "Run a command.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`),
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("ran"), nil
	})
	return agentTaskState{Request: AgentTurnRequest{Prompt: "run command", ToolSet: toolSet}}
}

func nativeAgentActionTestStateWithTools(toolNames ...string) agentTaskState {
	toolSet := toolcontract.NewToolSet(toolNames)
	for _, toolName := range toolNames {
		registerTestTool(toolSet, toolcontract.ToolDefinition{
			Name:        toolName,
			Description: "Test tool.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}
	return agentTaskState{Request: AgentTurnRequest{Prompt: "use a tool", ToolSet: toolSet}}
}

func nativeAgentActionContractState() agentTaskState {
	state := nativeAgentActionTestStateWithTools("file_write", toolcontract.TerminalRunToolName)
	state.Request.ContractToolWorkingSet.RequiredNextTools = []string{"file_write", toolcontract.TerminalRunToolName}
	return state
}

func nativeAgentActionCompletionReadyState() agentTaskState {
	toolDefinition := testToolDescriptor("task_add")
	toolDefinition.SideEffectClass = toolcontract.ToolSideEffectStateChange
	toolDefinition.Completion = toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation}
	toolDefinition.ResultContract = &toolcontract.ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"},"created":{"type":"boolean"}},"required":["taskID","created"],"additionalProperties":false}`),
		Effects: []toolcontract.ResourceEffectContract{{
			ObjectType:     "task",
			Effect:         "created",
			ResultField:    "taskID",
			EffectIdentity: "id",
		}},
		EvidenceCondition: &toolcontract.EvidenceCondition{
			ResultField: "created",
			Equals:      json.RawMessage(`true`),
		},
	}
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{toolDefinition})
	return agentTaskState{
		Request: AgentTurnRequest{
			Prompt:                "add task",
			ToolSet:               toolSet,
			RequiredEvidenceTools: []string{"task_add"},
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{"task_add"},
				RequiredEffects: []OutcomeEffect{{
					ObjectType: "task",
					Effect:     "created",
				}},
			},
		},
		Observations: []turnObservation{{
			ObservationID: "observation-1",
			Action:        "continue",
			Tool:          "task_add",
			ToolID:        toolDefinition.ID,
			ToolInput:     json.RawMessage(`{"title":"first"}`),
			Output:        toolcontract.ToolOutput{Content: "added", Data: json.RawMessage(`{"taskID":"task-1","created":true}`)},
			Effects:       []toolcontract.ResourceEffect{{ObjectType: "task", Effect: "created", ID: "task-1"}},
		}},
	}
}

func finishReasonRetryRequest(t *testing.T, state agentTaskState) model.ChatCompletionRequest {
	t.Helper()
	request := nativeAgentActionChatCompletionRequest(t, state)
	retryRequest, canRetry := retryAgentActionChatCompletionRequest(request, finishReasonCorrection(), state)
	if !canRetry {
		t.Fatal("expected finish-reason correction")
	}
	return retryRequest
}

func nativeAgentActionChatCompletionRequest(t *testing.T, state agentTaskState) model.ChatCompletionRequest {
	t.Helper()
	requestSource := buildAgentActionRequest(state, false)
	request, isRepresentable := buildAgentActionChatCompletionRequest(requestSource)
	if !isRepresentable {
		t.Fatal("expected native action request")
	}
	return request
}

func assertRequiredAgentActionTool(t *testing.T, request model.ChatCompletionRequest, toolName string) {
	t.Helper()
	if len(request.Tools) != 1 || request.Tools[0].Function.Name != toolName {
		t.Fatalf("expected only %q, got %+v", toolName, request.Tools)
	}
	if string(request.ToolChoice) != `"required"` || request.ParallelToolCalls {
		t.Fatalf("expected portable single-tool requirement, got choice=%s parallel=%t", request.ToolChoice, request.ParallelToolCalls)
	}
}

func finishReasonCorrection() model.StructuredOutputCorrection {
	return model.StructuredOutputCorrection{
		Code: "structured_output_invalid",
		Diagnostic: model.StructuredOutputDiagnostic{
			Category:     model.StructuredOutputDiagnosticFinishReason,
			FinishReason: model.StructuredOutputDiagnosticFinishStop,
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
		Output:        toolcontract.ToolOutput{Content: "succeeded"},
	}
}

func nativeAgentActionChatResponse(toolName string, arguments string) model.ChatCompletionResponse {
	return model.ChatCompletionResponse{
		FinishReason: "tool_calls",
		Message: model.ChatCompletionMessage{
			Role:      "assistant",
			ToolCalls: []model.ChatCompletionToolCall{nativeAgentActionToolCall(toolName, arguments)},
		},
	}
}

func nativeAgentActionMultipleCallsResponse() model.ChatCompletionResponse {
	return model.ChatCompletionResponse{
		FinishReason: "tool_calls",
		Message: model.ChatCompletionMessage{
			Role: "assistant",
			ToolCalls: []model.ChatCompletionToolCall{
				nativeAgentActionToolCall(toolcontract.TerminalRunToolName, `{"command":"pwd"}`),
				nativeAgentActionToolCall("finish", `{}`),
			},
		},
	}
}

func nativeAgentActionToolCall(toolName string, arguments string) model.ChatCompletionToolCall {
	return model.ChatCompletionToolCall{
		ID:   "call-1",
		Type: "function",
		Function: model.ChatCompletionToolCallFunction{
			Name:      toolName,
			Arguments: arguments,
		},
	}
}

func nativeChatTool(t *testing.T, tools []model.ChatCompletionTool, toolName string) model.ChatCompletionTool {
	t.Helper()
	for _, tool := range tools {
		if tool.Function.Name == toolName {
			return tool
		}
	}
	t.Fatalf("expected native tool %q in %+v", toolName, tools)
	return model.ChatCompletionTool{}
}

func cancelledContext() context.Context {
	contextValue, cancel := context.WithCancel(context.Background())
	cancel()
	return contextValue
}

type testStructuredOutputCorrectionError struct {
	correction model.StructuredOutputCorrection
}

func (errorValue testStructuredOutputCorrectionError) Error() string {
	return errorValue.correction.Code
}

func (errorValue testStructuredOutputCorrectionError) StructuredOutputCorrection() (model.StructuredOutputCorrection, bool) {
	return errorValue.correction, true
}

type nativeAgentActionLanguageModel struct {
	chatResponse    model.ChatCompletionResponse
	chatError       error
	chatResponses   []model.ChatCompletionResponse
	chatErrors      []error
	chatCalls       int
	structuredCalls int
	lastRequest     model.ChatCompletionRequest
	chatRequests    []model.ChatCompletionRequest
}

func (provider *nativeAgentActionLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (provider *nativeAgentActionLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	provider.structuredCalls++
	return provider.chatResponseAsStructured(), nil
}

func (provider *nativeAgentActionLanguageModel) GenerateChatCompletion(_ context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
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

func (provider *nativeAgentActionLanguageModel) chatResponseAsStructured() model.StructuredResponse {
	return model.StructuredResponse{Content: `{"action":"finish","message":"done"}`}
}

func chatMessageContent(messages []model.ChatCompletionMessage) string {
	contents := make([]string, 0, len(messages))
	for _, message := range messages {
		contents = append(contents, message.Content)
	}
	return strings.Join(contents, "\n")
}

type structuredOnlyAgentActionLanguageModel struct {
	response        model.StructuredResponse
	structuredCalls int
}

func (provider *structuredOnlyAgentActionLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (provider *structuredOnlyAgentActionLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	provider.structuredCalls++
	return provider.response, nil
}

func TestBuildAgentActionRequestPreservesNativeToolCallingWireShape(t *testing.T) {
	seed := int64(77)
	temperature := 0.4
	toolSet := toolcontract.NewToolSet([]string{toolcontract.TerminalRunToolName, "site_serve"})
	registerTestTool(toolSet, toolcontract.ToolDefinition{
		Name:        toolcontract.TerminalRunToolName,
		Description: "Run a command.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`),
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("ran"), nil
	})
	registerTestTool(toolSet, toolcontract.ToolDefinition{
		Name:        "site_serve",
		Description: "Publish a site.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"required":["siteID"],"additionalProperties":false}`),
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
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
		Options: TurnOptions{GenerationOptions: model.GenerationOptions{
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
	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolName":{"enum":["terminal_run"]`) {
		t.Fatalf("expected kernel toolName enum to be preserved, got %s", request.StructuredOutputSchema.Document)
	}
	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolName":{"enum":["site_serve"]`) {
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
	toolSet := toolcontract.NewToolSet([]string{toolcontract.AskInputToolName})
	registerTestTool(toolSet, toolcontract.ToolDefinition{
		Name:        toolcontract.AskInputToolName,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}},"required":["question"]}`),
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("waiting"), nil
	})

	request := BuildAgentActionRequest(agentTaskState{Request: AgentTurnRequest{ToolSet: toolSet}})

	if !strings.Contains(request.StructuredOutputSchema.Document, `"toolName":{"enum":["ask_input"]`) {
		t.Fatalf("expected typed ask_input exposure to remain in the action schema, got %s", request.StructuredOutputSchema.Document)
	}
}

func TestDirectActionSchemaPreservesToolRequiredFields(t *testing.T) {
	schemaDocument := buildActionSchemaFromToolDefinitions([]toolcontract.ToolDefinition{{
		Name:        "calendar_add",
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
	schemaDocument := buildActionSchemaFromToolDefinitions([]toolcontract.ToolDefinition{
		{Name: "missing_schema"},
		{Name: "invalid_schema", InputSchema: json.RawMessage(`{"type":`)},
		{Name: "scalar_schema", InputSchema: json.RawMessage(`{"type":"string"}`)},
		{Name: "valid_schema", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
	}, false, nil, false, false)

	if strings.Contains(schemaDocument, "missing_schema") {
		t.Fatalf("expected missing schema tool to be omitted, got %s", schemaDocument)
	}
	if strings.Contains(schemaDocument, "invalid_schema") {
		t.Fatalf("expected invalid schema tool to be omitted, got %s", schemaDocument)
	}
	if strings.Contains(schemaDocument, "scalar_schema") {
		t.Fatalf("expected non-object schema tool to be omitted, got %s", schemaDocument)
	}
	if !strings.Contains(schemaDocument, "valid_schema") {
		t.Fatalf("expected valid object schema tool to remain, got %s", schemaDocument)
	}
}

func TestActionSchemaPreservesRequiredFieldsOnArrayOfNestedObjects(t *testing.T) {
	schemaDocument := buildActionSchemaFromToolDefinitions([]toolcontract.ToolDefinition{{
		Name:        "calendar_add",
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
	toolSet := toolcontract.NewToolSet([]string{"browser_open"})
	registerTestTool(toolSet, toolcontract.ToolDefinition{Name: "browser_open"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("opened"), nil
	})
	state := agentTaskState{
		Request: AgentTurnRequest{
			Prompt:  "open browser",
			ToolSet: toolSet,
		},
	}
	seededState := state
	seededState.Options.GenerationOptions = model.GenerationOptions{Seed: &seed, Temperature: &temperature}

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
	events := []taskstate.TaskEvent{{
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

	state, errorValue := restoreAgentTaskState(AgentTurnRequest{Prompt: "continue"}, TurnOptions{}, taskstate.TaskRun{
		TaskRunID: "task-1",
		Status:    taskstate.TaskStatusRunning,
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
	action, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: `{"action":"finish","message":"summary","replyParts":[{"type":"text","text":"done"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":[],"qualityReview":[]}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "finish" || finishActionMessage(action) != "done" {
		t.Fatalf("expected replyParts to provide finish message, got %+v", action)
	}
}

func TestParseAgentActionResponseNormalizesUntypedFinishReplyParts(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: `{"action":"finish","message":"Delivering it directly.","replyParts":[{"type":"","text":"Still pending. It will be delivered as an attachment later."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":[],"qualityReview":[]}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if finishActionMessage(action) != "Still pending. It will be delivered as an attachment later." {
		t.Fatalf("expected untyped replyParts text to stay visible, got %+v", action)
	}
}

func TestParseAgentActionResponseCoercesStringCompletionEvidenceIDs(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: `{"action":"finish","message":"Done.","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":"obs-005, obs-008","qualityReview":[]}`})
	if errorValue != nil {
		t.Fatalf("expected string completionEvidenceIDs to parse: %v", errorValue)
	}
	if len(action.CompletionEvidenceIDs) != 2 || action.CompletionEvidenceIDs[0] != "obs-005" || action.CompletionEvidenceIDs[1] != "obs-008" {
		t.Fatalf("expected coerced evidence IDs, got %+v", action.CompletionEvidenceIDs)
	}
}

func TestParseAgentActionResponseNormalizesNestedFinishBlock(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: `{"executionStateUpdate":{"goal":"answer user"},"finish":{"message":"done","replyParts":[{"type":"text","text":"done"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"],"qualityReview":[{"id":"complete","passed":true,"evidenceIDs":["obs-001"]}]}}`})
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
	action, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: `{"action":"finish","message":"done","goalStatus":"satisfied","goalSatisfied":"true","completionEvidenceIDs":[],"qualityReview":[]}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.GoalSatisfied == nil || !*action.GoalSatisfied {
		t.Fatalf("expected string boolean to normalize, got %+v", action.GoalSatisfied)
	}
}

func TestParseAgentActionResponseRejectsAmbiguousNestedActionBlocks(t *testing.T) {
	_, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: `{"finish":{"message":"done"},"continue":{"toolName":"browser_open","toolInput":{}}}`})
	if errorValue == nil {
		t.Fatal("expected ambiguous action blocks to be rejected")
	}
}

func TestParseAgentActionResponseExpandsShallowEvidenceIDs(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: `{"action":"finish","message":"done","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"],"qualityReview":[{"id":"done","passed":true,"evidenceIDs":["obs-001"]}]}`})
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
	action, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: `{"action":"continue","toolName":"browser_open","toolInput":{"url":"https://example.com"}}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != "browser_open" {
		t.Fatalf("expected tool call action, got %+v", action)
	}
	if string(action.ToolInput) != `{"url":"https://example.com"}` {
		t.Fatalf("expected tool input to be preserved, got %s", string(action.ToolInput))
	}
}

func TestParseAgentActionResponseNormalizesContinueToolCall(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: `{"action":"continue","toolName":"browser_open","message":"opening it","toolInput":{"url":"https://example.com"}}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != "browser_open" || action.Message != "opening it" {
		t.Fatalf("expected continue action to normalize, got %+v", action)
	}
	if string(action.ToolInput) != `{"url":"https://example.com"}` {
		t.Fatalf("expected tool input to be preserved, got %s", string(action.ToolInput))
	}
}

func TestParseAgentActionResponsePreservesDirectToolName(t *testing.T) {
	action, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: `{"action":"continue","toolName":"task_add","toolInput":{"title":"quarterly settlement"}}`})
	if errorValue != nil {
		t.Fatalf("expected parsed action: %v", errorValue)
	}
	if action.ToolName != "task_add" {
		t.Fatalf("expected direct tool name to stay task_add, got %+v", action)
	}
	if string(action.ToolInput) != `{"title":"quarterly settlement"}` {
		t.Fatalf("expected direct tool input to be preserved, got %s", action.ToolInput)
	}
}

func TestParseAgentActionResponseRejectsMalformedJSON(t *testing.T) {
	_, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: `{"action":`})
	if errorValue == nil {
		t.Fatal("expected malformed JSON error")
	}
}

func TestApplyToolResultAppendsObservationDeterministically(t *testing.T) {
	state := agentTaskState{}
	result := toolcontract.ToolResult{
		Output: toolcontract.ToolOutput{Content: "attached"},
		Attachments: []toolcontract.FileAttachment{{
			DevicePath:  "/tmp/file.html",
			Filename:    "file.html",
			ContentType: "text/html",
		}},
	}

	nextState := applyToolResult(state, toolcontract.ToolInvocation{ToolName: "file_deliver", Input: json.RawMessage(`{"path":"file.html"}`)}, result)

	if len(nextState.Observations) != 1 {
		t.Fatalf("expected one observation, got %+v", nextState.Observations)
	}
	observation := nextState.Observations[0]
	if observation.ObservationID != "obs-001" || observation.Tool != "file_deliver" || observation.ContentText() != "attached" {
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
	toolSet := toolcontract.NewToolSet([]string{toolcontract.FileDeliverToolName})
	registerTestTool(toolSet, toolcontract.ToolDefinition{Name: toolcontract.FileDeliverToolName}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("delivered"), nil
	})
	state := agentTaskState{
		Request: AgentTurnRequest{
			Prompt:                     "HTML make the file",
			ToolSet:                    toolSet,
			WorkspaceRootPath:          workspaceRootPath,
			RequiredEvidenceTools:      []string{toolcontract.FileDeliverToolName},
			RequiredAttachmentSuffixes: []string{".html"},
			TurnStartedAt:              time.Now().Add(-time.Second),
		},
		Requirements: []toolUseRequirement{{
			ToolName:           toolcontract.FileDeliverToolName,
			RequiresAttachment: true,
			AttachmentSuffixes: []string{".html"},
		}},
	}

	transition := advanceAgentTask(state)

	if transition.Effect.Kind != agentEffectContinue {
		t.Fatalf("expected file delivery effect, got %+v", transition.Effect)
	}
	if transition.Effect.ToolCall == nil || transition.Effect.ToolCall.ToolName != toolcontract.FileDeliverToolName {
		t.Fatalf("expected file_deliver tool call, got %+v", transition.Effect.ToolCall)
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
			ToolSet: newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{{
				Name:            "browser_open",
				Namespace:       "browser",
				SideEffectClass: toolcontract.ToolSideEffectConnect,
				Completion:      toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
			}}),
			RequiredEvidenceTools: []string{"browser_open"},
		},
		Requirements: []toolUseRequirement{{
			ToolName: "browser_open",
		}},
		Observations: []turnObservation{{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "browser_open",
			Output:        toolcontract.ToolOutput{Content: "opened"},
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
	events := []taskstate.TaskEvent{{
		Name: "tool.browser_open.result",
		Body: `{"observationID":"obs-001","action":"continue","tool":"browser_open","content":"opened","isError":false}`,
	}}

	state, errorValue := restoreAgentTaskState(AgentTurnRequest{Prompt: "continue"}, TurnOptions{}, taskstate.TaskRun{
		TaskRunID: "task-1",
		Status:    taskstate.TaskStatusWaitingUserInput,
	}, events)

	if errorValue != nil {
		t.Fatalf("expected restored state: %v", errorValue)
	}
	if state.Status != taskstate.TaskStatusWaitingUserInput {
		t.Fatalf("expected restored status, got %s", state.Status)
	}
	if len(state.Observations) != 1 || state.Observations[0].Tool != "browser_open" {
		t.Fatalf("expected restored observation, got %+v", state.Observations)
	}
}

func TestWaitingTaskResumeRestoresObservationsWithoutFlags(t *testing.T) {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "find the note and tell me")
	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	taskRunService.AppendTaskEvent(runningTaskRun.TaskRunID, "tool.message_search.result", `{"observationID":"obs-001","action":"continue","tool":"message_search","content":"{\"messageIDs\":[\"message-1\"]}","isError":false}`)
	waitingTaskRun, errorValue := taskRunService.PauseTaskRun(runningTaskRun.TaskRunID, taskstate.TaskStatusWaitingUserInput, "ask input")
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	state, errorValue := agentTaskStateForTurn(AgentTurnRequest{
		Prompt:            "yes, that one",
		ExistingTaskRunID: waitingTaskRun.TaskRunID,
	}, TurnOptions{}, waitingTaskRun, taskRunService.ListTaskEvent(waitingTaskRun.TaskRunID), true)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(state.Observations) != 1 || state.Observations[0].Tool != "message_search" {
		t.Fatalf("expected the pre-pause observation to survive an input resume, got %+v", state.Observations)
	}
}

func TestBlockedResumeRestoresPriorObservations(t *testing.T) {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "build site")
	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	taskRunService.AppendTaskEvent(runningTaskRun.TaskRunID, "tool.file_write.result", `{"observationID":"obs-001","action":"continue","tool":"file_write","content":"wrote app","isError":false}`)
	taskRunService.AppendTaskEvent(runningTaskRun.TaskRunID, "tool.file_read.result", `{"observationID":"obs-003","action":"continue","tool":"file_read","content":"read app","isError":false}`)
	blockedTaskRun, errorValue := taskRunService.PauseTaskRun(runningTaskRun.TaskRunID, taskstate.TaskStatusBlocked, "max_iterations")
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
	}, TurnOptions{}, resumedTaskRun, taskRunService.ListTaskEvent(resumedTaskRun.TaskRunID), false)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(state.Observations) != 2 || state.Observations[0].Tool != "file_write" || state.Observations[1].Tool != "file_read" {
		t.Fatalf("expected prior observations to be restored, got %+v", state.Observations)
	}
	if state.ToolCallCount != 2 {
		t.Fatalf("expected restored tool call count, got %d", state.ToolCallCount)
	}
	state = applyToolResult(state, toolcontract.ToolInvocation{ToolName: "file_write", Input: json.RawMessage(`{"path":"app.txt","content":"next"}`)}, testToolSuccess("wrote next"))
	if state.Observations[2].ObservationID != "obs-004" {
		t.Fatalf("expected observation IDs to continue after the highest restored ID, got %+v", state.Observations)
	}
}

func TestDecodeLegacyObservationNormalizesMemorySearchFailureCode(t *testing.T) {
	observation, errorValue := decodeTurnObservation([]byte(`{"observationID":"obs-001","action":"continue","tool":"memory_search","content":"memory failed","isError":true,"errorCode":"memory_search_unavailable","failureStage":"graphiti_search","message":"memory failed"}`))
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if !observation.Failed() || observation.FailureCode() != toolcontract.FailureCodes.Unavailable.String() {
		t.Fatalf("expected canonical memory search failure, got %+v", observation)
	}
}

func TestUserResumeClearsInheritedFailureDebt(t *testing.T) {
	observations := []turnObservation{
		{ObservationID: "obs-001", Action: "continue", Tool: "site_serve", Output: toolcontract.ToolOutput{Content: `{"siteID":"site-1"}`}},
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          "site_serve",
			Failure:       &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.OperationFailed.String()},
			ToolInputKey:  "site_serve\x00{\"siteID\":\"site-1\"}",
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
	events := []taskstate.TaskEvent{
		toolResultTestEvent("tool.file_write.result", "obs-1", "file_write", `{"path":"tmp/deck/slides.html","sizeBytes":20}`, false),
		toolResultTestEvent("tool.file_edit.result", "obs-2", "file_edit", `{"editedFiles":["tmp/deck/slides.html","tmp/deck/DESIGN.md"]}`, false),
		toolResultTestEvent("tool.file_write.result", "obs-3", "file_write", `{"path":"tmp/deck/notes.md"}`, true),
	}
	paths := producedSourcePaths(events)
	if len(paths) != 2 || paths[0] != "tmp/deck/slides.html" || paths[1] != "tmp/deck/DESIGN.md" {
		t.Fatalf("expected deduped non-failed source paths, got %+v", paths)
	}
}
