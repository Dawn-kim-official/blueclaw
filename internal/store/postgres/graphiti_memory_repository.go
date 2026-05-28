package postgres

import (
	"context"
	"encoding/json"
	"errors"
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
  scope_circle_id, security_level_rank, required_classes, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (namespace_id) DO UPDATE SET
  scope_type = EXCLUDED.scope_type,
  scope_person_id = EXCLUDED.scope_person_id,
  scope_conversation_id = EXCLUDED.scope_conversation_id,
  scope_circle_id = EXCLUDED.scope_circle_id,
  security_level_rank = EXCLUDED.security_level_rank,
  required_classes = EXCLUDED.required_classes,
  updated_at = EXCLUDED.updated_at`,
			namespace.NamespaceID,
			namespace.ScopeType,
			emptyStringAsNil(namespace.ScopePersonID),
			emptyStringAsNil(namespace.ScopeConversationID),
			emptyStringAsNil(namespace.ScopeCircleID),
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
SELECT namespace_id, scope_type, COALESCE(scope_person_id, ''), COALESCE(scope_conversation_id, ''), COALESCE(scope_circle_id, ''),
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
  OR (
    scope_type = 'private'
    AND scope_person_id = $1
  )
  OR (
    scope_type = 'circle'
    AND scope_circle_id = ANY($5::text[])
  )
ORDER BY scope_type, namespace_id`,
		request.ReaderPersonID,
		request.ReaderSecurityLevelRank,
		request.ReaderGrantedClasses,
		append([]string{request.ConversationID}, request.AccessibleConversationIDs...),
		request.ReaderCircles,
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
			&namespace.ScopeCircleID,
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

func (repository GraphitiMemoryRepository) ListMemoryGraph(ctx context.Context, limit int) (memory.MemoryGraph, error) {
	if repository.database.SQL == nil {
		return memory.MemoryGraph{}, errors.New("postgres database is not open")
	}
	if limit <= 0 {
		limit = 80
	}
	namespaces, errorValue := repository.listMemoryGraphNamespaces(ctx, limit*2)
	if errorValue != nil {
		return memory.MemoryGraph{}, errorValue
	}
	episodes, errorValue := repository.listMemoryGraphEpisodes(ctx, limit)
	if errorValue != nil {
		return memory.MemoryGraph{}, errorValue
	}
	return buildMemoryGraph(namespaces, episodes), nil
}

func (repository GraphitiMemoryRepository) listMemoryGraphNamespaces(ctx context.Context, limit int) ([]memory.MemoryGraphNamespace, error) {
	rows, errorValue := repository.database.SQL.QueryContext(ctx, `
SELECT namespace_id, scope_type, COALESCE(scope_person_id, ''), COALESCE(scope_conversation_id, ''), COALESCE(scope_circle_id, ''),
       security_level_rank, required_classes, updated_at
FROM graphiti_namespace
ORDER BY updated_at DESC, namespace_id
LIMIT $1`, limit)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()

	namespaces := []memory.MemoryGraphNamespace{}
	for rows.Next() {
		var namespace memory.MemoryGraphNamespace
		errorValue = rows.Scan(
			&namespace.NamespaceID,
			&namespace.ScopeType,
			&namespace.ScopePersonID,
			&namespace.ScopeConversationID,
			&namespace.ScopeCircleID,
			&namespace.SecurityLevelRank,
			&namespace.RequiredClasses,
			&namespace.UpdatedAt,
		)
		if errorValue != nil {
			return nil, errorValue
		}
		namespaces = append(namespaces, namespace)
	}
	return namespaces, rows.Err()
}

func (repository GraphitiMemoryRepository) listMemoryGraphEpisodes(ctx context.Context, limit int) ([]memory.MemoryGraphEpisode, error) {
	rows, errorValue := repository.database.SQL.QueryContext(ctx, `
SELECT episode_id, source_platform, source_message_id, conversation_id, sender_person_id,
       namespace_document, ingestion_status, ingestion_error, occurred_at, updated_at
FROM graphiti_episode
ORDER BY updated_at DESC, episode_id
LIMIT $1`, limit)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()

	episodes := []memory.MemoryGraphEpisode{}
	for rows.Next() {
		var episode memory.MemoryGraphEpisode
		var namespaceDocument string
		errorValue = rows.Scan(
			&episode.EpisodeID,
			&episode.Platform,
			&episode.MessageID,
			&episode.ConversationID,
			&episode.SenderPersonID,
			&namespaceDocument,
			&episode.IngestionStatus,
			&episode.IngestionError,
			&episode.OccurredAt,
			&episode.UpdatedAt,
		)
		if errorValue != nil {
			return nil, errorValue
		}
		episode.NamespaceIDs = namespaceIDsFromDocument(namespaceDocument)
		episodes = append(episodes, episode)
	}
	return episodes, rows.Err()
}

func namespaceIDsFromDocument(document string) []string {
	var namespaces []memory.MemoryNamespace
	if errorValue := json.Unmarshal([]byte(document), &namespaces); errorValue != nil {
		return []string{}
	}
	namespaceIDs := []string{}
	seenNamespaceID := map[string]bool{}
	for _, namespace := range namespaces {
		if namespace.NamespaceID == "" || seenNamespaceID[namespace.NamespaceID] {
			continue
		}
		seenNamespaceID[namespace.NamespaceID] = true
		namespaceIDs = append(namespaceIDs, namespace.NamespaceID)
	}
	return namespaceIDs
}

func buildMemoryGraph(namespaces []memory.MemoryGraphNamespace, episodes []memory.MemoryGraphEpisode) memory.MemoryGraph {
	namespaceByID := map[string]int{}
	for index, namespace := range namespaces {
		namespaceByID[namespace.NamespaceID] = index
	}
	nodes := []memory.MemoryGraphNode{}
	edges := []memory.MemoryGraphEdge{}
	for _, namespace := range namespaces {
		nodes = append(nodes, memory.MemoryGraphNode{
			NodeID:    "namespace:" + namespace.NamespaceID,
			Label:     namespaceLabel(namespace),
			Kind:      "namespace",
			ScopeType: namespace.ScopeType,
		})
	}
	for _, episode := range episodes {
		episodeNodeID := "episode:" + episode.EpisodeID
		nodes = append(nodes, memory.MemoryGraphNode{
			NodeID: episodeNodeID,
			Label:  episodeLabel(episode),
			Kind:   "episode",
			Status: episode.IngestionStatus,
		})
		for _, namespaceID := range episode.NamespaceIDs {
			if namespaceIndex, isFound := namespaceByID[namespaceID]; isFound {
				namespaces[namespaceIndex].EpisodeCount += 1
			}
			edges = append(edges, memory.MemoryGraphEdge{
				SourceID: "namespace:" + namespaceID,
				TargetID: episodeNodeID,
				Label:    "episode",
				Weight:   1,
			})
		}
	}
	return memory.MemoryGraph{
		Namespaces: namespaces,
		Episodes:   episodes,
		Nodes:      nodes,
		Edges:      edges,
	}
}

func namespaceLabel(namespace memory.MemoryGraphNamespace) string {
	switch namespace.ScopeType {
	case memory.ScopeTypeUser, memory.ScopeTypePrivate:
		return firstNonEmptyGraphText(namespace.ScopePersonID, namespace.NamespaceID)
	case memory.ScopeTypeCircle:
		return firstNonEmptyGraphText(namespace.ScopeCircleID, namespace.NamespaceID)
	case memory.ScopeTypeConversation:
		return firstNonEmptyGraphText(namespace.ScopeConversationID, namespace.NamespaceID)
	default:
		return namespace.ScopeType
	}
}

func episodeLabel(episode memory.MemoryGraphEpisode) string {
	return firstNonEmptyGraphText(episode.Platform, "episode") + " " + episode.OccurredAt.Format("01-02 15:04")
}

func firstNonEmptyGraphText(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
