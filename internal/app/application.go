package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"blueclaw/internal/adminapi"
	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/auth"
	"blueclaw/internal/backup"
	"blueclaw/internal/capability"
	"blueclaw/internal/config"
	"blueclaw/internal/connectors"
	"blueclaw/internal/httpserver"
	"blueclaw/internal/identity"
	"blueclaw/internal/llm"
	"blueclaw/internal/mcp"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	runtimelogging "blueclaw/internal/runtime"
	"blueclaw/internal/security"
	"blueclaw/internal/skill"
	"blueclaw/internal/store/postgres"
	"blueclaw/internal/task"
	"blueclaw/internal/userapi"
)

type Application struct {
	httpServer                    *http.Server
	connectorTransports           []connectors.ConnectorTransport
	runtimeLogger                 *runtimelogging.PersistentLogger
	database                      postgres.Database
	startupError                  error
	connectorTransportCancel      context.CancelFunc
	logRetentionCancel            context.CancelFunc
	languageModelDefaultProvider  string
	languageModelFallbackProvider string
	languageModelConfigured       bool
}

func NewApplication(runtimeConfiguration config.RuntimeConfiguration, policyPath string) *Application {
	runtimeLogger, startupError := runtimelogging.NewPersistentLogger(runtimeConfiguration, time.Now())
	if startupError != nil {
		runtimeLogger = runtimelogging.NewDiscardLogger()
	}
	logger := runtimeLogger.Logger
	database, databaseError := openRuntimeDatabase(runtimeConfiguration)
	if databaseError != nil && startupError == nil {
		startupError = databaseError
	}
	policyLoader := policy.PolicyLoader{}
	policyDocument, _ := policyLoader.LoadPolicyDocument(policyPath)
	if database.SQL != nil {
		_ = postgres.NewPersonRepository(database).UpsertPeople(policyDocument)
	}
	policyProjectionService := policy.PolicyProjectionService{}
	identityService := identity.NewIdentityService(policyProjectionService.ReplacePolicyProjectionTransactionally(policyDocument))
	if database.SQL != nil {
		identityService.UsePlatformAccountRepository(postgres.NewPlatformAccountRepository(database))
	}
	policyWatcher := &policy.PolicyWatcher{}
	policyWatcher.ReloadPolicyDocument(policyDocument)

	auditHandler := adminapi.NewAuditHandler()
	taskEventService := task.NewTaskEventService()
	taskStepService := task.NewTaskStepService()
	taskArtifactService := task.NewTaskArtifactService()
	taskRunService := task.NewTaskRunService(taskEventService)
	if database.SQL != nil {
		taskEventService.UseRepository(postgres.NewTaskEventRepository(database))
		taskStepService.UseRepository(postgres.NewTaskStepRepository(database))
		taskArtifactService.UseRepository(postgres.NewTaskArtifactRepository(database))
		taskRunService.UseRepository(postgres.NewTaskRunRepository(database))
	}
	magicLinkService := auth.NewMagicLinkService()
	sessionService := auth.NewSessionService()
	taskAuthService := task.NewTaskAuthService(magicLinkService, sessionService, taskRunService)
	agentKernel := agent.NewAgentKernel(taskRunService, taskStepService)
	agentKernel.UseTaskArtifactService(taskArtifactService)
	agentKernel.UseTurnOptions(deriveAgentTurnOptions(runtimeConfiguration))
	agentKernel.UseIntakeOptions(deriveAgentIntakeOptions(runtimeConfiguration))
	agentKernel.UseInstructionBundleLoader(func() agent.InstructionBundle {
		return loadAgentInstructionBundle(runtimeConfiguration)
	})
	languageModelRuntimeConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	languageModelProvider := resolveLanguageModelProvider(runtimeConfiguration)
	if languageModelProvider != nil {
		agentKernel.UseLanguageModelProvider(languageModelProvider)
	}
	capabilityClient := newCapabilityClient(runtimeConfiguration)
	intakeLanguageModelProvider := resolveIntakeLanguageModelProvider(runtimeConfiguration, capabilityClient)
	if intakeLanguageModelProvider != nil {
		agentKernel.UseIntakeLanguageModelProvider(intakeLanguageModelProvider)
	}
	terminalService := security.NewTerminalSessionService(runtimeConfiguration.Terminal)
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(memory.NewGraphitiClient(
		runtimeConfiguration.Memory.GraphitiEndpoint,
		time.Duration(runtimeConfiguration.Memory.TimeoutSecond)*time.Second,
	))
	if database.SQL != nil {
		memoryService.UseMirror(postgres.NewGraphitiMemoryRepository(database))
	}
	memoryScopeRouter := memory.NewMemoryScopeRouter(languageModelProvider, runtimeConfiguration.Memory.WorkspaceID)
	backupCoordinator := backup.NewCoordinator(buildBackupManifest(runtimeConfiguration, database))
	mcpRegistry := mcp.NewMcpRegistry()
	mcpRegistry.LoadServerDefinition(runtimeConfiguration.MCPServers)
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseMCPRegistry(mcpRegistry)
	toolCatalogBuilder.UseCapabilityTools(capabilityClient, runtimeConfiguration.Capabilities.ToolNames)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(deriveAllowedToolNamesByProfile(runtimeConfiguration), deriveAllowedToolNames(runtimeConfiguration))
	toolCatalogBuilder.UseTerminalService(terminalService)
	toolCatalogBuilder.UseWorkspaceRootPath(runtimeConfiguration.Terminal.WorkspaceRootPath)
	toolCatalogBuilder.UseMemoryService(memoryService)
	taskLauncher := agentruntime.NewTaskLauncher(agentKernel, toolCatalogBuilder)
	connectorRuntime := connectors.NewConnectorRuntime(
		identityService,
		agentKernel,
		logger,
	)
	connectorRuntime.UseTaskLauncher(taskLauncher)
	connectorRuntime.UseAllowedToolNamesByProfile(deriveAllowedToolNamesByProfile(runtimeConfiguration), deriveAllowedToolNames(runtimeConfiguration))
	connectorRuntime.UseMemoryService(memoryService)
	connectorRuntime.UseMemoryScopeRouter(memoryScopeRouter)
	connectorRuntime.UseWorkspaceID(runtimeConfiguration.Memory.WorkspaceID)
	connectorRuntime.UseIngressGate(backupCoordinator)
	if database.SQL != nil {
		connectorRuntime.UseEventRepository(postgres.NewRawEventRepository(database))
	}
	connectorRuntime.RegisterAdapter(connectors.NewCapabilityPlatformAdapter("mattermost", capabilityClient))
	connectorRuntime.RegisterAdapter(connectors.NewCapabilityPlatformAdapter("slack", capabilityClient))
	connectorRuntime.RegisterAdapter(connectors.NewCapabilityPlatformAdapter("signal", capabilityClient))
	connectorEventHandler := httpserver.NewConnectorEventHandler(connectorRuntime)

	router := httpserver.NewRouter(httpserver.RouterDependencies{
		PolicyHandler: adminapi.PolicyHandler{
			PolicyPath:    policyPath,
			PolicyLoader:  policyLoader,
			PolicySaver:   policy.PolicySaver{},
			PolicyWatcher: policyWatcher,
			Validator:     policy.PolicyValidator{},
			AuditHandler:  auditHandler,
			OnPolicyReload: func(policyDocument policy.PolicyDocument) {
				if database.SQL != nil {
					_ = postgres.NewPersonRepository(database).UpsertPeople(policyDocument)
				}
				identityService.ReloadPolicyProjection(policyProjectionService.ReplacePolicyProjectionTransactionally(policyDocument))
			},
		},
		AuditHandler: auditHandler,
		TaskMonitorHandler: adminapi.TaskMonitorHandler{
			TaskRunService:   taskRunService,
			TaskStepService:  taskStepService,
			TaskEventService: taskEventService,
		},
		TaskRunHandler: adminapi.TaskRunHandler{
			TaskLauncher:    taskLauncher,
			IdentityService: identityService,
			WorkspaceID:     runtimeConfiguration.Memory.WorkspaceID,
		},
		BackupHandler: adminapi.BackupHandler{
			Coordinator: backupCoordinator,
		},
		TaskInboxHandler: userapi.TaskInboxHandler{
			TaskRunService:  taskRunService,
			TaskStepService: taskStepService,
			TaskAuthService: taskAuthService,
		},
		TaskActionHandler: userapi.TaskActionHandler{
			TaskRunService:  taskRunService,
			TaskAuthService: taskAuthService,
		},
		SSEHandler: httpserver.SSEHandler{
			TaskEventService: taskEventService,
		},
		ConnectorEventHandler: connectorEventHandler,
	})

	connectorTransports := []connectors.ConnectorTransport{
		connectors.NewHTTPWebhookTransport("mattermost-internal-ingress", "mattermost"),
		connectors.NewHTTPWebhookTransport("slack-internal-ingress", "slack"),
		connectors.NewHTTPWebhookTransport("signal-internal-ingress", "signal"),
	}

	return &Application{
		httpServer: &http.Server{
			Addr:    deriveListenAddress(runtimeConfiguration.BaseURL),
			Handler: router,
		},
		connectorTransports:           connectorTransports,
		runtimeLogger:                 runtimeLogger,
		database:                      database,
		startupError:                  startupError,
		languageModelDefaultProvider:  languageModelRuntimeConfiguration.LanguageModel.DefaultProvider,
		languageModelFallbackProvider: languageModelRuntimeConfiguration.LanguageModel.FallbackProvider,
		languageModelConfigured:       languageModelProvider != nil,
	}
}

