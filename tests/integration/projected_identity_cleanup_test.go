//go:build linux || darwin

package integration

import (
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
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
		return "Create it with one dscl -create per attribute, since -create takes a single key:\n" +
			strings.Join([]string{
				"sudo dscl . -create /Groups/blueclaw PrimaryGroupID 400",
				"sudo dscl . -create /Users/blueclaw",
				"sudo dscl . -create /Users/blueclaw UniqueID 400",
				"sudo dscl . -create /Users/blueclaw PrimaryGroupID 400",
				"sudo dscl . -create /Users/blueclaw UserShell /usr/bin/false",
				"sudo dscl . -create /Users/blueclaw NFSHomeDirectory /var/empty",
			}, "\n")
	}
	return "Create it with: sudo groupadd --system blueclaw && sudo useradd --system --gid blueclaw --no-create-home --shell /usr/sbin/nologin blueclaw"
}

func removeProjectedIdentitiesAfter(t *testing.T, personIDs []string, circleIDs []string) {
	t.Helper()
	removeProjectedIdentities(personIDs, circleIDs)
	t.Cleanup(func() { reportIdentitiesLeftBehind(t, removeProjectedIdentities(personIDs, circleIDs)) })
}

func reportIdentitiesLeftBehind(t *testing.T, remaining []string) {
	t.Helper()
	if len(remaining) == 0 {
		return
	}
	t.Errorf("these identities outlived the test and are still on this machine: %v. Removing them needs root, and this process runs as uid %d. Delete them by hand.", remaining, os.Geteuid())
}

func removeProjectedIdentities(personIDs []string, circleIDs []string) []string {
	remaining := []string{}
	for _, personID := range personIDs {
		userName := security.LinuxPersonUserName(personID)
		removeUser(userName)
		removeGroup(userName)
		remaining = append(remaining, stillPresentIdentities(userName)...)
	}
	for _, circleID := range circleIDs {
		groupName := security.LinuxCircleGroupName(circleID)
		removeGroup(groupName)
		remaining = append(remaining, stillPresentIdentities(groupName)...)
	}
	return remaining
}

func stillPresentIdentities(name string) []string {
	present := []string{}
	if _, errorValue := user.Lookup(name); errorValue == nil {
		present = append(present, "user "+name)
	}
	if _, errorValue := user.LookupGroup(name); errorValue == nil {
		present = append(present, "group "+name)
	}
	return present
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
