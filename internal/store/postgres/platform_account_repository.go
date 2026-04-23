package postgres

import "blueclaw/internal/identity"

type PlatformAccountRepository struct {
	database Database
}

func NewPlatformAccountRepository(database Database) PlatformAccountRepository {
	return PlatformAccountRepository{database: database}
}

func (platformAccountRepository PlatformAccountRepository) UpsertPlatformAccount(platformAccountIdentity identity.PlatformAccountIdentity) error {
	_ = platformAccountIdentity
	return nil
}
