package bluecollar

import (
	"encoding/base64"
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompletionStateRequiresDeclaredResultCondition(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{{
		Name:         "artifact_review",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		ResultContract: &toolcontract.ToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{"passed":{"type":"boolean"}},"required":["passed"],"additionalProperties":false}`),
			EvidenceCondition: &toolcontract.EvidenceCondition{
				ResultField: "passed",
				Equals:      json.RawMessage(`true`),
			},
		},
	}})
	requirements := []toolUseRequirement{{ToolName: "artifact_review"}}
	failedReview := turnObservation{
		ObservationID: "obs-001",
		Tool:          "artifact_review",
		Output:        toolcontract.ToolOutput{Data: json.RawMessage(`{"passed":false}`)},
	}

	state := buildCompletionState(AgentTurnRequest{ToolSet: toolSet}, requirements, []turnObservation{failedReview})

	if failedReview.Failed() {
		t.Fatal("expected a completed review call to remain successful")
	}
	if len(state.EvidenceReferences) != 0 || state.Requirements[0].Satisfied {
		t.Fatalf("expected failed review verdict to remain completion-ineligible, got %+v", state)
	}

	passedReview := failedReview
	passedReview.Output.Data = json.RawMessage(`{"passed":true}`)
	state = buildCompletionState(AgentTurnRequest{ToolSet: toolSet}, requirements, []turnObservation{passedReview})
	if len(state.EvidenceReferences) != 1 || !state.Requirements[0].Satisfied {
		t.Fatalf("expected passed review verdict to satisfy completion evidence, got %+v", state)
	}
}

func TestEvidenceConditionUsesSemanticJSONEquality(t *testing.T) {
	condition := toolcontract.EvidenceCondition{
		ResultField: "review",
		Equals:      json.RawMessage(`{"passed":true,"scores":[1,2]}`),
	}
	result := json.RawMessage(`{"review":{"scores":[1.0,2],"passed":true}}`)

	if !resultSatisfiesEvidenceCondition(result, condition) {
		t.Fatal("expected equivalent JSON values to match regardless of field order and number representation")
	}
}

func TestTerminalCompletionRequiresCompletedResult(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{{
		Name:            toolcontract.TerminalRunToolName,
		InputSchema:     json.RawMessage(`{"type":"object","additionalProperties":false}`),
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{"completed":{"type":"boolean"}},"required":["completed"],"additionalProperties":true}`),
		SideEffectClass: toolcontract.ToolSideEffectWorkspaceWrite,
		Completion:      toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		ResultContract: &toolcontract.ToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{"completed":{"type":"boolean"}},"required":["completed"],"additionalProperties":true}`),
			EvidenceCondition: &toolcontract.EvidenceCondition{
				ResultField: "completed",
				Equals:      json.RawMessage(`true`),
			},
		},
	}})
	testCases := []struct {
		name        string
		data        json.RawMessage
		isSatisfied bool
	}{
		{name: "command", data: json.RawMessage(`{"mode":"command","completed":true}`), isSatisfied: true},
		{name: "session status", data: json.RawMessage(`{"mode":"session_status","completed":true}`), isSatisfied: true},
		{name: "session start", data: json.RawMessage(`{"mode":"session_start","completed":false}`)},
		{name: "session write", data: json.RawMessage(`{"mode":"session_write","completed":false}`)},
		{name: "session close", data: json.RawMessage(`{"mode":"session_close","completed":false}`)},
		{name: "missing data"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			observation := turnObservation{
				ObservationID: "obs-001",
				Tool:          toolcontract.TerminalRunToolName,
				Output:        toolcontract.ToolOutput{Data: testCase.data},
			}
			state := buildCompletionState(
				AgentTurnRequest{ToolSet: toolSet},
				[]toolUseRequirement{{ToolName: toolcontract.TerminalRunToolName}},
				[]turnObservation{observation},
			)
			if state.Requirements[0].Satisfied != testCase.isSatisfied {
				t.Fatalf("expected satisfied=%t, got %+v", testCase.isSatisfied, state)
			}
		})
	}
}

func TestCompletionStateFindsNewestArtifactByRequiredSuffix(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	olderPath := filepath.Join(artifactDirectoryPath, "older.pptx")
	newerPath := filepath.Join(artifactDirectoryPath, "newer.pptx")
	writeValidPPTXTestFile(t, olderPath)
	writeValidPPTXTestFile(t, newerPath)
	if errorValue := os.Chtimes(olderPath, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); errorValue != nil {
		t.Fatal(errorValue)
	}

	state := buildCompletionState(
		AgentTurnRequest{WorkspaceRootPath: workspaceRootPath, ToolSet: newTestToolSet([]string{"file_deliver"})},
		[]toolUseRequirement{{ToolName: "file_deliver", RequiresAttachment: true, AttachmentSuffixes: []string{".pptx"}}},
		nil,
	)

	if state.RecommendedAction != completionActionAttachExistingArtifacts {
		t.Fatalf("expected attach action, got %+v", state)
	}
	if len(state.ExistingArtifacts) != 1 || state.ExistingArtifacts[0].Filename != "newer.pptx" {
		t.Fatalf("expected newest pptx artifact, got %+v", state.ExistingArtifacts)
	}
}

func TestCompletionStateFinalizesWhenRequiredAttachmentEvidenceExists(t *testing.T) {
	state := buildCompletionState(
		AgentTurnRequest{},
		[]toolUseRequirement{{ToolName: "file_deliver", RequiresAttachment: true, AttachmentSuffixes: []string{".pptx", ".pdf"}}},
		[]turnObservation{{
			ObservationID: "obs-001",
			Tool:          "file_deliver",
			Attachments: []toolcontract.FileAttachment{
				{DevicePath: "artifacts/deck/deck.pptx", Filename: "deck.pptx"},
				{DevicePath: "artifacts/deck/deck.pdf", Filename: "deck.pdf"},
			},
		}},
	)

	if state.RecommendedAction != completionActionFinalizeWithEvidence {
		t.Fatalf("expected finalize action, got %+v", state)
	}
	if len(state.EvidenceReferences) != 2 {
		t.Fatalf("expected file_deliver evidence reference, got %+v", state.EvidenceReferences)
	}
	for _, reference := range state.EvidenceReferences {
		if reference.ObservationID != "obs-001" || reference.AttachmentIndex == nil {
			t.Fatalf("expected exact file_deliver evidence reference, got %+v", state.EvidenceReferences)
		}
	}
}

func TestCompletionStateKeepsWorkingWithActiveFailureDebt(t *testing.T) {
	failedObservation := newFailureObservation(
		"obs-002",
		"continue",
		"file_read",
		"permission denied",
		toolcontract.FailurePermissionDenied,
		toolcontract.FailureCodes.AccessDenied,
		"file_read",
	)
	failedObservation.ToolInputKey = "file_read\x00{}"
	state := buildCompletionState(
		AgentTurnRequest{},
		[]toolUseRequirement{{ToolName: toolcontract.FileDeliverToolName, RequiresAttachment: true, AttachmentSuffixes: []string{".json"}}},
		[]turnObservation{{
			ObservationID: "obs-001",
			Tool:          toolcontract.FileDeliverToolName,
			Attachments: []toolcontract.FileAttachment{{
				DevicePath: "artifacts/report/report.json",
				Filename:   "report.json",
			}},
		}, failedObservation},
	)

	if state.RecommendedAction != completionActionContinueWork {
		t.Fatalf("expected active failure debt to prevent completion, got %+v", state)
	}
}

func TestCompletionStateUsesAttachmentIndexesForRequiredSuffixEvidence(t *testing.T) {
	state := buildCompletionState(
		AgentTurnRequest{},
		[]toolUseRequirement{{ToolName: "file_deliver", RequiresAttachment: true, AttachmentSuffixes: []string{".pptx"}}},
		[]turnObservation{{
			ObservationID: "obs-001",
			Tool:          "file_deliver",
			Attachments: []toolcontract.FileAttachment{
				{DevicePath: "artifacts/deck/DESIGN.md", Filename: "DESIGN.md"},
				{DevicePath: "artifacts/deck/deck.pptx", Filename: "deck.pptx"},
			},
		}},
	)

	if state.RecommendedAction != completionActionFinalizeWithEvidence {
		t.Fatalf("expected finalize action, got %+v", state)
	}
	if len(state.EvidenceReferences) != 1 || state.EvidenceReferences[0].AttachmentIndex == nil || *state.EvidenceReferences[0].AttachmentIndex != 1 {
		t.Fatalf("expected exact pptx attachment evidence reference, got %+v", state.EvidenceReferences)
	}
	if len(state.AttachedEvidence) != 1 || state.AttachedEvidence[0].Filename != "deck.pptx" {
		t.Fatalf("expected only required artifact evidence, got %+v", state.AttachedEvidence)
	}
}

func TestCompletionStateValidatesAttachedEvidenceFromPayload(t *testing.T) {
	state := buildCompletionState(
		AgentTurnRequest{WorkspaceRootPath: t.TempDir()},
		[]toolUseRequirement{{ToolName: "file_deliver", RequiresAttachment: true, AttachmentSuffixes: []string{".txt"}}},
		[]turnObservation{{
			ObservationID: "obs-001",
			Tool:          "file_deliver",
			Attachments: []toolcontract.FileAttachment{{
				DevicePath:    "/workspace/private/people/person-1/artifacts/deck/note.txt",
				Filename:      "note.txt",
				SizeBytes:     4,
				ContentBase64: base64.StdEncoding.EncodeToString([]byte("note")),
			}},
		}},
	)

	if state.RecommendedAction != completionActionFinalizeWithEvidence {
		t.Fatalf("expected payload-backed attachment to finalize, got %+v", state)
	}
	if !state.ValidityState.Passed {
		t.Fatalf("expected payload-backed attachment validity to pass, got %+v", state.ValidityState)
	}
}

func TestCompletionStateDoesNotRepeatFailedAttachment(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))

	failedAttachment := newFailureObservation("obs-001", "continue", "file_deliver", filepath.Join(artifactDirectoryPath, "deck.pptx"), toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "file_attach")
	failedAttachment.RelatedPaths = []string{filepath.Join(artifactDirectoryPath, "deck.pptx")}
	state := buildCompletionState(
		AgentTurnRequest{WorkspaceRootPath: workspaceRootPath, ToolSet: newTestToolSet([]string{"file_deliver"})},
		[]toolUseRequirement{{ToolName: "file_deliver", RequiresAttachment: true, AttachmentSuffixes: []string{".pptx"}}},
		[]turnObservation{failedAttachment},
	)

	if state.RecommendedAction != completionActionContinueWork {
		t.Fatalf("expected continue work after failed attach, got %+v", state)
	}
}

func TestCompletionStateIgnoresArtifactsOlderThanTurn(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	artifactPath := filepath.Join(artifactDirectoryPath, "deck.pptx")
	writeAgentTestFile(t, artifactPath, "pptx")
	if errorValue := os.Chtimes(artifactPath, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); errorValue != nil {
		t.Fatal(errorValue)
	}

	state := buildCompletionState(
		AgentTurnRequest{
			WorkspaceRootPath: workspaceRootPath,
			ToolSet:           newTestToolSet([]string{"file_deliver"}),
			TurnStartedAt:     time.Now().Add(-time.Minute),
		},
		[]toolUseRequirement{{ToolName: "file_deliver", RequiresAttachment: true, AttachmentSuffixes: []string{".pptx"}}},
		nil,
	)

	if state.RecommendedAction != completionActionContinueWork {
		t.Fatalf("expected old artifact to be ignored, got %+v", state)
	}
	if len(state.ExistingArtifacts) != 0 {
		t.Fatalf("expected no current artifacts, got %+v", state.ExistingArtifacts)
	}
}

func TestCompletionStateFindsArtifactsNewerThanTurn(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))
	writeValidPDFTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pdf"))

	state := buildCompletionState(
		AgentTurnRequest{
			WorkspaceRootPath: workspaceRootPath,
			ToolSet:           newTestToolSet([]string{"file_deliver"}),
			TurnStartedAt:     turnStartedAt,
		},
		[]toolUseRequirement{{ToolName: "file_deliver", RequiresAttachment: true, AttachmentSuffixes: []string{".pptx", ".pdf"}}},
		nil,
	)

	if state.RecommendedAction != completionActionAttachExistingArtifacts {
		t.Fatalf("expected current artifacts to be attachable, got %+v", state)
	}
}

func TestCompletionStateRejectsReadableArtifactWithWrongFormat(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"), "not a zip package")

	state := buildCompletionState(
		AgentTurnRequest{
			WorkspaceRootPath: workspaceRootPath,
			ToolSet:           newTestToolSet([]string{"file_deliver"}),
			TurnStartedAt:     time.Now().Add(-time.Minute),
		},
		[]toolUseRequirement{{ToolName: "file_deliver", RequiresAttachment: true, AttachmentSuffixes: []string{".pptx"}}},
		nil,
	)

	if state.RecommendedAction != completionActionBlockedInvalidArtifact {
		t.Fatalf("expected wrong-format artifact to be blocked, got %+v", state)
	}
	if state.ValidityState.Passed {
		t.Fatalf("expected format validity to fail, got %+v", state.ValidityState)
	}
}
