package userapi

import (
	"net/http"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type TaskActionHandler struct {
	TaskRunService  *task.TaskRunService
	TaskAuthService *task.TaskAuthService
}

func (taskActionHandler TaskActionHandler) HandleCancelOwnTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	taskSessionID := request.URL.Query().Get("taskSessionID")
	taskRunID := request.URL.Query().Get("taskRunID")
	taskSession, errorValue := taskActionHandler.TaskAuthService.ResolveTaskSession(taskSessionID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusUnauthorized)
		return
	}

	taskRun, errorValue := taskActionHandler.TaskRunService.CancelTaskRun(taskRunID, taskSession.PersonID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(responseWriter, http.StatusOK, taskRun)
}
