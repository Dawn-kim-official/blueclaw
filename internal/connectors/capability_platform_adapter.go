package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/identity"
)

const CapabilityNotInvitedReply = "This Intern Kim has not invited your account yet. Ask the administrator for access."

type CapabilityPlatformAdapter struct {
	PlatformName     string
	CapabilityClient capability.Client
}

type capabilityIdentityRequest struct {
	SenderID string `json:"senderID"`
}

type capabilityProgressRequest struct {
	ReplyTargetID string `json:"replyTargetID"`
}

type capabilityReactionRequest struct {
	ConversationID string `json:"conversationID"`
	MessageID      string `json:"messageID"`
	EmojiName      string `json:"emojiName"`
	Reason         string `json:"reason"`
}

type capabilityReplyRequest struct {
	ReplyTargetID   string                      `json:"replyTargetID"`
	Message         string                      `json:"message"`
	TaskRunID       string                      `json:"taskRunID,omitempty"`
	ReplyKind       string                      `json:"replyKind,omitempty"`
	RawEventID      string                      `json:"rawEventID,omitempty"`
	OutboxID        string                      `json:"outboxID,omitempty"`
	Attachments     []capabilityReplyAttachment `json:"attachments,omitempty"`
	RecoveryActions []agent.RecoveryAction      `json:"recoveryActions,omitempty"`
	Interaction     *AskInteraction             `json:"interaction,omitempty"`
}

