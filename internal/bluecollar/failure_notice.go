package bluecollar

import (
	"context"
	"errors"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"regexp"
	"strconv"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/model"
)

const (
	failureNoticeMaximumCharacters = 600
	finishMessageMaximumCharacters = 1200
)

type FailureReport struct {
	Phase               string   `json:"phase,omitempty"`
	StepName            string   `json:"stepName,omitempty"`
	StopReason          string   `json:"stopReason,omitempty"`
	FailedOperation     string   `json:"failedOperation,omitempty"`
	SafeFailureSummary  string   `json:"safeFailureSummary,omitempty"`
	RawError            string   `json:"rawError,omitempty"`
	CompletedSummary    string   `json:"completedSummary,omitempty"`
	NextAction          string   `json:"nextAction,omitempty"`
	OriginalRequest     string   `json:"originalRequest,omitempty"`
	ResponseLanguage    string   `json:"responseLanguage,omitempty"`
	ArtifactRequired    bool     `json:"artifactRequired,omitempty"`
	HasAttachments      bool     `json:"hasAttachments,omitempty"`
	AttachmentFilenames []string `json:"attachmentFilenames,omitempty"`
	DiagnosticEventID   string   `json:"diagnosticEventID,omitempty"`
}

type FailureNoticeGenerationStatus struct {
	Source             string `json:"source"`
	FirstInvalid       bool   `json:"firstInvalid"`
	RepairCount        int    `json:"repairCount"`
	Reason             string `json:"reason,omitempty"`
	TextRecoveryError  string `json:"textRecoveryError,omitempty"`
	LocalRecoveryError string `json:"localRecoveryError,omitempty"`
	OriginalWasInvalid bool   `json:"originalWasInvalid,omitempty"`
}

const elapsedLimitRawErrorSummary = "Execution limit reached; completed progress was saved for continuation."

type FailureNoticeGenerator struct {
	LanguageModel model.LanguageModelProvider
}

type IntakeReport struct {
	Classification    IntakeClassification `json:"classification,omitempty"`
	Reason            string               `json:"reason,omitempty"`
	OriginalRequest   string               `json:"originalRequest,omitempty"`
	ResponseLanguage  string               `json:"responseLanguage,omitempty"`
	DiagnosticEventID string               `json:"diagnosticEventID,omitempty"`
}

func buildFailureReport(request AgentTurnRequest, taskRunID string, phase string, stopReason string, observations []turnObservation, attachments []toolcontract.FileAttachment, executionState ExecutionState, decision recoveryDecision) FailureReport {
	report := FailureReport{
		Phase:               strings.TrimSpace(phase),
		StopReason:          compactWhitespace(strings.TrimSpace(stopReason)),
		RawError:            compactWhitespace(redactRawFailureNotice(strings.TrimSpace(stopReason))),
		FailedOperation:     latestFailedOperation(observations),
		SafeFailureSummary:  latestSafeFailureSummary(observations, stopReason),
		CompletedSummary:    buildLimitObservationSummary(observations),
		NextAction:          strings.TrimSpace(decision.NextAction),
		OriginalRequest:     strings.TrimSpace(request.Prompt),
		ResponseLanguage:    strings.TrimSpace(request.ResponseLanguage),
		ArtifactRequired:    requestRequiresFileAttachment(request),
		HasAttachments:      len(attachments) > 0,
		AttachmentFilenames: failureReportAttachmentFilenames(attachments),
		DiagnosticEventID:   diagnosticEventID(request, taskRunID, phase),
	}
	if report.Phase == "limit" && report.StopReason == "max_elapsed" {
		report.RawError = elapsedLimitRawErrorSummary
	}
	if report.NextAction == "" {
		report.NextAction = strings.TrimSpace(decision.UserReplyIntent)
	}
	if report.NextAction == "" {
		report.NextAction = strings.TrimSpace(executionState.NextPlan)
	}
	return report
}

func recoveryFinalizationContextWithParent(parentContext context.Context, request AgentTurnRequest) (context.Context, context.CancelFunc) {
	recoveryContext := model.ContextWithRequestContext(parentContext, model.RequestContext{
		RequesterPersonID:       request.RequesterPersonID,
		RequesterEmail:          request.RequesterEmail,
		RequesterName:           request.RequesterName,
		RequesterPlatformUserID: request.RequesterPlatformUserID,
		ConversationID:          request.ConversationID,
		Platform:                request.Platform,
	})
	return context.WithCancel(recoveryContext)
}

