package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

func (model Model) View() tea.View {
	sections := []string{
		model.renderTabBar(),
		model.renderScreenBody(),
		model.renderFooter(),
	}
	view := tea.NewView(strings.Join(sections, "\n"))
	view.AltScreen = true
	return view
}

func (model Model) renderTabBar() string {
	tabLabels := []struct {
		screen screenID
		label  string
	}{
		{screenTasks, "1 Tasks"},
		{screenDetail, "2 Detail"},
		{screenApprovals, "3 Approvals"},
		{screenHarness, "4 Harness"},
	}
	renderedTabs := make([]string, 0, len(tabLabels))
	for _, tabLabel := range tabLabels {
		label := tabLabel.label
		if tabLabel.screen == screenApprovals {
			if waitingCount := len(model.waitingApprovalTaskRuns()); waitingCount > 0 {
				label = fmt.Sprintf("%s (%d)", label, waitingCount)
			}
		}
		if tabLabel.screen == model.screen {
			renderedTabs = append(renderedTabs, styleTabActive.Render(label))
		} else {
			renderedTabs = append(renderedTabs, styleTabIdle.Render(label))
		}
	}
	return styleHeaderBar.Width(maximumInt(model.width, 0)).Render("blueclaw-tui") + "\n" + strings.Join(renderedTabs, " ")
}

func (model Model) renderScreenBody() string {
	switch model.screen {
	case screenTasks:
		return model.renderTasksScreen()
	case screenDetail:
		return model.renderDetailScreen()
	case screenApprovals:
		return model.renderApprovalsScreen()
	case screenHarness:
		return model.renderHarnessScreen()
	default:
		return ""
	}
}

func (model Model) renderFooter() string {
	connectionNotice := ""
	if model.taskRunsError != nil {
		connectionNotice = styleError.Render("connection: " + model.taskRunsError.Error())
	}
	helpText := styleFooter.Render("1-4 screens · tab/shift+tab cycle · up/down select · enter open · r refresh · q quit")
	if model.screen == screenApprovals {
		helpText = styleFooter.Render("1-4 screens · up/down select · y confirm · a confirm task · n cancel · r refresh · q quit")
	}
	if connectionNotice != "" {
		return connectionNotice + "\n" + helpText
	}
	return helpText
}

func (model Model) renderTasksScreen() string {
	if len(model.taskRuns) == 0 && model.taskRunsError == nil {
		return styleMuted.Render("no task runs yet")
	}
	lines := []string{styleSectionTitle.Render(fmt.Sprintf("Task runs (%d)", len(model.taskRuns)))}
	for taskIndex, taskRun := range model.taskRuns {
		lines = append(lines, model.renderTaskRunRow(taskRun, taskIndex == model.tasksCursor))
	}
	return strings.Join(lines, "\n")
}

func (model Model) renderTaskRunRow(taskRun TaskRun, isSelected bool) string {
	requester := firstNonEmpty(taskRun.RequesterDisplayName, taskRun.RequesterPersonID, "-")
	row := fmt.Sprintf("%-8s  %s  %-16s  %-5s  %s",
		truncateText(taskRun.TaskRunID, 8),
		statusStyle(taskRun.Status).Render(padRight(string(taskRun.Status), 12)),
		truncateText(requester, 16),
		formatAge(taskRun.CreatedAt, model.now),
		truncateText(taskRun.Prompt, 60),
	)
	if isSelected {
		return styleSelected.Render("> " + row)
	}
	return "  " + row
}

func (model Model) renderDetailScreen() string {
	if model.detailTaskRunID == "" {
		return styleMuted.Render("select a task run from the Tasks screen (enter) to see its timeline")
	}
	lines := []string{styleSectionTitle.Render("Task " + model.detailTaskRunID)}
	if model.detailLoading {
		lines = append(lines, styleMuted.Render("loading…"))
	}
	if model.detailError != nil {
		lines = append(lines, styleError.Render(model.detailError.Error()))
		return strings.Join(lines, "\n")
	}
	taskRun := model.detailData.TaskRun
	if taskRun.TaskRunID != "" {
		lines = append(lines, fmt.Sprintf("status: %s   requester: %s", statusStyle(taskRun.Status).Render(string(taskRun.Status)), firstNonEmpty(taskRun.RequesterDisplayName, taskRun.RequesterPersonID)))
		lines = append(lines, "prompt: "+truncateText(taskRun.Prompt, 100))
		lines = append(lines, "")
	}
	timelineEntries := BuildTimeline(model.detailData.TaskEvents)
	if len(timelineEntries) == 0 {
		lines = append(lines, styleMuted.Render("no events yet"))
	}
	for _, timelineEntry := range timelineEntries {
		lines = append(lines, renderTimelineEntry(timelineEntry))
	}
	return strings.Join(lines, "\n")
}

