package adminapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"blueclaw/internal/task"
)

func TestTaskMonitorHandlerFiltersAndLimitsTaskRunList(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	completedTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "summarize report")
	if _, errorValue := taskRunService.CompleteTaskRun(completedTaskRun.TaskRunID, "done"); errorValue != nil {
		t.Fatal(errorValue)
	}
	firstFailedTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "first failing task")
	if _, errorValue := taskRunService.FailTaskRun(firstFailedTaskRun.TaskRunID, "tool denied"); errorValue != nil {
		t.Fatal(errorValue)
	}
	secondFailedTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "second failing task")
	if _, errorValue := taskRunService.FailTaskRun(secondFailedTaskRun.TaskRunID, "network unreachable"); errorValue != nil {
		t.Fatal(errorValue)
	}
	handler := TaskMonitorHandler{TaskRunService: taskRunService}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?status=failed&limit=1", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	responseBody := responseRecorder.Body.String()
	if strings.Contains(responseBody, completedTaskRun.TaskRunID) {
		t.Fatalf("expected completed run to be filtered out, got %s", responseBody)
	}
	if strings.Count(responseBody, `"taskRunID"`) != 1 {
		t.Fatalf("expected a single task run, got %s", responseBody)
	}
}

func TestTaskMonitorHandlerListsAllTaskRunsWithoutQuery(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunService.CreateTaskRun("person-1", "conversation-1", "first task")
	taskRunService.CreateTaskRun("person-1", "conversation-1", "second task")
	handler := TaskMonitorHandler{TaskRunService: taskRunService}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if strings.Count(responseRecorder.Body.String(), `"taskRunID"`) != 2 {
		t.Fatalf("expected both task runs, got %s", responseRecorder.Body.String())
	}
}
