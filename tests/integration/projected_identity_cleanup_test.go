//go:build linux || darwin

package integration

import (
	"os/exec"
	"os/user"
	"runtime"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

func requireBlueclawServiceAccount(t *testing.T) {
	t.Helper()
	if _, errorValue := user.Lookup("blueclaw"); errorValue == nil {
		return
	}
	t.Skipf("the workspace skeleton is owned by the blueclaw service account, which this machine does not have. %s", blueclawServiceAccountCreationHint())
}

func blueclawServiceAccountCreationHint() string {
	if runtime.GOOS == "darwin" {
		return "Create it with: sudo dscl . -create /Groups/blueclaw PrimaryGroupID 400 && sudo dscl . -create /Users/blueclaw UniqueID 400 PrimaryGroupID 400 UserShell /usr/bin/false NFSHomeDirectory /var/empty IsHidden 1"
	}
	return "Create it with: sudo groupadd --system blueclaw && sudo useradd --system --gid blueclaw --no-create-home --shell /usr/sbin/nologin blueclaw"
}

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
