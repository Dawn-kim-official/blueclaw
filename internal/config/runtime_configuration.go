package config

import (
	"encoding/json"
	"os"
)

type RuntimeConfiguration struct {
	BaseURL       string                      `json:"baseURL"`
	AgentProfiles []AgentProfileConfiguration `json:"agentProfiles"`
	MCPServers    []MCPServerConfiguration    `json:"mcpServers"`
	Terminal      TerminalConfiguration       `json:"terminal"`
	Scheduler     SchedulerConfiguration      `json:"scheduler"`
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
	RetentionCheckIntervalMinute int `json:"retentionCheckIntervalMinute"`
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
