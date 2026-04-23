package postgres

type AttachmentRepository struct {
	database Database
}

func NewAttachmentRepository(database Database) AttachmentRepository {
	return AttachmentRepository{database: database}
}

func (attachmentRepository AttachmentRepository) InsertAttachment(attachmentID string) error {
	_ = attachmentID
	return nil
}