func buildElapsedLimitRawErrorFailureNotice(request AgentTurnRequest) FailureNotice {
	report := FailureReport{
		Phase:            "limit",
		StopReason:       "max_elapsed",
		RawError:         elapsedLimitRawErrorSummary,
		ResponseLanguage: request.ResponseLanguage,
		OriginalRequest:  request.Prompt,
	}
	return buildRawErrorFailureNotice(report)
}

func (generator FailureNoticeGenerator) Generate(ctx context.Context, report FailureReport) (FailureNotice, FailureNoticeGenerationStatus) {
	report = normalizeFailureReport(report)
	generationContext := ctx
	status := FailureNoticeGenerationStatus{}
	if generator.LanguageModel == nil {
		status.Source = "raw_error"
		status.Reason = "language_model_unavailable"
		return buildRawErrorFailureNotice(report), status
	}
	reply, errorValue := generator.generateRecoveryText(generationContext, "blueclaw_failure_notice", buildFailureNoticePrompt(report))
	if errorValue == nil {
		if notice, source, hasNotice := prepareFailureNoticeWithGenerator(generator, generationContext, reply, "generated", report); hasNotice {
			status.Source = source
			return notice, status
		}
	}
	if contextError := recoveryContextError(generationContext, errorValue); contextError != nil {
		status.Source = "raw_error"
		status.Reason = "request_aborted"
		status.TextRecoveryError = contextError.Error()
		return buildRawErrorFailureNotice(report), status
	}
	if strings.TrimSpace(reply) != "" {
		status.FirstInvalid = true
		for repairCount := 1; repairCount <= 2; repairCount++ {
			repairedReply, repairError := generator.generateRecoveryText(generationContext, "blueclaw_failure_notice_repair", buildFailureNoticeRepairPrompt(report, reply, repairCount))
			if repairError != nil || strings.TrimSpace(repairedReply) == "" {
				status.RepairCount = repairCount
				status.TextRecoveryError = firstNonEmptyString(errorString(repairError), "empty_repair")
				break
			}
			notice, source, hasNotice := prepareFailureNoticeWithGenerator(generator, generationContext, repairedReply, "generated_repair", report)
			if hasNotice {
				status.Source = source
				status.RepairCount = repairCount
				return notice, status
			}
			reply = repairedReply
		}
		if status.RepairCount == 0 {
			status.RepairCount = 2
		}
	}
	if generationContext.Err() != nil {
		status.Source = "raw_error"
		status.Reason = "request_aborted"
		status.TextRecoveryError = generationContext.Err().Error()
		return buildRawErrorFailureNotice(report), status
	}
	if notice, localError, hasNotice := generator.generateLocalFailureNotice(generationContext, report, reply); hasNotice {
		status.Source = notice.Source
		status.Reason = "local_recovery_after_text_failure"
		status.TextRecoveryError = firstNonEmptyString(status.TextRecoveryError, errorString(errorValue), "invalid_generated_reply")
		return notice, status
	} else if localError != "" {
		status.LocalRecoveryError = localError
	}
	status.Source = "raw_error"
	status.Reason = firstNonEmptyString(status.Reason, "text_recovery_failed")
	status.TextRecoveryError = firstNonEmptyString(status.TextRecoveryError, errorString(errorValue), "invalid_generated_reply")
	return buildRawErrorFailureNotice(report), status
}

func (generator FailureNoticeGenerator) GenerateIntakeNotice(ctx context.Context, report IntakeReport) FailureNotice {
	failureReport := normalizeFailureReport(FailureReport{
		Phase:             "task_intake",
		StopReason:        report.Reason,
		OriginalRequest:   report.OriginalRequest,
		ResponseLanguage:  report.ResponseLanguage,
		DiagnosticEventID: report.DiagnosticEventID,
	})
	if generator.LanguageModel == nil {
		return buildRawErrorFailureNotice(failureReport)
	}
	generationContext, cancel := context.WithCancel(ctx)
	defer cancel()
	prompt := buildIntakeNoticePrompt(report.Classification, failureReport)
	reply, errorValue := generator.generateRecoveryText(generationContext, "blueclaw_intake_notice", prompt)
	if errorValue == nil {
		if notice := buildFailureNotice(reply, "generated", failureReport); notice.IsSendable {
			return notice
		}
	}
	if recoveryContextError(generationContext, errorValue) != nil {
		return buildRawErrorFailureNotice(failureReport)
	}
	localReply, localError := generator.generateLocalRecoveryText(generationContext, "blueclaw_intake_notice", prompt)
	if localError == nil {
		if notice := buildFailureNotice(localReply, "local_generated", failureReport); notice.IsSendable {
			return notice
		}
	}
	return buildRawErrorFailureNotice(failureReport)
}

