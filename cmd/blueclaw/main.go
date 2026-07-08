package main

import (
	"flag"
	"log"

	"blueclaw/internal/app"
	"blueclaw/internal/config"
)

func main() {
	runtimeConfigurationPath := flag.String("runtime", "config/runtime.example.json", "runtime configuration path")
	policyPath := flag.String("policy", "config/policy.example.json", "policy document path")
	flag.Parse()

	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(*runtimeConfigurationPath)
	if errorValue != nil {
		log.Fatal(errorValue)
	}

	application := app.NewApplication(runtimeConfiguration, *policyPath)
	log.Fatal(application.Start())
}
