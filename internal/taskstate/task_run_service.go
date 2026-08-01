package taskstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TaskRunRepository interface {
	SaveTaskRun(TaskRun) error
	StartTaskRunAttempt(TaskRun, TaskAttempt) error
	FinishTaskRunAttempt(TaskRun, TaskAttempt) error
	TransitionTaskRun(TaskRunTransition) (TaskRun, error)
	FindTaskRun(string) (TaskRun, bool, error)
	FindTaskAttempt(string) (TaskAttempt, bool, error)
	ListTaskRun() ([]TaskRun, error)
	ListTaskRunByPersonID(string) ([]TaskRun, error)
	DeleteTaskRun(string, []string) (bool, error)
	DeleteTaskRunsBefore(time.Time, []string) ([]string, error)
}

var (
	ErrTaskRunNotFound     = errors.New("task run not found")
	ErrTaskRunAccessDenied = errors.New("task run access denied")
	ErrTaskRunNotDeletable = errors.New("task run is not deletable")
)

type ErrIllegalTransition struct {
	TaskRunID     string
	CurrentStatus TaskStatus
	FromStates    []TaskStatus
	ToState       TaskStatus
}

func (transitionError ErrIllegalTransition) Error() string {
	return "illegal task run transition from " + string(transitionError.CurrentStatus) + " to " + string(transitionError.ToState) + " for task run " + transitionError.TaskRunID
}

type TaskRunService struct {
	mutex            sync.RWMutex
	taskRuns         map[string]TaskRun
	taskAttempts     map[string]TaskAttempt
	activeAttempts   map[string]activeTaskAttempt
	taskEventService *TaskEventService
	repository       TaskRunRepository
	runnerID         string
}

type activeTaskAttempt struct {
	TaskRunID                string
	CancelFunction           context.CancelFunc
	CurrentToolName          string
	CurrentToolObservationID string
}

type InterruptedTaskResumeSelection struct {
	SelectedTaskRuns []TaskRun
	SkippedTaskRuns  []TaskRun
}

func NewTaskRunService(taskEventService *TaskEventService) *TaskRunService {
	return &TaskRunService{
		taskRuns:         map[string]TaskRun{},
		taskAttempts:     map[string]TaskAttempt{},
		activeAttempts:   map[string]activeTaskAttempt{},
		taskEventService: taskEventService,
		runnerID:         defaultTaskRunnerID(),
	}
}

func (taskRunService *TaskRunService) UseRepository(repository TaskRunRepository) {
	taskRunService.repository = repository
}

func (taskRunService *TaskRunService) CreateTaskRun(requesterPersonID string, originConversationID string, prompt string) TaskRun {
	return taskRunService.CreateTaskRunWithOrigin(requesterPersonID, TaskRunOrigin{ConversationID: originConversationID}, prompt)
}

func (taskRunService *TaskRunService) CreateTaskRunWithOrigin(requesterPersonID string, origin TaskRunOrigin, prompt string) TaskRun {
	taskRun, _ := taskRunService.CreateTaskRunWithOriginAndError(requesterPersonID, origin, prompt)
	return taskRun
}

func (taskRunService *TaskRunService) CreateTaskRunWithOriginAndError(requesterPersonID string, origin TaskRunOrigin, prompt string) (TaskRun, error) {
	taskRun := TaskRun{
		TaskRunID:            NewIdentifier(),
		RequesterPersonID:    requesterPersonID,
		OriginConversationID: strings.TrimSpace(origin.ConversationID),
		OriginReplyTargetID:  strings.TrimSpace(origin.ReplyTargetID),
		OriginIsThread:       origin.IsThread,
		Status:               TaskStatusPlanned,
		Prompt:               prompt,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}

	taskRunService.mutex.Lock()
	taskRunService.taskRuns[taskRun.TaskRunID] = taskRun
	taskRunService.mutex.Unlock()
	if errorValue := taskRunService.saveTaskRun(taskRun); errorValue != nil {
		return taskRun, errorValue
	}

	if _, errorValue := taskRunService.taskEventService.AppendTaskEventWithError(taskRun.TaskRunID, "task.created", prompt); errorValue != nil {
		return taskRun, errorValue
	}
	return taskRun, nil
}

func (taskRunService *TaskRunService) AppendTaskEvent(taskRunID string, name string, body string) {
	taskRunService.taskEventService.AppendTaskEvent(taskRunID, name, body)
}

func (taskRunService *TaskRunService) AppendTaskEventWithError(taskRunID string, name string, body string) (TaskEvent, error) {
	return taskRunService.taskEventService.AppendTaskEventWithError(taskRunID, name, body)
}

func (taskRunService *TaskRunService) RegisterTaskRunObserver(taskRunID string, observer func(RawTurnEvent)) func() {
	return taskRunService.taskEventService.RegisterTaskRunObserver(taskRunID, observer)
}

func (taskRunService *TaskRunService) RegisterTaskRunCancel(taskRunID string, cancelFunction context.CancelFunc) func() {
	trimmedTaskRunID := strings.TrimSpace(taskRunID)
	if trimmedTaskRunID == "" || cancelFunction == nil {
		return func() {}
	}
	taskRunService.mutex.Lock()
	taskRun, isFound := taskRunService.findTaskRunForMutation(trimmedTaskRunID)
	if !isFound || !taskRunService.taskRunHasActiveAttemptLocked(taskRun) {
		taskRunService.mutex.Unlock()
		return func() {}
	}
	taskAttemptID := taskRun.CurrentAttemptID
	activeAttempt := taskRunService.activeAttempts[taskAttemptID]
	activeAttempt.TaskRunID = trimmedTaskRunID
	activeAttempt.CancelFunction = cancelFunction
	taskRunService.activeAttempts[taskAttemptID] = activeAttempt
	taskRunService.mutex.Unlock()
	return func() {
		taskRunService.mutex.Lock()
		activeAttempt, isFound := taskRunService.activeAttempts[taskAttemptID]
		if isFound && activeAttempt.TaskRunID == trimmedTaskRunID {
			activeAttempt.CancelFunction = nil
			taskRunService.activeAttempts[taskAttemptID] = activeAttempt
		}
		taskRunService.mutex.Unlock()
	}
}

