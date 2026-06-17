package adminapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"blueclaw/internal/identity"
	"blueclaw/internal/policy"
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

func newTestIdentityService() *identity.IdentityService {
	return identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{
			"alice@example.com": "person-1",
			"bob@example.com":   "person-2",
		},
		DisplayNameByPersonID: map[string]string{
			"person-1": "Alice",
			"person-2": "Bob",
		},
	})
}

func TestTaskMonitorHandlerScopesNonAdminViewerToOwnTaskRuns(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	aliceTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "alice task")
	bobTaskRun := taskRunService.CreateTaskRun("person-2", "conversation-2", "bob task")
	handler := TaskMonitorHandler{TaskRunService: taskRunService, IdentityService: newTestIdentityService()}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?viewerEmail=alice@example.com", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	responseBody := responseRecorder.Body.String()
	if !strings.Contains(responseBody, aliceTaskRun.TaskRunID) {
		t.Fatalf("expected alice task run, got %s", responseBody)
	}
	if strings.Contains(responseBody, bobTaskRun.TaskRunID) {
		t.Fatalf("expected bob task run to be scoped out, got %s", responseBody)
	}
}

func TestTaskMonitorHandlerAdminViewerSeesAllWithDisplayName(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunService.CreateTaskRun("person-1", "conversation-1", "alice task")
	taskRunService.CreateTaskRun("person-2", "conversation-2", "bob task")
	handler := TaskMonitorHandler{TaskRunService: taskRunService, IdentityService: newTestIdentityService()}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?viewerEmail=alice@example.com&viewerIsAdmin=true", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	responseBody := responseRecorder.Body.String()
	if strings.Count(responseBody, `"taskRunID"`) != 2 {
		t.Fatalf("expected both task runs for admin viewer, got %s", responseBody)
	}
	if !strings.Contains(responseBody, `"requesterDisplayName":"Alice"`) {
		t.Fatalf("expected requester display name, got %s", responseBody)
	}
}

func TestTaskMonitorHandlerUnknownViewerSeesNoTaskRuns(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunService.CreateTaskRun("person-1", "conversation-1", "alice task")
	handler := TaskMonitorHandler{TaskRunService: taskRunService, IdentityService: newTestIdentityService()}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?viewerEmail=stranger@example.com", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	if strings.Contains(responseRecorder.Body.String(), `"taskRunID"`) {
		t.Fatalf("expected no task runs for unknown viewer, got %s", responseRecorder.Body.String())
	}
}
