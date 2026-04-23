package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type Session struct {
	SessionID string
	PersonID  string
	ExpiresAt time.Time
}

type SessionService struct {
	mutex    sync.RWMutex
	sessions map[string]Session
}

func NewSessionService() *SessionService {
	return &SessionService{
		sessions: map[string]Session{},
	}
}

func (sessionService *SessionService) CreateSession(personID string, lifetime time.Duration) (Session, error) {
	sessionBytes := make([]byte, 24)
	_, errorValue := rand.Read(sessionBytes)
	if errorValue != nil {
		return Session{}, errorValue
	}

	session := Session{
		SessionID: hex.EncodeToString(sessionBytes),
		PersonID:  personID,
		ExpiresAt: time.Now().Add(lifetime),
	}

	sessionService.mutex.Lock()
	defer sessionService.mutex.Unlock()
	sessionService.sessions[session.SessionID] = session
	return session, nil
}

func (sessionService *SessionService) ResolveSession(sessionID string) (Session, bool) {
	sessionService.mutex.RLock()
	defer sessionService.mutex.RUnlock()

	session, isFound := sessionService.sessions[sessionID]
	if !isFound || time.Now().After(session.ExpiresAt) {
		return Session{}, false
	}

	return session, true
}
