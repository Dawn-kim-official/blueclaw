package integration

import (
	"testing"

	"blueclaw/internal/connectors/mattermost"
	"blueclaw/internal/identity"
	"blueclaw/internal/policy"
	"blueclaw/tests/support"
)

func TestMattermostDirectMessageFlow(t *testing.T) {
	eventParser := mattermost.EventParser{}
	event, errorValue := eventParser.ParseEvent(support.MattermostMessagePayload())
	if errorValue != nil {
		t.Fatalf("expected mattermost event to parse: %v", errorValue)
	}
	if event.ConversationID != "channel-1" {
		t.Fatalf("expected conversation ID to match, got %s", event.ConversationID)
	}

	connector := mattermost.NewConnector()
	reply := connector.SendDirectReply(event.ConversationID, "hi")
	if reply == "" {
		t.Fatal("expected reply to be created")
	}
}

func TestMattermostEventLinksInvitedEmail(t *testing.T) {
	identityService := newMattermostIdentityService("person-1", "lee@example.com")
	connector := mattermost.NewConnectorWithIdentityResolver(mattermostStaticIdentityResolver{email: "lee@example.com"})

	authorizationResult, errorValue := connector.AuthorizeEvent(mattermost.Event{UserID: "user-1"}, identityService)
	if errorValue != nil {
		t.Fatalf("expected authorization to succeed: %v", errorValue)
	}
	if !authorizationResult.IsAllowed {
		t.Fatal("expected invited user to be allowed")
	}
	if authorizationResult.PersonID != "person-1" {
		t.Fatalf("expected person ID to match, got %s", authorizationResult.PersonID)
	}

	personID, isFound := identityService.ResolvePersonIDByPlatformAccount("mattermost", "user-1")
	if !isFound || personID != "person-1" {
		t.Fatalf("expected platform account to be remembered, got %s", personID)
	}
}

func TestMattermostEventRejectsUninvitedEmail(t *testing.T) {
	identityService := newMattermostIdentityService("person-1", "lee@example.com")
	connector := mattermost.NewConnectorWithIdentityResolver(mattermostStaticIdentityResolver{email: "stranger@example.com"})

	authorizationResult, errorValue := connector.AuthorizeEvent(mattermost.Event{UserID: "user-2"}, identityService)
	if errorValue != nil {
		t.Fatalf("expected authorization to complete: %v", errorValue)
	}
	if authorizationResult.IsAllowed {
		t.Fatal("expected uninvited user to be rejected")
	}
	if authorizationResult.Reply != mattermost.NotInvitedReply {
		t.Fatalf("expected not invited reply, got %q", authorizationResult.Reply)
	}
}

type mattermostStaticIdentityResolver struct {
	email string
}

func (mattermostStaticIdentityResolver mattermostStaticIdentityResolver) ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{
		ExternalUserID: externalUserID,
		Email:          mattermostStaticIdentityResolver.email,
		DisplayName:    "Mattermost User",
	}, nil
}

func newMattermostIdentityService(personID string, email string) *identity.IdentityService {
	policyDocument := policy.PolicyDocument{
		People: []policy.PersonPolicy{
			{
				PersonID:          personID,
				DisplayName:       "Invited User",
				Emails:            []string{email},
				SecurityLevelName: "member",
				SecurityLevelRank: 10,
				GrantedClasses:    []string{"internal"},
			},
		},
		Retention: policy.RetentionPolicy{RawEventDays: 60},
	}
	policyProjection := policy.PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(policyDocument)
	return identity.NewIdentityService(policyProjection)
}
