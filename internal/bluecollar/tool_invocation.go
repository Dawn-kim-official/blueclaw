package bluecollar

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"strings"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/model"
)

func (agentTurnRunner *AgentTurnRunner) recordToolObservation(taskRunID string, state *agentTaskState, actionDocument turnActionDocument, successfulToolCalls map[string]turnObservation, observation turnObservation, recoveryStep string) {
	if recoveryStep != "" {
		observation.RecoveryStep = recoveryStep
		observation.RecoveryAttemptSpent = recoveryStep != recoveryStepInspection
		observation.RecoveryAttemptKey = canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)
	}
	state.Observations = append(state.Observations, observation)
	state.Attachments = appendObservationAttachments(state.Attachments, observation)
	if observation.Failed() {
		agentTurnRunner.appendEvent(taskRunID, "agent.failure_debt_created", marshalEventBody(activeFailureDebtEventBody(state.Observations, agentTurnRunner.options.RecoveryBudget)))
		if recoveryAttemptCount(state.Observations) < agentTurnRunner.options.RecoveryAttemptLimit {
			originalInstruction := firstNonEmptyString(state.Request.ActiveGoal.OriginalInstruction, state.Request.Prompt)
			recoveryObservation := recoveryGuidanceObservation(nextObservationIndex(state.Observations), observation, originalInstruction)
			state.Observations = append(state.Observations, recoveryObservation)
			agentTurnRunner.appendEvent(taskRunID, "agent.recovery_guidance", marshalEventBody(recoveryObservation))
		}
		return
	}
	successfulToolCalls[canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)] = observation
}

func (agentTurnRunner *AgentTurnRunner) invokeTool(ctx context.Context, toolRegistry *toolcontract.ToolSet, taskRunID string, observationID string, toolName string, toolInput json.RawMessage, workspaceRootPath string, minimumModifiedAt time.Time, responseLanguage string, userFacingMessage string) turnObservation {
	trimmedToolName := strings.TrimSpace(toolName)
	toolInputKey := canonicalToolCallKey(trimmedToolName, toolInput)
	if toolRegistry == nil {
		observation := toolFailureObservation(observationID, trimmedToolName, "tool registry was not configured")
		observation.ToolInput = append(json.RawMessage{}, toolInput...)
		observation.ToolInputKey = toolInputKey
		observation.AttemptFingerprint = attemptFingerprint(toolInputKey, observation.FailureCode())
		return observation
	}
	agentTurnRunner.appendEvent(taskRunID, "tool."+trimmedToolName+".requested", marshalEventBody(map[string]any{
		"observationID": observationID,
		"toolName":      trimmedToolName,
		"input":         json.RawMessage(toolInput),
	}))
	unregisterTool := agentTurnRunner.taskRunService.RegisterTaskRunTool(taskRunID, observationID, trimmedToolName)
	defer unregisterTool()
	toolContext := WithUserFacingMessage(WithObservationID(WithResponseLanguage(WithTaskRunID(ctx, taskRunID), responseLanguage), observationID), userFacingMessage)
	invocationStartedAt := time.Now()
	toolDefinition, _ := toolRegistry.ToolDefinition(trimmedToolName)
	toolResult, errorValue := toolRegistry.Invoke(toolContext, toolcontract.ToolInvocation{ToolName: trimmedToolName, Input: toolInput})
	if errorValue != nil {
		toolResult = toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, trimmedToolName, errorValue.Error())
	}
	observation := agentTurnRunner.saveToolObservation(ctx, taskRunID, observationID, trimmedToolName, toolDefinition.ID, toolInput, effectiveObservationToolName(trimmedToolName, toolInput), toolInputKey, toolResult, workspaceRootPath, minimumModifiedAt, time.Since(invocationStartedAt).Milliseconds())
	return observation
}

func effectiveObservationToolName(toolName string, toolInput json.RawMessage) string {
	return strings.TrimSpace(toolName)
}

func toolFailureObservation(observationID string, toolName string, message string) turnObservation {
	return newFailureObservation(observationID, "continue", toolName, message, toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, firstNonEmptyString(toolName, "tool"))
}

