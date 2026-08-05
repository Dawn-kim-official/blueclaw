package adminapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type PlatformAccountLister interface {
	ListPlatformAccount() ([]identity.PlatformAccountIdentity, error)
}

type IdentityResolveHandler struct {
	PolicyWatcher         *policy.PolicyWatcher
	PlatformAccountLister PlatformAccountLister
}

type resolveRecipientRequest struct {
	Platform string `json:"platform"`
	Hint     string `json:"hint"`
}

func (identityResolveHandler IdentityResolveHandler) HandleResolveRecipient(responseWriter http.ResponseWriter, request *http.Request) {
	var resolveRequest resolveRecipientRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&resolveRequest); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(resolveRequest.Platform) == "" || strings.TrimSpace(resolveRequest.Hint) == "" {
		http.Error(responseWriter, "platform and hint are required", http.StatusBadRequest)
		return
	}
	platformAccounts, errorValue := identityResolveHandler.listPlatformAccounts()
	if errorValue != nil {
		http.Error(responseWriter, "platform account lookup failed: "+errorValue.Error(), http.StatusServiceUnavailable)
		return
	}
	people := identityResolveHandler.PolicyWatcher.CurrentPolicyDocument().People
	resolution := identity.ResolveRecipient(resolveRequest.Platform, resolveRequest.Hint, people, platformAccounts)
	writeJSON(responseWriter, http.StatusOK, resolution)
}

func (identityResolveHandler IdentityResolveHandler) listPlatformAccounts() ([]identity.PlatformAccountIdentity, error) {
	if identityResolveHandler.PlatformAccountLister == nil {
		return nil, nil
	}
	return identityResolveHandler.PlatformAccountLister.ListPlatformAccount()
}
