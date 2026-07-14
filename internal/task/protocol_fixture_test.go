package task

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestProtocolTaskRunFixtureMatchesTaskRun(t *testing.T) {
	documentBytes, errorValue := os.ReadFile("../../protocol/fixtures/valid/task-run.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var taskRun TaskRun
	if errorValue := json.Unmarshal(documentBytes, &taskRun); errorValue != nil {
		t.Fatal(errorValue)
	}
	if taskRun.TaskRunID != "task-1" || taskRun.Status != TaskStatusRunning {
		t.Fatalf("unexpected task run fixture: %#v", taskRun)
	}
	if taskRun.CreatedAt.Format(time.RFC3339) != "2026-07-14T00:00:00Z" {
		t.Fatalf("task run fixture lost RFC3339 timestamp: %s", taskRun.CreatedAt)
	}
}
