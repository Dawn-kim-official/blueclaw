package task

import "sync"

type TaskStepService struct {
	mutex     sync.RWMutex
	taskSteps map[string][]TaskStep
}

func NewTaskStepService() *TaskStepService {
	return &TaskStepService{
		taskSteps: map[string][]TaskStep{},
	}
}

func (taskStepService *TaskStepService) AddTaskStep(taskStep TaskStep) {
	taskStepService.mutex.Lock()
	defer taskStepService.mutex.Unlock()
	taskStepService.taskSteps[taskStep.TaskRunID] = append(taskStepService.taskSteps[taskStep.TaskRunID], taskStep)
}

func (taskStepService *TaskStepService) ListTaskStep(taskRunID string) []TaskStep {
	taskStepService.mutex.RLock()
	defer taskStepService.mutex.RUnlock()
	return append([]TaskStep{}, taskStepService.taskSteps[taskRunID]...)
}
