package adminapi

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"blueclaw/internal/task"
)

type TaskMonitorHandler struct {
	TaskRunService   *task.TaskRunService
	TaskStepService  *task.TaskStepService
	TaskEventService *task.TaskEventService
}

func (taskMonitorHandler TaskMonitorHandler) HandleListTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	taskRuns := selectTaskRunList(
		taskMonitorHandler.TaskRunService.ListTaskRun(),
		request.URL.Query().Get("status"),
		request.URL.Query().Get("limit"),
	)
	writeJSON(responseWriter, http.StatusOK, taskRuns)
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

func selectTaskRunList(taskRuns []task.TaskRun, status string, limitValue string) []task.TaskRun {
	selectedTaskRuns := filterTaskRunListByStatus(taskRuns, strings.TrimSpace(status))
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

func limitTaskRunList(taskRuns []task.TaskRun, limitValue string) []task.TaskRun {
	limit, errorValue := strconv.Atoi(strings.TrimSpace(limitValue))
	if errorValue != nil || limit <= 0 || len(taskRuns) <= limit {
		return taskRuns
	}
	return taskRuns[:limit]
}
