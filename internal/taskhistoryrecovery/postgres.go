package taskhistoryrecovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type databaseQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadSourceSnapshot(ctx context.Context, transaction *sql.Tx) (sourceSnapshot, error) {
	databaseIdentity, errorValue := loadDatabaseIdentity(ctx, transaction)
	if errorValue != nil {
		return sourceSnapshot{}, fmt.Errorf("identify source database: %w", errorValue)
	}
	schemas, errorValue := loadRecoverySchemas(ctx, transaction)
	if errorValue != nil {
		return sourceSnapshot{}, fmt.Errorf("inspect source schema: %w", errorValue)
	}
	if errorValue := validateRecoverySchemas(schemas); errorValue != nil {
		return sourceSnapshot{}, fmt.Errorf("source schema is unsafe: %w", errorValue)
	}
	tableCounts, errorValue := loadSourceTableCounts(ctx, transaction)
	if errorValue != nil {
		return sourceSnapshot{}, errorValue
	}
	taskRunsByStatus, errorValue := loadTaskRunStatusCounts(ctx, transaction)
	if errorValue != nil {
		return sourceSnapshot{}, errorValue
	}
	rows, errorValue := loadTerminalRecoveryRows(ctx, transaction, schemas)
	if errorValue != nil {
		return sourceSnapshot{}, errorValue
	}
	return sourceSnapshot{
		DatabaseIdentity: databaseIdentity,
		Schemas:          schemas,
		Rows:             rows,
		TableCounts:      tableCounts,
		TaskRunsByStatus: taskRunsByStatus,
	}, nil
}

func validateTargetSchemas(ctx context.Context, transaction *sql.Tx, sourceSchemas map[string]tableSchema) error {
	targetSchemas, errorValue := loadRecoverySchemas(ctx, transaction)
	if errorValue != nil {
		return fmt.Errorf("inspect target schema: %w", errorValue)
	}
	if errorValue := validateRecoverySchemas(targetSchemas); errorValue != nil {
		return fmt.Errorf("target schema is unsafe: %w", errorValue)
	}
	return compareRecoverySchemas(sourceSchemas, targetSchemas)
}

func compareRecoverySchemas(sourceSchemas map[string]tableSchema, targetSchemas map[string]tableSchema) error {
	for _, definition := range recoveryTableDefinitions {
		if !reflect.DeepEqual(sourceSchemas[definition.Name], targetSchemas[definition.Name]) {
			return fmt.Errorf("source and target column schemas differ for %s", definition.Name)
		}
	}
	return nil
}

func loadRecoverySchemas(ctx context.Context, queryer databaseQueryer) (map[string]tableSchema, error) {
	schemas := map[string]tableSchema{}
	for _, definition := range recoveryTableDefinitions {
		schema, errorValue := loadTableSchema(ctx, queryer, definition.Name)
		if errorValue != nil {
			return nil, errorValue
		}
		schemas[definition.Name] = schema
	}
	return schemas, nil
}

func loadTableSchema(ctx context.Context, queryer databaseQueryer, tableName string) (tableSchema, error) {
	rows, errorValue := queryer.QueryContext(ctx, `
SELECT
  column_name,
  ordinal_position,
  data_type,
  COALESCE(udt_schema, ''),
  COALESCE(udt_name, ''),
  COALESCE(domain_schema, ''),
  COALESCE(domain_name, ''),
  is_nullable,
  COALESCE(column_default, ''),
  is_identity,
  COALESCE(identity_generation, ''),
  is_generated,
  COALESCE(generation_expression, ''),
  COALESCE(character_maximum_length::text, ''),
  COALESCE(numeric_precision::text, ''),
  COALESCE(numeric_scale::text, ''),
  COALESCE(datetime_precision::text, ''),
  COALESCE(collation_schema, ''),
  COALESCE(collation_name, '')
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = $1
ORDER BY ordinal_position`, tableName)
	if errorValue != nil {
		return tableSchema{}, errorValue
	}
	defer rows.Close()
	columns := []columnSchema{}
	for rows.Next() {
		column, errorValue := scanColumnSchema(rows)
		if errorValue != nil {
			return tableSchema{}, errorValue
		}
		columns = append(columns, column)
	}
	if errorValue := rows.Err(); errorValue != nil {
		return tableSchema{}, errorValue
	}
	if len(columns) == 0 {
		return tableSchema{}, fmt.Errorf("required table public.%s is missing", tableName)
	}
	primaryKeyColumns, errorValue := loadPrimaryKeyColumns(ctx, queryer, tableName)
	if errorValue != nil {
		return tableSchema{}, errorValue
	}
	return tableSchema{Name: tableName, Columns: columns, PrimaryKeyColumns: primaryKeyColumns}, nil
}

