package tool

import (
	"context"
	"fmt"
	"time"
)

type HeartbeatIntervalSetter interface {
	SetInterval(duration string) error
}

type SetHeartbeatIntervalTool struct {
	setter HeartbeatIntervalSetter
}

func NewSetHeartbeatIntervalTool(setter HeartbeatIntervalSetter) *SetHeartbeatIntervalTool {
	return &SetHeartbeatIntervalTool{setter: setter}
}

func (tool *SetHeartbeatIntervalTool) Name() string { return "set_heartbeat_interval" }

func (tool *SetHeartbeatIntervalTool) Description() string {
	return "Set the interval before the next heartbeat check-in. The daemon enforces a valid min/max range. Use this to slow down when nothing is happening or speed up when something is urgent."
}

func (tool *SetHeartbeatIntervalTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"duration": map[string]any{
				"type":        "string",
				"description": "Go duration string (e.g. '5m', '1h30m', '30s'). Must be within the configured min/max range.",
			},
		},
		"required": []string{"duration"},
	}
}

func (tool *SetHeartbeatIntervalTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
	duration, _ := arguments["duration"].(string)
	if duration == "" {
		return Result{Error: "duration is required"}, nil
	}
	if _, err := time.ParseDuration(duration); err != nil {
		return Result{Error: fmt.Sprintf("invalid duration %q: %v", duration, err)}, nil
	}
	if err := tool.setter.SetInterval(duration); err != nil {
		return Result{Error: fmt.Sprintf("failed to set heartbeat interval: %v", err)}, nil
	}
	return Result{Output: fmt.Sprintf("heartbeat interval set to %s", duration)}, nil
}
