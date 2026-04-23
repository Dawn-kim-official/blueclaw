package postgres

import "blueclaw/internal/memory"

type ContentSegmentRepository struct {
	database Database
}

func NewContentSegmentRepository(database Database) ContentSegmentRepository {
	return ContentSegmentRepository{database: database}
}

func (contentSegmentRepository ContentSegmentRepository) InsertContentSegment(contentSegment memory.ContentSegment) error {
	_ = contentSegment
	return nil
}
