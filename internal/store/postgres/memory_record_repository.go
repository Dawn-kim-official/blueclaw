package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"time"

	"blueclaw/internal/memory"
)

type MemoryRecordRepository struct {
	database Database
}

func NewMemoryRecordRepository(database Database) MemoryRecordRepository {
	return MemoryRecordRepository{database: database}
}

func (memoryRecordRepository MemoryRecordRepository) InsertMemoryRecord(memoryRecord memory.MemoryRecord) error {
	return memoryRecordRepository.SaveMemoryRecord(memoryRecord)
}

func (memoryRecordRepository MemoryRecordRepository) SaveMemoryRecord(memoryRecord memory.MemoryRecord) error {
	contentHash := sha256.Sum256(memoryRecord.ContentCiphertext)
	if errorValue := memoryRecordRepository.supersedeExistingMemoryRecord(memoryRecord); errorValue != nil {
		return errorValue
	}
	_, errorValue := memoryRecordRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO memory_record (
  memory_record_id, scope_type, scope_person_id, scope_conversation_id, memory_type, title,
  content_ciphertext, encryption_key_version, content_sha256, embedding_model, embedding,
  security_level_rank, required_classes, source_conversation_id, source_platform, source_message_id,
  created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,1,$8,'none',$9,$10,$11,$12,$13,$14,$15,$15)
ON CONFLICT (memory_record_id) DO UPDATE SET
  title = EXCLUDED.title,
  memory_type = EXCLUDED.memory_type,
  content_ciphertext = EXCLUDED.content_ciphertext,
  content_sha256 = EXCLUDED.content_sha256,
  security_level_rank = EXCLUDED.security_level_rank,
  required_classes = EXCLUDED.required_classes,
  source_conversation_id = EXCLUDED.source_conversation_id,
  source_platform = EXCLUDED.source_platform,
  source_message_id = EXCLUDED.source_message_id,
  superseded_at = NULL,
  updated_at = EXCLUDED.updated_at`,
		memoryRecord.MemoryRecordID,
		memoryRecord.ScopeType,
		emptyStringAsNil(memoryRecord.ScopePersonID),
		emptyStringAsNil(memoryRecord.ScopeConversationID),
		emptyStringAsNil(memoryRecord.MemoryType),
		memoryRecord.Title,
		memoryRecord.ContentCiphertext,
		contentHash[:],
		[]float32{},
		memoryRecord.SecurityLevelRank,
		memoryRecord.RequiredClasses,
		emptyStringAsNil(memoryRecord.SourceConversationID),
		memoryRecord.SourcePlatform,
		memoryRecord.SourceMessageID,
		memoryRecord.UpdatedAt,
	)
	return errorValue
}

func (memoryRecordRepository MemoryRecordRepository) supersedeExistingMemoryRecord(memoryRecord memory.MemoryRecord) error {
	if memoryRecord.Title == "" {
		return nil
	}
	_, errorValue := memoryRecordRepository.database.SQL.ExecContext(context.Background(), `
UPDATE memory_record SET superseded_at = $1, updated_at = $1
WHERE superseded_at IS NULL
  AND memory_record_id <> $2
  AND scope_type = $3
  AND COALESCE(scope_person_id, '') = COALESCE($4, '')
  AND COALESCE(scope_conversation_id, '') = COALESCE($5, '')
  AND title = $6`,
		time.Now().UTC(),
		memoryRecord.MemoryRecordID,
		memoryRecord.ScopeType,
		emptyStringAsNil(memoryRecord.ScopePersonID),
		emptyStringAsNil(memoryRecord.ScopeConversationID),
		memoryRecord.Title,
	)
	return errorValue
}

func (memoryRecordRepository MemoryRecordRepository) SearchMemory(request memory.MemorySearchRequest) ([]memory.MemoryRecord, error) {
	accessibleConversationIDs := append([]string{}, request.AccessibleConversationIDs...)
	if request.ConversationID != "" && !containsString(accessibleConversationIDs, request.ConversationID) {
		accessibleConversationIDs = append(accessibleConversationIDs, request.ConversationID)
	}
	rows, errorValue := memoryRecordRepository.database.SQL.QueryContext(context.Background(), `
SELECT memory_record_id, scope_type, COALESCE(scope_person_id, ''), COALESCE(scope_conversation_id, ''),
  COALESCE(source_conversation_id, ''), COALESCE(title, ''), COALESCE(memory_type, ''),
  COALESCE(source_platform, ''), COALESCE(source_message_id, ''), content_ciphertext,
  security_level_rank, required_classes, updated_at
FROM memory_record
WHERE superseded_at IS NULL AND (
  (scope_type = 'user' AND scope_person_id = $1)
  OR (scope_type = 'workspace' AND security_level_rank <= $2 AND required_classes <@ $3::text[])
  OR (scope_type = 'conversation' AND scope_conversation_id = ANY($4::text[]) AND security_level_rank <= $2 AND required_classes <@ $3::text[])
)
ORDER BY CASE scope_type WHEN 'user' THEN 0 WHEN 'workspace' THEN 1 WHEN 'conversation' THEN 2 ELSE 3 END, updated_at DESC`,
		request.ReaderPersonID,
		request.ReaderSecurityLevelRank,
		request.ReaderGrantedClasses,
		accessibleConversationIDs,
	)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanMemoryRecords(rows)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (memoryRecordRepository MemoryRecordRepository) SearchAccessibleMemory(scopePersonID string) ([]memory.MemoryRecord, error) {
	rows, errorValue := memoryRecordRepository.database.SQL.QueryContext(context.Background(), `
SELECT memory_record_id, scope_type, COALESCE(scope_person_id, ''), COALESCE(scope_conversation_id, ''),
  COALESCE(source_conversation_id, ''), COALESCE(title, ''), COALESCE(memory_type, ''),
  COALESCE(source_platform, ''), COALESCE(source_message_id, ''), content_ciphertext,
  security_level_rank, required_classes, updated_at
FROM memory_record
WHERE superseded_at IS NULL AND (scope_type = 'user' AND scope_person_id = $1)
ORDER BY updated_at DESC`, scopePersonID)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanMemoryRecords(rows)
}

func scanMemoryRecords(rows *sql.Rows) ([]memory.MemoryRecord, error) {
	memoryRecords := []memory.MemoryRecord{}
	for rows.Next() {
		var memoryRecord memory.MemoryRecord
		if errorValue := rows.Scan(
			&memoryRecord.MemoryRecordID,
			&memoryRecord.ScopeType,
			&memoryRecord.ScopePersonID,
			&memoryRecord.ScopeConversationID,
			&memoryRecord.SourceConversationID,
			&memoryRecord.Title,
			&memoryRecord.MemoryType,
			&memoryRecord.SourcePlatform,
			&memoryRecord.SourceMessageID,
			&memoryRecord.ContentCiphertext,
			&memoryRecord.SecurityLevelRank,
			&memoryRecord.RequiredClasses,
			&memoryRecord.UpdatedAt,
		); errorValue != nil {
			return nil, errorValue
		}
		memoryRecords = append(memoryRecords, memoryRecord)
	}
	return memoryRecords, rows.Err()
}
