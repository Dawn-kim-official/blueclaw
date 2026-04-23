package security

type CommandRequest struct {
	ExecutableName       string            `json:"executableName"`
	Arguments            []string          `json:"arguments"`
	WorkingDirectoryPath string            `json:"workingDirectoryPath"`
	EnvironmentVariables map[string]string `json:"environmentVariables"`
	IsInteractive        bool              `json:"isInteractive"`
}
