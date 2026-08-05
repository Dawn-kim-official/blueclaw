//go:build linux || darwin

package integration

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/security"
)

func removeProjectedIdentitiesAfter(t *testing.T, personIDs []string, circleIDs []string) {
	t.Helper()
	removeProjectedIdentities(personIDs, circleIDs)
	t.Cleanup(func() { removeProjectedIdentities(personIDs, circleIDs) })
}

func removeProjectedIdentities(personIDs []string, circleIDs []string) {
	for _, personID := range personIDs {
		removeUser(security.LinuxPersonUserName(personID))
		removeGroup(security.LinuxPersonUserName(personID))
	}
	for _, circleID := range circleIDs {
		removeGroup(security.LinuxCircleGroupName(circleID))
	}
}

func removeUser(userName string) {
	if runtime.GOOS == "darwin" {
		_ = exec.Command("dscl", ".", "-delete", "/Users/"+userName).Run()
		return
	}
	_ = exec.Command("userdel", userName).Run()
}

func removeGroup(groupName string) {
	if runtime.GOOS == "darwin" {
		_ = exec.Command("dscl", ".", "-delete", "/Groups/"+groupName).Run()
		return
	}
	_ = exec.Command("groupdel", groupName).Run()
}
