package integration

import (
	"testing"
	"time"

	"blueclaw/internal/memory"
)

func TestRetentionExpiration(t *testing.T) {
	memoryService := memory.MemoryService{}
	contentSegments := []memory.ContentSegment{
		{
			ContentSegmentID:  "expired",
			ExpiresAt:         time.Now().Add(-time.Hour),
			ContentCiphertext: []byte("expired"),
		},
		{
			ContentSegmentID:  "active",
			ExpiresAt:         time.Now().Add(time.Hour),
			ContentCiphertext: []byte("active"),
		},
	}

	activeContentSegments := memoryService.ExpireRawContent(time.Now(), contentSegments)
	if len(activeContentSegments) != 1 {
		t.Fatalf("expected one active content segment, got %d", len(activeContentSegments))
	}
	if activeContentSegments[0].ContentSegmentID != "active" {
		t.Fatalf("expected active content segment to remain, got %s", activeContentSegments[0].ContentSegmentID)
	}
}