func deriveAgentTurnOptions(runtimeConfiguration config.RuntimeConfiguration) agent.TurnOptions {
	budgetProfile := agent.BudgetProfileForClass(agent.BudgetClass(runtimeConfiguration.Agent.DefaultBudgetClass))
	return agent.TurnOptions{
		MaxIterations:      budgetProfile.MaxIterations,
		MaxToolCalls:       budgetProfile.MaxToolCalls,
		WallClockSecond:    int(budgetProfile.Duration.Seconds()),
		BudgetClass:        budgetProfile.BudgetClass,
		ToolResultMaxBytes: runtimeConfiguration.Agent.ToolResultMaxBytes,
	}
}

func deriveAgentIntakeOptions(runtimeConfiguration config.RuntimeConfiguration) agent.IntakeOptions {
	return agent.IntakeOptions{
		IsEnabled:          runtimeConfiguration.Agent.Intake.Enabled,
		DefaultBudgetClass: agent.BudgetClass(runtimeConfiguration.Agent.DefaultBudgetClass),
	}
}

func loadAgentInstructionPrompt(runtimeConfiguration config.RuntimeConfiguration) string {
	return loadAgentInstructionBundle(runtimeConfiguration).Prompt
}

func loadAgentInstructionBundle(runtimeConfiguration config.RuntimeConfiguration) agent.InstructionBundle {
	parts := []string{}
	sources := []agent.InstructionSource{}
	skillInstructions := []agent.SkillInstruction{}
	includedSkillByName := map[string]bool{}
	for _, rootPath := range instructionRootPaths(runtimeConfiguration) {
		for _, instructionDocument := range readInstructionDocuments(rootPath) {
			if instructionDocument.Prompt == "" {
				continue
			}
			parts = append(parts, instructionDocument.Prompt)
			sources = append(sources, instructionDocument.Source)
		}
		if instructionDocument, instructionSource := readLegacyInstructionDocument(rootPath); instructionDocument != "" {
			parts = append(parts, instructionDocument)
			sources = append(sources, instructionSource)
		}
		discoveredSkillInstructions := readSkillInstructions(rootPath)
		for _, skillInstruction := range discoveredSkillInstructions {
			if strings.TrimSpace(skillInstruction.Name) != "" {
				includedSkillByName[skillInstruction.Name] = true
			}
			skillInstructions = append(skillInstructions, skillInstruction)
		}
	}
	if !includedSkillByName["agent-browser"] {
		sources = append(sources, agent.InstructionSource{
			Path:      ".agents/skills/agent-browser/SKILL.md",
			SkillName: "agent-browser",
			Missing:   true,
		})
	}
	return agent.InstructionBundle{
		Prompt:  strings.Join(parts, "\n\n"),
		Sources: sources,
		Skills:  skillInstructions,
	}
}

