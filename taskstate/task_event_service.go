package taskstate

import (
	"sync"
	"time"
)

type TaskEventRepository interface {
	InsertTaskEvent(TaskEvent) error
	ListTaskEvent(string) ([]TaskEvent, error)
	ListTaskEventByNameForTaskRuns([]string, string) ([]TaskEvent, error)
}

type TaskEventService struct {
	mutex                sync.RWMutex
	taskEvents           map[string][]TaskEvent
	repository           TaskEventRepository
	observerMutex        sync.RWMutex
	observers            map[string]func(RawTurnEvent)
	globalObservers      map[int]func(RawTurnEvent)
	nextGlobalObserverID int
}

func NewTaskEventService() *TaskEventService {
	return &TaskEventService{
		taskEvents:      map[string][]TaskEvent{},
		observers:       map[string]func(RawTurnEvent){},
		globalObservers: map[int]func(RawTurnEvent){},
	}
}

func (taskEventService *TaskEventService) RegisterTurnObserver(observer func(RawTurnEvent)) func() {
	taskEventService.observerMutex.Lock()
	observerID := taskEventService.nextGlobalObserverID
	taskEventService.nextGlobalObserverID++
	taskEventService.globalObservers[observerID] = observer
	taskEventService.observerMutex.Unlock()
	return func() {
		taskEventService.observerMutex.Lock()
		delete(taskEventService.globalObservers, observerID)
		taskEventService.observerMutex.Unlock()
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
	if observer := taskEventService.observers[rawTurnEvent.TaskRunID]; observer != nil {
		observer(rawTurnEvent)
	}
	for _, globalObserver := range taskEventService.globalObservers {
		globalObserver(rawTurnEvent)
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
		TaskEventID: NewIdentifier(),
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

func (taskEventService *TaskEventService) ListTaskEventByNameForTaskRuns(taskRunIDs []string, name string) []TaskEvent {
	if len(taskRunIDs) == 0 || name == "" {
		return []TaskEvent{}
	}
	if taskEventService.repository != nil {
		taskEvents, errorValue := taskEventService.repository.ListTaskEventByNameForTaskRuns(taskRunIDs, name)
		if errorValue == nil {
			return taskEvents
		}
	}
	seenTaskRunIDs := map[string]bool{}
	selectedTaskRunIDs := []string{}
	for _, taskRunID := range taskRunIDs {
		if seenTaskRunIDs[taskRunID] {
			continue
		}
		seenTaskRunIDs[taskRunID] = true
		selectedTaskRunIDs = append(selectedTaskRunIDs, taskRunID)
	}
	taskEventService.mutex.RLock()
	defer taskEventService.mutex.RUnlock()
	taskEvents := []TaskEvent{}
	for _, taskRunID := range selectedTaskRunIDs {
		for _, taskEvent := range taskEventService.taskEvents[taskRunID] {
			if taskEvent.Name == name {
				taskEvents = append(taskEvents, taskEvent)
			}
		}
	}
	return taskEvents
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
