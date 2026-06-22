package main

import (
	"context"
	"encoding/json"
	"errors"
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
	Seed                  *int64
	Temperature           *float64
	LiveLanguageModel     bool
	RecordCassettePath    string
	CassettePath          string
}

type languageModelCallEvent struct {
	Kind       string `json:"kind"`
	SchemaName string `json:"schemaName"`
	IsError    bool   `json:"isError"`
	Error      string `json:"error"`
}

func parseVirtualSessionArguments(arguments []string, defaultScenarioName string, defaultArtifactDirectoryPath string) (virtualSessionArguments, error) {
	flagSet := flag.NewFlagSet("virtual-session", flag.ContinueOnError)
	scenarioName := flagSet.String("scenario", defaultScenarioName, "virtual session scenario name")
	artifactDirectoryPath := flagSet.String("artifact-dir", defaultArtifactDirectoryPath, "virtual session artifact directory")
	languageModelEndpoint := flagSet.String("llm-endpoint", firstNonEmptyString(os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT"), capability.DefaultEndpoint), "live LLM capability endpoint")
	languageModelSocket := flagSet.String("llm-unix-socket", os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET"), "live LLM capability unix socket path")
	languageModelName := flagSet.String("llm-model", firstNonEmptyString(os.Getenv("INTERNKIM_E2E_MODEL"), os.Getenv("BLUECLAW_E2E_LLM_MODEL"), "google/gemma-4-31b-it:free"), "live LLM model name")
	executionMode := flagSet.String("llm-execution-mode", firstNonEmptyString(os.Getenv("BLUECLAW_E2E_LLM_EXECUTION_MODE"), "auto"), "live LLM execution mode")
	seed := flagSet.Int64("seed", 0, "generation seed for live LLM calls")
	temperature := flagSet.Float64("temperature", 0, "generation temperature for live LLM calls")
	skillDirectoryPath := flagSet.String("skill-dir", "", "skill directory to load into the virtual workspace")
	liveLanguageModel := flagSet.Bool("live-llm", truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")), "explicitly allow costed live LLM calls for unscripted scenarios")
	recordCassettePath := flagSet.String("record-cassette", "", "record live LLM responses to a cassette JSON file")
	cassettePath := flagSet.String("cassette", "", "replay LLM responses from a cassette JSON file")
	if errorValue := flagSet.Parse(arguments); errorValue != nil {
		return virtualSessionArguments{}, errorValue
	}
	if strings.TrimSpace(*cassettePath) != "" && strings.TrimSpace(*recordCassettePath) != "" {
		return virtualSessionArguments{}, errors.New("--cassette and --record-cassette cannot be used together")
	}
	if strings.TrimSpace(*cassettePath) != "" && hasVirtualSessionFlag(arguments, "live-llm") {
		return virtualSessionArguments{}, errors.New("--cassette cannot be combined with --live-llm")
	}
	if strings.TrimSpace(*recordCassettePath) != "" && !*liveLanguageModel {
		return virtualSessionArguments{}, errors.New("--record-cassette requires --live-llm")
	}
	return virtualSessionArguments{
		ScenarioName:          *scenarioName,
		ArtifactDirectoryPath: *artifactDirectoryPath,
		LanguageModelEndpoint: *languageModelEndpoint,
		LanguageModelSocket:   *languageModelSocket,
		LanguageModelName:     *languageModelName,
		ExecutionMode:         *executionMode,
		SkillDirectoryPath:    *skillDirectoryPath,
		Seed:                  virtualSessionInt64FlagPointer(arguments, "seed", *seed),
		Temperature:           virtualSessionFloat64FlagPointer(arguments, "temperature", *temperature),
		LiveLanguageModel:     *liveLanguageModel,
		RecordCassettePath:    *recordCassettePath,
		CassettePath:          *cassettePath,
	}, nil
}

func runVirtualSession(ctx context.Context, arguments virtualSessionArguments) error {
	scenario, errorValue := e2e.BuiltinScenario(arguments.ScenarioName, arguments.ArtifactDirectoryPath)
	if errorValue != nil {
		return errorValue
	}
	var cassetteRecorder *e2e.RecordingLanguageModel
	if strings.TrimSpace(arguments.CassettePath) != "" {
		cassette, errorValue := e2e.LoadLanguageModelCassette(arguments.CassettePath)
		if errorValue != nil {
			return errorValue
		}
		scenario.LanguageModel = e2e.NewReplayingLanguageModel(cassette)
		scenario.DisableScriptedModel = true
		scenario.UseLooseAssertions = true
	} else if arguments.LiveLanguageModel {
		openRouterAPIKey, errorValue := resolveOpenRouterAPIKey()
		if errorValue != nil {
			return errorValue
		}
		languageModel := llm.OpenRouterClient{
			APIKey:         openRouterAPIKey,
			BaseURL:        firstNonEmptyString(os.Getenv("OPENROUTER_BASE_URL"), llm.DefaultOpenRouterChatCompletionsURL),
			ModelName:      firstNonEmptyString(arguments.LanguageModelName, "google/gemma-4-31b-it:free"),
			AttemptCount:   3,
			InitialBackoff: 750 * time.Millisecond,
			GenerationOptions: llm.GenerationOptions{
				Seed:        arguments.Seed,
				Temperature: arguments.Temperature,
			},
		}
		scenario.LanguageModel = languageModel
		if strings.TrimSpace(arguments.RecordCassettePath) != "" {
			cassetteRecorder = e2e.NewRecordingLanguageModel(languageModel)
			scenario.LanguageModel = cassetteRecorder
		}
		scenario.DisableScriptedModel = true
		scenario.UseLooseAssertions = true
		scenario.ProgressWriter = os.Stderr
		delayLiveVirtualSession()
	} else if isLiveVirtualScenario(scenario) {
		return errors.New("virtual-session scenario needs live LLM calls; pass --live-llm or set BLUECLAW_E2E_LIVE=1")
	}
	if arguments.LiveLanguageModel || strings.TrimSpace(arguments.CassettePath) != "" {
		if skillDirectoryPath := firstNonEmptyString(arguments.SkillDirectoryPath, defaultSkillDirectoryPath(scenario.Name)); skillDirectoryPath != "" {
			scenario.Skills = nil
			scenario.SkillDirectoryPaths = []string{skillDirectoryPath}
		}
	}
	result, errorValue := e2e.RunVirtualSession(ctx, scenario)
	recordError := saveVirtualSessionCassette(arguments.RecordCassettePath, cassetteRecorder)
	if errorValue != nil {
		return errorValue
	}
	if recordError != nil {
		return recordError
	}
	fmt.Println("scenario:", result.ScenarioName)
	fmt.Println("artifactDirectoryPath:", result.ArtifactDirectoryPath)
	for index, turnResult := range result.TurnResults {
		fmt.Printf("turn %d taskRunID: %s\n", index+1, turnResult.TaskRunID)
		fmt.Printf("turn %d reply: %s\n", index+1, turnResult.FinishMessage)
		for _, summary := range languageModelCallFailureSummaries(turnResult) {
			fmt.Printf("turn %d llm.call error: %s\n", index+1, summary)
		}
		for _, assertion := range turnResult.InformationalAssertions {
			fmt.Printf("turn %d informational assertion %s: %t (%s)\n", index+1, assertion.Name, assertion.Satisfied, assertion.Detail)
		}
		for _, attachment := range turnResult.Attachments {
			fmt.Printf("turn %d attachment: %s\n", index+1, attachment.DevicePath)
		}
	}
	return nil
}

func languageModelCallFailureSummaries(turnResult e2e.VirtualTurnResult) []string {
	summaries := []string{}
	for _, event := range turnResult.LanguageModelCallEvents {
		if !event.IsError {
			continue
		}
		summaries = append(summaries, strings.TrimSpace(strings.Join([]string{event.Kind, event.SchemaName, event.Error}, " ")))
	}
	for _, event := range turnResult.Events {
		if event.Name != "llm.call" {
			continue
		}
		var callEvent languageModelCallEvent
		if errorValue := json.Unmarshal([]byte(event.Body), &callEvent); errorValue != nil {
			continue
		}
		if !callEvent.IsError {
			continue
		}
		summaries = append(summaries, strings.TrimSpace(strings.Join([]string{callEvent.Kind, callEvent.SchemaName, callEvent.Error}, " ")))
	}
	return summaries
}

func hasVirtualSessionFlag(arguments []string, name string) bool {
	longName := "--" + name
	shortName := "-" + name
	for _, argument := range arguments {
		if argument == longName || argument == shortName {
			return true
		}
		if strings.HasPrefix(argument, longName+"=") || strings.HasPrefix(argument, shortName+"=") {
			return true
		}
	}
	return false
}

func virtualSessionInt64FlagPointer(arguments []string, name string, value int64) *int64 {
	if !hasVirtualSessionFlag(arguments, name) {
		return nil
	}
	result := value
	return &result
}

func virtualSessionFloat64FlagPointer(arguments []string, name string, value float64) *float64 {
	if !hasVirtualSessionFlag(arguments, name) {
		return nil
	}
	result := value
	return &result
}

func resolveOpenRouterAPIKey() (string, error) {
	openRouterAPIKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if openRouterAPIKey != "" {
		return openRouterAPIKey, nil
	}
	homeDirectoryPath, errorValue := os.UserHomeDir()
	if errorValue != nil {
		return "", errorValue
	}
	document, errorValue := os.ReadFile(filepath.Join(homeDirectoryPath, ".internkim", "openrouter_api_key"))
	if errorValue != nil {
		return "", errors.New("OPENROUTER_API_KEY is required or ~/.internkim/openrouter_api_key must exist")
	}
	openRouterAPIKey = strings.TrimSpace(strings.TrimPrefix(string(document), "OPENROUTER_API_KEY="))
	if openRouterAPIKey == "" {
		return "", errors.New("OpenRouter API key file is empty")
	}
	return openRouterAPIKey, nil
}

func saveVirtualSessionCassette(path string, cassetteRecorder *e2e.RecordingLanguageModel) error {
	if strings.TrimSpace(path) == "" || cassetteRecorder == nil {
		return nil
	}
	return e2e.SaveLanguageModelCassette(path, cassetteRecorder.Cassette())
}

var delayLiveVirtualSession = func() {
	time.Sleep(1500 * time.Millisecond)
}

func truthyEnvironmentValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
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
		if len(virtualTurn.ActionResponses) > 0 {
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
