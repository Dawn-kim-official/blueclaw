package bluecollar

import (
	"encoding/json"
	"fmt"
	"strings"
)

const progressMessageLimit = 6000
const toolResultContextLimit = 12000
const maxProgressObservations = 12
const maxInteractiveReferences = 20
const maxSummaryTextLength = 500

type TurnProgress struct {
	Goal                          string                `json:"goal"`
	CompletedSteps                []ProgressObservation `json:"completedSteps,omitempty"`
	FailedOrBlockedSteps          []ProgressObservation `json:"failedOrBlockedSteps,omitempty"`
	CheckpointMessages            []string              `json:"checkpointMessages,omitempty"`
	RecentFiles                   []ProgressFileContext `json:"recentFiles,omitempty"`
	LastSuccessfulObservationID   string                `json:"lastSuccessfulObservationID,omitempty"`
	LastSuccessfulObservationTool string                `json:"lastSuccessfulObservationTool,omitempty"`
	AttachmentCandidates          []ProgressAttachment  `json:"attachmentCandidates,omitempty"`
	FailureDebt                   *ProgressFailureDebt  `json:"failureDebt,omitempty"`
	AttemptLedger                 []attemptLedgerEntry  `json:"attemptLedger,omitempty"`
	CompletionState               *CompletionState      `json:"completionState,omitempty"`
	ValidityState                 *ValidityState        `json:"validityState,omitempty"`
	RemainingWork                 string                `json:"remainingWork"`
	OmittedObservationCount       int                   `json:"omittedObservationCount,omitempty"`
}

type ProgressObservation struct {
	ObservationID      string               `json:"observationID"`
	ToolName           string               `json:"toolName,omitempty"`
	Status             string               `json:"status"`
	Summary            string               `json:"summary"`
	AttachmentRefs     []ProgressAttachment `json:"attachmentRefs,omitempty"`
	ImageRefs          []ToolResultImageRef `json:"imageRefs,omitempty"`
	AttemptFingerprint string               `json:"attemptFingerprint,omitempty"`
	RecoveryStep       string               `json:"recoveryStep,omitempty"`
	RepeatCount        int                  `json:"repeatCount,omitempty"`
}

type ProgressFailureDebt struct {
	ObservationID           string         `json:"observationID"`
	ToolName                string         `json:"toolName"`
	FailureStage            string         `json:"failureStage,omitempty"`
	ErrorCode               string         `json:"errorCode,omitempty"`
	AttemptFingerprint      string         `json:"attemptFingerprint,omitempty"`
	RemainingRecoveryBudget RecoveryBudget `json:"remainingRecoveryBudget"`
	AllowedFinalResolutions []string       `json:"allowedFinalResolutions"`
}

type ProgressAttachment struct {
	ObservationID    string `json:"observationID"`
	AttachmentIndex  int    `json:"attachmentIndex"`
	Filename         string `json:"filename,omitempty"`
	ContentType      string `json:"contentType,omitempty"`
	SizeBytes        int64  `json:"sizeBytes,omitempty"`
	Title            string `json:"title,omitempty"`
	HasDevicePayload bool   `json:"hasDevicePayload"`
}

type ProgressFileContext struct {
	Path                 string   `json:"path"`
	LastObservationID    string   `json:"lastObservationID"`
	ReadRanges           []string `json:"readRanges,omitempty"`
	TotalLines           int      `json:"totalLines,omitempty"`
	TotalLinesKnown      bool     `json:"totalLinesKnown,omitempty"`
	SizeBytes            int      `json:"sizeBytes,omitempty"`
	OriginalSizeBytes    int      `json:"originalSizeBytes,omitempty"`
	ReturnedBytes        int      `json:"returnedBytes,omitempty"`
	IsTruncated          bool     `json:"isTruncated,omitempty"`
	Summary              string   `json:"summary,omitempty"`
	Snippet              string   `json:"snippet,omitempty"`
	MarkdownPreview      string   `json:"markdownPreview,omitempty"`
	ConversionStatus     string   `json:"conversionStatus,omitempty"`
	ConversionMessage    string   `json:"conversionMessage,omitempty"`
	RepeatedReadGuidance string   `json:"repeatedReadGuidance,omitempty"`
}

