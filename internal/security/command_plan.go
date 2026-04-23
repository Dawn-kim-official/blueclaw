package security

import "time"

type CommandPlan struct {
	ExecutablePath       string            `json:"executablePath"`
	Arguments            []string          `json:"arguments"`
	WorkingDirectoryPath string            `json:"workingDirectoryPath"`
	EnvironmentVariables map[string]string `json:"environmentVariables"`
	Timeout              time.Duration     `json:"timeout"`
	UsesSandbox          bool              `json:"usesSandbox"`
}
