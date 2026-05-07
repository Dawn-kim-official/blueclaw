package security

import "time"

type CommandPlan struct {
	ExecutablePath       string            `json:"executablePath"`
	Arguments            []string          `json:"arguments"`
	Stdin                string            `json:"stdin"`
	WorkingDirectoryPath string            `json:"workingDirectoryPath"`
	EnvironmentVariables map[string]string `json:"environmentVariables"`
	Timeout              time.Duration     `json:"timeout"`
	UsesSandbox          bool              `json:"usesSandbox"`
	IsPTY                bool              `json:"isPTY"`
	ExecutionIdentity    ExecutionIdentity `json:"-"`
}
