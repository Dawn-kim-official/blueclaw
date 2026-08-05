//go:build linux

package integration

import (
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

func newCapabilityInvariantTestSocket(t *testing.T, mode os.FileMode) string {
	t.Helper()
	shortDirectory, temporaryDirectoryError := os.MkdirTemp("", "bcsock")
	if temporaryDirectoryError != nil {
		t.Fatalf("create short temp directory: %v", temporaryDirectoryError)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortDirectory) })
	socketPath := filepath.Join(shortDirectory, "capability.sock")
	listener, listenError := net.Listen("unix", socketPath)
	if listenError != nil {
		t.Fatalf("listen unix socket: %v", listenError)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if chmodError := os.Chmod(socketPath, mode); chmodError != nil {
		t.Fatalf("chmod socket: %v", chmodError)
	}
	return socketPath
}

func lookupCapabilitySocketOwningGroupName(t *testing.T, socketPath string) string {
	t.Helper()
	fileInformation, statError := os.Stat(socketPath)
	if statError != nil {
		t.Fatalf("stat socket: %v", statError)
	}
	unixStatInformation, isUnixStat := fileInformation.Sys().(*syscall.Stat_t)
	if !isUnixStat {
		t.Fatalf("expected a unix stat_t for %s", socketPath)
	}
	groupIdentifier, groupLookupError := user.LookupGroupId(strconv.FormatUint(uint64(unixStatInformation.Gid), 10))
	if groupLookupError != nil {
		t.Fatalf("lookup owning group for %s: %v", socketPath, groupLookupError)
	}
	return groupIdentifier.Name
}

func assignCapabilitySocketGroupOrSkip(t *testing.T, socketPath string, groupName string) {
	t.Helper()
	group, lookupError := user.LookupGroup(groupName)
	if lookupError != nil {
		if os.Geteuid() != 0 {
			t.Skipf("group %q does not exist on this machine and this test process is not root, so it cannot create the group needed to prove the group-collision failure honestly; rerun as root", groupName)
		}
		if createGroupError := exec.Command("groupadd", groupName).Run(); createGroupError != nil {
			t.Skipf("running as root but groupadd %q failed (%v); cannot prove the group-collision failure without a real colliding group", groupName, createGroupError)
		}
		t.Cleanup(func() { _ = exec.Command("groupdel", groupName).Run() })
		group, lookupError = user.LookupGroup(groupName)
		if lookupError != nil {
			t.Skipf("created group %q but cannot look it up afterward (%v)", groupName, lookupError)
		}
	}
	groupID, parseGroupIDError := strconv.Atoi(group.Gid)
	if parseGroupIDError != nil {
		t.Fatalf("expected a numeric gid for group %q, got %q", groupName, group.Gid)
	}
	if chownError := os.Chown(socketPath, -1, groupID); chownError != nil {
		t.Skipf("insufficient privilege to chown the socket to group %q (%v); this process must be root or already a member of that group to prove the group-collision failure honestly", groupName, chownError)
	}
}

func TestCapabilitySocketInvariantPassesOnRealLinuxSocket(t *testing.T) {
	socketPath := newCapabilityInvariantTestSocket(t, 0o660)

	owningGroupName := lookupCapabilitySocketOwningGroupName(t, socketPath)
	if strings.HasPrefix(owningGroupName, "bc_") {
		t.Skipf("the test process's own group %q collides with the projected requester namespace bc_*, so a pass cannot be proven honestly on this machine", owningGroupName)
	}

	result, verifyError := security.EnsureCapabilitySocketInvariant(socketPath, policy.PolicyDocument{})
	if verifyError != nil {
		t.Fatalf("expected EnsureCapabilitySocketInvariant to pass on a real linux socket owned by group %q, got error: %v", owningGroupName, verifyError)
	}
	if result.Skipped {
		t.Fatalf("expected the invariant check to run its non-skip path on linux against a real socket, got skipped: %s", result.SkipReason)
	}
	if result.GroupName != owningGroupName {
		t.Fatalf("expected the resolved group to be %q, got %q", owningGroupName, result.GroupName)
	}
	if result.Mode != 0o660 {
		t.Fatalf("expected the resolved mode to be 0660, got %04o", result.Mode)
	}
}

