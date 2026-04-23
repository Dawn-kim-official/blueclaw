package postgres

type Database struct {
	ConnectionString string
}

func OpenDatabase(connectionString string) Database {
	return Database{ConnectionString: connectionString}
}