type ToolResultContextItem struct {
	ObservationID  string               `json:"observationID"`
	ToolName       string               `json:"toolName,omitempty"`
	Status         string               `json:"status"`
	Summary        string               `json:"summary"`
	RecoveryPacket *RecoveryPacket      `json:"recoveryPacket,omitempty"`
	ImageRefs      []ToolResultImageRef `json:"imageRefs,omitempty"`
	Attachments    []ProgressAttachment `json:"attachments,omitempty"`
}

func buildTurnProgress(request AgentTurnRequest, observations []turnObservation) TurnProgress {
	progress := TurnProgress{
		Goal:          "Answer the current user request.",
		RemainingWork: "Continue from the latest observation and complete the user's request.",
	}
	progress.CheckpointMessages = checkpointMessages(observations)
	for _, observation := range compactProgressObservations(observations) {
		if observation.Status == "success" {
			progress.CompletedSteps = append(progress.CompletedSteps, observation)
			progress.LastSuccessfulObservationID = observation.ObservationID
			progress.LastSuccessfulObservationTool = observation.ToolName
			progress.AttachmentCandidates = append(progress.AttachmentCandidates, observation.AttachmentRefs...)
		} else {
			progress.FailedOrBlockedSteps = append(progress.FailedOrBlockedSteps, observation)
		}
	}
	progress.AttemptLedger = attemptLedger(observations)
	progress.RecentFiles = recentFileContexts(observations)
	progress.OmittedObservationCount = omittedObservationCount(progress)
	progress.CompletedSteps = keepLatestProgressObservations(progress.CompletedSteps)
	progress.FailedOrBlockedSteps = keepLatestProgressObservations(progress.FailedOrBlockedSteps)
	if failureDebt, hasFailureDebt := activeFailureDebt(observations); hasFailureDebt {
		progress.FailureDebt = buildProgressFailureDebt(failureDebt, observations)
	}
	if len(observations) > 0 && progress.LastSuccessfulObservationID == "" {
		progress.RemainingWork = "Resolve the latest failed or blocked step, or return a truthful failure if the goal cannot be completed."
	}
	requirements := deriveToolUseRequirements(request)
	if len(requirements) > 0 {
		completionState := buildCompletionState(request, requirements, observations)
		progress.CompletionState = &completionState
		progress.ValidityState = &completionState.ValidityState
		progress.RemainingWork = completionRemainingWork(completionState, progress.RemainingWork)
	}
	return progress
}

func checkpointMessages(observations []turnObservation) []string {
	messages := []string{}
	for _, observation := range observations {
		if observation.Action != "checkpoint" {
			continue
		}
		message := strings.TrimSpace(checkpointObservationMessage(observation))
		if message != "" {
			messages = append(messages, message)
		}
	}
	return messages
}

func completionRemainingWork(completionState CompletionState, fallback string) string {
	switch completionState.RecommendedAction {
	case completionActionAttachExistingArtifacts:
		return "Required artifacts already exist in the workspace. Attach the existing artifacts instead of rebuilding them."
	case completionActionFinalizeWithEvidence:
		return "Required evidence is attached. Return the final answer without starting more tool work."
	case completionActionBlockedMissingTool:
		return "Required artifact evidence exists, but the attachment tool is unavailable for this profile."
	case completionActionBlockedInvalidArtifact:
		return "Artifact candidates exist but failed validity checks. Regenerate or repair the artifacts before attaching them."
	default:
		return fallback
	}
}

func compactProgressObservations(observations []turnObservation) []ProgressObservation {
	compactedObservations := []ProgressObservation{}
	for _, observation := range observations {
		if observation.Action == "checkpoint" {
			continue
		}
		progressObservation := summarizeObservation(observation)
		index := len(compactedObservations) - 1
		if index >= 0 && progressObservationSignature(compactedObservations[index]) == progressObservationSignature(progressObservation) {
			compactedObservations[index].RepeatCount++
			compactedObservations[index].ObservationID = progressObservation.ObservationID
			compactedObservations[index].AttachmentRefs = append(compactedObservations[index].AttachmentRefs, progressObservation.AttachmentRefs...)
			continue
		}
		progressObservation.RepeatCount = 1
		compactedObservations = append(compactedObservations, progressObservation)
	}
	return compactedObservations
}

