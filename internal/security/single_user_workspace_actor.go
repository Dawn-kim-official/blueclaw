package security

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const TerminalModeSingleUser = "single_user"

type SingleUserWorkspaceActor struct {
	workspaceRootPath string
	timeoutSecond     int
}

func (actor SingleUserWorkspaceActor) Run(ctx context.Context, commandRequest CommandRequest) (CommandResult, error) {
	workingDirectory := strings.TrimSpace(commandRequest.WorkingDirectoryPath)
	if workingDirectory == "" {
		workingDirectory = actor.workspaceRootPath
	}
	command := actor.buildCommand(ctx, commandRequest)
	command.Dir = workingDirectory
	command.Env = os.Environ()
	for name, value := range commandRequest.EnvironmentVariables {
		command.Env = append(command.Env, name+"="+value)
	}
	if commandRequest.Stdin != "" {
		command.Stdin = strings.NewReader(commandRequest.Stdin)
	}
	combinedOutput, errorValue := command.CombinedOutput()
	result := CommandResult{Stdout: string(combinedOutput)}
	if errorValue != nil {
		result.ExitCode = 1
		result.Stderr = errorValue.Error()
	}
	return result, nil
}

func (actor SingleUserWorkspaceActor) buildCommand(ctx context.Context, commandRequest CommandRequest) *exec.Cmd {
	if executableName := strings.TrimSpace(commandRequest.ExecutableName); executableName != "" {
		return exec.CommandContext(ctx, executableName, commandRequest.Arguments...)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-lc", commandRequest.Command)
}

func (actor SingleUserWorkspaceActor) MkdirAll(_ context.Context, path string) error {
	return os.MkdirAll(actor.resolve(path), 0o755)
}

func (actor SingleUserWorkspaceActor) WriteFile(_ context.Context, path string, content []byte) error {
	resolvedPath := actor.resolve(path)
	if errorValue := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); errorValue != nil {
		return errorValue
	}
	return os.WriteFile(resolvedPath, content, 0o644)
}

func (actor SingleUserWorkspaceActor) BundleDirectory(context.Context, string, WorkspaceActorBundleOptions) (WorkspaceActorBundle, error) {
	return WorkspaceActorBundle{}, nil
}

func (actor SingleUserWorkspaceActor) Stat(_ context.Context, path string) (WorkspaceActorStat, error) {
	pathInformation, errorValue := os.Stat(actor.resolve(path))
	if errorValue != nil {
		return WorkspaceActorStat{}, errorValue
	}
	return WorkspaceActorStat{
		Path:        actor.resolve(path),
		IsRegular:   pathInformation.Mode().IsRegular(),
		IsDirectory: pathInformation.IsDir(),
		SizeBytes:   pathInformation.Size(),
		Mode:        pathInformation.Mode(),
	}, nil
}

func (actor SingleUserWorkspaceActor) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(actor.workspaceRootPath, path)
}
