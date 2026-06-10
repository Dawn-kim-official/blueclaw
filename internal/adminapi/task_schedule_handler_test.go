package adminapi

import (
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
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task-schedules?deliveryConversationID=channel-1&unboundedOnly=true&includeExpired=true&page=2&pageSize=5", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleList(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if repository.request.ConversationID != "channel-1" || !repository.request.UnboundedOnly || !repository.request.IncludeExpired || repository.request.Page != 2 || repository.request.PageSize != 5 {
		t.Fatalf("expected query parameters to reach repository, got %+v", repository.request)
	}
	responseBody := responseRecorder.Body.String()
	for _, expectedFragment := range []string{`"taskScheduleID":"schedule-1"`, `"deliveryChannelID":"channel-1"`, `"totalCount":1`, `"page":2`, `"pageSize":5`, `"promptPreview":"`} {
		if !strings.Contains(responseBody, expectedFragment) {
			t.Fatalf("expected response to contain %s, got %s", expectedFragment, responseBody)
		}
	}
	if strings.Contains(responseBody, longPrompt) {
		t.Fatalf("expected prompt to be compacted, got %s", responseBody)
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

func (repository *taskScheduleListRepositoryStub) ListTaskSchedules(request task.TaskScheduleListRequest) (task.TaskScheduleListResult, error) {
	repository.request = request
	return task.TaskScheduleListResult{TaskSchedules: repository.taskSchedules, TotalCount: len(repository.taskSchedules), Page: request.Page, PageSize: request.PageSize}, nil
}