func recentProgressObservations(observations []turnObservation) []ProgressObservation {
	return keepLatestProgressObservations(compactProgressObservations(observations))
}

func keepLatestProgressObservations(observations []ProgressObservation) []ProgressObservation {
	if len(observations) <= maxProgressObservations {
		return observations
	}
	return observations[len(observations)-maxProgressObservations:]
}

func omittedObservationCount(progress TurnProgress) int {
	count := len(progress.CompletedSteps) + len(progress.FailedOrBlockedSteps)
	if count <= maxProgressObservations*2 {
		return 0
	}
	return count - maxProgressObservations*2
}

func summarizeObservation(observation turnObservation) ProgressObservation {
	status := "success"
	if observation.Failed() {
		status = "error"
	}
	return ProgressObservation{
		ObservationID:      observation.ObservationID,
		ToolName:           observation.Tool,
		Status:             status,
		Summary:            summarizeObservationContent(observation),
		AttachmentRefs:     progressAttachments(observation),
		ImageRefs:          append([]ToolResultImageRef{}, observation.ImageRefs...),
		AttemptFingerprint: strings.TrimSpace(observation.AttemptFingerprint),
		RecoveryStep:       strings.TrimSpace(observation.RecoveryStep),
	}
}

func toolResultContextItems(observations []turnObservation) []ToolResultContextItem {
	items := []ToolResultContextItem{}
	totalLength := 0
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		summary := strings.TrimSpace(observation.Summary)
		if summary == "" && len(observation.ImageRefs) == 0 {
			continue
		}
		remainingLength := toolResultContextLimit - totalLength
		if remainingLength <= 0 {
			break
		}
		if len(summary) > remainingLength {
			summary = summary[:remainingLength] + "\n[trimmed]"
		}
		totalLength += len(summary)
		status := "success"
		if observation.Failed() {
			status = "error"
		}
		items = append([]ToolResultContextItem{{
			ObservationID:  observation.ObservationID,
			ToolName:       observation.Tool,
			Status:         status,
			Summary:        summary,
			RecoveryPacket: observation.RecoveryPacket,
			ImageRefs:      append([]ToolResultImageRef{}, observation.ImageRefs...),
			Attachments:    progressAttachments(observation),
		}}, items...)
	}
	return items
}

func buildProgressFailureDebt(failureDebt FailureDebt, observations []turnObservation) *ProgressFailureDebt {
	budget := defaultRecoveryBudget()
	return &ProgressFailureDebt{
		ObservationID:           failureDebt.LatestFailure.ObservationID,
		ToolName:                strings.TrimSpace(failureDebt.LatestFailure.Tool),
		FailureStage:            failureDebt.LatestFailure.FailureStage(),
		ErrorCode:               failureDebt.LatestFailure.FailureCode(),
		AttemptFingerprint:      strings.TrimSpace(failureDebt.LatestFailure.AttemptFingerprint),
		RemainingRecoveryBudget: remainingRecoveryBudget(observations, budget),
		AllowedFinalResolutions: []string{failureResolutionNoToolFallback, failureResolutionFailureReport},
	}
}

func remainingRecoveryBudget(observations []turnObservation, budget RecoveryBudget) RecoveryBudget {
	budget = normalizeRecoveryBudget(budget)
	return RecoveryBudget{
		CorrectedRetry: maxInt(0, budget.CorrectedRetry-recoveryStepUseCount(observations, recoveryStepCorrectedRetry)),
		AlternateRoute: maxInt(0, budget.AlternateRoute-recoveryStepUseCount(observations, recoveryStepAlternateRoute)),
		AdjacentTool:   maxInt(0, budget.AdjacentTool-recoveryStepUseCount(observations, recoveryStepAdjacentTool)),
		NoToolFallback: budget.NoToolFallback,
	}
}

func maxInt(first int, second int) int {
	if first > second {
		return first
	}
	return second
}

