package integration

import (
	"context"
	"testing"

	"blueclaw/internal/connectors"
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

	conversationClient := &mattermostIntegrationConversationClient{}
	adapter := mattermost.NewAdapter(mattermostStaticIdentityResolver{email: "lee@example.com"}, conversationClient)
	dispatchID, errorValue := adapter.SendReply(context.Background(), connectors.ReplyTarget{ConversationID: event.ConversationID, ReplyTargetID: "reply-target-1"}, connectors.OutboundReply{Message: "hi"})
	if errorValue != nil {
		t.Fatalf("expected reply to be sent: %v", errorValue)
	}
	if dispatchID == "" {
		t.Fatal("expected dispatch id")
	}
}

func TestMattermostEventLinksInvitedEmail(t *testing.T) {
	identityService := newMattermostIdentityService("person-1", "lee@example.com")
	connectorRuntime := newIntegrationConnectorRuntime(identityService)
	adapter := mattermost.NewAdapter(mattermostStaticIdentityResolver{email: "lee@example.com"}, &mattermostIntegrationConversationClient{})

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, mattermostInboundEvent("post-1", "user-1"))
	if errorValue != nil {
		t.Fatalf("expected authorization to succeed: %v", errorValue)
	}
	if result.TaskRunID == "" {
		t.Fatal("expected invited user to be allowed")
	}

	personID, isFound := identityService.ResolvePersonIDByPlatformAccount("mattermost", "user-1")
	if !isFound || personID != "person-1" {
		t.Fatalf("expected platform account to be remembered, got %s", personID)
	}
}

func TestMattermostEventRejectsUninvitedEmail(t *testing.T) {
	identityService := newMattermostIdentityService("person-1", "lee@example.com")
	conversationClient := &mattermostIntegrationConversationClient{}
	connectorRuntime := newIntegrationConnectorRuntime(identityService)
	adapter := mattermost.NewAdapter(mattermostStaticIdentityResolver{email: "stranger@example.com"}, conversationClient)

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, mattermostInboundEvent("post-2", "user-2"))
	if errorValue != nil {
		t.Fatalf("expected authorization to complete: %v", errorValue)
	}
	if result.TaskRunID != "" {
		t.Fatal("expected uninvited user to be rejected")
	}
	if conversationClient.lastMessage != connectors.NotInvitedReply {
		t.Fatalf("expected not invited reply, got %q", conversationClient.lastMessage)
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

type mattermostIntegrationConversationClient struct {
	lastMessage string
}

func (client *mattermostIntegrationConversationClient) CreatePost(_ string, _ string, message string) (string, error) {
	client.lastMessage = message
	return "reply-post-1", nil
}

func (client *mattermostIntegrationConversationClient) PublishTyping(string, string, string) error {
	return nil
}

func (client *mattermostIntegrationConversationClient) FetchPosts(string, string, int) ([]mattermost.ConversationPost, error) {
	return nil, nil
}

func (client *mattermostIntegrationConversationClient) AddReaction(string, string) error {
	return nil
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

func mattermostInboundEvent(messageID string, senderUserID string) connectors.PlatformInboundEvent {
	return connectors.PlatformInboundEvent{
		Platform:       "mattermost",
		Source:         "test",
		ConversationID: "channel-1",
		MessageID:      messageID,
		SenderID:       senderUserID,
		ReplyTargetID:  "reply-target-" + messageID,
		Prompt:         "hello",
	}
}
