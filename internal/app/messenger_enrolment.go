package app

import (
	"strings"
	"sync"

	"github.com/Dawn-kim-official/blueclaw/internal/enrollment"
	"github.com/Dawn-kim-official/blueclaw/internal/identity"
	"github.com/Dawn-kim-official/blueclaw/internal/policy"
)

type messengerPersonRegistrar struct {
	mutex           sync.Mutex
	home            enrollment.Home
	policyPath      string
	identityService *identity.IdentityService
	policyProjector func(policy.PolicyDocument) policy.PolicyProjection
}

func (registrar *messengerPersonRegistrar) RegisterPerson(displayName string, email string) (bool, error) {
	if strings.TrimSpace(email) == "" {
		return false, nil
	}
	registrar.mutex.Lock()
	defer registrar.mutex.Unlock()

	wasAdded, errorValue := enrollment.RegisterPerson(registrar.home, enrollment.Person{DisplayName: displayName, Email: email})
	if errorValue != nil || !wasAdded {
		return false, errorValue
	}
	policyDocument, errorValue := policy.PolicyLoader{}.LoadPolicyDocument(registrar.policyPath)
	if errorValue != nil {
		return false, errorValue
	}
	registrar.identityService.ReloadPolicyProjection(registrar.policyProjector(policyDocument))
	return true, nil
}