func instructionRootPaths(runtimeConfiguration config.RuntimeConfiguration) []string {
	rootPathByPath := map[string]bool{}
	rootPaths := []string{}
	for _, rootPath := range []string{runtimeConfiguration.Terminal.WorkspaceRootPath, "/workspace", "."} {
		cleanRootPath := strings.TrimSpace(rootPath)
		if cleanRootPath == "" || rootPathByPath[cleanRootPath] {
			continue
		}
		rootPathByPath[cleanRootPath] = true
		rootPaths = append(rootPaths, cleanRootPath)
	}
	return rootPaths
}

type instructionDocument struct {
	Prompt string
	Source agent.InstructionSource
}

func readInstructionDocuments(rootPath string) []instructionDocument {
	documents := []instructionDocument{}
	for _, fileName := range []string{"IDENTITY.md", "BOT_PROFILE.yaml", "SOUL.md"} {
		path := filepath.Join(rootPath, fileName)
		document, errorValue := os.ReadFile(path)
		if errorValue == nil && strings.TrimSpace(string(document)) != "" {
			prompt := strings.TrimSpace(string(document))
			if fileName == "BOT_PROFILE.yaml" {
				prompt = renderBotProfileInstruction(document)
			}
			documents = append(documents, instructionDocument{
				Prompt: prompt,
				Source: instructionSource(path, "", document),
			})
		}
	}
	return documents
}

