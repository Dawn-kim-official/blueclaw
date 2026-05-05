package postgres

import (
	"context"
	"time"

	"blueclaw/internal/policy"
)

type PersonRepository struct {
	database Database
}

func NewPersonRepository(database Database) PersonRepository {
	return PersonRepository{database: database}
}

func (personRepository PersonRepository) UpsertPerson(personPolicy policy.PersonPolicy) error {
	now := time.Now().UTC()
	_, errorValue := personRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO person (
  person_id, display_name, security_level_name, security_level_rank,
  granted_classes, circles, is_admin, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
ON CONFLICT (person_id) DO UPDATE SET
  display_name = EXCLUDED.display_name,
  security_level_name = EXCLUDED.security_level_name,
  security_level_rank = EXCLUDED.security_level_rank,
  granted_classes = EXCLUDED.granted_classes,
  circles = EXCLUDED.circles,
  is_admin = EXCLUDED.is_admin,
  updated_at = EXCLUDED.updated_at`,
		personPolicy.PersonID,
		personPolicy.DisplayName,
		personPolicy.SecurityLevelName,
		personPolicy.SecurityLevelRank,
		personPolicy.GrantedClasses,
		personPolicy.Circles,
		personPolicy.IsAdmin,
		now,
	)
	if errorValue != nil {
		return errorValue
	}
	for index, email := range personPolicy.Emails {
		if errorValue := personRepository.upsertPersonEmail(personPolicy.PersonID, email, index == 0, now); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func (personRepository PersonRepository) UpsertPeople(policyDocument policy.PolicyDocument) error {
	for _, personPolicy := range policyDocument.People {
		if errorValue := personRepository.UpsertPerson(personPolicy); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func (personRepository PersonRepository) upsertPersonEmail(personID string, email string, isPrimary bool, now time.Time) error {
	personEmailID := personID + ":" + email
	_, errorValue := personRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO person_email (person_email_id, person_id, email, is_primary, created_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (email) DO UPDATE SET
  person_id = EXCLUDED.person_id,
  is_primary = EXCLUDED.is_primary`,
		personEmailID,
		personID,
		email,
		isPrimary,
		now,
	)
	return errorValue
}