func TestCapabilitySocketInvariantFailsOnOtherAccessModeOnRealLinuxSocket(t *testing.T) {
	socketPath := newCapabilityInvariantTestSocket(t, 0o666)

	_, verifyError := security.EnsureCapabilitySocketInvariant(socketPath, policy.PolicyDocument{})
	if verifyError == nil {
		t.Fatal("expected an error for a real linux socket with other-access mode 0666")
	}
	if !strings.Contains(verifyError.Error(), "0666") {
		t.Fatalf("expected the error to mention the mode 0666, got: %v", verifyError)
	}
}

func TestCapabilitySocketInvariantFailsOnRequesterGroupCollisionOnRealLinuxSocket(t *testing.T) {
	const requesterPersonID = "person-capability-socket-invariant"
	policyDocument := policy.PolicyDocument{
		People: []policy.PersonPolicy{{PersonID: requesterPersonID, Emails: []string{requesterPersonID + "@example.com"}}},
	}
	collidingGroupName := security.LinuxPersonUserName(requesterPersonID)

	socketPath := newCapabilityInvariantTestSocket(t, 0o660)
	assignCapabilitySocketGroupOrSkip(t, socketPath, collidingGroupName)

	_, verifyError := security.EnsureCapabilitySocketInvariant(socketPath, policyDocument)
	if verifyError == nil {
		t.Fatal("expected an error when a projected requester's own group owns the capability socket")
	}
	if !strings.Contains(verifyError.Error(), collidingGroupName) {
		t.Fatalf("expected the error to mention the colliding group %q, got: %v", collidingGroupName, verifyError)
	}
}

func TestCapabilitySocketInvariantSkipsAndIsDistinguishableFromAPassOnLinux(t *testing.T) {
	missingSocketPath := filepath.Join(t.TempDir(), "does-not-exist.sock")
	missingResult, missingSocketError := security.EnsureCapabilitySocketInvariant(missingSocketPath, policy.PolicyDocument{})
	if missingSocketError != nil {
		t.Fatalf("expected no error when the socket does not exist yet, got %v", missingSocketError)
	}
	if !missingResult.Skipped {
		t.Fatal("expected the check to be marked skipped when the socket does not exist")
	}
	if missingResult.SkipReason == "" {
		t.Fatal("expected a skip reason for a missing socket")
	}

	emptyPathResult, emptyPathError := security.EnsureCapabilitySocketInvariant("", policy.PolicyDocument{})
	if emptyPathError != nil {
		t.Fatalf("expected no error when no socket path is configured, got %v", emptyPathError)
	}
	if !emptyPathResult.Skipped {
		t.Fatal("expected the check to be marked skipped when no socket path is configured")
	}
	if emptyPathResult.SkipReason == "" {
		t.Fatal("expected a skip reason when no socket path is configured")
	}

	passingSocketPath := newCapabilityInvariantTestSocket(t, 0o660)
	owningGroupName := lookupCapabilitySocketOwningGroupName(t, passingSocketPath)
	if strings.HasPrefix(owningGroupName, "bc_") {
		t.Skipf("the test process's own group %q collides with the projected requester namespace bc_*, so the pass/skip distinction cannot be proven honestly on this machine", owningGroupName)
	}
	passingResult, passingError := security.EnsureCapabilitySocketInvariant(passingSocketPath, policy.PolicyDocument{})
	if passingError != nil {
		t.Fatalf("expected no error for a real conforming socket, got %v", passingError)
	}
	if passingResult.Skipped {
		t.Fatal("expected a real conforming socket check to run its non-skip path, distinguishable from the skip cases above")
	}
}
