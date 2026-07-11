package taskhistoryrecovery

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildRecoveryPlanSkipsExistingTaskRunAndEveryChild(t *testing.T) {
	snapshot := recoveryTestSnapshot()
	target := targetState{
		DatabaseIdentity:          "target",
		AcceptsWrites:             true,
		ExistingTaskRunIDs:        map[string]bool{"existing-run": true},
		ChildConflictIDs:          emptyChildIdentifierSets(),
		MissingRequesterPersonIDs: map[string]bool{},
	}

	planState := buildRecoveryPlan(snapshot, target, Options{SampleLimit: 10})

	if planState.Plan.ExistingTaskRunCount != 1 {
		t.Fatalf("expected one existing task conflict, got %d", planState.Plan.ExistingTaskRunCount)
	}
	for tableName, rows := range planState.ExpectedRows {
		for _, row := range rows {
			if row.TaskRunID == "existing-run" {
				t.Fatalf("existing task child remained in %s", tableName)
			}
		}
	}
	if planState.Plan.ExpectedInsertCounts["task_run"] != 1 {
		t.Fatalf("expected one task run insert, got %d", planState.Plan.ExpectedInsertCounts["task_run"])
	}
	if planState.Plan.ExpectedInsertCounts["task_event"] != 1 {
		t.Fatalf("expected one task event insert, got %d", planState.Plan.ExpectedInsertCounts["task_event"])
	}
	if !reflect.DeepEqual(planState.Plan.ExpectedTaskRunSamples, []string{"new-run"}) {
		t.Fatalf("unexpected expected task sample: %v", planState.Plan.ExpectedTaskRunSamples)
	}
}

func TestBuildRecoveryPlanReportsExcludedNonterminalTasks(t *testing.T) {
	snapshot := recoveryTestSnapshot()
	snapshot.TaskRunsByStatus = map[string]int64{
		"blocked":   1,
		"completed": 2,
		"failed":    3,
		"running":   4,
		"waiting":   5,
	}

	planState := buildRecoveryPlan(snapshot, emptyRecoveryTarget(), Options{})

	if planState.Plan.SourceCounts.ExcludedNonterminalTasks != 9 {
		t.Fatalf("expected nine excluded nonterminal tasks, got %d", planState.Plan.SourceCounts.ExcludedNonterminalTasks)
	}
}

func TestBuildRecoveryPlanIncludesDigestAndSortedIdentifierSamples(t *testing.T) {
	snapshot := recoveryTestSnapshot()
	target := emptyRecoveryTarget()
	target.ExistingTaskRunIDs = map[string]bool{"z-run": true, "a-run": true}

	planState := buildRecoveryPlan(snapshot, target, Options{SampleLimit: 1})

	if len(planState.Plan.CandidateDigest) != 64 || len(planState.Plan.ExpectedInsertDigest) != 64 {
		t.Fatal("expected SHA-256 recovery digests")
	}
	if !reflect.DeepEqual(planState.Plan.ExistingTaskRunSamples, []string{"a-run"}) {
		t.Fatalf("unexpected conflict sample: %v", planState.Plan.ExistingTaskRunSamples)
	}
}

func TestValidatePlanForApplyRejectsChildConflictAndMissingRequester(t *testing.T) {
	plan := Plan{SafetyChecks: SafetyChecks{
		SchemasMatch:                  true,
		DatabasesAreDistinct:          true,
		TargetAcceptsWrites:           true,
		HasNoChildIdentifierConflicts: false,
		AllRequesterPersonsExist:      false,
	}}

	errorValue := validatePlanForApply(plan)

	if errorValue == nil {
		t.Fatal("expected unsafe plan rejection")
	}
	for _, expectedText := range []string{"child row identifier conflicts", "missing requester people"} {
		if !strings.Contains(errorValue.Error(), expectedText) {
			t.Fatalf("expected %q in %q", expectedText, errorValue)
		}
	}
}

