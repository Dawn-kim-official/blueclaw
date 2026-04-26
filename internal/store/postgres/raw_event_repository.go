package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"blueclaw/internal/connectors"
)

type RawEventRepository struct {
	database Database
}

func NewRawEventRepository(database Database) RawEventRepository {
	return RawEventRepository{database: database}
}

func (rawEventRepository RawEventRepository) InsertRawEvent(rawEventID string) error {
	_, errorValue := rawEventRepository.database.SQL.ExecContext(context.Background(), "INSERT INTO raw_event (raw_event_id) VALUES ($1)", rawEventID)
	return errorValue
}

func (rawEventRepository RawEventRepository) TryInsertConnectorEvent(event connectors.PlatformInboundEvent) (bool, connectors.ConnectorRuntimeResult, error) {
	conversationRepository := NewConversationRepository(rawEventRepository.database)
	conversationID, errorValue := conversationRepository.EnsureConversation(event.Platform, event.ConversationID)
	if errorValue != nil {
		return false, connectors.ConnectorRuntimeResult{}, errorValue
	}

	result := connectors.ConnectorRuntimeResult{}
	contentHash := sha256.Sum256([]byte(event.Prompt))
	rawEventID := event.DedupeKey()
	execResult, errorValue := rawEventRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO raw_event (
  raw_event_id, platform, conversation_id, external_message_id, event_type,
  content_ciphertext, encryption_key_version, content_sha256, security_level_rank,
  required_classes, occurred_at, ingested_at, expires_at,
  reply_target_id, visible_context_ciphertext, visible_context_sha256, has_more_before, history_cursor
) VALUES ($1,$2,$3,$4,'message',$5,1,$6,0,'{}',$7,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (platform, conversation_id, external_message_id) DO NOTHING`,
		rawEventID,
		event.Platform,
		conversationID,
		event.MessageID,
		[]byte(event.Prompt),
		contentHash[:],
		time.Now().UTC(),
		time.Now().UTC().AddDate(0, 0, 60),
		event.ReplyTargetID,
		mustJSON(event.Context),
		hashJSON(event.Context),
		event.Context.HasMoreBefore,
		emptyStringAsNil(event.Context.HistoryCursor),
	)
	if errorValue != nil {
		return false, connectors.ConnectorRuntimeResult{}, errorValue
	}
	affectedRows, errorValue := execResult.RowsAffected()
	if errorValue == nil && affectedRows == 1 {
		return false, result, nil
	}

	existingResult, fetchError := rawEventRepository.GetConnectorResult(event.Platform, conversationID, event.MessageID)
	if fetchError != nil {
		return true, connectors.ConnectorRuntimeResult{}, fetchError
	}
	return true, existingResult, nil
}

func (rawEventRepository RawEventRepository) SaveConnectorResult(event connectors.PlatformInboundEvent, result connectors.ConnectorRuntimeResult) error {
	conversationID := event.Platform + ":" + event.ConversationID
	resultDocument, errorValue := json.Marshal(result)
	if errorValue != nil {
		return errorValue
	}
	_, errorValue = rawEventRepository.database.SQL.ExecContext(context.Background(), `
UPDATE raw_event SET connector_result_json = $1
WHERE platform = $2 AND conversation_id = $3 AND external_message_id = $4`,
		resultDocument,
		event.Platform,
		conversationID,
		event.MessageID,
	)
	return errorValue
}

func (rawEventRepository RawEventRepository) GetConnectorResult(platform string, conversationID string, messageID string) (connectors.ConnectorRuntimeResult, error) {
	var document []byte
	errorValue := rawEventRepository.database.SQL.QueryRowContext(context.Background(), `
SELECT connector_result_json FROM raw_event
WHERE platform = $1 AND conversation_id = $2 AND external_message_id = $3`,
		platform,
		conversationID,
		messageID,
	).Scan(&document)
	if errorValue != nil {
		return connectors.ConnectorRuntimeResult{}, errorValue
	}
	var result connectors.ConnectorRuntimeResult
	errorValue = json.Unmarshal(document, &result)
	return result, errorValue
}

func mustJSON(value any) []byte {
	document, _ := json.Marshal(value)
	return document
}

func hashJSON(value any) []byte {
	sum := sha256.Sum256(mustJSON(value))
	return sum[:]
}
