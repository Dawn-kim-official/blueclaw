package config

import (
	"encoding/json"
	"os"
)

type RuntimeConfiguration struct {
	BaseURL       string                      `json:"baseURL"`
	Capabilities  CapabilityConfiguration     `json:"capabilities"`
	AgentProfiles []AgentProfileConfiguration `json:"agentProfiles"`
	LanguageModel LanguageModelConfiguration  `json:"languageModel"`
	Firecracker   FirecrackerConfiguration    `json:"firecracker"`
	Bridge        BridgeConfiguration         `json:"bridge"`
	Database      DatabaseConfiguration       `json:"database"`
	Memory        MemoryConfiguration         `json:"memory"`
	Agent         AgentConfiguration          `json:"agent"`
	Connectors    ConnectorConfiguration      `json:"connectors"`
	Logging       LoggingConfiguration        `json:"logging"`
	MCPServers    []MCPServerConfiguration    `json:"mcpServers"`
	Terminal      TerminalConfiguration       `json:"terminal"`
	Scheduler     SchedulerConfiguration      `json:"scheduler"`
}

type CapabilityConfiguration struct {
	Endpoint       string   `json:"endpoint"`
	UnixSocketPath string   `json:"unixSocketPath"`
	TimeoutSecond  int      `json:"timeoutSecond"`
	ToolNames      []string `json:"toolNames"`
}

type AgentProfileConfiguration struct {
	Name             string   `json:"name"`
	AllowedToolNames []string `json:"allowedToolNames"`
}

type MCPServerConfiguration struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Command   string   `json:"command"`
	Arguments []string `json:"arguments"`
	Endpoint  string   `json:"endpoint"`
	ToolNames []string `json:"toolNames"`
}

type AgentConfiguration struct {
	Intake             AgentIntakeConfiguration `json:"intake"`
	DefaultBudgetClass string                   `json:"defaultBudgetClass"`
	ToolResultMaxBytes int                      `json:"toolResultMaxBytes"`
}

type AgentIntakeConfiguration struct {
	Enabled       bool   `json:"enabled"`
	Model         string `json:"model"`
	ExecutionMode string `json:"executionMode"`
}

type LanguageModelConfiguration struct {
	DefaultProvider  string                               `json:"defaultProvider"`
	FallbackProvider string                               `json:"fallbackProvider"`
	Capability       LanguageModelCapabilityConfiguration `json:"capability"`
}

type LanguageModelCapabilityConfiguration struct {
	Model               string `json:"model"`
	ExecutionMode       string `json:"executionMode"`
	ContextWindowTokens int    `json:"contextWindowTokens"`
}

type FirecrackerConfiguration struct {
	FirecrackerPath     string `json:"firecrackerPath"`
	JailerPath          string `json:"jailerPath"`
	KernelImagePath     string `json:"kernelImagePath"`
	RootfsImagePath     string `json:"rootfsImagePath"`
	WorkspaceImagePath  string `json:"workspaceImagePath"`
	VCPUCount           int    `json:"vcpuCount"`
	MemoryMiB           int    `json:"memoryMiB"`
	VSockCID            uint32 `json:"vsockCID"`
	HealthPortOrService string `json:"healthPortOrService"`
	LogDirectoryPath    string `json:"logDirectoryPath"`
}

type BridgeConfiguration struct {
	Mode                     string `json:"mode"`
	AuthMode                 string `json:"authMode"`
	AuthorizedPublicKeysPath string `json:"authorizedPublicKeysPath"`
	ListenAddress            string `json:"listenAddress"`
}

type DatabaseConfiguration struct {
	Driver                 string `json:"driver"`
	ConnectionString       string `json:"connectionString"`
	MigrationDirectoryPath string `json:"migrationDirectoryPath"`
}

type MemoryConfiguration struct {
	WorkspaceID      string `json:"workspaceID"`
	GraphitiEndpoint string `json:"graphitiEndpoint"`
	GraphitiKuzuPath string `json:"graphitiKuzuPath"`
	TimeoutSecond    int    `json:"timeoutSecond"`
}

type ConnectorConfiguration struct {
	Mattermost MattermostConnectorConfiguration `json:"mattermost"`
	Slack      SlackConnectorConfiguration      `json:"slack"`
	Signal     SignalConnectorConfiguration     `json:"signal"`
}

type MattermostConnectorConfiguration struct {
	BaseURL string `json:"baseURL"`
}

type SlackConnectorConfiguration struct {
	BaseURL string `json:"baseURL"`
}

type SignalConnectorConfiguration struct {
	Enabled bool `json:"enabled"`
}

type LoggingConfiguration struct {
	DirectoryPath string `json:"directoryPath"`
	RetentionDays int    `json:"retentionDays"`
}

type TerminalConfiguration struct {
	Mode                   string   `json:"mode"`
	SandboxProvider        string   `json:"sandboxProvider"`
	WorkspaceRootPath      string   `json:"workspaceRootPath"`
	AllowedExecutableNames []string `json:"allowedExecutableNames"`
	DeniedExecutableNames  []string `json:"deniedExecutableNames"`
	DeniedPathPrefixes     []string `json:"deniedPathPrefixes"`
	TimeoutSecond          int      `json:"timeoutSecond"`
	AllowNetwork           bool     `json:"allowNetwork"`
	AllowInteractiveShell  bool     `json:"allowInteractiveShell"`
}

type SchedulerConfiguration struct {
	RetentionCheckIntervalMinute   int `json:"retentionCheckIntervalMinute"`
	TaskSchedulePollIntervalSecond int `json:"taskSchedulePollIntervalSecond"`
}

func LoadRuntimeConfiguration(path string) (RuntimeConfiguration, error) {
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return RuntimeConfiguration{}, errorValue
	}

	var configuration RuntimeConfiguration
	errorValue = json.Unmarshal(document, &configuration)
	if errorValue != nil {
		return RuntimeConfiguration{}, errorValue
	}

	return configuration, nil
}
