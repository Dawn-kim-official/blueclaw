package taskhistoryrecovery

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"blueclaw/internal/task"
)

const defaultSampleLimit = 10

var terminalTaskStatuses = []string{
	string(task.TaskStatusBlocked),
	string(task.TaskStatusCancelled),
	string(task.TaskStatusCompleted),
	string(task.TaskStatusFailed),
}

var recoveryTableDefinitions = []tableDefinition{
	{Name: "task_run", PrimaryKeyColumn: "task_run_id"},
	{Name: "task_attempt", PrimaryKeyColumn: "task_attempt_id"},
	{Name: "task_step", PrimaryKeyColumn: "task_step_id"},
	{Name: "task_event", PrimaryKeyColumn: "task_event_id"},
	{Name: "task_artifact", PrimaryKeyColumn: "task_artifact_id"},
}

type Options struct {
	Apply       bool
	SampleLimit int
	LockTimeout string
}

type Plan struct {
	Mode                    string              `json:"mode"`
	TerminalStatuses        []string            `json:"terminalStatuses"`
	SourceDatabaseDigest    string              `json:"sourceDatabaseDigest"`
	TargetDatabaseDigest    string              `json:"targetDatabaseDigest"`
	SourceCounts            SourceCounts        `json:"sourceCounts"`
	CandidateCounts         map[string]int64    `json:"candidateCounts"`
	ExpectedInsertCounts    map[string]int64    `json:"expectedInsertCounts"`
	InsertedCounts          map[string]int64    `json:"insertedCounts"`
	ExistingTaskRunCount    int64               `json:"existingTaskRunConflictCount"`
	ChildConflictCounts     map[string]int64    `json:"childIdentifierConflictCounts"`
	MissingRequesterCount   int64               `json:"missingRequesterPersonCount"`
	CandidateDigest         string              `json:"candidateDigest"`
	ExpectedInsertDigest    string              `json:"expectedInsertDigest"`
	CandidateTaskRunSamples []string            `json:"candidateTaskRunIDSamples"`
	ExpectedTaskRunSamples  []string            `json:"expectedTaskRunIDSamples"`
	ExistingTaskRunSamples  []string            `json:"existingTaskRunIDSamples"`
	ChildConflictSamples    map[string][]string `json:"childIdentifierConflictIDSamples"`
	MissingRequesterSamples []string            `json:"missingRequesterPersonIDSamples"`
	SafetyChecks            SafetyChecks        `json:"safetyChecks"`
	Applied                 bool                `json:"applied"`
}

type SourceCounts struct {
	Tables                   map[string]int64 `json:"tables"`
	TaskRunsByStatus         map[string]int64 `json:"taskRunsByStatus"`
	ExcludedNonterminalTasks int64            `json:"excludedNonterminalTaskRuns"`
}

type SafetyChecks struct {
	SchemasMatch                  bool `json:"schemasMatch"`
	DatabasesAreDistinct          bool `json:"databasesAreDistinct"`
	TargetAcceptsWrites           bool `json:"targetAcceptsWrites"`
	HasNoChildIdentifierConflicts bool `json:"hasNoChildIdentifierConflicts"`
	AllRequesterPersonsExist      bool `json:"allRequesterPersonsExist"`
}

type tableDefinition struct {
	Name             string
	PrimaryKeyColumn string
}

type tableSchema struct {
	Name              string
	Columns           []columnSchema
	PrimaryKeyColumns []string
}

type columnSchema struct {
	Name                   string
	OrdinalPosition        int
	DataType               string
	UserDefinedTypeSchema  string
	UserDefinedTypeName    string
	DomainSchema           string
	DomainName             string
	IsNullable             string
	ColumnDefault          string
	IsIdentity             string
	IdentityGeneration     string
	IsGenerated            string
	GenerationExpression   string
	CharacterMaximumLength string
	NumericPrecision       string
	NumericScale           string
	DateTimePrecision      string
	CollationSchema        string
	CollationName          string
}

type recoveryRow struct {
	PrimaryKey        string
	TaskRunID         string
	RequesterPersonID string
	Values            []any
	CanonicalJSON     string
}

type sourceSnapshot struct {
	DatabaseIdentity string
	Schemas          map[string]tableSchema
	Rows             map[string][]recoveryRow
	TableCounts      map[string]int64
	TaskRunsByStatus map[string]int64
}

type targetState struct {
	DatabaseIdentity          string
	AcceptsWrites             bool
	ExistingTaskRunIDs        map[string]bool
	ChildConflictIDs          map[string]map[string]bool
	MissingRequesterPersonIDs map[string]bool
}

type recoveryPlanState struct {
	Plan         Plan
	ExpectedRows map[string][]recoveryRow
}

func normalizeOptions(options Options) Options {
	if options.SampleLimit <= 0 {
		options.SampleLimit = defaultSampleLimit
	}
	if strings.TrimSpace(options.LockTimeout) == "" {
		options.LockTimeout = "5s"
	}
	return options
}

