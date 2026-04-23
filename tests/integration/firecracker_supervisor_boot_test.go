package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"blueclaw/internal/config"
	"blueclaw/internal/firecracker"
)

type fakeGuestHealthClient struct{}

func (fakeGuestHealthClient) CheckHealth(healthContext context.Context, guestCID uint32, healthPortOrService string) error {
	_ = healthContext
	if guestCID != 52 {
		return os.ErrInvalid
	}
	if healthPortOrService != "8080" {
		return os.ErrInvalid
	}
	return nil
}

func TestSupervisorBootGuestWithFakeJailer(t *testing.T) {
	workspacePath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspacePath, "artifacts")
	jailerPath := filepath.Join(workspacePath, "fake-jailer.sh")
	firecrackerPath := filepath.Join(workspacePath, "fake-firecracker")
	jailerOutputPath := filepath.Join(workspacePath, "jailer-output.txt")

	errorValue := os.WriteFile(firecrackerPath, []byte("fake"), 0o700)
	if errorValue != nil {
		t.Fatalf("expected fake firecracker to be written: %v", errorValue)
	}

	jailerScript := "#!/bin/sh\nprintf '%s\n' \"$@\" > \"" + jailerOutputPath + "\"\nsleep 5\n"
	errorValue = os.WriteFile(jailerPath, []byte(jailerScript), 0o700)
	if errorValue != nil {
		t.Fatalf("expected fake jailer to be written: %v", errorValue)
	}

	supervisorService := firecracker.NewSupervisorService(
		config.FirecrackerConfiguration{
			FirecrackerPath:     firecrackerPath,
			JailerPath:          jailerPath,
			KernelImagePath:     "/opt/kernel",
			RootfsImagePath:     "/opt/rootfs.ext4",
			WorkspaceImagePath:  filepath.Join(workspacePath, "workspace.ext4"),
			VCPUCount:           4,
			MemoryMiB:           8192,
			VSockCID:            52,
			HealthPortOrService: "8080",
			LogDirectoryPath:    artifactDirectoryPath,
		},
		firecracker.WorkspaceVolumeService{ImageSizeByte: 1024 * 1024},
		fakeGuestHealthClient{},
	)

	guestInstance, errorValue := supervisorService.BootGuest(context.Background())
	if errorValue != nil {
		t.Fatalf("expected guest boot to succeed: %v", errorValue)
	}
	defer supervisorService.StopGuest(guestInstance)

	errorValue = supervisorService.WaitForGuestHealth(context.Background(), guestInstance)
	if errorValue != nil {
		t.Fatalf("expected guest health to succeed: %v", errorValue)
	}

	configurationDocument, errorValue := os.ReadFile(guestInstance.BootSpecification.ConfigurationFilePath)
	if errorValue != nil {
		t.Fatalf("expected configuration document to be readable: %v", errorValue)
	}
	if len(configurationDocument) == 0 {
		t.Fatal("expected configuration document to be written")
	}

	jailerOutputDocument, errorValue := waitForFile(jailerOutputPath)
	if errorValue != nil {
		t.Fatalf("expected fake jailer output to be readable: %v", errorValue)
	}
	if len(jailerOutputDocument) == 0 {
		t.Fatal("expected fake jailer to receive arguments")
	}
}

func waitForFile(documentPath string) ([]byte, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		document, errorValue := os.ReadFile(documentPath)
		if errorValue == nil {
			return document, nil
		}
		if time.Now().After(deadline) {
			return nil, errorValue
		}
		time.Sleep(50 * time.Millisecond)
	}
}
