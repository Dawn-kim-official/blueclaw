package security

import (
	"context"
	"errors"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strings"

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
	_ = ctx
	personID := strings.TrimSpace(personAccess.PersonID)
	if personID == "" || strings.TrimSpace(provisioner.synchronizer.terminalConfiguration.POSIXHelperPath) == "" {
		return nil
	}
	if requesterPOSIXWorkspaceIsProvisioned(personID, workspaceRootPath) {
		return nil
	}
	if errorValue := provisioner.synchronizer.Synchronize(); errorValue != nil {
		if requesterPOSIXWorkspaceIsProvisioned(personID, workspaceRootPath) {
			return nil
		}
		return errors.New("requester POSIX workspace provisioning failed for " + personID + ": " + errorValue.Error())
	}
	if !requesterPOSIXWorkspaceIsProvisioned(personID, workspaceRootPath) {
		return errors.New("requester POSIX workspace provisioning did not create user and home for " + personID)
	}
	return nil
}

func (synchronizer POSIXSynchronizer) Synchronize() error {
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
	command := exec.Command(helperPath, "sync", "--policy", synchronizer.policyPath, "--workspace", workspaceRootPath)
	output, errorValue := command.CombinedOutput()
	if errorValue != nil {
		return errors.New("POSIX synchronization failed: " + strings.TrimSpace(string(output)))
	}
	return nil
}

func requesterPOSIXWorkspaceIsProvisioned(personID string, workspaceRootPath string) bool {
	if _, errorValue := osuser.Lookup(LinuxPersonUserName(personID)); errorValue != nil {
		return false
	}
	fileInformation, errorValue := os.Stat(requesterWorkspaceHomePath(personID, workspaceRootPath))
	return errorValue == nil && fileInformation.IsDir()
}

func requesterWorkspaceHomePath(personID string, workspaceRootPath string) string {
	rootPath := strings.TrimSpace(workspaceRootPath)
	if rootPath == "" {
		rootPath = "/workspace"
	}
	return filepath.Join(rootPath, "private", "people", strings.TrimSpace(personID))
}
