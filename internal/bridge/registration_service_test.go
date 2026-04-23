package bridge

import (
	"path/filepath"
	"testing"
)

func TestRegisterCompanionBySSHKeyAndListTrustedCompanions(t *testing.T) {
	workspacePath := t.TempDir()
	registrationService := RegistrationService{
		AuthorizedPublicKeysPath: filepath.Join(workspacePath, "authorized_companions"),
	}

	publicKey := "ssh-ed25519 SGVsbG8= lee@desktop"
	errorValue := registrationService.RegisterCompanionBySSHKey(publicKey)
	if errorValue != nil {
		t.Fatalf("expected public key to register: %v", errorValue)
	}

	errorValue = registrationService.RegisterCompanionBySSHKey(publicKey)
	if errorValue != nil {
		t.Fatalf("expected duplicate public key registration to be ignored: %v", errorValue)
	}

	trustedCompanions, errorValue := registrationService.ListTrustedCompanions()
	if errorValue != nil {
		t.Fatalf("expected trusted companions to list: %v", errorValue)
	}
	if len(trustedCompanions) != 1 {
		t.Fatalf("expected one trusted companion, got %d", len(trustedCompanions))
	}
	if trustedCompanions[0].KeyComment != "lee@desktop" {
		t.Fatalf("expected key comment to match, got %q", trustedCompanions[0].KeyComment)
	}
}

func TestRegisterCompanionBySSHKeyRejectsInvalidKey(t *testing.T) {
	workspacePath := t.TempDir()
	registrationService := RegistrationService{
		AuthorizedPublicKeysPath: filepath.Join(workspacePath, "authorized_companions"),
	}

	errorValue := registrationService.RegisterCompanionBySSHKey("not-a-real-key")
	if errorValue == nil {
		t.Fatal("expected invalid ssh key to be rejected")
	}
}
