package firecracker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"blueclaw/internal/config"
)

type SupervisorService struct {
	FirecrackerConfiguration config.FirecrackerConfiguration
	WorkspaceVolumeService   WorkspaceVolumeService
	GuestHealthClient        GuestHealthClient
	HealthCheckInterval      time.Duration

	mutex               sync.RWMutex
	commandByInstanceID map[string]*exec.Cmd
}

func NewSupervisorService(
	firecrackerConfiguration config.FirecrackerConfiguration,
	workspaceVolumeService WorkspaceVolumeService,
	guestHealthClient GuestHealthClient,
) *SupervisorService {
	return &SupervisorService{
		FirecrackerConfiguration: firecrackerConfiguration,
		WorkspaceVolumeService:   workspaceVolumeService,
		GuestHealthClient:        guestHealthClient,
		commandByInstanceID:      map[string]*exec.Cmd{},
	}
}

func (supervisorService *SupervisorService) BootGuest(bootContext context.Context) (GuestInstance, error) {
	bootSpecification, errorValue := supervisorService.buildBootSpecification()
	if errorValue != nil {
		return GuestInstance{}, errorValue
	}

	errorValue = supervisorService.writeConfigurationDocument(bootSpecification)
	if errorValue != nil {
		return GuestInstance{}, errorValue
	}

	standardOutputFile, errorValue := os.OpenFile(filepath.Join(filepath.Dir(bootSpecification.ConfigurationFilePath), "stdout.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if errorValue != nil {
		return GuestInstance{}, errorValue
	}

	standardErrorFile, errorValue := os.OpenFile(filepath.Join(filepath.Dir(bootSpecification.ConfigurationFilePath), "stderr.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if errorValue != nil {
		_ = standardOutputFile.Close()
		return GuestInstance{}, errorValue
	}

	command := exec.CommandContext(bootContext, supervisorService.FirecrackerConfiguration.JailerPath, bootSpecification.JailerArguments...)
	command.Stdout = standardOutputFile
	command.Stderr = standardErrorFile

	errorValue = command.Start()
	_ = standardOutputFile.Close()
	_ = standardErrorFile.Close()
	if errorValue != nil {
		return GuestInstance{}, errorValue
	}

	supervisorService.mutex.Lock()
	supervisorService.commandByInstanceID[bootSpecification.InstanceID] = command
	supervisorService.mutex.Unlock()

	return GuestInstance{
		InstanceID:        bootSpecification.InstanceID,
		BootSpecification: bootSpecification,
	}, nil
}

func (supervisorService *SupervisorService) StopGuest(guestInstance GuestInstance) error {
	supervisorService.mutex.Lock()
	defer supervisorService.mutex.Unlock()

	command, isFound := supervisorService.commandByInstanceID[guestInstance.InstanceID]
	if !isFound {
		return errors.New("guest instance was not found")
	}

	if command.Process != nil {
		errorValue := command.Process.Kill()
		delete(supervisorService.commandByInstanceID, guestInstance.InstanceID)
		return errorValue
	}

	delete(supervisorService.commandByInstanceID, guestInstance.InstanceID)
	return nil
}

func (supervisorService *SupervisorService) RestartGuest(bootContext context.Context, guestInstance GuestInstance) (GuestInstance, error) {
	errorValue := supervisorService.StopGuest(guestInstance)
	if errorValue != nil {
		return GuestInstance{}, errorValue
	}

	return supervisorService.BootGuest(bootContext)
}

func (supervisorService *SupervisorService) WaitForGuestHealth(healthContext context.Context, guestInstance GuestInstance) error {
	healthCheckInterval := supervisorService.HealthCheckInterval
	if healthCheckInterval <= 0 {
		healthCheckInterval = 200 * time.Millisecond
	}

	for {
		errorValue := supervisorService.GuestHealthClient.CheckHealth(
			healthContext,
			guestInstance.BootSpecification.VSockCID,
			guestInstance.BootSpecification.HealthPortOrService,
		)
		if errorValue == nil {
			return nil
		}

		select {
		case <-healthContext.Done():
			return healthContext.Err()
		case <-time.After(healthCheckInterval):
		}
	}
}

func (supervisorService *SupervisorService) buildBootSpecification() (BootSpecification, error) {
	errorValue := supervisorService.validateConfiguration()
	if errorValue != nil {
		return BootSpecification{}, errorValue
	}

	workspaceVolumeMetadata, errorValue := supervisorService.WorkspaceVolumeService.EnsureWorkspaceImage(supervisorService.FirecrackerConfiguration.WorkspaceImagePath)
	if errorValue != nil {
		return BootSpecification{}, errorValue
	}

	instanceID := newIdentifier()
	instanceDirectoryPath := filepath.Join(supervisorService.FirecrackerConfiguration.LogDirectoryPath, instanceID)
	errorValue = os.MkdirAll(instanceDirectoryPath, 0o755)
	if errorValue != nil {
		return BootSpecification{}, errorValue
	}

	apiUnixSocketPath := filepath.Join(instanceDirectoryPath, "firecracker-api.socket")
	vsockUnixSocketPath := filepath.Join(instanceDirectoryPath, "firecracker-vsock.socket")
	configurationFilePath := filepath.Join(instanceDirectoryPath, "firecracker-config.json")

	configurationDocument := ConfigurationDocument{
		BootSource: BootSourceConfiguration{
			KernelImagePath: supervisorService.FirecrackerConfiguration.KernelImagePath,
			BootArguments:   "console=ttyS0 reboot=k panic=1 pci=off",
		},
		DriveConfigurations: []DriveConfiguration{
			{
				DriveID:      "rootfs",
				PathOnHost:   supervisorService.FirecrackerConfiguration.RootfsImagePath,
				IsRootDevice: true,
				IsReadOnly:   true,
			},
			{
				DriveID:      "workspace",
				PathOnHost:   workspaceVolumeMetadata.HostImagePath,
				IsRootDevice: false,
				IsReadOnly:   false,
			},
		},
		MachineConfiguration: MachineConfiguration{
			VCPUCount: supervisorService.FirecrackerConfiguration.VCPUCount,
			MemoryMiB: supervisorService.FirecrackerConfiguration.MemoryMiB,
		},
		VSockConfiguration: VSockConfiguration{
			GuestCID:       supervisorService.FirecrackerConfiguration.VSockCID,
			UnixSocketPath: vsockUnixSocketPath,
		},
	}

	return BootSpecification{
		InstanceID:              instanceID,
		ConfigurationFilePath:   configurationFilePath,
		APIUnixSocketPath:       apiUnixSocketPath,
		VSockUnixSocketPath:     vsockUnixSocketPath,
		HealthPortOrService:     supervisorService.FirecrackerConfiguration.HealthPortOrService,
		VSockCID:                supervisorService.FirecrackerConfiguration.VSockCID,
		WorkspaceVolumeMetadata: workspaceVolumeMetadata,
		ConfigurationDocument:   configurationDocument,
		JailerArguments: []string{
			"--id", instanceID,
			"--exec-file", supervisorService.FirecrackerConfiguration.FirecrackerPath,
			"--uid", strconv.Itoa(os.Getuid()),
			"--gid", strconv.Itoa(os.Getgid()),
			"--chroot-base-dir", instanceDirectoryPath,
			"--",
			"--api-sock", apiUnixSocketPath,
			"--config-file", configurationFilePath,
		},
	}, nil
}

func (supervisorService *SupervisorService) writeConfigurationDocument(bootSpecification BootSpecification) error {
	configurationDocument, errorValue := json.MarshalIndent(bootSpecification.ConfigurationDocument, "", "  ")
	if errorValue != nil {
		return errorValue
	}

	return os.WriteFile(bootSpecification.ConfigurationFilePath, configurationDocument, 0o600)
}

func (supervisorService *SupervisorService) validateConfiguration() error {
	if supervisorService.FirecrackerConfiguration.FirecrackerPath == "" {
		return errors.New("firecrackerPath is required")
	}
	if supervisorService.FirecrackerConfiguration.JailerPath == "" {
		return errors.New("jailerPath is required")
	}
	if supervisorService.FirecrackerConfiguration.KernelImagePath == "" {
		return errors.New("kernelImagePath is required")
	}
	if supervisorService.FirecrackerConfiguration.RootfsImagePath == "" {
		return errors.New("rootfsImagePath is required")
	}
	if supervisorService.FirecrackerConfiguration.WorkspaceImagePath == "" {
		return errors.New("workspaceImagePath is required")
	}
	if supervisorService.FirecrackerConfiguration.LogDirectoryPath == "" {
		return errors.New("logDirectoryPath is required")
	}
	if supervisorService.FirecrackerConfiguration.VCPUCount <= 0 {
		return errors.New("vcpuCount must be positive")
	}
	if supervisorService.FirecrackerConfiguration.MemoryMiB <= 0 {
		return errors.New("memoryMiB must be positive")
	}
	if supervisorService.FirecrackerConfiguration.VSockCID == 0 {
		return errors.New("vsockCID must be positive")
	}
	if supervisorService.FirecrackerConfiguration.HealthPortOrService == "" {
		return errors.New("healthPortOrService is required")
	}

	return nil
}
