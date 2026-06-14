//go:build linux

package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/config"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
	"blueclaw/internal/workspacepath"
)

func TestSiteSourceCreation_RealPOSIX(t *testing.T) {
	requireRealPOSIXIntegration(t)

	helperPath := buildOrLocatePOSIXHelper(t)
	personID := uniquePOSIXIntegrationPersonID()
	personUserName := security.LinuxPersonUserName(personID)
	identitySnapshot := newLinuxIdentitySnapshot(
		[]string{personUserName, "blueclaw"},
		[]string{personUserName, security.LinuxCircleGroupName(policy.StaffCircleID), "bc_shared", "blueclaw"},
	)
	t.Cleanup(func() {
		identitySnapshot.removeCreatedIdentities(t)
	})
	ensureBlueclawIdentity(t)

	t.Run("Regression/ProvisionedPersonHomeAbsent", func(t *testing.T) {
		fixture := newPOSIXSiteSourceFixture(t, helperPath, personID)
		synchronizePOSIXWorkspace(t, fixture)
		removeRequesterHome(t, fixture)
		assertDirectoryMode(t, filepath.Join(fixture.WorkspaceRootPath, "private"), 0711)
		assertPathDoesNotExist(t, requesterHomePath(fixture))

		_, toolFailure, errorValue := runRealPOSIXSiteSourceCreation(t, fixture, siteCreationRequest(personID), testSiteCreateResult("site-regression"))
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if toolFailure == nil || !toolFailure.Failed() {
			t.Fatal("expected site source creation to fail when requester home is absent")
		}
		if toolFailure.Failure.Code != agent.FailureCodes.AccessDenied.String() {
			t.Fatalf("expected access denied failure, got %+v", toolFailure.Failure)
		}
		if !strings.Contains(strings.ToLower(toolFailure.ContentText()), "permission denied") {
			t.Fatalf("expected permission denied failure, got %s", toolFailure.ContentText())
		}
		assertPathDoesNotExist(t, requesterHomePath(fixture))
	})

	t.Run("Fix/ProvisionedPersonHomePresent", func(t *testing.T) {
		fixture := newPOSIXSiteSourceFixture(t, helperPath, personID)
		provisionRequesterWorkspace(t, fixture)

		sourceWorkspace, toolFailure, errorValue := runRealPOSIXSiteSourceCreation(t, fixture, siteCreationRequest(personID), testSiteCreateResult("site-provisioned"))
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if toolFailure != nil {
			t.Fatalf("expected site source creation to pass after provisioning, got %s", toolFailure.ContentText())
		}
		assertDraftDirectoryPOSIXGroupAndMode(t, fixture, sourceWorkspace.ConcretePath)
		assertRequesterCanWriteDraftDirectory(t, fixture, sourceWorkspace)
	})

	t.Run("Fix/DriftedPersonHomeRepaired", func(t *testing.T) {
		fixture := newPOSIXSiteSourceFixture(t, helperPath, personID)
		provisionRequesterWorkspace(t, fixture)
		driftRequesterHomeGroup(t, fixture)
		provisionRequesterWorkspace(t, fixture)

		sourceWorkspace, toolFailure, errorValue := runRealPOSIXSiteSourceCreation(t, fixture, siteCreationRequest(personID), testSiteCreateResult("site-drift-repaired"))
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if toolFailure != nil {
			t.Fatalf("expected site source creation to pass after drift repair, got %s", toolFailure.ContentText())
		}
		assertDraftDirectoryPOSIXGroupAndMode(t, fixture, sourceWorkspace.ConcretePath)
		assertRequesterCanWriteDraftDirectory(t, fixture, sourceWorkspace)
	})

	t.Run("EdgeCase/EmptyPersonID", func(t *testing.T) {
		fixture := newPOSIXSiteSourceFixture(t, helperPath, personID)
		_, toolFailure, errorValue := runRealPOSIXSiteSourceCreation(t, fixture, ToolCatalogRequest{}, testSiteCreateResult("site-empty-person"))
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if toolFailure == nil || !toolFailure.Failed() {
			t.Fatal("expected empty requester person ID to fail before workspace resolution")
		}
		if toolFailure.Failure.Code != agent.FailureCodes.InvalidInput.String() {
			t.Fatalf("expected invalid input failure, got %+v", toolFailure.Failure)
		}
		if !strings.Contains(toolFailure.ContentText(), "requester personID is required") {
			t.Fatalf("expected clear requester person ID failure, got %s", toolFailure.ContentText())
		}
		assertPathDoesNotExist(t, filepath.Join(fixture.WorkspaceRootPath, "sites"))
	})
}

