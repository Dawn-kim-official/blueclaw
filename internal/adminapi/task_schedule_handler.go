package adminapi

import (
	"net/http"
	"time"

	"blueclaw/internal/task"
)

type TaskScheduleSummaryRepository interface {
	SummarizeActiveTaskSchedules(time.Time) (task.TaskScheduleSummary, error)
}

type TaskScheduleHandler struct {
	SummaryRepository TaskScheduleSummaryRepository
}

func (taskScheduleHandler TaskScheduleHandler) HandleSummary(responseWriter http.ResponseWriter, request *http.Request) {
	if taskScheduleHandler.SummaryRepository == nil {
		http.Error(responseWriter, "task schedule summary repository is not configured", http.StatusServiceUnavailable)
		return
	}
	summary, errorValue := taskScheduleHandler.SummaryRepository.SummarizeActiveTaskSchedules(time.Now().UTC())
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, summary)
}
