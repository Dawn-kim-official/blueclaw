package adminapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"blueclaw/internal/task"
)

type TaskScheduleSummaryRepository interface {
	SummarizeActiveTaskSchedules(time.Time) (task.TaskScheduleSummary, error)
}

type TaskScheduleListRepository interface {
	ListActiveTaskSchedules(task.TaskScheduleListRequest) ([]task.TaskSchedule, error)
}

type TaskScheduleHandler struct {
	SummaryRepository TaskScheduleSummaryRepository
	ListRepository    TaskScheduleListRepository
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

func (taskScheduleHandler TaskScheduleHandler) HandleList(responseWriter http.ResponseWriter, request *http.Request) {
	if taskScheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule list repository is not configured", http.StatusServiceUnavailable)
		return
	}
	taskSchedules, errorValue := taskScheduleHandler.ListRepository.ListActiveTaskSchedules(taskScheduleListRequestFromHTTP(request))
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"schedules": taskScheduleListItems(taskSchedules),
		"count":     len(taskSchedules),
		"checkedAt": time.Now().UTC(),
	})
}

type taskScheduleListItem struct {
	TaskScheduleID    string     `json:"taskScheduleID"`
	CreatorPersonID   string     `json:"creatorPersonID"`
	ExecutionMode     string     `json:"executionMode"`
	Kind              string     `json:"kind"`
	IntervalSecond    int        `json:"intervalSecond,omitempty"`
	CronExpression    string     `json:"cronExpression,omitempty"`
	MaxRunCount       int        `json:"maxRunCount,omitempty"`
	CompletedRunCount int        `json:"completedRunCount"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	NextRunAt         *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt         *time.Time `json:"lastRunAt,omitempty"`
	ExpiresAt         *time.Time `json:"expiresAt,omitempty"`
	LastTaskRunID     string     `json:"lastTaskRunID,omitempty"`
	FailureCount      int        `json:"failureCount"`
	DeliveryChannelID string     `json:"deliveryChannelID"`
	ReplyTargetID     string     `json:"replyTargetID,omitempty"`
	PromptPreview     string     `json:"promptPreview"`
}

func taskScheduleListRequestFromHTTP(request *http.Request) task.TaskScheduleListRequest {
	queryValues := request.URL.Query()
	return task.TaskScheduleListRequest{
		ConversationID:  strings.TrimSpace(queryValues.Get("deliveryConversationID")),
		CreatorPersonID: strings.TrimSpace(queryValues.Get("creatorPersonID")),
		UnboundedOnly:   parseBoolQuery(queryValues.Get("unboundedOnly")),
		Limit:           parseLimitQuery(queryValues.Get("limit")),
		ReferenceTime:   time.Now().UTC(),
	}
}

func taskScheduleListItems(taskSchedules []task.TaskSchedule) []taskScheduleListItem {
	items := []taskScheduleListItem{}
	for _, taskSchedule := range taskSchedules {
		items = append(items, taskScheduleListItem{
			TaskScheduleID:    taskSchedule.TaskScheduleID,
			CreatorPersonID:   taskSchedule.CreatorPersonID,
			ExecutionMode:     string(taskSchedule.ExecutionMode),
			Kind:              string(taskSchedule.Kind),
			IntervalSecond:    taskSchedule.IntervalSecond,
			CronExpression:    taskSchedule.CronExpression,
			MaxRunCount:       taskSchedule.MaxRunCount,
			CompletedRunCount: taskSchedule.CompletedRunCount,
			CreatedAt:         taskSchedule.CreatedAt,
			UpdatedAt:         taskSchedule.UpdatedAt,
			NextRunAt:         taskSchedule.NextRunAt,
			LastRunAt:         taskSchedule.LastRunAt,
			ExpiresAt:         taskSchedule.ExpiresAt,
			LastTaskRunID:     taskSchedule.LastTaskRunID,
			FailureCount:      taskSchedule.FailureCount,
			DeliveryChannelID: taskSchedule.ConversationID,
			ReplyTargetID:     taskSchedule.ReplyTargetID,
			PromptPreview:     compactPromptPreview(taskSchedule.Prompt, 160),
		})
	}
	return items
}

func parseBoolQuery(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func parseLimitQuery(value string) int {
	limit, errorValue := strconv.Atoi(strings.TrimSpace(value))
	if errorValue != nil {
		return 50
	}
	return limit
}

func compactPromptPreview(value string, limit int) string {
	words := strings.Fields(value)
	preview := strings.Join(words, " ")
	if limit <= 0 || len([]rune(preview)) <= limit {
		return preview
	}
	return string([]rune(preview)[:limit]) + "..."
}
