//go:build linux || darwin

package integration

import (
	"slices"
	"testing"
)

func TestAncestorsBelowTemporaryRootNeverIncludeTheRootItself(testInstance *testing.T) {
	ancestors := ancestorsBelowTemporaryRoot("/tmp/TestSeparation123/001", "/tmp")

	if slices.Contains(ancestors, "/tmp") {
		testInstance.Fatalf("chmod would be applied to the temporary root itself, which on a real machine takes /tmp from 1777 to 0755 and breaks every other program on it: %v", ancestors)
	}
	if !slices.Equal(ancestors, []string{"/tmp/TestSeparation123/001", "/tmp/TestSeparation123"}) {
		testInstance.Fatalf("expected the directory and its ancestors below the root, got %v", ancestors)
	}
}

func TestAncestorsBelowTemporaryRootStopAtAPrivateTemporaryDirectory(testInstance *testing.T) {
	temporaryRootPath := "/var/folders/8n/abc/T"
	ancestors := ancestorsBelowTemporaryRoot(temporaryRootPath+"/TestSeparation456/001", temporaryRootPath)

	if slices.Contains(ancestors, temporaryRootPath) {
		testInstance.Fatalf("expected the per-user temporary root to be left alone, got %v", ancestors)
	}
	if len(ancestors) != 2 {
		testInstance.Fatalf("expected two ancestors below the root, got %v", ancestors)
	}
}