type posixSiteSourceFixture struct {
	HelperPath            string
	PersonID              string
	PolicyPath            string
	WorkspaceRootPath     string
	TerminalConfiguration config.TerminalConfiguration
}

type linuxIdentitySnapshot struct {
	UserNames     []string
	GroupNames    []string
	UsersExisted  map[string]bool
	GroupsExisted map[string]bool
}

func requireRealPOSIXIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("INTERNKIM_POSIX_IT") != "1" || os.Getuid() != 0 {
		t.Skip("set INTERNKIM_POSIX_IT=1 and run as root on Linux")
	}
}

func buildOrLocatePOSIXHelper(t *testing.T) string {
	t.Helper()
	repositoryRootPath := findRepositoryRootPath(t)
	repositoryHelperPath := filepath.Join(repositoryRootPath, "bin", "blueclaw-posix-helper")
	if isExecutableFile(repositoryHelperPath) {
		return repositoryHelperPath
	}
	helperPath := filepath.Join(t.TempDir(), "blueclaw-posix-helper")
	runTestCommand(t, repositoryRootPath, "go", "build", "-o", helperPath, "./cmd/blueclaw-posix-helper")
	return helperPath
}

func findRepositoryRootPath(t *testing.T) string {
	t.Helper()
	currentPath, errorValue := os.Getwd()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for {
		if isRegularFile(filepath.Join(currentPath, "go.mod")) {
			return currentPath
		}
		parentPath := filepath.Dir(currentPath)
		if parentPath == currentPath {
			t.Fatal("repository root with go.mod was not found")
		}
		currentPath = parentPath
	}
}

func isExecutableFile(path string) bool {
	fileInformation, errorValue := os.Stat(path)
	return errorValue == nil && fileInformation.Mode().IsRegular() && fileInformation.Mode().Perm()&0111 != 0
}

func isRegularFile(path string) bool {
	fileInformation, errorValue := os.Stat(path)
	return errorValue == nil && fileInformation.Mode().IsRegular()
}

func uniquePOSIXIntegrationPersonID() string {
	return fmt.Sprintf("it%x", time.Now().UnixNano())
}

func newLinuxIdentitySnapshot(userNames []string, groupNames []string) linuxIdentitySnapshot {
	usersExisted := map[string]bool{}
	groupsExisted := map[string]bool{}
	for _, userName := range userNames {
		usersExisted[userName] = linuxUserExists(userName)
	}
	for _, groupName := range groupNames {
		groupsExisted[groupName] = linuxGroupExists(groupName)
	}
	return linuxIdentitySnapshot{
		UserNames:     append([]string{}, userNames...),
		GroupNames:    append([]string{}, groupNames...),
		UsersExisted:  usersExisted,
		GroupsExisted: groupsExisted,
	}
}

func (snapshot linuxIdentitySnapshot) removeCreatedIdentities(t *testing.T) {
	t.Helper()
	for _, userName := range snapshot.UserNames {
		if snapshot.UsersExisted[userName] {
			continue
		}
		runCleanupCommand(t, "userdel", userName)
	}
	for _, groupName := range snapshot.GroupNames {
		if snapshot.GroupsExisted[groupName] {
			continue
		}
		runCleanupCommand(t, "groupdel", groupName)
	}
}

func ensureBlueclawIdentity(t *testing.T) {
	t.Helper()
	if !linuxGroupExists("blueclaw") {
		runTestCommand(t, "", "groupadd", "--system", "blueclaw")
	}
	if linuxUserExists("blueclaw") {
		return
	}
	runTestCommand(t, "", "useradd", "--system", "--no-create-home", "--gid", "blueclaw", "--shell", "/usr/sbin/nologin", "blueclaw")
}

