package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
)

const (
	completionJudgeSchemaName           = "blueclaw_completion_judge"
	completionJudgeMaxMissingWork       = 5
	completionJudgeMissingWorkMaxLength = 200
	completionJudgeReasonMaxLength      = 400
	completionJudgeInputMaxLength       = 2000
	completionJudgeResultMaxLength      = 300
	completionJudgeLedgerByteBudget     = 24000
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
	if !outcomeContractHasSideEffectEvidence(request.ToolSet, request.OutcomeContract) &&
		!observationsIncludeSideEffect(request.ToolSet, observations) {
		return completionGateResult
	}
	if judgeResult := agentTurnRunner.evaluateCompletionJudge(ctx, taskRunID, request, observations, actionDocument); !judgeResult.IsSatisfied {
		return judgeResult
	}
	return completionGateResult
}

func (agentTurnRunner *AgentTurnRunner) evaluateCompletionJudge(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation, actionDocument turnActionDocument) completionGateResult {
	if agentTurnRunner.languageModel == nil {
		agentTurnRunner.appendEvent(taskRunID, "completion_judge.degraded", marshalEventBody(map[string]string{"error": "completion judge language model was not configured"}))
		return completionGateResult{IsSatisfied: true}
	}
	response, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, completionJudgeRequest(request, observations, actionDocument))
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

func completionJudgeUnsatisfiedMessage(verdict completionJudgeVerdict) string {
	reason := strings.TrimSpace(verdict.Reason)
	if len(verdict.MissingWork) == 0 {
		return reason
	}
	missingWorkText := strings.Join(verdict.MissingWork, "; ")
	if reason == "" {
		return missingWorkText
	}
	return reason + " Missing: " + missingWorkText
}

func completionJudgeRequest(request AgentTurnRequest, observations []turnObservation, actionDocument turnActionDocument) llm.StructuredResponseRequest {
	return llm.StructuredResponseRequest{
		Messages: completionJudgeMessages(request, observations, actionDocument),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               completionJudgeSchemaName,
			Document:           completionJudgeSchema(),
			IsStrictlyEnforced: true,
		},
		GenerationOptions: terminalStructuredGenerationOptions(llm.GenerationOptions{}),
	}
}

func completionJudgeMessages(request AgentTurnRequest, observations []turnObservation, actionDocument turnActionDocument) []llm.Message {
	messages := []llm.Message{
		{Role: "system", Content: completionJudgeInstruction()},
		{Role: "system", Content: buildTemporalContextDescription(request.TurnStartedAt)},
		{Role: "system", Content: "Original instruction:\n" + completionJudgeOriginalInstruction(request)},
	}
	if expectedResultsDescription := completionJudgeExpectedResultsDescription(request.OutcomeContract.ExpectedResults); expectedResultsDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: "Expected results:\n" + expectedResultsDescription})
	}
	if finishReply := strings.TrimSpace(finishActionMessage(actionDocument)); finishReply != "" {
		messages = append(messages, llm.Message{Role: "system", Content: "Finish reply that accepting this completion delivers to the user:\n" + truncateForLedger(finishReply, completionJudgeInputMaxLength)})
	}
	if planContext := completionJudgePlanContext(observations); planContext != "" {
		messages = append(messages, llm.Message{Role: "system", Content: planContext})
	}
	messages = append(messages, llm.Message{Role: "system", Content: completionJudgeAttachmentDescription(attachmentsFromObservations(observations))})
	messages = append(messages, llm.Message{Role: "user", Content: "Recorded successful operations this turn, reads and state changes alike:\n" + completionJudgeLedgerDocument(request.ToolSet, observations)})
	return messages
}

// The ledger records deliveries as individual operations, leaving the judge to
// reconstruct the resulting attachment set from history. Only the runtime knows
// that set, so state it instead of having the judge infer it.
func completionJudgeAttachmentDescription(attachments []FileAttachment) string {
	if len(attachments) == 0 {
		return "Files this completion attaches for the user: none."
	}
	filenames := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		filenames = append(filenames, firstNonEmptyString(attachment.Filename, attachment.DevicePath))
	}
	return strings.Join([]string{
		"Files this completion attaches for the user:",
		strings.Join(filenames, "\n"),
		"Judge attachment requirements against exactly this list.",
	}, "\n")
}

