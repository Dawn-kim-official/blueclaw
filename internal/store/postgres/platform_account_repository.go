package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
)

type PlatformAccountRepository struct {
	database Database
}

func NewPlatformAccountRepository(database Database) PlatformAccountRepository {
	return PlatformAccountRepository{database: database}
}

func (platformAccountRepository PlatformAccountRepository) UpsertPlatformAccount(platformAccountIdentity identity.PlatformAccountIdentity) error {
	now := time.Now().UTC()
	platformAccountID := platformAccountIdentity.Platform + ":" + platformAccountIdentity.ExternalUserID
	_, errorValue := platformAccountRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO platform_account (
  platform_account_id, platform, external_user_id, email, display_name, person_id,
  is_approved_internal, last_seen_at, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,$8)
ON CONFLICT (platform, external_user_id) DO UPDATE SET
  email = EXCLUDED.email,
  display_name = EXCLUDED.display_name,
  person_id = EXCLUDED.person_id,
  is_approved_internal = EXCLUDED.is_approved_internal,
  last_seen_at = EXCLUDED.last_seen_at,
  updated_at = EXCLUDED.updated_at`,
		platformAccountID,
		platformAccountIdentity.Platform,
		platformAccountIdentity.ExternalUserID,
		emptyStringAsNil(platformAccountIdentity.Email),
		platformAccountIdentity.DisplayName,
		emptyStringAsNil(platformAccountIdentity.PersonID),
		platformAccountIdentity.PersonID != "",
		now,
	)
	return errorValue
}

func (platformAccountRepository PlatformAccountRepository) ListPlatformAccount() ([]identity.PlatformAccountIdentity, error) {
	rows, errorValue := platformAccountRepository.database.SQL.QueryContext(context.Background(), `
SELECT platform, external_user_id, COALESCE(email, ''), display_name, COALESCE(person_id, '')
FROM platform_account ORDER BY platform, external_user_id`)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanPlatformAccounts(rows)
}

func scanPlatformAccounts(rows *sql.Rows) ([]identity.PlatformAccountIdentity, error) {
	platformAccounts := []identity.PlatformAccountIdentity{}
	for rows.Next() {
		var platformAccount identity.PlatformAccountIdentity
		if errorValue := rows.Scan(&platformAccount.Platform, &platformAccount.ExternalUserID, &platformAccount.Email, &platformAccount.DisplayName, &platformAccount.PersonID); errorValue != nil {
			return nil, errorValue
		}
		platformAccounts = append(platformAccounts, platformAccount)
	}
	return platformAccounts, rows.Err()
}
