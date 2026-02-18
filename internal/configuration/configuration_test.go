package configuration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfiguration(t *testing.T) {
	configuration := DefaultConfiguration()
	if configuration.LLMProvider != DefaultLLMProvider {
		t.Errorf("expected LLMProvider %q, got %q", DefaultLLMProvider, configuration.LLMProvider)
	}
	if configuration.ContainerRuntime != DefaultContainerRuntime {
		t.Errorf("expected ContainerRuntime %q, got %q", DefaultContainerRuntime, configuration.ContainerRuntime)
	}
	if configuration.APIPort != DefaultAPIPort {
		t.Errorf("expected APIPort %d, got %d", DefaultAPIPort, configuration.APIPort)
	}
	if configuration.MemoryTopK != DefaultMemoryTopK {
		t.Errorf("expected MemoryTopK %d, got %d", DefaultMemoryTopK, configuration.MemoryTopK)
	}
	if configuration.EmbeddingPort != DefaultEmbeddingPort {
		t.Errorf("expected EmbeddingPort %d, got %d", DefaultEmbeddingPort, configuration.EmbeddingPort)
	}
	if configuration.AchievementTTL != DefaultAchievementTTL.String() {
		t.Errorf("expected AchievementTTL %q, got %q", DefaultAchievementTTL.String(), configuration.AchievementTTL)
	}
}

func TestLoadFromTOML(t *testing.T) {
	temporaryDirectory := t.TempDir()
	configPath := filepath.Join(temporaryDirectory, "config.toml")
	tomlContent := `llmProvider = "openai"
apiPort = 9090
memoryTopK = 10
heartbeatInterval = "1h"
`
	if err := os.WriteFile(configPath, []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}
	configuration, err := Load(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if configuration.LLMProvider != "openai" {
		t.Errorf("expected LLMProvider %q, got %q", "openai", configuration.LLMProvider)
	}
	if configuration.APIPort != 9090 {
		t.Errorf("expected APIPort %d, got %d", 9090, configuration.APIPort)
	}
	if configuration.MemoryTopK != 10 {
		t.Errorf("expected MemoryTopK %d, got %d", 10, configuration.MemoryTopK)
	}
}

func TestLoadMissingFileFallsBackToDefaults(t *testing.T) {
	configuration, err := Load("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("unexpected error for missing file: %v", err)
	}
	if configuration.LLMProvider != DefaultLLMProvider {
		t.Errorf("expected default LLMProvider %q, got %q", DefaultLLMProvider, configuration.LLMProvider)
	}
}

func TestLoadInvalidTOMLReturnsError(t *testing.T) {
	temporaryDirectory := t.TempDir()
	configPath := filepath.Join(temporaryDirectory, "config.toml")
	if err := os.WriteFile(configPath, []byte("llmProvider = [invalid\n  broken = {toml"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected error for invalid TOML, got nil")
	}
}

func TestEnvironmentVariableOverrides(t *testing.T) {
	temporaryDirectory := t.TempDir()
	configPath := filepath.Join(temporaryDirectory, "config.toml")
	if err := os.WriteFile(configPath, []byte(`llmProvider = "anthropic"`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-123")
	t.Setenv("BLUECLAW_LLM_PROVIDER", "openai")
	t.Setenv("BLUECLAW_API_PORT", "3000")
	configuration, err := Load(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if configuration.AnthropicAPIKey != "test-key-123" {
		t.Errorf("expected AnthropicAPIKey %q, got %q", "test-key-123", configuration.AnthropicAPIKey)
	}
	if configuration.LLMProvider != "openai" {
		t.Errorf("expected LLMProvider %q from env override, got %q", "openai", configuration.LLMProvider)
	}
	if configuration.APIPort != 3000 {
		t.Errorf("expected APIPort %d from env override, got %d", 3000, configuration.APIPort)
	}
}

func TestParsedHeartbeatInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval string
		expected string
	}{
		{"valid duration", "1h", "1h0m0s"},
		{"invalid falls back to default", "invalid", DefaultHeartbeatInterval.String()},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			configuration := Configuration{HeartbeatInterval: testCase.interval}
			if got := configuration.ParsedHeartbeatInterval().String(); got != testCase.expected {
				t.Errorf("ParsedHeartbeatInterval() = %q, want %q", got, testCase.expected)
			}
		})
	}
}

func TestParsedAchievementTTL(t *testing.T) {
	tests := []struct {
		name     string
		ttl      string
		expected string
	}{
		{"default fallback on empty", "", DefaultAchievementTTL.String()},
		{"custom 24h", "24h", "24h0m0s"},
		{"invalid falls back to default", "notaduration", DefaultAchievementTTL.String()},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			configuration := Configuration{AchievementTTL: testCase.ttl}
			if got := configuration.ParsedAchievementTTL().String(); got != testCase.expected {
				t.Errorf("ParsedAchievementTTL() = %q, want %q", got, testCase.expected)
			}
		})
	}
}

func TestActiveAPIKey(t *testing.T) {
	tests := []struct {
		provider    string
		expectedKey string
	}{
		{"anthropic", "anthropic-key"},
		{"openai", "openai-key"},
		{"gemini", "gemini-key"},
		{"deepseek", "deepseek-key"},
		{"glm", "glm-key"},
		{"unknown", ""},
	}
	for _, testCase := range tests {
		t.Run(testCase.provider, func(t *testing.T) {
			configuration := Configuration{
				LLMProvider:     testCase.provider,
				AnthropicAPIKey: "anthropic-key",
				OpenAIAPIKey:    "openai-key",
				GeminiAPIKey:    "gemini-key",
				DeepSeekAPIKey:  "deepseek-key",
				GLMAPIKey:       "glm-key",
			}
			if got := configuration.ActiveAPIKey(); got != testCase.expectedKey {
				t.Errorf("ActiveAPIKey() = %q, want %q", got, testCase.expectedKey)
			}
		})
	}
}
