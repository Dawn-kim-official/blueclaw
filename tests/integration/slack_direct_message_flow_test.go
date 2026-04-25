package integration

import (
	"context"
	"testing"

	"blueclaw/internal/connectors"
	"blueclaw/internal/connectors/slack"
	"blueclaw/internal/identity"
	"blueclaw/internal/policy"
	"blueclaw/tests/support"
)

func TestSlackDirectMessageFlow(t *testing.T) {
	eventParser := slack.EventParser{}
	event, errorValue := eventParser.ParseEvent(support.SlackMessagePayload())
	if errorValue != nil {
		t.Fatalf("expected slack event to parse: %v", errorValue)
	}
	if event.ConversationID != "C123" {
		t.Fatalf("expected conversation ID to match, got %s", event.ConversationID)
	}

	adapter := slack.NewAdapter(slackStaticIdentityResolver{email: "lee@example.com"}, slackIntegrationConversationClient{}, "")
	dispatchID, errorValue := adapter.SendReply(context.Background(), connectors.ReplyTarget{ConversationID: event.ConversationID}, "hi")
	if errorValue != nil {
		t.Fatalf("expected reply to be sent: %v", errorValue)
	}
	if dispatchID == "" {
		t.Fatal("expected dispatch id")
	}
}

func TestSlackEventLinksInvitedEmail(t *testing.T) {
	identityService := newSlackIdentityService("person-1", "lee@example.com")
	connector := slack.NewConnectorWithIdentityResolver(slackStaticIdentityResolver{email: "lee@example.com"})

	authorizationResult, errorValue := connector.AuthorizeEvent(slack.Event{UserID: "U123"}, identityService)
	if errorValue != nil {
		t.Fatalf("expected authorization to succeed: %v", errorValue)
	}
	if !authorizationResult.IsAllowed {
		t.Fatal("expected invited user to be allowed")
	}
	if authorizationResult.PersonID != "person-1" {
		t.Fatalf("expected person ID to match, got %s", authorizationResult.PersonID)
	}

	personID, isFound := identityService.ResolvePersonIDByPlatformAccount("slack", "U123")
	if !isFound || personID != "person-1" {
		t.Fatalf("expected platform account to be remembered, got %s", personID)
	}
}

func TestSlackEventRejectsUninvitedEmail(t *testing.T) {
	identityService := newSlackIdentityService("person-1", "lee@example.com")
	connector := slack.NewConnectorWithIdentityResolver(slackStaticIdentityResolver{email: "stranger@example.com"})

	authorizationResult, errorValue := connector.AuthorizeEvent(slack.Event{UserID: "U456"}, identityService)
	if errorValue != nil {
		t.Fatalf("expected authorization to complete: %v", errorValue)
	}
	if authorizationResult.IsAllowed {
		t.Fatal("expected uninvited user to be rejected")
	}
	if authorizationResult.Reply != slack.NotInvitedReply {
		t.Fatalf("expected not invited reply, got %q", authorizationResult.Reply)
	}
}

type slackStaticIdentityResolver struct {
	email string
}

func (slackStaticIdentityResolver slackStaticIdentityResolver) ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{
		ExternalUserID: externalUserID,
		Email:          slackStaticIdentityResolver.email,
		DisplayName:    "Slack User",
	}, nil
}

func (slackStaticIdentityResolver slackStaticIdentityResolver) ResolveBotUserID() (string, error) {
	return "bot-1", nil
}

type slackIntegrationConversationClient struct{}

func (client slackIntegrationConversationClient) CreateMessage(string, string, string) (string, error) {
	return "reply-ts", nil
}

func newSlackIdentityService(personID string, email string) *identity.IdentityService {
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
