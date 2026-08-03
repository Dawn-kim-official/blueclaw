package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/Dawn-kim-official/blueclaw/internal/tui"
)

func main() {
	baseURL := flag.String("base-url", "http://127.0.0.1:8080", "base URL of the blueclaw admin API")
	runtimeConfigurationPath := flag.String("runtime", "", "path to the sandbox's runtime configuration JSON, used to show the configured agent harness")
	flag.Parse()

	client := tui.NewClient(*baseURL, nil)
	model := tui.NewModel(client, *runtimeConfigurationPath)

	program := tea.NewProgram(model)
	if _, errorValue := program.Run(); errorValue != nil {
		fmt.Fprintln(os.Stderr, "blueclaw-tui:", errorValue)
		os.Exit(1)
	}
}
