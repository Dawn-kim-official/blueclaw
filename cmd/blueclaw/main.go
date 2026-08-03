package main

import (
	"flag"
	"log"

	"github.com/Dawn-kim-official/blueclaw/internal/app"
	"github.com/Dawn-kim-official/blueclaw/internal/bluecollarharness"
	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/enrollment"
)

func main() {
	home := enrollment.ResolveHome()
	runtimeConfigurationPath := flag.String("runtime", home.RuntimeConfigurationPath(), "runtime configuration path")
	policyPath := flag.String("policy", home.PolicyPath(), "policy document path")
	flag.Parse()

	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(*runtimeConfigurationPath)
	if errorValue != nil {
		log.Fatalf("%v\n\nThis install has no configuration at %s yet. Run blueclaw-tui to set one up.", errorValue, *runtimeConfigurationPath)
	}

	application := app.NewApplication(runtimeConfiguration, *policyPath, bluecollarharness.New)
	log.Fatal(application.Start())
}
