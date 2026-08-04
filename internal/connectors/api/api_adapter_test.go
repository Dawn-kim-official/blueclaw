package api

import (
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/connectors"
)

type stubIdentityResolver struct {
	personIDByEmail map[string]string
	displayName     map[string]string
}

func (resolver stubIdentityResolver) ResolvePersonIDByEmail(email string) (string, bool) {
	personID, isFound := resolver.personIDByEmail[email]
	return personID, isFound
}

func (resolver stubIdentityResolver) ResolvePersonDisplayName(personID string) string {
	return resolver.displayName[personID]
}

func TestResolveIdentityResolvesApprovedEmail(t *testing.T) {
	adapter := NewAdapter(stubIdentityResolver{
		personIDByEmail: map[string]string{"lee@example.com": "person-1"},
		displayName:     map[string]string{"person-1": "Lee"},
	}, NewReplyStore())

	identity, errorValue := adapter.ResolveIdentity(context.Background(), "Lee@Example.Com")
	if errorValue != nil {
		t.Fatalf("unexpected error: %v", errorValue)
	}
	if identity.PersonID != "person-1" || identity.Platform != "api" || identity.Email != "lee@example.com" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}

func TestResolveIdentityReturnsEmptyForUnknownEmail(t *testing.T) {
	adapter := NewAdapter(stubIdentityResolver{}, NewReplyStore())

	identity, errorValue := adapter.ResolveIdentity(context.Background(), "stranger@example.com")
	if errorValue != nil {
		t.Fatalf("unexpected error: %v", errorValue)
	}
	if strings.TrimSpace(identity.PersonID) != "" {
		t.Fatalf("expected empty personID, got %q", identity.PersonID)
	}
}

func TestParseHTTPEventReadsNormalizedEvent(t *testing.T) {
	adapter := NewAdapter(stubIdentityResolver{}, NewReplyStore())
	body := `{"conversationID":"dm:api:p1","messageID":"m1","senderID":"lee@example.com","replyTargetID":"dm:api:p1","prompt":"hi"}`
	request := httptest.NewRequest("POST", "/connectors/api/events", strings.NewReader(body))

	parseResult, errorValue := adapter.ParseHTTPEvent(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("unexpected error: %v", errorValue)
	}
	if !parseResult.HasEvent || parseResult.Event.Prompt != "hi" || parseResult.Event.Platform != "api" {
		t.Fatalf("unexpected parse result: %+v", parseResult)
	}
}

func TestSendReplyStoresReplyForConversation(t *testing.T) {
	replyStore := NewReplyStore()
	adapter := NewAdapter(stubIdentityResolver{}, replyStore)
	target := connectors.ReplyTarget{ConversationID: "dm:api:p1", ReplyTargetID: "dm:api:p1", DedupeKey: "api:dm:api:p1:m1"}

	dispatchID, errorValue := adapter.SendReply(context.Background(), target, connectors.OutboundReply{Message: "done", TaskRunID: "task-1"})
	if errorValue != nil {
		t.Fatalf("unexpected error: %v", errorValue)
	}
	if dispatchID != "task-1" {
		t.Fatalf("unexpected dispatch id: %q", dispatchID)
	}
	stored := replyStore.List("dm:api:p1")
	if len(stored) != 1 || stored[0].Reply.Message != "done" {
		t.Fatalf("unexpected stored replies: %+v", stored)
	}
}

func TestReplyStoreCapsConversationHistory(t *testing.T) {
	replyStore := NewReplyStore()
	for index := 0; index < conversationReplyCapacity+10; index++ {
		replyStore.Append("dm:api:p1", "dm:api:p1", connectors.OutboundReply{Message: "m"})
	}
	if len(replyStore.List("dm:api:p1")) != conversationReplyCapacity {
		t.Fatalf("expected capped history of %d", conversationReplyCapacity)
	}
}

func TestReplyStoreEvictsOldestConversation(t *testing.T) {
	replyStore := NewReplyStore()
	oldestConversationID := "dm:api:c0"
	replyStore.Append(oldestConversationID, oldestConversationID, connectors.OutboundReply{Message: "first"})
	for index := 1; index <= trackedConversationCapacity; index++ {
		conversationID := "dm:api:c" + strconv.Itoa(index)
		replyStore.Append(conversationID, conversationID, connectors.OutboundReply{Message: "m"})
	}
	if len(replyStore.List(oldestConversationID)) != 0 {
		t.Fatalf("expected oldest conversation to be evicted")
	}
	if len(replyStore.List("dm:api:c"+strconv.Itoa(trackedConversationCapacity))) != 1 {
		t.Fatalf("expected newest conversation to be retained")
	}
}