func (agentTurnRunner *AgentTurnRunner) saveToolObservation(ctx context.Context, taskRunID string, observationID string, toolName string, toolID string, toolInput json.RawMessage, observationToolName string, toolInputKey string, toolResult toolcontract.ToolResult, workspaceRootPath string, minimumModifiedAt time.Time, durationMS int64) turnObservation {
	toolResult = normalizeToolFailureResult(toolName, toolResult)
	content := toolResult.ContentText()
	originalContent := content
	isError := toolResult.Failed()
	artifactID := ""
	if len(content) > agentTurnRunner.options.ToolResultMaxBytes {
		taskArtifact := agentTurnRunner.taskArtifactService.AddTaskArtifactBody(taskRunID, "tool."+toolName+".result", content)
		artifactID = taskArtifact.TaskArtifactID
		content = content[:agentTurnRunner.options.ToolResultMaxBytes] + "\n[truncated; full result saved as artifact " + taskArtifact.TaskArtifactID + "]"
	}
	attachments := []toolcontract.FileAttachment{}
	if !isError {
		attachments = append(attachments, toolResult.Attachments...)
	}
	if !isError && toolcontract.IsArtifactDeliveryTool(toolName) && len(attachments) > 0 {
		validityState := buildAttachmentValidityState(workspaceRootPath, attachments)
		if !validityState.Passed {
			content = validityFailureMessage(validityState)
			originalContent = content
			isError = true
			toolResult.Failure = &toolcontract.ToolFailure{Kind: toolcontract.FailureInvalidInput, Code: toolcontract.FailureCodes.InvalidInput.String(), Stage: "artifact_validation", UserSafeSummary: content}
			attachments = nil
			agentTurnRunner.appendEvent(taskRunID, "agent.artifact_attach_rejected", marshalEventBody(validityState))
		}
	}
	observation := turnObservation{
		ObservationID:   observationID,
		Action:          "continue",
		Tool:            firstNonEmptyString(observationToolName, toolName),
		ToolID:          strings.TrimSpace(toolID),
		ToolInput:       append(json.RawMessage{}, toolInput...),
		Output:          toolResult.Output,
		Effects:         append([]toolcontract.ResourceEffect{}, toolResult.Effects...),
		Failure:         toolResult.Failure,
		Attachments:     attachments,
		RecoveryActions: append([]toolcontract.RecoveryAction{}, toolResult.RecoveryActions...),
	}
	observation.Output.Content = content
	observation.ImageRefs = toolResultImageRefs(observationID, attachments)
	observation.Summary = agentTurnRunner.buildToolResultSummary(ctx, taskRunID, toolName, originalContent, isError, attachments, artifactID, toolResult)
	observation.ToolInputKey = toolInputKey
	observation.DurationMS = durationMS
	if observation.Failed() {
		observation.AttemptFingerprint = attemptFingerprint(toolInputKey, observation.FailureCode())
	}
	agentTurnRunner.appendEvent(taskRunID, "tool."+toolName+".result", marshalEventBody(observation))
	return observation
}

func normalizeToolFailureResult(toolName string, toolResult toolcontract.ToolResult) toolcontract.ToolResult {
	if !toolResult.Failed() {
		return toolResult
	}
	if toolResult.Failure.Kind == "" {
		toolResult.Failure.Kind = toolcontract.FailureUnknown
	}
	if strings.TrimSpace(toolResult.Failure.Code) == "" {
		toolResult.Failure.Code = toolcontract.FailureCodes.OperationFailed.String()
	} else {
		toolResult.Failure.Code = toolcontract.CanonicalFailureCode(toolcontract.FailureCode(toolResult.Failure.Code))
	}
	if strings.TrimSpace(toolResult.Failure.Stage) == "" {
		toolResult.Failure.Stage = firstNonEmptyString(toolName, "tool")
	}
	if strings.TrimSpace(toolResult.Failure.UserSafeSummary) == "" {
		toolResult.Failure.UserSafeSummary = strings.TrimSpace(toolResult.ContentText())
	}
	if strings.TrimSpace(toolResult.Output.Content) == "" {
		toolResult.Output.Content = toolResult.Failure.UserSafeSummary
	}
	return toolResult
}

func recoveryActionsFromObservations(observations []turnObservation) []toolcontract.RecoveryAction {
	recoveryActions := []toolcontract.RecoveryAction{}
	seen := map[string]bool{}
	for _, observation := range observations {
		for _, recoveryAction := range observation.RecoveryActions {
			key := recoveryAction.Kind + "\x00" + recoveryAction.Delivery + "\x00" + recoveryAction.DownloadURL + "\x00" + recoveryAction.ConnectCommand
			if strings.TrimSpace(recoveryAction.Kind) == "" || seen[key] {
				continue
			}
			seen[key] = true
			recoveryActions = append(recoveryActions, recoveryAction)
		}
	}
	return recoveryActions
}

func (agentTurnRunner *AgentTurnRunner) buildToolResultSummary(ctx context.Context, taskRunID string, toolName string, content string, isError bool, attachments []toolcontract.FileAttachment, artifactID string, toolResult toolcontract.ToolResult) string {
	observation := turnObservation{
		Tool:        toolName,
		Output:      toolcontract.ToolOutput{Content: content, Data: append(json.RawMessage{}, toolResult.Output.Data...)},
		Attachments: attachments,
	}
	if isError {
		observation.Failure = toolResult.Failure
	}
	summary := modelVisibleToolResultSummary(ctx, agentTurnRunner.languageModel, toolName, observation)
	if strings.TrimSpace(artifactID) != "" {
		summary = strings.TrimSpace(summary) + " Full result stored as artifact " + strings.TrimSpace(artifactID) + "."
	}
	return strings.TrimSpace(summary)
}

const rawToolResultInlineLimit = 2000

const semanticToolSummaryTarget = 1200

