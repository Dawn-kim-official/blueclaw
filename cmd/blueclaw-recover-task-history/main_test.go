package main

import (
	"io"
	"testing"
)

func TestParseCommandOptionsDefaultsToDryRun(t *testing.T) {
	t.Setenv("BLUECLAW_HISTORY_SOURCE_DSN", "")
	t.Setenv("BLUECLAW_HISTORY_TARGET_DSN", "")

	options, errorValue := parseCommandOptions([]string{
		"--source-dsn", "postgres://source",
		"--target-dsn", "postgres://target",
	}, io.Discard)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if options.Apply {
		t.Fatal("recovery must default to dry-run")
	}
}

func TestParseCommandOptionsRejectsApplyAndDryRunTogether(t *testing.T) {
	_, errorValue := parseCommandOptions([]string{
		"--source-dsn", "postgres://source",
		"--target-dsn", "postgres://target",
		"--apply",
		"--dry-run",
	}, io.Discard)

	if errorValue == nil {
		t.Fatal("expected conflicting mode flags to fail")
	}
}

func TestParseCommandOptionsAcceptsDSNsFromEnvironment(t *testing.T) {
	t.Setenv("BLUECLAW_HISTORY_SOURCE_DSN", "postgres://source")
	t.Setenv("BLUECLAW_HISTORY_TARGET_DSN", "postgres://target")

	options, errorValue := parseCommandOptions([]string{"--apply"}, io.Discard)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !options.Apply {
		t.Fatal("expected apply mode")
	}
}
