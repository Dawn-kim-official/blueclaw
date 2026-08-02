package main

import (
	"flag"
	"log"

	"github.com/Dawn-kim-official/blueclaw/internal/app"
	"github.com/Dawn-kim-official/blueclaw/internal/bluecollarharness"
	"github.com/Dawn-kim-official/blueclaw/internal/config"
)

func main() {
	runtimeConfigurationPath := flag.String("runtime", "config/runtime.example.json", "runtime configuration path")
	policyPath := flag.String("policy", "config/policy.example.json", "policy document path")
	flag.Parse()

	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(*runtimeConfigurationPath)
	if errorValue != nil {
		log.Fatal(errorValue)
	}

	application := app.NewApplication(runtimeConfiguration, *policyPath, bluecollarharness.New)
	log.Fatal(application.Start())
}
