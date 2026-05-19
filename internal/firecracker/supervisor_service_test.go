package firecracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"blueclaw/internal/config"
)

type readyGuestHealthClient struct{}

func (readyGuestHealthClient) CheckHealth(healthContext context.Context, vsockUnixSocketPath string, healthPortOrService string) error {
	_ = healthContext
	_ = vsockUnixSocketPath
	_ = healthPortOrService
	return nil
}

type recordingOutboundNetworkService struct {
	preparedNetwork OutboundNetwork
	cleanedNetwork  OutboundNetwork
}

func (service *recordingOutboundNetworkService) PrepareOutboundNetwork(outboundNetwork OutboundNetwork) error {
	service.preparedNetwork = outboundNetwork
	return nil
}

func (service *recordingOutboundNetworkService) CleanupOutboundNetwork(outboundNetwork OutboundNetwork) error {
	service.cleanedNetwork = outboundNetwork
	return nil
}

func TestBuildBootSpecificationIncludesWorkspaceAndVSock(t *testing.T) {
	workspacePath := t.TempDir()
	kernelImagePath := filepath.Join(workspacePath, "kernel")
	rootfsImagePath := filepath.Join(workspacePath, "rootfs.ext4")
	if errorValue := os.WriteFile(kernelImagePath, []byte("kernel"), 0o600); errorValue != nil {
		t.Fatalf("expected kernel image fixture: %v", errorValue)
	}
	if errorValue := os.WriteFile(rootfsImagePath, []byte("rootfs"), 0o600); errorValue != nil {
		t.Fatalf("expected rootfs fixture: %v", errorValue)
	}
	supervisorService := NewSupervisorService(
		config.FirecrackerConfiguration{
			FirecrackerPath:        "/usr/bin/firecracker",
			JailerPath:             "/usr/bin/jailer",
			KernelImagePath:        kernelImagePath,
			RootfsImagePath:        rootfsImagePath,
			WorkspaceImagePath:     filepath.Join(workspacePath, "workspace.ext4"),
			VCPUCount:              4,
			MemoryMiB:              8192,
			VSockCID:               52,
			HealthPortOrService:    "8080",
			GuestHTTPPortOrService: "8081",
			HostHTTPListenAddress:  "127.0.0.1:8080",
			LogDirectoryPath:       filepath.Join(workspacePath, "log"),
		},
		WorkspaceVolumeService{ImageSizeByte: 1024 * 1024, FormatterPath: writeFakeExt4Formatter(t, workspacePath)},
		readyGuestHealthClient{},
	)

	bootSpecification, errorValue := supervisorService.buildBootSpecification()
	if errorValue != nil {
		t.Fatalf("expected boot specification to build: %v", errorValue)
	}

	if bootSpecification.ConfigurationDocument.MachineConfiguration.VCPUCount != 4 {
		t.Fatalf("expected vcpu count to match, got %d", bootSpecification.ConfigurationDocument.MachineConfiguration.VCPUCount)
	}
	if bootSpecification.ConfigurationDocument.VSockConfiguration.GuestCID != 52 {
		t.Fatalf("expected guest cid to match, got %d", bootSpecification.ConfigurationDocument.VSockConfiguration.GuestCID)
	}
	if bootSpecification.ConfigurationDocument.BootSource.KernelImagePath != "/vmlinux.bin" {
		t.Fatalf("expected jailed kernel path, got %q", bootSpecification.ConfigurationDocument.BootSource.KernelImagePath)
	}
	if !strings.Contains(bootSpecification.ConfigurationDocument.BootSource.BootArguments, " rw") {
		t.Fatalf("expected guest rootfs to boot writable, got %q", bootSpecification.ConfigurationDocument.BootSource.BootArguments)
	}
	if bootSpecification.ConfigurationDocument.DriveConfigurations[0].PathOnHost != "/rootfs.ext4" {
		t.Fatalf("expected jailed rootfs path, got %q", bootSpecification.ConfigurationDocument.DriveConfigurations[0].PathOnHost)
	}
	if bootSpecification.ConfigurationDocument.DriveConfigurations[0].IsReadOnly {
		t.Fatal("expected jailed rootfs drive to be writable")
	}
	if bootSpecification.ConfigurationDocument.DriveConfigurations[1].PathOnHost != "/workspace.ext4" {
		t.Fatalf("expected jailed workspace path, got %q", bootSpecification.ConfigurationDocument.DriveConfigurations[1].PathOnHost)
	}
	if bootSpecification.ConfigurationDocument.VSockConfiguration.UnixSocketPath != "/firecracker-vsock.socket" {
		t.Fatalf("expected jailed vsock path, got %q", bootSpecification.ConfigurationDocument.VSockConfiguration.UnixSocketPath)
	}
	if bootSpecification.JailerRootPath == "" {
		t.Fatal("expected jailer root path")
	}
	if len(bootSpecification.ConfigurationDocument.DriveConfigurations) != 2 {
		t.Fatalf("expected rootfs and workspace drives, got %d", len(bootSpecification.ConfigurationDocument.DriveConfigurations))
	}
	if bootSpecification.WorkspaceVolumeMetadata.GuestMountPath != "/workspace" {
		t.Fatalf("expected workspace mount path to match, got %q", bootSpecification.WorkspaceVolumeMetadata.GuestMountPath)
	}
}

