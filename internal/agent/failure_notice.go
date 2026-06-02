package agent

import (
	"strconv"
	"strings"
)

const (
	failureNoticeMaximumCharacters = 600
	finishMessageMaximumCharacters = 1200
)

type FailureReport struct {
	Phase               string   `json:"phase,omitempty"`
	StopReason          string   `json:"stopReason,omitempty"`
	FailedOperation     string   `json:"failedOperation,omitempty"`
	SafeFailureSummary  string   `json:"safeFailureSummary,omitempty"`
	CompletedSummary    string   `json:"completedSummary,omitempty"`
	NextAction          string   `json:"nextAction,omitempty"`
	OriginalRequest     string   `json:"originalRequest,omitempty"`
	ResponseLanguage    string   `json:"responseLanguage,omitempty"`
	ArtifactRequired    bool     `json:"artifactRequired,omitempty"`
	HasAttachments      bool     `json:"hasAttachments,omitempty"`
	AttachmentFilenames []string `json:"attachmentFilenames,omitempty"`
	DiagnosticEventID   string   `json:"diagnosticEventID,omitempty"`
}

type FailureNotice struct {
	Message           string `json:"message,omitempty"`
	Source            string `json:"source,omitempty"`
	Language          string `json:"language,omitempty"`
	DiagnosticEventID string `json:"diagnosticEventID,omitempty"`
	IsSendable        bool   `json:"isSendable,omitempty"`
}

func (notice FailureNotice) SendableMessage() string {
	if !notice.IsSendable {
		return ""
	}
	return strings.TrimSpace(notice.Message)
}

func buildFailureReport(request AgentTurnRequest, taskRunID string, phase string, stopReason string, observations []turnObservation, attachments []FileAttachment, executionState ExecutionState, decision recoveryDecision) FailureReport {
	report := FailureReport{
		Phase:               strings.TrimSpace(phase),
		StopReason:          compactWhitespace(strings.TrimSpace(stopReason)),
		FailedOperation:     latestFailedOperation(observations),
		SafeFailureSummary:  latestSafeFailureSummary(observations, stopReason),
		CompletedSummary:    buildLimitObservationSummary(observations),
		NextAction:          strings.TrimSpace(decision.NextAction),
		OriginalRequest:     strings.TrimSpace(request.Prompt),
		ResponseLanguage:    strings.TrimSpace(request.ResponseLanguage),
		ArtifactRequired:    requestRequiresDurableArtifact(request),
		HasAttachments:      len(attachments) > 0,
		AttachmentFilenames: failureReportAttachmentFilenames(attachments),
		DiagnosticEventID:   diagnosticEventID(request, taskRunID, phase),
	}
	if report.NextAction == "" {
		report.NextAction = strings.TrimSpace(decision.UserReplyIntent)
	}
	if report.NextAction == "" {
		report.NextAction = strings.TrimSpace(executionState.NextPlan)
	}
	return report
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

func failureReportAttachmentFilenames(attachments []FileAttachment) []string {
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

func buildFailureNoticePrompt(report FailureReport) string {
	sections := []string{
		"You are writing a short user-facing failure notice.",
		responseLanguageInstruction(report.ResponseLanguage),
		"Use only the compact failure context below. Do not infer from earlier conversation history.",
		"Write one or two natural sentences.",
		"Keep the notice under 600 Korean characters or an equivalent short length.",
		"Preserve the safe meaning of the failure, but do not expose provider errors, stack traces, internal service URLs, internal filesystem paths, tokens, or serialized reply status.",
		"Do not claim an attachment or completed artifact exists unless attachment filenames are listed.",
		"Compact failure context:\n" + marshalEventBody(report),
	}
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

func failureNoticeMessageIsSendableForReport(message string, report FailureReport) bool {
	if !failureNoticeMessageIsSendable(message) {
		return false
	}
	if report.ArtifactRequired && offersChatTextAsArtifactSubstitute(message) {
		return false
	}
	return true
}

func failureNoticeMessageIsSendable(message string) bool {
	trimmedMessage := strings.TrimSpace(message)
	if trimmedMessage == "" {
		return false
	}
	if len([]rune(trimmedMessage)) > failureNoticeMaximumCharacters {
		return false
	}
	if ValidateUserNoticeDelivery(trimmedMessage) != nil {
		return false
	}
	return !containsInternalDiagnosticLeak(trimmedMessage)
}

func offersChatTextAsArtifactSubstitute(message string) bool {
	normalizedMessage := strings.ToLower(strings.TrimSpace(message))
	for _, fragment := range []string{"텍스트로", "글로 정리", "채팅으로", "이곳에 바로", "here in chat", "as text"} {
		if strings.Contains(normalizedMessage, fragment) {
			return true
		}
	}
	return false
}

func containsInternalDiagnosticLeak(message string) bool {
	normalizedMessage := strings.ToLower(strings.TrimSpace(message))
	if normalizedMessage == "" {
		return false
	}
	for _, fragment := range internalDiagnosticLeakFragments() {
		if strings.Contains(normalizedMessage, fragment) {
			return true
		}
	}
	return containsInternalFilesystemPath(normalizedMessage)
}

func internalDiagnosticLeakFragments() []string {
	return []string{
		"replystatus",
		"reply_status",
		"reply_reason",
		"what_failed",
		"textrecoveryerror",
		"text_recovery_error",
		"structuredrecoveryerror",
		"structured_recovery_error",
		"source=suppressed",
		"context deadline exceeded",
		"/v1/llm/text",
		"internkim-capability",
		"blueclaw-runtime",
		"traceback",
		"stack trace",
		"goroutine ",
		"panic:",
		"authorization:",
		"bearer ",
		"api_key",
		"apikey",
		"token=",
	}
}

func containsInternalFilesystemPath(message string) bool {
	for _, fragment := range []string{"/workspace/.blueclaw", "/root/", "/home/", "/var/folders/", "/private/var/", "/tmp/"} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func textExceedsCharacterBudget(value string, maximumCharacters int) bool {
	return maximumCharacters > 0 && len([]rune(strings.TrimSpace(value))) > maximumCharacters
}
