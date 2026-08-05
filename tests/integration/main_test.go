package integration

import (
	"os"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/tests/support"
)

func TestMain(m *testing.M) {
	if support.IsFakeMCPServerRequested(os.Args) {
		support.RunFakeMCPServer()
		return
	}
	os.Exit(m.Run())
}
