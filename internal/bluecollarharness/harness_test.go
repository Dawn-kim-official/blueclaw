package bluecollarharness

import (
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/config"
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
