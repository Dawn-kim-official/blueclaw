package memory

import (
	"sync"
	"time"
)

type MemoryService struct {
	mutex         sync.RWMutex
	memoryRecords []MemoryRecord
}

func (memoryService *MemoryService) StoreDerivedMemory(memoryRecord MemoryRecord) {
	memoryService.mutex.Lock()
	defer memoryService.mutex.Unlock()
	memoryService.memoryRecords = append(memoryService.memoryRecords, memoryRecord)
}

func (memoryService *MemoryService) SearchAccessibleMemory(scopePersonID string) []MemoryRecord {
	memoryService.mutex.RLock()
	defer memoryService.mutex.RUnlock()

	filteredMemoryRecords := []MemoryRecord{}
	for _, memoryRecord := range memoryService.memoryRecords {
		if memoryRecord.ScopePersonID == "" || memoryRecord.ScopePersonID == scopePersonID {
			filteredMemoryRecords = append(filteredMemoryRecords, memoryRecord)
		}
	}

	return filteredMemoryRecords
}

func (memoryService *MemoryService) ExpireRawContent(expiresBefore time.Time, contentSegments []ContentSegment) []ContentSegment {
	activeContentSegments := []ContentSegment{}

	for _, contentSegment := range contentSegments {
		if contentSegment.ExpiresAt.After(expiresBefore) {
			activeContentSegments = append(activeContentSegments, contentSegment)
		}
	}

	return activeContentSegments
}
