package agentruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"blueclaw/internal/access"
	"blueclaw/internal/agent"
	"blueclaw/internal/security"

	_ "modernc.org/sqlite"
)

const maximumDatabaseResultRows = 1000

type databaseSQLToolInput struct {
	SQL   string `json:"sql"`
	Scope string `json:"scope"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerDatabaseTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[databaseSQLToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "db.sql",
			Description: "Run SQL against a persistent SQLite database for this requester scope. Each scope keeps one database that survives across turns and tasks, and you can only reach data your account is allowed to see. Every result lists the current tables and their schema; reuse an existing table when it already fits instead of creating a duplicate.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Executes one SQL statement against the scope SQLite database, creating the database on first use, and returns the current table schema.",
				Produces:   "Result rows with columns for reads or affected row count for writes, plus the list of existing tables and their CREATE statements.",
				SideEffect: "workspace_write",
				UseWhen:    "You need structured records you will query, filter, count, or aggregate across many rows, such as an expense log, a task tracker, or a collected dataset.",
				AvoidWhen:  "You only need to recall a few facts, preferences, or relationships by meaning later; use memory.remember and memory.search for those, and file.write for plain text artifacts.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string","description":"One SQL statement to run, for example CREATE TABLE, INSERT, or SELECT."},"scope":{"type":"string","description":"Database scope: \"me\" for your private database, or \"circle:<id>\" for a circle you belong to. Defaults to the current conversation scope."}},"required":["sql"]}`),
		},
		Handler: func(toolContext context.Context, input databaseSQLToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.runDatabaseSQLTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) runDatabaseSQLTool(toolContext context.Context, input databaseSQLToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	sqlText := strings.TrimSpace(input.SQL)
	if sqlText == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "db_sql", "sql is required"), nil
	}
	scope := normalizeDatabaseScope(input.Scope, handlerContext.conversationScope)
	databasePathInput, scopeFailure := databaseScopePathInput(scope)
	if scopeFailure != nil {
		return *scopeFailure, nil
	}
	workspaceScope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(databasePathInput, workspaceScope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "db_sql", errorValue.Error()), nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, resolvedPath.ConcretePath) {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "db_sql", "current account cannot use this scope database"), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	if ensureFailure := ensureDatabaseFile(toolContext, workspaceActor, resolvedPath); ensureFailure != nil {
		return *ensureFailure, nil
	}
	result, errorValue := executeScopeDatabaseSQL(toolContext, resolvedPath.ConcretePath, sqlText)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "db_sql", errorValue.Error()), nil
	}
	result["scope"] = scope
	result["path"] = resolvedPath.VirtualPath
	return agent.ToolSuccess(marshalToolResult(result)), nil
}

func normalizeDatabaseScope(requestedScope string, conversationScope ConversationResourceScope) string {
	trimmedScope := strings.ToLower(strings.TrimSpace(requestedScope))
	if trimmedScope != "" {
		return trimmedScope
	}
	if conversationScope.Kind == "circle" && strings.TrimSpace(conversationScope.CircleID) != "" {
		return "circle:" + strings.ToLower(strings.TrimSpace(conversationScope.CircleID))
	}
	return "me"
}

func databaseScopePathInput(scope string) (string, *agent.ToolResult) {
	if scope == "me" {
		return "home/agent/store.db", nil
	}
	if circleID, isCircleScope := strings.CutPrefix(scope, "circle:"); isCircleScope {
		trimmedCircleID := strings.TrimSpace(circleID)
		if trimmedCircleID == "" {
			result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "db_sql", "circle scope needs a circle id, for example circle:staff")
			return "", &result
		}
		return "/workspace/circles/" + trimmedCircleID + "/agent/store.db", nil
	}
	result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "db_sql", `scope must be "me" or "circle:<id>"`)
	return "", &result
}