func buildIntakeNoticePrompt(classification IntakeClassification, report FailureReport) string {
	sections := []string{
		"You are writing a short user-facing reply for a request that was not started.",
		responseLanguageInstruction(report.ResponseLanguage),
		intakeNoticeIntent(classification),
		"Use only the compact intake context below. Do not infer from earlier conversation history.",
		"Write one or two natural sentences.",
		"Do not expose provider errors, internal service URLs, internal filesystem paths, or tokens.",
		"Compact intake context:\n" + marshalEventBody(report),
	}
	return strings.Join(sections, "\n\n")
}

func intakeNoticeIntent(classification IntakeClassification) string {
	switch classification {
	case IntakeClassificationNeedsConfirmation:
		return "Ask the user to confirm a narrower scope or split the request into smaller steps before work starts."
	case IntakeClassificationUnsupported:
		return "Explain that the request cannot run safely within the current execution boundary and suggest narrowing it."
	default:
		return "Explain briefly why the request was not started and what the user can do next."
	}
}

func normalizeFailureReport(report FailureReport) FailureReport {
	report.Phase = strings.TrimSpace(report.Phase)
	report.StepName = strings.TrimSpace(report.StepName)
	report.StopReason = compactWhitespace(strings.TrimSpace(report.StopReason))
	report.SafeFailureSummary = compactWhitespace(strings.TrimSpace(report.SafeFailureSummary))
	report.RawError = compactWhitespace(redactRawFailureNotice(strings.TrimSpace(report.RawError)))
	report.OriginalRequest = strings.TrimSpace(report.OriginalRequest)
	report.ResponseLanguage = ResolveResponseLanguage(report.ResponseLanguage)
	report.DiagnosticEventID = strings.TrimSpace(report.DiagnosticEventID)
	if report.SafeFailureSummary == "" {
		report.SafeFailureSummary = report.StopReason
	}
	if report.RawError == "" {
		report.RawError = redactRawFailureNotice(firstNonEmptyString(report.StopReason, report.SafeFailureSummary))
	}
	return report
}

func (generator FailureNoticeGenerator) generateRecoveryText(ctx context.Context, schemaName string, prompt string) (string, error) {
	reply, errorValue, isSupported := generateRecoveryChatText(ctx, generator.LanguageModel, schemaName, prompt)
	if !isSupported {
		return "", errors.New("recovery chat completion unavailable")
	}
	return reply, errorValue
}

func (generator FailureNoticeGenerator) generateLocalRecoveryText(ctx context.Context, schemaName string, prompt string) (string, error) {
	reply, errorValue, isSupported := generateLocalRecoveryChatText(ctx, generator.LanguageModel, schemaName, prompt)
	if !isSupported {
		return "", errors.New("local recovery chat completion unavailable")
	}
	return reply, errorValue
}

func generateRecoveryChatText(ctx context.Context, provider model.LanguageModelProvider, schemaName string, prompt string) (string, error, bool) {
	recoveryProvider, isAvailable := model.ResolveRecoveryChatCompleter(provider)
	if !isAvailable {
		return "", nil, false
	}
	response, errorValue := recoveryProvider.GenerateRecoveryChatCompletion(ctx, recoveryChatCompletionRequest(schemaName, prompt))
	if errorValue != nil {
		return "", errorValue, true
	}
	reply, errorValue := model.RecoveryChatCompletionText(response)
	return reply, errorValue, true
}

func generateLocalRecoveryChatText(ctx context.Context, provider model.LanguageModelProvider, schemaName string, prompt string) (string, error, bool) {
	localRecoveryProvider, isAvailable := model.ResolveLocalRecoveryChatCompleter(provider)
	if !isAvailable {
		return "", nil, false
	}
	response, errorValue := localRecoveryProvider.GenerateLocalRecoveryChatCompletion(ctx, recoveryChatCompletionRequest(schemaName, prompt))
	if errorValue != nil {
		return "", errorValue, true
	}
	reply, errorValue := model.RecoveryChatCompletionText(response)
	return reply, errorValue, true
}

