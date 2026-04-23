package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type MagicLink struct {
	TokenHash  string
	PersonID   string
	TaskRunID  string
	ExpiresAt  time.Time
	ConsumedAt time.Time
}

type MagicLinkService struct {
	mutex      sync.RWMutex
	magicLinks map[string]MagicLink
}

func NewMagicLinkService() *MagicLinkService {
	return &MagicLinkService{
		magicLinks: map[string]MagicLink{},
	}
}

func (magicLinkService *MagicLinkService) IssueMagicLink(personID string, taskRunID string, lifetime time.Duration) (string, error) {
	tokenBytes := make([]byte, 32)
	_, errorValue := rand.Read(tokenBytes)
	if errorValue != nil {
		return "", errorValue
	}

	token := hex.EncodeToString(tokenBytes)
	tokenHash := hashToken(token)

	magicLinkService.mutex.Lock()
	defer magicLinkService.mutex.Unlock()

	magicLinkService.magicLinks[tokenHash] = MagicLink{
		TokenHash: tokenHash,
		PersonID:  personID,
		TaskRunID: taskRunID,
		ExpiresAt: time.Now().Add(lifetime),
	}

	return token, nil
}

func (magicLinkService *MagicLinkService) ConsumeMagicLink(token string) (MagicLink, bool) {
	magicLinkService.mutex.Lock()
	defer magicLinkService.mutex.Unlock()

	tokenHash := hashToken(token)
	magicLink, isFound := magicLinkService.magicLinks[tokenHash]
	if !isFound {
		return MagicLink{}, false
	}
	if time.Now().After(magicLink.ExpiresAt) || !magicLink.ConsumedAt.IsZero() {
		return MagicLink{}, false
	}

	magicLink.ConsumedAt = time.Now()
	magicLinkService.magicLinks[tokenHash] = magicLink
	return magicLink, true
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