func (taskRunService *TaskRunService) RegisterTaskRunTool(taskRunID string, observationID string, toolName string) func() {
	trimmedTaskRunID := strings.TrimSpace(taskRunID)
	trimmedObservationID := strings.TrimSpace(observationID)
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedTaskRunID == "" || trimmedToolName == "" {
		return func() {}
	}
	taskRunService.mutex.Lock()
	taskRun, isFound := taskRunService.findTaskRunForMutation(trimmedTaskRunID)
	if !isFound || !taskRunService.taskRunHasActiveAttemptLocked(taskRun) {
		taskRunService.mutex.Unlock()
		return func() {}
	}
	taskAttemptID := taskRun.CurrentAttemptID
	activeAttempt := taskRunService.activeAttempts[taskAttemptID]
	activeAttempt.TaskRunID = trimmedTaskRunID
	activeAttempt.CurrentToolName = trimmedToolName
	activeAttempt.CurrentToolObservationID = trimmedObservationID
	taskRunService.activeAttempts[taskAttemptID] = activeAttempt
	taskRunService.mutex.Unlock()
	return func() {
		taskRunService.mutex.Lock()
		activeAttempt, isFound := taskRunService.activeAttempts[taskAttemptID]
		if isFound && activeAttempt.TaskRunID == trimmedTaskRunID && activeAttempt.CurrentToolObservationID == trimmedObservationID {
			activeAttempt.CurrentToolName = ""
			activeAttempt.CurrentToolObservationID = ""
			taskRunService.activeAttempts[taskAttemptID] = activeAttempt
		}
		taskRunService.mutex.Unlock()
	}
}

func (taskRunService *TaskRunService) IsTaskRunCancelled(taskRunID string) bool {
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	return isFound && taskRun.Status == TaskStatusCancelled
}

func (taskRunService *TaskRunService) ListTaskEvent(taskRunID string) []TaskEvent {
	return taskRunService.taskEventService.ListTaskEvent(taskRunID)
}

func (taskRunService *TaskRunService) AdvanceTaskRun(taskRunID string, currentAgentProfileName string) (TaskRun, error) {
	now := time.Now()
	taskAttempt := TaskAttempt{
		TaskAttemptID: NewIdentifier(),
		TaskRunID:     taskRunID,
		RunnerID:      taskRunService.runnerID,
		Status:        TaskAttemptStatusRunning,
		StartedAt:     now,
	}
	return taskRunService.TransitionTaskRun(TaskRunTransition{
		TaskRunID:               taskRunID,
		FromStates:              advanceTaskRunFromStates(),
		ToState:                 TaskStatusRunning,
		CurrentAgentProfileName: currentAgentProfileName,
		StartedAttempt:          &taskAttempt,
		Event:                   newTaskRunTransitionEvent(taskRunID, "task.running", currentAgentProfileName, now),
		UpdatedAt:               now,
	})
}

func (taskRunService *TaskRunService) PauseTaskRun(taskRunID string, status TaskStatus, reason string) (TaskRun, error) {
	now := time.Now()
	return taskRunService.TransitionTaskRun(TaskRunTransition{
		TaskRunID:             taskRunID,
		FromStates:            pauseTaskRunFromStates(),
		ToState:               status,
		FailureReason:         reason,
		FinishCurrentAttempt:  true,
		FinishedAttemptStatus: taskAttemptStatusForTaskStatus(status),
		RunnerID:              taskRunService.runnerID,
		Event:                 newTaskRunTransitionEvent(taskRunID, "task.paused", reason, now),
		UpdatedAt:             now,
	})
}

func (taskRunService *TaskRunService) FailTaskRun(taskRunID string, reason string) (TaskRun, error) {
	now := time.Now()
	return taskRunService.TransitionTaskRun(TaskRunTransition{
		TaskRunID:             taskRunID,
		FromStates:            failTaskRunFromStates(),
		ToState:               TaskStatusFailed,
		FailureReason:         reason,
		FinishCurrentAttempt:  true,
		FinishedAttemptStatus: TaskAttemptStatusFailed,
		RunnerID:              taskRunService.runnerID,
		Event:                 newTaskRunTransitionEvent(taskRunID, "task.paused", reason, now),
		UpdatedAt:             now,
	})
}

func (taskRunService *TaskRunService) TransitionTaskRun(transition TaskRunTransition) (TaskRun, error) {
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()

	normalizedTransition := taskRunService.normalizeTaskRunTransition(transition)
	if taskRunService.repository != nil {
		taskRun, errorValue := taskRunService.repository.TransitionTaskRun(normalizedTransition)
		if errorValue != nil {
			return TaskRun{}, errorValue
		}
		taskRunService.applyTransitionCacheLocked(normalizedTransition, taskRun)
		return taskRun, nil
	}
	return taskRunService.transitionTaskRunInMemoryLocked(normalizedTransition)
}

func (taskRunService *TaskRunService) normalizeTaskRunTransition(transition TaskRunTransition) TaskRunTransition {
	if transition.UpdatedAt.IsZero() {
		transition.UpdatedAt = time.Now()
	}
	if transition.StartedAttempt != nil && transition.StartedAttempt.StartedAt.IsZero() {
		startedAttempt := *transition.StartedAttempt
		startedAttempt.StartedAt = transition.UpdatedAt
		transition.StartedAttempt = &startedAttempt
	}
	if transition.Event != nil && transition.Event.CreatedAt.IsZero() {
		taskEvent := *transition.Event
		taskEvent.CreatedAt = transition.UpdatedAt
		transition.Event = &taskEvent
	}
	if transition.Event != nil && strings.TrimSpace(transition.Event.TaskEventID) == "" {
		taskEvent := *transition.Event
		taskEvent.TaskEventID = NewIdentifier()
		transition.Event = &taskEvent
	}
	return transition
}

