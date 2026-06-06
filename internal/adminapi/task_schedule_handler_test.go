package adminapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/task"
)

func TestTaskScheduleHandlerReturnsSummary(t *testing.T) {
	nextRunAt := time.Date(2026, 6, 6, 3, 0, 0, 0, time.UTC)
	handler := TaskScheduleHandler{
		SummaryRepository: taskScheduleSummaryRepositoryStub{
			summary: task.TaskScheduleSummary{
				ActiveCount:       3,
				UnboundedCount:    1,
				IntervalCount:     2,
				EarliestNextRunAt: &nextRunAt,
				LatestNextRunAt:   &nextRunAt,
				CheckedAt:         nextRunAt,
			},
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task-schedules/summary", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleSummary(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	responseBody := responseRecorder.Body.String()
	for _, expectedFragment := range []string{`"activeCount":3`, `"unboundedCount":1`, `"intervalCount":2`} {
		if !strings.Contains(responseBody, expectedFragment) {
			t.Fatalf("expected response to contain %s, got %s", expectedFragment, responseBody)
		}
	}
	for _, forbiddenFragment := range []string{"prompt", "creator", "conversation", "replyTarget"} {
		if strings.Contains(responseBody, forbiddenFragment) {
			t.Fatalf("expected summary to avoid private schedule details, got %s", responseBody)
		}
	}
}

func TestTaskScheduleHandlerListsActiveSchedules(t *testing.T) {
	nextRunAt := time.Date(2026, 6, 6, 3, 0, 0, 0, time.UTC)
	longPrompt := strings.Repeat("예약 메시지 ", 40)
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{{
			TaskScheduleID:   "schedule-1",
			CreatorPersonID:  "person-1",
			Prompt:           longPrompt,
			ExecutionMode:    task.TaskScheduleExecutionModeAgent,
			Kind:             task.TaskScheduleKindCron,
			CronExpression:   "0 * * * *",
			NextRunAt:        &nextRunAt,
			CreatedAt:        nextRunAt.Add(-time.Hour),
			UpdatedAt:        nextRunAt.Add(-time.Minute),
			ConversationID:   "channel-1",
			ReplyTargetID:    "post-1",
			AgentProfileName: "default",
		}},
	}
	handler := TaskScheduleHandler{ListRepository: repository}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task-schedules?deliveryConversationID=channel-1&unboundedOnly=true&limit=5", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleList(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if repository.request.ConversationID != "channel-1" || !repository.request.UnboundedOnly || repository.request.Limit != 5 {
		t.Fatalf("expected query parameters to reach repository, got %+v", repository.request)
	}
	responseBody := responseRecorder.Body.String()
	for _, expectedFragment := range []string{`"taskScheduleID":"schedule-1"`, `"deliveryChannelID":"channel-1"`, `"promptPreview":"`} {
		if !strings.Contains(responseBody, expectedFragment) {
			t.Fatalf("expected response to contain %s, got %s", expectedFragment, responseBody)
		}
	}
	if strings.Contains(responseBody, longPrompt) {
		t.Fatalf("expected prompt to be compacted, got %s", responseBody)
	}
}

func TestTaskScheduleHandlerListsDeliveryGroupsWithoutPromptDetails(t *testing.T) {
	nextRunAt := time.Date(2026, 6, 6, 3, 0, 0, 0, time.UTC)
	repository := &taskScheduleDeliveryGroupRepositoryStub{
		groups: []task.TaskScheduleDeliveryGroup{{
			ConversationID:  "channel-1",
			ActiveCount:     12,
			UnboundedCount:  12,
			LatestCreatedAt: &nextRunAt,
			LatestNextRunAt: &nextRunAt,
		}},
	}
	handler := TaskScheduleHandler{DeliveryGroupRepository: repository}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task-schedules/delivery-groups?unboundedOnly=true&limit=5", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleDeliveryGroups(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !repository.request.UnboundedOnly || repository.request.Limit != 5 {
		t.Fatalf("expected query parameters to reach repository, got %+v", repository.request)
	}
	responseBody := responseRecorder.Body.String()
	for _, expectedFragment := range []string{`"deliveryConversationID":"channel-1"`, `"activeCount":12`, `"unboundedCount":12`} {
		if !strings.Contains(responseBody, expectedFragment) {
			t.Fatalf("expected response to contain %s, got %s", expectedFragment, responseBody)
		}
	}
	for _, forbiddenFragment := range []string{"prompt", "replyTarget"} {
		if strings.Contains(responseBody, forbiddenFragment) {
			t.Fatalf("expected delivery groups to avoid private schedule details, got %s", responseBody)
		}
	}
}

