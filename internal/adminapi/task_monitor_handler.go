package adminapi

import (
	"net/http"

	"blueclaw/internal/task"
)

type TaskMonitorHandler struct {
	TaskRunService   *task.TaskRunService
	TaskStepService  *task.TaskStepService
	TaskEventService *task.TaskEventService
}

func (taskMonitorHandler TaskMonitorHandler) HandleListTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	writeJSON(responseWriter, http.StatusOK, taskMonitorHandler.TaskRunService.ListTaskRun())
}

func (taskMonitorHandler TaskMonitorHandler) HandleGetTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	taskRunID := request.URL.Query().Get("taskRunID")
	taskRun, isFound := taskMonitorHandler.TaskRunService.FindTaskRun(taskRunID)
	if !isFound {
		http.Error(responseWriter, "task run not found", http.StatusNotFound)
		return
	}

	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"taskRun":    taskRun,
		"taskSteps":  taskMonitorHandler.TaskStepService.ListTaskStep(taskRunID),
		"taskEvents": taskMonitorHandler.TaskEventService.ListTaskEvent(taskRunID),
	})
}