func scanColumnSchema(rows *sql.Rows) (columnSchema, error) {
	column := columnSchema{}
	errorValue := rows.Scan(
		&column.Name,
		&column.OrdinalPosition,
		&column.DataType,
		&column.UserDefinedTypeSchema,
		&column.UserDefinedTypeName,
		&column.DomainSchema,
		&column.DomainName,
		&column.IsNullable,
		&column.ColumnDefault,
		&column.IsIdentity,
		&column.IdentityGeneration,
		&column.IsGenerated,
		&column.GenerationExpression,
		&column.CharacterMaximumLength,
		&column.NumericPrecision,
		&column.NumericScale,
		&column.DateTimePrecision,
		&column.CollationSchema,
		&column.CollationName,
	)
	return column, errorValue
}

func loadPrimaryKeyColumns(ctx context.Context, queryer databaseQueryer, tableName string) ([]string, error) {
	rows, errorValue := queryer.QueryContext(ctx, `
SELECT key_usage.column_name
FROM information_schema.table_constraints AS constraints
JOIN information_schema.key_column_usage AS key_usage
  ON key_usage.constraint_schema = constraints.constraint_schema
  AND key_usage.constraint_name = constraints.constraint_name
  AND key_usage.table_schema = constraints.table_schema
  AND key_usage.table_name = constraints.table_name
WHERE constraints.table_schema = 'public'
  AND constraints.table_name = $1
  AND constraints.constraint_type = 'PRIMARY KEY'
ORDER BY key_usage.ordinal_position`, tableName)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var columnName string
		if errorValue := rows.Scan(&columnName); errorValue != nil {
			return nil, errorValue
		}
		columns = append(columns, columnName)
	}
	return columns, rows.Err()
}

func validateRecoverySchemas(schemas map[string]tableSchema) error {
	for _, definition := range recoveryTableDefinitions {
		schema, isFound := schemas[definition.Name]
		if !isFound {
			return fmt.Errorf("required table %s is missing", definition.Name)
		}
		if !reflect.DeepEqual(schema.PrimaryKeyColumns, []string{definition.PrimaryKeyColumn}) {
			return fmt.Errorf("table %s must use primary key %s", definition.Name, definition.PrimaryKeyColumn)
		}
		for _, column := range schema.Columns {
			if column.IsIdentity == "YES" || column.IsGenerated != "NEVER" {
				return fmt.Errorf("table %s column %s cannot be copied exactly", definition.Name, column.Name)
			}
		}
		for _, requiredColumn := range requiredColumns(definition) {
			if !hasColumn(schema, requiredColumn) {
				return fmt.Errorf("table %s is missing required column %s", definition.Name, requiredColumn)
			}
		}
	}
	return nil
}

func requiredColumns(definition tableDefinition) []string {
	required := []string{definition.PrimaryKeyColumn, "task_run_id"}
	if definition.Name == "task_run" {
		return []string{"task_run_id", "status", "requester_person_id"}
	}
	return required
}

func hasColumn(schema tableSchema, columnName string) bool {
	for _, column := range schema.Columns {
		if column.Name == columnName {
			return true
		}
	}
	return false
}

