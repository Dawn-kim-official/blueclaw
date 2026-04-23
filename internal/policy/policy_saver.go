package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type PolicySaver struct{}

func (policySaver PolicySaver) SavePolicyDocumentAtomically(path string, policyDocument PolicyDocument) error {
	document, errorValue := json.MarshalIndent(policyDocument, "", "  ")
	if errorValue != nil {
		return errorValue
	}

	temporaryPath := path + ".tmp"
	errorValue = os.WriteFile(temporaryPath, append(document, '\n'), 0o600)
	if errorValue != nil {
		return errorValue
	}

	return os.Rename(temporaryPath, path)
}

func (policySaver PolicySaver) BackupPolicyDocument(path string) (string, error) {
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return "", errorValue
	}

	backupPath := path + "." + time.Now().UTC().Format("20060102T150405") + ".bak"
	errorValue = os.MkdirAll(filepath.Dir(backupPath), 0o755)
	if errorValue != nil {
		return "", errorValue
	}

	errorValue = os.WriteFile(backupPath, document, 0o600)
	if errorValue != nil {
		return "", errorValue
	}

	return backupPath, nil
}
