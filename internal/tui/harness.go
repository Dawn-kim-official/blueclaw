package tui

import (
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/config"
)

// HarnessInfo describes what the TUI knows about the running sandbox's agent
// harness. There is no admin API endpoint that reports the harness a running
// sandbox is actually using, so this is read from the runtime configuration
// file passed via --runtime; it is not a live report from the sandbox
// process.
type HarnessInfo struct {
	Name              string
	AgentCommandPath  string
	IsKnown           bool
	RuntimeConfigPath string
	UnknownReason     string
}

// UnknownHarnessInfo describes a harness that could not be determined, along
// with why, so the UI can say so plainly instead of guessing.
func UnknownHarnessInfo(runtimeConfigPath string, reason string) HarnessInfo {
	return HarnessInfo{RuntimeConfigPath: runtimeConfigPath, UnknownReason: reason}
}

// LoadHarnessInfo reads agent.harness.name and agent.harness.agentCommandPath
// from the runtime configuration JSON at runtimeConfigPath. It never panics
// and never fabricates a harness name: a missing path, unreadable file, or
// empty harness name.name all report IsKnown=false with a reason.
func LoadHarnessInfo(runtimeConfigPath string) HarnessInfo {
	trimmedPath := strings.TrimSpace(runtimeConfigPath)
	if trimmedPath == "" {
		return UnknownHarnessInfo(trimmedPath, "no --runtime configuration path was provided")
	}

	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(trimmedPath)
	if errorValue != nil {
		return UnknownHarnessInfo(trimmedPath, "failed to load runtime configuration: "+errorValue.Error())
	}

	harnessName := strings.TrimSpace(runtimeConfiguration.Agent.Harness.Name)
	if harnessName == "" {
		return UnknownHarnessInfo(trimmedPath, "runtime configuration has no agent.harness.name set")
	}

	return HarnessInfo{
		Name:              harnessName,
		AgentCommandPath:  strings.TrimSpace(runtimeConfiguration.Agent.Harness.AgentCommandPath),
		IsKnown:           true,
		RuntimeConfigPath: trimmedPath,
	}
}
