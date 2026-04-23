package postgres

type RawEventRepository struct {
	database Database
}

func NewRawEventRepository(database Database) RawEventRepository {
	return RawEventRepository{database: database}
}

func (rawEventRepository RawEventRepository) InsertRawEvent(rawEventID string) error {
	_ = rawEventID
	return nil
}
