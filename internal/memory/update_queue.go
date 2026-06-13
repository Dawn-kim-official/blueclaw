package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"
)

const defaultMemoryUpdateQueueSize = 128

type MemoryUpdateJob struct {
	JobID           string
	Namespace       MemoryNamespace
	Content         string
	Platform        string
	ConversationID  string
	SenderPersonID  string
	SourceReference string
	OccurredAt      time.Time
	SkipMarkdown    bool
}

type MemoryUpdateAccepted struct {
	Accepted         bool   `json:"accepted"`
	JobID            string `json:"jobID,omitempty"`
	Status           string `json:"status"`
	Durability       string `json:"durability"`
	GraphitiStatus   string `json:"graphitiStatus,omitempty"`
	MarkdownUpdated  bool   `json:"markdownUpdated,omitempty"`
	FailureCode      string `json:"failureCode,omitempty"`
	FailureComponent string `json:"failureComponent,omitempty"`
}

type MemoryUpdateEnqueuer interface {
	Enqueue(MemoryUpdateJob) (MemoryUpdateAccepted, error)
}

type MemoryUpdateProcessor struct {
	memoryService *MemoryService
	markdownStore *MarkdownStore
}

type MemoryUpdateResult struct {
	GraphitiSucceeded bool
	GraphitiError     string
	MarkdownUpdated   bool
	MarkdownError     string
}

type BackgroundMemoryUpdateQueue struct {
	processor MemoryUpdateProcessor
	jobs      chan MemoryUpdateJob
	logger    *slog.Logger
}

func NewMemoryUpdateProcessor(memoryService *MemoryService, markdownStore *MarkdownStore) MemoryUpdateProcessor {
	return MemoryUpdateProcessor{
		memoryService: memoryService,
		markdownStore: markdownStore,
	}
}

func NewBackgroundMemoryUpdateQueue(processor MemoryUpdateProcessor, logger *slog.Logger) *BackgroundMemoryUpdateQueue {
	return &BackgroundMemoryUpdateQueue{
		processor: processor,
		jobs:      make(chan MemoryUpdateJob, defaultMemoryUpdateQueueSize),
		logger:    loggerOrDefault(logger),
	}
}

func (queue *BackgroundMemoryUpdateQueue) Start(ctx context.Context) {
	if queue == nil {
		return
	}
	go queue.run(ctx)
}

func (queue *BackgroundMemoryUpdateQueue) Enqueue(job MemoryUpdateJob) (MemoryUpdateAccepted, error) {
	if queue == nil {
		return MemoryUpdateAccepted{}, errors.New("memory update queue is unavailable")
	}
	normalizedJob := normalizeMemoryUpdateJob(job)
	// TODO: Replace the volatile in-process memory update queue with a durable queue.
	select {
	case queue.jobs <- normalizedJob:
		return MemoryUpdateAccepted{
			Accepted:       true,
			JobID:          normalizedJob.JobID,
			Status:         "queued_volatile",
			Durability:     "volatile",
			GraphitiStatus: "queued",
		}, nil
	default:
		return MemoryUpdateAccepted{}, errors.New("memory update queue is full")
	}
}

func (queue *BackgroundMemoryUpdateQueue) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-queue.jobs:
			result := queue.processor.Process(context.WithoutCancel(ctx), job)
			queue.logResult(job, result)
		}
	}
}

func (queue *BackgroundMemoryUpdateQueue) logResult(job MemoryUpdateJob, result MemoryUpdateResult) {
	if result.GraphitiError != "" {
		queue.logger.Warn("memory.graphiti_update_failed", slog.String("jobID", job.JobID), slog.String("error", result.GraphitiError))
	} else if result.GraphitiSucceeded {
		queue.logger.Info("memory.graphiti_update_succeeded", slog.String("jobID", job.JobID), slog.String("namespaceID", job.Namespace.NamespaceID))
	}
	if result.MarkdownError != "" {
		queue.logger.Warn("memory.markdown_update_failed", slog.String("jobID", job.JobID), slog.String("error", result.MarkdownError))
	} else if result.MarkdownUpdated {
		queue.logger.Info("memory.markdown_update_succeeded", slog.String("jobID", job.JobID), slog.String("namespaceID", job.Namespace.NamespaceID))
	}
}

func (processor MemoryUpdateProcessor) Process(ctx context.Context, job MemoryUpdateJob) MemoryUpdateResult {
	normalizedJob := normalizeMemoryUpdateJob(job)
	result := MemoryUpdateResult{}
	if processor.memoryService != nil {
		if _, errorValue := processor.memoryService.AddEpisode(ctx, memoryEpisodeFromUpdateJob(normalizedJob)); errorValue != nil {
			result.GraphitiError = errorValue.Error()
		} else {
			result.GraphitiSucceeded = true
		}
	}
	if processor.markdownStore != nil && normalizedJob.Namespace.ScopeType == ScopeTypeUser && !normalizedJob.SkipMarkdown {
		isUpdated, errorValue := processor.markdownStore.MergePersonMemory(ctx, normalizedJob.Namespace.ScopePersonID, normalizedJob.Content)
		if errorValue != nil {
			result.MarkdownError = errorValue.Error()
		} else {
			result.MarkdownUpdated = isUpdated
		}
	}
	return result
}

func PrepareMemoryUpdateJob(job MemoryUpdateJob) MemoryUpdateJob {
	return normalizeMemoryUpdateJob(job)
}

func memoryEpisodeFromUpdateJob(job MemoryUpdateJob) MemoryEpisode {
	occurredAt := job.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return MemoryEpisode{
		EpisodeID:       job.JobID,
		Platform:        job.Platform,
		ConversationID:  job.ConversationID,
		SenderPersonID:  job.SenderPersonID,
		Prompt:          job.Content,
		OccurredAt:      occurredAt,
		Namespaces:      []MemoryNamespace{job.Namespace},
		Source:          "memory.remember",
		SourceReference: job.SourceReference,
	}
}

func normalizeMemoryUpdateJob(job MemoryUpdateJob) MemoryUpdateJob {
	job.JobID = firstNonEmptyMemoryString(strings.TrimSpace(job.JobID), newMemoryUpdateJobID())
	job.Content = strings.TrimSpace(job.Content)
	if job.OccurredAt.IsZero() {
		job.OccurredAt = time.Now().UTC()
	}
	return job
}

func newMemoryUpdateJobID() string {
	buffer := make([]byte, 12)
	if _, errorValue := rand.Read(buffer); errorValue == nil {
		return "memory-update-" + hex.EncodeToString(buffer)
	}
	return "memory-update-" + time.Now().UTC().Format("20060102150405.000000000")
}

func loggerOrDefault(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.Default()
}

func firstNonEmptyMemoryString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
