package firecracker

import (
	"context"
	"path/filepath"
	"testing"

	"blueclaw/internal/config"
)

type readyGuestHealthClient struct{}

func (readyGuestHealthClient) CheckHealth(healthContext context.Context, guestCID uint32, healthPortOrService string) error {
	_ = healthContext
	_ = guestCID
	_ = healthPortOrService
	return nil
}

func TestBuildBootSpecificationIncludesWorkspaceAndVSock(t *testing.T) {
	workspacePath := t.TempDir()
	supervisorService := NewSupervisorService(
		config.FirecrackerConfiguration{
			FirecrackerPath:     "/usr/bin/firecracker",
			JailerPath:          "/usr/bin/jailer",
			KernelImagePath:     "/opt/kernel",
			RootfsImagePath:     "/opt/rootfs.ext4",
			WorkspaceImagePath:  filepath.Join(workspacePath, "workspace.ext4"),
			VCPUCount:           4,
			MemoryMiB:           8192,
			VSockCID:            52,
			HealthPortOrService: "8080",
			LogDirectoryPath:    filepath.Join(workspacePath, "log"),
		},
		WorkspaceVolumeService{ImageSizeByte: 1024 * 1024},
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
	if len(bootSpecification.ConfigurationDocument.DriveConfigurations) != 2 {
		t.Fatalf("expected rootfs and workspace drives, got %d", len(bootSpecification.ConfigurationDocument.DriveConfigurations))
	}
	if bootSpecification.WorkspaceVolumeMetadata.GuestMountPath != "/workspace" {
		t.Fatalf("expected workspace mount path to match, got %q", bootSpecification.WorkspaceVolumeMetadata.GuestMountPath)
	}
}
