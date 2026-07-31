package integration

import (
	"path/filepath"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/policy"
)

func TestPolicySaveReload(t *testing.T) {
	policyLoader := policy.PolicyLoader{}
	policyDocument, errorValue := policyLoader.LoadPolicyDocument("../../config/policy.example.json")
	if errorValue != nil {
		t.Fatalf("expected policy document to load: %v", errorValue)
	}

	validator := policy.PolicyValidator{}
	errorValue = validator.ValidatePolicyDocument(policyDocument)
	if errorValue != nil {
		t.Fatalf("expected policy document to validate: %v", errorValue)
	}

	workspacePath := t.TempDir()
	targetPath := filepath.Join(workspacePath, "policy.json")
	saver := policy.PolicySaver{}
	errorValue = saver.SavePolicyDocumentAtomically(targetPath, policyDocument)
	if errorValue != nil {
		t.Fatalf("expected policy document to save: %v", errorValue)
	}

	watcher := &policy.PolicyWatcher{}
	watcher.ReloadPolicyDocument(policyDocument)
	if watcher.CurrentPolicyDocument().Retention.RawEventDays != 60 {
		t.Fatal("expected retention days to be retained")
	}
}
