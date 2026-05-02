package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"blueclaw/internal/capability"
	"blueclaw/internal/e2e"
	"blueclaw/internal/lab"
	"blueclaw/internal/llm"
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
		virtualSessionArguments, parseError := parseVirtualSessionArguments(flag.Args()[1:], *virtualScenarioName, *virtualArtifactDirectoryPath)
		if parseError != nil {
			errorValue = parseError
		} else {
			errorValue = runVirtualSession(ctx, virtualSessionArguments)
		}
	default:
		errorValue = fmt.Errorf("unsupported lab command: %s", commandName)
	}
	if errorValue != nil {
		log.Fatal(errorValue)
	}
}

type virtualSessionArguments struct {
	ScenarioName          string
	ArtifactDirectoryPath string
	LanguageModelEndpoint string
	LanguageModelSocket   string
	LanguageModelName     string
	ExecutionMode         string
	SkillDirectoryPath    string
}

func parseVirtualSessionArguments(arguments []string, defaultScenarioName string, defaultArtifactDirectoryPath string) (virtualSessionArguments, error) {
	flagSet := flag.NewFlagSet("virtual-session", flag.ContinueOnError)
	scenarioName := flagSet.String("scenario", defaultScenarioName, "virtual session scenario name")
	artifactDirectoryPath := flagSet.String("artifact-dir", defaultArtifactDirectoryPath, "virtual session artifact directory")
	languageModelEndpoint := flagSet.String("llm-endpoint", firstNonEmptyString(os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT"), capability.DefaultEndpoint), "live LLM capability endpoint")
	languageModelSocket := flagSet.String("llm-unix-socket", os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET"), "live LLM capability unix socket path")
	languageModelName := flagSet.String("llm-model", os.Getenv("BLUECLAW_E2E_LLM_MODEL"), "live LLM model name")
	executionMode := flagSet.String("llm-execution-mode", firstNonEmptyString(os.Getenv("BLUECLAW_E2E_LLM_EXECUTION_MODE"), "auto"), "live LLM execution mode")
	skillDirectoryPath := flagSet.String("skill-dir", "", "skill directory to load into the virtual workspace")
	if errorValue := flagSet.Parse(arguments); errorValue != nil {
		return virtualSessionArguments{}, errorValue
	}
	return virtualSessionArguments{
		ScenarioName:          *scenarioName,
		ArtifactDirectoryPath: *artifactDirectoryPath,
		LanguageModelEndpoint: *languageModelEndpoint,
		LanguageModelSocket:   *languageModelSocket,
		LanguageModelName:     *languageModelName,
		ExecutionMode:         *executionMode,
		SkillDirectoryPath:    *skillDirectoryPath,
	}, nil
}

func runVirtualSession(ctx context.Context, arguments virtualSessionArguments) error {
	scenario, errorValue := e2e.BuiltinScenario(arguments.ScenarioName, arguments.ArtifactDirectoryPath)
	if errorValue != nil {
		return errorValue
	}
	if isLiveVirtualScenario(scenario) {
		scenario.LanguageModel = llm.CapabilityLLMClient{
			CapabilityClient: capability.NewClient(capability.Configuration{
				Endpoint:       endpointForVirtualSession(arguments),
				UnixSocketPath: strings.TrimSpace(arguments.LanguageModelSocket),
				Timeout:        90 * time.Second,
			}),
			ModelName:     strings.TrimSpace(arguments.LanguageModelName),
			ExecutionMode: firstNonEmptyString(arguments.ExecutionMode, "auto"),
		}
		if skillDirectoryPath := firstNonEmptyString(arguments.SkillDirectoryPath, defaultSkillDirectoryPath(scenario.Name)); skillDirectoryPath != "" {
			scenario.Skills = nil
			scenario.SkillDirectoryPaths = []string{skillDirectoryPath}
		}
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

func endpointForVirtualSession(arguments virtualSessionArguments) string {
	if strings.TrimSpace(arguments.LanguageModelSocket) == "" {
		return strings.TrimSpace(arguments.LanguageModelEndpoint)
	}
	if strings.TrimSpace(arguments.LanguageModelEndpoint) == capability.DefaultEndpoint {
		return ""
	}
	return strings.TrimSpace(arguments.LanguageModelEndpoint)
}

func isLiveVirtualScenario(scenario e2e.VirtualSessionScenario) bool {
	for _, virtualTurn := range scenario.Turns {
		if len(virtualTurn.ModelResponses) > 0 {
			return false
		}
	}
	return true
}

func defaultSkillDirectoryPath(scenarioName string) string {
	if scenarioName != "slides_local_multiturn_success" {
		return ""
	}
	candidatePath := filepath.Clean("../../assets/blueclaw-workspace/skills/simple-slides")
	if _, errorValue := os.Stat(candidatePath); errorValue == nil {
		return candidatePath
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func printExecutableCommand(executableCommand lab.ExecutableCommand) {
	parts := []string{executableCommand.ExecutableName}
	parts = append(parts, executableCommand.Arguments...)
	if executableCommand.StandardInputPath != "" {
		parts = append(parts, "<", executableCommand.StandardInputPath)
	}

	fmt.Println(strings.Join(parts, " "))
}
