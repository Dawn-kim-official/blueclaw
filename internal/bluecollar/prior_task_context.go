package bluecollar

import (
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"strings"
)

func normalizePriorTaskContext(context PriorTaskContext) PriorTaskContext {
	context.TaskRunID = strings.TrimSpace(context.TaskRunID)
	context.Status = strings.TrimSpace(context.Status)
	context.Prompt = strings.TrimSpace(context.Prompt)
	context.Result = strings.TrimSpace(context.Result)
	context.FailureReason = strings.TrimSpace(context.FailureReason)
	context.OutcomeContract = normalizeOutcomeContract(context.OutcomeContract)
	context.RequestedOutputFormats = normalizeRequestedOutputFormats(context.RequestedOutputFormats)
	return context
}

func priorTaskContextDescription(context PriorTaskContext) string {
	context = normalizePriorTaskContext(context)
	if !priorTaskContextHasContent(context) {
		return ""
	}
	document, errorValue := json.Marshal(context)
	if errorValue != nil {
		return ""
	}
	return strings.Join([]string{
		"Prior task context:",
		string(document),
		"This is context for interpreting the latest user message, not permission to finish from old text. If the latest user message asks to deliver, retry, continue, or revise this prior task's outcome, set priorTaskReference=outcome_recovery. If it is unrelated or self-contained, set priorTaskReference=none. When recovering an outcome, infer the needed structured output formats from the prior task prompt, prior result, known contract, and latest user message. A file deliverable reaches the user only through successful file_deliver completionEvidence in the current task; a prepared file, generated path, task link, or prior result text is not delivery.",
	}, "\n")
}

func priorTaskContextHasContent(context PriorTaskContext) bool {
	return strings.TrimSpace(context.TaskRunID) != "" ||
		strings.TrimSpace(context.Prompt) != "" ||
		OutcomeContractHasRequirements(context.OutcomeContract) ||
		len(context.RequestedOutputFormats) > 0
}

func applyPriorTaskOutcomeRecovery(request AgentRequest, decision IntakeDecision) (AgentRequest, IntakeDecision) {
	if normalizePriorTaskReference(decision.PriorTaskReference) != PriorTaskReferenceOutcomeRecovery {
		return request, decision
	}
	priorTask := normalizePriorTaskContext(request.PriorTask)
	if !priorTaskContextHasContent(priorTask) {
		decision.PriorTaskReference = PriorTaskReferenceNone
		return request, decision
	}
	priorTask.RequestedOutputFormats = normalizeRequestedOutputFormats(appendUniqueStrings(priorTask.RequestedOutputFormats, decision.RequestedOutputFormats...))
	contract := outcomeContractFromPriorTask(priorTask)
	if !OutcomeContractHasRequirements(contract) {
		decision.PriorTaskReference = PriorTaskReferenceNone
		return request, decision
	}
	request.ActiveGoal = ActiveGoal{
		OriginalInstruction: firstNonEmptyString(priorTask.Prompt, request.Prompt),
		CurrentObjective:    request.Prompt,
		KnownContext:        priorTaskKnownContext(priorTask),
		OutcomeContract:     contract,
		Status:              ActiveGoalStatusActive,
	}
	decision.RequestedOutputFormats = normalizeRequestedOutputFormats(appendUniqueStrings(decision.RequestedOutputFormats, priorTask.RequestedOutputFormats...))
	decision.InitialToolNames = appendUniqueStrings(decision.InitialToolNames, contract.SelectedEvidenceHints...)
	decision.InitialToolNames = appendUniqueStrings(decision.InitialToolNames, outcomeContractRequiredToolNames(contract)...)
	return request, decision
}

func outcomeContractFromPriorTask(priorTask PriorTaskContext) OutcomeContract {
	contract := normalizeOutcomeContract(priorTask.OutcomeContract)
	if OutcomeContractHasRequirements(contract) {
		contract.Source = firstNonEmptyString(contract.Source, "prior_task")
		return contract
	}
	requiredAttachmentSuffixes := attachmentSuffixesForRequestedOutputFormats(priorTask.RequestedOutputFormats)
	if len(requiredAttachmentSuffixes) == 0 {
		return contract
	}
	contract.RequiredAttachmentSuffixes = appendUniqueStrings(contract.RequiredAttachmentSuffixes, requiredAttachmentSuffixes...)
	contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, toolcontract.FileDeliverToolName)
	contract.ExpectedResults = appendExpectedResults(contract.ExpectedResults, ExpectedResult{
		ID:              "attached-file",
		Type:            ExpectedResultTypeFile,
		Description:     "At least one file in the requested format is attached for the user",
		Required:        true,
		AcceptanceHints: appendUniqueStrings(requiredAttachmentSuffixes),
	})
	contract.ArtifactRequirement = ArtifactRequirementRequired
	contract.Source = "prior_task"
	return normalizeOutcomeContract(contract)
}

func priorTaskKnownContext(priorTask PriorTaskContext) []string {
	values := []string{}
	if priorTask.TaskRunID != "" {
		values = append(values, "Prior task run: "+priorTask.TaskRunID)
	}
	if priorTask.Status != "" {
		values = append(values, "Prior task status: "+priorTask.Status)
	}
	if priorTask.FailureReason != "" {
		values = append(values, "Prior task failure: "+priorTask.FailureReason)
	}
	if priorTask.Result != "" {
		values = append(values, "Prior task result: "+priorTask.Result)
	}
	return values
}
