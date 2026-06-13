package runtimecontrol

import "sync/atomic"

type TaskIntakeController struct {
	quiesced atomic.Bool
}

func NewTaskIntakeController() *TaskIntakeController {
	return &TaskIntakeController{}
}

func (controller *TaskIntakeController) SetQuiesced(isQuiesced bool) {
	controller.quiesced.Store(isQuiesced)
}

func (controller *TaskIntakeController) IsQuiesced() bool {
	if controller == nil {
		return false
	}
	return controller.quiesced.Load()
}