func linuxUserExists(userName string) bool {
	return exec.Command("id", "-u", userName).Run() == nil
}

func linuxGroupExists(groupName string) bool {
	return exec.Command("getent", "group", groupName).Run() == nil
}

func newPOSIXSiteSourceFixture(t *testing.T, helperPath string, personID string) posixSiteSourceFixture {
	t.Helper()
	workspaceRootPath := createTraversableWorkspaceRoot(t)
	preparePOSIXWorkspaceRoot(t, workspaceRootPath)
	policyPath := writeMinimalPOSIXPolicy(t, t.TempDir(), personID)
	terminalConfiguration := config.TerminalConfiguration{
		WorkspaceRootPath: workspaceRootPath,
		POSIXHelperPath:   helperPath,
	}
	return posixSiteSourceFixture{
		HelperPath:            helperPath,
		PersonID:              personID,
		PolicyPath:            policyPath,
		WorkspaceRootPath:     workspaceRootPath,
		TerminalConfiguration: terminalConfiguration,
	}
}

func createTraversableWorkspaceRoot(t *testing.T) string {
	t.Helper()
	workspaceRootPath, errorValue := os.MkdirTemp("/", "blueclaw-posix-it-")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspaceRootPath) })
	return absoluteTestPath(t, workspaceRootPath)
}

func absoluteTestPath(t *testing.T, path string) string {
	t.Helper()
	absolutePath, errorValue := filepath.Abs(path)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return absolutePath
}

func preparePOSIXWorkspaceRoot(t *testing.T, workspaceRootPath string) {
	t.Helper()
	if errorValue := os.Chmod(workspaceRootPath, 0755); errorValue != nil {
		t.Fatal(errorValue)
	}
	chownPath(t, workspaceRootPath, "blueclaw", "blueclaw")
	privatePath := filepath.Join(workspaceRootPath, "private")
	if errorValue := os.MkdirAll(privatePath, 0711); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.Chmod(privatePath, 0711); errorValue != nil {
		t.Fatal(errorValue)
	}
	chownPath(t, privatePath, "blueclaw", "blueclaw")
}

func writeMinimalPOSIXPolicy(t *testing.T, directoryPath string, personID string) string {
	t.Helper()
	policyDocument := policy.PolicyDocument{
		People: []policy.PersonPolicy{{
			PersonID:    personID,
			DisplayName: "POSIX Integration Person",
			Circles:     []string{policy.StaffCircleID},
		}},
		Circles: []policy.CirclePolicy{{
			CircleID:    policy.StaffCircleID,
			DisplayName: "Staff",
		}},
	}
	document, errorValue := json.Marshal(policyDocument)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	policyPath := filepath.Join(directoryPath, "policy.json")
	if errorValue := os.WriteFile(policyPath, append(document, '\n'), 0644); errorValue != nil {
		t.Fatal(errorValue)
	}
	return policyPath
}

func synchronizePOSIXWorkspace(t *testing.T, fixture posixSiteSourceFixture) {
	t.Helper()
	runTestCommand(t, "", fixture.HelperPath, "sync", "--policy", fixture.PolicyPath, "--workspace", fixture.WorkspaceRootPath)
}

