package ingestion

import (
	"time"

	"blueclaw/internal/memory"
)

type MessageIngestionService struct {
	contentSegmentService *memory.ContentSegmentService
}

func NewMessageIngestionService(contentSegmentService *memory.ContentSegmentService) *MessageIngestionService {
	return &MessageIngestionService{contentSegmentService: contentSegmentService}
}

func (messageIngestionService *MessageIngestionService) IngestMessageEvent(contentSegment memory.ContentSegment) memory.ContentSegment {
	if contentSegment.OccurredAt.IsZero() {
		contentSegment.OccurredAt = time.Now()
	}
	if contentSegment.ExpiresAt.IsZero() {
		contentSegment.ExpiresAt = time.Now().Add(60 * 24 * time.Hour)
	}

	messageIngestionService.contentSegmentService.AddContentSegment(contentSegment)
	return contentSegment
}