func readLegacyInstructionDocument(rootPath string) (string, agent.InstructionSource) {
	for _, fileName := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(rootPath, fileName)
		document, errorValue := os.ReadFile(path)
		if errorValue == nil && strings.TrimSpace(string(document)) != "" {
			return strings.TrimSpace(string(document)), instructionSource(path, "", document)
		}
	}
	return "", agent.InstructionSource{}
}

func readSkillInstructions(rootPath string) []agent.SkillInstruction {
	skillInstructions := []agent.SkillInstruction{}
	skillRegistry := skill.NewSkillRegistry()
	for _, relativePath := range []string{filepath.Join(".agents", "skills"), "skills"} {
		discoveredSkillBundles, errorValue := skillRegistry.DiscoverSkill(filepath.Join(rootPath, relativePath))
		if errorValue == nil {
			for _, skillBundle := range discoveredSkillBundles {
				document, readError := os.ReadFile(filepath.Join(skillBundle.DirectoryPath, "SKILL.md"))
				if readError == nil {
					skillInstructions = append(skillInstructions, agent.SkillInstruction{
						Name:            skillBundle.Name,
						Description:     skillBundle.Description,
						Category:        skillBundle.Category,
						Tags:            append([]string{}, skillBundle.Tags...),
						Prompt:          strings.TrimSpace((skill.SkillPromptBuilder{}).BuildSkillPrompt([]skill.SkillBundle{skillBundle})),
						Activation:      agent.SkillActivation(skillBundle.Activation),
						RequiredTools:   append([]string{}, skillBundle.RequiredTools...),
						AllowedProfiles: append([]string{}, skillBundle.AllowedProfiles...),
						TriggerHints:    append([]string{}, skillBundle.TriggerHints...),
						References:      append([]string{}, skillBundle.References...),
						Scripts:         append([]string{}, skillBundle.Scripts...),
						Assets:          append([]string{}, skillBundle.Assets...),
						Source:          instructionSource(filepath.Join(skillBundle.DirectoryPath, "SKILL.md"), skillBundle.Name, document),
					})
				}
			}
		}
	}
	return skillInstructions
}

func renderBotProfileInstruction(document []byte) string {
	profile := parseSimpleYAML(document)
	lines := []string{
		"Runtime bot profile:",
		"- internal username: " + firstNonEmptyString(profile["username"], "internkim"),
		"- current displayName: " + profile["displayName"],
		"- English displayName: " + profile["englishDisplayName"],
		"- aliases: " + profile["aliases"],
		"- public description: " + profile["publicDescription"],
	}
	if strings.TrimSpace(profile["identityExtension"]) != "" {
		lines = append(lines, "Identity extension:\n"+strings.TrimSpace(profile["identityExtension"]))
	}
	return strings.Join(lines, "\n")
}

func parseSimpleYAML(document []byte) map[string]string {
	values := map[string]string{}
	lines := strings.Split(string(document), "\n")
	for index := 0; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "" || strings.HasPrefix(line, "#") || line == "---" {
			continue
		}
		if line == "aliases:" {
			aliases := []string{}
			for index+1 < len(lines) {
				nextLine := strings.TrimSpace(lines[index+1])
				if !strings.HasPrefix(nextLine, "- ") {
					break
				}
				aliases = append(aliases, unquoteSimpleYAML(strings.TrimSpace(strings.TrimPrefix(nextLine, "- "))))
				index++
			}
			values["aliases"] = strings.Join(aliases, ", ")
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if found {
			values[strings.TrimSpace(key)] = unquoteSimpleYAML(strings.TrimSpace(value))
		}
	}
	return values
}

