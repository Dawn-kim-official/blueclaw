//go:build linux

package integration

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/cliharness"
	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/mcpserver"
	"github.com/Dawn-kim-official/blueclaw/internal/policy"
	"github.com/Dawn-kim-official/blueclaw/internal/security"
)

func writePolicyDocumentForPeople(t *testing.T, personIDs []string) string {
	t.Helper()
	people := []policy.PersonPolicy{}
	for _, personID := range personIDs {
		people = append(people, policy.PersonPolicy{PersonID: personID, Emails: []string{personID + "@example.com"}})
	}
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	document, errorValue := json.MarshalIndent(policy.PolicyDocument{People: people}, "", "  ")
	if errorValue != nil {
		t.Fatalf("expected a policy document: %v", errorValue)
	}
	if errorValue := os.WriteFile(policyPath, document, 0o644); errorValue != nil {
		t.Fatalf("expected the policy to be written: %v", errorValue)
	}
	return policyPath
}

func makeWorkspaceTraversable(t *testing.T, workspaceRootPath string) {
	t.Helper()
	for traversedPath := workspaceRootPath; traversedPath != "/" && traversedPath != "."; traversedPath = filepath.Dir(traversedPath) {
		pathInformation, statError := os.Stat(traversedPath)
		if statError != nil {
			return
		}
		ownership, isOwnershipKnown := pathInformation.Sys().(*syscall.Stat_t)
		if !isOwnershipKnown || int(ownership.Uid) != os.Geteuid() {
			return
		}
		if chmodError := os.Chmod(traversedPath, 0o755); chmodError != nil {
			t.Fatalf("expected the workspace path to be traversable: %v", chmodError)
		}
	}
}

func TestTwoPeopleAskingAtOnceEachGetTheirOwnUnixIdentity(t *testing.T) {
	posixHelperPath := requireUnprivilegedSandboxProcess(t)
	requesterPersonIDs := []string{"person-parallel-one", "person-parallel-two"}

	workspaceRootPath, errorValue := os.MkdirTemp("", "blueclaw-parallel-identity")
	if errorValue != nil {
		t.Fatalf("expected a workspace root: %v", errorValue)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspaceRootPath) })
	makeWorkspaceTraversable(t, workspaceRootPath)

	terminalConfiguration := config.TerminalConfiguration{
		Mode:              "native",
		WorkspaceRootPath: workspaceRootPath,
		TimeoutSecond:     60,
		OutputMaxBytes:    65536,
		SessionMaxCount:   4,
		AllowNetwork:      true,
		POSIXHelperPath:   posixHelperPath,
	}
	synchronizer := security.NewPOSIXSynchronizer(terminalConfiguration, writePolicyDocumentForPeople(t, requesterPersonIDs))
	provisioner := security.NewPOSIXRequesterWorkspaceProvisioner(synchronizer)
	expectedUIDByPerson := map[string]string{}
	for _, personID := range requesterPersonIDs {
		personAccess := policy.PersonAccess{PersonID: personID, SecurityLevelRank: 100}
		if errorValue := provisioner.ProvisionRequesterWorkspace(context.Background(), personAccess, workspaceRootPath); errorValue != nil {
			t.Fatalf("expected %s to be projected onto a linux user: %v", personID, errorValue)
		}
		projectedUser, errorValue := user.Lookup(security.LinuxPersonUserName(personID))
		if errorValue != nil {
			t.Fatalf("expected %s to exist as a linux user: %v", personID, errorValue)
		}
		expectedUIDByPerson[personID] = projectedUser.Uid
	}

	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token-parallel" })
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(catalogServer.Close)
	terminalService := security.NewTerminalSessionService(terminalConfiguration)

	reportedUIDByPerson := map[string]string{}
	reportedMutex := sync.Mutex{}
	startTogether := make(chan struct{})
	waitGroup := sync.WaitGroup{}
	for _, personID := range requesterPersonIDs {
		waitGroup.Add(1)
		go func(personID string) {
			defer waitGroup.Done()
			harness := cliharness.New(cliharness.AgentCommand{
				Path:            "/bin/sh",
				PromptArguments: []string{"-c", "sleep 1; id -u"},
			}, identityCatalogPublisher{endpointURL: catalogServer.URL, resolver: resolver}, nil)
			harness.UseRequesterProcessRunner(terminalService.WorkspaceActorFactory(), workspaceRootPath)
			<-startTogether
			turnResult, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
				RequesterPersonID: personID,
				Prompt:            "unused",
				WorkspaceRootPath: workspaceRootPath,
				ToolSet:           harnessIdentityToolSet(t),
			})
			if errorValue != nil {
				reportedMutex.Lock()
				reportedUIDByPerson[personID] = "error: " + errorValue.Error()
				reportedMutex.Unlock()
				return
			}
			reportedMutex.Lock()
			reportedUIDByPerson[personID] = strings.TrimSpace(turnResult.FinishMessage)
			reportedMutex.Unlock()
		}(personID)
	}
	close(startTogether)
	waitGroup.Wait()

	for _, personID := range requesterPersonIDs {
		if reportedUIDByPerson[personID] != expectedUIDByPerson[personID] {
			t.Fatalf("expected %s's harness to run as uid %s while the other person's turn was in flight, it reported %q", personID, expectedUIDByPerson[personID], reportedUIDByPerson[personID])
		}
	}
	if reportedUIDByPerson[requesterPersonIDs[0]] == reportedUIDByPerson[requesterPersonIDs[1]] {
		t.Fatal("expected two people asking at the same time to run as two different unix users, which is the whole boundary")
	}
}