func completionJudgeInstruction() string {
	return strings.Join([]string{
		"Judge whether the recorded successful operations actually accomplish the user's original instruction.",
		"Judge only from the recorded ledger facts below. The executor's own completion claims are not evidence.",
		"Accepting this completion delivers the finish reply to the user as the task's answer. Content the finish reply itself carries, such as links, results, and answers, is thereby delivered; never require a separate send or delivery operation for it. The reply's claims about operations it performed remain non-evidence and must match the ledger.",
		"Mark unsatisfied when the recorded operations do not plausibly accomplish the instruction: wrong target, wrong values, or a missing step.",
		"When the instruction states an explicit deadline, date, time, quantity, title, or recipient, that value must appear in at least one successful recorded operation input; if a stated value appears nowhere and no relevant entry is display-truncated, mark unsatisfied and name exactly that value in missingWork.",
		"A ledger entry ending with a display-truncated marker was cut for this display only; the full content was recorded and executed. Content that would lie beyond the cut is unknown, not missing: never cite display truncation as missing work, an incomplete file, or cut-off content.",
		"Resolve relative dates such as today, tomorrow, 오늘, and 내일 only from the runtime temporal context below. Never guess the current date from ledger values.",
		"Judge state changes by the recorded operation results. Items that merely appear inside another result's diagnostic fields, such as candidate lists in a search result, are not additional requirements unless the instruction itself names them.",
		"Do not invent requirements the instruction does not state. Wording, formatting, phrasing, and which list or table a record appears in are not failures. If the right operations ran and every explicitly stated value appears in some recorded input, mark satisfied.",
	}, "\n")
}

func completionJudgePlanContext(observations []turnObservation) string {
	plan, hasPlan := latestPlanUpdate(observations)
	if !hasPlan || len(plan.Steps) == 0 {
		return ""
	}
	document, errorValue := json.Marshal(plan)
	if errorValue != nil {
		return ""
	}
	return "The model's own step plan is below as a checklist hint; the source of truth is the user's original request — verify every part of the request is satisfied.\n" + string(document)
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

func observationsIncludeSideEffect(toolSet *ToolSet, observations []turnObservation) bool {
	for _, observation := range observations {
		if isSideEffectObservation(toolSet, observation) {
			return true
		}
	}
	return false
}

func completionJudgeLedger(toolSet *ToolSet, observations []turnObservation) []completionLedgerEntry {
	ledger := []completionLedgerEntry{}
	for _, observation := range observations {
		if observation.Action != "continue" || observation.Failed() || strings.TrimSpace(observation.Tool) == "" {
			continue
		}
		ledger = append(ledger, completionLedgerEntry{
			Tool:   strings.TrimSpace(observation.Tool),
			Input:  truncateForLedger(string(observation.ToolInput), completionJudgeInputMaxLength),
			Result: truncateForLedger(observation.ContentText(), completionJudgeResultMaxLength),
		})
	}
	return newestLedgerEntriesWithinBudget(ledger, completionJudgeLedgerByteBudget)
}

func newestLedgerEntriesWithinBudget(ledger []completionLedgerEntry, byteBudget int) []completionLedgerEntry {
	usedBytes := 0
	keptFromIndex := len(ledger)
	for entryIndex := len(ledger) - 1; entryIndex >= 0; entryIndex-- {
		entry := ledger[entryIndex]
		entryBytes := len(entry.Tool) + len(entry.Input) + len(entry.Result)
		if usedBytes+entryBytes > byteBudget && keptFromIndex < len(ledger) {
			break
		}
		usedBytes += entryBytes
		keptFromIndex = entryIndex
	}
	if keptFromIndex == 0 {
		return ledger
	}
	kept := ledger[keptFromIndex:]
	marker := completionLedgerEntry{
		Tool:   "earlier_operations",
		Result: "…" + strconv.Itoa(keptFromIndex) + " earlier successful operations were recorded and executed but are not shown here.",
	}
	return append([]completionLedgerEntry{marker}, kept...)
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
	return trimmedValue[:maxLength] + " …[display truncated; full " + strconv.Itoa(len(trimmedValue)) + " bytes were recorded and executed]"
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
