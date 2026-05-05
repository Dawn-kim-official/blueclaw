package hooks

import (
	"context"
	"errors"
	"strings"
)

const EventPreToolUse = "PreToolUse"

type HookRequest struct {
	EventName string
	ToolName  string
	ToolInput any
}

type HookResult struct {
	UpdatedToolInput any
	BlockReason      string
}

type Hook func(context.Context, HookRequest) (HookResult, error)

type Runner struct {
	hooksByEventName map[string][]Hook
}

func NewRunner() *Runner {
	return &Runner{hooksByEventName: map[string][]Hook{}}
}

func (runner *Runner) RegisterHook(eventName string, hook Hook) {
	if strings.TrimSpace(eventName) == "" || hook == nil {
		return
	}
	runner.hooksByEventName[eventName] = append(runner.hooksByEventName[eventName], hook)
}

func (runner *Runner) Run(ctx context.Context, request HookRequest) (any, error) {
	if runner == nil {
		return request.ToolInput, nil
	}

	currentInput := request.ToolInput
	for _, hook := range runner.hooksByEventName[request.EventName] {
		result, errorValue := hook(ctx, HookRequest{
			EventName: request.EventName,
			ToolName:  request.ToolName,
			ToolInput: currentInput,
		})
		if errorValue != nil {
			return currentInput, errorValue
		}
		if strings.TrimSpace(result.BlockReason) != "" {
			return currentInput, errors.New(strings.TrimSpace(result.BlockReason))
		}
		if result.UpdatedToolInput != nil {
			currentInput = result.UpdatedToolInput
		}
	}

	return currentInput, nil
}
