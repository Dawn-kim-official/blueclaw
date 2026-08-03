package bluecollarharness

import (
	"testing"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/bluecollar"
)

func TestDeriveTurnOptionsWiresContextWindowTokens(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.Capability.ContextWindowTokens = 128000
	seed := int64(41)
	temperature := 0.2
	runtimeConfiguration.Agent.GenerationOptions.Seed = &seed
	runtimeConfiguration.Agent.GenerationOptions.Temperature = &temperature

	options := deriveTurnOptions(runtimeConfiguration)

	if options.ContextWindowTokens != 128000 {
		t.Fatalf("expected context window tokens to be wired, got %d", options.ContextWindowTokens)
	}
	if options.GenerationOptions.Seed == nil || *options.GenerationOptions.Seed != seed {
		t.Fatalf("expected generation seed to be wired, got %+v", options.GenerationOptions)
	}
	if options.GenerationOptions.Temperature == nil || *options.GenerationOptions.Temperature != temperature {
		t.Fatalf("expected generation temperature to be wired, got %+v", options.GenerationOptions)
	}
}

func TestVirtualTurnOptionsUseProductionTaskLevelBudget(t *testing.T) {
	defaultOptions := virtualTurnOptions(agentcontract.TurnOptions{})
	lowProfile := bluecollar.TaskLevelProfileForLevel(agentcontract.TaskLevelLow)
	if defaultOptions.TaskLevel != lowProfile.TaskLevel ||
		defaultOptions.MaxIterationCount != lowProfile.MaxIterationCount ||
		defaultOptions.MaxToolCallCount != lowProfile.MaxToolCallCount ||
		defaultOptions.MaxElapsedSecond != int(lowProfile.Duration.Seconds()) {
		t.Fatalf("expected production low defaults, got %+v", defaultOptions)
	}

	xHighOptions := virtualTurnOptions(agentcontract.TurnOptions{TaskLevel: agentcontract.TaskLevelXHigh})
	xHighProfile := bluecollar.TaskLevelProfileForLevel(agentcontract.TaskLevelXHigh)
	if xHighOptions.TaskLevel != xHighProfile.TaskLevel ||
		xHighOptions.MaxElapsedSecond != int(xHighProfile.Duration.Seconds()) {
		t.Fatalf("expected xhigh task budget, got %+v", xHighOptions)
	}
}
