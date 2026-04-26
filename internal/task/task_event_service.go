package task

import (
	"sync"
	"time"
)

type TaskEventRepository interface {
	InsertTaskEvent(TaskEvent) error
	ListTaskEvent(string) ([]TaskEvent, error)
}

type TaskEventService struct {
	mutex      sync.RWMutex
	taskEvents map[string][]TaskEvent
	repository TaskEventRepository
}

func NewTaskEventService() *TaskEventService {
	return &TaskEventService{
		taskEvents: map[string][]TaskEvent{},
	}
}

func (taskEventService *TaskEventService) UseRepository(repository TaskEventRepository) {
	taskEventService.repository = repository
}

func (taskEventService *TaskEventService) AppendTaskEvent(taskRunID string, name string, body string) TaskEvent {
	taskEvent := TaskEvent{
		TaskEventID: newIdentifier(),
		TaskRunID:   taskRunID,
		Name:        name,
		Body:        body,
		CreatedAt:   time.Now(),
	}

	taskEventService.mutex.Lock()
	defer taskEventService.mutex.Unlock()
	taskEventService.taskEvents[taskRunID] = append(taskEventService.taskEvents[taskRunID], taskEvent)
	_ = taskEventService.saveTaskEvent(taskEvent)
	return taskEvent
}

func (taskEventService *TaskEventService) ListTaskEvent(taskRunID string) []TaskEvent {
	if taskEventService.repository != nil {
		taskEvents, errorValue := taskEventService.repository.ListTaskEvent(taskRunID)
		if errorValue == nil {
			return taskEvents
		}
	}
	taskEventService.mutex.RLock()
	defer taskEventService.mutex.RUnlock()
	return append([]TaskEvent{}, taskEventService.taskEvents[taskRunID]...)
}

func (taskEventService *TaskEventService) saveTaskEvent(taskEvent TaskEvent) error {
	if taskEventService.repository == nil {
		return nil
	}
	return taskEventService.repository.InsertTaskEvent(taskEvent)
}
