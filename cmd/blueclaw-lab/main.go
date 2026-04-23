package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"blueclaw/internal/lab"
)

type PrintingCommandRunner struct{}

func (printingCommandRunner PrintingCommandRunner) Run(ctx context.Context, executableCommand lab.ExecutableCommand) error {
	_ = ctx
	printExecutableCommand(executableCommand)
	return nil
}

func (printingCommandRunner PrintingCommandRunner) Start(ctx context.Context, executableCommand lab.ExecutableCommand) error {
	_ = ctx
	printExecutableCommand(executableCommand)
	return nil
}

func (printingCommandRunner PrintingCommandRunner) Output(ctx context.Context, executableCommand lab.ExecutableCommand) (string, error) {
	_ = ctx
	printExecutableCommand(executableCommand)
	return "127.0.0.1", nil
}

func main() {
	configurationPath := flag.String("configuration", "config/lab.example.json", "lab configuration path")
	mode := flag.String("mode", "", "lab mode override")
	dryRun := flag.Bool("dry-run", false, "print commands without executing them")
	flag.Parse()

	if flag.NArg() == 0 {
		log.Fatal("lab command is required")
	}

	configuration, errorValue := lab.LoadConfiguration(*configurationPath)
	if errorValue != nil {
		log.Fatal(errorValue)
	}
	if *mode != "" {
		configuration.Host.Mode = *mode
	}

	commandRunner := lab.CommandRunner(lab.OperatingSystemCommandRunner{})
	if *dryRun {
		commandRunner = PrintingCommandRunner{}
	}

	repositoryRootPath, errorValue := os.Getwd()
	if errorValue != nil {
		log.Fatal(errorValue)
	}

	service := lab.NewService(configuration, commandRunner, repositoryRootPath)
	ctx := context.Background()

	commandName := flag.Arg(0)
	switch commandName {
	case "image-build":
		errorValue = service.ImageBuild(ctx)
	case "vm-up":
		errorValue = service.VirtualMachineUp(ctx)
	case "vm-down":
		errorValue = service.VirtualMachineDown(ctx)
	case "vm-ssh":
		errorValue = service.VirtualMachineSSH(ctx, flag.Args()[1:])
	case "smoke-firecracker":
		errorValue = service.SmokeFirecracker(ctx)
	case "scenario-browser-handoff":
		errorValue = service.ScenarioBrowserHandoff(ctx)
	case "scenario-mattermost":
		errorValue = service.ScenarioMattermost(ctx)
	case "scenario-slack":
		errorValue = service.ScenarioSlack(ctx)
	default:
		errorValue = fmt.Errorf("unsupported lab command: %s", commandName)
	}
	if errorValue != nil {
		log.Fatal(errorValue)
	}
}

func printExecutableCommand(executableCommand lab.ExecutableCommand) {
	parts := []string{executableCommand.ExecutableName}
	parts = append(parts, executableCommand.Arguments...)
	if executableCommand.StandardInputPath != "" {
		parts = append(parts, "<", executableCommand.StandardInputPath)
	}

	fmt.Println(strings.Join(parts, " "))
}