func TestRecoveryTableSetContainsOnlyTaskHistory(t *testing.T) {
	expectedNames := []string{"task_run", "task_attempt", "task_step", "task_event", "task_artifact"}
	actualNames := []string{}
	for _, definition := range recoveryTableDefinitions {
		actualNames = append(actualNames, definition.Name)
	}

	if !reflect.DeepEqual(actualNames, expectedNames) {
		t.Fatalf("unexpected recovery table set: %v", actualNames)
	}
	if errorValue := ensureNoExcludedTables(); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestCompareRecoverySchemasRejectsColumnDrift(t *testing.T) {
	sourceSchemas := recoveryTestSchemas()
	targetSchemas := recoveryTestSchemas()
	targetSchema := targetSchemas["task_event"]
	targetSchema.Columns[0].DataType = "uuid"
	targetSchemas["task_event"] = targetSchema

	errorValue := compareRecoverySchemas(sourceSchemas, targetSchemas)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "task_event") {
		t.Fatalf("expected task_event schema drift rejection, got %v", errorValue)
	}
}

func TestBuildInsertStatementHasNoConflictOrMutationClause(t *testing.T) {
	definition := tableDefinition{Name: "task_event", PrimaryKeyColumn: "task_event_id"}
	schema := tableSchema{Columns: []columnSchema{{Name: "task_event_id"}, {Name: "task_run_id"}}}

	statement := buildInsertStatement(definition, schema)

	for _, forbiddenText := range []string{"ON CONFLICT", "UPDATE", "DELETE"} {
		if strings.Contains(statement, forbiddenText) {
			t.Fatalf("append-only insert contains %s: %s", forbiddenText, statement)
		}
	}
	if !strings.Contains(statement, `INSERT INTO public."task_event"`) {
		t.Fatalf("unexpected insert statement: %s", statement)
	}
}

func TestBuildTerminalRowsQueryFiltersThroughTerminalParent(t *testing.T) {
	definition := tableDefinition{Name: "task_event", PrimaryKeyColumn: "task_event_id"}
	schema := tableSchema{Columns: []columnSchema{{Name: "task_event_id"}, {Name: "task_run_id"}}}

	query := buildTerminalRowsQuery(definition, schema)

	for _, expectedText := range []string{"JOIN public.task_run", "terminal_run.status IN", "$1", "$4"} {
		if !strings.Contains(query, expectedText) {
			t.Fatalf("expected %q in %s", expectedText, query)
		}
	}
}

func recoveryTestSnapshot() sourceSnapshot {
	rows := map[string][]recoveryRow{}
	for _, definition := range recoveryTableDefinitions {
		existingPrimaryKey := definition.PrimaryKeyColumn + "-existing"
		newPrimaryKey := definition.PrimaryKeyColumn + "-new"
		if definition.Name == "task_run" {
			existingPrimaryKey = "existing-run"
			newPrimaryKey = "new-run"
		}
		rows[definition.Name] = []recoveryRow{
			recoveryTestRow(existingPrimaryKey, "existing-run"),
			recoveryTestRow(newPrimaryKey, "new-run"),
		}
	}
	return sourceSnapshot{
		DatabaseIdentity: "source",
		Rows:             rows,
		TableCounts:      countRows(rows),
		TaskRunsByStatus: map[string]int64{"completed": 2},
	}
}

func recoveryTestRow(primaryKey string, taskRunID string) recoveryRow {
	return recoveryRow{
		PrimaryKey:    primaryKey,
		TaskRunID:     taskRunID,
		CanonicalJSON: `{"id":"` + primaryKey + `"}`,
	}
}

func emptyRecoveryTarget() targetState {
	return targetState{
		DatabaseIdentity:          "target",
		AcceptsWrites:             true,
		ExistingTaskRunIDs:        map[string]bool{},
		ChildConflictIDs:          emptyChildIdentifierSets(),
		MissingRequesterPersonIDs: map[string]bool{},
	}
}

func emptyChildIdentifierSets() map[string]map[string]bool {
	identifierSets := map[string]map[string]bool{}
	for _, definition := range recoveryTableDefinitions[1:] {
		identifierSets[definition.Name] = map[string]bool{}
	}
	return identifierSets
}

func recoveryTestSchemas() map[string]tableSchema {
	schemas := map[string]tableSchema{}
	for _, definition := range recoveryTableDefinitions {
		schemas[definition.Name] = tableSchema{
			Name: definition.Name,
			Columns: []columnSchema{{
				Name:        definition.PrimaryKeyColumn,
				DataType:    "text",
				IsGenerated: "NEVER",
			}},
			PrimaryKeyColumns: []string{definition.PrimaryKeyColumn},
		}
	}
	return schemas
}
