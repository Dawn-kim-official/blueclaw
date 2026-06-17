package task

import (
	"sync"
	"time"
)

type TaskEventRepository interface {
	InsertTaskEvent(TaskEvent) error
	ListTaskEvent(string) ([]TaskEvent, error)
}

type RawTurnEvent struct {
	TaskRunID string
	Name      string
	Body      string
}

type TaskEventService struct {
	mutex         sync.RWMutex
	taskEvents    map[string][]TaskEvent
	repository    TaskEventRepository
	observerMutex sync.RWMutex
	observers     map[string]func(RawTurnEvent)
}

func NewTaskEventService() *TaskEventService {
	return &TaskEventService{
		taskEvents: map[string][]TaskEvent{},
		observers:  map[string]func(RawTurnEvent){},
	}
}

func (taskEventService *TaskEventService) RegisterTaskRunObserver(taskRunID string, observer func(RawTurnEvent)) func() {
	taskEventService.observerMutex.Lock()
	taskEventService.observers[taskRunID] = observer
	taskEventService.observerMutex.Unlock()
	return func() {
		taskEventService.observerMutex.Lock()
		delete(taskEventService.observers, taskRunID)
		taskEventService.observerMutex.Unlock()
	}
}

func (taskEventService *TaskEventService) notifyTaskRunObserver(rawTurnEvent RawTurnEvent) {
	taskEventService.observerMutex.RLock()
	defer taskEventService.observerMutex.RUnlock()
	observer := taskEventService.observers[rawTurnEvent.TaskRunID]
	if observer != nil {
		observer(rawTurnEvent)
	}
}

func (taskEventService *TaskEventService) UseRepository(repository TaskEventRepository) {
	taskEventService.repository = repository
}

func (taskEventService *TaskEventService) AppendTaskEvent(taskRunID string, name string, body string) TaskEvent {
	taskEvent, _ := taskEventService.AppendTaskEventWithError(taskRunID, name, body)
	return taskEvent
}

func (taskEventService *TaskEventService) AppendTaskEventWithError(taskRunID string, name string, body string) (TaskEvent, error) {
	taskEvent := TaskEvent{
		TaskEventID: newIdentifier(),
		TaskRunID:   taskRunID,
		Name:        name,
		Body:        body,
		CreatedAt:   time.Now(),
	}

	taskEventService.mutex.Lock()
	taskEventService.taskEvents[taskRunID] = append(taskEventService.taskEvents[taskRunID], taskEvent)
	saveError := taskEventService.saveTaskEvent(taskEvent)
	taskEventService.mutex.Unlock()

	taskEventService.notifyTaskRunObserver(RawTurnEvent{TaskRunID: taskRunID, Name: name, Body: body})
	return taskEvent, saveError
}

func (taskEventService *TaskEventService) RecordTaskEvent(taskEvent TaskEvent) {
	taskEventService.mutex.Lock()
	defer taskEventService.mutex.Unlock()
	taskEventService.taskEvents[taskEvent.TaskRunID] = append(taskEventService.taskEvents[taskEvent.TaskRunID], taskEvent)
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

func (taskEventService *TaskEventService) RemoveTaskRunEvents(taskRunID string) {
	taskEventService.mutex.Lock()
	defer taskEventService.mutex.Unlock()
	delete(taskEventService.taskEvents, taskRunID)
}

func (taskEventService *TaskEventService) saveTaskEvent(taskEvent TaskEvent) error {
	if taskEventService.repository == nil {
		return nil
	}
	return taskEventService.repository.InsertTaskEvent(taskEvent)
}
