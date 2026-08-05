package e2e

import "github.com/yeomyeonggeori/blueclaw/internal/bluecollarharness"

func init() {
	UseAgentHarnessFactory(bluecollarharness.New)
}
