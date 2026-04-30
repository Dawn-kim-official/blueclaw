package firecracker

import (
	"context"
	"os"
	"path/filepath"
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
	if bootSpecification.ConfigurationDocument.DriveConfigurations[0].PathOnHost != "/rootfs.ext4" {
		t.Fatalf("expected jailed rootfs path, got %q", bootSpecification.ConfigurationDocument.DriveConfigurations[0].PathOnHost)
	}
	if bootSpecification.ConfigurationDocument.DriveConfigurations[1].PathOnHost != "/workspace.ext4" {
		t.Fatalf("expected jailed workspace path, got %q", bootSpecification.ConfigurationDocument.DriveConfigurations[1].PathOnHost)
	}
	if bootSpecification.ConfigurationDocument.VSockConfiguration.UnixSocketPath != "/firecracker-vsock.socket" {
		t.Fatalf("expected jailed vsock path, got %q", bootSpecification.ConfigurationDocument.VSockConfiguration.UnixSocketPath)
	}
	if len(bootSpecification.ConfigurationDocument.DriveConfigurations) != 2 {
		t.Fatalf("expected rootfs and workspace drives, got %d", len(bootSpecification.ConfigurationDocument.DriveConfigurations))
	}
	if bootSpecification.WorkspaceVolumeMetadata.GuestMountPath != "/workspace" {
		t.Fatalf("expected workspace mount path to match, got %q", bootSpecification.WorkspaceVolumeMetadata.GuestMountPath)
	}
}
