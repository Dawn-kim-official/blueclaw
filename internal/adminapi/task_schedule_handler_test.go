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

type taskScheduleSummaryRepositoryStub struct {
	summary task.TaskScheduleSummary
}

func (repository taskScheduleSummaryRepositoryStub) SummarizeActiveTaskSchedules(time.Time) (task.TaskScheduleSummary, error) {
	return repository.summary, nil
}
