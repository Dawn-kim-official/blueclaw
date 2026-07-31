package api

import (
	"sync"

	"github.com/Dawn-kim-official/blueclaw/internal/connectors"
)

const conversationReplyCapacity = 50
const trackedConversationCapacity = 2000

type StoredReply struct {
	ConversationID string                   `json:"conversationID"`
	ReplyTargetID  string                   `json:"replyTargetID"`
	Reply          connectors.OutboundReply `json:"reply"`
}

type ReplyStore struct {
	mutex             sync.Mutex
	byConversation    map[string][]StoredReply
	conversationOrder []string
}

func NewReplyStore() *ReplyStore {
	return &ReplyStore{byConversation: map[string][]StoredReply{}}
}

func (replyStore *ReplyStore) Append(conversationID string, replyTargetID string, reply connectors.OutboundReply) {
	replyStore.mutex.Lock()
	defer replyStore.mutex.Unlock()
	existing, isTracked := replyStore.byConversation[conversationID]
	stored := append(existing, StoredReply{
		ConversationID: conversationID,
		ReplyTargetID:  replyTargetID,
		Reply:          reply,
	})
	if len(stored) > conversationReplyCapacity {
		stored = stored[len(stored)-conversationReplyCapacity:]
	}
	replyStore.byConversation[conversationID] = stored
	if !isTracked {
		replyStore.trackConversation(conversationID)
	}
}

func (replyStore *ReplyStore) trackConversation(conversationID string) {
	replyStore.conversationOrder = append(replyStore.conversationOrder, conversationID)
	if len(replyStore.conversationOrder) <= trackedConversationCapacity {
		return
	}
	oldestConversationID := replyStore.conversationOrder[0]
	replyStore.conversationOrder = replyStore.conversationOrder[1:]
	delete(replyStore.byConversation, oldestConversationID)
}

func (replyStore *ReplyStore) List(conversationID string) []StoredReply {
	replyStore.mutex.Lock()
	defer replyStore.mutex.Unlock()
	return append([]StoredReply{}, replyStore.byConversation[conversationID]...)
}
