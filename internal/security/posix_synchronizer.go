package security

import (
	"context"
	"errors"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"blueclaw/internal/config"
	"blueclaw/internal/policy"
)

type POSIXSynchronizer struct {
	terminalConfiguration config.TerminalConfiguration
	policyPath            string
}

func NewPOSIXSynchronizer(terminalConfiguration config.TerminalConfiguration, policyPath string) POSIXSynchronizer {
	return POSIXSynchronizer{
		terminalConfiguration: terminalConfiguration,
		policyPath:            policyPath,
	}
}

type POSIXRequesterWorkspaceProvisioner struct {
	synchronizer POSIXSynchronizer
}

func NewPOSIXRequesterWorkspaceProvisioner(synchronizer POSIXSynchronizer) POSIXRequesterWorkspaceProvisioner {
	return POSIXRequesterWorkspaceProvisioner{synchronizer: synchronizer}
}

func (provisioner POSIXRequesterWorkspaceProvisioner) ProvisionRequesterWorkspace(ctx context.Context, personAccess policy.PersonAccess, workspaceRootPath string) error {
	personID := strings.TrimSpace(personAccess.PersonID)
	if personID == "" || strings.TrimSpace(provisioner.synchronizer.terminalConfiguration.POSIXHelperPath) == "" {
		return nil
	}
	if requesterPOSIXWorkspaceIsProvisioned(personID, workspaceRootPath) {
		return nil
	}
	if errorValue := provisioner.synchronizer.Synchronize(ctx); errorValue != nil {
		if requesterPOSIXWorkspaceIsProvisioned(personID, workspaceRootPath) {
			return nil
		}
		return errors.New("requester POSIX workspace provisioning failed for " + personID + ": " + errorValue.Error())
	}
	if !requesterPOSIXWorkspaceIsProvisioned(personID, workspaceRootPath) {
		return errors.New("requester POSIX workspace provisioning did not repair home for " + personID)
	}
	return nil
}

func (synchronizer POSIXSynchronizer) Synchronize(ctx context.Context) error {
	helperPath := strings.TrimSpace(synchronizer.terminalConfiguration.POSIXHelperPath)
	if helperPath == "" {
		return nil
	}
	if strings.TrimSpace(synchronizer.policyPath) == "" {
		return errors.New("policy path is required for POSIX synchronization")
	}
	workspaceRootPath := strings.TrimSpace(synchronizer.terminalConfiguration.WorkspaceRootPath)
	if workspaceRootPath == "" {
		workspaceRootPath = "/workspace"
	}
	command := exec.CommandContext(ctx, helperPath, "sync", "--policy", synchronizer.policyPath, "--workspace", workspaceRootPath)
	output, errorValue := command.CombinedOutput()
	if errorValue != nil {
		return errors.New("POSIX synchronization failed: " + strings.TrimSpace(string(output)))
	}
	return nil
}

func requesterPOSIXWorkspaceIsProvisioned(personID string, workspaceRootPath string) bool {
	identityName := LinuxPersonUserName(personID)
	if _, errorValue := osuser.Lookup(identityName); errorValue != nil {
		return false
	}
	homePath := requesterWorkspaceHomePath(personID, workspaceRootPath)
	requesterDirectoryPaths := []string{
		homePath,
		filepath.Join(homePath, "tmp"),
		filepath.Join(homePath, "artifacts"),
	}
	for _, directoryPath := range requesterDirectoryPaths {
		if !requesterDirectoryIsGroupAccessible(directoryPath, identityName) {
			return false
		}
	}
	return true
}

func requesterDirectoryIsGroupAccessible(directoryPath string, groupName string) bool {
	fileInformation, errorValue := os.Stat(directoryPath)
	if errorValue != nil || !fileInformation.IsDir() {
		return false
	}
	if fileInformation.Mode()&os.ModeSetgid == 0 {
		return false
	}
	if fileInformation.Mode().Perm()&0770 != 0770 {
		return false
	}
	return directoryBelongsToGroup(fileInformation, groupName)
}

func directoryBelongsToGroup(fileInformation os.FileInfo, groupName string) bool {
	systemInformation, isStatType := fileInformation.Sys().(*syscall.Stat_t)
	if !isStatType {
		return false
	}
	resolvedGroup, errorValue := osuser.LookupGroup(groupName)
	if errorValue != nil {
		return false
	}
	expectedGroupID, errorValue := strconv.ParseUint(resolvedGroup.Gid, 10, 32)
	if errorValue != nil {
		return false
	}
	return uint64(systemInformation.Gid) == expectedGroupID
}

func requesterWorkspaceHomePath(personID string, workspaceRootPath string) string {
	rootPath := strings.TrimSpace(workspaceRootPath)
	if rootPath == "" {
		rootPath = "/workspace"
	}
	return filepath.Join(rootPath, "private", "people", strings.TrimSpace(personID))
}