func ensureDatabaseFile(toolContext context.Context, workspaceActor security.WorkspaceActor, resolvedPath ResolvedWorkspacePath) *agent.ToolResult {
	parentDirectory := resolvedPath.Parent()
	if errorValue := workspaceActor.MkdirAll(toolContext, parentDirectory, workspaceDirectoryCreateMode(parentDirectory)); errorValue != nil {
		failure := actorToolFailure("mkdir_all", "db_sql", resolvedPath.VirtualPath, errorValue)
		return &failure
	}
	fileInformation, statError := workspaceActor.Stat(toolContext, resolvedPath)
	if statError == nil {
		if fileInformation.IsDirectory {
			failure := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "db_sql", "scope database path is a directory")
			return &failure
		}
		return nil
	}
	if actorFailureCode(statError) != security.ActorErrorCodeNotFound {
		failure := actorToolFailure("stat", "db_sql", resolvedPath.VirtualPath, statError)
		return &failure
	}
	if errorValue := workspaceActor.WriteFile(toolContext, resolvedPath, []byte{}, workspaceFileCreateMode(resolvedPath)); errorValue != nil {
		failure := actorToolFailure("write_file", "db_sql", resolvedPath.VirtualPath, errorValue)
		return &failure
	}
	return nil
}

func executeScopeDatabaseSQL(toolContext context.Context, databasePath string, sqlText string) (map[string]any, error) {
	database, errorValue := sql.Open("sqlite", "file:"+databasePath+"?_pragma=busy_timeout(5000)")
	if errorValue != nil {
		return nil, errorValue
	}
	defer database.Close()
	result, errorValue := runScopeDatabaseStatement(toolContext, database, sqlText)
	if errorValue != nil {
		return nil, errorValue
	}
	result["tables"] = databaseSchema(toolContext, database)
	return result, nil
}

func runScopeDatabaseStatement(toolContext context.Context, database *sql.DB, sqlText string) (map[string]any, error) {
	if isReadDatabaseSQL(sqlText) {
		return runReadDatabaseSQL(toolContext, database, sqlText)
	}
	return runWriteDatabaseSQL(toolContext, database, sqlText)
}

func databaseSchema(toolContext context.Context, database *sql.DB) []map[string]any {
	tables := []map[string]any{}
	rows, errorValue := database.QueryContext(toolContext, "SELECT name, sql FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if errorValue != nil {
		return tables
	}
	defer rows.Close()
	for rows.Next() {
		var tableName string
		var createStatement sql.NullString
		if errorValue := rows.Scan(&tableName, &createStatement); errorValue != nil {
			return tables
		}
		tables = append(tables, map[string]any{"name": tableName, "sql": strings.TrimSpace(createStatement.String)})
	}
	return tables
}

func isReadDatabaseSQL(sqlText string) bool {
	head := strings.ToUpper(strings.TrimSpace(sqlText))
	return strings.HasPrefix(head, "SELECT") ||
		strings.HasPrefix(head, "PRAGMA") ||
		strings.HasPrefix(head, "WITH") ||
		strings.HasPrefix(head, "EXPLAIN") ||
		strings.HasPrefix(head, "VALUES")
}

func runWriteDatabaseSQL(toolContext context.Context, database *sql.DB, sqlText string) (map[string]any, error) {
	execResult, errorValue := database.ExecContext(toolContext, sqlText)
	if errorValue != nil {
		return nil, errorValue
	}
	rowsAffected, _ := execResult.RowsAffected()
	return map[string]any{"ok": true, "rowsAffected": rowsAffected}, nil
}

func runReadDatabaseSQL(toolContext context.Context, database *sql.DB, sqlText string) (map[string]any, error) {
	rows, errorValue := database.QueryContext(toolContext, sqlText)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	columns, errorValue := rows.Columns()
	if errorValue != nil {
		return nil, errorValue
	}
	resultRows := []map[string]any{}
	isTruncated := false
	for rows.Next() {
		if len(resultRows) >= maximumDatabaseResultRows {
			isTruncated = true
			break
		}
		rowValues := make([]any, len(columns))
		rowValuePointers := make([]any, len(columns))
		for index := range rowValues {
			rowValuePointers[index] = &rowValues[index]
		}
		if errorValue := rows.Scan(rowValuePointers...); errorValue != nil {
			return nil, errorValue
		}
		resultRows = append(resultRows, databaseRowMap(columns, rowValues))
	}
	if errorValue := rows.Err(); errorValue != nil {
		return nil, errorValue
	}
	return map[string]any{
		"columns":   columns,
		"rows":      resultRows,
		"rowCount":  len(resultRows),
		"truncated": isTruncated,
	}, nil
}

func databaseRowMap(columns []string, values []any) map[string]any {
	row := map[string]any{}
	for index, column := range columns {
		row[column] = normalizeDatabaseValue(values[index])
	}
	return row
}

func normalizeDatabaseValue(value any) any {
	if bytesValue, isBytes := value.([]byte); isBytes {
		return string(bytesValue)
	}
	return value
}
