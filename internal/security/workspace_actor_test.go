package security

import (
	"os"
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
