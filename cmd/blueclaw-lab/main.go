package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"blueclaw/internal/e2e"
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
	virtualScenarioName := flag.String("scenario", "slides", "virtual session scenario name")
	virtualArtifactDirectoryPath := flag.String("artifact-dir", ".artifacts/blueclaw-e2e", "virtual session artifact directory")
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
	case "virtual-session":
		scenarioName, artifactDirectoryPath, parseError := parseVirtualSessionArguments(flag.Args()[1:], *virtualScenarioName, *virtualArtifactDirectoryPath)
		if parseError != nil {
			errorValue = parseError
		} else {
			errorValue = runVirtualSession(ctx, scenarioName, artifactDirectoryPath)
		}
	default:
		errorValue = fmt.Errorf("unsupported lab command: %s", commandName)
	}
	if errorValue != nil {
		log.Fatal(errorValue)
	}
}

func parseVirtualSessionArguments(arguments []string, defaultScenarioName string, defaultArtifactDirectoryPath string) (string, string, error) {
	flagSet := flag.NewFlagSet("virtual-session", flag.ContinueOnError)
	scenarioName := flagSet.String("scenario", defaultScenarioName, "virtual session scenario name")
	artifactDirectoryPath := flagSet.String("artifact-dir", defaultArtifactDirectoryPath, "virtual session artifact directory")
	if errorValue := flagSet.Parse(arguments); errorValue != nil {
		return "", "", errorValue
	}
	return *scenarioName, *artifactDirectoryPath, nil
}

func runVirtualSession(ctx context.Context, scenarioName string, artifactDirectoryPath string) error {
	scenario, errorValue := e2e.BuiltinScenario(scenarioName, artifactDirectoryPath)
	if errorValue != nil {
		return errorValue
	}
	result, errorValue := e2e.RunVirtualSession(ctx, scenario)
	if errorValue != nil {
		return errorValue
	}
	fmt.Println("scenario:", result.ScenarioName)
	fmt.Println("artifactDirectoryPath:", result.ArtifactDirectoryPath)
	for index, turnResult := range result.TurnResults {
		fmt.Printf("turn %d taskRunID: %s\n", index+1, turnResult.TaskRunID)
		fmt.Printf("turn %d reply: %s\n", index+1, turnResult.FinalReply)
		for _, attachment := range turnResult.Attachments {
			fmt.Printf("turn %d attachment: %s\n", index+1, attachment.DevicePath)
		}
	}
	return nil
}

func printExecutableCommand(executableCommand lab.ExecutableCommand) {
	parts := []string{executableCommand.ExecutableName}
	parts = append(parts, executableCommand.Arguments...)
	if executableCommand.StandardInputPath != "" {
		parts = append(parts, "<", executableCommand.StandardInputPath)
	}

	fmt.Println(strings.Join(parts, " "))
}
