package postgres

import (
	"context"
	"time"
)

type ConversationRepository struct {
	database Database
}

func NewConversationRepository(database Database) ConversationRepository {
	return ConversationRepository{database: database}
}

func (conversationRepository ConversationRepository) UpsertConversation(platform string, externalConversationID string) error {
	_, errorValue := conversationRepository.EnsureConversation(platform, externalConversationID)
	return errorValue
}

func (conversationRepository ConversationRepository) EnsureConversation(platform string, externalConversationID string) (string, error) {
	now := time.Now().UTC()
	conversationID := platform + ":" + externalConversationID
	_, errorValue := conversationRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO conversation (
  conversation_id, platform, external_conversation_id, conversation_type, display_name,
  last_seen_at, created_at, updated_at
) VALUES ($1,$2,$3,'opaque',$3,$4,$4,$4)
ON CONFLICT (platform, external_conversation_id) DO UPDATE SET
  last_seen_at = EXCLUDED.last_seen_at,
  updated_at = EXCLUDED.updated_at`,
		conversationID,
		platform,
		externalConversationID,
		now,
	)
	return conversationID, errorValue
}
