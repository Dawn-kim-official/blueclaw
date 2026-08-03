package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/Dawn-kim-official/blueclaw/internal/enrollment"
	"github.com/Dawn-kim-official/blueclaw/internal/tui"
)

func main() {
	baseURL := flag.String("base-url", "http://127.0.0.1:8080", "base URL of the blueclaw admin API")
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
	client := tui.NewClient(*baseURL, nil)
	program := tea.NewProgram(tui.NewModel(client, configurationPath))
	if _, errorValue := program.Run(); errorValue != nil {
		fmt.Fprintln(os.Stderr, "blueclaw:", errorValue)
		os.Exit(1)
	}
}

func runSetup(home enrollment.Home) error {
	program := tea.NewProgram(tui.NewSetupModel(home))
	_, errorValue := program.Run()
	return errorValue
}
