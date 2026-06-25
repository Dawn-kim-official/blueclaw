package security

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"blueclaw/internal/config"
	"blueclaw/internal/policy"
)

func TestPOSIXSynchronizerPassesComputedStateDocumentToHelper(t *testing.T) {
	rootPath := t.TempDir()
	observedStatePath := filepath.Join(rootPath, "observed-state.json")
	helperPath := filepath.Join(rootPath, "helper")
	helperDocument := "#!/bin/sh\nset -eu\nstate_path=\"\"\nwhile [ \"$#\" -gt 0 ]; do\ncase \"$1\" in\n--state) state_path=\"$2\"; shift 2 ;;\n--workspace) shift 2 ;;\n*) shift ;;\nesac\ndone\nif [ -z \"$state_path\" ]; then exit 7; fi\ncat \"$state_path\" > \"" + observedStatePath + "\"\n"
	if errorValue := os.WriteFile(helperPath, []byte(helperDocument), 0700); errorValue != nil {
		t.Fatal(errorValue)
	}

	policyPath := filepath.Join(rootPath, "policy.json")
	policyDocument := policy.PolicyDocument{
		People: []policy.PersonPolicy{{
			PersonID: "id_22120f1e_432e6dde",
			Circles:  []string{policy.StaffCircleID},
		}},
	}
	document, errorValue := json.Marshal(policyDocument)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(policyPath, document, 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	synchronizer := NewPOSIXSynchronizer(config.TerminalConfiguration{
		POSIXHelperPath:   helperPath,
		WorkspaceRootPath: "/workspace",
	}, policyPath)
	if errorValue := synchronizer.Synchronize(context.Background()); errorValue != nil {
		t.Fatal(errorValue)
	}

	observedDocument, errorValue := os.ReadFile(observedStatePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var state POSIXState
	if errorValue := json.Unmarshal(observedDocument, &state); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !containsPOSIXDirectory(state, "/workspace/circles/staff/sites", "blueclaw", "bc_circle_staff", "2770") {
		t.Fatalf("expected staff sites directory in state, got %+v", state.Directories)
	}
}

func containsPOSIXDirectory(state POSIXState, path string, owner string, group string, modeText string) bool {
	for _, directory := range state.Directories {
		if directory.Path == path && directory.Owner == owner && directory.Group == group && directory.ModeText == modeText {
			return true
		}
	}
	return false
}
