package connectors

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

type debugFailureSnapshot struct {
	TaskRun    task.TaskRun
	Phase      string
	Summary    string
	StopReason string
	Source     string
	Reason     string
	HasReport  bool
}

func exactDebugControlRequested(prompt string) bool {
	return strings.ToLower(strings.TrimSpace(prompt)) == "/debug"
}

func (connectorRuntime *ConnectorRuntime) handleDebugControlIfRequested(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	personID string,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (ConnectorRuntimeResult, bool) {
	if !exactDebugControlRequested(event.Prompt) {
		return ConnectorRuntimeResult{}, false
	}
	snapshot, isFound := connectorRuntime.latestFailureSnapshot(personID, event.ConversationID)
	reply := debugControlReply(snapshot, isFound, connectorRuntime.adminTaskLinkBaseURL, responseLanguageForEvent(event))
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply})
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".debug_control.reply_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "debug_control_reply_failed"}, true
	}
	return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "debug_control", ReplyDispatchID: dispatchID}, true
}

func (connectorRuntime *ConnectorRuntime) latestFailureSnapshot(personID string, conversationID string) (debugFailureSnapshot, bool) {
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range connectorRuntime.agentKernel.ListTaskRunByPersonID(personID) {
		if taskRun.Status != task.TaskStatusFailed && taskRun.Status != task.TaskStatusBlocked {
			continue
		}
		if taskRun.OriginConversationID != conversationID {
			continue
		}
		if isSelected && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		isSelected = true
	}
	if !isSelected {
		return debugFailureSnapshot{}, false
	}
	snapshot := debugFailureSnapshot{TaskRun: selectedTaskRun}
	taskEvents := connectorRuntime.agentKernel.ListTaskEvent(selectedTaskRun.TaskRunID)
	for index := len(taskEvents) - 1; index >= 0; index-- {
		if taskEvents[index].Name != "agent.failure_report" {
			continue
		}
		var reportEvent struct {
			Phase  string `json:"phase"`
			Report struct {
				SafeFailureSummary string `json:"safeFailureSummary"`
				StopReason         string `json:"stopReason"`
			} `json:"report"`
			Generation struct {
				Source string `json:"source"`
				Reason string `json:"reason"`
			} `json:"generation"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvents[index].Body), &reportEvent); errorValue != nil {
			continue
		}
		snapshot.Phase = reportEvent.Phase
		snapshot.Summary = reportEvent.Report.SafeFailureSummary
		snapshot.StopReason = reportEvent.Report.StopReason
		snapshot.Source = reportEvent.Generation.Source
		snapshot.Reason = reportEvent.Generation.Reason
		snapshot.HasReport = true
		break
	}
	return snapshot, true
}

func debugControlReply(snapshot debugFailureSnapshot, isFound bool, adminTaskLinkBaseURL string, responseLanguage string) string {
	if !isFound {
		if responseLanguage == "en" {
			return "There is no failed task in this conversation."
		}
		return "이 대화에는 실패한 작업이 없습니다."
	}
	lines := []string{}
	if responseLanguage == "en" {
		lines = append(lines, "Last failed task: `"+snapshot.TaskRun.TaskRunID+"` ("+string(snapshot.TaskRun.Status)+")")
	} else {
		lines = append(lines, "마지막 실패 작업: `"+snapshot.TaskRun.TaskRunID+"` ("+string(snapshot.TaskRun.Status)+")")
	}
	if snapshot.HasReport {
		lines = append(lines, "phase: "+snapshot.Phase)
		if summary := firstNonEmptyString(snapshot.Summary, snapshot.StopReason); summary != "" {
			lines = append(lines, "summary: "+summary)
		}
		noticeSource := snapshot.Source
		if snapshot.Reason != "" {
			noticeSource += " (" + snapshot.Reason + ")"
		}
		lines = append(lines, "notice: "+noticeSource)
	} else if failureReason := strings.TrimSpace(agent.RedactDiagnosticText(snapshot.TaskRun.FailureReason)); failureReason != "" {
		lines = append(lines, "reason: "+failureReason)
	}
	if adminTaskLinkBaseURL != "" {
		lines = append(lines, adminTaskLinkBaseURL+"/tasks/"+snapshot.TaskRun.TaskRunID)
	}
	return strings.Join(lines, "\n")
}
