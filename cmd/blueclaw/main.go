package main

import (
	"context"
	"flag"
	"log"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/app"
	"github.com/yeomyeonggeori/blueclaw/internal/bluecollarharness"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/enrollment"
)

func main() {
	home := enrollment.ResolveHome()
	runtimeConfigurationPath := flag.String("runtime", home.RuntimeConfigurationPath(), "runtime configuration path")
	policyPath := flag.String("policy", home.PolicyPath(), "policy document path")
	flag.Parse()

	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(*runtimeConfigurationPath)
	if errorValue != nil {
		log.Fatalf("%v\n\nThis install has no configuration at %s yet. Run blueclaw-cli to set one up.", errorValue, *runtimeConfigurationPath)
	}

	if errorValue := ensureManagedDatabase(home, runtimeConfiguration); errorValue != nil {
		log.Fatal(errorValue)
	}

	application := app.NewApplication(runtimeConfiguration, *policyPath, bluecollarharness.New)
	log.Fatal(application.Start())
}

func ensureManagedDatabase(home enrollment.Home, runtimeConfiguration config.RuntimeConfiguration) error {
	managedPostgres := enrollment.NewManagedPostgres(home)
	if strings.TrimSpace(runtimeConfiguration.Database.ConnectionString) != managedPostgres.ConnectionString() {
		return nil
	}
	return managedPostgres.EnsureRunning(context.Background())
}