func (taskRunService *TaskRunService) transitionTaskRunInMemoryLocked(transition TaskRunTransition) (TaskRun, error) {
	taskRun, isFound := taskRunService.findTaskRunForMutation(transition.TaskRunID)
	if !isFound {
		return TaskRun{}, errors.New("task run not found")
	}
	if !taskStatusAllowed(taskRun.Status, transition.FromStates) {
		return TaskRun{}, ErrIllegalTransition{
			TaskRunID:     transition.TaskRunID,
			CurrentStatus: taskRun.Status,
			FromStates:    append([]TaskStatus{}, transition.FromStates...),
			ToState:       transition.ToState,
		}
	}

	taskRun = applyTaskRunTransition(taskRun, transition)
	taskRunService.taskRuns[transition.TaskRunID] = taskRun
	taskRunService.applyTransitionAttemptCacheLocked(transition, taskRun)
	taskRunService.recordTransitionEvent(transition)
	return taskRun, nil
}

func (taskRunService *TaskRunService) applyTransitionCacheLocked(transition TaskRunTransition, taskRun TaskRun) {
	taskRunService.taskRuns[taskRun.TaskRunID] = taskRun
	taskRunService.applyTransitionAttemptCacheLocked(transition, taskRun)
	taskRunService.recordTransitionEvent(transition)
}

func (taskRunService *TaskRunService) applyTransitionAttemptCacheLocked(transition TaskRunTransition, taskRun TaskRun) {
	if transition.StartedAttempt != nil {
		taskRunService.taskAttempts[transition.StartedAttempt.TaskAttemptID] = *transition.StartedAttempt
		taskRunService.activeAttempts[transition.StartedAttempt.TaskAttemptID] = activeTaskAttempt{TaskRunID: taskRun.TaskRunID}
	}
	if !transition.FinishCurrentAttempt {
		return
	}
	taskAttemptID := strings.TrimSpace(taskRun.CurrentAttemptID)
	if taskAttemptID == "" {
		taskRunService.closeOpenToolRequests(taskRun.TaskRunID, "", "cancelled_by_attempt_end")
		return
	}
	taskAttempt := taskRunService.findTaskAttemptForMutation(taskAttemptID, taskRun.TaskRunID)
	taskAttempt.Status = transition.FinishedAttemptStatus
	taskAttempt.FinishedAt = &transition.UpdatedAt
	taskAttempt.FailureReason = strings.TrimSpace(transition.FailureReason)
	taskRunService.taskAttempts[taskAttemptID] = taskAttempt
	delete(taskRunService.activeAttempts, taskAttemptID)
	taskRunService.closeOpenToolRequests(taskRun.TaskRunID, taskAttemptID, "cancelled_by_attempt_end")
}

func (taskRunService *TaskRunService) recordTransitionEvent(transition TaskRunTransition) {
	if transition.Event == nil {
		return
	}
	taskRunService.taskEventService.RecordTaskEvent(*transition.Event)
}

func applyTaskRunTransition(taskRun TaskRun, transition TaskRunTransition) TaskRun {
	taskRun.Status = transition.ToState
	taskRun.UpdatedAt = transition.UpdatedAt
	if transition.StartedAttempt != nil {
		taskRun.CurrentAttemptID = transition.StartedAttempt.TaskAttemptID
		taskRun.CurrentAgentProfileName = transition.CurrentAgentProfileName
		taskRun.FailureReason = ""
	}
	if transition.Result != "" {
		taskRun.Result = transition.Result
	}
	if transition.FailureReason != "" || transition.FinishCurrentAttempt {
		taskRun.FailureReason = transition.FailureReason
	}
	return taskRun
}

func taskStatusAllowed(status TaskStatus, allowedStatuses []TaskStatus) bool {
	for _, allowedStatus := range allowedStatuses {
		if status == allowedStatus {
			return true
		}
	}
	return false
}

func newTaskRunTransitionEvent(taskRunID string, name string, body string, createdAt time.Time) *TaskEvent {
	return &TaskEvent{
		TaskEventID: NewIdentifier(),
		TaskRunID:   taskRunID,
		Name:        name,
		Body:        body,
		CreatedAt:   createdAt,
	}
}

func advanceTaskRunFromStates() []TaskStatus {
	return []TaskStatus{
		TaskStatusPlanned,
		TaskStatusRunning,
		TaskStatusWaitingUserInput,
		TaskStatusWaitingApproval,
		TaskStatusBlocked,
		TaskStatusInterrupted,
	}
}

func pauseTaskRunFromStates() []TaskStatus {
	return []TaskStatus{
		TaskStatusPlanned,
		TaskStatusRunning,
		TaskStatusWaitingUserInput,
		TaskStatusWaitingApproval,
		TaskStatusBlocked,
		TaskStatusInterrupted,
	}
}

func failTaskRunFromStates() []TaskStatus {
	return pauseTaskRunFromStates()
}

func completeTaskRunFromStates() []TaskStatus {
	return []TaskStatus{
		TaskStatusPlanned,
		TaskStatusRunning,
		TaskStatusWaitingUserInput,
		TaskStatusWaitingApproval,
		TaskStatusBlocked,
	}
}

func cancelTaskRunFromStates() []TaskStatus {
	return []TaskStatus{
		TaskStatusPlanned,
		TaskStatusRunning,
		TaskStatusWaitingUserInput,
		TaskStatusWaitingApproval,
		TaskStatusBlocked,
	}
}

func interruptInactiveTaskRunFromStates() []TaskStatus {
	return []TaskStatus{
		TaskStatusPlanned,
		TaskStatusRunning,
	}
}

func (taskRunService *TaskRunService) ResumeTaskRun(taskRunID string) (TaskRun, error) {
	return taskRunService.AdvanceTaskRun(taskRunID, "planner")
}

