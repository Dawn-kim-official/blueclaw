package bridge

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type RegistrationService struct {
	AuthorizedPublicKeysPath string
}

type TrustedCompanion struct {
	KeyType    string
	KeyBody    string
	KeyComment string
}

func (registrationService RegistrationService) RegisterCompanionBySSHKey(publicKey string) error {
	normalizedPublicKey, errorValue := normalizePublicKey(publicKey)
	if errorValue != nil {
		return errorValue
	}

	errorValue = os.MkdirAll(filepath.Dir(registrationService.AuthorizedPublicKeysPath), 0o755)
	if errorValue != nil {
		return errorValue
	}

	trustedCompanions, errorValue := registrationService.ListTrustedCompanions()
	if errorValue != nil {
		return errorValue
	}

	for _, trustedCompanion := range trustedCompanions {
		if strings.TrimSpace(trustedCompanion.KeyType+" "+trustedCompanion.KeyBody+" "+trustedCompanion.KeyComment) == normalizedPublicKey {
			return nil
		}
	}

	authorizedPublicKeysFile, errorValue := os.OpenFile(registrationService.AuthorizedPublicKeysPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if errorValue != nil {
		return errorValue
	}
	defer authorizedPublicKeysFile.Close()

	_, errorValue = authorizedPublicKeysFile.WriteString(normalizedPublicKey + "\n")
	return errorValue
}

func (registrationService RegistrationService) ListTrustedCompanions() ([]TrustedCompanion, error) {
	authorizedPublicKeysDocument, errorValue := os.ReadFile(registrationService.AuthorizedPublicKeysPath)
	if errorValue != nil {
		if os.IsNotExist(errorValue) {
			return []TrustedCompanion{}, nil
		}
		return nil, errorValue
	}

	trustedCompanions := []TrustedCompanion{}
	for _, line := range strings.Split(string(authorizedPublicKeysDocument), "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}

		keyFields := strings.Fields(trimmedLine)
		if len(keyFields) < 2 {
			continue
		}

		trustedCompanion := TrustedCompanion{
			KeyType: keyFields[0],
			KeyBody: keyFields[1],
		}
		if len(keyFields) > 2 {
			trustedCompanion.KeyComment = strings.Join(keyFields[2:], " ")
		}
		trustedCompanions = append(trustedCompanions, trustedCompanion)
	}

	return trustedCompanions, nil
}

func normalizePublicKey(publicKey string) (string, error) {
	trimmedPublicKey := strings.TrimSpace(publicKey)
	keyFields := strings.Fields(trimmedPublicKey)
	if len(keyFields) < 2 {
		return "", errors.New("ssh public key must include key type and key body")
	}

	_, errorValue := base64.StdEncoding.DecodeString(keyFields[1])
	if errorValue != nil {
		return "", errors.New("ssh public key body must be valid base64")
	}

	if !strings.HasPrefix(keyFields[0], "ssh-") && !strings.HasPrefix(keyFields[0], "ecdsa-") {
		return "", errors.New("ssh public key type is not supported")
	}

	return strings.Join(keyFields, " "), nil
}
