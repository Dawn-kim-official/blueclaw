package configuration

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	DefaultLLMProvider          = "anthropic"
	DefaultContainerRuntime     = ""
	DefaultContainerImage       = "blueclaw:latest"
	DefaultAPIPort              = 8080
	DefaultHeartbeatInterval    = 30 * time.Minute
	DefaultMinHeartbeatInterval = 1 * time.Minute
	DefaultMaxHeartbeatInterval = 4 * time.Hour
	DefaultOutboxPollInterval   = 2 * time.Second
	DefaultMemoryTopK           = 5
	DefaultEmbeddingPort        = 8990
	DefaultAchievementTTL       = 168 * time.Hour
	DefaultContainerNetworkMode = "bridge"
)

type Configuration struct {
	LLMProvider          string `toml:"llmProvider"`
	AnthropicAPIKey      string `toml:"anthropicApiKey"`
	OpenAIAPIKey         string `toml:"openaiApiKey"`
	GeminiAPIKey         string `toml:"geminiApiKey"`
	DeepSeekAPIKey       string `toml:"deepseekApiKey"`
	GLMAPIKey            string `toml:"glmApiKey"`
	ProviderEndpoint     string `toml:"providerEndpoint"`
	ContainerRuntime     string `toml:"containerRuntime"`
	ContainerImage       string `toml:"containerImage"`
	ContainerNetworkMode string `toml:"containerNetwork"`
	APIPort              int    `toml:"apiPort"`
	HeartbeatInterval    string `toml:"heartbeatInterval"`
	MinHeartbeatInterval string `toml:"minHeartbeatInterval"`
	MaxHeartbeatInterval string `toml:"maxHeartbeatInterval"`
	OutboxPollInterval   string `toml:"outboxPollInterval"`
	MemoryTopK           int    `toml:"memoryTopK"`
	Model                string `toml:"model"`
	EmbeddingPort        int    `toml:"embeddingPort"`
	AchievementTTL       string `toml:"achievementTTL"`
	Debug                bool   `toml:"-"`
}

func DefaultConfiguration() Configuration {
	return Configuration{
		LLMProvider:          DefaultLLMProvider,
		ContainerRuntime:     DefaultContainerRuntime,
		ContainerImage:       DefaultContainerImage,
		ContainerNetworkMode: DefaultContainerNetworkMode,
		APIPort:              DefaultAPIPort,
		HeartbeatInterval:    DefaultHeartbeatInterval.String(),
		MinHeartbeatInterval: DefaultMinHeartbeatInterval.String(),
		MaxHeartbeatInterval: DefaultMaxHeartbeatInterval.String(),
		OutboxPollInterval:   DefaultOutboxPollInterval.String(),
		MemoryTopK:           DefaultMemoryTopK,
		EmbeddingPort:        DefaultEmbeddingPort,
		AchievementTTL:       DefaultAchievementTTL.String(),
	}
}

func Load(filePath string) (Configuration, error) {
	configuration := DefaultConfiguration()
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			applyEnvironmentOverrides(&configuration)
			return configuration, nil
		}
		return configuration, fmt.Errorf("reading configuration file: %w", err)
	}
	if _, err := toml.Decode(string(data), &configuration); err != nil {
		return DefaultConfiguration(), fmt.Errorf("parsing configuration file: %w", err)
	}
	applyEnvironmentOverrides(&configuration)
	return configuration, nil
}

func applyEnvironmentOverrides(configuration *Configuration) {
	if value := os.Getenv("ANTHROPIC_API_KEY"); value != "" {
		configuration.AnthropicAPIKey = value
	}
	if value := os.Getenv("OPENAI_API_KEY"); value != "" {
		configuration.OpenAIAPIKey = value
	}
	if value := os.Getenv("GEMINI_API_KEY"); value != "" {
		configuration.GeminiAPIKey = value
	}
	if value := os.Getenv("DEEPSEEK_API_KEY"); value != "" {
		configuration.DeepSeekAPIKey = value
	}
	if value := os.Getenv("GLM_API_KEY"); value != "" {
		configuration.GLMAPIKey = value
	}
	if value := os.Getenv("BLUECLAW_LLM_PROVIDER"); value != "" {
		configuration.LLMProvider = value
	}
	if value := os.Getenv("BLUECLAW_CONTAINER_RUNTIME"); value != "" {
		configuration.ContainerRuntime = value
	}
	if value := os.Getenv("BLUECLAW_API_PORT"); value != "" {
		if port, err := strconv.Atoi(value); err == nil {
			configuration.APIPort = port
		}
	}
	if value := os.Getenv("BLUECLAW_MODEL"); value != "" {
		configuration.Model = value
	}
	if value := os.Getenv("BLUECLAW_CONTAINER_IMAGE"); value != "" {
		configuration.ContainerImage = value
	}
}

func (configuration Configuration) ParsedHeartbeatInterval() time.Duration {
	duration, err := time.ParseDuration(configuration.HeartbeatInterval)
	if err != nil {
		return DefaultHeartbeatInterval
	}
	return duration
}

func (configuration Configuration) ParsedMinHeartbeatInterval() time.Duration {
	duration, err := time.ParseDuration(configuration.MinHeartbeatInterval)
	if err != nil {
		return DefaultMinHeartbeatInterval
	}
	return duration
}

func (configuration Configuration) ParsedMaxHeartbeatInterval() time.Duration {
	duration, err := time.ParseDuration(configuration.MaxHeartbeatInterval)
	if err != nil {
		return DefaultMaxHeartbeatInterval
	}
	return duration
}

func (configuration Configuration) ParsedOutboxPollInterval() time.Duration {
	duration, err := time.ParseDuration(configuration.OutboxPollInterval)
	if err != nil {
		return DefaultOutboxPollInterval
	}
	return duration
}
func (configuration Configuration) ParsedAchievementTTL() time.Duration {
	duration, err := time.ParseDuration(configuration.AchievementTTL)
	if err != nil {
		return DefaultAchievementTTL
	}
	return duration
}

func (configuration Configuration) ActiveAPIKey() string {
	switch configuration.LLMProvider {
	case "anthropic":
		return configuration.AnthropicAPIKey
	case "openai":
		return configuration.OpenAIAPIKey
	case "gemini":
		return configuration.GeminiAPIKey
	case "deepseek":
		return configuration.DeepSeekAPIKey
	case "glm":
		return configuration.GLMAPIKey
	default:
		return ""
	}
}

func BlueclawDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".blueclaw")
	}
	return filepath.Join(home, ".blueclaw")
}

func ConfigFilePath() string {
	return filepath.Join(BlueclawDirectory(), "config.toml")
}
