package taskstate

import "sync"

type TaskStepRepository interface {
	InsertTaskStep(TaskStep) error
	ListTaskStep(string) ([]TaskStep, error)
}

type TaskStepService struct {
	mutex      sync.RWMutex
	taskSteps  map[string][]TaskStep
	repository TaskStepRepository
}

func NewTaskStepService() *TaskStepService {
	return &TaskStepService{
		taskSteps: map[string][]TaskStep{},
	}
}

func (taskStepService *TaskStepService) UseRepository(repository TaskStepRepository) {
	taskStepService.repository = repository
}

func (taskStepService *TaskStepService) AddTaskStep(taskStep TaskStep) {
	taskStepService.mutex.Lock()
	defer taskStepService.mutex.Unlock()
	taskSteps := taskStepService.taskSteps[taskStep.TaskRunID]
	for index, existingTaskStep := range taskSteps {
		if existingTaskStep.TaskStepID == taskStep.TaskStepID {
			taskSteps[index] = taskStep
			taskStepService.taskSteps[taskStep.TaskRunID] = taskSteps
			_ = taskStepService.saveTaskStep(taskStep)
			return
		}
	}
	taskStepService.taskSteps[taskStep.TaskRunID] = append(taskSteps, taskStep)
	_ = taskStepService.saveTaskStep(taskStep)
}

func (taskStepService *TaskStepService) ListTaskStep(taskRunID string) []TaskStep {
	if taskStepService.repository != nil {
		taskSteps, errorValue := taskStepService.repository.ListTaskStep(taskRunID)
		if errorValue == nil {
			return taskSteps
		}
	}
	taskStepService.mutex.RLock()
	defer taskStepService.mutex.RUnlock()
	return append([]TaskStep{}, taskStepService.taskSteps[taskRunID]...)
}

func (taskStepService *TaskStepService) RemoveTaskRunSteps(taskRunID string) {
	taskStepService.mutex.Lock()
	defer taskStepService.mutex.Unlock()
	delete(taskStepService.taskSteps, taskRunID)
}

func (taskStepService *TaskStepService) saveTaskStep(taskStep TaskStep) error {
	if taskStepService.repository == nil {
		return nil
	}
	return taskStepService.repository.InsertTaskStep(taskStep)
}
