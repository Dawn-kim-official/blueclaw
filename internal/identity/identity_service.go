package identity

import (
	"strings"
	"sync"

	"blueclaw/internal/policy"
)

type PlatformAccountRepository interface {
	UpsertPlatformAccount(PlatformAccountIdentity) error
	ListPlatformAccount() ([]PlatformAccountIdentity, error)
}

type IdentityService struct {
	mutex                        sync.RWMutex
	personIDByEmail              map[string]string
	personIDByPlatformAccountKey map[string]string
	platformAccountRepository    PlatformAccountRepository
}

func NewIdentityService(policyProjection policy.PolicyProjection) *IdentityService {
	identityService := &IdentityService{
		personIDByEmail:              map[string]string{},
		personIDByPlatformAccountKey: map[string]string{},
	}

	identityService.reloadPolicyProjection(policyProjection)

	return identityService
}

func (identityService *IdentityService) UsePlatformAccountRepository(repository PlatformAccountRepository) {
	identityService.mutex.Lock()
	defer identityService.mutex.Unlock()
	identityService.platformAccountRepository = repository
	identityService.reloadPlatformAccounts()
}

func (identityService *IdentityService) ReloadPolicyProjection(policyProjection policy.PolicyProjection) {
	identityService.mutex.Lock()
	defer identityService.mutex.Unlock()
	identityService.reloadPolicyProjection(policyProjection)
	identityService.reloadPlatformAccounts()
}

func (identityService *IdentityService) reloadPolicyProjection(policyProjection policy.PolicyProjection) {
	identityService.personIDByEmail = map[string]string{}
	identityService.personIDByPlatformAccountKey = map[string]string{}
	for email, personID := range policyProjection.PersonIDByEmail {
		identityService.personIDByEmail[email] = personID
	}
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
	platformAccountIdentity.PersonID = personID
	_ = identityService.savePlatformAccount(platformAccountIdentity)
}

func (identityService *IdentityService) reloadPlatformAccounts() {
	if identityService.platformAccountRepository == nil {
		return
	}
	platformAccounts, errorValue := identityService.platformAccountRepository.ListPlatformAccount()
	if errorValue != nil {
		return
	}
	for _, platformAccount := range platformAccounts {
		personID := platformAccount.PersonID
		if personID == "" && platformAccount.Email != "" {
			resolvedPersonID, isFound := identityService.personIDByEmail[strings.ToLower(strings.TrimSpace(platformAccount.Email))]
			if isFound {
				personID = resolvedPersonID
			}
		}
		if personID == "" {
			continue
		}
		identityService.personIDByPlatformAccountKey[platformAccount.Platform+":"+platformAccount.ExternalUserID] = personID
	}
}

func (identityService *IdentityService) savePlatformAccount(platformAccountIdentity PlatformAccountIdentity) error {
	if identityService.platformAccountRepository == nil {
		return nil
	}
	return identityService.platformAccountRepository.UpsertPlatformAccount(platformAccountIdentity)
}
