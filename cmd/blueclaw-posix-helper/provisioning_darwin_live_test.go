//go:build darwin

package main

import (
	"os"
	"os/exec"
	"os/user"
	"strings"
	"testing"
)

const liveTestUserName = "bc_person_livetest"
const liveTestGroupName = "bc_circle_livetest"
const liveTestUserID = 100901
const liveTestGroupID = 100902

func TestRealDirectoryServiceProvisionsAProjectedPerson(testInstance *testing.T) {
	if strings.TrimSpace(os.Getenv("BLUECLAW_TEST_MACOS_PROVISIONING")) == "" {
		testInstance.Skip("set BLUECLAW_TEST_MACOS_PROVISIONING=1 and run as root to provision real macOS identities here")
	}
	if os.Geteuid() != 0 {
		testInstance.Fatal("provisioning macOS identities needs root; rerun under sudo")
	}
	testInstance.Cleanup(removeLiveTestIdentities)
	removeLiveTestIdentities()

	if errorValue := createGroup(liveTestGroupName, liveTestGroupID); errorValue != nil {
		testInstance.Fatalf("createGroup: %v", errorValue)
	}
	if errorValue := createUser(liveTestUserName, "/tmp/"+liveTestUserName, liveTestUserID, liveTestGroupID); errorValue != nil {
		testInstance.Fatalf("createUser: %v", errorValue)
	}

	resolvedUser, errorValue := user.Lookup(liveTestUserName)
	if errorValue != nil {
		testInstance.Fatalf("the created user is not resolvable through the C library: %v", errorValue)
	}
	if resolvedUser.Uid != "100901" || resolvedUser.Gid != "100902" {
		testInstance.Fatalf("expected uid 100901 and gid 100902, got uid %s gid %s", resolvedUser.Uid, resolvedUser.Gid)
	}

	requireDirectoryServiceValue(testInstance, userRecordPath(liveTestUserName), "IsHidden", "1")
	requireDirectoryServiceValue(testInstance, userRecordPath(liveTestUserName), "UserShell", macOSServiceAccountShell)

	if errorValue := addUserToGroup(liveTestUserName, liveTestGroupName); errorValue != nil {
		testInstance.Fatalf("addUserToGroup: %v", errorValue)
	}

	reserved := map[uint32]bool{}
	systemUsers, errorValue := readSystemUsers()
	if errorValue != nil {
		testInstance.Fatalf("readSystemUsers: %v", errorValue)
	}
	for _, systemUser := range systemUsers {
		reserved[systemUser.identityID] = true
	}
	if !reserved[liveTestUserID] {
		testInstance.Fatal("the account database does not report the identity that was just created, so the allocator could hand it out twice")
	}
}

func requireDirectoryServiceValue(testInstance *testing.T, recordPath string, key string, expected string) {
	testInstance.Helper()
	output, errorValue := exec.Command("dscl", ".", "-read", recordPath, key).Output()
	if errorValue != nil {
		testInstance.Fatalf("dscl . -read %s %s: %v", recordPath, key, errorValue)
	}
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(output)), key+":"))
	if value != expected {
		testInstance.Fatalf("expected %s to be %q, got %q", key, expected, value)
	}
}

func removeLiveTestIdentities() {
	exec.Command("dscl", ".", "-delete", userRecordPath(liveTestUserName)).Run()
	exec.Command("dscl", ".", "-delete", groupRecordPath(liveTestGroupName)).Run()
}
