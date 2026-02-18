package outbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ProactiveMessage struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

type Outbox struct {
	directory string
}

func NewOutbox(blueclawDirectory string) *Outbox {
	return &Outbox{
		directory: filepath.Join(blueclawDirectory, "outbox"),
	}
}

func (outbox *Outbox) Write(source string, content string) error {
	if err := os.MkdirAll(outbox.directory, 0755); err != nil {
		return fmt.Errorf("creating outbox directory: %w", err)
	}
	message := ProactiveMessage{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Source:    source,
		Content:   content,
		CreatedAt: time.Now(),
	}
	data, err := json.MarshalIndent(message, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling outbox message: %w", err)
	}
	filePath := filepath.Join(outbox.directory, message.ID+".json")
	return os.WriteFile(filePath, data, 0644)
}

func (outbox *Outbox) List() ([]ProactiveMessage, error) {
	entries, err := os.ReadDir(outbox.directory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading outbox: %w", err)
	}
	messages := make([]ProactiveMessage, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		filePath := filepath.Join(outbox.directory, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		var message ProactiveMessage
		if err := json.Unmarshal(data, &message); err != nil {
			continue
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func (outbox *Outbox) Clear() error {
	entries, err := os.ReadDir(outbox.directory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading outbox for clear: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		filePath := filepath.Join(outbox.directory, entry.Name())
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing outbox message: %w", err)
		}
	}
	return nil
}
