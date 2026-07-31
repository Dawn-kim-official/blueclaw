//go:build linux

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/policy"
	"github.com/Dawn-kim-official/blueclaw/internal/security"
)

// The product's central claim, on real Linux: two people sharing one Blueclaw
// get two Linux users, and neither can reach the other's private workspace.
// Needs root to create users, so it skips everywhere else.
func TestTwoPeopleGetSeparatePOSIXWorkspaces(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("run as root to exercise POSIX workspace separation")
	}
	helperPath := strings.TrimSpace(os.Getenv("BLUECLAW_TEST_POSIX_HELPER"))
	if helperPath == "" {
		t.Skip("set BLUECLAW_TEST_POSIX_HELPER to the installed blueclaw-posix-helper")
	}

	workspaceRootPath := traversableTempDir(t)
	policyPath := writeTwoPersonPolicy(t)
	terminalConfiguration := config.TerminalConfiguration{
		Mode:              "native",
		WorkspaceRootPath: workspaceRootPath,
		POSIXHelperPath:   helperPath,
		TimeoutSecond:     60,
		AllowNetwork:      true,
	}
	provisioner := security.NewPOSIXRequesterWorkspaceProvisioner(security.NewPOSIXSynchronizer(terminalConfiguration, policyPath))

	for _, personID := range []string{"person-one", "person-two"} {
		personAccess := policy.PersonAccess{PersonID: personID, Circles: []string{"staff"}}
		if errorValue := provisioner.ProvisionRequesterWorkspace(context.Background(), personAccess, workspaceRootPath); errorValue != nil {
			t.Fatalf("expected %s to be provisioned: %v", personID, errorValue)
		}
	}

	firstUserName := security.LinuxPersonUserName("person-one")
	secondUserName := security.LinuxPersonUserName("person-two")
	firstHome := filepath.Join(workspaceRootPath, "private", "people", "person-one")
	secondHome := filepath.Join(workspaceRootPath, "private", "people", "person-two")

	if ownerOf(t, firstHome) != firstUserName {
		t.Fatalf("expected %s to own %s, got %s", firstUserName, firstHome, ownerOf(t, firstHome))
	}
	if ownerOf(t, secondHome) != secondUserName {
		t.Fatalf("expected %s to own %s, got %s", secondUserName, secondHome, ownerOf(t, secondHome))
	}
	if ownerOf(t, firstHome) == ownerOf(t, secondHome) {
		t.Fatal("expected two people to own two different private workspaces")
	}

	writeAs(t, firstUserName, filepath.Join(firstHome, "notes.txt"), "owned by one")
	if ownerOf(t, filepath.Join(firstHome, "notes.txt")) != firstUserName {
		t.Fatal("expected a file written by a person to be owned by that person")
	}
	if canReadAs(t, secondUserName, filepath.Join(firstHome, "notes.txt")) {
		t.Fatal("expected one person to be unable to read another person's private file")
	}
}

// t.TempDir hands back a 0700 root-owned tree, which no other user can even
// traverse; the workspace root a real deployment uses is reachable.
func traversableTempDir(t *testing.T) string {
	t.Helper()
	directoryPath := t.TempDir()
	for path := directoryPath; strings.HasPrefix(path, os.TempDir()); path = filepath.Dir(path) {
		if errorValue := os.Chmod(path, 0o755); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	return directoryPath
}

func writeTwoPersonPolicy(t *testing.T) string {
	t.Helper()
	document := `{"people":[
	  {"personID":"person-one","displayName":"One","emails":["one@example.com"],"securityLevelName":"member","securityLevelRank":50,"grantedClasses":["internal"],"circles":["staff"]},
	  {"personID":"person-two","displayName":"Two","emails":["two@example.com"],"securityLevelName":"member","securityLevelRank":50,"grantedClasses":["internal"],"circles":["staff"]}
	],"circles":[{"circleID":"staff","displayName":"Staff"}]}`
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if errorValue := os.WriteFile(policyPath, []byte(document), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	return policyPath
}

func ownerOf(t *testing.T, path string) string {
	t.Helper()
	output, errorValue := exec.Command("stat", "-c", "%U", path).Output()
	if errorValue != nil {
		t.Fatalf("expected %s to exist: %v", path, errorValue)
	}
	return strings.TrimSpace(string(output))
}

func writeAs(t *testing.T, userName string, path string, content string) {
	t.Helper()
	command := exec.Command("su", "-s", "/bin/sh", userName, "-c", "printf %s "+content+" > "+path)
	if output, errorValue := command.CombinedOutput(); errorValue != nil {
		t.Fatalf("expected %s to write %s: %v %s", userName, path, errorValue, output)
	}
}

func canReadAs(t *testing.T, userName string, path string) bool {
	t.Helper()
	return exec.Command("su", "-s", "/bin/sh", userName, "-c", "cat "+path).Run() == nil
}