func provisionRequesterWorkspace(t *testing.T, fixture posixSiteSourceFixture) {
	t.Helper()
	provisioner := security.NewPOSIXRequesterWorkspaceProvisioner(security.NewPOSIXSynchronizer(fixture.TerminalConfiguration, fixture.PolicyPath))
	errorValue := provisioner.ProvisionRequesterWorkspace(context.Background(), siteCreationPersonAccess(fixture.PersonID), fixture.WorkspaceRootPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
}

func removeRequesterHome(t *testing.T, fixture posixSiteSourceFixture) {
	t.Helper()
	if errorValue := os.RemoveAll(requesterHomePath(fixture)); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func driftRequesterHomeGroup(t *testing.T, fixture posixSiteSourceFixture) {
	t.Helper()
	foreignGroup, errorValue := osuser.LookupGroup("bc_shared")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	foreignGroupID, errorValue := strconv.Atoi(foreignGroup.Gid)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.Chown(requesterHomePath(fixture), -1, foreignGroupID); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func requesterHomePath(fixture posixSiteSourceFixture) string {
	return filepath.Join(fixture.WorkspaceRootPath, "private", "people", fixture.PersonID)
}

func siteCreationRequest(personID string) ToolCatalogRequest {
	return ToolCatalogRequest{
		RequesterPersonID: personID,
		PersonAccess:      siteCreationPersonAccess(personID),
	}
}

func siteCreationPersonAccess(personID string) policy.PersonAccess {
	return policy.EnsureRequesterDefaults(policy.PersonAccess{PersonID: personID})
}

func testSiteCreateResult(siteID string) siteCreateResult {
	sourceWorkspacePath := canonicalSiteSourceWorkspacePath(siteID)
	return siteCreateResult{
		SiteID:              siteID,
		Slug:                siteID,
		Title:               "POSIX Integration Site",
		Description:         "Site source creation POSIX integration test.",
		Idea:                "Verify requester home provisioning before site source creation.",
		Purpose:             "runtime-test",
		Audience:            "maintainers",
		Archetype:           "internal",
		WorkspacePath:       canonicalSiteProjectWorkspacePath(siteID),
		SourceWorkspacePath: sourceWorkspacePath,
		AppWorkspacePath:    filepath.ToSlash(filepath.Join(sourceWorkspacePath, "app")),
		Status:              "draft",
	}
}

func runRealPOSIXSiteSourceCreation(t *testing.T, fixture posixSiteSourceFixture, request ToolCatalogRequest, site siteCreateResult) (ResolvedWorkspacePath, *agent.ToolResult, error) {
	t.Helper()
	toolContext := context.Background()
	toolCatalogBuilder := newPOSIXToolCatalogBuilder(fixture)
	if toolFailure := siteWorkspaceRequesterIdentityFailure(request, "site_source_workspace"); toolFailure != nil {
		return ResolvedWorkspacePath{}, toolFailure, nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return ResolvedWorkspacePath{}, actorFailure, nil
	}
	sourceWorkspace, errorValue := toolCatalogBuilder.resolveSiteProjectSourceWorkspace(toolContext, request, workspaceActor, siteProjectResolutionInput{
		SiteID:              site.SiteID,
		SourceWorkspacePath: site.SourceWorkspacePath,
	})
	if errorValue != nil {
		return ResolvedWorkspacePath{}, nil, errorValue
	}
	assertResolvedSiteSourceWorkspace(t, fixture, site, sourceWorkspace)
	if toolFailure := writeSiteStarterFiles(toolContext, workspaceActor, workspacepath.Directory(sourceWorkspace), site); toolFailure != nil {
		return sourceWorkspace, toolFailure, nil
	}
	return sourceWorkspace, nil, nil
}

func assertResolvedSiteSourceWorkspace(t *testing.T, fixture posixSiteSourceFixture, site siteCreateResult, sourceWorkspace ResolvedWorkspacePath) {
	t.Helper()
	if !filepath.IsAbs(sourceWorkspace.ConcretePath) {
		t.Fatalf("expected absolute concrete source workspace path, got %+v", sourceWorkspace)
	}
	expectedConcretePath := filepath.Join(requesterHomePath(fixture), "sites", site.SiteID, "draft")
	if sourceWorkspace.ConcretePath != expectedConcretePath {
		t.Fatalf("expected concrete source workspace path %s, got %+v", expectedConcretePath, sourceWorkspace)
	}
	expectedVirtualPath := canonicalSiteSourceWorkspacePath(site.SiteID)
	if sourceWorkspace.VirtualPath != expectedVirtualPath {
		t.Fatalf("expected virtual source workspace path %s, got %+v", expectedVirtualPath, sourceWorkspace)
	}
}

func newPOSIXToolCatalogBuilder(fixture posixSiteSourceFixture) *ToolCatalogBuilder {
	terminalService := security.NewTerminalSessionService(fixture.TerminalConfiguration)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(fixture.WorkspaceRootPath)
	toolCatalogBuilder.UseTerminalService(terminalService)
	return toolCatalogBuilder
}

func assertDirectoryMode(t *testing.T, directoryPath string, expectedMode os.FileMode) {
	t.Helper()
	fileInformation, errorValue := os.Stat(directoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !fileInformation.IsDir() {
		t.Fatalf("expected %s to be a directory", directoryPath)
	}
	if fileInformation.Mode().Perm() != expectedMode {
		t.Fatalf("expected %s mode %04o, got %04o", directoryPath, expectedMode, fileInformation.Mode().Perm())
	}
}

func assertPathDoesNotExist(t *testing.T, path string) {
	t.Helper()
	_, errorValue := os.Stat(path)
	if os.IsNotExist(errorValue) {
		return
	}
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Fatalf("expected %s not to exist", path)
}

func assertDraftDirectoryPOSIXGroupAndMode(t *testing.T, fixture posixSiteSourceFixture, draftPath string) {
	t.Helper()
	fileInformation, errorValue := os.Stat(draftPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !fileInformation.IsDir() {
		t.Fatalf("expected %s to be a directory", draftPath)
	}
	actualGroupID := fileGroupID(t, fileInformation)
	expectedGroupID := linuxGroupID(t, security.LinuxPersonUserName(fixture.PersonID))
	if actualGroupID != expectedGroupID {
		t.Fatalf("expected %s group %d, got %d", draftPath, expectedGroupID, actualGroupID)
	}
	if fileInformation.Mode()&os.ModeSetgid == 0 {
		t.Fatalf("expected %s to have setgid bit, got %v", draftPath, fileInformation.Mode())
	}
	if fileInformation.Mode().Perm()&0020 == 0 {
		t.Fatalf("expected %s to be group-writable, got %04o", draftPath, fileInformation.Mode().Perm())
	}
}

func assertRequesterCanWriteDraftDirectory(t *testing.T, fixture posixSiteSourceFixture, sourceWorkspace ResolvedWorkspacePath) {
	t.Helper()
	workspaceActor, errorValue := security.NewTerminalSessionService(fixture.TerminalConfiguration).WorkspaceActorFactory().Requester(context.Background(), security.WorkspaceActorRequest{
		PersonAccess:      siteCreationPersonAccess(fixture.PersonID),
		WorkspaceRootPath: fixture.WorkspaceRootPath,
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	requesterWritePath := workspacepath.Path{
		ConcretePath: filepath.Join(sourceWorkspace.ConcretePath, "requester-write.txt"),
		VirtualPath:  filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, "requester-write.txt")),
		Kind:         sourceWorkspace.Kind,
	}
	if errorValue := workspaceActor.WriteFile(context.Background(), requesterWritePath, []byte("ok\n"), 0660); errorValue != nil {
		t.Fatalf("expected requester to write in draft directory: %v", errorValue)
	}
}

func fileGroupID(t *testing.T, fileInformation os.FileInfo) uint32 {
	t.Helper()
	systemStat, isStat := fileInformation.Sys().(*syscall.Stat_t)
	if !isStat {
		t.Fatalf("expected linux stat information for %s", fileInformation.Name())
	}
	return systemStat.Gid
}

func linuxGroupID(t *testing.T, groupName string) uint32 {
	t.Helper()
	group, errorValue := osuser.LookupGroup(groupName)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	groupID, errorValue := strconv.ParseUint(group.Gid, 10, 32)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return uint32(groupID)
}

func chownPath(t *testing.T, path string, owner string, group string) {
	t.Helper()
	runTestCommand(t, "", "chown", owner+":"+group, path)
}

func runTestCommand(t *testing.T, directoryPath string, name string, arguments ...string) string {
	t.Helper()
	command := exec.Command(name, arguments...)
	if strings.TrimSpace(directoryPath) != "" {
		command.Dir = directoryPath
	}
	output, errorValue := command.CombinedOutput()
	if errorValue != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(arguments, " "), errorValue, strings.TrimSpace(string(output)))
	}
	return string(output)
}

func runCleanupCommand(t *testing.T, name string, arguments ...string) {
	t.Helper()
	command := exec.Command(name, arguments...)
	output, errorValue := command.CombinedOutput()
	if errorValue != nil {
		t.Logf("%s %s cleanup failed: %v %s", name, strings.Join(arguments, " "), errorValue, strings.TrimSpace(string(output)))
	}
}
