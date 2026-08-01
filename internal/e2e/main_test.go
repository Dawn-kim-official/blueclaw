package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Blueclaw bundles the workspace skills that need only its own kernel. The rest
// belong to the appliance whose capability tools they call, so a standalone
// checkout cannot drive every scenario and the suite says which ones are absent
// instead of failing on a missing file.
func TestMain(mainTesting *testing.M) {
	if missingSkills := MissingScenarioSkills(); len(missingSkills) > 0 {
		fmt.Printf("skipping the end-to-end scenarios: no workspace skill bundle for %s\n", strings.Join(missingSkills, ", "))
		os.Exit(0)
	}
	os.Exit(mainTesting.Run())
}