type capabilityReplyAttachment struct {
	DevicePath    string `json:"devicePath"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	Title         string `json:"title,omitempty"`
	ContentBase64 string `json:"contentBase64,omitempty"`
}

type capabilityHistoryRequest struct {
	HistoryCursor string `json:"historyCursor"`
	Limit         int    `json:"limit"`
	Direction     string `json:"direction"`
}

type capabilityReplyResponse struct {
	DispatchID string `json:"dispatchID"`
}

type normalizedEventEnvelope struct {
	Event PlatformInboundEvent `json:"event"`
}

type capabilityMattermostInteractivePayload struct {
	UserID         string                                       `json:"user_id"`
	PostID         string                                       `json:"post_id"`
	ChannelID      string                                       `json:"channel_id"`
	SelectedOption string                                       `json:"selected_option"`
	Context        capabilityMattermostInteractiveActionContext `json:"context"`
}

type capabilityMattermostInteractiveActionContext struct {
	Action           string `json:"action"`
	InteractionID    string `json:"interactionID"`
	TaskRunID        string `json:"taskRunID"`
	ConversationID   string `json:"conversationID"`
	ReplyTargetID    string `json:"replyTargetID"`
	ChoiceKey        string `json:"choiceKey,omitempty"`
	ResponseLanguage string `json:"responseLanguage,omitempty"`
}

func NewCapabilityPlatformAdapter(platform string, capabilityClient capability.Client) CapabilityPlatformAdapter {
	return CapabilityPlatformAdapter{
		PlatformName:     strings.TrimSpace(platform),
		CapabilityClient: capabilityClient,
	}
}

func (adapter CapabilityPlatformAdapter) Name() string {
	return adapter.PlatformName
}

func (adapter CapabilityPlatformAdapter) ParseHTTPEvent(_ context.Context, request *http.Request) (HTTPParseResult, error) {
	payload, errorValue := io.ReadAll(request.Body)
	if errorValue != nil {
		return HTTPParseResult{}, errorValue
	}

	if adapter.Name() == "mattermost" {
		event, hasEvent := parseCapabilityMattermostInteractiveEvent(payload)
		if hasEvent {
			return HTTPParseResult{Event: event, HasEvent: true}, nil
		}
	}

	event, hasEvent, errorValue := adapter.parseNormalizedEvent(payload, "http")
	if errorValue != nil || !hasEvent {
		return HTTPParseResult{}, errorValue
	}

	return HTTPParseResult{Event: event, HasEvent: true}, nil
}

func parseCapabilityMattermostInteractiveEvent(payload []byte) (PlatformInboundEvent, bool) {
	var actionPayload capabilityMattermostInteractivePayload
	if errorValue := json.Unmarshal(payload, &actionPayload); errorValue != nil {
		return PlatformInboundEvent{}, false
	}
	action := strings.TrimSpace(actionPayload.Context.Action)
	if !strings.HasPrefix(action, "ask.") {
		return PlatformInboundEvent{}, false
	}
	if strings.TrimSpace(actionPayload.UserID) == "" || strings.TrimSpace(actionPayload.Context.ConversationID) == "" {
		return PlatformInboundEvent{}, false
	}
	choiceKey := firstNonEmptyString(actionPayload.Context.ChoiceKey, actionPayload.SelectedOption)
	messageIDParts := []string{"ask", strings.TrimSpace(actionPayload.PostID), action, strings.TrimSpace(actionPayload.Context.InteractionID), strings.TrimSpace(choiceKey)}
	return PlatformInboundEvent{
		Platform:         "mattermost",
		Source:           "http",
		ConversationID:   strings.TrimSpace(actionPayload.Context.ConversationID),
		MessageID:        strings.Join(messageIDParts, ":"),
		SenderID:         strings.TrimSpace(actionPayload.UserID),
		ReplyTargetID:    strings.TrimSpace(actionPayload.Context.ReplyTargetID),
		Prompt:           capabilityMattermostInteractivePrompt(action, choiceKey),
		ResponseLanguage: strings.TrimSpace(actionPayload.Context.ResponseLanguage),
		Context: VisibleContext{
			ChannelID:        strings.TrimSpace(actionPayload.ChannelID),
			ConversationType: "direct",
		},
		RawReceivedAt: time.Now(),
		LegacyFields: map[string]interface{}{
			"askAction":     strings.TrimPrefix(action, "ask."),
			"interactionID": strings.TrimSpace(actionPayload.Context.InteractionID),
			"taskRunID":     strings.TrimSpace(actionPayload.Context.TaskRunID),
			"choiceKey":     strings.TrimSpace(choiceKey),
			"postID":        strings.TrimSpace(actionPayload.PostID),
		},
	}, true
}

func capabilityMattermostInteractivePrompt(action string, choiceKey string) string {
	switch strings.TrimSpace(action) {
	case "ask.confirm":
		return "approved"
	case "ask.cancel":
		return "rejected"
	case "ask.choice":
		return "selected " + strings.TrimSpace(choiceKey)
	default:
		return strings.TrimSpace(action)
	}
}

func (adapter CapabilityPlatformAdapter) ParseRealtimeEvent(_ context.Context, payload []byte, source string) (PlatformInboundEvent, bool, error) {
	return adapter.parseNormalizedEvent(payload, source)
}

func (adapter CapabilityPlatformAdapter) ResolveIdentity(ctx context.Context, senderUserID string) (identity.PlatformAccountIdentity, error) {
	var response identity.PlatformAccountIdentity
	errorValue := adapter.post(ctx, "identity.resolve", capabilityIdentityRequest{SenderID: senderUserID}, &response)
	if errorValue != nil {
		return identity.PlatformAccountIdentity{}, errorValue
	}

	response.Platform = adapter.Name()
	response.ExternalUserID = senderUserID
	return response, nil
}

func (adapter CapabilityPlatformAdapter) StartProgress(ctx context.Context, replyTarget ReplyTarget) error {
	return adapter.post(ctx, "progress.start", capabilityProgressRequest{ReplyTargetID: replyTarget.ReplyTargetID}, nil)
}

func (adapter CapabilityPlatformAdapter) StopProgress(ctx context.Context, replyTarget ReplyTarget) error {
	return adapter.post(ctx, "progress.stop", capabilityProgressRequest{ReplyTargetID: replyTarget.ReplyTargetID}, nil)
}

func (adapter CapabilityPlatformAdapter) AddReaction(ctx context.Context, target ReactionTarget) error {
	return adapter.post(ctx, "reaction.add", capabilityReactionRequest{
		ConversationID: strings.TrimSpace(target.ConversationID),
		MessageID:      strings.TrimSpace(target.MessageID),
		EmojiName:      strings.TrimSpace(target.EmojiName),
		Reason:         strings.TrimSpace(target.Reason),
	}, nil)
}

func (adapter CapabilityPlatformAdapter) SendReply(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
	var response capabilityReplyResponse
	errorValue := adapter.post(ctx, "reply.send", capabilityReplyRequest{
		ReplyTargetID:   replyTarget.ReplyTargetID,
		Message:         reply.Message,
		TaskRunID:       reply.TaskRunID,
		ReplyKind:       reply.ReplyKind,
		RawEventID:      reply.RawEventID,
		OutboxID:        reply.OutboxID,
		Attachments:     buildCapabilityReplyAttachments(reply.Attachments),
		RecoveryActions: reply.RecoveryActions,
		Interaction:     reply.Interaction,
	}, &response)
	if errorValue != nil {
		return "", errorValue
	}
	return strings.TrimSpace(response.DispatchID), nil
}

func (adapter CapabilityPlatformAdapter) ResolveInteraction(ctx context.Context, resolution InteractionResolution) error {
	return adapter.post(ctx, "interaction.resolve", map[string]string{
		"dispatchID": resolution.DispatchID,
	}, nil)
}

func buildCapabilityReplyAttachments(attachments []agent.FileAttachment) []capabilityReplyAttachment {
	replyAttachments := []capabilityReplyAttachment{}
	for _, attachment := range attachments {
		replyAttachments = append(replyAttachments, capabilityReplyAttachment{
			DevicePath:    attachment.DevicePath,
			Filename:      attachment.Filename,
			ContentType:   attachment.ContentType,
			SizeBytes:     attachment.SizeBytes,
			Title:         attachment.Title,
			ContentBase64: attachment.ContentBase64,
		})
	}
	return replyAttachments
}

func (adapter CapabilityPlatformAdapter) FetchHistory(ctx context.Context, historyCursor string, limit int) (VisibleContext, error) {
	var response VisibleContext
	errorValue := adapter.post(ctx, "history.fetch", capabilityHistoryRequest{
		HistoryCursor: historyCursor,
		Limit:         limit,
		Direction:     "before",
	}, &response)
	if errorValue != nil {
		return VisibleContext{}, errorValue
	}
	return response, nil
}

func (adapter CapabilityPlatformAdapter) NotInvitedReply() string {
	return CapabilityNotInvitedReply
}

func (adapter CapabilityPlatformAdapter) parseNormalizedEvent(payload []byte, source string) (PlatformInboundEvent, bool, error) {
	if len(strings.TrimSpace(string(payload))) == 0 {
		return PlatformInboundEvent{}, false, nil
	}

	var event PlatformInboundEvent
	errorValue := json.Unmarshal(payload, &event)
	if errorValue != nil {
		return PlatformInboundEvent{}, false, errorValue
	}

	if strings.TrimSpace(event.MessageID) == "" {
		var envelope normalizedEventEnvelope
		errorValue = json.Unmarshal(payload, &envelope)
		if errorValue != nil {
			return PlatformInboundEvent{}, false, errorValue
		}
		event = envelope.Event
	}

	if strings.TrimSpace(event.MessageID) == "" {
		return PlatformInboundEvent{}, false, nil
	}

	event.Platform = adapter.Name()
	if strings.TrimSpace(event.Source) == "" {
		event.Source = source
	}
	if event.RawReceivedAt.IsZero() {
		event.RawReceivedAt = time.Now()
	}

	return event, true, nil
}

func (adapter CapabilityPlatformAdapter) post(ctx context.Context, capabilityName string, requestDocument any, responseDocument any) error {
	if strings.TrimSpace(adapter.Name()) == "" {
		return errors.New("capability platform name is required")
	}
	return adapter.CapabilityClient.PostJSON(ctx, adapter.endpointPath(capabilityName), requestDocument, responseDocument)
}

func (adapter CapabilityPlatformAdapter) endpointPath(capabilityName string) string {
	return "/v1/platform/" + url.PathEscape(adapter.Name()) + "/" + capabilityName
}
