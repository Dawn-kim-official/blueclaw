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

type capabilityReplyRequest struct {
	ReplyTargetID string                 `json:"replyTargetID"`
	Message       string                 `json:"message"`
	Attachments   []agent.FileAttachment `json:"attachments,omitempty"`
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

	event, hasEvent, errorValue := adapter.parseNormalizedEvent(payload, "http")
	if errorValue != nil || !hasEvent {
		return HTTPParseResult{}, errorValue
	}

	return HTTPParseResult{Event: event, HasEvent: true}, nil
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

func (adapter CapabilityPlatformAdapter) SendReply(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
	var response capabilityReplyResponse
	errorValue := adapter.post(ctx, "reply.send", capabilityReplyRequest{
		ReplyTargetID: replyTarget.ReplyTargetID,
		Message:       reply.Message,
		Attachments:   reply.Attachments,
	}, &response)
	if errorValue != nil {
		return "", errorValue
	}
	return strings.TrimSpace(response.DispatchID), nil
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
