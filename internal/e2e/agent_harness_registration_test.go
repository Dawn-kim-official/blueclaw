package e2e

import "github.com/Dawn-kim-official/blueclaw/internal/bluecollarharness"

func init() {
	UseAgentHarnessFactory(bluecollarharness.NewVirtualSession)
}
