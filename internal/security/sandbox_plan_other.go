//go:build !linux

package security

import "errors"

func (commandGuardrailService CommandGuardrailService) buildSandboxCommandPlan(commandPlan CommandPlan, workspaceRootPath string) (CommandPlan, error) {
	_ = commandPlan
	_ = workspaceRootPath
	return CommandPlan{}, errors.New("sandbox mode is only available on linux")
}
