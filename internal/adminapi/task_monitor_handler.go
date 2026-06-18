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
	query := request.URL.Query()
	filteredTaskRuns := selectTaskRunList(
		taskMonitorHandler.TaskRunService.ListTaskRun(),
		query.Get("status"),
		requesterPersonID,
	)
	pagedTaskRuns := taskMonitorHandler.decorateTaskRunList(
		pageTaskRunList(filteredTaskRuns, query.Get("offset"), query.Get("limit")),
	)

	if !isTotalRequested(query.Get("includeTotal")) {
		writeJSON(responseWriter, http.StatusOK, pagedTaskRuns)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"taskRuns":   pagedTaskRuns,
		"totalCount": len(filteredTaskRuns),
	})
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

func selectTaskRunList(taskRuns []task.TaskRun, status string, requesterPersonID string) []task.TaskRun {
	selectedTaskRuns := filterTaskRunListByStatus(taskRuns, strings.TrimSpace(status))
	selectedTaskRuns = filterTaskRunListByRequester(selectedTaskRuns, strings.TrimSpace(requesterPersonID))
	sort.Slice(selectedTaskRuns, func(leftIndex int, rightIndex int) bool {
		return selectedTaskRuns[leftIndex].CreatedAt.After(selectedTaskRuns[rightIndex].CreatedAt)
	})
	return selectedTaskRuns
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

func pageTaskRunList(taskRuns []task.TaskRun, offsetValue string, limitValue string) []task.TaskRun {
	offset := parseNonNegativeInteger(offsetValue)
	if offset >= len(taskRuns) {
		return []task.TaskRun{}
	}
	windowedTaskRuns := taskRuns[offset:]

	limit := parseNonNegativeInteger(limitValue)
	if limit <= 0 || len(windowedTaskRuns) <= limit {
		return windowedTaskRuns
	}
	return windowedTaskRuns[:limit]
}

func parseNonNegativeInteger(value string) int {
	parsed, errorValue := strconv.Atoi(strings.TrimSpace(value))
	if errorValue != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func isTotalRequested(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}
