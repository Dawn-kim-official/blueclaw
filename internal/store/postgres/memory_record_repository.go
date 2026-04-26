package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"

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
	_, errorValue := memoryRecordRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO memory_record (
  memory_record_id, scope_type, scope_person_id, scope_conversation_id, memory_type, title,
  content_ciphertext, encryption_key_version, content_sha256, embedding_model, embedding,
  security_level_rank, required_classes, source_conversation_id, created_at, updated_at
) VALUES ($1,$2,$3,NULL,$4,$5,$6,1,$7,'none',$8,$9,$10,$11,$12,$12)
ON CONFLICT (memory_record_id) DO UPDATE SET
  title = EXCLUDED.title,
  content_ciphertext = EXCLUDED.content_ciphertext,
  content_sha256 = EXCLUDED.content_sha256,
  security_level_rank = EXCLUDED.security_level_rank,
  required_classes = EXCLUDED.required_classes,
  updated_at = EXCLUDED.updated_at`,
		memoryRecord.MemoryRecordID,
		memoryRecord.ScopeType,
		emptyStringAsNil(memoryRecord.ScopePersonID),
		"derived",
		memoryRecord.Title,
		memoryRecord.ContentCiphertext,
		contentHash[:],
		[]float32{},
		memoryRecord.SecurityLevelRank,
		memoryRecord.RequiredClasses,
		emptyStringAsNil(memoryRecord.SourceConversationID),
		memoryRecord.UpdatedAt,
	)
	return errorValue
}

func (memoryRecordRepository MemoryRecordRepository) SearchAccessibleMemory(scopePersonID string) ([]memory.MemoryRecord, error) {
	rows, errorValue := memoryRecordRepository.database.SQL.QueryContext(context.Background(), `
SELECT memory_record_id, scope_type, COALESCE(scope_person_id, ''), COALESCE(source_conversation_id, ''),
  COALESCE(title, ''), content_ciphertext, security_level_rank, required_classes, updated_at
FROM memory_record
WHERE superseded_at IS NULL AND (scope_person_id IS NULL OR scope_person_id = $1)
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
			&memoryRecord.SourceConversationID,
			&memoryRecord.Title,
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