func (taskRunService *TaskRunService) CancelTaskRun(taskRunID string, requesterPersonID string) (TaskRun, error) {
	return taskRunService.cancelTaskRun(taskRunID, requesterPersonID, requesterPersonID)
}

func (taskRunService *TaskRunService) CancelTaskRunWithReason(taskRunID string, requesterPersonID string, reason string) (TaskRun, error) {
	return taskRunService.cancelTaskRun(taskRunID, requesterPersonID, reason)
}

func (taskRunService *TaskRunService) CancelWaitingTaskRuns(requesterPersonID string, originConversationID string, reason string) []TaskRun {
	cancelledTaskRuns := []TaskRun{}
	for _, taskRun := range taskRunService.ListTaskRunByPersonID(requesterPersonID) {
		if !taskRunIsWaiting(taskRun) {
			continue
		}
		if originConversationID != "" && taskRun.OriginConversationID != originConversationID {
			continue
		}
		cancelledTaskRun, errorValue := taskRunService.cancelTaskRun(taskRun.TaskRunID, requesterPersonID, reason)
		if errorValue != nil {
			continue
		}
		taskRunService.taskEventService.AppendTaskEvent(taskRun.TaskRunID, "task.wait_cancelled", reason)
		cancelledTaskRuns = append(cancelledTaskRuns, cancelledTaskRun)
	}
	return cancelledTaskRuns
}

func (taskRunService *TaskRunService) CancelActiveTaskRuns(request TaskRunCancelRequest) []TaskRun {
	cancelledTaskRuns := []TaskRun{}
	for _, taskRun := range taskRunService.taskRunsForCancelRequest(request) {
		if !taskRunMatchesCancelRequest(taskRun, request) {
			continue
		}
		cancelledTaskRun, errorValue := taskRunService.cancelTaskRun(taskRun.TaskRunID, request.RequesterPersonID, request.Reason)
		if errorValue != nil {
			continue
		}
		cancelledTaskRuns = append(cancelledTaskRuns, cancelledTaskRun)
	}
	return cancelledTaskRuns
}

func (taskRunService *TaskRunService) InterruptOrphanedRuntimeTaskRuns(reason string) []TaskRun {
	interruptedTaskRuns := []TaskRun{}
	for _, taskRun := range taskRunService.ListTaskRun() {
		if !taskRunIsRuntimeOwned(taskRun) {
			continue
		}
		interruptedTaskRun, isInterrupted := taskRunService.InterruptInactiveTaskRun(taskRun.TaskRunID, reason)
		if !isInterrupted {
			continue
		}
		interruptedTaskRuns = append(interruptedTaskRuns, interruptedTaskRun)
	}
	return interruptedTaskRuns
}

func (taskRunService *TaskRunService) InterruptRuntimeTaskRunsForPlannedShutdown() []TaskRun {
	interruptedTaskRuns := []TaskRun{}
	for _, taskRun := range taskRunService.ListTaskRun() {
		if !taskRunIsRuntimeOwned(taskRun) {
			continue
		}
		now := time.Now()
		interruptedTaskRun, errorValue := taskRunService.TransitionTaskRun(TaskRunTransition{
			TaskRunID:             taskRun.TaskRunID,
			FromStates:            interruptInactiveTaskRunFromStates(),
			ToState:               TaskStatusInterrupted,
			FailureReason:         TaskInterruptReasonPlannedShutdown,
			FinishCurrentAttempt:  true,
			FinishedAttemptStatus: TaskAttemptStatusInterrupted,
			RunnerID:              taskRunService.runnerID,
			Event:                 newTaskRunTransitionEvent(taskRun.TaskRunID, "task.interrupted", TaskInterruptReasonPlannedShutdown, now),
			UpdatedAt:             now,
		})
		if errorValue != nil {
			continue
		}
		interruptedTaskRuns = append(interruptedTaskRuns, interruptedTaskRun)
	}
	return interruptedTaskRuns
}

func (taskRunService *TaskRunService) InterruptInactiveTaskRun(taskRunID string, reason string) (TaskRun, bool) {
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return TaskRun{}, false
	}
	if taskRun.Status == TaskStatusRunning && taskRunService.IsTaskRunActuallyRunning(taskRun) {
		return TaskRun{}, false
	}

	now := time.Now()
	interruptedTaskRun, errorValue := taskRunService.TransitionTaskRun(TaskRunTransition{
		TaskRunID:             taskRunID,
		FromStates:            interruptInactiveTaskRunFromStates(),
		ToState:               TaskStatusInterrupted,
		FailureReason:         reason,
		FinishCurrentAttempt:  true,
		FinishedAttemptStatus: TaskAttemptStatusInterrupted,
		RunnerID:              taskRunService.runnerID,
		Event:                 newTaskRunTransitionEvent(taskRunID, "task.interrupted", reason, now),
		UpdatedAt:             now,
	})
	if errorValue != nil {
		return TaskRun{}, false
	}
	return interruptedTaskRun, true
}

func (taskRunService *TaskRunService) SelectInterruptedTaskRunsForAutoResume(now time.Time, limit int) InterruptedTaskResumeSelection {
	if now.IsZero() {
		now = time.Now()
	}
	candidates := taskRunService.interruptedTaskRunsEligibleForAutoResume(now)
	if limit <= 0 || len(candidates) <= limit {
		return InterruptedTaskResumeSelection{SelectedTaskRuns: candidates}
	}
	return InterruptedTaskResumeSelection{
		SelectedTaskRuns: candidates[:limit],
		SkippedTaskRuns:  candidates[limit:],
	}
}

func (taskRunService *TaskRunService) ClaimInterruptedTaskRunAutoResume(taskRunID string, reason string) bool {
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	if !isFound || taskRun.Status != TaskStatusInterrupted {
		return false
	}
	attemptCount := taskRunService.autoResumeAttemptCount(taskRun.TaskRunID)
	if attemptCount > 0 && !taskRunWasInterruptedByPlannedShutdown(taskRun) {
		return false
	}
	taskRunService.taskEventService.AppendTaskEvent(taskRun.TaskRunID, "task.auto_resume_attempted", marshalTaskRunServiceEventBody(map[string]any{
		"attemptCount": attemptCount + 1,
		"reason":       strings.TrimSpace(reason),
	}))
	return true
}

