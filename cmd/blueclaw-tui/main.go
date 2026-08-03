package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/enrollment"
	"github.com/Dawn-kim-official/blueclaw/internal/tui"
)

func main() {
	baseURL := flag.String("base-url", "", "base URL of the blueclaw admin API, taken from the install when empty")
	runtimeConfigurationPath := flag.String("runtime", "", "path to the sandbox's runtime configuration JSON, used to show the configured agent harness")
	flag.Parse()

	home := enrollment.ResolveHome()
	if !home.IsEnrolled() {
		if errorValue := runSetup(home); errorValue != nil {
			fmt.Fprintln(os.Stderr, "blueclaw:", errorValue)
			os.Exit(1)
		}
		if !home.IsEnrolled() {
			return
		}
	}

	configurationPath := *runtimeConfigurationPath
	if configurationPath == "" {
		configurationPath = home.RuntimeConfigurationPath()
	}
	client := tui.NewClient(resolveBaseURL(*baseURL, configurationPath), nil)
	model := tui.NewModel(client, configurationPath)
	if operator, isFound := enrollment.OperatorPerson(home); isFound {
		model = model.UseRequester(operator.PersonID)
	}
	program := tea.NewProgram(model)
	if _, errorValue := program.Run(); errorValue != nil {
		fmt.Fprintln(os.Stderr, "blueclaw:", errorValue)
		os.Exit(1)
	}
}

func resolveBaseURL(configuredBaseURL string, runtimeConfigurationPath string) string {
	if strings.TrimSpace(configuredBaseURL) != "" {
		return configuredBaseURL
	}
	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(runtimeConfigurationPath)
	if errorValue != nil || strings.TrimSpace(runtimeConfiguration.BaseURL) == "" {
		return "http://127.0.0.1:8080"
	}
	return runtimeConfiguration.BaseURL
}

func runSetup(home enrollment.Home) error {
	program := tea.NewProgram(tui.NewSetupModel(home))
	_, errorValue := program.Run()
	return errorValue
}