func unquoteSimpleYAML(value string) string {
	return strings.Trim(value, `"'`)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func instructionSource(path string, skillName string, document []byte) agent.InstructionSource {
	hash := sha256.Sum256(document)
	return agent.InstructionSource{
		Path:      path,
		SkillName: skillName,
		ByteSize:  len(document),
		SHA256:    hex.EncodeToString(hash[:]),
	}
}

func deriveAllowedToolNames(runtimeConfiguration config.RuntimeConfiguration) []string {
	allowedToolNameByName := map[string]bool{
		"conversation.history": true,
		"memory.search":        true,
	}
	for _, agentProfile := range runtimeConfiguration.AgentProfiles {
		for _, allowedToolName := range agentProfile.AllowedToolNames {
			trimmedToolName := strings.TrimSpace(allowedToolName)
			if trimmedToolName != "" {
				allowedToolNameByName[trimmedToolName] = true
			}
		}
	}
	for _, mcpServer := range runtimeConfiguration.MCPServers {
		for _, toolName := range mcpServer.ToolNames {
			trimmedToolName := strings.TrimSpace(toolName)
			if trimmedToolName != "" {
				allowedToolNameByName[trimmedToolName] = true
			}
		}
		for _, tool := range mcpServer.Tools {
			trimmedToolName := strings.TrimSpace(tool.Name)
			if trimmedToolName != "" {
				allowedToolNameByName[trimmedToolName] = true
			}
		}
	}
	for _, toolName := range runtimeConfiguration.Capabilities.ToolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName != "" {
			allowedToolNameByName[trimmedToolName] = true
		}
	}
	allowedToolNames := []string{}
	for allowedToolName := range allowedToolNameByName {
		allowedToolNames = append(allowedToolNames, allowedToolName)
	}
	return allowedToolNames
}

func deriveAllowedToolNamesByProfile(runtimeConfiguration config.RuntimeConfiguration) map[string][]string {
	allowedToolNamesByProfile := map[string][]string{}
	for _, agentProfile := range runtimeConfiguration.AgentProfiles {
		profileName := strings.TrimSpace(agentProfile.Name)
		if profileName == "" {
			profileName = "default"
		}
		allowedToolNamesByProfile[profileName] = append([]string{}, agentProfile.AllowedToolNames...)
	}
	return allowedToolNamesByProfile
}

func openRuntimeDatabase(runtimeConfiguration config.RuntimeConfiguration) (postgres.Database, error) {
	if strings.TrimSpace(runtimeConfiguration.Database.ConnectionString) == "" {
		return postgres.Database{}, nil
	}
	database, errorValue := postgres.OpenDatabase(runtimeConfiguration.Database.ConnectionString)
	if errorValue != nil {
		return postgres.Database{}, errorValue
	}
	migrationDirectoryPath := strings.TrimSpace(runtimeConfiguration.Database.MigrationDirectoryPath)
	if migrationDirectoryPath == "" {
		migrationDirectoryPath = "migrations"
	}
	if errorValue := (postgres.MigrationRunner{MigrationDirectoryPath: migrationDirectoryPath}).ApplyMigrations(context.Background(), database); errorValue != nil {
		_ = database.Close()
		return postgres.Database{}, errorValue
	}
	return database, nil
}

func buildBackupManifest(runtimeConfiguration config.RuntimeConfiguration, database postgres.Database) backup.Manifest {
	databaseKind := "none"
	requiredArtifacts := []string{"policy", "workspace"}
	if database.SQL != nil {
		databaseKind = "postgres"
		requiredArtifacts = append(requiredArtifacts, "blueclaw-postgres-dump")
	}
	return backup.Manifest{
		ContractVersion: 1,
		BlueclawVersion: "main",
		SchemaVersion:   "012_graphiti_memory_metadata",
		PersistentDataRoots: []string{
			"/workspace/.blueclaw",
			graphitiKuzuPath(runtimeConfiguration),
			runtimeConfiguration.Terminal.WorkspaceRootPath,
		},
		DatabaseKind:            databaseKind,
		RequiredBackupArtifacts: requiredArtifacts,
	}
}

func graphitiKuzuPath(runtimeConfiguration config.RuntimeConfiguration) string {
	if strings.TrimSpace(runtimeConfiguration.Memory.GraphitiKuzuPath) != "" {
		return strings.TrimSpace(runtimeConfiguration.Memory.GraphitiKuzuPath)
	}
	return "/workspace/.blueclaw/graphiti/kuzu"
}

