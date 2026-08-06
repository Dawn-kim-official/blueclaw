package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

type screenID int

const (
	screenTasks screenID = iota
	screenDetail
	screenApprovals
	screenHarness
)

var screenOrder = []screenID{screenTasks, screenDetail, screenApprovals, screenHarness}

const defaultPollInterval = 3 * time.Second

type Model struct {
	client            *Client
	runtimeConfigPath string
	pollInterval      time.Duration

	screen screenID
	width  int
	height int
	now    time.Time

	taskRuns      []TaskRun
	taskRunsError error
	tasksCursor   int

	detailTaskRunID string
	detailData      TaskRunDetail
	detailError     error
	detailLoading   bool

	approvalCursor       int
	approvalDetailByID   map[string]TaskRunDetail
	approvalErrorByID    map[string]error
	approvalStatusByID   map[string]string
	approvalSubmittingID string

	harnessInfo HarnessInfo
}

func NewModel(client *Client, runtimeConfigPath string) Model {
	return Model{
		client:             client,
		runtimeConfigPath:  runtimeConfigPath,
		pollInterval:       defaultPollInterval,
		now:                time.Now(),
		approvalDetailByID: map[string]TaskRunDetail{},
		approvalErrorByID:  map[string]error{},
		approvalStatusByID: map[string]string{},
		harnessInfo:        LoadHarnessInfo(runtimeConfigPath),
	}
}

func (model Model) Init() tea.Cmd {
	return tea.Batch(fetchTaskRunsCmd(model.client), model.refreshHarnessCmd(), tickCmd(model.pollInterval))
}

type taskRunsLoadedMsg struct {
	taskRuns []TaskRun
	err      error
}

type taskDetailLoadedMsg struct {
	taskRunID string
	detail    TaskRunDetail
	err       error
}

type approvalDetailLoadedMsg struct {
	taskRunID string
	detail    TaskRunDetail
	err       error
}

type approvalSubmittedMsg struct {
	taskRunID string
	result    ApprovalResult
	err       error
}

type tickMsg time.Time

func fetchTaskRunsCmd(client *Client) tea.Cmd {
	return func() tea.Msg {
		taskRuns, errorValue := client.ListTaskRuns(context.Background())
		return taskRunsLoadedMsg{taskRuns: taskRuns, err: errorValue}
	}
}

func fetchTaskDetailCmd(client *Client, taskRunID string) tea.Cmd {
	return func() tea.Msg {
		detail, errorValue := client.GetTaskRunDetail(context.Background(), taskRunID)
		return taskDetailLoadedMsg{taskRunID: taskRunID, detail: detail, err: errorValue}
	}
}

func fetchApprovalDetailCmd(client *Client, taskRunID string) tea.Cmd {
	return func() tea.Msg {
		detail, errorValue := client.GetTaskRunDetail(context.Background(), taskRunID)
		return approvalDetailLoadedMsg{taskRunID: taskRunID, detail: detail, err: errorValue}
	}
}

func submitApprovalCmd(client *Client, taskRunID string, decision string) tea.Cmd {
	return func() tea.Msg {
		result, errorValue := client.SubmitApproval(context.Background(), taskRunID, decision)
		return approvalSubmittedMsg{taskRunID: taskRunID, result: result, err: errorValue}
	}
}

func tickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(tickTime time.Time) tea.Msg {
		return tickMsg(tickTime)
	})
}

func (model Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typedMsg := msg.(type) {
	case tea.WindowSizeMsg:
		model.width = typedMsg.Width
		model.height = typedMsg.Height
		return model, nil

	case tea.KeyPressMsg:
		command := (&model).handleKeyPress(typedMsg)
		return model, command

	case harnessStatusMessage:
		model.harnessInfo = typedMsg.harnessInfo
		return model, nil

	case tickMsg:
		model.now = time.Time(typedMsg)
		return model, tea.Batch(fetchTaskRunsCmd(model.client), model.refreshVisibleScreenCmd(), tickCmd(model.pollInterval))

	case taskRunsLoadedMsg:
		model.taskRuns = typedMsg.taskRuns
		model.taskRunsError = typedMsg.err
		model.tasksCursor = clampInt(model.tasksCursor, 0, maximumInt(0, len(model.taskRuns)-1))
		model.approvalCursor = clampInt(model.approvalCursor, 0, maximumInt(0, len(model.waitingApprovalTaskRuns())-1))
		return model, nil

	case taskDetailLoadedMsg:
		if typedMsg.taskRunID != model.detailTaskRunID {
			return model, nil
		}
		model.detailLoading = false
		model.detailData = typedMsg.detail
		model.detailError = typedMsg.err
		return model, nil

	case approvalDetailLoadedMsg:
		if typedMsg.err != nil {
			model.approvalErrorByID[typedMsg.taskRunID] = typedMsg.err
			return model, nil
		}
		delete(model.approvalErrorByID, typedMsg.taskRunID)
		model.approvalDetailByID[typedMsg.taskRunID] = typedMsg.detail
		return model, nil

	case approvalSubmittedMsg:
		model.approvalSubmittingID = ""
		if typedMsg.err != nil {
			model.approvalStatusByID[typedMsg.taskRunID] = "approval failed: " + typedMsg.err.Error()
			return model, nil
		}
		model.approvalStatusByID[typedMsg.taskRunID] = "decision applied, new status: " + typedMsg.result.Status
		return model, fetchTaskRunsCmd(model.client)
	}

	return model, nil
}