func (taskRunService *TaskRunService) MarkInterruptedTaskRunAutoResumeSkipped(taskRunID string, reason string) {
	taskRunService.taskEventService.AppendTaskEvent(taskRunID, "task.auto_resume_skipped", marshalTaskRunServiceEventBody(map[string]string{
		"reason": strings.TrimSpace(reason),
	}))
}

func (taskRunService *TaskRunService) IsTaskRunActuallyRunning(taskRun TaskRun) bool {
	taskRunService.mutex.RLock()
	defer taskRunService.mutex.RUnlock()
	return taskRunService.taskRunHasActiveAttemptLocked(taskRun)
}

func (taskRunService *TaskRunService) CloseOpenToolRequests(taskRunID string, reason string) {
	taskRunService.closeOpenToolRequests(taskRunID, "", reason)
}

func (taskRunService *TaskRunService) closeOpenToolRequests(taskRunID string, taskAttemptID string, reason string) {
	openRequests := openToolRequests(taskRunService.taskEventService.ListTaskEvent(taskRunID))
	for _, openRequest := range openRequests {
		taskRunService.taskEventService.AppendTaskEvent(taskRunID, "tool."+openRequest.ToolName+".cancelled", marshalTaskRunServiceEventBody(map[string]string{
			"observationID":  openRequest.ObservationID,
			"toolName":       openRequest.ToolName,
			"taskAttemptID":  taskAttemptID,
			"reason":         reason,
			"terminalStatus": "cancelled",
		}))
	}
}

func (taskRunService *TaskRunService) taskRunsForCancelRequest(request TaskRunCancelRequest) []TaskRun {
	if len(request.TaskRunIDs) > 0 {
		taskRuns := []TaskRun{}
		for _, taskRunID := range trimUniqueTaskRunIDs(request.TaskRunIDs) {
			taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
			if isFound {
				taskRuns = append(taskRuns, taskRun)
			}
		}
		return taskRuns
	}
	if strings.TrimSpace(request.RequesterPersonID) != "" {
		return taskRunService.ListTaskRunByPersonID(request.RequesterPersonID)
	}
	return taskRunService.ListTaskRun()
}

func taskRunMatchesCancelRequest(taskRun TaskRun, request TaskRunCancelRequest) bool {
	if !taskRunIsActive(taskRun) {
		return false
	}
	if requesterPersonID := strings.TrimSpace(request.RequesterPersonID); requesterPersonID != "" && taskRun.RequesterPersonID != requesterPersonID {
		return false
	}
	if request.ScheduleOnly && !strings.HasPrefix(taskRun.OriginConversationID, "schedule:") {
		return false
	}
	if originConversationIDPrefix := strings.TrimSpace(request.OriginConversationIDPrefix); originConversationIDPrefix != "" && !strings.HasPrefix(taskRun.OriginConversationID, originConversationIDPrefix) {
		return false
	}
	if len(request.OriginConversationIDs) > 0 && !containsTrimmedString(request.OriginConversationIDs, taskRun.OriginConversationID) {
		return false
	}
	if request.StaleBefore != nil && !taskRun.UpdatedAt.Before(*request.StaleBefore) {
		return false
	}
	return true
}

func taskRunIsActive(taskRun TaskRun) bool {
	switch taskRun.Status {
	case TaskStatusPlanned, TaskStatusRunning, TaskStatusWaitingApproval, TaskStatusWaitingUserInput, TaskStatusBlocked:
		return true
	default:
		return false
	}
}

func taskRunIsWaiting(taskRun TaskRun) bool {
	return taskRun.Status == TaskStatusWaitingApproval || taskRun.Status == TaskStatusWaitingUserInput
}

func taskRunIsRuntimeOwned(taskRun TaskRun) bool {
	return taskRun.Status == TaskStatusPlanned || taskRun.Status == TaskStatusRunning
}

const staleBlockedTaskRunAge = 24 * time.Hour
const staleWaitingTaskRunAge = 72 * time.Hour

func StaleUnattendedTaskRunReason(taskRun TaskRun, now time.Time) string {
	switch taskRun.Status {
	case TaskStatusBlocked:
		if now.Sub(taskRun.UpdatedAt) > staleBlockedTaskRunAge {
			return "blocked_expired"
		}
	case TaskStatusWaitingApproval, TaskStatusWaitingUserInput:
		if now.Sub(taskRun.UpdatedAt) > staleWaitingTaskRunAge {
			return "waiting_expired"
		}
	}
	return ""
}

func (taskRunService *TaskRunService) SelectStaleUnattendedTaskRuns(now time.Time) []TaskRun {
	if now.IsZero() {
		now = time.Now()
	}
	taskRuns := []TaskRun{}
	for _, taskRun := range taskRunService.ListTaskRun() {
		if StaleUnattendedTaskRunReason(taskRun, now) == "" {
			continue
		}
		taskRuns = append(taskRuns, taskRun)
	}
	sort.SliceStable(taskRuns, func(leftIndex int, rightIndex int) bool {
		return taskRuns[leftIndex].UpdatedAt.Before(taskRuns[rightIndex].UpdatedAt)
	})
	return taskRuns
}

func (taskRunService *TaskRunService) interruptedTaskRunsEligibleForAutoResume(now time.Time) []TaskRun {
	taskRuns := []TaskRun{}
	for _, taskRun := range taskRunService.ListTaskRun() {
		if !taskRunService.canAutoResumeInterruptedTaskRun(taskRun, now) {
			continue
		}
		taskRuns = append(taskRuns, taskRun)
	}
	sort.SliceStable(taskRuns, func(leftIndex int, rightIndex int) bool {
		return taskRuns[leftIndex].UpdatedAt.After(taskRuns[rightIndex].UpdatedAt)
	})
	return taskRuns
}