func renderTimelineEntry(entry TimelineEntry) string {
	timestamp := entry.Time.Format("15:04:05")
	switch entry.Kind {
	case TimelineEntryToolCall:
		resultLabel := styleMuted.Render("(pending)")
		if entry.HasResult {
			if entry.ResultIsFailure {
				resultLabel = styleFailure.Render("failed: " + truncateText(entry.ResultSummary, 80))
			} else {
				resultLabel = styleSuccess.Render("ok: " + truncateText(entry.ResultSummary, 80))
			}
		}
		return fmt.Sprintf("%s  tool %s  %s", timestamp, entry.ToolName, resultLabel)
	case TimelineEntryAgentMessage:
		return fmt.Sprintf("%s  agent: %s", timestamp, truncateText(entry.Message, 100))
	case TimelineEntryApprovalPending:
		return fmt.Sprintf("%s  %s", timestamp, styleWarning.Render("approval requested: "+truncateText(entry.Message, 100)))
	case TimelineEntryApprovalExecuted:
		return fmt.Sprintf("%s  %s", timestamp, styleSuccess.Render("approval resolved for "+entry.ToolName))
	default:
		return fmt.Sprintf("%s  %s", timestamp, styleMuted.Render(entry.RawEventName))
	}
}

func (model Model) renderApprovalsScreen() string {
	waitingTaskRuns := model.waitingApprovalTaskRuns()
	if len(waitingTaskRuns) == 0 {
		return styleMuted.Render("no task runs are waiting for approval")
	}
	lines := []string{styleSectionTitle.Render(fmt.Sprintf("Waiting for approval (%d)", len(waitingTaskRuns)))}
	for taskIndex, taskRun := range waitingTaskRuns {
		lines = append(lines, model.renderApprovalRow(taskRun, taskIndex == model.approvalCursor))
	}

	selectedApproval, hasSelection := model.selectedApprovalTaskRun()
	if !hasSelection {
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "")
	lines = append(lines, styleSectionTitle.Render("Question"))
	if approvalError, hasError := model.approvalErrorByID[selectedApproval.TaskRunID]; hasError {
		lines = append(lines, styleError.Render(approvalError.Error()))
	} else if approvalDetail, hasDetail := model.approvalDetailByID[selectedApproval.TaskRunID]; hasDetail {
		if question, hasQuestion := LatestApprovalQuestion(approvalDetail.TaskEvents); hasQuestion {
			lines = append(lines, question)
		} else {
			lines = append(lines, styleMuted.Render("no approval.pending_call event found in this run's ledger"))
		}
	} else {
		lines = append(lines, styleMuted.Render("loading…"))
	}
	if statusMessage, hasStatus := model.approvalStatusByID[selectedApproval.TaskRunID]; hasStatus {
		lines = append(lines, statusMessage)
	}
	if model.approvalSubmittingID == selectedApproval.TaskRunID {
		lines = append(lines, styleMuted.Render("submitting…"))
	}
	return strings.Join(lines, "\n")
}

func (model Model) renderApprovalRow(taskRun TaskRun, isSelected bool) string {
	requester := firstNonEmpty(taskRun.RequesterDisplayName, taskRun.RequesterPersonID, "-")
	row := fmt.Sprintf("%-8s  %-16s  %-5s  %s",
		truncateText(taskRun.TaskRunID, 8),
		truncateText(requester, 16),
		formatAge(taskRun.CreatedAt, model.now),
		truncateText(taskRun.Prompt, 60),
	)
	if isSelected {
		return styleSelected.Render("> " + row)
	}
	return "  " + row
}

func (model Model) renderHarnessScreen() string {
	lines := []string{styleSectionTitle.Render("Harness")}
	if !model.harnessInfo.IsKnown {
		lines = append(lines, styleWarning.Render("harness is unknown: "+model.harnessInfo.UnknownReason))
		lines = append(lines, styleMuted.Render("the admin API does not report which harness a running sandbox uses; pass --runtime pointing at the sandbox's runtime configuration JSON"))
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "name: "+model.harnessInfo.Name)
	if model.harnessInfo.AgentCommandPath != "" {
		lines = append(lines, "agentCommandPath: "+model.harnessInfo.AgentCommandPath)
	}
	lines = append(lines, styleMuted.Render("source: "+model.harnessInfo.RuntimeConfigPath))
	lines = append(lines, styleMuted.Render("this is the configured harness, not a live report from a running sandbox process"))
	return strings.Join(lines, "\n")
}

func padRight(text string, width int) string {
	textWidth := lipgloss.Width(text)
	if textWidth >= width {
		return text
	}
	return text + strings.Repeat(" ", width-textWidth)
}
