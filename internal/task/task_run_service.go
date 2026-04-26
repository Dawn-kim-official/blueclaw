package task

import (
	"errors"
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
	mutex            sync.RWMutex
	taskRuns         map[string]TaskRun
	taskEventService *TaskEventService
	repository       TaskRunRepository
}

func NewTaskRunService(taskEventService *TaskEventService) *TaskRunService {
	return &TaskRunService{
		taskRuns:         map[string]TaskRun{},
		taskEventService: taskEventService,
	}
}

func (taskRunService *TaskRunService) UseRepository(repository TaskRunRepository) {
	taskRunService.repository = repository
}

func (taskRunService *TaskRunService) CreateTaskRun(requesterPersonID string, originConversationID string, prompt string) TaskRun {
	taskRun := TaskRun{
		TaskRunID:            newIdentifier(),
		RequesterPersonID:    requesterPersonID,
		OriginConversationID: originConversationID,
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
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()

	taskRun, isFound := taskRunService.findTaskRunForMutation(taskRunID)
	if !isFound {
		return TaskRun{}, errors.New("task run not found")
	}
	if taskRun.RequesterPersonID != requesterPersonID {
		return TaskRun{}, errors.New("task run access denied")
	}
	if taskRun.Status == TaskStatusCompleted || taskRun.Status == TaskStatusCancelled {
		return TaskRun{}, errors.New("task run cannot be cancelled")
	}

	taskRun.Status = TaskStatusCancelled
	taskRun.UpdatedAt = time.Now()
	taskRunService.taskRuns[taskRunID] = taskRun
	_ = taskRunService.saveTaskRun(taskRun)
	taskRunService.taskEventService.AppendTaskEvent(taskRunID, "task.cancelled", requesterPersonID)
	return taskRun, nil
}

func (taskRunService *TaskRunService) CompleteTaskRun(taskRunID string, result string) (TaskRun, error) {
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()

	taskRun, isFound := taskRunService.findTaskRunForMutation(taskRunID)
	if !isFound {
		return TaskRun{}, errors.New("task run not found")
	}

	taskRun.Status = TaskStatusCompleted
	taskRun.Result = result
	taskRun.UpdatedAt = time.Now()
	taskRunService.taskRuns[taskRunID] = taskRun
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
