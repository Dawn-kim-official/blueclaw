package e2e

import (
	"strings"
	"testing"
)

// Scenarios that drive an appliance skill run its bundled scripts and assert on
// their real output, so they need that appliance's workspace bundle beside this
// checkout. The rest exercise the runtime itself and run anywhere.
func requireWorkspaceSkills(scenarioTesting *testing.T) {
	scenarioTesting.Helper()
	if missingSkills := MissingScenarioSkills(); len(missingSkills) > 0 {
		scenarioTesting.Skipf("no workspace skill bundle for %s", strings.Join(missingSkills, ", "))
	}
}
