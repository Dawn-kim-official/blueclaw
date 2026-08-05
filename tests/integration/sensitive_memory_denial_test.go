package integration

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
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
}
