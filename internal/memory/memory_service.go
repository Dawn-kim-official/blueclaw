package memory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"blueclaw/internal/access"
	"blueclaw/internal/policy"
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
	Query                     string   `json:"query"`
	ReaderPersonID            string   `json:"readerPersonID"`
	ReaderCircles             []string `json:"readerCircles"`
	ResourceAccessRules       []policy.ResourceAccessPolicy
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
		memoryFacts, errorValue := memoryService.store.SearchFacts(ctx, request)
		if errorValue != nil {
			return nil, errorValue
		}
		return filterReadableMemoryFacts(request, memoryFacts), nil
	}

	memoryService.mutex.RLock()
	defer memoryService.mutex.RUnlock()

	filteredMemoryFacts := filterReadableMemoryFacts(request, memoryService.memoryFacts)
	rankedMemoryFacts := rankMemoryFacts(filteredMemoryFacts, request.Query)
	if len(rankedMemoryFacts) > request.Limit {
		return rankedMemoryFacts[:request.Limit], nil
	}
	return rankedMemoryFacts, nil
}

func filterReadableMemoryFacts(request MemorySearchRequest, memoryFacts []MemoryFact) []MemoryFact {
	filteredMemoryFacts := []MemoryFact{}
	for _, memoryFact := range memoryFacts {
		if canReadMemoryFact(request, memoryFact) {
			filteredMemoryFacts = append(filteredMemoryFacts, memoryFact)
		}
	}
	return filteredMemoryFacts
}

func rankMemoryFacts(memoryFacts []MemoryFact, query string) []MemoryFact {
	rankedMemoryFacts := append([]MemoryFact{}, memoryFacts...)
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	sort.SliceStable(rankedMemoryFacts, func(leftIndex int, rightIndex int) bool {
		leftMemoryFact := rankedMemoryFacts[leftIndex]
		rightMemoryFact := rankedMemoryFacts[rightIndex]
		leftScore := relevanceScore(leftMemoryFact, normalizedQuery)
		rightScore := relevanceScore(rightMemoryFact, normalizedQuery)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if !leftMemoryFact.ValidAt.Equal(rightMemoryFact.ValidAt) {
			return leftMemoryFact.ValidAt.After(rightMemoryFact.ValidAt)
		}
		return memoryFactStableKey(leftMemoryFact) < memoryFactStableKey(rightMemoryFact)
	})
	return rankedMemoryFacts
}

func relevanceScore(memoryFact MemoryFact, normalizedQuery string) float64 {
	score := memoryFact.Score
	normalizedContent := strings.ToLower(memoryFact.Content)
	if normalizedQuery == "" {
		return score
	}
	if strings.Contains(normalizedContent, normalizedQuery) {
		score += 1
	}
	for _, queryTerm := range strings.Fields(normalizedQuery) {
		if strings.Contains(normalizedContent, queryTerm) {
			score += 0.25
		}
	}
	return score
}

func memoryFactStableKey(memoryFact MemoryFact) string {
	if strings.TrimSpace(memoryFact.FactID) != "" {
		return memoryFact.FactID
	}
	return memoryFact.ScopeType + ":" + memoryFact.NamespaceID + ":" + memoryFact.Content
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
	if !containsAll(request.ReaderGrantedClasses, memoryFact.RequiredClasses) {
		return false
	}
	return access.CanAccess(access.Request{
		PersonAccess: policy.PersonAccess{
			PersonID:            request.ReaderPersonID,
			Circles:             request.ReaderCircles,
			ResourceAccessRules: request.ResourceAccessRules,
		},
		Action:   access.ActionRead,
		Resource: memoryResourceForFact(request.Namespaces, memoryFact),
	})
}

func memoryResourceForFact(namespaces []MemoryNamespace, memoryFact MemoryFact) string {
	for _, namespace := range namespaces {
		if namespace.NamespaceID != memoryFact.NamespaceID {
			continue
		}
		switch namespace.ScopeType {
		case ScopeTypeCircle:
			return "memory:circle:" + namespace.ScopeCircleID
		case ScopeTypePrivate, ScopeTypeUser:
			return "memory:private:" + namespace.ScopePersonID
		case ScopeTypeConversation:
			return "memory:conversation"
		default:
			return "memory:workspace"
		}
	}
	return "memory:workspace"
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
