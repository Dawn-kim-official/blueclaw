package tui

import (
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

type HarnessInfo struct {
	Name                    string
	AgentCommandPath        string
	IsKnown                 bool
	RuntimeConfigPath       string
	UnknownReason           string
	IsLiveReport            bool
	RunsAsRequesterIdentity bool
	ToolCatalogURL          string
}

func HarnessInfoFromStatus(harnessStatus HarnessStatus) HarnessInfo {
	return HarnessInfo{
		Name:                    harnessStatus.Name,
		AgentCommandPath:        harnessStatus.AgentCommandPath,
		IsKnown:                 strings.TrimSpace(harnessStatus.Name) != "",
		IsLiveReport:            true,
		RunsAsRequesterIdentity: harnessStatus.RunsAsRequesterIdentity,
		ToolCatalogURL:          harnessStatus.ToolCatalogURL,
	}
}

func UnknownHarnessInfo(runtimeConfigPath string, reason string) HarnessInfo {
	return HarnessInfo{RuntimeConfigPath: runtimeConfigPath, UnknownReason: reason}
}

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