func loadSourceTableCounts(ctx context.Context, queryer databaseQueryer) (map[string]int64, error) {
	counts := map[string]int64{}
	for _, definition := range recoveryTableDefinitions {
		query := "SELECT count(*) FROM public." + quoteIdentifier(definition.Name)
		var count int64
		if errorValue := queryer.QueryRowContext(ctx, query).Scan(&count); errorValue != nil {
			return nil, fmt.Errorf("count source %s rows: %w", definition.Name, errorValue)
		}
		counts[definition.Name] = count
	}
	return counts, nil
}

func loadTaskRunStatusCounts(ctx context.Context, queryer databaseQueryer) (map[string]int64, error) {
	rows, errorValue := queryer.QueryContext(ctx, `
SELECT status, count(*)
FROM public.task_run
GROUP BY status
ORDER BY status`)
	if errorValue != nil {
		return nil, fmt.Errorf("count source task runs by status: %w", errorValue)
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var status string
		var count int64
		if errorValue := rows.Scan(&status, &count); errorValue != nil {
			return nil, errorValue
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

func loadTerminalRecoveryRows(ctx context.Context, queryer databaseQueryer, schemas map[string]tableSchema) (map[string][]recoveryRow, error) {
	rowsByTable := map[string][]recoveryRow{}
	for _, definition := range recoveryTableDefinitions {
		rows, errorValue := loadTerminalTableRows(ctx, queryer, definition, schemas[definition.Name])
		if errorValue != nil {
			return nil, errorValue
		}
		rowsByTable[definition.Name] = rows
	}
	return rowsByTable, nil
}

func loadTerminalTableRows(ctx context.Context, queryer databaseQueryer, definition tableDefinition, schema tableSchema) ([]recoveryRow, error) {
	query := buildTerminalRowsQuery(definition, schema)
	rows, errorValue := queryer.QueryContext(ctx, query, stringArguments(terminalTaskStatuses)...)
	if errorValue != nil {
		return nil, fmt.Errorf("read source %s terminal rows: %w", definition.Name, errorValue)
	}
	defer rows.Close()
	return scanRecoveryRows(rows, definition, schema)
}

func buildTerminalRowsQuery(definition tableDefinition, schema tableSchema) string {
	alias := "recovery_row"
	selectedColumns := make([]string, 0, len(schema.Columns))
	for _, column := range schema.Columns {
		selectedColumns = append(selectedColumns, alias+"."+quoteIdentifier(column.Name))
	}
	statusPlaceholders := placeholders(1, len(terminalTaskStatuses))
	fromClause := "public." + quoteIdentifier(definition.Name) + " AS " + alias
	if definition.Name != "task_run" {
		fromClause += " JOIN public.task_run AS terminal_run ON terminal_run.task_run_id = " + alias + ".task_run_id"
	}
	statusAlias := alias
	if definition.Name != "task_run" {
		statusAlias = "terminal_run"
	}
	orderColumns := []string{alias + ".task_run_id"}
	if definition.PrimaryKeyColumn != "task_run_id" {
		orderColumns = append(orderColumns, alias+"."+quoteIdentifier(definition.PrimaryKeyColumn))
	}
	return "SELECT " + strings.Join(selectedColumns, ", ") + ", to_jsonb(" + alias + ")::text" +
		" FROM " + fromClause +
		" WHERE " + statusAlias + ".status IN (" + strings.Join(statusPlaceholders, ", ") + ")" +
		" ORDER BY " + strings.Join(orderColumns, ", ")
}

func scanRecoveryRows(rows *sql.Rows, definition tableDefinition, schema tableSchema) ([]recoveryRow, error) {
	primaryKeyIndex, errorValue := columnIndex(schema, definition.PrimaryKeyColumn)
	if errorValue != nil {
		return nil, errorValue
	}
	taskRunIndex, errorValue := columnIndex(schema, "task_run_id")
	if errorValue != nil {
		return nil, errorValue
	}
	requesterPersonIndex := -1
	if definition.Name == "task_run" {
		requesterPersonIndex, errorValue = columnIndex(schema, "requester_person_id")
		if errorValue != nil {
			return nil, errorValue
		}
	}
	recoveryRows := []recoveryRow{}
	for rows.Next() {
		row, errorValue := scanRecoveryRow(rows, definition, len(schema.Columns), primaryKeyIndex, taskRunIndex, requesterPersonIndex)
		if errorValue != nil {
			return nil, errorValue
		}
		recoveryRows = append(recoveryRows, row)
	}
	return recoveryRows, rows.Err()
}

func scanRecoveryRow(rows *sql.Rows, definition tableDefinition, columnCount int, primaryKeyIndex int, taskRunIndex int, requesterPersonIndex int) (recoveryRow, error) {
	values := make([]any, columnCount)
	destinations := make([]any, columnCount+1)
	for index := range values {
		destinations[index] = &values[index]
	}
	var canonicalJSON string
	destinations[columnCount] = &canonicalJSON
	if errorValue := rows.Scan(destinations...); errorValue != nil {
		return recoveryRow{}, errorValue
	}
	primaryKey, errorValue := requiredStringValue(values[primaryKeyIndex], definition.PrimaryKeyColumn)
	if errorValue != nil {
		return recoveryRow{}, errorValue
	}
	taskRunID, errorValue := requiredStringValue(values[taskRunIndex], "task_run_id")
	if errorValue != nil {
		return recoveryRow{}, errorValue
	}
	requesterPersonID := ""
	if requesterPersonIndex >= 0 {
		requesterPersonID, errorValue = optionalStringValue(values[requesterPersonIndex], "requester_person_id")
		if errorValue != nil {
			return recoveryRow{}, errorValue
		}
	}
	return recoveryRow{
		PrimaryKey:        primaryKey,
		TaskRunID:         taskRunID,
		RequesterPersonID: requesterPersonID,
		Values:            values,
		CanonicalJSON:     canonicalJSON,
	}, nil
}

func inspectTargetState(ctx context.Context, queryer databaseQueryer, snapshot sourceSnapshot, databaseIdentity string) (targetState, error) {
	acceptsWrites, errorValue := targetAcceptsWrites(ctx, queryer)
	if errorValue != nil {
		return targetState{}, errorValue
	}
	candidateTaskRunIDs := rowPrimaryKeys(snapshot.Rows["task_run"])
	existingTaskRunIDs, errorValue := findExistingIdentifiers(ctx, queryer, "task_run", "task_run_id", candidateTaskRunIDs)
	if errorValue != nil {
		return targetState{}, errorValue
	}
	expectedRows := filterRowsByTaskRunIDs(snapshot.Rows, existingTaskRunIDs)
	childConflictIDs, errorValue := findChildIdentifierConflicts(ctx, queryer, expectedRows)
	if errorValue != nil {
		return targetState{}, errorValue
	}
	missingRequesterPersonIDs, errorValue := findMissingRequesterPersonIDs(ctx, queryer, expectedRows["task_run"])
	if errorValue != nil {
		return targetState{}, errorValue
	}
	return targetState{
		DatabaseIdentity:          databaseIdentity,
		AcceptsWrites:             acceptsWrites,
		ExistingTaskRunIDs:        existingTaskRunIDs,
		ChildConflictIDs:          childConflictIDs,
		MissingRequesterPersonIDs: missingRequesterPersonIDs,
	}, nil
}

func targetAcceptsWrites(ctx context.Context, queryer databaseQueryer) (bool, error) {
	var acceptsWrites bool
	if errorValue := queryer.QueryRowContext(ctx, "SELECT NOT pg_is_in_recovery()").Scan(&acceptsWrites); errorValue != nil {
		return false, fmt.Errorf("inspect target recovery state: %w", errorValue)
	}
	return acceptsWrites, nil
}

func findChildIdentifierConflicts(ctx context.Context, queryer databaseQueryer, rowsByTable map[string][]recoveryRow) (map[string]map[string]bool, error) {
	conflicts := map[string]map[string]bool{}
	for _, definition := range recoveryTableDefinitions[1:] {
		existingIdentifiers, errorValue := findExistingIdentifiers(ctx, queryer, definition.Name, definition.PrimaryKeyColumn, rowPrimaryKeys(rowsByTable[definition.Name]))
		if errorValue != nil {
			return nil, errorValue
		}
		conflicts[definition.Name] = existingIdentifiers
	}
	return conflicts, nil
}

func findMissingRequesterPersonIDs(ctx context.Context, queryer databaseQueryer, taskRunRows []recoveryRow) (map[string]bool, error) {
	requesterPersonIDs := map[string]bool{}
	for _, row := range taskRunRows {
		if row.RequesterPersonID != "" {
			requesterPersonIDs[row.RequesterPersonID] = true
		}
	}
	requestedIdentifiers := sortedIdentifiers(requesterPersonIDs)
	existingIdentifiers, errorValue := findExistingIdentifiers(ctx, queryer, "person", "person_id", requestedIdentifiers)
	if errorValue != nil {
		return nil, fmt.Errorf("validate target requester people: %w", errorValue)
	}
	missingIdentifiers := map[string]bool{}
	for _, identifier := range requestedIdentifiers {
		if !existingIdentifiers[identifier] {
			missingIdentifiers[identifier] = true
		}
	}
	return missingIdentifiers, nil
}

func findExistingIdentifiers(ctx context.Context, queryer databaseQueryer, tableName string, columnName string, identifiers []string) (map[string]bool, error) {
	existingIdentifiers := map[string]bool{}
	for _, identifierBatch := range identifierBatches(identifiers, 500) {
		query := "SELECT " + quoteIdentifier(columnName) + " FROM public." + quoteIdentifier(tableName) +
			" WHERE " + quoteIdentifier(columnName) + " IN (" + strings.Join(placeholders(1, len(identifierBatch)), ", ") + ")"
		rows, errorValue := queryer.QueryContext(ctx, query, stringArguments(identifierBatch)...)
		if errorValue != nil {
			return nil, fmt.Errorf("inspect target %s identifiers: %w", tableName, errorValue)
		}
		for rows.Next() {
			var identifier string
			if errorValue := rows.Scan(&identifier); errorValue != nil {
				rows.Close()
				return nil, errorValue
			}
			existingIdentifiers[identifier] = true
		}
		errorValue = rows.Err()
		rows.Close()
		if errorValue != nil {
			return nil, errorValue
		}
	}
	return existingIdentifiers, nil
}

func loadDatabaseIdentity(ctx context.Context, queryer databaseQueryer) (string, error) {
	var databaseName string
	var postmasterStartedAt string
	errorValue := queryer.QueryRowContext(ctx, `
SELECT
  current_database(),
  pg_postmaster_start_time()::text`).Scan(&databaseName, &postmasterStartedAt)
	if errorValue != nil {
		return "", errorValue
	}
	return strings.Join([]string{databaseName, postmasterStartedAt}, "|"), nil
}

func configureCanonicalSession(ctx context.Context, transaction *sql.Tx) error {
	_, errorValue := transaction.ExecContext(ctx, "SELECT set_config('TimeZone', 'UTC', true)")
	return errorValue
}

func lockRecoveryTables(ctx context.Context, transaction *sql.Tx, lockTimeout string) error {
	if _, errorValue := transaction.ExecContext(ctx, "SELECT set_config('lock_timeout', $1, true)", lockTimeout); errorValue != nil {
		return fmt.Errorf("configure target recovery lock timeout: %w", errorValue)
	}
	tableNames := make([]string, 0, len(recoveryTableDefinitions))
	for _, definition := range recoveryTableDefinitions {
		tableNames = append(tableNames, "public."+quoteIdentifier(definition.Name))
	}
	query := "LOCK TABLE " + strings.Join(tableNames, ", ") + " IN SHARE ROW EXCLUSIVE MODE"
	if _, errorValue := transaction.ExecContext(ctx, query); errorValue != nil {
		return fmt.Errorf("lock target task history tables: %w", errorValue)
	}
	return nil
}

func insertRecoveryRows(ctx context.Context, transaction *sql.Tx, schemas map[string]tableSchema, rowsByTable map[string][]recoveryRow) (map[string]int64, error) {
	insertedCounts := emptyTableCounts()
	for _, definition := range recoveryTableDefinitions {
		statement := buildInsertStatement(definition, schemas[definition.Name])
		for _, row := range rowsByTable[definition.Name] {
			var insertedCanonicalJSON string
			errorValue := transaction.QueryRowContext(ctx, statement, row.Values...).Scan(&insertedCanonicalJSON)
			if errorValue != nil {
				return nil, fmt.Errorf("insert %s %s: %w", definition.Name, row.PrimaryKey, errorValue)
			}
			if insertedCanonicalJSON != row.CanonicalJSON {
				return nil, fmt.Errorf("inserted %s %s does not match source row", definition.Name, row.PrimaryKey)
			}
			insertedCounts[definition.Name]++
		}
	}
	return insertedCounts, nil
}

func buildInsertStatement(definition tableDefinition, schema tableSchema) string {
	columnNames := make([]string, 0, len(schema.Columns))
	for _, column := range schema.Columns {
		columnNames = append(columnNames, quoteIdentifier(column.Name))
	}
	tableName := quoteIdentifier(definition.Name)
	return "INSERT INTO public." + tableName + " AS recovered_row (" + strings.Join(columnNames, ", ") + ")" +
		" VALUES (" + strings.Join(placeholders(1, len(columnNames)), ", ") + ")" +
		" RETURNING to_jsonb(recovered_row)::text"
}

func columnIndex(schema tableSchema, columnName string) (int, error) {
	for index, column := range schema.Columns {
		if column.Name == columnName {
			return index, nil
		}
	}
	return -1, fmt.Errorf("table %s is missing column %s", schema.Name, columnName)
}

func rowPrimaryKeys(rows []recoveryRow) []string {
	identifiers := make([]string, 0, len(rows))
	for _, row := range rows {
		identifiers = append(identifiers, row.PrimaryKey)
	}
	return identifiers
}

func requiredStringValue(value any, fieldName string) (string, error) {
	stringValue, errorValue := optionalStringValue(value, fieldName)
	if errorValue != nil {
		return "", errorValue
	}
	if strings.TrimSpace(stringValue) == "" {
		return "", fmt.Errorf("%s must not be empty", fieldName)
	}
	return stringValue, nil
}

func optionalStringValue(value any, fieldName string) (string, error) {
	if value == nil {
		return "", nil
	}
	switch typedValue := value.(type) {
	case string:
		return typedValue, nil
	case []byte:
		return string(typedValue), nil
	default:
		return "", fmt.Errorf("%s has unsupported value type %T", fieldName, value)
	}
}

func placeholders(firstPosition int, count int) []string {
	values := make([]string, count)
	for index := range values {
		values[index] = "$" + strconv.Itoa(firstPosition+index)
	}
	return values
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func stringArguments(values []string) []any {
	arguments := make([]any, len(values))
	for index, value := range values {
		arguments[index] = value
	}
	return arguments
}

func identifierBatches(identifiers []string, batchSize int) [][]string {
	if len(identifiers) == 0 {
		return nil
	}
	sortedValues := append([]string{}, identifiers...)
	sort.Strings(sortedValues)
	batches := [][]string{}
	for startIndex := 0; startIndex < len(sortedValues); startIndex += batchSize {
		endIndex := startIndex + batchSize
		if endIndex > len(sortedValues) {
			endIndex = len(sortedValues)
		}
		batches = append(batches, sortedValues[startIndex:endIndex])
	}
	return batches
}

func sortedIdentifiers(identifiers map[string]bool) []string {
	values := make([]string, 0, len(identifiers))
	for identifier := range identifiers {
		values = append(values, identifier)
	}
	sort.Strings(values)
	return values
}

func ensureNoExcludedTables() error {
	excludedNames := map[string]bool{
		"task_wait_token":  true,
		"task_session":     true,
		"live_reply_post":  true,
		"connector_outbox": true,
		"raw_event":        true,
		"task_schedule":    true,
	}
	for _, definition := range recoveryTableDefinitions {
		if excludedNames[definition.Name] {
			return errors.New("recovery table set contains excluded table " + definition.Name)
		}
	}
	return nil
}
