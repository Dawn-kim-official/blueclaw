package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadHarnessInfoReportsUnknownWithoutPath(testInstance *testing.T) {
	harnessInfo := LoadHarnessInfo("")
	if harnessInfo.IsKnown {
		testInstance.Fatalf("expected unknown harness, got %+v", harnessInfo)
	}
	if harnessInfo.UnknownReason == "" {
		testInstance.Fatal("expected a reason explaining why the harness is unknown")
	}
}

func TestLoadHarnessInfoReportsUnknownForMissingFile(testInstance *testing.T) {
	harnessInfo := LoadHarnessInfo(filepath.Join(testInstance.TempDir(), "does-not-exist.json"))
	if harnessInfo.IsKnown {
		testInstance.Fatalf("expected unknown harness, got %+v", harnessInfo)
	}
}

func TestLoadHarnessInfoReportsUnknownWhenNameMissing(testInstance *testing.T) {
	configPath := writeTestRuntimeConfiguration(testInstance, `{"agent":{"harness":{}}}`)
	harnessInfo := LoadHarnessInfo(configPath)
	if harnessInfo.IsKnown {
		testInstance.Fatalf("expected unknown harness, got %+v", harnessInfo)
	}
}

func TestLoadHarnessInfoReadsNameAndCommandPath(testInstance *testing.T) {
	configPath := writeTestRuntimeConfiguration(testInstance, `{"agent":{"harness":{"name":"claude-agent-sdk","agentCommandPath":"/usr/local/bin/claude-agent"}}}`)
	harnessInfo := LoadHarnessInfo(configPath)
	if !harnessInfo.IsKnown {
		testInstance.Fatalf("expected known harness, got %+v", harnessInfo)
	}
	if harnessInfo.Name != "claude-agent-sdk" || harnessInfo.AgentCommandPath != "/usr/local/bin/claude-agent" {
		testInstance.Fatalf("unexpected harness info: %+v", harnessInfo)
	}
}

func writeTestRuntimeConfiguration(testInstance *testing.T, jsonDocument string) string {
	configPath := filepath.Join(testInstance.TempDir(), "runtime.json")
	if errorValue := os.WriteFile(configPath, []byte(jsonDocument), 0o600); errorValue != nil {
		testInstance.Fatalf("write test runtime configuration: %v", errorValue)
	}
	return configPath
}
