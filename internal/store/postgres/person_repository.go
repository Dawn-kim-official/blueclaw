package postgres

import "blueclaw/internal/policy"

type PersonRepository struct {
	database Database
}

func NewPersonRepository(database Database) PersonRepository {
	return PersonRepository{database: database}
}

func (personRepository PersonRepository) UpsertPerson(personPolicy policy.PersonPolicy) error {
	_ = personPolicy
	return nil
}
