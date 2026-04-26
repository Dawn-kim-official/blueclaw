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
  "capabilities": {
    "endpoint": "http://127.0.0.1:7781",
    "unixSocketPath": "/run/internkim/capability.sock",
    "timeoutSecond": 15
  },
  "languageModel": {
    "defaultProvider": "capabilityLLM",
    "capability": {
      "model": "gemma-4-E4B-it",
      "executionMode": "auto"
    },
    "openRouter": {
      "baseURL": "https://openrouter.ai/api/v1/chat/completions",
      "modelName": "openai/gpt-4.1-mini",
      "apiKeyPath": "/run/secrets/openrouter-api-key",
      "requireParameters": true,
      "enableResponseHealing": true
    },
    "liteRTLM": {
      "wrapperPath": "/usr/local/bin/blueclaw-litert-wrapper",
      "wrapperArguments": ["--stdio"],
      "modelPath": "/models/gemma-4-E4B-it.litertlm",
      "backendPreference": ["gpu"],
      "allowCPUFallback": false,
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
  "connectors": {
    "mattermost": {
      "baseURL": "http://localhost:8065"
    },
    "slack": {
      "baseURL": "https://slack.com/api"
    },
    "signal": {
      "enabled": false
    }
  },
  "terminal": {
    "mode": "native"
  },
  "logging": {
    "directoryPath": "/workspace/.blueclaw/logs",
    "retentionDays": 7
  },
  "scheduler": {
    "retentionCheckIntervalMinute": 60,
    "taskSchedulePollIntervalSecond": 30
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
	if runtimeConfiguration.Capabilities.Endpoint != "http://127.0.0.1:7781" {
		t.Fatalf("expected capability endpoint to match, got %q", runtimeConfiguration.Capabilities.Endpoint)
	}
	if runtimeConfiguration.Capabilities.UnixSocketPath != "/run/internkim/capability.sock" {
		t.Fatalf("expected capability unix socket path to match, got %q", runtimeConfiguration.Capabilities.UnixSocketPath)
	}
	if runtimeConfiguration.Capabilities.TimeoutSecond != 15 {
		t.Fatalf("expected capability timeout to match, got %d", runtimeConfiguration.Capabilities.TimeoutSecond)
	}
	if runtimeConfiguration.Connectors.Slack.BaseURL != "https://slack.com/api" {
		t.Fatalf("expected slack base url to match, got %q", runtimeConfiguration.Connectors.Slack.BaseURL)
	}
	if runtimeConfiguration.Connectors.Signal.Enabled {
		t.Fatal("expected signal connector to be disabled")
	}
	if runtimeConfiguration.LanguageModel.DefaultProvider != "capabilityLLM" {
		t.Fatalf("expected default language model provider to match, got %q", runtimeConfiguration.LanguageModel.DefaultProvider)
	}
	if runtimeConfiguration.LanguageModel.Capability.Model != "gemma-4-E4B-it" {
		t.Fatalf("expected capability model to match, got %q", runtimeConfiguration.LanguageModel.Capability.Model)
	}
	if runtimeConfiguration.LanguageModel.Capability.ExecutionMode != "auto" {
		t.Fatalf("expected capability execution mode to match, got %q", runtimeConfiguration.LanguageModel.Capability.ExecutionMode)
	}
	if runtimeConfiguration.LanguageModel.Capability.ExecutionMode != "auto" {
		t.Fatalf("expected capability execution mode to match, got %q", runtimeConfiguration.LanguageModel.Capability.ExecutionMode)
	}
	if runtimeConfiguration.LanguageModel.LiteRTLM.ModelPath != "/models/gemma-4-E4B-it.litertlm" {
		t.Fatalf("expected litert-lm model path to match, got %q", runtimeConfiguration.LanguageModel.LiteRTLM.ModelPath)
	}
	if len(runtimeConfiguration.LanguageModel.LiteRTLM.BackendPreference) != 1 || runtimeConfiguration.LanguageModel.LiteRTLM.BackendPreference[0] != "gpu" {
		t.Fatalf("expected litert-lm backend preference to prefer gpu, got %v", runtimeConfiguration.LanguageModel.LiteRTLM.BackendPreference)
	}
	if runtimeConfiguration.LanguageModel.LiteRTLM.AllowCPUFallback {
		t.Fatal("expected litert-lm cpu fallback to be disabled")
	}
	if runtimeConfiguration.Logging.RetentionDays != 7 {
		t.Fatalf("expected log retention to match, got %d", runtimeConfiguration.Logging.RetentionDays)
	}
	if runtimeConfiguration.Scheduler.TaskSchedulePollIntervalSecond != 30 {
		t.Fatalf("expected task schedule poll interval to match, got %d", runtimeConfiguration.Scheduler.TaskSchedulePollIntervalSecond)
	}
}