func (taskRunService *TaskRunService) canAutoResumeInterruptedTaskRun(taskRun TaskRun, now time.Time) bool {
	if taskRun.Status != TaskStatusInterrupted {
		return false
	}
	if now.Sub(taskRun.UpdatedAt) > 24*time.Hour {
		return false
	}
	if taskRunService.autoResumeAttemptCount(taskRun.TaskRunID) > 0 && !taskRunWasInterruptedByPlannedShutdown(taskRun) {
		return false
	}
	return taskRunService.taskRunHasInterruptedMarker(taskRun.TaskRunID)
}

func taskRunWasInterruptedByPlannedShutdown(taskRun TaskRun) bool {
	return taskRun.Status == TaskStatusInterrupted && taskRun.FailureReason == TaskInterruptReasonPlannedShutdown
}

func (taskRunService *TaskRunService) taskRunHasInterruptedMarker(taskRunID string) bool {
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == "task.interrupted" {
			return true
		}
	}
	return false
}

func (taskRunService *TaskRunService) autoResumeAttemptCount(taskRunID string) int {
	count := 0
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == "task.auto_resume_attempted" {
			count++
		}
	}
	return count
}

func trimUniqueTaskRunIDs(taskRunIDs []string) []string {
	seenTaskRunIDs := map[string]bool{}
	trimmedTaskRunIDs := []string{}
	for _, taskRunID := range taskRunIDs {
		trimmedTaskRunID := strings.TrimSpace(taskRunID)
		if trimmedTaskRunID == "" || seenTaskRunIDs[trimmedTaskRunID] {
			continue
		}
		seenTaskRunIDs[trimmedTaskRunID] = true
		trimmedTaskRunIDs = append(trimmedTaskRunIDs, trimmedTaskRunID)
	}
	return trimmedTaskRunIDs
}

func containsTrimmedString(values []string, expectedValue string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expectedValue {
			return true
		}
	}
	return false
}

func (taskRunService *TaskRunService) CompleteTaskRun(taskRunID string, result string) (TaskRun, error) {
	now := time.Now()
	return taskRunService.TransitionTaskRun(TaskRunTransition{
		TaskRunID:             taskRunID,
		FromStates:            completeTaskRunFromStates(),
		ToState:               TaskStatusCompleted,
		Result:                result,
		FinishCurrentAttempt:  true,
		FinishedAttemptStatus: TaskAttemptStatusCompleted,
		RunnerID:              taskRunService.runnerID,
		Event:                 newTaskRunTransitionEvent(taskRunID, "task.completed", result, now),
		UpdatedAt:             now,
	})
}

func (taskRunService *TaskRunService) RecordTaskRunResult(taskRunID string, result string) (TaskRun, error) {
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return TaskRun{}, ErrTaskRunNotFound
	}
	taskRun.Result = result
	taskRun.UpdatedAt = time.Now()
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()
	if taskRunService.repository != nil {
		if errorValue := taskRunService.repository.SaveTaskRun(taskRun); errorValue != nil {
			return TaskRun{}, errorValue
		}
	}
	taskRunService.taskRuns[taskRunID] = taskRun
	return taskRun, nil
}

func (taskRunService *TaskRunService) FindTaskRun(taskRunID string) (TaskRun, bool) {
	if taskRunService.repository != nil {
		taskRun, isFound, errorValue := taskRunService.repository.FindTaskRun(taskRunID)
		if errorValue == nil {
			return taskRun, isFound
		}
	}
	taskRunService.mutex.RLock()
	defer taskRunService.mutex.RUnlock()

	taskRun, isFound := taskRunService.taskRuns[taskRunID]
	return taskRun, isFound
}

func (taskRunService *TaskRunService) ListTaskRun() []TaskRun {
	if taskRunService.repository != nil {
		taskRuns, errorValue := taskRunService.repository.ListTaskRun()
		if errorValue == nil {
			return taskRuns
		}
	}
	taskRunService.mutex.RLock()
	defer taskRunService.mutex.RUnlock()

	taskRuns := make([]TaskRun, 0, len(taskRunService.taskRuns))
	for _, taskRun := range taskRunService.taskRuns {
		taskRuns = append(taskRuns, taskRun)
	}

	return taskRuns
}

func (taskRunService *TaskRunService) ListTaskRunByPersonID(personID string) []TaskRun {
	if taskRunService.repository != nil {
		taskRuns, errorValue := taskRunService.repository.ListTaskRunByPersonID(personID)
		if errorValue == nil {
			return taskRuns
		}
	}
	taskRuns := []TaskRun{}
	for _, taskRun := range taskRunService.ListTaskRun() {
		if taskRun.RequesterPersonID == personID {
			taskRuns = append(taskRuns, taskRun)
		}
	}
	return taskRuns
}

func (taskRunService *TaskRunService) DeleteTerminalTaskRun(taskRunID string, requesterPersonID string) (TaskRun, error) {
	taskRun, isFound := taskRunService.FindTaskRun(strings.TrimSpace(taskRunID))
	if !isFound {
		return TaskRun{}, ErrTaskRunNotFound
	}
	if requesterPersonID != "" && taskRun.RequesterPersonID != requesterPersonID {
		return TaskRun{}, ErrTaskRunAccessDenied
	}
	if !isTerminalTaskRunStatus(taskRun.Status) {
		return TaskRun{}, ErrTaskRunNotDeletable
	}
	if taskRunService.repository != nil {
		wasDeleted, errorValue := taskRunService.repository.DeleteTaskRun(taskRun.TaskRunID, terminalTaskRunStatusStrings())
		if errorValue != nil {
			return TaskRun{}, errorValue
		}
		if !wasDeleted {
			return TaskRun{}, ErrTaskRunNotFound
		}
		taskRunService.evictTaskRunIDs([]string{taskRun.TaskRunID})
		return taskRun, nil
	}
	if !taskRunService.deleteTerminalTaskRunFromMemory(taskRun.TaskRunID) {
		return TaskRun{}, ErrTaskRunNotFound
	}
	return taskRun, nil
}