func TestTaskScheduleHandlerMaintenanceCancelDefaultsToDryRun(t *testing.T) {
	repository := &taskScheduleMaintenanceRepositoryStub{
		result: task.TaskScheduleMaintenanceCancelResult{
			DryRun:               true,
			MatchedScheduleCount: 2,
			ScheduleIDs:          []string{"schedule-1", "schedule-2"},
		},
	}
	handler := TaskScheduleHandler{MaintenanceRepository: repository}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/task-schedules/maintenance-cancel", bytes.NewReader([]byte(`{
		"deliveryConversationIDs":["channel-1"],
		"deliveryConversationIDPrefix":"thread:channel-1:",
		"includeScheduleChildren":true,
		"unboundedOnly":true
	}`)))
	responseRecorder := httptest.NewRecorder()

	handler.HandleMaintenanceCancel(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !repository.request.DryRun || !repository.request.IncludeScheduleChildren || !repository.request.UnboundedOnly {
		t.Fatalf("expected maintenance options to reach repository, got %+v", repository.request)
	}
	if repository.request.DeliveryConversationIDPrefix != "thread:channel-1:" || len(repository.request.DeliveryConversationIDs) != 1 {
		t.Fatalf("expected target conversations, got %+v", repository.request)
	}
}

func TestTaskScheduleHandlerMaintenanceCancelReturnsNotFound(t *testing.T) {
	repository := &taskScheduleMaintenanceRepositoryStub{
		result: task.TaskScheduleMaintenanceCancelResult{DryRun: true},
	}
	handler := TaskScheduleHandler{MaintenanceRepository: repository}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/task-schedules/maintenance-cancel", bytes.NewReader([]byte(`{}`)))
	responseRecorder := httptest.NewRecorder()

	handler.HandleMaintenanceCancel(responseRecorder, request)

	if responseRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected not found response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"status":"not_found"`) {
		t.Fatalf("expected not_found body, got %s", responseRecorder.Body.String())
	}
}

type taskScheduleSummaryRepositoryStub struct {
	summary task.TaskScheduleSummary
}

func (repository taskScheduleSummaryRepositoryStub) SummarizeActiveTaskSchedules(time.Time) (task.TaskScheduleSummary, error) {
	return repository.summary, nil
}

type taskScheduleListRepositoryStub struct {
	request       task.TaskScheduleListRequest
	taskSchedules []task.TaskSchedule
}

func (repository *taskScheduleListRepositoryStub) ListActiveTaskSchedules(request task.TaskScheduleListRequest) ([]task.TaskSchedule, error) {
	repository.request = request
	return repository.taskSchedules, nil
}

type taskScheduleDeliveryGroupRepositoryStub struct {
	request task.TaskScheduleDeliveryGroupRequest
	groups  []task.TaskScheduleDeliveryGroup
}

func (repository *taskScheduleDeliveryGroupRepositoryStub) ListActiveTaskScheduleDeliveryGroups(request task.TaskScheduleDeliveryGroupRequest) ([]task.TaskScheduleDeliveryGroup, error) {
	repository.request = request
	return repository.groups, nil
}

type taskScheduleMaintenanceRepositoryStub struct {
	request task.TaskScheduleMaintenanceCancelRequest
	result  task.TaskScheduleMaintenanceCancelResult
}

func (repository *taskScheduleMaintenanceRepositoryStub) MaintenanceCancelTaskSchedules(request task.TaskScheduleMaintenanceCancelRequest) (task.TaskScheduleMaintenanceCancelResult, error) {
	repository.request = request
	return repository.result, nil
}
