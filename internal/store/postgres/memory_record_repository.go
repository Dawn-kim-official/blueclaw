package postgres

import "blueclaw/internal/memory"

type MemoryRecordRepository struct {
	database Database
}

func NewMemoryRecordRepository(database Database) MemoryRecordRepository {
	return MemoryRecordRepository{database: database}
}

func (memoryRecordRepository MemoryRecordRepository) InsertMemoryRecord(memoryRecord memory.MemoryRecord) error {
	_ = memoryRecord
	return nil
}
