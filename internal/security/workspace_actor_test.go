package security

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelperFailureDetailPreservesExecutionContext(t *testing.T) {
	detail := helperFailureDetail("/usr/local/bin/blueclaw-posix-helper", "capabilities", "permission denied", []byte("stderr tail"))
	for _, expectedFragment := range []string{
		"posix helper capabilities failed",
		"path=/usr/local/bin/blueclaw-posix-helper",
		"output=stderr tail",
		"detail=permission denied",
	} {
		if !strings.Contains(detail, expectedFragment) {
			t.Fatalf("expected helper failure detail to contain %q, got %q", expectedFragment, detail)
		}
	}
}

func TestHelperExecutionFailureDetectsPermissionDeniedBeforeHelperStarts(t *testing.T) {
	if !isHelperExecutionFailure(os.ErrPermission, "") {
		t.Fatal("expected permission denied without stderr to be helper execution failure")
	}
	if isHelperExecutionFailure(os.ErrPermission, "permission denied") {
		t.Fatal("expected helper stderr to be treated as helper-reported failure")
	}
}

func writeFakeCapabilitiesHelper(t *testing.T, helperPath string, script string) {
	t.Helper()
	if errorValue := os.WriteFile(helperPath, []byte(script), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func helperInvocationCount(t *testing.T, invocationLogPath string) int {
	t.Helper()
	content, errorValue := os.ReadFile(invocationLogPath)
	if errorValue != nil {
		if os.IsNotExist(errorValue) {
			return 0
		}
		t.Fatal(errorValue)
	}
	return strings.Count(string(content), "run")
}

func TestEnsureHelperSupportsFSCachesSuccessfulProbePerHelperPath(t *testing.T) {
	directory := t.TempDir()
	helperPath := filepath.Join(directory, "blueclaw-posix-helper")
	invocationLogPath := filepath.Join(directory, "invocations.log")
	writeFakeCapabilitiesHelper(t, helperPath, "#!/bin/sh\necho run >> "+invocationLogPath+"\nprintf '{\"version\":2,\"capabilities\":[\"fs\"]}'\n")
	for attempt := 0; attempt < 3; attempt++ {
		if errorValue := ensureHelperSupportsFS(context.Background(), helperPath, "bc_person_test"); errorValue != nil {
			t.Fatalf("expected capabilities probe to succeed, got %v", errorValue)
		}
	}
	if count := helperInvocationCount(t, invocationLogPath); count != 1 {
		t.Fatalf("expected exactly one capabilities probe exec, got %d", count)
	}
}

func TestEnsureHelperSupportsFSDoesNotCacheFailedProbe(t *testing.T) {
	directory := t.TempDir()
	helperPath := filepath.Join(directory, "blueclaw-posix-helper")
	writeFakeCapabilitiesHelper(t, helperPath, "#!/bin/sh\nexit 1\n")
	if errorValue := ensureHelperSupportsFS(context.Background(), helperPath, "bc_person_test"); errorValue == nil {
		t.Fatal("expected failing capabilities probe to return an error")
	}
	writeFakeCapabilitiesHelper(t, helperPath, "#!/bin/sh\nprintf '{\"version\":2,\"capabilities\":[\"fs\"]}'\n")
	if errorValue := ensureHelperSupportsFS(context.Background(), helperPath, "bc_person_test"); errorValue != nil {
		t.Fatalf("expected probe retry after failure to succeed, got %v", errorValue)
	}
}
