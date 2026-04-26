package memory

import (
	"context"
	"sync"
	"time"
)

type GraphMemoryStore interface {
	AddEpisode(context.Context, MemoryEpisode) error
	SearchFacts(context.Context, MemorySearchRequest) ([]MemoryFact, error)
}

type GraphMemoryMirror interface {
	SaveGraphNamespaces(context.Context, []MemoryNamespace) error
	SaveGraphEpisode(context.Context, MemoryEpisode, string, string) error
	ListAccessibleNamespaces(context.Context, MemorySearchRequest) ([]MemoryNamespace, error)
}

type MemorySearchRequest struct {
	Query                     string            `json:"query"`
	ReaderPersonID            string            `json:"readerPersonID"`
	ReaderSecurityLevelRank   int               `json:"readerSecurityLevelRank"`
	ReaderGrantedClasses      []string          `json:"readerGrantedClasses"`
	ConversationID            string            `json:"conversationID"`
	AccessibleConversationIDs []string          `json:"accessibleConversationIDs"`
	Namespaces                []MemoryNamespace `json:"namespaces"`
	Limit                     int               `json:"limit"`
}

type MemoryService struct {
	mutex       sync.RWMutex
	memoryFacts []MemoryFact
	store       GraphMemoryStore
	mirror      GraphMemoryMirror
}

func (memoryService *MemoryService) UseGraphStore(store GraphMemoryStore) {
	memoryService.store = store
}

func (memoryService *MemoryService) UseMirror(mirror GraphMemoryMirror) {
	memoryService.mirror = mirror
}

func (memoryService *MemoryService) StoreMemoryFact(memoryFact MemoryFact) {
	memoryService.mutex.Lock()
	defer memoryService.mutex.Unlock()
	memoryService.memoryFacts = append(memoryService.memoryFacts, memoryFact)
}

func (memoryService *MemoryService) AddEpisode(ctx context.Context, episode MemoryEpisode) error {
	if memoryService.mirror != nil {
		_ = memoryService.mirror.SaveGraphNamespaces(ctx, episode.Namespaces)
	}
	if memoryService.store == nil {
		return nil
	}

	errorValue := memoryService.store.AddEpisode(ctx, episode)
	if memoryService.mirror != nil {
		status := "succeeded"
		errorMessage := ""
		if errorValue != nil {
			status = "failed"
			errorMessage = errorValue.Error()
		}
		_ = memoryService.mirror.SaveGraphEpisode(ctx, episode, status, errorMessage)
	}
	return errorValue
}

func (memoryService *MemoryService) SearchMemory(ctx context.Context, request MemorySearchRequest) ([]MemoryFact, error) {
	if request.Limit <= 0 {
		request.Limit = 12
	}
	request.Namespaces = memoryService.resolveAccessibleNamespaces(ctx, request)
	if memoryService.store != nil {
		return memoryService.store.SearchFacts(ctx, request)
	}

	memoryService.mutex.RLock()
	defer memoryService.mutex.RUnlock()

	filteredMemoryFacts := []MemoryFact{}
	for _, memoryFact := range memoryService.memoryFacts {
		if canReadMemoryFact(request, memoryFact) {
			filteredMemoryFacts = append(filteredMemoryFacts, memoryFact)
		}
	}
	if len(filteredMemoryFacts) > request.Limit {
		return filteredMemoryFacts[:request.Limit], nil
	}
	return filteredMemoryFacts, nil
}

func (memoryService *MemoryService) resolveAccessibleNamespaces(ctx context.Context, request MemorySearchRequest) []MemoryNamespace {
	namespaces := append([]MemoryNamespace{}, request.Namespaces...)
	if memoryService.mirror == nil {
		return namespaces
	}
	mirrorNamespaces, errorValue := memoryService.mirror.ListAccessibleNamespaces(ctx, request)
	if errorValue != nil {
		return namespaces
	}
	return mergeNamespaces(namespaces, mirrorNamespaces)
}

func mergeNamespaces(leftNamespaces []MemoryNamespace, rightNamespaces []MemoryNamespace) []MemoryNamespace {
	seenNamespaceIDs := map[string]bool{}
	namespaces := []MemoryNamespace{}
	for _, namespace := range append(append([]MemoryNamespace{}, leftNamespaces...), rightNamespaces...) {
		if namespace.NamespaceID == "" || seenNamespaceIDs[namespace.NamespaceID] {
			continue
		}
		seenNamespaceIDs[namespace.NamespaceID] = true
		namespaces = append(namespaces, namespace)
	}
	return namespaces
}

func canReadMemoryFact(request MemorySearchRequest, memoryFact MemoryFact) bool {
	if !containsNamespace(request.Namespaces, memoryFact.NamespaceID) {
		return false
	}
	if request.ReaderSecurityLevelRank < memoryFact.SecurityLevelRank {
		return false
	}
	return containsAll(request.ReaderGrantedClasses, memoryFact.RequiredClasses)
}

func containsNamespace(namespaces []MemoryNamespace, namespaceID string) bool {
	for _, namespace := range namespaces {
		if namespace.NamespaceID == namespaceID {
			return true
		}
	}
	return false
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

func (memoryService *MemoryService) ExpireRawContent(expiresBefore time.Time, contentSegments []ContentSegment) []ContentSegment {
	activeContentSegments := []ContentSegment{}

	for _, contentSegment := range contentSegments {
		if contentSegment.ExpiresAt.After(expiresBefore) {
			activeContentSegments = append(activeContentSegments, contentSegment)
		}
	}

	return activeContentSegments
}