func TestBuildBootSpecificationIncludesOutboundNetworkWhenEnabled(t *testing.T) {
	workspacePath := t.TempDir()
	kernelImagePath := filepath.Join(workspacePath, "kernel")
	rootfsImagePath := filepath.Join(workspacePath, "rootfs.ext4")
	if errorValue := os.WriteFile(kernelImagePath, []byte("kernel"), 0o600); errorValue != nil {
		t.Fatalf("expected kernel image fixture: %v", errorValue)
	}
	if errorValue := os.WriteFile(rootfsImagePath, []byte("rootfs"), 0o600); errorValue != nil {
		t.Fatalf("expected rootfs fixture: %v", errorValue)
	}
	outboundNetworkService := &recordingOutboundNetworkService{}
	supervisorService := NewSupervisorService(
		config.FirecrackerConfiguration{
			FirecrackerPath:        "/usr/bin/firecracker",
			JailerPath:             "/usr/bin/jailer",
			KernelImagePath:        kernelImagePath,
			RootfsImagePath:        rootfsImagePath,
			WorkspaceImagePath:     filepath.Join(workspacePath, "workspace.ext4"),
			VCPUCount:              4,
			MemoryMiB:              8192,
			VSockCID:               52,
			HealthPortOrService:    "8080",
			GuestHTTPPortOrService: "8081",
			HostHTTPListenAddress:  "127.0.0.1:8080",
			LogDirectoryPath:       filepath.Join(workspacePath, "log"),
			OutboundNetwork: config.OutboundNetworkConfiguration{
				Enabled: true,
			},
		},
		WorkspaceVolumeService{ImageSizeByte: 1024 * 1024, FormatterPath: writeFakeExt4Formatter(t, workspacePath)},
		readyGuestHealthClient{},
	)
	supervisorService.OutboundNetworkService = outboundNetworkService

	bootSpecification, errorValue := supervisorService.buildBootSpecification()
	if errorValue != nil {
		t.Fatalf("expected boot specification to build: %v", errorValue)
	}

	if len(bootSpecification.ConfigurationDocument.NetworkConfigurations) != 1 {
		t.Fatalf("expected one network interface, got %+v", bootSpecification.ConfigurationDocument.NetworkConfigurations)
	}
	networkConfiguration := bootSpecification.ConfigurationDocument.NetworkConfigurations[0]
	if networkConfiguration.InterfaceID != "eth0" || networkConfiguration.GuestMACAddress != "AA:FC:00:00:00:01" {
		t.Fatalf("expected default guest network configuration, got %+v", networkConfiguration)
	}
	if outboundNetworkService.preparedNetwork.NetworkCIDR != "172.31.0.0/30" {
		t.Fatalf("expected host network setup to use guest subnet, got %+v", outboundNetworkService.preparedNetwork)
	}
	if !strings.HasPrefix(networkConfiguration.HostDeviceName, "bctap") {
		t.Fatalf("expected deterministic tap device, got %+v", networkConfiguration)
	}
}

