//go:build appliance

package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The appliance scenarios run the bundled scripts of skills this repository does
// not ship, so they need that appliance's workspace beside the checkout. Say
// which bundle is missing rather than failing on a path.
func TestMain(mainTesting *testing.M) {
	if missingSkills := MissingScenarioSkills(); len(missingSkills) > 0 {
		fmt.Printf("skipping the appliance scenarios: no workspace skill bundle for %s\n", strings.Join(missingSkills, ", "))
		os.Exit(0)
	}
	os.Exit(mainTesting.Run())
}
