package mcp

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

type ServerClient struct{}

func (serverClient ServerClient) InvokeTool(ctx context.Context, serverDefinition ServerDefinition, invocation Invocation) (string, error) {
	if serverDefinition.Transport != "stdio" {
		return "", errors.New("only stdio transport is supported in v1")
	}

	command := exec.CommandContext(ctx, serverDefinition.Command, serverDefinition.Arguments...)
	command.Stdin = bytes.NewBufferString(invocation.Input)

	output, errorValue := command.Output()
	if errorValue != nil {
		return "", errorValue
	}

	return string(output), nil
}
