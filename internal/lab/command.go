package lab

type ExecutableCommand struct {
	ExecutableName       string
	Arguments            []string
	WorkingDirectoryPath string
	EnvironmentVariables map[string]string
	StandardInputPath    string
}
