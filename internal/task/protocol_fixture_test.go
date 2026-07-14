package task

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestProtocolTaskRunFixtureMatchesTaskRun(t *testing.T) {
	var taskRun TaskRun
	if errorValue := json.Unmarshal(protocolTaskFixture(t, "task-run"), &taskRun); errorValue != nil {
		t.Fatal(errorValue)
	}
	if taskRun.TaskRunID != "task-1" || taskRun.Status != TaskStatusRunning {
		t.Fatalf("unexpected task run fixture: %#v", taskRun)
	}
	if taskRun.CreatedAt.Format(time.RFC3339) != "2026-07-14T00:00:00Z" {
		t.Fatalf("task run fixture lost RFC3339 timestamp: %s", taskRun.CreatedAt)
	}
}

func protocolTaskFixture(t *testing.T, fixtureName string) json.RawMessage {
	t.Helper()
	documentBytes, errorValue := os.ReadFile("../../protocol/fixtures/valid.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var fixtures map[string][]json.RawMessage
	if errorValue := json.Unmarshal(documentBytes, &fixtures); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(fixtures[fixtureName]) != 1 {
		t.Fatalf("expected one %s fixture", fixtureName)
	}
	return fixtures[fixtureName][0]
}
