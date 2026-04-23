package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRuntimeConfigurationIncludesFirecrackerAndBridge(t *testing.T) {
	workspacePath := t.TempDir()
	runtimeConfigurationPath := filepath.Join(workspacePath, "runtime.json")
	runtimeConfigurationDocument := `{
  "baseURL": "http://127.0.0.1:8080",
  "languageModel": {
    "defaultProvider": "openRouter",
    "fallbackProvider": "liteRTLM",
    "openRouter": {
      "baseURL": "https://openrouter.ai/api/v1/chat/completions",
      "modelName": "openai/gpt-4.1-mini",
      "requireParameters": true,
      "enableResponseHealing": true
    },
    "liteRTLM": {
      "wrapperPath": "/usr/local/bin/blueclaw-litert-wrapper",
      "wrapperArguments": ["--stdio"],
      "modelPath": "/models/gemma-3n.litertlm",
      "backend": "cpu",
      "constraintProvider": "llguidance"
    }
  },
  "firecracker": {
    "firecrackerPath": "/usr/bin/firecracker",
    "jailerPath": "/usr/bin/jailer",
    "kernelImagePath": "/opt/kernel",
    "rootfsImagePath": "/opt/rootfs.ext4",
    "workspaceImagePath": "/var/lib/blueclaw/workspace.ext4",
    "vcpuCount": 4,
    "memoryMiB": 8192,
    "vsockCID": 52,
    "healthPortOrService": "8080",
    "logDirectoryPath": "/var/log/blueclaw"
  },
  "bridge": {
    "mode": "localAgent",
    "authMode": "sshKeyReuse",
    "authorizedPublicKeysPath": "/var/lib/blueclaw/authorized_companions",
    "listenAddress": "127.0.0.1:7778"
  },
  "terminal": {
    "mode": "native"
  },
  "scheduler": {
    "retentionCheckIntervalMinute": 60
  }
}`

	errorValue := os.WriteFile(runtimeConfigurationPath, []byte(runtimeConfigurationDocument), 0o600)
	if errorValue != nil {
		t.Fatalf("expected runtime configuration to be written: %v", errorValue)
	}

	runtimeConfiguration, errorValue := LoadRuntimeConfiguration(runtimeConfigurationPath)
	if errorValue != nil {
		t.Fatalf("expected runtime configuration to load: %v", errorValue)
	}

	if runtimeConfiguration.Firecracker.VCPUCount != 4 {
		t.Fatalf("expected vcpu count to match, got %d", runtimeConfiguration.Firecracker.VCPUCount)
	}
	if runtimeConfiguration.Firecracker.VSockCID != 52 {
		t.Fatalf("expected vsock cid to match, got %d", runtimeConfiguration.Firecracker.VSockCID)
	}
	if runtimeConfiguration.Bridge.AuthMode != "sshKeyReuse" {
		t.Fatalf("expected bridge auth mode to match, got %q", runtimeConfiguration.Bridge.AuthMode)
	}
	if runtimeConfiguration.Bridge.ListenAddress != "127.0.0.1:7778" {
		t.Fatalf("expected bridge listen address to match, got %q", runtimeConfiguration.Bridge.ListenAddress)
	}
	if runtimeConfiguration.LanguageModel.DefaultProvider != "openRouter" {
		t.Fatalf("expected default language model provider to match, got %q", runtimeConfiguration.LanguageModel.DefaultProvider)
	}
	if runtimeConfiguration.LanguageModel.LiteRTLM.ConstraintProvider != "llguidance" {
		t.Fatalf("expected litert-lm constraint provider to match, got %q", runtimeConfiguration.LanguageModel.LiteRTLM.ConstraintProvider)
	}
}
