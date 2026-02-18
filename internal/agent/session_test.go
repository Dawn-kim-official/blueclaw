package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueclaw/blueclaw/internal/provider"
)

func TestNewSessionSetsFields(t *testing.T) {
	before := time.Now()
	session := NewSession("test-id")
	after := time.Now()
	if session.ID != "test-id" {
		t.Errorf("expected ID %q, got %q", "test-id", session.ID)
	}
	if session.CreatedAt.Before(before) || session.CreatedAt.After(after) {
		t.Error("CreatedAt not within expected range")
	}
	if session.LastActivityAt.Before(before) || session.LastActivityAt.After(after) {
		t.Error("LastActivityAt not within expected range")
	}
	if len(session.Messages) != 0 {
		t.Errorf("expected empty messages, got %d", len(session.Messages))
	}
}

func TestAddMessageAppendsAndUpdatesActivity(t *testing.T) {
	session := NewSession("test-id")
	initialActivity := session.LastActivityAt
	time.Sleep(time.Millisecond)
	session.AddMessage(provider.Message{Role: "user", Content: "hello"})
	if len(session.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(session.Messages))
	}
	if session.Messages[0].Content != "hello" {
		t.Errorf("expected content %q, got %q", "hello", session.Messages[0].Content)
	}
	if !session.LastActivityAt.After(initialActivity) {
		t.Error("LastActivityAt not updated after AddMessage")
	}
}

func TestUserMessageHelper(t *testing.T) {
	message := UserMessage("test content")
	if message.Role != "user" {
		t.Errorf("expected role %q, got %q", "user", message.Role)
	}
	if message.Content != "test content" {
		t.Errorf("expected content %q, got %q", "test content", message.Content)
	}
}

func TestSaveAndLoadSession(t *testing.T) {
	temporaryDirectory := t.TempDir()
	session := NewSession("persist-test")
	session.AddMessage(provider.Message{Role: "user", Content: "hello"})
	session.AddMessage(provider.Message{Role: "assistant", Content: "hi there"})
	if err := session.Save(temporaryDirectory); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	filePath := filepath.Join(temporaryDirectory, "persist-test.json")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("session file not created: %v", err)
	}
	loaded, err := LoadSession(temporaryDirectory, "persist-test")
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}
	if loaded.ID != "persist-test" {
		t.Errorf("expected ID %q, got %q", "persist-test", loaded.ID)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "hello" {
		t.Errorf("expected first message %q, got %q", "hello", loaded.Messages[0].Content)
	}
	if loaded.Messages[1].Content != "hi there" {
		t.Errorf("expected second message %q, got %q", "hi there", loaded.Messages[1].Content)
	}
}

func TestLoadSessionNotFound(t *testing.T) {
	temporaryDirectory := t.TempDir()
	_, err := LoadSession(temporaryDirectory, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session, got nil")
	}
}

func TestLoadSessionInvalidJSON(t *testing.T) {
	temporaryDirectory := t.TempDir()
	filePath := filepath.Join(temporaryDirectory, "bad.json")
	os.WriteFile(filePath, []byte("{invalid json"), 0644)
	_, err := LoadSession(temporaryDirectory, "bad")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestUpdateActivity(t *testing.T) {
	session := NewSession("test-id")
	initialActivity := session.LastActivityAt
	time.Sleep(time.Millisecond)
	session.UpdateActivity()
	if !session.LastActivityAt.After(initialActivity) {
		t.Error("LastActivityAt not updated after UpdateActivity")
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	temporaryDirectory := t.TempDir()
	nestedDirectory := filepath.Join(temporaryDirectory, "sub", "sessions")
	session := NewSession("nested-test")
	if err := session.Save(nestedDirectory); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	filePath := filepath.Join(nestedDirectory, "nested-test.json")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("session file not created in nested directory: %v", err)
	}
}
