//go:build linux

package security

import (
	"errors"
	"os"
	"os/exec"
)

func (commandGuardrailService CommandGuardrailService) buildSandboxCommandPlan(commandPlan CommandPlan, workspaceRootPath string) (CommandPlan, error) {
	bubblewrapPath, errorValue := exec.LookPath("bwrap")
	if errorValue != nil {
		return CommandPlan{}, errors.New("sandbox mode requires bubblewrap on linux")
	}

	arguments := []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-all",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--bind", workspaceRootPath, workspaceRootPath,
		"--chdir", commandPlan.WorkingDirectoryPath,
	}

	if !commandGuardrailService.terminalConfiguration.AllowNetwork {
		arguments = append(arguments, "--unshare-net")
	}

	for _, systemPath := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc"} {
		if information, statErrorValue := os.Stat(systemPath); statErrorValue == nil && information.IsDir() {
			arguments = append(arguments, "--ro-bind", systemPath, systemPath)
		}
	}

	for name, value := range commandPlan.EnvironmentVariables {
		arguments = append(arguments, "--setenv", name, value)
	}

	arguments = append(arguments, commandPlan.ExecutablePath)
	arguments = append(arguments, commandPlan.Arguments...)

	return CommandPlan{
		ExecutablePath:       bubblewrapPath,
		Arguments:            arguments,
		WorkingDirectoryPath: workspaceRootPath,
		EnvironmentVariables: map[string]string{},
		Timeout:              commandPlan.Timeout,
		UsesSandbox:          true,
	}, nil
}
