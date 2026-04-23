package lab

import (
	"context"
	"os"
	"os/exec"
)

type CommandRunner interface {
	Run(context.Context, ExecutableCommand) error
	Start(context.Context, ExecutableCommand) error
	Output(context.Context, ExecutableCommand) (string, error)
}

type OperatingSystemCommandRunner struct{}

func (operatingSystemCommandRunner OperatingSystemCommandRunner) Run(ctx context.Context, executableCommand ExecutableCommand) error {
	command, standardInputFile, errorValue := createCommand(ctx, executableCommand)
	if errorValue != nil {
		return errorValue
	}
	if standardInputFile != nil {
		defer standardInputFile.Close()
	}

	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func (operatingSystemCommandRunner OperatingSystemCommandRunner) Start(ctx context.Context, executableCommand ExecutableCommand) error {
	command, standardInputFile, errorValue := createCommand(ctx, executableCommand)
	if errorValue != nil {
		return errorValue
	}
	if standardInputFile != nil {
		_ = standardInputFile.Close()
	}

	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	errorValue = command.Start()
	if errorValue != nil {
		return errorValue
	}

	return command.Process.Release()
}

func (operatingSystemCommandRunner OperatingSystemCommandRunner) Output(ctx context.Context, executableCommand ExecutableCommand) (string, error) {
	command, standardInputFile, errorValue := createCommand(ctx, executableCommand)
	if errorValue != nil {
		return "", errorValue
	}
	if standardInputFile != nil {
		defer standardInputFile.Close()
	}

	output, errorValue := command.Output()
	if errorValue != nil {
		return "", errorValue
	}

	return string(output), nil
}

func createCommand(ctx context.Context, executableCommand ExecutableCommand) (*exec.Cmd, *os.File, error) {
	command := exec.CommandContext(ctx, executableCommand.ExecutableName, executableCommand.Arguments...)
	command.Dir = executableCommand.WorkingDirectoryPath
	command.Env = mergeEnvironmentVariables(os.Environ(), executableCommand.EnvironmentVariables)

	if executableCommand.StandardInputPath == "" {
		return command, nil, nil
	}

	standardInputFile, errorValue := os.Open(executableCommand.StandardInputPath)
	if errorValue != nil {
		return nil, nil, errorValue
	}
	command.Stdin = standardInputFile

	return command, standardInputFile, nil
}

func mergeEnvironmentVariables(baseEnvironmentVariables []string, extraEnvironmentVariables map[string]string) []string {
	mergedEnvironmentVariables := append([]string{}, baseEnvironmentVariables...)
	for key, value := range extraEnvironmentVariables {
		mergedEnvironmentVariables = append(mergedEnvironmentVariables, key+"="+value)
	}

	return mergedEnvironmentVariables
}
