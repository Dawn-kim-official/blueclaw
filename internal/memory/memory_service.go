package memory

import (
	"sync"
	"time"
)

type MemoryRecordRepository interface {
	SaveMemoryRecord(MemoryRecord) error
	SearchMemory(MemorySearchRequest) ([]MemoryRecord, error)
	SearchAccessibleMemory(string) ([]MemoryRecord, error)
}

type MemorySearchRequest struct {
	ReaderPersonID            string
	ReaderSecurityLevelRank   int
	ReaderGrantedClasses      []string
	ConversationID            string
	AccessibleConversationIDs []string
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

func (memoryService *MemoryService) SearchMemory(request MemorySearchRequest) []MemoryRecord {
	if memoryService.repository != nil {
		memoryRecords, errorValue := memoryService.repository.SearchMemory(request)
		if errorValue == nil {
			return memoryRecords
		}
	}
	memoryService.mutex.RLock()
	defer memoryService.mutex.RUnlock()

	filteredMemoryRecords := []MemoryRecord{}
	for _, memoryRecord := range memoryService.memoryRecords {
		if canReadMemoryRecord(request, memoryRecord) {
			filteredMemoryRecords = append(filteredMemoryRecords, memoryRecord)
		}
	}

	return filteredMemoryRecords
}

func canReadMemoryRecord(request MemorySearchRequest, memoryRecord MemoryRecord) bool {
	switch memoryRecord.ScopeType {
	case "", ScopeTypeUser:
		return memoryRecord.ScopePersonID == "" || memoryRecord.ScopePersonID == request.ReaderPersonID
	case ScopeTypeWorkspace:
		return canReadSecurityLabel(request, memoryRecord)
	case ScopeTypeConversation:
		return containsString(accessibleConversationIDs(request), memoryRecord.ScopeConversationID) && canReadSecurityLabel(request, memoryRecord)
	default:
		return false
	}
}

func accessibleConversationIDs(request MemorySearchRequest) []string {
	values := append([]string{}, request.AccessibleConversationIDs...)
	if request.ConversationID != "" && !containsString(values, request.ConversationID) {
		values = append(values, request.ConversationID)
	}
	return values
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func canReadSecurityLabel(request MemorySearchRequest, memoryRecord MemoryRecord) bool {
	if request.ReaderSecurityLevelRank < memoryRecord.SecurityLevelRank {
		return false
	}
	return containsAll(request.ReaderGrantedClasses, memoryRecord.RequiredClasses)
}

func containsAll(grantedClasses []string, requiredClasses []string) bool {
	grantedSet := map[string]bool{}
	for _, grantedClass := range grantedClasses {
		grantedSet[grantedClass] = true
	}
	for _, requiredClass := range requiredClasses {
		if !grantedSet[requiredClass] {
			return false
		}
	}
	return true
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
