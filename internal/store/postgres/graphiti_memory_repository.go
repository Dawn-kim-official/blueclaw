package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"blueclaw/internal/memory"
)

type GraphitiMemoryRepository struct {
	database Database
}

func NewGraphitiMemoryRepository(database Database) GraphitiMemoryRepository {
	return GraphitiMemoryRepository{database: database}
}

func (repository GraphitiMemoryRepository) SaveGraphNamespaces(ctx context.Context, namespaces []memory.MemoryNamespace) error {
	for _, namespace := range namespaces {
		if strings.TrimSpace(namespace.NamespaceID) == "" {
			continue
		}
		_, errorValue := repository.database.SQL.ExecContext(ctx, `
INSERT INTO graphiti_namespace (
  namespace_id, scope_type, scope_person_id, scope_conversation_id,
  security_level_rank, required_classes, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (namespace_id) DO UPDATE SET
  scope_type = EXCLUDED.scope_type,
  scope_person_id = EXCLUDED.scope_person_id,
  scope_conversation_id = EXCLUDED.scope_conversation_id,
  security_level_rank = EXCLUDED.security_level_rank,
  required_classes = EXCLUDED.required_classes,
  updated_at = EXCLUDED.updated_at`,
			namespace.NamespaceID,
			namespace.ScopeType,
			emptyStringAsNil(namespace.ScopePersonID),
			emptyStringAsNil(namespace.ScopeConversationID),
			namespace.SecurityLevelRank,
			namespace.RequiredClasses,
			time.Now().UTC(),
		)
		if errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func (repository GraphitiMemoryRepository) SaveGraphEpisode(ctx context.Context, episode memory.MemoryEpisode, status string, errorMessage string) error {
	namespaceDocument, errorValue := json.Marshal(episode.Namespaces)
	if errorValue != nil {
		return errorValue
	}
	_, errorValue = repository.database.SQL.ExecContext(ctx, `
INSERT INTO graphiti_episode (
  episode_id, source_platform, source_message_id, conversation_id, sender_person_id,
  namespace_document, ingestion_status, ingestion_error, occurred_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (episode_id) DO UPDATE SET
  namespace_document = EXCLUDED.namespace_document,
  ingestion_status = EXCLUDED.ingestion_status,
  ingestion_error = EXCLUDED.ingestion_error,
  updated_at = EXCLUDED.updated_at`,
		episode.EpisodeID,
		episode.Platform,
		episode.MessageID,
		episode.ConversationID,
		episode.SenderPersonID,
		string(namespaceDocument),
		status,
		errorMessage,
		episode.OccurredAt,
		time.Now().UTC(),
	)
	return errorValue
}

func (repository GraphitiMemoryRepository) ListAccessibleNamespaces(ctx context.Context, request memory.MemorySearchRequest) ([]memory.MemoryNamespace, error) {
	rows, errorValue := repository.database.SQL.QueryContext(ctx, `
SELECT namespace_id, scope_type, COALESCE(scope_person_id, ''), COALESCE(scope_conversation_id, ''),
       security_level_rank, required_classes
FROM graphiti_namespace
WHERE
  (scope_type = 'user' AND scope_person_id = $1)
  OR (
    scope_type = 'workspace'
    AND security_level_rank <= $2
    AND required_classes <@ $3::text[]
  )
  OR (
    scope_type = 'conversation'
    AND scope_conversation_id = ANY($4::text[])
    AND security_level_rank <= $2
    AND required_classes <@ $3::text[]
  )
ORDER BY scope_type, namespace_id`,
		request.ReaderPersonID,
		request.ReaderSecurityLevelRank,
		request.ReaderGrantedClasses,
		append([]string{request.ConversationID}, request.AccessibleConversationIDs...),
	)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()

	namespaces := []memory.MemoryNamespace{}
	for rows.Next() {
		var namespace memory.MemoryNamespace
		errorValue = rows.Scan(
			&namespace.NamespaceID,
			&namespace.ScopeType,
			&namespace.ScopePersonID,
			&namespace.ScopeConversationID,
			&namespace.SecurityLevelRank,
			&namespace.RequiredClasses,
		)
		if errorValue != nil {
			return nil, errorValue
		}
		namespaces = append(namespaces, namespace)
	}
	return namespaces, rows.Err()
}
