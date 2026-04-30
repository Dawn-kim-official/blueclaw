package firecracker

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

type WorkspaceVolumeService struct {
	ImageSizeByte    int64
	FormatterPath    string
	MountPath        string
	UnmountPath      string
	SyncPath         string
	TemporaryRootDir string
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
		if workspaceImageIsExt4(workspaceImagePath) {
			return workspaceVolumeService.MountWorkspaceMetadata(workspaceImagePath), nil
		}
		if fileInformation.Size() > 0 && !workspaceImageLooksEmpty(workspaceImagePath) {
			return WorkspaceVolumeMetadata{}, errors.New("workspace image exists but is not ext4")
		}
		if errorValue := workspaceVolumeService.formatWorkspaceImage(workspaceImagePath); errorValue != nil {
			return WorkspaceVolumeMetadata{}, errorValue
		}
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

	if errorValue := workspaceVolumeService.formatWorkspaceImage(workspaceImagePath); errorValue != nil {
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

func (workspaceVolumeService WorkspaceVolumeService) SyncWorkspaceDirectory(workspaceImagePath string, sourceDirectoryPath string) error {
	if sourceDirectoryPath == "" {
		return nil
	}
	sourceInformation, errorValue := os.Stat(sourceDirectoryPath)
	if errorValue != nil {
		return errorValue
	}
	if !sourceInformation.IsDir() {
		return errors.New("workspace source is not a directory")
	}

	temporaryRootDirectoryPath := workspaceVolumeService.TemporaryRootDir
	if temporaryRootDirectoryPath == "" {
		temporaryRootDirectoryPath = os.TempDir()
	}
	mountDirectoryPath, errorValue := os.MkdirTemp(temporaryRootDirectoryPath, "blueclaw-workspace-")
	if errorValue != nil {
		return errorValue
	}
	defer os.RemoveAll(mountDirectoryPath)

	mountPath := workspaceVolumeService.MountPath
	if mountPath == "" {
		mountPath = "mount"
	}
	unmountPath := workspaceVolumeService.UnmountPath
	if unmountPath == "" {
		unmountPath = "umount"
	}
	syncPath := workspaceVolumeService.SyncPath
	if syncPath == "" {
		syncPath = "rsync"
	}

	mountCommand := exec.Command(mountPath, "-o", "loop", workspaceImagePath, mountDirectoryPath)
	if output, errorValue := mountCommand.CombinedOutput(); errorValue != nil {
		return errors.New("mount workspace image: " + string(output))
	}
	defer exec.Command(unmountPath, mountDirectoryPath).Run()

	syncCommand := exec.Command(syncPath, "-a", filepath.Clean(sourceDirectoryPath)+"/", mountDirectoryPath+"/")
	if output, errorValue := syncCommand.CombinedOutput(); errorValue != nil {
		return errors.New("sync workspace image: " + string(output))
	}

	return nil
}

func (workspaceVolumeService WorkspaceVolumeService) imageSizeByte() int64 {
	if workspaceVolumeService.ImageSizeByte > 0 {
		return workspaceVolumeService.ImageSizeByte
	}

	return 4 * 1024 * 1024 * 1024
}

func (workspaceVolumeService WorkspaceVolumeService) formatWorkspaceImage(workspaceImagePath string) error {
	formatterPath := workspaceVolumeService.FormatterPath
	if formatterPath == "" {
		formatterPath = "mkfs.ext4"
	}

	command := exec.Command(formatterPath, "-F", "-L", "blueclaw-workspace", workspaceImagePath)
	output, errorValue := command.CombinedOutput()
	if errorValue != nil {
		return errors.New("format workspace image as ext4: " + string(output))
	}

	return nil
}

func workspaceImageIsExt4(workspaceImagePath string) bool {
	document, errorValue := readWorkspaceImagePrefix(workspaceImagePath, 4096)
	if errorValue != nil || len(document) < 1082 {
		return false
	}
	return document[1080] == 0x53 && document[1081] == 0xef
}

func workspaceImageLooksEmpty(workspaceImagePath string) bool {
	document, errorValue := readWorkspaceImagePrefix(workspaceImagePath, 4096)
	if errorValue != nil {
		return false
	}
	for _, value := range document {
		if value != 0 {
			return false
		}
	}
	return true
}

func readWorkspaceImagePrefix(workspaceImagePath string, byteCount int) ([]byte, error) {
	file, errorValue := os.Open(workspaceImagePath)
	if errorValue != nil {
		return nil, errorValue
	}
	defer file.Close()

	document := make([]byte, byteCount)
	readCount, errorValue := io.ReadFull(file, document)
	if errorValue != nil && errorValue != io.EOF && errorValue != io.ErrUnexpectedEOF {
		return nil, errorValue
	}
	return document[:readCount], nil
}
