package agentruntime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseScopePathInput(t *testing.T) {
	cases := []struct {
		scope         string
		expectedPath  string
		expectFailure bool
	}{
		{scope: "me", expectedPath: "home/agent/store.db"},
		{scope: "circle:staff", expectedPath: "/workspace/circles/staff/agent/store.db"},
		{scope: "circle:", expectFailure: true},
		{scope: "shared", expectFailure: true},
		{scope: "bogus", expectFailure: true},
	}
	for _, testCase := range cases {
		path, failure := databaseScopePathInput(testCase.scope)
		if testCase.expectFailure {
			if failure == nil {
				t.Fatalf("scope %q should be rejected", testCase.scope)
			}
			continue
		}
		if failure != nil {
			t.Fatalf("scope %q should be accepted, got failure", testCase.scope)
		}
		if path != testCase.expectedPath {
			t.Fatalf("scope %q path = %q, want %q", testCase.scope, path, testCase.expectedPath)
		}
	}
}

func TestNormalizeDatabaseScope(t *testing.T) {
	if scope := normalizeDatabaseScope("Circle:Finance", ConversationResourceScope{}); scope != "circle:finance" {
		t.Fatalf("explicit scope = %q, want circle:finance", scope)
	}
	circleScope := normalizeDatabaseScope("", ConversationResourceScope{Kind: "circle", CircleID: "Staff"})
	if circleScope != "circle:staff" {
		t.Fatalf("circle conversation default = %q, want circle:staff", circleScope)
	}
	if scope := normalizeDatabaseScope("", ConversationResourceScope{Kind: "private", PersonID: "lee"}); scope != "me" {
		t.Fatalf("private conversation default = %q, want me", scope)
	}
	if scope := normalizeDatabaseScope("", ConversationResourceScope{Kind: "workspace"}); scope != "me" {
		t.Fatalf("workspace conversation default = %q, want me", scope)
	}
}

func TestIsReadDatabaseSQL(t *testing.T) {
	reads := []string{"SELECT 1", "  select * from t", "WITH x AS (SELECT 1) SELECT * FROM x", "PRAGMA table_info(t)"}
	writes := []string{"CREATE TABLE t(id INTEGER)", "INSERT INTO t VALUES (1)", "UPDATE t SET id = 2", "DELETE FROM t"}
	for _, sqlText := range reads {
		if !isReadDatabaseSQL(sqlText) {
			t.Fatalf("%q should be read", sqlText)
		}
	}
	for _, sqlText := range writes {
		if isReadDatabaseSQL(sqlText) {
			t.Fatalf("%q should be write", sqlText)
		}
	}
}

func TestExecuteScopeDatabaseSQLRoundTrip(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "store.db")
	toolContext := context.Background()

	if _, errorValue := executeScopeDatabaseSQL(toolContext, databasePath, "CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"); errorValue != nil {
		t.Fatalf("create table failed: %v", errorValue)
	}
	insertResult, errorValue := executeScopeDatabaseSQL(toolContext, databasePath, "INSERT INTO notes(body) VALUES ('hello')")
	if errorValue != nil {
		t.Fatalf("insert failed: %v", errorValue)
	}
	if insertResult["rowsAffected"].(int64) != 1 {
		t.Fatalf("rowsAffected = %v, want 1", insertResult["rowsAffected"])
	}
	selectResult, errorValue := executeScopeDatabaseSQL(toolContext, databasePath, "SELECT id, body FROM notes")
	if errorValue != nil {
		t.Fatalf("select failed: %v", errorValue)
	}
	rows := selectResult["rows"].([]map[string]any)
	if len(rows) != 1 || rows[0]["body"] != "hello" {
		t.Fatalf("select rows = %v, want one row body=hello", rows)
	}

	tables := selectResult["tables"].([]map[string]any)
	if len(tables) != 1 || tables[0]["name"] != "notes" {
		t.Fatalf("schema should list the notes table for reuse, got %v", tables)
	}
	if !strings.Contains(tables[0]["sql"].(string), "CREATE TABLE") {
		t.Fatalf("schema should include the CREATE statement, got %v", tables[0]["sql"])
	}
}

func TestExecuteScopeDatabaseSQLReportsError(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "store.db")
	if _, errorValue := executeScopeDatabaseSQL(context.Background(), databasePath, "SELECT * FROM missing_table"); errorValue == nil {
		t.Fatal("query against missing table should error so the model can fix its SQL")
	}
}
