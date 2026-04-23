package integration

import (
	"testing"

	"blueclaw/internal/security"
)

func TestSensitiveMemoryDenial(t *testing.T) {
	accessControlService := security.AccessControlService{}
	isAllowed := accessControlService.CanReadSecurityLabel(10, []string{"internal"}, []string{"general"}, security.SecurityLabel{
		SourceConversationID:     "payroll",
		MinimumSecurityLevelRank: 90,
		RequiredClasses:          []string{"executive"},
	})
	if isAllowed {
		t.Fatal("expected sensitive access to be denied")
	}

	denialResponse := security.DenialResponseBuilder{}.BuildDeniedReply()
	if denialResponse == "" {
		t.Fatal("expected denial response to be non-empty")
	}
}
