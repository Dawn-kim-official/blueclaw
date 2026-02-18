package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blueclaw/blueclaw/internal/provider"
)

type Session struct {
	ID             string             `json:"id"`
	ContainerID    string             `json:"containerId"`
	CreatedAt      time.Time          `json:"createdAt"`
	LastActivityAt time.Time          `json:"lastActivityAt"`
	Messages       []provider.Message `json:"messages"`
}

func NewSession(sessionID string) *Session {
	now := time.Now()
	return &Session{
		ID:             sessionID,
		CreatedAt:      now,
		LastActivityAt: now,
		Messages:       make([]provider.Message, 0),
	}
}

func LoadSession(sessionsDirectory string, sessionID string) (*Session, error) {
	filePath := sessionFilePath(sessionsDirectory, sessionID)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("loading session %s: %w", sessionID, err)
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parsing session %s: %w", sessionID, err)
	}
	return &session, nil
}

func (session *Session) AddMessage(message provider.Message) {
	session.Messages = append(session.Messages, message)
	session.LastActivityAt = time.Now()
}

func (session *Session) UpdateActivity() {
	session.LastActivityAt = time.Now()
}

func (session *Session) Save(sessionsDirectory string) error {
	if err := os.MkdirAll(sessionsDirectory, 0755); err != nil {
		return fmt.Errorf("creating sessions directory: %w", err)
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session %s: %w", session.ID, err)
	}
	filePath := sessionFilePath(sessionsDirectory, session.ID)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("writing session %s: %w", session.ID, err)
	}
	return nil
}

func UserMessage(content string) provider.Message {
	return provider.Message{Role: "user", Content: content}
}

func sessionFilePath(sessionsDirectory string, sessionID string) string {
	return filepath.Join(sessionsDirectory, sessionID+".json")
}
