package task

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

type TaskRunRepository interface {
	SaveTaskRun(TaskRun) error
	FindTaskRun(string) (TaskRun, bool, error)
	ListTaskRun() ([]TaskRun, error)
	ListTaskRunByPersonID(string) ([]TaskRun, error)
}

type TaskRunService struct {
	mutex               sync.RWMutex
	taskRuns            map[string]TaskRun
	taskCancelFunctions map[string]context.CancelFunc
	taskEventService    *TaskEventService
	repository          TaskRunRepository
}

type TaskRunCancelRequest struct {
	TaskRunIDs                 []string
	RequesterPersonID          string
	OriginConversationIDs      []string
	OriginConversationIDPrefix string
	ScheduleOnly               bool
	StaleBefore                *time.Time
	Reason                     string
}

type TaskRunOrigin struct {
	ConversationID string
	ReplyTargetID  string
	IsThread       bool
}

func NewTaskRunService(taskEventService *TaskEventService) *TaskRunService {
	return &TaskRunService{
		taskRuns:            map[string]TaskRun{},
		taskCancelFunctions: map[string]context.CancelFunc{},
		taskEventService:    taskEventService,
	}
}

func (taskRunService *TaskRunService) UseRepository(repository TaskRunRepository) {
	taskRunService.repository = repository
}

func (taskRunService *TaskRunService) CreateTaskRun(requesterPersonID string, originConversationID string, prompt string) TaskRun {
	return taskRunService.CreateTaskRunWithOrigin(requesterPersonID, TaskRunOrigin{ConversationID: originConversationID}, prompt)
}

func (taskRunService *TaskRunService) CreateTaskRunWithOrigin(requesterPersonID string, origin TaskRunOrigin, prompt string) TaskRun {
	taskRun := TaskRun{
		TaskRunID:            newIdentifier(),
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
	_ = taskRunService.saveTaskRun(taskRun)

	taskRunService.taskEventService.AppendTaskEvent(taskRun.TaskRunID, "task.created", prompt)
	return taskRun
}

func (taskRunService *TaskRunService) AppendTaskEvent(taskRunID string, name string, body string) {
	taskRunService.taskEventService.AppendTaskEvent(taskRunID, name, body)
}

func (taskRunService *TaskRunService) RegisterTaskRunCancel(taskRunID string, cancelFunction context.CancelFunc) func() {
	trimmedTaskRunID := strings.TrimSpace(taskRunID)
	if trimmedTaskRunID == "" || cancelFunction == nil {
		return func() {}
	}
	taskRunService.mutex.Lock()
	taskRunService.taskCancelFunctions[trimmedTaskRunID] = cancelFunction
	taskRunService.mutex.Unlock()
	return func() {
		taskRunService.mutex.Lock()
		delete(taskRunService.taskCancelFunctions, trimmedTaskRunID)
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
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()

	taskRun, isFound := taskRunService.findTaskRunForMutation(taskRunID)
	if !isFound {
		return TaskRun{}, errors.New("task run not found")
	}

	taskRun.Status = TaskStatusRunning
	taskRun.CurrentAgentProfileName = currentAgentProfileName
	taskRun.UpdatedAt = time.Now()
	taskRunService.taskRuns[taskRunID] = taskRun
	_ = taskRunService.saveTaskRun(taskRun)
	taskRunService.taskEventService.AppendTaskEvent(taskRunID, "task.running", currentAgentProfileName)
	return taskRun, nil
}

func (taskRunService *TaskRunService) PauseTaskRun(taskRunID string, status TaskStatus, reason string) (TaskRun, error) {
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()

	taskRun, isFound := taskRunService.findTaskRunForMutation(taskRunID)
	if !isFound {
		return TaskRun{}, errors.New("task run not found")
	}

	taskRun.Status = status
	taskRun.FailureReason = reason
	taskRun.UpdatedAt = time.Now()
	taskRunService.taskRuns[taskRunID] = taskRun
	_ = taskRunService.saveTaskRun(taskRun)
	taskRunService.taskEventService.AppendTaskEvent(taskRunID, "task.paused", reason)
	return taskRun, nil
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
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()

	taskRun, isFound := taskRunService.findTaskRunForMutation(taskRunID)
	if !isFound {
		return TaskRun{}, errors.New("task run not found")
	}
	if taskRun.Status == TaskStatusCancelled {
		return taskRun, errors.New("task run was cancelled")
	}

	taskRun.Status = TaskStatusCompleted
	taskRun.Result = result
	taskRun.UpdatedAt = time.Now()
	taskRunService.taskRuns[taskRunID] = taskRun
	delete(taskRunService.taskCancelFunctions, taskRunID)
	_ = taskRunService.saveTaskRun(taskRun)
	taskRunService.taskEventService.AppendTaskEvent(taskRunID, "task.completed", result)
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

func (taskRunService *TaskRunService) saveTaskRun(taskRun TaskRun) error {
	if taskRunService.repository == nil {
		return nil
	}
	return taskRunService.repository.SaveTaskRun(taskRun)
}

func (taskRunService *TaskRunService) cancelTaskRun(taskRunID string, requesterPersonID string, reason string) (TaskRun, error) {
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()

	taskRun, isFound := taskRunService.findTaskRunForMutation(taskRunID)
	if !isFound {
		return TaskRun{}, errors.New("task run not found")
	}
	if requesterPersonID != "" && taskRun.RequesterPersonID != requesterPersonID {
		return TaskRun{}, errors.New("task run access denied")
	}
	if !taskRunIsActive(taskRun) {
		return TaskRun{}, errors.New("task run cannot be cancelled")
	}

	taskRun.Status = TaskStatusCancelled
	taskRun.FailureReason = strings.TrimSpace(reason)
	taskRun.UpdatedAt = time.Now()
	taskRunService.taskRuns[taskRunID] = taskRun
	cancelFunction := taskRunService.taskCancelFunctions[taskRunID]
	delete(taskRunService.taskCancelFunctions, taskRunID)
	_ = taskRunService.saveTaskRun(taskRun)
	taskRunService.taskEventService.AppendTaskEvent(taskRunID, "task.cancelled", firstNonEmptyTaskRunString(reason, requesterPersonID))
	if cancelFunction != nil {
		cancelFunction()
	}
	return taskRun, nil
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