func newCapabilityClient(runtimeConfiguration config.RuntimeConfiguration) capability.Client {
	return capability.NewClient(capability.Configuration{
		Endpoint:       runtimeConfiguration.Capabilities.Endpoint,
		Transport:      runtimeConfiguration.Capabilities.Transport,
		UnixSocketPath: runtimeConfiguration.Capabilities.UnixSocketPath,
		VSockCID:       runtimeConfiguration.Capabilities.VSockCID,
		VSockPort:      runtimeConfiguration.Capabilities.VSockPort,
		Timeout:        time.Duration(runtimeConfiguration.Capabilities.TimeoutSecond) * time.Second,
	})
}

func resolveLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration) llm.LanguageModelProvider {
	languageModelConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	if strings.TrimSpace(languageModelConfiguration.LanguageModel.DefaultProvider) == "" {
		return nil
	}

	languageModelProvider, errorValue := llm.NewConfiguredLanguageModelProvider(
		languageModelConfiguration,
	)
	if errorValue != nil {
		return nil
	}

	return languageModelProvider
}

func resolveIntakeLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration, capabilityClient capability.Client) llm.LanguageModelProvider {
	if !runtimeConfiguration.Agent.Intake.Enabled {
		return nil
	}
	modelName := strings.TrimSpace(runtimeConfiguration.Agent.Intake.Model)
	executionMode := strings.TrimSpace(runtimeConfiguration.Agent.Intake.ExecutionMode)
	if executionMode == "" {
		executionMode = "auto"
	}
	return llm.CapabilityLLMClient{
		CapabilityClient: capabilityClient,
		ModelName:        modelName,
		ExecutionMode:    executionMode,
	}
}

func deriveLanguageModelRuntimeConfiguration(runtimeConfiguration config.RuntimeConfiguration) config.RuntimeConfiguration {
	runtimeConfiguration.LanguageModel.DefaultProvider = "capabilityLLM"
	runtimeConfiguration.LanguageModel.FallbackProvider = ""
	return runtimeConfiguration
}

func (application *Application) Start() error {
	if application.startupError != nil {
		return application.startupError
	}
	application.startLogRetentionLoop()
	application.startConnectorTransports()
	listener, errorValue := net.Listen("tcp", application.httpServer.Addr)
	if errorValue != nil {
		return errorValue
	}
	application.runtimeLogger.Logger.Info(
		"application.started",
		"listenAddress",
		application.httpServer.Addr,
		"connectorTransports",
		strings.Join(application.connectorTransportNames(), ","),
		"languageModelDefaultProvider",
		application.languageModelDefaultProvider,
		"languageModelFallbackProvider",
		application.languageModelFallbackProvider,
		"languageModelConfigured",
		application.languageModelConfigured,
		"logDirectoryPath",
		application.runtimeLogger.DirectoryPath(),
	)
	return application.httpServer.Serve(listener)
}

func (application *Application) Shutdown(ctx context.Context) error {
	if application.connectorTransportCancel != nil {
		application.connectorTransportCancel()
	}
	if application.logRetentionCancel != nil {
		application.logRetentionCancel()
	}
	errorValue := application.httpServer.Shutdown(ctx)
	closeErrorValue := application.runtimeLogger.Close()
	databaseCloseError := application.database.Close()
	if errorValue != nil {
		return errorValue
	}
	if closeErrorValue != nil {
		return closeErrorValue
	}
	return databaseCloseError
}

func (application *Application) startConnectorTransports() {
	if len(application.connectorTransports) == 0 || application.connectorTransportCancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	application.connectorTransportCancel = cancel
	for _, connectorTransport := range application.connectorTransports {
		transport := connectorTransport
		application.runtimeLogger.Logger.Info(
			"connector."+transport.Platform()+".transport.registered",
			"name",
			transport.Name(),
			"platform",
			transport.Platform(),
		)
		go transport.Start(ctx)
	}
}

func (application *Application) connectorTransportNames() []string {
	transportNames := make([]string, 0, len(application.connectorTransports))
	for _, connectorTransport := range application.connectorTransports {
		transportNames = append(transportNames, connectorTransport.Platform()+":"+connectorTransport.Name())
	}
	return transportNames
}

func (application *Application) startLogRetentionLoop() {
	if application.runtimeLogger == nil || application.logRetentionCancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	application.logRetentionCancel = cancel
	go application.runtimeLogger.StartRetentionLoop(ctx)
}

func deriveListenAddress(baseURL string) string {
	if baseURL == "" {
		return "127.0.0.1:8080"
	}

	parsedURL, errorValue := url.Parse(baseURL)
	if errorValue != nil || parsedURL.Host == "" {
		return baseURL
	}

	return parsedURL.Host
}
