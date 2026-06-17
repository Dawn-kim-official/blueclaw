package adminapi

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"blueclaw/internal/identity"
	"blueclaw/internal/task"
)

type TaskMonitorHandler struct {
	TaskRunService   *task.TaskRunService
	TaskStepService  *task.TaskStepService
	TaskEventService *task.TaskEventService
	IdentityService  *identity.IdentityService
}

type taskRunListItem struct {
	task.TaskRun
	RequesterDisplayName string `json:"requesterDisplayName,omitempty"`
}

func (taskMonitorHandler TaskMonitorHandler) HandleListTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	requesterPersonID, isViewerAllowed := taskMonitorHandler.requesterScope(request)
	if !isViewerAllowed {
		writeJSON(responseWriter, http.StatusOK, []taskRunListItem{})
		return
	}
	taskRuns := selectTaskRunList(
		taskMonitorHandler.TaskRunService.ListTaskRun(),
		request.URL.Query().Get("status"),
		requesterPersonID,
		request.URL.Query().Get("limit"),
	)
	writeJSON(responseWriter, http.StatusOK, taskMonitorHandler.decorateTaskRunList(taskRuns))
}

func (taskMonitorHandler TaskMonitorHandler) HandleGetTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	taskRunID := request.URL.Query().Get("taskRunID")
	taskRun, isFound := taskMonitorHandler.TaskRunService.FindTaskRun(taskRunID)
	if !isFound {
		http.Error(responseWriter, "task run not found", http.StatusNotFound)
		return
	}
	requesterPersonID, isViewerAllowed := taskMonitorHandler.requesterScope(request)
	if !isViewerAllowed || (requesterPersonID != "" && taskRun.RequesterPersonID != requesterPersonID) {
		http.Error(responseWriter, "task run not found", http.StatusNotFound)
		return
	}

	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"taskRun":    taskMonitorHandler.decorateTaskRun(taskRun),
		"taskSteps":  taskMonitorHandler.TaskStepService.ListTaskStep(taskRunID),
		"taskEvents": taskMonitorHandler.TaskEventService.ListTaskEvent(taskRunID),
	})
}

func (taskMonitorHandler TaskMonitorHandler) requesterScope(request *http.Request) (string, bool) {
	if strings.EqualFold(strings.TrimSpace(request.URL.Query().Get("viewerIsAdmin")), "true") {
		return "", true
	}
	viewerEmail := strings.TrimSpace(request.URL.Query().Get("viewerEmail"))
	if viewerEmail == "" {
		return "", true
	}
	if taskMonitorHandler.IdentityService == nil {
		return "", false
	}
	personID, isFound := taskMonitorHandler.IdentityService.ResolvePersonIDByEmail(viewerEmail)
	if !isFound || personID == "" {
		return "", false
	}
	return personID, true
}

func (taskMonitorHandler TaskMonitorHandler) decorateTaskRunList(taskRuns []task.TaskRun) []taskRunListItem {
	decoratedTaskRuns := make([]taskRunListItem, 0, len(taskRuns))
	for _, taskRun := range taskRuns {
		decoratedTaskRuns = append(decoratedTaskRuns, taskMonitorHandler.decorateTaskRun(taskRun))
	}
	return decoratedTaskRuns
}

func (taskMonitorHandler TaskMonitorHandler) decorateTaskRun(taskRun task.TaskRun) taskRunListItem {
	return taskRunListItem{
		TaskRun:              taskRun,
		RequesterDisplayName: taskMonitorHandler.resolveRequesterDisplayName(taskRun.RequesterPersonID),
	}
}

func (taskMonitorHandler TaskMonitorHandler) resolveRequesterDisplayName(requesterPersonID string) string {
	if taskMonitorHandler.IdentityService == nil {
		return ""
	}
	return taskMonitorHandler.IdentityService.ResolvePersonDisplayName(requesterPersonID)
}

func selectTaskRunList(taskRuns []task.TaskRun, status string, requesterPersonID string, limitValue string) []task.TaskRun {
	selectedTaskRuns := filterTaskRunListByStatus(taskRuns, strings.TrimSpace(status))
	selectedTaskRuns = filterTaskRunListByRequester(selectedTaskRuns, strings.TrimSpace(requesterPersonID))
	sort.Slice(selectedTaskRuns, func(leftIndex int, rightIndex int) bool {
		return selectedTaskRuns[leftIndex].CreatedAt.After(selectedTaskRuns[rightIndex].CreatedAt)
	})
	return limitTaskRunList(selectedTaskRuns, limitValue)
}

func filterTaskRunListByStatus(taskRuns []task.TaskRun, status string) []task.TaskRun {
	if status == "" {
		return taskRuns
	}
	filteredTaskRuns := []task.TaskRun{}
	for _, taskRun := range taskRuns {
		if string(taskRun.Status) == status {
			filteredTaskRuns = append(filteredTaskRuns, taskRun)
		}
	}
	return filteredTaskRuns
}

func filterTaskRunListByRequester(taskRuns []task.TaskRun, requesterPersonID string) []task.TaskRun {
	if requesterPersonID == "" {
		return taskRuns
	}
	filteredTaskRuns := []task.TaskRun{}
	for _, taskRun := range taskRuns {
		if taskRun.RequesterPersonID == requesterPersonID {
			filteredTaskRuns = append(filteredTaskRuns, taskRun)
		}
	}
	return filteredTaskRuns
}

func limitTaskRunList(taskRuns []task.TaskRun, limitValue string) []task.TaskRun {
	limit, errorValue := strconv.Atoi(strings.TrimSpace(limitValue))
	if errorValue != nil || limit <= 0 || len(taskRuns) <= limit {
		return taskRuns
	}
	return taskRuns[:limit]
}
