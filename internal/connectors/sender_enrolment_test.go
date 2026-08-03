package connectors

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/identity"
	"github.com/Dawn-kim-official/blueclaw/internal/policy"
)

type recordingPersonRegistrar struct {
	registeredEmails []string
	addPerson        func(displayName string, email string)
}

func (registrar *recordingPersonRegistrar) RegisterPerson(displayName string, email string) (bool, error) {
	registrar.registeredEmails = append(registrar.registeredEmails, email)
	if registrar.addPerson == nil {
		return false, nil
	}
	registrar.addPerson(displayName, email)
	return true, nil
}

type enrolmentTestAdapter struct {
	PlatformAdapter
	identityByExternalUserID map[string]identity.PlatformAccountIdentity
}

func (adapter enrolmentTestAdapter) Name() string { return "mattermost" }

func (adapter enrolmentTestAdapter) ResolveIdentity(_ context.Context, externalUserID string) (identity.PlatformAccountIdentity, error) {
	return adapter.identityByExternalUserID[externalUserID], nil
}

func identityServiceWithPeople(people []policy.PersonPolicy) (*identity.IdentityService, func(displayName string, email string)) {
	projectionService := policy.PolicyProjectionService{}
	currentPeople := append([]policy.PersonPolicy{}, people...)
	identityService := identity.NewIdentityService(projectionService.ReplacePolicyProjectionTransactionally(policy.PolicyDocument{People: currentPeople}))
	addPerson := func(displayName string, email string) {
		currentPeople = append(currentPeople, policy.PersonPolicy{PersonID: "person-" + email, DisplayName: displayName, Emails: []string{email}})
		identityService.ReloadPolicyProjection(projectionService.ReplacePolicyProjectionTransactionally(policy.PolicyDocument{People: currentPeople}))
	}
	return identityService, addPerson
}

func TestAColleagueWhoSpeaksForTheFirstTimeBecomesSomebodyTheSandboxCanRunAs(t *testing.T) {
	identityService, addPerson := identityServiceWithPeople([]policy.PersonPolicy{
		{PersonID: "person-operator", Emails: []string{"operator@example.com"}},
	})
	registrar := &recordingPersonRegistrar{addPerson: addPerson}
	connectorRuntime := &ConnectorRuntime{identityService: identityService, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	connectorRuntime.UsePersonRegistrar(registrar)
	adapter := enrolmentTestAdapter{identityByExternalUserID: map[string]identity.PlatformAccountIdentity{
		"mm-user-1": {DisplayName: "김예시", Email: "seeun@example.com"},
	}}

	personID, isAuthorized, errorValue := connectorRuntime.authorizeSender(context.Background(), adapter, PlatformInboundEvent{SenderID: "mm-user-1"})

	if errorValue != nil {
		t.Fatalf("expected an unknown sender to be handled: %v", errorValue)
	}
	if !isAuthorized {
		t.Fatal("expected a colleague in the connected workspace to be able to ask, which is the whole point of connecting one")
	}
	if personID == "" {
		t.Fatal("expected the colleague to resolve to a person, because the sandbox runs their tools as that person")
	}
	if len(registrar.registeredEmails) != 1 || registrar.registeredEmails[0] != "seeun@example.com" {
		t.Fatalf("expected the sender's own email to be registered, got %v", registrar.registeredEmails)
	}
}

func TestAnInstallThatDidNotOpenItselfUpStillRefusesStrangers(t *testing.T) {
	identityService, _ := identityServiceWithPeople([]policy.PersonPolicy{
		{PersonID: "person-operator", Emails: []string{"operator@example.com"}},
	})
	connectorRuntime := &ConnectorRuntime{identityService: identityService, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	adapter := enrolmentTestAdapter{identityByExternalUserID: map[string]identity.PlatformAccountIdentity{
		"mm-user-1": {DisplayName: "김예시", Email: "seeun@example.com"},
	}}

	_, isAuthorized, _ := connectorRuntime.authorizeSender(context.Background(), adapter, PlatformInboundEvent{SenderID: "mm-user-1"})

	if isAuthorized {
		t.Fatal("expected an install that never opened itself to the workspace to keep refusing people it does not know")
	}
}

func TestAKnownPersonIsNotRegisteredAgain(t *testing.T) {
	identityService, addPerson := identityServiceWithPeople([]policy.PersonPolicy{
		{PersonID: "person-seeun", Emails: []string{"seeun@example.com"}},
	})
	registrar := &recordingPersonRegistrar{addPerson: addPerson}
	connectorRuntime := &ConnectorRuntime{identityService: identityService, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	connectorRuntime.UsePersonRegistrar(registrar)
	adapter := enrolmentTestAdapter{identityByExternalUserID: map[string]identity.PlatformAccountIdentity{
		"mm-user-1": {DisplayName: "김예시", Email: "seeun@example.com"},
	}}

	personID, isAuthorized, _ := connectorRuntime.authorizeSender(context.Background(), adapter, PlatformInboundEvent{SenderID: "mm-user-1"})

	if !isAuthorized || personID != "person-seeun" {
		t.Fatalf("expected an existing person to be recognised, got %q authorized=%v", personID, isAuthorized)
	}
	if len(registrar.registeredEmails) != 0 {
		t.Fatalf("expected somebody already known not to be registered again, got %v", registrar.registeredEmails)
	}
}
