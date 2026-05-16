package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunCapabilitiesReportsFilesystemSupport(t *testing.T) {
	var output bytes.Buffer
	previousOutput := os.Stdout
	readFile, writeFile, errorValue := os.Pipe()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	os.Stdout = writeFile
	errorValue = runCapabilities()
	_ = writeFile.Close()
	os.Stdout = previousOutput
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := output.ReadFrom(readFile); errorValue != nil {
		t.Fatal(errorValue)
	}
	var capabilities helperCapabilitiesDocument
	if errorValue := json.Unmarshal(output.Bytes(), &capabilities); errorValue != nil {
		t.Fatal(errorValue)
	}
	if capabilities.Version != 2 || !containsString(capabilities.Capabilities, "fs") {
		t.Fatalf("expected helper fs capability, got %+v", capabilities)
	}
}

func TestHelperCallerAuthorizationAllowsRootAndBlueclawOnly(t *testing.T) {
	blueclawUserID := 998
	for _, realUserID := range []int{0, blueclawUserID} {
		if !isAuthorizedHelperCaller(realUserID, blueclawUserID) {
			t.Fatalf("expected real uid %d to be authorized", realUserID)
		}
	}
	if isAuthorizedHelperCaller(1001, blueclawUserID) {
		t.Fatal("expected requester uid to be rejected")
	}
}

func TestPerformFSOperationCopiesFileWithOverwritePolicy(t *testing.T) {
	rootPath := t.TempDir()
	sourcePath := filepath.Join(rootPath, "source.txt")
	destinationPath := filepath.Join(rootPath, "destination.txt")
	if errorValue := os.WriteFile(sourcePath, []byte("first"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := performFSOperation(fsOperationRequest{
		Operation: "copy_file",
		Source:    sourcePath,
		Path:      destinationPath,
		Mode:      0660,
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := performFSOperation(fsOperationRequest{
		Operation: "copy_file",
		Source:    sourcePath,
		Path:      destinationPath,
		Mode:      0660,
	}); errorValue == nil {
		t.Fatal("expected copy_file without overwrite to reject existing destination")
	}
	if errorValue := os.WriteFile(sourcePath, []byte("second"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := performFSOperation(fsOperationRequest{
		Operation: "copy_file",
		Source:    sourcePath,
		Path:      destinationPath,
		Mode:      0660,
		Overwrite: true,
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	document, errorValue := os.ReadFile(destinationPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != "second" {
		t.Fatalf("expected overwritten destination, got %q", string(document))
	}
}

type helperCapabilitiesDocument struct {
	Version      int      `json:"version"`
	Capabilities []string `json:"capabilities"`
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