func buildRecoveryPlan(snapshot sourceSnapshot, target targetState, options Options) recoveryPlanState {
	expectedRows := filterRowsByTaskRunIDs(snapshot.Rows, target.ExistingTaskRunIDs)
	plan := Plan{
		Mode:                    recoveryMode(options.Apply),
		TerminalStatuses:        append([]string{}, terminalTaskStatuses...),
		SourceDatabaseDigest:    digestText(snapshot.DatabaseIdentity),
		TargetDatabaseDigest:    digestText(target.DatabaseIdentity),
		SourceCounts:            summarizeSourceCounts(snapshot),
		CandidateCounts:         countRows(snapshot.Rows),
		ExpectedInsertCounts:    countRows(expectedRows),
		InsertedCounts:          emptyTableCounts(),
		ExistingTaskRunCount:    int64(len(target.ExistingTaskRunIDs)),
		ChildConflictCounts:     countIdentifierSets(target.ChildConflictIDs),
		MissingRequesterCount:   int64(len(target.MissingRequesterPersonIDs)),
		CandidateDigest:         digestRecoveryRows(snapshot.Rows),
		ExpectedInsertDigest:    digestRecoveryRows(expectedRows),
		CandidateTaskRunSamples: sampleTaskRunIDs(snapshot.Rows["task_run"], options.SampleLimit),
		ExpectedTaskRunSamples:  sampleTaskRunIDs(expectedRows["task_run"], options.SampleLimit),
		ExistingTaskRunSamples:  sampleIdentifiers(target.ExistingTaskRunIDs, options.SampleLimit),
		ChildConflictSamples:    sampleIdentifierSets(target.ChildConflictIDs, options.SampleLimit),
		MissingRequesterSamples: sampleIdentifiers(target.MissingRequesterPersonIDs, options.SampleLimit),
		SafetyChecks: SafetyChecks{
			SchemasMatch:                  true,
			DatabasesAreDistinct:          snapshot.DatabaseIdentity != target.DatabaseIdentity,
			TargetAcceptsWrites:           target.AcceptsWrites,
			HasNoChildIdentifierConflicts: countIdentifiers(target.ChildConflictIDs) == 0,
			AllRequesterPersonsExist:      len(target.MissingRequesterPersonIDs) == 0,
		},
	}
	return recoveryPlanState{Plan: plan, ExpectedRows: expectedRows}
}

func summarizeSourceCounts(snapshot sourceSnapshot) SourceCounts {
	nonterminalCount := int64(0)
	for status, count := range snapshot.TaskRunsByStatus {
		if !isTerminalTaskStatus(status) {
			nonterminalCount += count
		}
	}
	return SourceCounts{
		Tables:                   cloneCounts(snapshot.TableCounts),
		TaskRunsByStatus:         cloneCounts(snapshot.TaskRunsByStatus),
		ExcludedNonterminalTasks: nonterminalCount,
	}
}

func filterRowsByTaskRunIDs(rowsByTable map[string][]recoveryRow, skippedTaskRunIDs map[string]bool) map[string][]recoveryRow {
	filteredRows := map[string][]recoveryRow{}
	for _, definition := range recoveryTableDefinitions {
		for _, row := range rowsByTable[definition.Name] {
			if skippedTaskRunIDs[row.TaskRunID] {
				continue
			}
			filteredRows[definition.Name] = append(filteredRows[definition.Name], row)
		}
	}
	return filteredRows
}

func countRows(rowsByTable map[string][]recoveryRow) map[string]int64 {
	counts := emptyTableCounts()
	for tableName, rows := range rowsByTable {
		counts[tableName] = int64(len(rows))
	}
	return counts
}

func emptyTableCounts() map[string]int64 {
	counts := map[string]int64{}
	for _, definition := range recoveryTableDefinitions {
		counts[definition.Name] = 0
	}
	return counts
}

func countIdentifierSets(identifierSets map[string]map[string]bool) map[string]int64 {
	counts := map[string]int64{}
	for _, definition := range recoveryTableDefinitions[1:] {
		counts[definition.Name] = int64(len(identifierSets[definition.Name]))
	}
	return counts
}

func countIdentifiers(identifierSets map[string]map[string]bool) int {
	count := 0
	for _, identifiers := range identifierSets {
		count += len(identifiers)
	}
	return count
}

func sampleTaskRunIDs(rows []recoveryRow, limit int) []string {
	identifiers := map[string]bool{}
	for _, row := range rows {
		identifiers[row.TaskRunID] = true
	}
	return sampleIdentifiers(identifiers, limit)
}

func sampleIdentifierSets(identifierSets map[string]map[string]bool, limit int) map[string][]string {
	samples := map[string][]string{}
	for _, definition := range recoveryTableDefinitions[1:] {
		samples[definition.Name] = sampleIdentifiers(identifierSets[definition.Name], limit)
	}
	return samples
}

func sampleIdentifiers(identifiers map[string]bool, limit int) []string {
	values := make([]string, 0, len(identifiers))
	for identifier := range identifiers {
		values = append(values, identifier)
	}
	sort.Strings(values)
	if len(values) > limit {
		values = values[:limit]
	}
	return values
}

func digestRecoveryRows(rowsByTable map[string][]recoveryRow) string {
	hashValue := sha256.New()
	for _, definition := range recoveryTableDefinitions {
		for _, row := range rowsByTable[definition.Name] {
			hashValue.Write([]byte(definition.Name))
			hashValue.Write([]byte{0})
			hashValue.Write([]byte(row.CanonicalJSON))
			hashValue.Write([]byte{'\n'})
		}
	}
	return hex.EncodeToString(hashValue.Sum(nil))
}

func digestText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])[:16]
}

func cloneCounts(counts map[string]int64) map[string]int64 {
	clonedCounts := map[string]int64{}
	for name, count := range counts {
		clonedCounts[name] = count
	}
	return clonedCounts
}

func recoveryMode(isApply bool) string {
	if isApply {
		return "apply"
	}
	return "dry-run"
}

func isTerminalTaskStatus(status string) bool {
	for _, terminalStatus := range terminalTaskStatuses {
		if status == terminalStatus {
			return true
		}
	}
	return false
}
