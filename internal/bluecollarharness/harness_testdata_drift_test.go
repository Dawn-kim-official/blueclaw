package bluecollarharness

import (
	"bytes"
	"os"
	"testing"
)

func TestHarnessTestdataMatchesProtocolSource(t *testing.T) {
	for _, pair := range []struct {
		protocolPath string
		harnessPath  string
	}{
		{"../../protocol/fixtures/valid.json", "../../.dependency/bluecollar/testdata/protocol-fixtures.json"},
		{"../../protocol/generated/capability-tools.json", "../../.dependency/bluecollar/testdata/capability-tools.json"},
	} {
		protocolBytes, errorValue := os.ReadFile(pair.protocolPath)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		harnessBytes, errorValue := os.ReadFile(pair.harnessPath)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !bytes.Equal(protocolBytes, harnessBytes) {
			t.Fatalf("%s drifted from %s: copy the protocol file into the harness testdata", pair.harnessPath, pair.protocolPath)
		}
	}
}
