package acpharness

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
)

type AgentCommand struct {
	Path             string
	Arguments        []string
	WorkingDirectory string
	Environment      []string
}

func (agentCommand AgentCommand) Start(ctx context.Context) (io.Writer, io.Reader, func() error, error) {
	if strings.TrimSpace(agentCommand.Path) == "" {
		return nil, nil, nil, errors.New("acp harness needs an agent command to run")
	}
	command := exec.CommandContext(ctx, agentCommand.Path, agentCommand.Arguments...)
	command.Dir = agentCommand.WorkingDirectory
	command.Env = agentCommand.Environment
	agentInput, errorValue := command.StdinPipe()
	if errorValue != nil {
		return nil, nil, nil, errorValue
	}
	agentOutput, errorValue := command.StdoutPipe()
	if errorValue != nil {
		return nil, nil, nil, errorValue
	}
	if errorValue := command.Start(); errorValue != nil {
		return nil, nil, nil, errorValue
	}
	return agentInput, agentOutput, func() error {
		_ = agentInput.Close()
		return command.Wait()
	}, nil
}
