package postgres

func emptyStringAsNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