func (taskRunService *TaskRunService) saveTaskRun(taskRun TaskRun) error {
	if taskRunService.repository == nil {
		return nil
	}
	return taskRunService.repository.SaveTaskRun(taskRun)
}

func (taskRunService *TaskRunService) cancelTaskRun(taskRunID string, requesterPersonID string, reason string) (TaskRun, error) {
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return TaskRun{}, errors.New("task run not found")
	}
	if requesterPersonID != "" && taskRun.RequesterPersonID != requesterPersonID {
		return TaskRun{}, errors.New("task run access denied")
	}

	cancelFunction := taskRunService.cancelFunctionForTaskRun(taskRun)
	now := time.Now()
	cancelledTaskRun, errorValue := taskRunService.TransitionTaskRun(TaskRunTransition{
		TaskRunID:             taskRunID,
		FromStates:            cancelTaskRunFromStates(),
		ToState:               TaskStatusCancelled,
		FailureReason:         strings.TrimSpace(reason),
		FinishCurrentAttempt:  true,
		FinishedAttemptStatus: TaskAttemptStatusCancelled,
		RunnerID:              taskRunService.runnerID,
		Event:                 newTaskRunTransitionEvent(taskRunID, "task.cancelled", firstNonEmptyTaskRunString(reason, requesterPersonID), now),
		UpdatedAt:             now,
	})
	if errorValue != nil {
		return TaskRun{}, errorValue
	}
	if cancelFunction != nil {
		cancelFunction()
	}
	return cancelledTaskRun, nil
}

func (taskRunService *TaskRunService) cancelFunctionForTaskRun(taskRun TaskRun) context.CancelFunc {
	taskRunService.mutex.RLock()
	defer taskRunService.mutex.RUnlock()
	if !taskRunService.taskRunHasActiveAttemptLocked(taskRun) {
		return nil
	}
	activeAttempt := taskRunService.activeAttempts[taskRun.CurrentAttemptID]
	return activeAttempt.CancelFunction
}

func (taskRunService *TaskRunService) startTaskRunAttempt(taskRun TaskRun, taskAttempt TaskAttempt) error {
	if taskRunService.repository == nil {
		return nil
	}
	return taskRunService.repository.StartTaskRunAttempt(taskRun, taskAttempt)
}

func (taskRunService *TaskRunService) finishCurrentAttemptLocked(taskRun TaskRun, status TaskAttemptStatus, reason string) (context.CancelFunc, error) {
	taskAttemptID := strings.TrimSpace(taskRun.CurrentAttemptID)
	if taskAttemptID == "" {
		if errorValue := taskRunService.saveTaskRun(taskRun); errorValue != nil {
			return nil, errorValue
		}
		taskRunService.closeOpenToolRequests(taskRun.TaskRunID, "", "cancelled_by_attempt_end")
		return nil, nil
	}
	taskAttempt := taskRunService.findTaskAttemptForMutation(taskAttemptID, taskRun.TaskRunID)
	now := time.Now()
	taskAttempt.Status = status
	taskAttempt.FinishedAt = &now
	taskAttempt.FailureReason = strings.TrimSpace(reason)
	if errorValue := taskRunService.finishTaskRunAttempt(taskRun, taskAttempt); errorValue != nil {
		return nil, errorValue
	}
	taskRunService.taskAttempts[taskAttemptID] = taskAttempt
	activeAttempt := taskRunService.activeAttempts[taskAttemptID]
	delete(taskRunService.activeAttempts, taskAttemptID)
	taskRunService.closeOpenToolRequests(taskRun.TaskRunID, taskAttemptID, "cancelled_by_attempt_end")
	return activeAttempt.CancelFunction, nil
}

func (taskRunService *TaskRunService) findTaskAttemptForMutation(taskAttemptID string, taskRunID string) TaskAttempt {
	taskAttempt, isFound := taskRunService.taskAttempts[taskAttemptID]
	if isFound {
		return taskAttempt
	}
	if taskRunService.repository != nil {
		taskAttempt, isFound, errorValue := taskRunService.repository.FindTaskAttempt(taskAttemptID)
		if errorValue == nil && isFound {
			taskRunService.taskAttempts[taskAttemptID] = taskAttempt
			return taskAttempt
		}
	}
	return TaskAttempt{
		TaskAttemptID: taskAttemptID,
		TaskRunID:     taskRunID,
		RunnerID:      taskRunService.runnerID,
		Status:        TaskAttemptStatusRunning,
		StartedAt:     time.Now(),
	}
}

func (taskRunService *TaskRunService) finishTaskRunAttempt(taskRun TaskRun, taskAttempt TaskAttempt) error {
	if taskRunService.repository == nil {
		return nil
	}
	return taskRunService.repository.FinishTaskRunAttempt(taskRun, taskAttempt)
}

func (taskRunService *TaskRunService) taskRunHasActiveAttemptLocked(taskRun TaskRun) bool {
	if taskRun.Status != TaskStatusRunning {
		return false
	}
	taskAttemptID := strings.TrimSpace(taskRun.CurrentAttemptID)
	if taskAttemptID == "" {
		return false
	}
	activeAttempt, isFound := taskRunService.activeAttempts[taskAttemptID]
	return isFound && activeAttempt.TaskRunID == taskRun.TaskRunID
}

func taskAttemptStatusForTaskStatus(status TaskStatus) TaskAttemptStatus {
	switch status {
	case TaskStatusCompleted:
		return TaskAttemptStatusCompleted
	case TaskStatusCancelled:
		return TaskAttemptStatusCancelled
	case TaskStatusFailed:
		return TaskAttemptStatusFailed
	default:
		return TaskAttemptStatusInterrupted
	}
}

type openToolRequest struct {
	ObservationID string
	ToolName      string
}

