package outbox

import (
	"testing"
)

func TestWriteAndList(t *testing.T) {
	temporaryDirectory := t.TempDir()
	messageOutbox := NewOutbox(temporaryDirectory)
	if err := messageOutbox.Write("heartbeat", "test message"); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	messages, err := messageOutbox.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Source != "heartbeat" {
		t.Errorf("expected source %q, got %q", "heartbeat", messages[0].Source)
	}
	if messages[0].Content != "test message" {
		t.Errorf("expected content %q, got %q", "test message", messages[0].Content)
	}
}

func TestClearRemovesMessages(t *testing.T) {
	temporaryDirectory := t.TempDir()
	messageOutbox := NewOutbox(temporaryDirectory)
	messageOutbox.Write("heartbeat", "message one")
	messageOutbox.Write("cron:job1", "message two")
	messages, _ := messageOutbox.List()
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages before clear, got %d", len(messages))
	}
	if err := messageOutbox.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
	messagesAfter, _ := messageOutbox.List()
	if len(messagesAfter) != 0 {
		t.Errorf("expected 0 messages after clear, got %d", len(messagesAfter))
	}
}

func TestListEmptyOutbox(t *testing.T) {
	temporaryDirectory := t.TempDir()
	messageOutbox := NewOutbox(temporaryDirectory)
	messages, err := messageOutbox.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(messages))
	}
}

func TestClearEmptyOutbox(t *testing.T) {
	temporaryDirectory := t.TempDir()
	messageOutbox := NewOutbox(temporaryDirectory)
	if err := messageOutbox.Clear(); err != nil {
		t.Fatalf("Clear on empty outbox failed: %v", err)
	}
}
