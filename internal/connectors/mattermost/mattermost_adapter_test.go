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
	httpRequest := httptestRequest([]byte(`{"post_id":"post-1","user_id":"user-1","channel_id":"direct-1","channel_name":"circle-hr-compensation","message":"hello","channel_type":"D"}`))

	httpResult, errorValue := adapter.ParseHTTPEvent(context.Background(), httpRequest)
	if errorValue != nil {
		t.Fatalf("expected http event parse: %v", errorValue)
	}
	websocketEvent, hasEvent, errorValue := adapter.ParseRealtimeEvent(context.Background(), []byte(`{
		"event":"posted",
		"data":{
			"channel_type":"D",
			"channel_name":"circle-hr-compensation",
			"post":"{\"id\":\"post-1\",\"user_id\":\"user-1\",\"channel_id\":\"direct-1\",\"channel_name\":\"circle-hr-compensation\",\"message\":\"hello\",\"root_id\":\"\"}"
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
	if httpResult.Event.Prompt != "hello" || websocketEvent.Prompt != "hello" {
		t.Fatalf("expected prompt to be normalized")
	}
	if httpResult.Event.SenderID != "user-1" || websocketEvent.SenderID != "user-1" {
		t.Fatalf("expected sender id to be normalized")
	}
	if httpResult.Event.Context.ChannelName != "circle-hr-compensation" || websocketEvent.Context.ChannelName != "circle-hr-compensation" {
		t.Fatalf("expected channel name to be normalized")
	}
}

func TestAdapterSendsMattermostDirectAndThreadReplies(t *testing.T) {
	conversationClient := &testMattermostConversationClient{}
	adapter := NewAdapter(testMattermostIdentityClient{}, conversationClient)

	_, errorValue := adapter.SendReply(context.Background(), testReplyTarget("direct-1", ""), connectors.OutboundReply{Message: "direct"})
	if errorValue != nil {
		t.Fatalf("expected direct send: %v", errorValue)
	}
	errorValue = adapter.StartProgress(context.Background(), testReplyTarget("channel-1", "root-1"))
	if errorValue != nil {
		t.Fatalf("expected progress start: %v", errorValue)
	}
	_, errorValue = adapter.SendReply(context.Background(), testReplyTarget("channel-1", "root-1"), connectors.OutboundReply{Message: "thread"})
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

func TestAdapterFetchesVisibleHistoryBeforeCurrentPost(t *testing.T) {
	conversationClient := &testMattermostConversationClient{
		historyPosts: []ConversationPost{
			{ID: "post-2", UserID: "user-2", Message: "이전 답변", CreateAt: 20},
			{ID: "post-current", UserID: "user-1", Message: "지금 질문", CreateAt: 30},
			{ID: "post-1", UserID: "user-1", Message: "이전 질문", CreateAt: 10},
		},
	}
	adapter := NewAdapter(testMattermostIdentityClient{}, conversationClient)

	visibleContext, errorValue := adapter.FetchHistory(context.Background(), "channel-1:post-current", 20)
	if errorValue != nil {
		t.Fatalf("expected history fetch: %v", errorValue)
	}

	if conversationClient.historyConversationID != "channel-1" || conversationClient.historyBeforePostID != "post-current" {
		t.Fatalf("expected parsed cursor, got conversation=%q before=%q", conversationClient.historyConversationID, conversationClient.historyBeforePostID)
	}
	if len(visibleContext.Messages) != 2 {
		t.Fatalf("expected previous messages only, got %+v", visibleContext.Messages)
	}
	if visibleContext.Messages[0].Text != "이전 질문" || visibleContext.Messages[1].Text != "이전 답변" {
		t.Fatalf("expected chronological visible context, got %+v", visibleContext.Messages)
	}
}

type testMattermostIdentityClient struct{}

func (client testMattermostIdentityClient) ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{
		Platform:       "mattermost",
		ExternalUserID: externalUserID,
		Email:          "invited@example.com",
		DisplayName:    "Invited",
	}, nil
}

type testMattermostConversationClient struct {
	posts                 []testMattermostPost
	typingParentIDs       []string
	historyPosts          []ConversationPost
	historyConversationID string
	historyBeforePostID   string
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

func (client *testMattermostConversationClient) FetchPosts(conversationID string, beforePostID string, _ int) ([]ConversationPost, error) {
	client.historyConversationID = conversationID
	client.historyBeforePostID = beforePostID
	return client.historyPosts, nil
}

func httptestRequest(payload []byte) *http.Request {
	request, _ := http.NewRequest(http.MethodPost, "/connectors/mattermost/events", bytes.NewReader(payload))
	return request
}

func testReplyTarget(conversationID string, parentID string) connectors.ReplyTarget {
	return connectors.ReplyTarget{
		ConversationID: conversationID,
		ReplyTargetID:  parentID,
	}
}
