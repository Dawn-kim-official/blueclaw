package firecracker

import (
	"os"
	"path/filepath"
)

type WorkspaceVolumeService struct {
	ImageSizeByte int64
}

type WorkspaceVolumeMetadata struct {
	HostImagePath     string
	GuestMountPath    string
	DataDirectoryPath string
}

func (workspaceVolumeService WorkspaceVolumeService) EnsureWorkspaceImage(workspaceImagePath string) (WorkspaceVolumeMetadata, error) {
	errorValue := os.MkdirAll(filepath.Dir(workspaceImagePath), 0o755)
	if errorValue != nil {
		return WorkspaceVolumeMetadata{}, errorValue
	}

	fileInformation, errorValue := os.Stat(workspaceImagePath)
	if errorValue == nil && !fileInformation.IsDir() {
		return workspaceVolumeService.MountWorkspaceMetadata(workspaceImagePath), nil
	}
	if errorValue != nil && !os.IsNotExist(errorValue) {
		return WorkspaceVolumeMetadata{}, errorValue
	}

	workspaceImageFile, errorValue := os.OpenFile(workspaceImagePath, os.O_CREATE|os.O_RDWR, 0o600)
	if errorValue != nil {
		return WorkspaceVolumeMetadata{}, errorValue
	}
	defer workspaceImageFile.Close()

	errorValue = workspaceImageFile.Truncate(workspaceVolumeService.imageSizeByte())
	if errorValue != nil {
		return WorkspaceVolumeMetadata{}, errorValue
	}

	return workspaceVolumeService.MountWorkspaceMetadata(workspaceImagePath), nil
}

func (workspaceVolumeService WorkspaceVolumeService) MountWorkspaceMetadata(workspaceImagePath string) WorkspaceVolumeMetadata {
	return WorkspaceVolumeMetadata{
		HostImagePath:     workspaceImagePath,
		GuestMountPath:    "/workspace",
		DataDirectoryPath: "/workspace/.blueclaw",
	}
}

func (workspaceVolumeService WorkspaceVolumeService) imageSizeByte() int64 {
	if workspaceVolumeService.ImageSizeByte > 0 {
		return workspaceVolumeService.ImageSizeByte
	}

	return 4 * 1024 * 1024 * 1024
}
