package task

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type InMemoryTaskWaitTokenRepository struct {
	mutex          sync.RWMutex
	taskWaitTokens map[string]TaskWaitToken
}

func NewInMemoryTaskWaitTokenRepository() *InMemoryTaskWaitTokenRepository {
	return &InMemoryTaskWaitTokenRepository{
		taskWaitTokens: map[string]TaskWaitToken{},
	}
}

func (repository *InMemoryTaskWaitTokenRepository) InsertTaskWaitToken(taskWaitToken TaskWaitToken) error {
	taskWaitToken = normalizeTaskWaitToken(taskWaitToken)
	if taskWaitToken.WaitID == "" {
		taskWaitToken.WaitID = NewIdentifier()
	}
	repository.mutex.Lock()
	repository.taskWaitTokens[taskWaitToken.WaitID] = taskWaitToken
	repository.mutex.Unlock()
	return nil
}

func (repository *InMemoryTaskWaitTokenRepository) FindOpenByWaitID(waitID string) (TaskWaitToken, bool, error) {
	return repository.findOpen(func(taskWaitToken TaskWaitToken) bool {
		return taskWaitToken.WaitID == strings.TrimSpace(waitID)
	})
}

func (repository *InMemoryTaskWaitTokenRepository) FindOpenByPersonConversationAndReplyTarget(personID string, platform string, conversationID string, replyTargetID string) (TaskWaitToken, bool, error) {
	return repository.findOpen(func(taskWaitToken TaskWaitToken) bool {
		return taskWaitTokenMatchesConversation(taskWaitToken, personID, platform, conversationID) && taskWaitToken.ReplyTargetID == strings.TrimSpace(replyTargetID)
	})
}

func (repository *InMemoryTaskWaitTokenRepository) FindOpenByPersonConversationAndThreadRoot(personID string, platform string, conversationID string, threadRootID string) (TaskWaitToken, bool, error) {
	return repository.findOpen(func(taskWaitToken TaskWaitToken) bool {
		return taskWaitTokenMatchesConversation(taskWaitToken, personID, platform, conversationID) && taskWaitToken.ThreadRootID == strings.TrimSpace(threadRootID)
	})
}

func (repository *InMemoryTaskWaitTokenRepository) FindOpenByPersonConversationAndDispatchID(personID string, platform string, conversationID string, dispatchID string) (TaskWaitToken, bool, error) {
	return repository.findOpen(func(taskWaitToken TaskWaitToken) bool {
		return taskWaitTokenMatchesConversation(taskWaitToken, personID, platform, conversationID) && taskWaitToken.DispatchID == strings.TrimSpace(dispatchID)
	})
}

func (repository *InMemoryTaskWaitTokenRepository) FindOpenByPersonTaskRunAndInteraction(personID string, taskRunID string, interactionID string) (TaskWaitToken, bool, error) {
	return repository.findOpen(func(taskWaitToken TaskWaitToken) bool {
		return taskWaitToken.PersonID == strings.TrimSpace(personID) && taskWaitToken.TaskRunID == strings.TrimSpace(taskRunID) && taskWaitToken.InteractionID == strings.TrimSpace(interactionID)
	})
}

func (repository *InMemoryTaskWaitTokenRepository) FindOpenByPersonAndConversation(personID string, platform string, conversationID string) ([]TaskWaitToken, error) {
	taskWaitTokens := []TaskWaitToken{}
	repository.mutex.RLock()
	for _, taskWaitToken := range repository.taskWaitTokens {
		if !taskWaitTokenIsOpen(taskWaitToken, time.Now().UTC()) {
			continue
		}
		if taskWaitTokenMatchesConversation(taskWaitToken, personID, platform, conversationID) {
			taskWaitTokens = append(taskWaitTokens, taskWaitToken)
		}
	}
	repository.mutex.RUnlock()
	sortTaskWaitTokens(taskWaitTokens)
	return taskWaitTokens, nil
}

func (repository *InMemoryTaskWaitTokenRepository) ResolveTaskWait(waitID string, resolvedAt time.Time) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	taskWaitToken, isFound := repository.taskWaitTokens[strings.TrimSpace(waitID)]
	if !isFound {
		return nil
	}
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	taskWaitToken.State = "resolved"
	taskWaitToken.ResolvedAt = &resolvedAt
	repository.taskWaitTokens[taskWaitToken.WaitID] = taskWaitToken
	return nil
}

func (repository *InMemoryTaskWaitTokenRepository) ExpireOldTaskWaits(before time.Time) ([]string, error) {
	if before.IsZero() {
		before = time.Now().UTC()
	}
	return repository.expireMatchingTaskWaits(before, func(taskWaitToken TaskWaitToken) bool {
		return taskWaitToken.ExpiresAt.Before(before) || taskWaitToken.ExpiresAt.Equal(before)
	}), nil
}