func summarizeObservationContent(observation turnObservation) string {
	if strings.TrimSpace(observation.Summary) != "" {
		return truncateText(compactWhitespace(observation.Summary), maxSummaryTextLength)
	}
	if observation.Failed() {
		if summary := summarizeStructuredFailure(observation); summary != "" {
			return summary
		}
	}
	content := observation.ContentText()
	switch strings.TrimSpace(observation.Tool) {
	case "browser_snapshot", "browser.observe":
		return summarizeBrowserSnapshot(content)
	case "browser_screenshot":
		if len(observation.Attachments) > 0 {
			return "Screenshot captured with attachment evidence."
		}
		return summarizeSafeJSONFields(content, []string{"capturedAt", "contentType", "filename", "sizeBytes"})
	case "file_pick":
		if len(observation.Attachments) > 0 {
			return "User selected a file and it is available as attachment evidence."
		}
		return summarizeSafeJSONFields(content, []string{"filename", "sizeBytes", "contentType", "expiresAt"})
	case "file_read":
		return summarizeFileReadObservation(observation)
	case "browser_open":
		return summarizeSafeJSONFields(content, []string{"url", "title", "status", "ok"})
	case "browser_click", "browser_fill", "browser_select", "browser_press", "browser_wait":
		return summarizeSafeJSONFields(content, []string{"ok", "action", "target", "capturedAt"})
	case "site_serve":
		return summarizeSafeJSONFields(content, []string{"siteID", "slug", "mode", "previewURL", "publishedURL", "sourceSHA256"})
	case "memory_search", "conversation_history":
		return summarizeCollection(content)
	default:
		if observation.Failed() {
			return truncateText(compactWhitespace(redactUnsafeText(content)), 500)
		}
		return summarizeSafeJSONFields(content, []string{"ok", "status", "message", "error", "url", "title", "filename", "sizeBytes", "contentType"})
	}
}

func recentFileContexts(observations []turnObservation) []ProgressFileContext {
	byPath := map[string]ProgressFileContext{}
	order := []string{}
	for _, observation := range observations {
		context, isFileRead := progressFileContextFromObservation(observation)
		if !isFileRead {
			continue
		}
		existingContext, exists := byPath[context.Path]
		if !exists {
			order = append(order, context.Path)
			byPath[context.Path] = context
			continue
		}
		context.ReadRanges = appendUniqueStrings(append(existingContext.ReadRanges, context.ReadRanges...))
		if existingContext.Summary != "" && context.Summary == "" {
			context.Summary = existingContext.Summary
		}
		if existingContext.Snippet != "" && context.Snippet == "" {
			context.Snippet = existingContext.Snippet
		}
		if existingContext.MarkdownPreview != "" && context.MarkdownPreview == "" {
			context.MarkdownPreview = existingContext.MarkdownPreview
		}
		if existingContext.ConversionStatus != "" && context.ConversionStatus == "" {
			context.ConversionStatus = existingContext.ConversionStatus
		}
		if existingContext.ConversionMessage != "" && context.ConversionMessage == "" {
			context.ConversionMessage = existingContext.ConversionMessage
		}
		if existingContext.OriginalSizeBytes > context.OriginalSizeBytes {
			context.OriginalSizeBytes = existingContext.OriginalSizeBytes
		}
		context.RepeatedReadGuidance = repeatedReadGuidance(context.Path, context.ReadRanges)
		if context.RepeatedReadGuidance == "" {
			context.RepeatedReadGuidance = "Already read " + strings.TrimSpace(context.Path) + "; use this context, read a different range, or edit/build instead of rereading the same content."
		}
		byPath[context.Path] = context
	}
	if len(order) > 6 {
		order = order[len(order)-6:]
	}
	result := []ProgressFileContext{}
	for _, path := range order {
		result = append(result, byPath[path])
	}
	return result
}

