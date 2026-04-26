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
	dispatchID, errorValue := adapter.SendReply(context.Background(), connectors.ReplyTarget{ConversationID: event.ConversationID, ReplyTargetID: "reply-target-1"}, "hi")
	if errorValue != nil {
		t.Fatalf("expected reply to be sent: %v", errorValue)
	}
	if dispatchID == "" {
		t.Fatal("expected dispatch id")
	}
}

func TestSlackEventLinksInvitedEmail(t *testing.T) {
	identityService := newSlackIdentityService("person-1", "lee@example.com")
	connectorRuntime := newIntegrationConnectorRuntime(identityService)
	adapter := slack.NewAdapter(slackStaticIdentityResolver{email: "lee@example.com"}, &slackIntegrationConversationClient{})

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, slackInboundEvent("event-1", "U123"))
	if errorValue != nil {
		t.Fatalf("expected authorization to succeed: %v", errorValue)
	}
	if result.TaskRunID == "" {
		t.Fatal("expected invited user to be allowed")
	}

	personID, isFound := identityService.ResolvePersonIDByPlatformAccount("slack", "U123")
	if !isFound || personID != "person-1" {
		t.Fatalf("expected platform account to be remembered, got %s", personID)
	}
}

func TestSlackEventRejectsUninvitedEmail(t *testing.T) {
	identityService := newSlackIdentityService("person-1", "lee@example.com")
	conversationClient := slackIntegrationConversationClient{}
	connectorRuntime := newIntegrationConnectorRuntime(identityService)
	adapter := slack.NewAdapter(slackStaticIdentityResolver{email: "stranger@example.com"}, &conversationClient)

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, slackInboundEvent("event-2", "U456"))
	if errorValue != nil {
		t.Fatalf("expected authorization to complete: %v", errorValue)
	}
	if result.TaskRunID != "" {
		t.Fatal("expected uninvited user to be rejected")
	}
	if conversationClient.lastMessage != slack.NotInvitedReply {
		t.Fatalf("expected not invited reply, got %q", conversationClient.lastMessage)
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

type slackIntegrationConversationClient struct{}

func (client *slackIntegrationConversationClient) CreateMessage(_ string, _ string, message string) (string, error) {
	client.lastMessage = message
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

func slackInboundEvent(messageID string, senderUserID string) connectors.PlatformInboundEvent {
	return connectors.PlatformInboundEvent{
		Platform:       "slack",
		Source:         "test",
		EventID:        messageID,
		ConversationID: "D123",
		MessageID:      messageID,
		SenderUserID:   senderUserID,
		ChannelType:    "im",
		Text:           "hello",
	}
}
