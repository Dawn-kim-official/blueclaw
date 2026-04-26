package memory

import (
	"sync"
	"time"
)

type MemoryRecordRepository interface {
	SaveMemoryRecord(MemoryRecord) error
	SearchAccessibleMemory(string) ([]MemoryRecord, error)
}

type MemoryService struct {
	mutex         sync.RWMutex
	memoryRecords []MemoryRecord
	repository    MemoryRecordRepository
}

func (memoryService *MemoryService) UseRepository(repository MemoryRecordRepository) {
	memoryService.repository = repository
}

func (memoryService *MemoryService) StoreDerivedMemory(memoryRecord MemoryRecord) {
	memoryService.mutex.Lock()
	defer memoryService.mutex.Unlock()
	memoryService.memoryRecords = append(memoryService.memoryRecords, memoryRecord)
	_ = memoryService.saveMemoryRecord(memoryRecord)
}

func (memoryService *MemoryService) SearchAccessibleMemory(scopePersonID string) []MemoryRecord {
	if memoryService.repository != nil {
		memoryRecords, errorValue := memoryService.repository.SearchAccessibleMemory(scopePersonID)
		if errorValue == nil {
			return memoryRecords
		}
	}
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

func (memoryService *MemoryService) saveMemoryRecord(memoryRecord MemoryRecord) error {
	if memoryService.repository == nil {
		return nil
	}
	return memoryService.repository.SaveMemoryRecord(memoryRecord)
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
