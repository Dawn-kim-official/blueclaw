package mattermost

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"blueclaw/internal/connectors"
	"blueclaw/internal/identity"
)

func TestAdapterNormalizesHTTPAndWebSocketPostedEvents(t *testing.T) {
	adapter := NewAdapter(testMattermostIdentityClient{}, &testMattermostConversationClient{})
	httpRequest := httptestRequest([]byte(`{"post_id":"post-1","user_id":"user-1","channel_id":"direct-1","message":"hello","channel_type":"D"}`))

	httpResult, errorValue := adapter.ParseHTTPEvent(context.Background(), httpRequest)
	if errorValue != nil {
		t.Fatalf("expected http event parse: %v", errorValue)
	}
	websocketEvent, hasEvent, errorValue := adapter.ParseRealtimeEvent(context.Background(), []byte(`{
		"event":"posted",
		"data":{
			"channel_type":"D",
			"post":"{\"id\":\"post-1\",\"user_id\":\"user-1\",\"channel_id\":\"direct-1\",\"message\":\"hello\",\"root_id\":\"\"}"
		}
	}`), "websocket")
	if errorValue != nil {
		t.Fatalf("expected websocket event parse: %v", errorValue)
	}

	if !httpResult.HasEvent || !hasEvent {
		t.Fatal("expected both event sources to produce an event")
	}
	if httpResult.Event.MessageID != websocketEvent.MessageID {
		t.Fatalf("expected stable post id, got %q and %q", httpResult.Event.MessageID, websocketEvent.MessageID)
	}
	if httpResult.Event.ChannelType != "D" || websocketEvent.ChannelType != "D" {
		t.Fatalf("expected direct channel type")
	}
}

func TestAdapterSendsMattermostDirectAndThreadReplies(t *testing.T) {
	conversationClient := &testMattermostConversationClient{}
	adapter := NewAdapter(testMattermostIdentityClient{}, conversationClient)

	_, errorValue := adapter.SendReply(context.Background(), testReplyTarget("direct-1", ""), "direct")
	if errorValue != nil {
		t.Fatalf("expected direct send: %v", errorValue)
	}
	errorValue = adapter.PublishTyping(context.Background(), "bot-1", testReplyTarget("channel-1", "root-1"))
	if errorValue != nil {
		t.Fatalf("expected typing send: %v", errorValue)
	}
	_, errorValue = adapter.SendReply(context.Background(), testReplyTarget("channel-1", "root-1"), "thread")
	if errorValue != nil {
		t.Fatalf("expected thread send: %v", errorValue)
	}

	if conversationClient.posts[0].rootID != "" {
		t.Fatalf("expected direct reply without root, got %q", conversationClient.posts[0].rootID)
	}
	if conversationClient.typingParentIDs[0] != "root-1" {
		t.Fatalf("expected typing parent id, got %q", conversationClient.typingParentIDs[0])
	}
	if conversationClient.posts[1].rootID != "root-1" {
		t.Fatalf("expected thread root id, got %q", conversationClient.posts[1].rootID)
	}
}

type testMattermostIdentityClient struct{}

func (client testMattermostIdentityClient) ResolveBotUserID() (string, error) {
	return "bot-1", nil
}

func (client testMattermostIdentityClient) ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{
		Platform:       "mattermost",
		ExternalUserID: externalUserID,
		Email:          "invited@example.com",
		DisplayName:    "Invited",
	}, nil
}

type testMattermostConversationClient struct {
	posts           []testMattermostPost
	typingParentIDs []string
}

type testMattermostPost struct {
	conversationID string
	rootID         string
	message        string
}

func (client *testMattermostConversationClient) CreatePost(conversationID string, rootID string, message string) (string, error) {
	client.posts = append(client.posts, testMattermostPost{conversationID: conversationID, rootID: rootID, message: message})
	return "post-reply", nil
}

func (client *testMattermostConversationClient) PublishTyping(_ string, _ string, parentID string) error {
	client.typingParentIDs = append(client.typingParentIDs, parentID)
	return nil
}

func (client *testMattermostConversationClient) ResolveChannelType(string) (string, error) {
	return "O", nil
}

func httptestRequest(payload []byte) *http.Request {
	request, _ := http.NewRequest(http.MethodPost, "/connectors/mattermost/events", bytes.NewReader(payload))
	return request
}

func testReplyTarget(conversationID string, parentID string) connectors.ReplyTarget {
	return connectors.ReplyTarget{
		ConversationID: conversationID,
		ParentID:       parentID,
	}
}