func TestStopGuestRemovesJailerDirectory(t *testing.T) {
	temporaryDirectory := t.TempDir()
	instanceID := "testinstance"
	jailerRootPath := buildJailerRootPath(temporaryDirectory, instanceID)
	if errorValue := os.MkdirAll(filepath.Join(jailerRootPath, "nested"), 0o700); errorValue != nil {
		t.Fatalf("expected jailer fixture: %v", errorValue)
	}

	command := exec.Command("sleep", "30")
	if errorValue := command.Start(); errorValue != nil {
		t.Fatalf("expected process fixture: %v", errorValue)
	}

	supervisorService := NewSupervisorService(config.FirecrackerConfiguration{}, WorkspaceVolumeService{}, readyGuestHealthClient{})
	supervisorService.commandByInstanceID[instanceID] = command

	errorValue := supervisorService.StopGuest(GuestInstance{
		InstanceID: instanceID,
		BootSpecification: BootSpecification{
			JailerRootPath: jailerRootPath,
		},
	})
	if errorValue != nil {
		t.Fatalf("expected stop to succeed: %v", errorValue)
	}

	if _, errorValue := os.Stat(filepath.Dir(jailerRootPath)); !os.IsNotExist(errorValue) {
		t.Fatalf("expected jailer directory to be removed, got %v", errorValue)
	}
}

func TestStopGuestCleansOutboundNetwork(t *testing.T) {
	temporaryDirectory := t.TempDir()
	instanceID := "testinstance"
	jailerRootPath := buildJailerRootPath(temporaryDirectory, instanceID)
	if errorValue := os.MkdirAll(filepath.Join(jailerRootPath, "nested"), 0o700); errorValue != nil {
		t.Fatalf("expected jailer fixture: %v", errorValue)
	}

	command := exec.Command("sleep", "30")
	if errorValue := command.Start(); errorValue != nil {
		t.Fatalf("expected process fixture: %v", errorValue)
	}

	outboundNetworkService := &recordingOutboundNetworkService{}
	supervisorService := NewSupervisorService(config.FirecrackerConfiguration{}, WorkspaceVolumeService{}, readyGuestHealthClient{})
	supervisorService.OutboundNetworkService = outboundNetworkService
	supervisorService.commandByInstanceID[instanceID] = command

	errorValue := supervisorService.StopGuest(GuestInstance{
		InstanceID: instanceID,
		BootSpecification: BootSpecification{
			JailerRootPath: jailerRootPath,
			OutboundNetwork: OutboundNetwork{
				Enabled:         true,
				HostDeviceName:  "bctap-test",
				NetworkCIDR:     "172.31.0.0/30",
				HostAddressCIDR: "172.31.0.1/30",
			},
		},
	})
	if errorValue != nil {
		t.Fatalf("expected stop to succeed: %v", errorValue)
	}

	if outboundNetworkService.cleanedNetwork.HostDeviceName != "bctap-test" {
		t.Fatalf("expected outbound network cleanup, got %+v", outboundNetworkService.cleanedNetwork)
	}
}

func TestRemoveInactiveJailerDirectoriesKeepsActiveInstance(t *testing.T) {
	temporaryDirectory := t.TempDir()
	activeInstanceID := "active"
	inactiveInstanceID := "inactive"
	activeRootPath := buildJailerRootPath(temporaryDirectory, activeInstanceID)
	inactiveRootPath := buildJailerRootPath(temporaryDirectory, inactiveInstanceID)
	if errorValue := os.MkdirAll(activeRootPath, 0o700); errorValue != nil {
		t.Fatalf("expected active jailer fixture: %v", errorValue)
	}
	if errorValue := os.MkdirAll(inactiveRootPath, 0o700); errorValue != nil {
		t.Fatalf("expected inactive jailer fixture: %v", errorValue)
	}

	supervisorService := NewSupervisorService(config.FirecrackerConfiguration{}, WorkspaceVolumeService{}, readyGuestHealthClient{})
	supervisorService.commandByInstanceID[activeInstanceID] = exec.Command("sleep", "30")

	if errorValue := supervisorService.removeInactiveJailerDirectories(temporaryDirectory); errorValue != nil {
		t.Fatalf("expected inactive cleanup to succeed: %v", errorValue)
	}

	if _, errorValue := os.Stat(filepath.Dir(activeRootPath)); errorValue != nil {
		t.Fatalf("expected active jailer directory to remain: %v", errorValue)
	}
	if _, errorValue := os.Stat(filepath.Dir(inactiveRootPath)); !os.IsNotExist(errorValue) {
		t.Fatalf("expected inactive jailer directory to be removed, got %v", errorValue)
	}
}
