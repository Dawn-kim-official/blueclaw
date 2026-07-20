package agent

import (
	"context"
	"encoding/json"
	"strings"

	"blueclaw/internal/llm"
)

const (
	completionJudgeSchemaName           = "blueclaw_completion_judge"
	completionJudgeMaxMissingWork       = 5
	completionJudgeMissingWorkMaxLength = 200
	completionJudgeReasonMaxLength      = 400
	completionJudgeInputMaxLength       = 600
	completionJudgeResultMaxLength      = 300
)

type completionJudgeVerdict struct {
	Satisfied   bool     `json:"satisfied"`
	MissingWork []string `json:"missingWork"`
	Reason      string   `json:"reason"`
}

type completionLedgerEntry struct {
	Tool   string `json:"tool"`
	Input  string `json:"input"`
	Result string `json:"result"`
}

func outcomeContractHasSideEffectEvidence(toolSet *ToolSet, contract OutcomeContract) bool {
	if requiredEvidenceIncludesSideEffect(toolSet, contract.RequiredEvidenceTools) {
		return true
	}
	for _, toolNames := range contract.RequiredEvidenceAnyOf {
		if requiredEvidenceIncludesSideEffect(toolSet, toolNames) {
			return true
		}
	}
	return false
}

func (agentTurnRunner *AgentTurnRunner) validateCompletionGateWithJudge(ctx context.Context, taskRunID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion, actionDocument turnActionDocument) completionGateResult {
	completionGateResult := validateCompletionGateForRequestWithExpectedResults(request, requirements, observations, attachments, criteria, actionDocument, agentTurnRunner.options.RecoveryBudget)
	if !completionGateResult.IsSatisfied || ctx.Err() != nil {
		return completionGateResult
	}
	if !outcomeContractHasSideEffectEvidence(request.ToolSet, request.OutcomeContract) {
		return completionGateResult
	}
	if judgeResult := agentTurnRunner.evaluateCompletionJudge(ctx, taskRunID, request, observations); !judgeResult.IsSatisfied {
		return judgeResult
	}
	return completionGateResult
}

func (agentTurnRunner *AgentTurnRunner) evaluateCompletionJudge(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation) completionGateResult {
	if agentTurnRunner.languageModel == nil {
		agentTurnRunner.appendEvent(taskRunID, "completion_judge.degraded", marshalEventBody(map[string]string{"error": "completion judge language model was not configured"}))
		return completionGateResult{IsSatisfied: true}
	}
	response, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, completionJudgeRequest(request, observations))
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, "completion_judge.degraded", marshalEventBody(map[string]string{"error": errorValue.Error()}))
		return completionGateResult{IsSatisfied: true}
	}
	verdict, errorValue := parseCompletionJudgeVerdict(response.Content)
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, "completion_judge.degraded", marshalEventBody(map[string]string{"error": errorValue.Error()}))
		return completionGateResult{IsSatisfied: true}
	}
	agentTurnRunner.appendEvent(taskRunID, "completion_judge.verdict", marshalEventBody(verdict))
	if verdict.Satisfied {
		return completionGateResult{IsSatisfied: true}
	}
	return completionGateResult{
		Message:        completionJudgeUnsatisfiedMessage(verdict),
		EvidenceKind:   evidenceKindExpectedResult,
		IsJudgeVerdict: true,
	}
}

func parseCompletionJudgeVerdict(content string) (completionJudgeVerdict, error) {
	var verdict completionJudgeVerdict
	if errorValue := json.Unmarshal([]byte(content), &verdict); errorValue != nil {
		return completionJudgeVerdict{}, errorValue
	}
	return verdict, nil
}

const completionJudgeAmendInstruction = "Amend the records this turn already created instead of creating new ones: use the matching update operation with the record's exact current title or ID as the hint. Never repeat an add or create operation for work that is already recorded in this turn."

func completionJudgeUnsatisfiedMessage(verdict completionJudgeVerdict) string {
	reason := strings.TrimSpace(verdict.Reason)
	missingWorkText := strings.Join(verdict.MissingWork, "; ")
	parts := []string{}
	if reason != "" {
		parts = append(parts, reason)
	}
	if missingWorkText != "" {
		parts = append(parts, "Missing: "+missingWorkText)
	}
	parts = append(parts, completionJudgeAmendInstruction)
	return strings.Join(parts, " ")
}