func (repository *InMemoryTaskWaitTokenRepository) ExpireTaskWaitTokensForPerson(personID string, expiredAt time.Time) ([]string, error) {
	if expiredAt.IsZero() {
		expiredAt = time.Now().UTC()
	}
	trimmedPersonID := strings.TrimSpace(personID)
	return repository.expireMatchingTaskWaits(expiredAt, func(taskWaitToken TaskWaitToken) bool {
		return taskWaitToken.PersonID == trimmedPersonID
	}), nil
}

func (repository *InMemoryTaskWaitTokenRepository) findOpen(matches func(TaskWaitToken) bool) (TaskWaitToken, bool, error) {
	taskWaitTokens := []TaskWaitToken{}
	repository.mutex.RLock()
	for _, taskWaitToken := range repository.taskWaitTokens {
		if taskWaitTokenIsOpen(taskWaitToken, time.Now().UTC()) && matches(taskWaitToken) {
			taskWaitTokens = append(taskWaitTokens, taskWaitToken)
		}
	}
	repository.mutex.RUnlock()
	sortTaskWaitTokens(taskWaitTokens)
	if len(taskWaitTokens) == 0 {
		return TaskWaitToken{}, false, nil
	}
	return taskWaitTokens[0], true, nil
}

func (repository *InMemoryTaskWaitTokenRepository) expireMatchingTaskWaits(expiredAt time.Time, matches func(TaskWaitToken) bool) []string {
	taskRunIDs := []string{}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	for waitID, taskWaitToken := range repository.taskWaitTokens {
		if taskWaitToken.State != "open" || !matches(taskWaitToken) {
			continue
		}
		taskWaitToken.State = "expired"
		taskWaitToken.ResolvedAt = &expiredAt
		repository.taskWaitTokens[waitID] = taskWaitToken
		taskRunIDs = append(taskRunIDs, taskWaitToken.TaskRunID)
	}
	sort.Strings(taskRunIDs)
	return taskRunIDs
}

func normalizeTaskWaitToken(taskWaitToken TaskWaitToken) TaskWaitToken {
	taskWaitToken.WaitID = strings.TrimSpace(taskWaitToken.WaitID)
	taskWaitToken.TaskRunID = strings.TrimSpace(taskWaitToken.TaskRunID)
	taskWaitToken.PersonID = strings.TrimSpace(taskWaitToken.PersonID)
	taskWaitToken.Platform = strings.TrimSpace(taskWaitToken.Platform)
	taskWaitToken.ConversationID = strings.TrimSpace(taskWaitToken.ConversationID)
	taskWaitToken.ReplyTargetID = strings.TrimSpace(taskWaitToken.ReplyTargetID)
	taskWaitToken.ThreadRootID = strings.TrimSpace(taskWaitToken.ThreadRootID)
	taskWaitToken.DispatchID = strings.TrimSpace(taskWaitToken.DispatchID)
	taskWaitToken.InteractionID = strings.TrimSpace(taskWaitToken.InteractionID)
	taskWaitToken.Kind = strings.TrimSpace(taskWaitToken.Kind)
	taskWaitToken.State = strings.TrimSpace(taskWaitToken.State)
	if taskWaitToken.State == "" {
		taskWaitToken.State = "open"
	}
	if taskWaitToken.CreatedAt.IsZero() {
		taskWaitToken.CreatedAt = time.Now().UTC()
	}
	return taskWaitToken
}

func taskWaitTokenMatchesConversation(taskWaitToken TaskWaitToken, personID string, platform string, conversationID string) bool {
	return taskWaitToken.PersonID == strings.TrimSpace(personID) &&
		taskWaitToken.Platform == strings.TrimSpace(platform) &&
		taskWaitToken.ConversationID == strings.TrimSpace(conversationID)
}

func taskWaitTokenIsOpen(taskWaitToken TaskWaitToken, now time.Time) bool {
	if taskWaitToken.State != "open" {
		return false
	}
	if taskWaitToken.ExpiresAt.IsZero() {
		return true
	}
	return taskWaitToken.ExpiresAt.After(now)
}

func sortTaskWaitTokens(taskWaitTokens []TaskWaitToken) {
	sort.SliceStable(taskWaitTokens, func(leftIndex int, rightIndex int) bool {
		left := taskWaitTokens[leftIndex]
		right := taskWaitTokens[rightIndex]
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.WaitID < right.WaitID
	})
}

var _ TaskWaitTokenRepository = (*InMemoryTaskWaitTokenRepository)(nil)
