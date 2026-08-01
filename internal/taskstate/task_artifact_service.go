package taskstate

import "sync"

type TaskArtifactRepository interface {
	InsertTaskArtifact(TaskArtifact) error
	ListTaskArtifact(string) ([]TaskArtifact, error)
}

type TaskArtifactService struct {
	mutex         sync.RWMutex
	taskArtifacts map[string][]TaskArtifact
	repository    TaskArtifactRepository
}

func NewTaskArtifactService() *TaskArtifactService {
	return &TaskArtifactService{
		taskArtifacts: map[string][]TaskArtifact{},
	}
}

func (taskArtifactService *TaskArtifactService) UseRepository(repository TaskArtifactRepository) {
	taskArtifactService.repository = repository
}

func (taskArtifactService *TaskArtifactService) AddTaskArtifact(taskArtifact TaskArtifact) {
	taskArtifactService.mutex.Lock()
	defer taskArtifactService.mutex.Unlock()
	taskArtifactService.taskArtifacts[taskArtifact.TaskRunID] = append(taskArtifactService.taskArtifacts[taskArtifact.TaskRunID], taskArtifact)
	_ = taskArtifactService.saveTaskArtifact(taskArtifact)
}

func (taskArtifactService *TaskArtifactService) AddTaskArtifactBody(taskRunID string, name string, body string) TaskArtifact {
	taskArtifact := TaskArtifact{
		TaskArtifactID: NewIdentifier(),
		TaskRunID:      taskRunID,
		Name:           name,
		Body:           body,
	}
	taskArtifactService.AddTaskArtifact(taskArtifact)
	return taskArtifact
}

func (taskArtifactService *TaskArtifactService) ListTaskArtifact(taskRunID string) []TaskArtifact {
	if taskArtifactService.repository != nil {
		taskArtifacts, errorValue := taskArtifactService.repository.ListTaskArtifact(taskRunID)
		if errorValue == nil {
			return taskArtifacts
		}
	}
	taskArtifactService.mutex.RLock()
	defer taskArtifactService.mutex.RUnlock()
	return append([]TaskArtifact{}, taskArtifactService.taskArtifacts[taskRunID]...)
}

func (taskArtifactService *TaskArtifactService) RemoveTaskRunArtifacts(taskRunID string) {
	taskArtifactService.mutex.Lock()
	defer taskArtifactService.mutex.Unlock()
	delete(taskArtifactService.taskArtifacts, taskRunID)
}

func (taskArtifactService *TaskArtifactService) saveTaskArtifact(taskArtifact TaskArtifact) error {
	if taskArtifactService.repository == nil {
		return nil
	}
	return taskArtifactService.repository.InsertTaskArtifact(taskArtifact)
}
