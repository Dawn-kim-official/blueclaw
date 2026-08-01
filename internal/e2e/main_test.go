package e2e

import (
	"fmt"
	"os"
	"testing"
)

// These scenarios drive the workspace skills the appliance ships. Blueclaw does
// not own them, so a standalone checkout has nothing to drive and the suite says
// so rather than failing on a path that only exists beside a consumer.
func TestMain(mainTesting *testing.M) {
	if RootWorkspaceSkillsPath() == "" {
		fmt.Println("skipping the end-to-end scenarios: no assets/blueclaw-workspace/skills beside this checkout")
		os.Exit(0)
	}
	os.Exit(mainTesting.Run())
}
