package security

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
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
	personID := strings.TrimSpace(personAccess.PersonID)
	if personID == "" || strings.TrimSpace(provisioner.synchronizer.terminalConfiguration.POSIXHelperPath) == "" {
		return nil
	}
	if errorValue := provisioner.synchronizer.Synchronize(ctx); errorValue != nil {
		return errors.New("requester POSIX workspace provisioning failed for " + personID + ": " + errorValue.Error())
	}
	if errorValue := provisioner.synchronizer.ReconcileHome(ctx, personID, workspaceRootPath); errorValue != nil {
		return errors.New("requester POSIX home reconciliation failed for " + personID + ": " + errorValue.Error())
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
	statePath, cleanupStateDocument, errorValue := synchronizer.createStateDocument(workspaceRootPath)
	if errorValue != nil {
		return errorValue
	}
	defer cleanupStateDocument()

	command := exec.CommandContext(ctx, helperPath, "sync", "--state", statePath, "--workspace", workspaceRootPath)
	output, errorValue := command.CombinedOutput()
	if errorValue != nil {
		return errors.New("POSIX synchronization failed: " + strings.TrimSpace(string(output)))
	}
	return nil
}

func (synchronizer POSIXSynchronizer) createStateDocument(workspaceRootPath string) (string, func(), error) {
	policyDocument, errorValue := synchronizer.loadPolicyDocument()
	if errorValue != nil {
		return "", func() {}, errorValue
	}
	document, errorValue := json.Marshal(POSIXStateForPolicy(policyDocument, workspaceRootPath))
	if errorValue != nil {
		return "", func() {}, errorValue
	}
	return writeTemporaryPOSIXStateDocument(document)
}

func (synchronizer POSIXSynchronizer) loadPolicyDocument() (policy.PolicyDocument, error) {
	document, errorValue := os.ReadFile(synchronizer.policyPath)
	if errorValue != nil {
		return policy.PolicyDocument{}, errorValue
	}
	var policyDocument policy.PolicyDocument
	if errorValue := json.Unmarshal(document, &policyDocument); errorValue != nil {
		return policy.PolicyDocument{}, errorValue
	}
	return policyDocument, nil
}

func writeTemporaryPOSIXStateDocument(document []byte) (string, func(), error) {
	file, errorValue := os.CreateTemp("", "blueclaw-posix-state-*.json")
	if errorValue != nil {
		return "", func() {}, errorValue
	}
	statePath := file.Name()
	cleanup := func() {
		_ = os.Remove(statePath)
	}
	if _, errorValue := file.Write(document); errorValue != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, errorValue
	}
	if errorValue := file.Close(); errorValue != nil {
		cleanup()
		return "", func() {}, errorValue
	}
	return statePath, cleanup, nil
}

func (synchronizer POSIXSynchronizer) ReconcileHome(ctx context.Context, personID string, workspaceRootPath string) error {
	helperPath := strings.TrimSpace(synchronizer.terminalConfiguration.POSIXHelperPath)
	if helperPath == "" {
		return nil
	}
	rootPath := strings.TrimSpace(workspaceRootPath)
	if rootPath == "" {
		rootPath = strings.TrimSpace(synchronizer.terminalConfiguration.WorkspaceRootPath)
	}
	if rootPath == "" {
		rootPath = "/workspace"
	}
	command := exec.CommandContext(ctx, helperPath, "reconcile-home", "--person-id", strings.TrimSpace(personID), "--workspace", rootPath)
	output, errorValue := command.CombinedOutput()
	if errorValue != nil {
		return errors.New("POSIX home reconciliation failed: " + strings.TrimSpace(string(output)))
	}
	return nil
}
