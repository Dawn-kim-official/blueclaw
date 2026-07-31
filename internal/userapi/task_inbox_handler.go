package userapi

import (
	"net/http"

	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

type TaskInboxHandler struct {
	TaskRunService  *task.TaskRunService
	TaskStepService *task.TaskStepService
	TaskAuthService *task.TaskAuthService
}

func (taskInboxHandler TaskInboxHandler) HandleListOwnTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	taskSessionID := request.URL.Query().Get("taskSessionID")
	taskSession, errorValue := taskInboxHandler.TaskAuthService.ResolveTaskSession(taskSessionID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusUnauthorized)
		return
	}

	writeJSON(responseWriter, http.StatusOK, taskInboxHandler.TaskRunService.ListTaskRunByPersonID(taskSession.PersonID))
}

func (taskInboxHandler TaskInboxHandler) HandleGetOwnTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	taskSessionID := request.URL.Query().Get("taskSessionID")
	taskRunID := request.URL.Query().Get("taskRunID")
	taskRun, errorValue := taskInboxHandler.TaskAuthService.AuthorizeTaskAccess(taskSessionID, taskRunID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusUnauthorized)
		return
	}

	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"taskRun":   taskRun,
		"taskSteps": taskInboxHandler.TaskStepService.ListTaskStep(taskRunID),
	})
}
