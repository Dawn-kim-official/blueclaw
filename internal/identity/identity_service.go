package identity

import (
	"strings"
	"sync"

	"blueclaw/internal/policy"
)

type IdentityService struct {
	mutex                        sync.RWMutex
	personIDByEmail              map[string]string
	personIDByPlatformAccountKey map[string]string
}

func NewIdentityService(policyProjection policy.PolicyProjection) *IdentityService {
	identityService := &IdentityService{
		personIDByEmail:              map[string]string{},
		personIDByPlatformAccountKey: map[string]string{},
	}

	for email, personID := range policyProjection.PersonIDByEmail {
		identityService.personIDByEmail[email] = personID
	}

	return identityService
}

func (identityService *IdentityService) ResolvePersonIDByEmail(email string) (string, bool) {
	identityService.mutex.RLock()
	defer identityService.mutex.RUnlock()

	personID, isFound := identityService.personIDByEmail[strings.ToLower(strings.TrimSpace(email))]
	return personID, isFound
}

func (identityService *IdentityService) ResolvePersonIDByPlatformAccount(platform string, externalUserID string) (string, bool) {
	identityService.mutex.RLock()
	defer identityService.mutex.RUnlock()

	personID, isFound := identityService.personIDByPlatformAccountKey[platform+":"+externalUserID]
	return personID, isFound
}

func (identityService *IdentityService) IsApprovedInternalEmail(email string) bool {
	_, isFound := identityService.ResolvePersonIDByEmail(email)
	return isFound
}

func (identityService *IdentityService) RememberPlatformAccount(platformAccountIdentity PlatformAccountIdentity) {
	identityService.mutex.Lock()
	defer identityService.mutex.Unlock()

	normalizedEmail := strings.ToLower(strings.TrimSpace(platformAccountIdentity.Email))
	personID, isFound := identityService.personIDByEmail[normalizedEmail]
	if !isFound {
		return
	}

	identityService.personIDByPlatformAccountKey[platformAccountIdentity.Platform+":"+platformAccountIdentity.ExternalUserID] = personID
}