func (model *Model) handleKeyPress(keyPressMsg tea.KeyPressMsg) tea.Cmd {
	switch keyPressMsg.String() {
	case "ctrl+c", "q":
		return tea.Quit
	case "1":
		model.screen = screenTasks
		return nil
	case "2":
		model.screen = screenDetail
		return model.openSelectedDetailCmd()
	case "3":
		model.screen = screenApprovals
		return model.refreshVisibleScreenCmd()
	case "4":
		model.screen = screenHarness
		return model.refreshHarnessCmd()
	case "tab":
		model.screen = nextScreen(model.screen)
		return model.refreshVisibleScreenCmd()
	case "shift+tab":
		model.screen = previousScreen(model.screen)
		return model.refreshVisibleScreenCmd()
	case "r":
		return tea.Batch(fetchTaskRunsCmd(model.client), model.refreshVisibleScreenCmd())
	case "up", "k":
		model.moveCursor(-1)
		return nil
	case "down", "j":
		model.moveCursor(1)
		return nil
	case "enter":
		if model.screen == screenTasks {
			model.screen = screenDetail
			return model.openSelectedDetailCmd()
		}
		return nil
	case "esc":
		if model.screen == screenDetail {
			model.screen = screenTasks
		}
		return nil
	}

	if model.screen == screenApprovals {
		if decision, isApprovalKey := ApprovalDecisionForKey(keyPressMsg.String()); isApprovalKey {
			return model.submitSelectedApproval(decision)
		}
	}

	return nil
}

func (model *Model) moveCursor(delta int) {
	switch model.screen {
	case screenTasks:
		model.tasksCursor = clampInt(model.tasksCursor+delta, 0, len(model.taskRuns)-1)
	case screenApprovals:
		model.approvalCursor = clampInt(model.approvalCursor+delta, 0, len(model.waitingApprovalTaskRuns())-1)
	}
}

func (model *Model) openSelectedDetailCmd() tea.Cmd {
	selectedTaskRun, hasSelection := model.selectedTaskRun()
	if !hasSelection {
		return nil
	}
	model.detailTaskRunID = selectedTaskRun.TaskRunID
	model.detailLoading = true
	model.detailError = nil
	return fetchTaskDetailCmd(model.client, selectedTaskRun.TaskRunID)
}

func (model Model) refreshVisibleScreenCmd() tea.Cmd {
	switch model.screen {
	case screenDetail:
		if model.detailTaskRunID == "" {
			return nil
		}
		return fetchTaskDetailCmd(model.client, model.detailTaskRunID)
	case screenApprovals:
		selectedApproval, hasSelection := model.selectedApprovalTaskRun()
		if !hasSelection {
			return nil
		}
		return fetchApprovalDetailCmd(model.client, selectedApproval.TaskRunID)
	default:
		return nil
	}
}

func (model *Model) submitSelectedApproval(decision string) tea.Cmd {
	selectedApproval, hasSelection := model.selectedApprovalTaskRun()
	if !hasSelection || model.approvalSubmittingID != "" {
		return nil
	}
	model.approvalSubmittingID = selectedApproval.TaskRunID
	delete(model.approvalStatusByID, selectedApproval.TaskRunID)
	return submitApprovalCmd(model.client, selectedApproval.TaskRunID, decision)
}

func (model Model) selectedTaskRun() (TaskRun, bool) {
	if model.tasksCursor < 0 || model.tasksCursor >= len(model.taskRuns) {
		return TaskRun{}, false
	}
	return model.taskRuns[model.tasksCursor], true
}

func (model Model) waitingApprovalTaskRuns() []TaskRun {
	return FilterWaitingApproval(model.taskRuns)
}

func (model Model) selectedApprovalTaskRun() (TaskRun, bool) {
	waitingTaskRuns := model.waitingApprovalTaskRuns()
	if model.approvalCursor < 0 || model.approvalCursor >= len(waitingTaskRuns) {
		return TaskRun{}, false
	}
	return waitingTaskRuns[model.approvalCursor], true
}

func nextScreen(current screenID) screenID {
	for screenIndex, candidateScreen := range screenOrder {
		if candidateScreen == current {
			return screenOrder[(screenIndex+1)%len(screenOrder)]
		}
	}
	return screenTasks
}

func previousScreen(current screenID) screenID {
	for screenIndex, candidateScreen := range screenOrder {
		if candidateScreen == current {
			return screenOrder[(screenIndex-1+len(screenOrder))%len(screenOrder)]
		}
	}
	return screenTasks
}

func clampInt(value int, minimumValue int, maximumValue int) int {
	if maximumValue < minimumValue {
		return minimumValue
	}
	if value < minimumValue {
		return minimumValue
	}
	if value > maximumValue {
		return maximumValue
	}
	return value
}

func maximumInt(leftValue int, rightValue int) int {
	if leftValue > rightValue {
		return leftValue
	}
	return rightValue
}

type harnessStatusMessage struct {
	harnessInfo HarnessInfo
}

func (model Model) refreshHarnessCmd() tea.Cmd {
	runtimeConfigPath := model.runtimeConfigPath
	client := model.client
	return func() tea.Msg {
		requestContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		harnessStatus, errorValue := client.GetHarnessStatus(requestContext)
		if errorValue == nil {
			return harnessStatusMessage{harnessInfo: HarnessInfoFromStatus(harnessStatus)}
		}
		return harnessStatusMessage{harnessInfo: LoadHarnessInfo(runtimeConfigPath)}
	}
}
