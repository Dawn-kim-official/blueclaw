package postgres

import "github.com/yeomyeonggeori/blueclaw/internal/adminapi"

type AdminAuditLogRepository struct {
	database Database
}

func NewAdminAuditLogRepository(database Database) AdminAuditLogRepository {
	return AdminAuditLogRepository{database: database}
}

func (adminAuditLogRepository AdminAuditLogRepository) InsertAuditEntry(auditEntry adminapi.AuditEntry) error {
	_ = auditEntry
	return nil
}