func progressFileContextFromObservation(observation turnObservation) (ProgressFileContext, bool) {
	toolName := strings.TrimSpace(observation.Tool)
	if (toolName != "file_read" && toolName != "file_preview") || observation.Failed() {
		return ProgressFileContext{}, false
	}
	payload := map[string]any{}
	if json.Unmarshal([]byte(observation.ContentText()), &payload) != nil {
		return ProgressFileContext{}, false
	}
	path := stringField(payload, "path")
	content := stringField(payload, "content")
	if path == "" {
		return ProgressFileContext{}, false
	}
	if toolName == "file_preview" {
		return ProgressFileContext{
			Path:              path,
			LastObservationID: observation.ObservationID,
			SizeBytes:         intField(payload, "sizeBytes"),
			OriginalSizeBytes: intField(payload, "sizeBytes"),
			Summary:           summarizeFilePreviewContent(path, stringField(payload, "markdownPreview")),
			MarkdownPreview:   truncateText(compactWhitespace(stringField(payload, "markdownPreview")), 1000),
			ConversionStatus:  stringField(payload, "conversionStatus"),
			ConversionMessage: stringField(payload, "conversionMessage"),
		}, true
	}
	startLine := intField(payload, "startLine")
	endLine := intField(payload, "endLine")
	totalLines := intField(payload, "totalLines")
	sizeBytes := intField(payload, "sizeBytes")
	originalSizeBytes := intField(payload, "originalSizeBytes")
	if originalSizeBytes <= 0 {
		originalSizeBytes = sizeBytes
	}
	readRange := formatLineRange(startLine, endLine)
	return ProgressFileContext{
		Path:              path,
		LastObservationID: observation.ObservationID,
		ReadRanges:        appendUniqueStrings([]string{readRange}),
		TotalLines:        totalLines,
		TotalLinesKnown:   booleanField(payload, "totalLinesKnown"),
		SizeBytes:         sizeBytes,
		OriginalSizeBytes: originalSizeBytes,
		ReturnedBytes:     intField(payload, "returnedBytes"),
		IsTruncated:       booleanField(payload, "isTruncated"),
		Summary:           summarizeFileReadContent(path, content),
		Snippet:           truncateText(content, 1200),
	}, true
}

func summarizeFileReadObservation(observation turnObservation) string {
	context, isFileRead := progressFileContextFromObservation(observation)
	if !isFileRead {
		return summarizeSafeJSONFields(observation.ContentText(), []string{"path", "startLine", "endLine", "totalLines", "sizeBytes", "isTruncated"})
	}
	parts := []string{
		"path=" + context.Path,
		"range=" + strings.Join(context.ReadRanges, ","),
	}
	if context.TotalLines > 0 {
		parts = append(parts, fmt.Sprintf("totalLines=%d", context.TotalLines))
	}
	if context.SizeBytes > 0 {
		parts = append(parts, fmt.Sprintf("sizeBytes=%d", context.SizeBytes))
	}
	if context.Summary != "" {
		parts = append(parts, "summary="+context.Summary)
	}
	return strings.Join(parts, "; ")
}

func formatLineRange(startLine int, endLine int) string {
	if startLine <= 0 || endLine <= 0 {
		return "default"
	}
	if startLine == endLine {
		return fmt.Sprintf("%d", startLine)
	}
	return fmt.Sprintf("%d-%d", startLine, endLine)
}

func repeatedReadGuidance(path string, ranges []string) string {
	if len(ranges) < 2 {
		return ""
	}
	return "Already read " + strings.TrimSpace(path) + " ranges " + strings.Join(ranges, ", ") + "; use this context, read a different range, or edit/build instead of rereading the same content."
}

func summarizeFileReadContent(path string, content string) string {
	values := appendUniqueStrings(fileContentExportNames(content))
	values = appendUniqueStrings(values, markdownHeadingNames(content)...)
	if len(values) == 0 {
		values = append(values, truncateText(compactWhitespace(content), 240))
	}
	if len(values) > 12 {
		values = values[:12]
	}
	return strings.TrimSpace(filepathBase(path) + " symbols/headings: " + strings.Join(values, ", "))
}

func summarizeFilePreviewContent(path string, content string) string {
	if strings.TrimSpace(content) == "" {
		return filepathBase(path) + " preview: no markdown preview available"
	}
	return filepathBase(path) + " preview: " + truncateText(compactWhitespace(content), 240)
}

func fileContentExportNames(content string) []string {
	names := []string{}
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		for index, field := range fields {
			if field != "export" || index+2 >= len(fields) {
				continue
			}
			kind := fields[index+1]
			if kind != "const" && kind != "let" && kind != "var" && kind != "function" && kind != "type" && kind != "interface" && kind != "class" {
				continue
			}
			names = appendUniqueStrings(names, cleanSymbolName(fields[index+2]))
		}
	}
	return names
}

func markdownHeadingNames(content string) []string {
	names := []string{}
	for _, line := range strings.Split(content, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmedLine, "#") {
			continue
		}
		names = appendUniqueStrings(names, truncateText(compactWhitespace(strings.TrimLeft(trimmedLine, "# ")), 80))
	}
	return names
}