func completionJudgeRequest(request AgentTurnRequest, observations []turnObservation) llm.StructuredResponseRequest {
	return llm.StructuredResponseRequest{
		Messages: completionJudgeMessages(request, observations),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               completionJudgeSchemaName,
			Document:           completionJudgeSchema(),
			IsStrictlyEnforced: true,
		},
	}
}

func completionJudgeMessages(request AgentTurnRequest, observations []turnObservation) []llm.Message {
	messages := []llm.Message{
		{Role: "system", Content: completionJudgeInstruction()},
		{Role: "system", Content: "Original instruction:\n" + completionJudgeOriginalInstruction(request)},
	}
	if expectedResultsDescription := completionJudgeExpectedResultsDescription(request.OutcomeContract.ExpectedResults); expectedResultsDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: "Expected results:\n" + expectedResultsDescription})
	}
	messages = append(messages, llm.Message{Role: "user", Content: "Recorded successful side-effect operations this turn:\n" + completionJudgeLedgerDocument(request.ToolSet, observations)})
	return messages
}

func completionJudgeInstruction() string {
	return strings.Join([]string{
		"Judge whether the recorded successful operations actually accomplish the user's original instruction.",
		"Judge only from the recorded ledger facts below. The executor's own completion claims are not evidence.",
		"Mark unsatisfied when the recorded operations do not plausibly accomplish the instruction: wrong target, wrong values, or a missing step.",
		"When the instruction states an explicit deadline, date, time, quantity, title, or recipient, that value must appear in at least one successful recorded operation input; if a stated value appears nowhere, mark unsatisfied and name exactly that value in missingWork.",
		"Do not invent requirements the instruction does not state. Wording, formatting, phrasing, and which list or table a record appears in are not failures. If the right operations ran and every explicitly stated value appears in some recorded input, mark satisfied.",
	}, "\n")
}

func completionJudgeOriginalInstruction(request AgentTurnRequest) string {
	return firstNonEmptyString(request.ActiveGoal.OriginalInstruction, request.Prompt)
}

func completionJudgeExpectedResultsDescription(expectedResults []ExpectedResult) string {
	normalizedResults := normalizeExpectedResults(expectedResults)
	if len(normalizedResults) == 0 {
		return ""
	}
	document, errorValue := json.Marshal(normalizedResults)
	if errorValue != nil {
		return ""
	}
	return string(document)
}

func completionJudgeLedgerDocument(toolSet *ToolSet, observations []turnObservation) string {
	document, errorValue := json.Marshal(completionJudgeLedger(toolSet, observations))
	if errorValue != nil {
		return "[]"
	}
	return string(document)
}

func completionJudgeLedger(toolSet *ToolSet, observations []turnObservation) []completionLedgerEntry {
	ledger := []completionLedgerEntry{}
	for _, observation := range observations {
		if !isSideEffectObservation(toolSet, observation) {
			continue
		}
		ledger = append(ledger, completionLedgerEntry{
			Tool:   strings.TrimSpace(observation.Tool),
			Input:  truncateForLedger(string(observation.ToolInput), completionJudgeInputMaxLength),
			Result: truncateForLedger(observation.ContentText(), completionJudgeResultMaxLength),
		})
	}
	return ledger
}

func isSideEffectObservation(toolSet *ToolSet, observation turnObservation) bool {
	toolName := strings.TrimSpace(observation.Tool)
	if toolName == "" || observation.Failed() {
		return false
	}
	return IsArtifactDeliveryTool(toolName) || requiredEvidenceToolNeedsSuccessfulSideEffect(toolSet, toolName)
}

func truncateForLedger(value string, maxLength int) string {
	trimmedValue := strings.TrimSpace(value)
	if len(trimmedValue) <= maxLength {
		return trimmedValue
	}
	return trimmedValue[:maxLength]
}

func completionJudgeSchema() string {
	document := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"satisfied", "missingWork", "reason"},
		"properties": map[string]any{
			"satisfied": map[string]any{"type": "boolean"},
			"missingWork": map[string]any{
				"type":     "array",
				"maxItems": completionJudgeMaxMissingWork,
				"items":    map[string]any{"type": "string", "maxLength": completionJudgeMissingWorkMaxLength},
			},
			"reason": map[string]any{"type": "string", "maxLength": completionJudgeReasonMaxLength},
		},
	}
	encodedDocument, _ := json.Marshal(document)
	return string(encodedDocument)
}