func recoveryChatCompletionRequest(schemaName string, prompt string) model.ChatCompletionRequest {
	return model.ChatCompletionRequest{
		SchemaName: schemaName,
		Messages: []model.ChatCompletionMessage{{
			Role:    "user",
			Content: prompt,
		}},
	}
}

func recoveryContextError(ctx context.Context, errorValue error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(errorValue, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(errorValue, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}

func (generator FailureNoticeGenerator) generateLocalFailureNotice(ctx context.Context, report FailureReport, rejectedReply string) (FailureNotice, string, bool) {
	prompt := buildFailureNoticePrompt(report)
	if strings.TrimSpace(rejectedReply) != "" {
		prompt = buildFailureNoticeRepairPrompt(report, rejectedReply, 3)
	}
	reply, errorValue := generator.generateLocalRecoveryText(ctx, "blueclaw_failure_notice", prompt)
	if errorValue != nil || strings.TrimSpace(reply) == "" {
		return FailureNotice{}, firstNonEmptyString(errorString(errorValue), "empty_local_reply"), false
	}
	notice, _, hasNotice := prepareFailureNoticeWithGenerator(generator, ctx, reply, "local_generated", report)
	if hasNotice {
		return notice, "", true
	}
	for repairCount := 1; repairCount <= 2; repairCount++ {
		repairedReply, repairError := generator.generateLocalRecoveryText(ctx, "blueclaw_failure_notice_repair", buildFailureNoticeRepairPrompt(report, reply, repairCount))
		if repairError != nil || strings.TrimSpace(repairedReply) == "" {
			return FailureNotice{}, firstNonEmptyString(errorString(repairError), "empty_local_repair"), false
		}
		notice, _, hasNotice := prepareFailureNoticeWithGenerator(generator, ctx, repairedReply, "local_generated", report)
		if hasNotice {
			return notice, "", true
		}
		reply = repairedReply
	}
	return FailureNotice{}, "invalid_local_generated_reply", false
}

func prepareFailureNoticeWithGenerator(generator FailureNoticeGenerator, ctx context.Context, reply string, source string, report FailureReport) (FailureNotice, string, bool) {
	candidate := strings.TrimSpace(reply)
	if textExceedsCharacterBudget(candidate, failureNoticeMaximumCharacters) {
		compressedReply, errorValue := generator.generateRecoveryText(ctx, "blueclaw_failure_notice_compression", buildFailureNoticeCompressionPrompt(report, reply, failureNoticeMaximumCharacters))
		if errorValue != nil || strings.TrimSpace(compressedReply) == "" {
			return FailureNotice{}, "", false
		}
		candidate = strings.TrimSpace(compressedReply)
	}
	if !failureNoticeRequiresReview(report) {
		notice := buildFailureNotice(candidate, source, report)
		return notice, notice.Source, notice.IsSendable
	}
	reviewedReply, reviewedSource, hasReviewedReply := generator.reviewFailureNotice(ctx, report, candidate, source)
	if !hasReviewedReply {
		return FailureNotice{}, "", false
	}
	notice := buildFailureNotice(reviewedReply, reviewedSource, report)
	if notice.IsSendable {
		return notice, reviewedSource, true
	}
	return FailureNotice{}, "", false
}

func failureNoticeRequiresReview(report FailureReport) bool {
	return strings.TrimSpace(report.Phase) == "stall"
}

func (generator FailureNoticeGenerator) reviewFailureNotice(ctx context.Context, report FailureReport, candidate string, source string) (string, string, bool) {
	trimmedCandidate := strings.TrimSpace(candidate)
	if trimmedCandidate == "" {
		return "", "", false
	}
	prompt := strings.Join([]string{
		"Review and, when needed, rewrite this user-facing failure notice.",
		responseLanguageInstruction(report.ResponseLanguage),
		"Return only the final notice. Ground it in the compact failure context.",
		"Keep it concise and natural. Do not expose provider errors, stack traces, internal service URLs, internal filesystem paths, tokens, serialized status, or false artifact delivery claims.",
		"Compact failure context:\n" + marshalEventBody(report),
		"Candidate notice:\n" + trimmedCandidate,
	}, "\n\n")
	message, errorValue := generator.generateFailureNoticeReview(ctx, source, prompt)
	if errorValue != nil {
		return "", "", false
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", "", false
	}
	return message, source + "_review", true
}

func (generator FailureNoticeGenerator) generateFailureNoticeReview(ctx context.Context, source string, prompt string) (string, error) {
	if strings.HasPrefix(source, "local_") {
		return generator.generateLocalRecoveryText(ctx, "blueclaw_failure_notice_review", prompt)
	}
	return generator.generateRecoveryText(ctx, "blueclaw_failure_notice_review", prompt)
}

func buildRawErrorFailureNotice(report FailureReport) FailureNotice {
	message := firstNonEmptyString(report.RawError, report.SafeFailureSummary, report.StopReason)
	message = truncateText(compactWhitespace(redactRawFailureNotice(message)), failureNoticeMaximumCharacters)
	return FailureNotice{
		Message:           message,
		Source:            "raw_error",
		Language:          strings.TrimSpace(report.ResponseLanguage),
		DiagnosticEventID: strings.TrimSpace(report.DiagnosticEventID),
		IsSendable:        strings.TrimSpace(message) != "",
	}
}

var rawFailureNoticePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)((?:api[_-]?key|token|secret|authorization)\s*[:=]\s*)[^\s,;]+`),
	regexp.MustCompile(`(?i)sk-[A-Za-z0-9_-]{8,}`),
}

func redactRawFailureNotice(message string) string {
	redactedMessage := strings.TrimSpace(message)
	for _, pattern := range rawFailureNoticePatterns {
		redactedMessage = pattern.ReplaceAllString(redactedMessage, "${1}[redacted]")
	}
	return redactedMessage
}

func latestFailedOperation(observations []turnObservation) string {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if !observation.Failed() {
			continue
		}
		operation := strings.TrimSpace(observation.Tool)
		if operation == "" {
			operation = strings.TrimSpace(observation.Action)
		}
		return operation
	}
	return ""
}

func latestSafeFailureSummary(observations []turnObservation, fallback string) string {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if !observation.Failed() {
			continue
		}
		summary := strings.TrimSpace(observation.FailureSummary())
		if summary == "" {
			summary = strings.TrimSpace(summarizeObservationContent(observation))
		}
		if summary != "" {
			return truncateText(compactWhitespace(redactUnsafeText(summary)), 360)
		}
	}
	return truncateText(compactWhitespace(redactUnsafeText(fallback)), 360)
}

func failureReportAttachmentFilenames(attachments []toolcontract.FileAttachment) []string {
	filenames := []string{}
	seenFilenames := map[string]bool{}
	for _, attachment := range attachments {
		filename := strings.TrimSpace(attachment.Filename)
		if filename == "" || seenFilenames[filename] {
			continue
		}
		seenFilenames[filename] = true
		filenames = append(filenames, filename)
	}
	return filenames
}

func diagnosticEventID(request AgentTurnRequest, taskRunID string, phase string) string {
	if strings.TrimSpace(taskRunID) != "" {
		return strings.TrimSpace(taskRunID) + ":" + strings.TrimSpace(phase)
	}
	if strings.TrimSpace(request.ExistingTaskRunID) != "" {
		return strings.TrimSpace(request.ExistingTaskRunID) + ":" + strings.TrimSpace(phase)
	}
	if strings.TrimSpace(request.ConversationID) != "" {
		return strings.TrimSpace(request.ConversationID) + ":" + strings.TrimSpace(phase)
	}
	return strings.TrimSpace(phase)
}

func failureNoticeFramingInstruction(phase string) string {
	if phase == "stall" {
		return "You are writing a short user-facing notice that the task is paused because repeated attempts stopped making progress. Explain in plain terms what is stuck, then end with one concrete question asking how the user wants to proceed."
	}
	if phase == "limit" {
		return "You are writing a short user-facing notice that the run stopped because it reached its time or step limit."
	}
	return "You are writing a short user-facing failure notice."
}

func failureNoticeCompletedSummaryInstruction(report FailureReport) string {
	if report.Phase != "limit" || strings.TrimSpace(report.CompletedSummary) == "" {
		return ""
	}
	return "completedSummary in the compact failure context lists concrete partial findings already gathered before the limit was reached. Your reply must state those concrete findings for the user first; only mention the time/step limit as the reason work stopped."
}

func buildFailureNoticePrompt(report FailureReport) string {
	sections := []string{
		failureNoticeFramingInstruction(report.Phase),
		responseLanguageInstruction(report.ResponseLanguage),
		"Use only the compact failure context below. Do not infer from earlier conversation history.",
	}
	if completedSummaryInstruction := failureNoticeCompletedSummaryInstruction(report); completedSummaryInstruction != "" {
		sections = append(sections, completedSummaryInstruction)
	}
	sections = append(sections,
		"Never claim a retry or recovery is currently underway, and never promise the system will follow up on its own: nothing runs after this notice. Work continues only if the user replies or asks again.",
		"Write one or two natural sentences.",
		"Keep the notice under 600 Korean characters or an equivalent short length.",
		"Preserve the safe meaning of the failure, but do not expose provider errors, stack traces, internal service URLs, internal filesystem paths, tokens, or serialized reply status.",
		"Do not claim an attachment or completed artifact exists unless attachment filenames are listed.",
	)
	if report.ArtifactRequired && len(report.AttachmentFilenames) > 0 {
		sections = append(sections, "A requested file artifact WAS delivered and is attached ("+strings.Join(report.AttachmentFilenames, ", ")+"). Acknowledge the attached file as the current result. Do not claim it was not created, not made, or not delivered. If the run stopped before further refinement, say only that this delivered version is the best result so far and further polishing was interrupted.")
	}
	if report.ArtifactRequired && len(report.AttachmentFilenames) == 0 {
		sections = append(sections, "The requested file artifact was not delivered. State that plainly, summarize the concrete failed operation and safe failure reason, and give the next practical check. Do not offer chat text as a substitute.")
	}
	sections = append(sections, "Compact failure context:\n"+marshalEventBody(report))
	return strings.Join(sections, "\n\n")
}

func buildFailureNoticeRepairPrompt(report FailureReport, rejectedReply string, repairCount int) string {
	sections := []string{
		buildFailureNoticePrompt(report),
		"Previous draft was rejected because it was unsafe, too long, exposed internal diagnostics, or claimed unavailable delivery.",
		"Rewrite it as a concise user-facing notice. Preserve only safe facts from the compact context.",
		"Rejected draft:\n" + strings.TrimSpace(rejectedReply),
	}
	if repairCount > 1 {
		sections = append(sections, "Use the shortest clear wording that still names what could not be completed and the next check.")
	}
	return strings.Join(sections, "\n\n")
}

func buildFailureNoticeCompressionPrompt(report FailureReport, reply string, maximumCharacters int) string {
	return strings.Join([]string{
		"You are compressing a user-facing failure notice for Mattermost.",
		responseLanguageInstruction(report.ResponseLanguage),
		"Keep the same meaning and omit internal diagnostics.",
		"Write one or two natural sentences.",
		"Maximum characters: " + strconv.Itoa(maximumCharacters),
		"Compact failure context:\n" + marshalEventBody(report),
		"Notice to compress:\n" + strings.TrimSpace(reply),
	}, "\n\n")
}

func buildFinishMessageCompressionPrompt(reply string, responseLanguage string, maximumCharacters int) string {
	return strings.Join([]string{
		"You are compressing a successful user-facing reply for Mattermost.",
		responseLanguageInstruction(responseLanguage),
		"Keep the concrete result, attachment filenames, and next useful action if present.",
		"Do not add claims that were not in the original reply.",
		"Write a concise reply under the character limit.",
		"Maximum characters: " + strconv.Itoa(maximumCharacters),
		"Original reply:\n" + strings.TrimSpace(reply),
	}, "\n\n")
}

func buildFailureNotice(message string, source string, report FailureReport) FailureNotice {
	trimmedMessage := strings.TrimSpace(message)
	return FailureNotice{
		Message:           trimmedMessage,
		Source:            strings.TrimSpace(source),
		Language:          strings.TrimSpace(report.ResponseLanguage),
		DiagnosticEventID: strings.TrimSpace(report.DiagnosticEventID),
		IsSendable:        failureNoticeMessageIsSendableForReport(trimmedMessage, report),
	}
}

func failureNoticeMessageIsSendableForReport(message string, _ FailureReport) bool {
	return failureNoticeMessageIsSendable(message)
}

func failureNoticeMessageIsSendable(message string) bool {
	trimmedMessage := strings.TrimSpace(message)
	if trimmedMessage == "" {
		return false
	}
	if len([]rune(trimmedMessage)) > failureNoticeMaximumCharacters {
		return false
	}
	return true
}

func textExceedsCharacterBudget(value string, maximumCharacters int) bool {
	return maximumCharacters > 0 && len([]rune(strings.TrimSpace(value))) > maximumCharacters
}
