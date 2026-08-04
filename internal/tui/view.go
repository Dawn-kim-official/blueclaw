package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
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
	return styleHeaderBar.Width(maximumInt(model.width, 0)).Render(model.headerTitle()) + "\n" + strings.Join(renderedTabs, " ")
}

func (model Model) headerTitle() string {
	host := hostOfURL(model.client.BaseURL())
	if host == "" {
		return "blueclaw"
	}
	return "blueclaw · " + host
}

func hostOfURL(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	for _, scheme := range []string{"https://", "http://"} {
		trimmed = strings.TrimPrefix(trimmed, scheme)
	}
	if slashIndex := strings.Index(trimmed, "/"); slashIndex >= 0 {
		trimmed = trimmed[:slashIndex]
	}
	return trimmed
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
		return renderEmptyState("No task runs yet", "Task runs appear here as they arrive. Press r to refresh.")
	}
	rows := make([][]string, 0, len(model.taskRuns))
	for _, taskRun := range model.taskRuns {
		rows = append(rows, []string{
			truncateText(taskRun.TaskRunID, 12),
			string(taskRun.Status),
			truncateText(firstNonEmpty(taskRun.RequesterDisplayName, taskRun.RequesterPersonID, "-"), 16),
			formatAge(taskRun.CreatedAt, model.now),
			truncateText(taskRun.Prompt, 60),
		})
	}
	return strings.Join([]string{
		styleSectionTitle.Render(fmt.Sprintf("Task runs (%d)", len(model.taskRuns))),
		renderTaskTable([]string{"ID", "STATUS", "REQUESTER", "AGE", "PROMPT"}, rows, model.tasksCursor, columnTaskStatus),
	}, "\n")
}

const (
	columnTaskStatus   = 1
	columnNoneIsStatus = -1
)

func renderTaskTable(headers []string, rows [][]string, selectedRow int, statusColumn int) string {
	ageColumn := len(headers) - 2
	promptColumn := len(headers) - 1
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(colorBrandDeep)).
		BorderColumn(false).
		BorderRow(false).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row int, column int) lipgloss.Style {
			if row == table.HeaderRow {
				return styleTableCell.Foreground(colorBrandSoft).Bold(true)
			}
			cell := styleTableCell
			if column == ageColumn {
				cell = cell.AlignHorizontal(lipgloss.Right)
			}
			if row == selectedRow {
				return cell.Background(colorBrand).Foreground(colorInk).Bold(true)
			}
			if column == statusColumn {
				return cell.Foreground(statusColor(rows[row][statusColumn]))
			}
			if column == promptColumn {
				return cell.Foreground(colorMuted)
			}
			return cell
		}).
		Render()
}

func renderEmptyState(title string, detail string) string {
	return strings.Join([]string{
		"",
		styleSectionTitle.Render(title),
		styleMuted.Render(detail),
	}, "\n")
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
		return renderEmptyState("Nothing is waiting for approval", "Task runs that need a human decision appear here.")
	}
	rows := make([][]string, 0, len(waitingTaskRuns))
	for _, taskRun := range waitingTaskRuns {
		rows = append(rows, []string{
			truncateText(taskRun.TaskRunID, 12),
			truncateText(firstNonEmpty(taskRun.RequesterDisplayName, taskRun.RequesterPersonID, "-"), 16),
			formatAge(taskRun.CreatedAt, model.now),
			truncateText(taskRun.Prompt, 60),
		})
	}
	lines := []string{
		styleSectionTitle.Render(fmt.Sprintf("Waiting for approval (%d)", len(waitingTaskRuns))),
		renderTaskTable([]string{"ID", "REQUESTER", "AGE", "PROMPT"}, rows, model.approvalCursor, columnNoneIsStatus),
	}

	selectedApproval, hasSelection := model.selectedApprovalTaskRun()
	if !hasSelection {
		return strings.Join(lines, "\n")
	}
	panelLines := []string{styleSectionTitle.Render("Question")}
	if approvalError, hasError := model.approvalErrorByID[selectedApproval.TaskRunID]; hasError {
		panelLines = append(panelLines, styleError.Render(approvalError.Error()))
	} else if approvalDetail, hasDetail := model.approvalDetailByID[selectedApproval.TaskRunID]; hasDetail {
		if question, hasQuestion := LatestApprovalQuestion(approvalDetail.TaskEvents); hasQuestion {
			panelLines = append(panelLines, question)
		} else {
			panelLines = append(panelLines, styleMuted.Render("no approval.pending_call event found in this run's ledger"))
		}
	} else {
		panelLines = append(panelLines, styleMuted.Render("loading…"))
	}
	if statusMessage, hasStatus := model.approvalStatusByID[selectedApproval.TaskRunID]; hasStatus {
		panelLines = append(panelLines, statusMessage)
	}
	if model.approvalSubmittingID == selectedApproval.TaskRunID {
		panelLines = append(panelLines, styleMuted.Render("submitting…"))
	}
	return strings.Join(append(lines, "", stylePanel.Render(strings.Join(panelLines, "\n"))), "\n")
}

func (model Model) renderHarnessScreen() string {
	lines := []string{styleSectionTitle.Render("Harness")}
	if !model.harnessInfo.IsKnown {
		lines = append(lines, styleWarning.Render("harness is unknown: "+model.harnessInfo.UnknownReason))
		lines = append(lines, styleMuted.Render("the admin API does not report which harness a running daemon uses; pass --runtime pointing at the daemon's runtime configuration JSON"))
		return strings.Join(lines, "\n")
	}
	fields := [][]string{{"name", model.harnessInfo.Name}}
	if model.harnessInfo.AgentCommandPath != "" {
		fields = append(fields, []string{"agent command", model.harnessInfo.AgentCommandPath})
	}
	fields = append(fields, []string{"runs as", harnessIdentityDescription(model.harnessInfo)})
	if model.harnessInfo.ToolCatalogURL != "" {
		fields = append(fields, []string{"tool catalog", model.harnessInfo.ToolCatalogURL})
	}
	lines = append(lines, renderFieldTable(fields))
	lines = append(lines, "", styleMuted.Render(harnessProvenanceDescription(model.harnessInfo)))
	return strings.Join(lines, "\n")
}

func renderFieldTable(fields [][]string) string {
	return table.New().
		Border(lipgloss.HiddenBorder()).
		BorderColumn(false).
		BorderRow(false).
		Rows(fields...).
		StyleFunc(func(row int, column int) lipgloss.Style {
			if column == 0 {
				return styleTableCell.Foreground(colorMuted)
			}
			return styleTableCell
		}).
		Render()
}

func harnessIdentityDescription(harnessInfo HarnessInfo) string {
	if harnessInfo.RunsAsRequesterIdentity {
		return styleOK.Render("the requester's own POSIX user")
	}
	return styleWarning.Render("the daemon account — tool calls are not isolated per person")
}

func harnessProvenanceDescription(harnessInfo HarnessInfo) string {
	if harnessInfo.IsLiveReport {
		return "reported by the running daemon"
	}
	return "read from " + harnessInfo.RuntimeConfigPath + "; the daemon was not reachable, so this is the configured harness rather than the running one"
}
