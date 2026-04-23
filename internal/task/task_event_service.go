package task

import (
	"sync"
	"time"
)

type TaskEventService struct {
	mutex      sync.RWMutex
	taskEvents map[string][]TaskEvent
}

func NewTaskEventService() *TaskEventService {
	return &TaskEventService{
		taskEvents: map[string][]TaskEvent{},
	}
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
	return taskEvent
}

func (taskEventService *TaskEventService) ListTaskEvent(taskRunID string) []TaskEvent {
	taskEventService.mutex.RLock()
	defer taskEventService.mutex.RUnlock()
	return append([]TaskEvent{}, taskEventService.taskEvents[taskRunID]...)
}
