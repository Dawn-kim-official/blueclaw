package memory

import "sync"

type ContentSegmentService struct {
	mutex           sync.RWMutex
	contentSegments []ContentSegment
}

func (contentSegmentService *ContentSegmentService) AddContentSegment(contentSegment ContentSegment) {
	contentSegmentService.mutex.Lock()
	defer contentSegmentService.mutex.Unlock()
	contentSegmentService.contentSegments = append(contentSegmentService.contentSegments, contentSegment)
}

func (contentSegmentService *ContentSegmentService) ListContentSegment() []ContentSegment {
	contentSegmentService.mutex.RLock()
	defer contentSegmentService.mutex.RUnlock()
	return append([]ContentSegment{}, contentSegmentService.contentSegments...)
}
