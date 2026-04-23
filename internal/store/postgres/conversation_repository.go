package postgres

type ConversationRepository struct {
	database Database
}

func NewConversationRepository(database Database) ConversationRepository {
	return ConversationRepository{database: database}
}

func (conversationRepository ConversationRepository) UpsertConversation(platform string, externalConversationID string) error {
	_ = platform
	_ = externalConversationID
	return nil
}