type toolEventBody struct {
	ObservationID string `json:"observationID"`
	ToolName      string `json:"toolName"`
}

func openToolRequests(taskEvents []TaskEvent) []openToolRequest {
	requests := []openToolRequest{}
	closedObservationIDs := map[string]bool{}
	for _, taskEvent := range taskEvents {
		toolName, isToolEvent := toolEventName(taskEvent.Name, ".requested")
		if isToolEvent {
			body := parseToolEventBody(taskEvent.Body)
			requests = append(requests, openToolRequest{ObservationID: firstNonEmptyTaskRunString(body.ObservationID, taskEvent.TaskEventID), ToolName: firstNonEmptyTaskRunString(body.ToolName, toolName)})
			continue
		}
		if _, isResult := toolEventName(taskEvent.Name, ".result"); isResult {
			body := parseToolEventBody(taskEvent.Body)
			if body.ObservationID != "" {
				closedObservationIDs[body.ObservationID] = true
			}
			continue
		}
		if _, isCancelled := toolEventName(taskEvent.Name, ".cancelled"); isCancelled {
			body := parseToolEventBody(taskEvent.Body)
			if body.ObservationID != "" {
				closedObservationIDs[body.ObservationID] = true
			}
		}
	}
	openRequests := []openToolRequest{}
	for _, request := range requests {
		if !closedObservationIDs[request.ObservationID] {
			openRequests = append(openRequests, request)
		}
	}
	return openRequests
}

func toolEventName(eventName string, suffix string) (string, bool) {
	trimmedName := strings.TrimSpace(eventName)
	if !strings.HasPrefix(trimmedName, "tool.") || !strings.HasSuffix(trimmedName, suffix) {
		return "", false
	}
	toolName := strings.TrimSuffix(strings.TrimPrefix(trimmedName, "tool."), suffix)
	return strings.TrimSpace(toolName), strings.TrimSpace(toolName) != ""
}

func parseToolEventBody(body string) toolEventBody {
	var parsedBody toolEventBody
	_ = json.Unmarshal([]byte(body), &parsedBody)
	return parsedBody
}

func marshalTaskRunServiceEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return "{}"
	}
	return string(document)
}

func defaultTaskRunnerID() string {
	hostname, _ := os.Hostname()
	return firstNonEmptyTaskRunString(strings.TrimSpace(hostname), "unknown-host") + ":" + strconv.Itoa(os.Getpid())
}

func firstNonEmptyTaskRunString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func (taskRunService *TaskRunService) findTaskRunForMutation(taskRunID string) (TaskRun, bool) {
	taskRun, isFound := taskRunService.taskRuns[taskRunID]
	if isFound || taskRunService.repository == nil {
		return taskRun, isFound
	}
	taskRun, isFound, errorValue := taskRunService.repository.FindTaskRun(taskRunID)
	if errorValue != nil || !isFound {
		return TaskRun{}, false
	}
	taskRunService.taskRuns[taskRunID] = taskRun
	return taskRun, true
}

func (taskRunService *TaskRunService) PruneTerminalTaskRunsBefore(cutoff time.Time) []string {
	terminalStatuses := terminalTaskRunStatusStrings()
	if taskRunService.repository != nil {
		deletedIDs, errorValue := taskRunService.repository.DeleteTaskRunsBefore(cutoff, terminalStatuses)
		if errorValue == nil {
			taskRunService.evictTaskRunIDs(deletedIDs)
			return deletedIDs
		}
	}
	return taskRunService.pruneTerminalTaskRunsFromMemory(cutoff, terminalStatuses)
}

func (taskRunService *TaskRunService) pruneTerminalTaskRunsFromMemory(cutoff time.Time, terminalStatuses []string) []string {
	terminalStatusSet := map[string]bool{}
	for _, status := range terminalStatuses {
		terminalStatusSet[status] = true
	}
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()
	prunedIDs := []string{}
	for taskRunID, taskRun := range taskRunService.taskRuns {
		if terminalStatusSet[string(taskRun.Status)] && taskRun.UpdatedAt.Before(cutoff) {
			prunedIDs = append(prunedIDs, taskRunID)
		}
	}
	taskRunService.evictTaskRunIDsLocked(prunedIDs)
	return prunedIDs
}

func (taskRunService *TaskRunService) deleteTerminalTaskRunFromMemory(taskRunID string) bool {
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()
	taskRun, isFound := taskRunService.taskRuns[taskRunID]
	if !isFound || !isTerminalTaskRunStatus(taskRun.Status) {
		return false
	}
	taskRunService.evictTaskRunIDsLocked([]string{taskRunID})
	return true
}

func (taskRunService *TaskRunService) evictTaskRunIDs(taskRunIDs []string) {
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()
	taskRunService.evictTaskRunIDsLocked(taskRunIDs)
}

func (taskRunService *TaskRunService) evictTaskRunIDsLocked(taskRunIDs []string) {
	for _, taskRunID := range taskRunIDs {
		delete(taskRunService.taskRuns, taskRunID)
	}
	for taskAttemptID, taskAttempt := range taskRunService.taskAttempts {
		for _, taskRunID := range taskRunIDs {
			if taskAttempt.TaskRunID == taskRunID {
				delete(taskRunService.taskAttempts, taskAttemptID)
				break
			}
		}
	}
	for taskAttemptID, activeAttempt := range taskRunService.activeAttempts {
		for _, taskRunID := range taskRunIDs {
			if activeAttempt.TaskRunID == taskRunID {
				delete(taskRunService.activeAttempts, taskAttemptID)
				break
			}
		}
	}
}

func terminalTaskRunStatusStrings() []string {
	return []string{
		string(TaskStatusCompleted),
		string(TaskStatusFailed),
		string(TaskStatusCancelled),
		string(TaskStatusBlocked),
	}
}

func isTerminalTaskRunStatus(status TaskStatus) bool {
	for _, terminalStatus := range terminalTaskRunStatusStrings() {
		if string(status) == terminalStatus {
			return true
		}
	}
	return false
}