func modelVisibleToolResultSummary(ctx context.Context, languageModel model.LanguageModelProvider, toolName string, observation turnObservation) string {
	content := strings.TrimSpace(observation.ContentText())
	if content == "" {
		return summarizeObservationContent(observation)
	}
	if strings.TrimSpace(toolName) == "terminal.run" {
		if summary := summarizeTerminalRun(observation); summary != "" {
			return summary
		}
	}
	if shouldUseSanitizedToolPresenter(toolName) {
		return sanitizedToolResultSummary(observation)
	}
	if len(content) <= rawToolResultInlineLimit {
		return content
	}
	if shouldSummarizeLongToolResult(toolName) && languageModel != nil {
		summary, errorValue := summarizeLongToolResult(ctx, languageModel, toolName, content)
		if errorValue == nil && strings.TrimSpace(summary) != "" {
			return strings.TrimSpace(summary)
		}
	}
	return deterministicLongToolResultSummary(content)
}

func shouldUseSanitizedToolPresenter(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "browser.snapshot", "browser.observe", "browser.screenshot", "file.pick", toolcontract.FileDeliverToolName, "file.read", "site.serve", "site.list", "terminal.run":
		return true
	default:
		return false
	}
}

func shouldSummarizeLongToolResult(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "web.fetch", "web.search", "memory.search", "conversation.history":
		return true
	default:
		return false
	}
}

func sanitizedToolResultSummary(observation turnObservation) string {
	switch strings.TrimSpace(observation.Tool) {
	case "browser.snapshot", "browser.observe":
		return summarizeBrowserSnapshot(observation.ContentText())
	case "browser.screenshot":
		if len(observation.Attachments) > 0 {
			return "Screenshot captured. Use the imageRefs for visual inspection."
		}
		return summarizeSafeJSONFields(observation.ContentText(), []string{"capturedAt", "contentType", "filename", "sizeBytes"})
	case "file.pick":
		return attachmentResultSummary("User selected file", observation.Attachments)
	case toolcontract.FileDeliverToolName:
		return attachmentResultSummary("File attached", observation.Attachments)
	case "file.read":
		return summarizeFileReadObservation(observation)
	case "site.serve":
		return summarizeSafeJSONFields(observation.ContentText(), []string{"siteID", "slug", "mode", "previewURL", "publishedURL", "sourceSHA256"})
	case "site.list":
		return summarizeSafeJSONFields(observation.ContentText(), []string{"sites", "siteID", "slug", "title", "status", "publishedURL", "updatedAt"})
	case "terminal.run":
		if summary := summarizeTerminalFailure(observation); summary != "" {
			return summary
		}
		return summarizeSafeJSONFields(observation.ContentText(), []string{"exitCode", "timedOut"})
	default:
		return summarizeObservationContent(observation)
	}
}

func attachmentResultSummary(prefix string, attachments []toolcontract.FileAttachment) string {
	if len(attachments) == 0 {
		return prefix + "."
	}
	parts := []string{prefix + "."}
	for index, attachment := range attachments {
		values := []string{
			fmt.Sprintf("attachmentIndex=%d", index),
			"filename=" + strings.TrimSpace(attachment.Filename),
			"contentType=" + strings.TrimSpace(attachment.ContentType),
			fmt.Sprintf("sizeBytes=%d", attachment.SizeBytes),
		}
		parts = append(parts, strings.Join(nonEmptyStrings(values), "; "))
	}
	return strings.Join(parts, "\n")
}

func summarizeLongToolResult(ctx context.Context, languageModel model.LanguageModelProvider, toolName string, content string) (string, error) {
	prompt := strings.Join([]string{
		"Summarize this tool result for the next agent action.",
		"Preserve concrete facts needed for the next action.",
		"Preserve URLs, titles, IDs, errors, file names, stdout/stderr facts.",
		"Do not add facts not present in the tool output.",
		"Do not include secrets, cookies, local private paths, CDP URLs, profile paths, or hidden policy.",
		fmt.Sprintf("Target length: about %d characters.", semanticToolSummaryTarget),
		"Tool: " + strings.TrimSpace(toolName),
		"Tool output:\n" + content,
	}, "\n")
	return languageModel.GenerateResponse(ctx, prompt)
}

func deterministicLongToolResultSummary(content string) string {
	content = strings.TrimSpace(content)
	if len(content) <= rawToolResultInlineLimit {
		return content
	}
	headLimit := rawToolResultInlineLimit / 2
	tailLimit := rawToolResultInlineLimit / 2
	return content[:headLimit] + "\n[truncated]\n" + content[len(content)-tailLimit:]
}

func toolResultImageRefs(observationID string, attachments []toolcontract.FileAttachment) []ToolResultImageRef {
	imageRefs := []ToolResultImageRef{}
	for index, attachment := range attachments {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.ContentType)), "image/") {
			continue
		}
		imageRefs = append(imageRefs, ToolResultImageRef{
			ObservationID:   observationID,
			AttachmentIndex: index,
			MimeType:        strings.TrimSpace(attachment.ContentType),
			Filename:        strings.TrimSpace(attachment.Filename),
		})
	}
	return imageRefs
}
