package ingestion

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

type BlobStore interface {
	PutObject(namespace string, fileName string, content []byte) (string, string, error)
	ReadObject(objectPath string) ([]byte, error)
	DeleteExpiredObject(objectPath string) error
}

type FileSystemBlobStore struct {
	RootPath string
}

func (fileSystemBlobStore FileSystemBlobStore) PutObject(namespace string, fileName string, content []byte) (string, string, error) {
	objectDirectoryPath := filepath.Join(fileSystemBlobStore.RootPath, namespace)
	errorValue := os.MkdirAll(objectDirectoryPath, 0o755)
	if errorValue != nil {
		return "", "", errorValue
	}

	objectPath := filepath.Join(objectDirectoryPath, fileName)
	errorValue = os.WriteFile(objectPath, content, 0o600)
	if errorValue != nil {
		return "", "", errorValue
	}

	sum := sha256.Sum256(content)
	return objectPath, hex.EncodeToString(sum[:]), nil
}

func (fileSystemBlobStore FileSystemBlobStore) ReadObject(objectPath string) ([]byte, error) {
	return os.ReadFile(objectPath)
}

func (fileSystemBlobStore FileSystemBlobStore) DeleteExpiredObject(objectPath string) error {
	return os.Remove(objectPath)
}
