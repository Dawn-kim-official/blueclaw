package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

type BackupService struct{}

func (backupService BackupService) CreateSnapshotBundle(bundlePath string, includedPaths []string) (SnapshotBundle, error) {
	if len(includedPaths) == 0 {
		return SnapshotBundle{}, errors.New("included paths are required")
	}

	bundleFile, errorValue := os.Create(bundlePath)
	if errorValue != nil {
		return SnapshotBundle{}, errorValue
	}
	defer bundleFile.Close()

	gzipWriter := gzip.NewWriter(bundleFile)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	for _, includedPath := range includedPaths {
		errorValue = addPathToTar(tarWriter, includedPath)
		if errorValue != nil {
			return SnapshotBundle{}, errorValue
		}
	}

	return SnapshotBundle{
		BundlePath:    bundlePath,
		IncludedPaths: includedPaths,
		CreatedAt:     time.Now(),
	}, nil
}

func (backupService BackupService) VerifySnapshotBundle(bundlePath string) error {
	bundleFile, errorValue := os.Open(bundlePath)
	if errorValue != nil {
		return errorValue
	}
	defer bundleFile.Close()

	gzipReader, errorValue := gzip.NewReader(bundleFile)
	if errorValue != nil {
		return errorValue
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	_, errorValue = tarReader.Next()
	return errorValue
}

func addPathToTar(tarWriter *tar.Writer, includedPath string) error {
	fileInfo, errorValue := os.Stat(includedPath)
	if errorValue != nil {
		return errorValue
	}

	if fileInfo.IsDir() {
		return filepath.Walk(includedPath, func(path string, information os.FileInfo, walkError error) error {
			if walkError != nil || information.IsDir() {
				return walkError
			}
			return addFileToTar(tarWriter, includedPath, path, information)
		})
	}

	return addFileToTar(tarWriter, filepath.Dir(includedPath), includedPath, fileInfo)
}

func addFileToTar(tarWriter *tar.Writer, rootPath string, filePath string, fileInfo os.FileInfo) error {
	file, errorValue := os.Open(filePath)
	if errorValue != nil {
		return errorValue
	}
	defer file.Close()

	header, errorValue := tar.FileInfoHeader(fileInfo, "")
	if errorValue != nil {
		return errorValue
	}

	relativePath, errorValue := filepath.Rel(rootPath, filePath)
	if errorValue != nil {
		return errorValue
	}
	header.Name = filepath.ToSlash(relativePath)

	errorValue = tarWriter.WriteHeader(header)
	if errorValue != nil {
		return errorValue
	}

	_, errorValue = io.Copy(tarWriter, file)
	return errorValue
}