func cleanSymbolName(value string) string {
	return strings.Trim(strings.TrimSpace(value), "=:;,{(")
}

func filepathBase(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	index := strings.LastIndex(path, "/")
	if index < 0 {
		return path
	}
	return path[index+1:]
}

func intField(document map[string]any, key string) int {
	switch value := document[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}

func summarizeStructuredFailure(observation turnObservation) string {
	if terminalSummary := summarizeTerminalFailure(observation); terminalSummary != "" {
		return terminalSummary
	}
	parts := []string{}
	if observation.FailureCode() != "" {
		parts = append(parts, "errorCode="+observation.FailureCode())
	}
	if observation.FailureStage() != "" {
		parts = append(parts, "failureStage="+observation.FailureStage())
	}
	if observation.FailureSummary() != "" {
		parts = append(parts, "message="+truncateText(compactWhitespace(observation.FailureSummary()), 240))
	}
	if len(parts) == 0 {
		return ""
	}
	if observation.FailureSummary() == "" {
		parts = append(parts, "message="+truncateText(compactWhitespace(redactUnsafeText(observation.ContentText())), 240))
	}
	return strings.Join(parts, "; ")
}

// summarizeTerminalRun keeps a terminal_run result diagnosable instead of
// collapsing a long build log to a bare "success": it always surfaces the exit
// code and the tail of stdout and stderr, so warnings like a failed browser
// render are visible in the task record and to the model.
func summarizeTerminalRun(observation turnObservation) string {
	tail, ok := terminalObservationTail(observation)
	if !ok {
		return ""
	}
	parts := []string{}
	if tail.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exitCode=%d", *tail.ExitCode))
	}
	if tail.TimedOut {
		parts = append(parts, "timedOut=true")
	}
	if len(tail.StdoutTail) > 0 {
		parts = append(parts, "stdout:\n"+strings.Join(tail.StdoutTail, "\n"))
	}
	if len(tail.StderrTail) > 0 {
		parts = append(parts, "stderr:\n"+strings.Join(tail.StderrTail, "\n"))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

func summarizeTerminalFailure(observation turnObservation) string {
	if strings.TrimSpace(observation.Tool) != "terminal_run" {
		return ""
	}
	tail, ok := terminalObservationTail(observation)
	if !ok {
		return ""
	}
	parts := []string{}
	if observation.FailureCode() != "" {
		parts = append(parts, "errorCode="+observation.FailureCode())
	}
	if observation.FailureStage() != "" {
		parts = append(parts, "failureStage="+observation.FailureStage())
	}
	if tail.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exitCode=%d", *tail.ExitCode))
	}
	if len(tail.StderrTail) > 0 {
		parts = append(parts, "stderrTail="+truncateText(compactWhitespace(strings.Join(tail.StderrTail, " | ")), 240))
	} else if len(tail.StdoutTail) > 0 {
		parts = append(parts, "stdoutTail="+truncateText(compactWhitespace(strings.Join(tail.StdoutTail, " | ")), 240))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}

func summarizeBrowserSnapshot(content string) string {
	var document map[string]any
	if json.Unmarshal([]byte(content), &document) != nil {
		return "Browser snapshot captured. " + truncateText(compactWhitespace(redactUnsafeText(content)), 500)
	}
	parts := []string{}
	if value := stringField(document, "url"); value != "" {
		parts = append(parts, "url="+value)
	}
	if value := stringField(document, "title"); value != "" {
		parts = append(parts, "title="+value)
	}
	if value := stringField(document, "snapshotText"); value != "" {
		parts = append(parts, "visibleText="+truncateText(compactWhitespace(value), maxSummaryTextLength))
	}
	references := stringSliceField(document, "interactiveRefs")
	if len(references) > maxInteractiveReferences {
		references = references[:maxInteractiveReferences]
	}
	if len(references) > 0 {
		parts = append(parts, "interactiveRefs="+strings.Join(references, ", "))
	}
	if booleanField(document, "hasMore") {
		parts = append(parts, "hasMore=true")
	}
	if len(parts) == 0 {
		return "Browser snapshot captured."
	}
	return strings.Join(parts, "; ")
}

func summarizeCollection(content string) string {
	var value any
	if json.Unmarshal([]byte(content), &value) != nil {
		return truncateText(compactWhitespace(redactUnsafeText(content)), 500)
	}
	switch typedValue := value.(type) {
	case []any:
		excerpts := []string{}
		for _, item := range typedValue {
			excerpt := summarizeJSONValue(item, []string{"speaker", "text", "content", "title", "fact", "score"})
			if excerpt != "" {
				excerpts = append(excerpts, excerpt)
			}
			if len(excerpts) >= 5 {
				break
			}
		}
		return fmt.Sprintf("Returned %d item(s). Top excerpts: %s", len(typedValue), strings.Join(excerpts, " | "))
	default:
		return summarizeJSONValue(value, []string{"messages", "hasMoreBefore", "historyCursor", "facts"})
	}
}

func summarizeSafeJSONFields(content string, fieldNames []string) string {
	var value any
	if json.Unmarshal([]byte(content), &value) != nil {
		return truncateText(compactWhitespace(redactUnsafeText(content)), 500)
	}
	summary := summarizeJSONValue(value, fieldNames)
	if summary == "" {
		return "Tool completed successfully."
	}
	return summary
}

func summarizeJSONValue(value any, fieldNames []string) string {
	document, isDocument := value.(map[string]any)
	if !isDocument {
		return truncateText(compactWhitespace(fmt.Sprintf("%v", value)), 500)
	}
	parts := []string{}
	for _, fieldName := range fieldNames {
		fieldValue, isFound := document[fieldName]
		if !isFound {
			continue
		}
		if isUnsafePromptField(fieldName, fieldValue) {
			continue
		}
		parts = append(parts, fieldName+"="+truncateText(compactWhitespace(fmt.Sprintf("%v", fieldValue)), 300))
	}
	return strings.Join(parts, "; ")
}

func progressAttachments(observation turnObservation) []ProgressAttachment {
	attachments := []ProgressAttachment{}
	for index, attachment := range observation.Attachments {
		attachments = append(attachments, ProgressAttachment{
			ObservationID:    observation.ObservationID,
			AttachmentIndex:  index,
			Filename:         attachment.Filename,
			ContentType:      attachment.ContentType,
			SizeBytes:        attachment.SizeBytes,
			Title:            attachment.Title,
			HasDevicePayload: strings.TrimSpace(attachment.DevicePath) != "",
		})
	}
	return attachments
}

func progressObservationSignature(observation ProgressObservation) string {
	return strings.Join([]string{observation.ToolName, observation.Status, observation.Summary}, "\x00")
}

func stringField(document map[string]any, fieldName string) string {
	value, isString := document[fieldName].(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(value)
}

func booleanField(document map[string]any, fieldName string) bool {
	value, isBool := document[fieldName].(bool)
	return isBool && value
}

func stringSliceField(document map[string]any, fieldName string) []string {
	values, isSlice := document[fieldName].([]any)
	if !isSlice {
		return nil
	}
	result := []string{}
	for _, value := range values {
		text, isString := value.(string)
		if isString && strings.TrimSpace(text) != "" {
			result = append(result, strings.TrimSpace(text))
		}
	}
	return result
}

func compactWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncateText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func isUnsafePromptField(fieldName string, fieldValue any) bool {
	normalizedFieldName := strings.ToLower(fieldName)
	if isAgentWorkspacePathField(normalizedFieldName) && isAgentWorkspacePathValue(fieldValue) {
		return false
	}
	return strings.Contains(normalizedFieldName, "path") || strings.Contains(normalizedFieldName, "cookie") || strings.Contains(normalizedFieldName, "token") || strings.Contains(normalizedFieldName, "authorization") || strings.Contains(normalizedFieldName, "cdp") || strings.Contains(normalizedFieldName, "profile")
}

func isAgentWorkspacePathField(fieldName string) bool {
	return fieldName == "workspacepath" || fieldName == "sourceworkspacepath"
}

func isAgentWorkspacePathValue(value any) bool {
	text, isString := value.(string)
	if !isString {
		return false
	}
	trimmedText := strings.TrimSpace(text)
	return strings.HasPrefix(trimmedText, "/workspace/") ||
		strings.HasPrefix(trimmedText, "~/") ||
		strings.HasPrefix(trimmedText, "~")
}
