package httpserver

import (
	"fmt"
	"net/http"

	"blueclaw/internal/task"
)

type SSEHandler struct {
	TaskEventService *task.TaskEventService
}

func (sseHandler SSEHandler) HandleTaskEventStream(responseWriter http.ResponseWriter, request *http.Request) {
	flusher, isSupported := responseWriter.(http.Flusher)
	if !isSupported {
		http.Error(responseWriter, "streaming is not supported", http.StatusInternalServerError)
		return
	}

	taskRunID := request.URL.Query().Get("taskRunID")
	responseWriter.Header().Set("Content-Type", "text/event-stream")
	for _, taskEvent := range sseHandler.TaskEventService.ListTaskEvent(taskRunID) {
		_, _ = fmt.Fprintf(responseWriter, "event: %s\ndata: %s\n\n", taskEvent.Name, taskEvent.Body)
		flusher.Flush()
	}
}
