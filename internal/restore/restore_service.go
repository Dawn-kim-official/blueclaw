package restore

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
)

type RestoreService struct{}

func (restoreService RestoreService) RestoreSnapshotBundle(bundlePath string, targetDirectoryPath string) error {
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
	for {
		header, nextErrorValue := tarReader.Next()
		if nextErrorValue == io.EOF {
			return nil
		}
		if nextErrorValue != nil {
			return nextErrorValue
		}

		targetPath := filepath.Join(targetDirectoryPath, header.Name)
		errorValue = os.MkdirAll(filepath.Dir(targetPath), 0o755)
		if errorValue != nil {
			return errorValue
		}

		file, innerErrorValue := os.Create(targetPath)
		if innerErrorValue != nil {
			return innerErrorValue
		}
		_, innerErrorValue = io.Copy(file, tarReader)
		closeErrorValue := file.Close()
		if innerErrorValue != nil {
			return innerErrorValue
		}
		if closeErrorValue != nil {
			return closeErrorValue
		}
	}
}
