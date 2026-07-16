package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
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
	apiconnector "blueclaw/internal/connectors/api"
	"blueclaw/internal/httpserver"
	"blueclaw/internal/identity"
	"blueclaw/internal/llm"
	"blueclaw/internal/mcp"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	runtimelogging "blueclaw/internal/runtime"
	"blueclaw/internal/runtimecontrol"
	"blueclaw/internal/scheduler"
	"blueclaw/internal/security"
	"blueclaw/internal/skill"
	"blueclaw/internal/store/postgres"
	"blueclaw/internal/task"
	"blueclaw/internal/userapi"
)

const databaseInitializationTimeout = 240 * time.Second

type Application struct {
	httpServer                    *http.Server
	connectorRuntime              *connectors.ConnectorRuntime
	connectorTransports           []connectors.ConnectorTransport
	taskRunService                *task.TaskRunService
	interruptedTaskResumer        interruptedTaskResumer
	runtimeLogger                 *runtimelogging.PersistentLogger
	database                      postgres.Database
	startupError                  error
	connectorRuntimeCancel        context.CancelFunc
	connectorTransportCancel      context.CancelFunc
	interruptedTaskResumeCancel   context.CancelFunc
	taskScheduleCancel            context.CancelFunc
	logRetentionCancel            context.CancelFunc
	memoryUpdateCancel            context.CancelFunc
	taskRetentionCancel           context.CancelFunc
	taskSchedulePoller            *scheduler.TaskSchedulePoller
	taskRetentionSweeper          *scheduler.TaskRetentionSweeper
	memoryUpdateQueue             *memory.BackgroundMemoryUpdateQueue
	taskSchedulePollSecond        int
	taskRetentionIntervalMinute   int
	interruptedTaskResumeDelay    time.Duration
	languageModelDefaultProvider  string
	languageModelFallbackProvider string
	languageModelConfigured       bool
}

type interruptedTaskResumer interface {
	CanResumeInterruptedTaskRun(task.TaskRun) bool
	ResumeInterruptedTaskRun(context.Context, task.TaskRun) (connectors.ConnectorRuntimeResult, error)
	FailUnresumedInterruptedTaskRun(context.Context, task.TaskRun, string) bool
}

func NewApplication(runtimeConfiguration config.RuntimeConfiguration, policyPath string) *Application {
	runtimeLogger, startupError := runtimelogging.NewPersistentLogger(runtimeConfiguration, time.Now())
	if startupError != nil {
		runtimeLogger = runtimelogging.NewDiscardLogger()
	}
	logger := runtimeLogger.Logger
	logger.Info("application.initializing", "stage", "open_database")
	database, databaseError := openRuntimeDatabase(runtimeConfiguration, logger)
	if databaseError != nil && startupError == nil {
		startupError = databaseError
	}
	logger.Info("application.initializing", "stage", "load_policy")
	policyLoader := policy.PolicyLoader{}
	policyDocument, _ := policyLoader.LoadPolicyDocument(policyPath)
	logger.Info("application.initializing", "stage", "posix_synchronize")
	posixSynchronizer := security.NewPOSIXSynchronizer(runtimeConfiguration.Terminal, policyPath)
	if errorValue := posixSynchronizer.Synchronize(context.Background()); errorValue != nil && startupError == nil {
		startupError = errorValue
	}
	logger.Info("application.initializing", "stage", "project_policy")
	if database.SQL != nil {
		_ = postgres.NewPersonRepository(database).UpsertPeople(policyDocument)
	}
	logger.Info("application.initializing", "stage", "identity")
	policyProjectionService := policy.PolicyProjectionService{}
	identityService := identity.NewIdentityService(policyProjectionService.ReplacePolicyProjectionTransactionally(policyDocument))
	var platformAccountLister adminapi.PlatformAccountLister
	if database.SQL != nil {
		platformAccountRepository := postgres.NewPlatformAccountRepository(database)
		identityService.UsePlatformAccountRepository(platformAccountRepository)
		platformAccountLister = platformAccountRepository
	}
	policyWatcher := &policy.PolicyWatcher{}
	policyWatcher.ReloadPolicyDocument(policyDocument)

	auditHandler := adminapi.NewAuditHandler()
	taskEventService := task.NewTaskEventService()
	taskStepService := task.NewTaskStepService()
	taskArtifactService := task.NewTaskArtifactService()
	taskRunService := task.NewTaskRunService(taskEventService)
	var taskScheduleRepository task.TaskScheduleRepository
	var taskScheduleSummaryRepository adminapi.TaskScheduleSummaryRepository
	var taskScheduleListRepository adminapi.TaskScheduleListRepository
	var taskScheduleCreatorRepairRepository adminapi.TaskScheduleCreatorRepairRepository
	var connectorEventDiagnosticRepository adminapi.ConnectorEventDiagnosticRepository
	var conversationResetRepository adminapi.ConversationResetRepository
	var taskWaitTokenRepository task.TaskWaitTokenRepository
	var scheduledDeliveryRepository scheduler.TaskScheduleDeliveryRepository
	var personRepository postgres.PersonRepository
	var personReferenceCanonicalizer adminapi.PersonReferenceCanonicalizer
	if database.SQL != nil {
		personRepository = postgres.NewPersonRepository(database)
		personReferenceCanonicalizer = personRepository
		taskEventService.UseRepository(postgres.NewTaskEventRepository(database))
		taskStepService.UseRepository(postgres.NewTaskStepRepository(database))
		taskArtifactService.UseRepository(postgres.NewTaskArtifactRepository(database))
		taskRunService.UseRepository(postgres.NewTaskRunRepository(database))
		taskRunService.InterruptOrphanedRuntimeTaskRuns(task.TaskInterruptReasonRuntimeRestart)
		postgresTaskScheduleRepository := postgres.NewTaskScheduleRepository(database)
		taskScheduleRepository = postgresTaskScheduleRepository
		taskScheduleSummaryRepository = postgresTaskScheduleRepository
		taskScheduleListRepository = postgresTaskScheduleRepository
		taskScheduleCreatorRepairRepository = postgresTaskScheduleRepository
		connectorEventDiagnosticRepository = postgres.NewRawEventRepository(database)
		conversationResetRepository = postgres.NewConversationResetRepository(database)
		taskWaitTokenRepository = postgres.NewTaskWaitTokenRepository(database)
		scheduledDeliveryRepository = postgres.NewRawEventRepository(database)
	}
	magicLinkService := auth.NewMagicLinkService()
	sessionService := auth.NewSessionService()
	taskAuthService := task.NewTaskAuthService(magicLinkService, sessionService, taskRunService)
	logger.Info("application.initializing", "stage", "agent_kernel")
	agentKernel := agent.NewAgentKernel(taskRunService, taskStepService)
	agentKernel.UseTaskArtifactService(taskArtifactService)
	agentKernel.UseTurnOptions(deriveAgentTurnOptions(runtimeConfiguration))
	agentKernel.UseIntakeOptions(deriveAgentIntakeOptions(runtimeConfiguration))
	instructionBundleLoader := func() agent.InstructionBundle {
		return loadAgentInstructionBundle(runtimeConfiguration)
	}
	agentKernel.UseInstructionBundleLoader(instructionBundleLoader)
	languageModelRuntimeConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	taskTierLanguageModels := resolveTaskTierLanguageModelProviders(runtimeConfiguration, logger)
	languageModelProvider := taskTierLanguageModels.High
	lowTierLanguageModelProvider := taskTierLanguageModels.Low
	if lowTierLanguageModelProvider != nil {
		agentKernel.UseLanguageModelProvider(lowTierLanguageModelProvider)
		agentKernel.UseTaskTierLanguageModels(taskTierLanguageModels.Max, taskTierLanguageModels.XHigh, taskTierLanguageModels.High, taskTierLanguageModels.Medium, taskTierLanguageModels.XLow, taskTierLanguageModels.Coding)
	}
	capabilityClient := newCapabilityClient(runtimeConfiguration)
	logger.Info("application.initializing", "stage", "skill_retriever")
	embeddingClient := llm.CapabilityEmbeddingClient{
		CapabilityClient: capabilityClient,
		ModelName:        llm.DefaultEmbeddingModelName,
		ExecutionMode:    firstNonEmptyString(runtimeConfiguration.LanguageModel.Capability.ExecutionMode, "auto"),
	}
	skillRetriever := agent.NewEmbeddingSkillRetriever(
		embeddingClient,
		skillIndexPath(runtimeConfiguration),
	)
	skillRetriever.EmbeddingModel = embeddingClient.ModelName
	agentKernel.UseSkillRetriever(skillRetriever)
	agentKernel.UseCompanyProvider(func() agent.CompanyContext {
		company := policyWatcher.CurrentPolicyDocument().Company
		return agent.CompanyContext{
			Name:           company.Name,
			BrandName:      company.BrandName,
			Slogan:         company.Slogan,
			Description:    company.Description,
			Representative: company.Representative,
			Website:        company.Website,
		}
	})
	go agentKernel.RefreshSkillIndex(context.Background(), instructionBundleLoader())
	intakeLanguageModelProvider := resolveIntakeLanguageModelProvider(runtimeConfiguration, logger)
	if intakeLanguageModelProvider != nil {
		agentKernel.UseIntakeLanguageModelProvider(intakeLanguageModelProvider)
	}
	logger.Info("application.initializing", "stage", "memory")
	terminalService := security.NewTerminalSessionService(runtimeConfiguration.Terminal)
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(memory.NewGraphitiClient(
		runtimeConfiguration.Memory.GraphitiEndpoint,
		time.Duration(runtimeConfiguration.Memory.TimeoutSecond)*time.Second,
	))
	var memoryGraphReporter memory.GraphMemoryReporter
	var memoryGraphMigrator memory.GraphMemoryMigrator
	if database.SQL != nil {
		graphitiMemoryRepository := postgres.NewGraphitiMemoryRepository(database)
		memoryService.UseMirror(graphitiMemoryRepository)
		memoryGraphReporter = graphitiMemoryRepository
		memoryGraphMigrator = graphitiMemoryRepository
	}
	pinnedMemoryStore := memory.NewMarkdownStore(pinnedMemoryRootPath(runtimeConfiguration), pinnedMemoryHardLimitCharacterCount(runtimeConfiguration))
	pinnedMemoryStore.UseCompressor(memory.NewLLMMarkdownMemoryCompressor(lowTierLanguageModelProvider), pinnedMemoryCompressionTargetCharacterCount(runtimeConfiguration))
	memoryUpdateProcessor := memory.NewMemoryUpdateProcessor(memoryService, pinnedMemoryStore)
	memoryUpdateQueue := memory.NewBackgroundMemoryUpdateQueue(memoryUpdateProcessor, logger)
	backupCoordinator := backup.NewCoordinator(buildBackupManifest(runtimeConfiguration, database))
	taskIntakeController := runtimecontrol.NewTaskIntakeController()
	mcpRegistry := mcp.NewMcpRegistry()
	mcpRegistry.LoadServerDefinition(runtimeConfiguration.MCPServers)
	logger.Info("application.initializing", "stage", "tool_catalog")
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseMCPRegistry(mcpRegistry)
	toolCatalogBuilder.UseCapabilityTools(capabilityClient, runtimeConfiguration.Capabilities.ToolNames)
	toolCatalogBuilder.UseCapabilityToolDescriptors(capabilityClient, capabilityToolDescriptors(runtimeConfiguration.Capabilities.ToolDescriptors))
	toolCatalogBuilder.UseAllowedToolNamesByProfile(deriveAllowedToolNamesByProfile(runtimeConfiguration), deriveAllowedToolNames(runtimeConfiguration))
	toolCatalogBuilder.UseSkillSearch(skillRetriever, instructionBundleLoader)
	toolCatalogBuilder.UseTerminalService(terminalService)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseTaskArtifactService(taskArtifactService)
	toolCatalogBuilder.UseTaskScheduleRepository(taskScheduleRepository)
	toolCatalogBuilder.UseTaskWaitTokenRepository(taskWaitTokenRepository)
	toolCatalogBuilder.UseWorkspaceRootPath(runtimeConfiguration.Terminal.WorkspaceRootPath)
	toolCatalogBuilder.UseSkillChangeHandler(func(ctx context.Context) {
		agentKernel.RefreshSkillIndex(ctx, instructionBundleLoader())
	})
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UsePinnedMemoryStore(pinnedMemoryStore)
	toolCatalogBuilder.UseMemoryUpdateQueue(memoryUpdateQueue)
	taskLauncher := agentruntime.NewTaskLauncher(agentKernel, toolCatalogBuilder)
	taskLauncher.UseRequesterWorkspaceProvisioner(security.NewPOSIXRequesterWorkspaceProvisioner(posixSynchronizer))
	taskLauncher.UseRequesterEmailResolver(identityService)
	var taskSchedulePoller *scheduler.TaskSchedulePoller
	if taskScheduleRepository != nil && scheduledDeliveryRepository != nil {
		poller := scheduler.TaskSchedulePoller{
			TaskScheduleRepository: taskScheduleRepository,
			DeliveryRepository:     scheduledDeliveryRepository,
			TaskScheduleRunner:     agentruntime.NewTaskScheduleRunner(taskLauncher),
			TaskRunService:         taskRunService,
			PersonAccessResolver:   identityService,
			TaskIntakeGate:         taskIntakeController,
			WorkspaceID:            runtimeConfiguration.Memory.WorkspaceID,
			WorkerID:               "blueclaw-app",
			Logger:                 logger,
		}
		taskSchedulePoller = &poller
	}
	logger.Info("application.initializing", "stage", "connector_runtime")
	taskRetentionSweeper := &scheduler.TaskRetentionSweeper{
		TaskRunService:      taskRunService,
		TaskEventService:    taskEventService,
		TaskStepService:     taskStepService,
		TaskArtifactService: taskArtifactService,
		Logger:              logger,
		RetentionDays:       runtimeConfiguration.Scheduler.TaskRetentionDays,
	}
	connectorRuntime := connectors.NewConnectorRuntime(
		identityService,
		agentKernel,
		logger,
	)
	connectorRuntime.UseTaskLauncher(taskLauncher)
	connectorRuntime.UseAllowedToolNamesByProfile(deriveAllowedToolNamesByProfile(runtimeConfiguration), deriveAllowedToolNames(runtimeConfiguration))
	connectorRuntime.UseMemoryService(memoryService)
	connectorRuntime.UseWorkspaceID(runtimeConfiguration.Memory.WorkspaceID)
	connectorRuntime.UseAdminTaskLinkBaseURL(runtimeConfiguration.Agent.AdminTaskLinkBaseURL)
	connectorRuntime.UseIngressGate(backupCoordinator)
	connectorRuntime.UseTaskIntakeGate(taskIntakeController)
	if database.SQL != nil {
		connectorRuntime.UseEventRepository(postgres.NewRawEventRepository(database))
	}
	connectorRuntime.RegisterAdapter(connectors.NewCapabilityPlatformAdapter("mattermost", capabilityClient))
	connectorRuntime.RegisterAdapter(connectors.NewCapabilityPlatformAdapter("slack", capabilityClient))
	connectorRuntime.RegisterAdapter(connectors.NewCapabilityPlatformAdapter("signal", capabilityClient))
	agentReplyStore := apiconnector.NewReplyStore()
	connectorRuntime.RegisterAdapter(apiconnector.NewAdapter(identityService, agentReplyStore))
	connectorEventHandler := httpserver.NewConnectorEventHandler(connectorRuntime)

	logger.Info("application.initializing", "stage", "router")
	router := httpserver.NewRouter(httpserver.RouterDependencies{
		HealthHandler: httpserver.HealthHandler{
			Database:         database,
			ConnectorRuntime: connectorRuntime,
			MemoryService:    memoryService,
			MaximumBacklog:   1000,
		},
		WorkspaceFilesHandler: httpserver.WorkspaceFilesHandler{
			WorkspaceRootPath: runtimeConfiguration.Terminal.WorkspaceRootPath,
		},
		PolicyHandler: adminapi.PolicyHandler{
			PolicyPath:                   policyPath,
			PolicyLoader:                 policyLoader,
			PolicySaver:                  policy.PolicySaver{},
			PolicyWatcher:                policyWatcher,
			Validator:                    policy.PolicyValidator{},
			AuditHandler:                 auditHandler,
			PersonReferenceCanonicalizer: personReferenceCanonicalizer,
			PlatformAccountLinker:        identityService,
			OnPolicyReload: func(policyDocument policy.PolicyDocument) {
				if database.SQL != nil {
					_ = personRepository.UpsertPeople(policyDocument)
				}
				identityService.ReloadPolicyProjection(policyProjectionService.ReplacePolicyProjectionTransactionally(policyDocument))
				_ = posixSynchronizer.Synchronize(context.Background())
			},
		},
		IdentityResolve: adminapi.IdentityResolveHandler{
			PolicyWatcher:         policyWatcher,
			PlatformAccountLister: platformAccountLister,
		},
		AuditHandler: auditHandler,
		AttentionHandler: adminapi.AttentionHandler{
			LanguageModel: languageModelProvider,
		},
		TaskMonitorHandler: adminapi.TaskMonitorHandler{
			TaskRunService:   taskRunService,
			TaskStepService:  taskStepService,
			TaskEventService: taskEventService,
			IdentityService:  identityService,
		},
		TaskRunHandler: adminapi.TaskRunHandler{
			TaskLauncher:            taskLauncher,
			IdentityService:         identityService,
			WorkspaceID:             runtimeConfiguration.Memory.WorkspaceID,
			TaskRunService:          taskRunService,
			TaskIntakeGate:          taskIntakeController,
			AllowTaskDecisionPreset: runtimeConfiguration.Agent.AllowAdminTaskDiagnostic,
		},
		QuiesceHandler: adminapi.QuiesceHandler{
			Controller:     taskIntakeController,
			TaskRunService: taskRunService,
		},
		TaskScheduleHandler: adminapi.TaskScheduleHandler{
			SummaryRepository: taskScheduleSummaryRepository,
			ListRepository:    taskScheduleListRepository,
			RepairRepository:  taskScheduleCreatorRepairRepository,
		},
		ConnectorDiagnostics: adminapi.ConnectorEventDiagnosticHandler{
			Repository: connectorEventDiagnosticRepository,
		},
		ConversationReset: adminapi.ConversationResetHandler{
			Repository: conversationResetRepository,
		},
		MemoryGraphHandler: adminapi.MemoryGraphHandler{
			MemoryService: memoryService,
			Reporter:      memoryGraphReporter,
			Migrator:      memoryGraphMigrator,
			MarkdownStore: pinnedMemoryStore,
			Identity:      identityService,
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
		AgentReplyHandler: httpserver.AgentReplyHandler{
			ReplyStore: agentReplyStore,
		},
	})

	connectorTransports := []connectors.ConnectorTransport{
		connectors.NewHTTPWebhookTransport("mattermost-internal-ingress", "mattermost"),
		connectors.NewHTTPWebhookTransport("slack-internal-ingress", "slack"),
		connectors.NewHTTPWebhookTransport("signal-internal-ingress", "signal"),
	}

	logger.Info("application.initializing", "stage", "ready")
	return &Application{
		httpServer: &http.Server{
			Addr:    deriveListenAddress(runtimeConfiguration.BaseURL),
			Handler: router,
		},
		connectorRuntime:              connectorRuntime,
		connectorTransports:           connectorTransports,
		taskRunService:                taskRunService,
		interruptedTaskResumer:        connectorRuntime,
		runtimeLogger:                 runtimeLogger,
		database:                      database,
		startupError:                  startupError,
		taskSchedulePoller:            taskSchedulePoller,
		taskRetentionSweeper:          taskRetentionSweeper,
		memoryUpdateQueue:             memoryUpdateQueue,
		taskSchedulePollSecond:        runtimeConfiguration.Scheduler.TaskSchedulePollIntervalSecond,
		taskRetentionIntervalMinute:   runtimeConfiguration.Scheduler.RetentionCheckIntervalMinute,
		interruptedTaskResumeDelay:    2 * time.Second,
		languageModelDefaultProvider:  languageModelRuntimeConfiguration.LanguageModel.DefaultProvider,
		languageModelFallbackProvider: languageModelRuntimeConfiguration.LanguageModel.FallbackProvider,
		languageModelConfigured:       languageModelProvider != nil,
	}
}

func deriveAgentTurnOptions(runtimeConfiguration config.RuntimeConfiguration) agent.TurnOptions {
	taskLevelProfile := agent.TaskLevelProfileForLevel(agent.NormalizeTaskLevel(runtimeConfiguration.Agent.DefaultTaskLevel))
	return agent.TurnOptions{
		MaxIterationCount:   taskLevelProfile.MaxIterationCount,
		MaxToolCallCount:    taskLevelProfile.MaxToolCallCount,
		MaxElapsedSecond:    int(taskLevelProfile.Duration.Seconds()),
		ContextWindowTokens: runtimeConfiguration.LanguageModel.Capability.ContextWindowTokens,
		TaskLevel:           taskLevelProfile.TaskLevel,
		ToolResultMaxBytes:  runtimeConfiguration.Agent.ToolResultMaxBytes,
		GenerationOptions: llm.GenerationOptions{
			Seed:        runtimeConfiguration.Agent.GenerationOptions.Seed,
			Temperature: runtimeConfiguration.Agent.GenerationOptions.Temperature,
		},
		RecoveryBudget: agent.RecoveryBudget{
			CorrectedRetry: runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.CorrectedRetry,
			AlternateRoute: runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.AlternateRoute,
			AdjacentTool:   runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.AdjacentTool,
			NoToolFallback: runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.NoToolFallback,
		},
	}
}

func deriveAgentIntakeOptions(runtimeConfiguration config.RuntimeConfiguration) agent.IntakeOptions {
	return agent.IntakeOptions{
		IsEnabled:           runtimeConfiguration.Agent.Intake.Enabled,
		DefaultTaskLevel:    agent.NormalizeTaskLevel(runtimeConfiguration.Agent.DefaultTaskLevel),
		SkillTaskLevelFloor: agent.NormalizeTaskLevel(runtimeConfiguration.Agent.SkillTaskLevelFloor),
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

func pinnedMemoryRootPath(runtimeConfiguration config.RuntimeConfiguration) string {
	if strings.TrimSpace(runtimeConfiguration.Memory.PinnedMemoryRootPath) != "" {
		return strings.TrimSpace(runtimeConfiguration.Memory.PinnedMemoryRootPath)
	}
	return filepath.Join(runtimeConfiguration.Terminal.WorkspaceRootPath, ".blueclaw", "memory")
}

func pinnedMemoryHardLimitCharacterCount(runtimeConfiguration config.RuntimeConfiguration) int {
	if runtimeConfiguration.Memory.PinnedMemoryHardLimitCharacterCount > 0 {
		return runtimeConfiguration.Memory.PinnedMemoryHardLimitCharacterCount
	}
	if runtimeConfiguration.Memory.PinnedMemoryCharacterLimit > 0 {
		return runtimeConfiguration.Memory.PinnedMemoryCharacterLimit
	}
	return memory.DefaultPinnedMemoryHardLimitCharacterCount
}

func pinnedMemoryCompressionTargetCharacterCount(runtimeConfiguration config.RuntimeConfiguration) int {
	if runtimeConfiguration.Memory.PinnedMemoryCompressionTargetCharacterCount > 0 {
		return runtimeConfiguration.Memory.PinnedMemoryCompressionTargetCharacterCount
	}
	return memory.DefaultPinnedMemoryCompressionTargetCharacterCount
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
						Name:                   skillBundle.Name,
						Description:            skillBundle.Description,
						WhenToUse:              skillBundle.WhenToUse,
						Category:               skillBundle.Category,
						Tags:                   append([]string{}, skillBundle.Tags...),
						Prompt:                 strings.TrimSpace((skill.SkillPromptBuilder{}).BuildSkillPrompt([]skill.SkillBundle{skillBundle})),
						Activation:             agent.SkillActivation(skillBundle.Activation),
						Completion:             agent.SkillCompletion(skillBundle.Completion),
						Quality:                agent.SkillQuality(skillBundle.Quality),
						RecommendedMinutes:     skillBundle.RecommendedMinutes,
						AllowedTools:           append([]string{}, skillBundle.AllowedTools...),
						AllowedProfiles:        append([]string{}, skillBundle.AllowedProfiles...),
						HiddenFromCircles:      append([]string{}, skillBundle.HiddenFromCircles...),
						TriggerHints:           append([]string{}, skillBundle.TriggerHints...),
						DisableModelInvocation: skillBundle.DisableModelInvocation,
						Paths:                  append([]string{}, skillBundle.Paths...),
						References:             append([]string{}, skillBundle.References...),
						Scripts:                append([]string{}, skillBundle.Scripts...),
						Assets:                 append([]string{}, skillBundle.Assets...),
						Source:                 instructionSource(filepath.Join(skillBundle.DirectoryPath, "SKILL.md"), skillBundle.Name, document),
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
	allowedToolNameByName := map[string]bool{}
	for _, toolName := range agent.KernelToolNames() {
		allowedToolNameByName[toolName] = true
	}
	for _, agentProfile := range runtimeConfiguration.AgentProfiles {
		for _, allowedToolName := range agentProfile.AllowedToolNames {
			trimmedToolName := strings.TrimSpace(allowedToolName)
			if agent.IsKernelToolName(trimmedToolName) {
				allowedToolNameByName[trimmedToolName] = true
			}
		}
	}
	allowedToolNames := []string{}
	for allowedToolName := range allowedToolNameByName {
		allowedToolNames = append(allowedToolNames, allowedToolName)
	}
	return allowedToolNames
}

func capabilityToolDescriptors(toolDescriptors []config.CapabilityToolDescriptor) []agentruntime.CapabilityToolDescriptor {
	catalogToolDescriptors := []agentruntime.CapabilityToolDescriptor{}
	for _, toolDescriptor := range toolDescriptors {
		trimmedName := strings.TrimSpace(toolDescriptor.Name)
		if trimmedName == "" {
			continue
		}
		catalogToolDescriptors = append(catalogToolDescriptors, agentruntime.CapabilityToolDescriptor{
			Name:             trimmedName,
			Description:      capabilityToolDescription(toolDescriptor),
			InputSchema:      toolDescriptor.InputSchema,
			OutputSchema:     toolDescriptor.OutputSchema,
			PolicyResource:   toolDescriptor.PolicyResource,
			SideEffectClass:  toolDescriptor.SideEffectClass,
			RequiresApproval: toolDescriptor.RequiresApproval,
		})
	}
	return catalogToolDescriptors
}

func capabilityToolDescription(toolDescriptor config.CapabilityToolDescriptor) string {
	if strings.TrimSpace(toolDescriptor.Description) != "" {
		return strings.TrimSpace(toolDescriptor.Description)
	}
	if strings.TrimSpace(toolDescriptor.PrivacyClass) == "" && strings.TrimSpace(toolDescriptor.EstimatedLatency) == "" {
		return ""
	}
	descriptionParts := []string{"Workspace capability tool"}
	if strings.TrimSpace(toolDescriptor.PrivacyClass) != "" {
		descriptionParts = append(descriptionParts, "privacy="+strings.TrimSpace(toolDescriptor.PrivacyClass))
	}
	if strings.TrimSpace(toolDescriptor.EstimatedLatency) != "" {
		descriptionParts = append(descriptionParts, "latency="+strings.TrimSpace(toolDescriptor.EstimatedLatency))
	}
	return strings.Join(descriptionParts, ", ")
}

func deriveAllowedToolNamesByProfile(runtimeConfiguration config.RuntimeConfiguration) map[string][]string {
	allowedToolNamesByProfile := map[string][]string{}
	for _, agentProfile := range runtimeConfiguration.AgentProfiles {
		profileName := strings.TrimSpace(agentProfile.Name)
		if profileName == "" {
			profileName = "default"
		}
		allowedToolNamesByProfile[profileName] = appendDefaultBuiltInToolNames(agentProfile.AllowedToolNames)
	}
	return allowedToolNamesByProfile
}

func appendDefaultBuiltInToolNames(toolNames []string) []string {
	result := agent.KernelToolNames()
	for _, toolName := range toolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if agent.IsKernelToolName(trimmedToolName) && !containsString(result, trimmedToolName) {
			result = append(result, trimmedToolName)
		}
	}
	return result
}

func containsString(values []string, expectedValue string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expectedValue {
			return true
		}
	}
	return false
}

func openRuntimeDatabase(runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) (postgres.Database, error) {
	if strings.TrimSpace(runtimeConfiguration.Database.ConnectionString) == "" {
		return postgres.Database{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseInitializationTimeout)
	defer cancel()
	logger.Info("application.open_database.phase", "phase", "connect")
	database, errorValue := postgres.OpenDatabase(ctx, runtimeConfiguration.Database.ConnectionString)
	if errorValue != nil {
		return postgres.Database{}, errorValue
	}
	logger.Info("application.open_database.phase", "phase", "validate_migration_directory")
	migrationDirectoryPath := strings.TrimSpace(runtimeConfiguration.Database.MigrationDirectoryPath)
	if migrationDirectoryPath == "" {
		migrationDirectoryPath = "migrations"
	}
	migrationRunner := postgres.MigrationRunner{MigrationDirectoryPath: migrationDirectoryPath, Logger: logger}
	if errorValue := postgres.ValidateConnectorMigrationDirectory(migrationRunner); errorValue != nil {
		_ = database.Close()
		return postgres.Database{}, errorValue
	}
	logger.Info("application.open_database.phase", "phase", "apply_migrations")
	if errorValue := migrationRunner.ApplyMigrations(ctx, database); errorValue != nil {
		_ = database.Close()
		return postgres.Database{}, errorValue
	}
	logger.Info("application.open_database.phase", "phase", "validate_schema")
	if errorValue := postgres.ValidateConnectorDeliverySchema(ctx, database); errorValue != nil {
		_ = database.Close()
		return postgres.Database{}, errorValue
	}
	logger.Info("application.open_database.phase", "phase", "ready")
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

func skillIndexPath(runtimeConfiguration config.RuntimeConfiguration) string {
	workspaceRootPath := firstNonEmptyString(runtimeConfiguration.Terminal.WorkspaceRootPath, "/workspace")
	return filepath.Join(workspaceRootPath, ".blueclaw", "skill-index.json")
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

type taskTierLanguageModelProviders struct {
	Low    llm.LanguageModelProvider
	XLow   llm.LanguageModelProvider
	Medium llm.LanguageModelProvider
	High   llm.LanguageModelProvider
	XHigh  llm.LanguageModelProvider
	Max    llm.LanguageModelProvider
	Coding llm.LanguageModelProvider
}

func resolveTaskTierLanguageModelProviders(runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) taskTierLanguageModelProviders {
	languageModelConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	if strings.TrimSpace(languageModelConfiguration.LanguageModel.DefaultProvider) == "" {
		return taskTierLanguageModelProviders{}
	}
	tierNames := llm.ResolveModelTierNames(languageModelConfiguration)
	maximumModelTier := normalizeMaximumModelTier(languageModelConfiguration.LanguageModel.Capability.MaximumModelTier)
	if maximumModelTier != "" {
		return resolveCappedTaskTierLanguageModelProviders(languageModelConfiguration, tierNames, maximumModelTier, logger)
	}
	if logger != nil {
		logger.Info("resolved task model tiers",
			"max", tierNames.Max,
			"xhigh", tierNames.XHigh,
			"high", tierNames.High,
			"medium", tierNames.Medium,
			"low", tierNames.Low,
			"xlow", tierNames.XLow,
			"coding", tierNames.Coding)
	}
	hasConfigurationError := false
	configuredProvider := func(modelName string) llm.LanguageModelProvider {
		provider, errorValue := llm.NewConfiguredLanguageModelProviderForModel(languageModelConfiguration, modelName)
		if errorValue != nil {
			hasConfigurationError = true
			if logger != nil {
				logger.Error("language model provider configuration failed", "model", modelName, "error", errorValue.Error())
			}
		}
		return provider
	}
	lowModel := llm.WithModelTier(configuredProvider(tierNames.Low), "low")
	xLowModel := llm.WithModelTier(configuredProvider(tierNames.XLow), "xlow")
	mediumModel := llm.WithModelTier(configuredProvider(tierNames.Medium), "medium")
	highModel := llm.WithModelTier(configuredProvider(tierNames.High), "high")
	xHighModel := llm.WithModelTier(configuredProvider(tierNames.XHigh), "xhigh")
	maxModel := llm.WithModelTier(configuredProvider(tierNames.Max), "max")
	codingModel := llm.WithModelTier(configuredProvider(tierNames.Coding), "coding")
	if hasConfigurationError {
		return taskTierLanguageModelProviders{}
	}

	lowWithFallback := llm.LanguageModelProvider(lowModel)
	if tierNames.Medium != tierNames.Low {
		lowWithFallback = llm.FallbackLanguageModelProvider{
			PrimaryProvider:  lowModel,
			FallbackProvider: mediumModel,
			PrimaryLabel:     "low",
			FallbackLabel:    "medium",
			Logger:           logger,
		}
	}
	xLowWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  xLowModel,
		FallbackProvider: lowWithFallback,
		PrimaryLabel:     "xlow",
		FallbackLabel:    "low",
		Logger:           logger,
	}
	mediumWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  mediumModel,
		FallbackProvider: lowModel,
		PrimaryLabel:     "medium",
		FallbackLabel:    "low",
		Logger:           logger,
	}
	highWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  highModel,
		FallbackProvider: mediumWithFallback,
		PrimaryLabel:     "high",
		FallbackLabel:    "medium",
		Logger:           logger,
	}
	xHighWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  xHighModel,
		FallbackProvider: highWithFallback,
		PrimaryLabel:     "xhigh",
		FallbackLabel:    "high",
		Logger:           logger,
	}
	maxWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  maxModel,
		FallbackProvider: xHighWithFallback,
		PrimaryLabel:     "max",
		FallbackLabel:    "xhigh",
		Logger:           logger,
	}
	codingWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider: llm.VisionFallbackProvider{
			TextOnlyModel: codingModel,
			VisionModel:   highWithFallback,
		},
		FallbackProvider: mediumWithFallback,
		PrimaryLabel:     "coding",
		FallbackLabel:    "medium",
		Logger:           logger,
	}
	return taskTierLanguageModelProviders{
		Low:    lowWithFallback,
		XLow:   xLowWithFallback,
		Medium: mediumWithFallback,
		High:   highWithFallback,
		XHigh:  xHighWithFallback,
		Max:    maxWithFallback,
		Coding: codingWithFallback,
	}
}

func resolveIntakeLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) llm.LanguageModelProvider {
	if !runtimeConfiguration.Agent.Intake.Enabled {
		return nil
	}
	executionMode := strings.TrimSpace(runtimeConfiguration.Agent.Intake.ExecutionMode)
	if executionMode == "" {
		executionMode = "auto"
	}
	languageModelConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	languageModelConfiguration.LanguageModel.Capability.ExecutionMode = executionMode
	languageModelConfiguration.LanguageModel.SDKD.ExecutionMode = executionMode
	tierNames := llm.ResolveModelTierNames(languageModelConfiguration)
	maximumModelTier := normalizeMaximumModelTier(languageModelConfiguration.LanguageModel.Capability.MaximumModelTier)
	hasConfigurationError := false
	configuredProvider := func(modelName string) llm.LanguageModelProvider {
		provider, errorValue := llm.NewConfiguredLanguageModelProviderForModel(languageModelConfiguration, modelName)
		if errorValue == nil {
			return provider
		}
		hasConfigurationError = true
		if logger != nil {
			logger.Error("language model provider configuration failed", "model", modelName, "error", errorValue.Error())
		}
		return nil
	}
	if maximumModelTier != "" {
		providers := buildCappedModelTierProviders(tierNames, configuredProvider, logger)
		if hasConfigurationError {
			return nil
		}
		return providers.providerForTier(maximumModelTier)
	}
	primaryModelName := firstNonEmptyString(runtimeConfiguration.Agent.Intake.Model, tierNames.Medium)
	primaryProvider := configuredProvider(primaryModelName)
	if strings.TrimSpace(runtimeConfiguration.Agent.Intake.Model) == "" {
		primaryProvider = llm.WithModelTier(primaryProvider, "medium")
	}
	fallbackProvider := llm.WithModelTier(configuredProvider(tierNames.High), "high")
	if hasConfigurationError {
		return nil
	}
	return llm.FallbackLanguageModelProvider{
		PrimaryProvider:  primaryProvider,
		FallbackProvider: fallbackProvider,
		PrimaryLabel:     "intake",
		FallbackLabel:    "high",
		Logger:           logger,
	}
}

type cappedModelTierProviders struct {
	xLow   llm.LanguageModelProvider
	low    llm.LanguageModelProvider
	medium llm.LanguageModelProvider
	high   llm.LanguageModelProvider
	xHigh  llm.LanguageModelProvider
	max    llm.LanguageModelProvider
}

func resolveCappedTaskTierLanguageModelProviders(runtimeConfiguration config.RuntimeConfiguration, tierNames llm.ModelTierNames, maximumModelTier string, logger *slog.Logger) taskTierLanguageModelProviders {
	hasConfigurationError := false
	providerFactory := func(modelName string) llm.LanguageModelProvider {
		provider, errorValue := llm.NewConfiguredLanguageModelProviderForModel(runtimeConfiguration, modelName)
		if errorValue == nil {
			return provider
		}
		hasConfigurationError = true
		if logger != nil {
			logger.Error("language model provider configuration failed", "model", modelName, "error", errorValue.Error())
		}
		return nil
	}
	providers := buildCappedModelTierProviders(tierNames, providerFactory, logger)
	if hasConfigurationError {
		return taskTierLanguageModelProviders{}
	}
	if logger != nil {
		logger.Info("resolved capped task model tiers", "maximumModelTier", maximumModelTier, "xlow", tierNames.XLow, "lowVision", tierNames.Low)
	}
	return taskTierLanguageModelProviders{
		Low:    providers.providerAtOrBelow("low", maximumModelTier),
		XLow:   providers.providerAtOrBelow("xlow", maximumModelTier),
		Medium: providers.providerAtOrBelow("medium", maximumModelTier),
		High:   providers.providerAtOrBelow("high", maximumModelTier),
		XHigh:  providers.providerAtOrBelow("xhigh", maximumModelTier),
		Max:    providers.providerAtOrBelow("max", maximumModelTier),
		Coding: providers.providerForTier(maximumModelTier),
	}
}

func buildCappedModelTierProviders(tierNames llm.ModelTierNames, providerFactory func(string) llm.LanguageModelProvider, logger *slog.Logger) cappedModelTierProviders {
	xLowModel := llm.WithModelTier(providerFactory(tierNames.XLow), "xlow")
	lowModel := llm.WithModelTier(providerFactory(tierNames.Low), "low")
	lowProvider := descendingFallbackProvider(lowModel, xLowModel, "low", "xlow", logger)
	xLowProvider := llm.VisionFallbackProvider{TextOnlyModel: xLowModel, VisionModel: lowProvider}
	mediumProvider := descendingFallbackProvider(llm.WithModelTier(providerFactory(tierNames.Medium), "medium"), lowProvider, "medium", "low", logger)
	highProvider := descendingFallbackProvider(llm.WithModelTier(providerFactory(tierNames.High), "high"), mediumProvider, "high", "medium", logger)
	xHighProvider := descendingFallbackProvider(llm.WithModelTier(providerFactory(tierNames.XHigh), "xhigh"), highProvider, "xhigh", "high", logger)
	maxProvider := descendingFallbackProvider(llm.WithModelTier(providerFactory(tierNames.Max), "max"), xHighProvider, "max", "xhigh", logger)
	return cappedModelTierProviders{xLow: xLowProvider, low: lowProvider, medium: mediumProvider, high: highProvider, xHigh: xHighProvider, max: maxProvider}
}

func descendingFallbackProvider(primaryProvider llm.LanguageModelProvider, fallbackProvider llm.LanguageModelProvider, primaryLabel string, fallbackLabel string, logger *slog.Logger) llm.LanguageModelProvider {
	return llm.FallbackLanguageModelProvider{
		PrimaryProvider: primaryProvider, FallbackProvider: fallbackProvider,
		PrimaryLabel: primaryLabel, FallbackLabel: fallbackLabel, Logger: logger,
	}
}

func (providers cappedModelTierProviders) providerAtOrBelow(requestedTier string, maximumModelTier string) llm.LanguageModelProvider {
	if modelTierRank(requestedTier) > modelTierRank(maximumModelTier) {
		return providers.providerForTier(maximumModelTier)
	}
	return providers.providerForTier(requestedTier)
}

func (providers cappedModelTierProviders) providerForTier(modelTier string) llm.LanguageModelProvider {
	switch normalizeMaximumModelTier(modelTier) {
	case "max":
		return providers.max
	case "xhigh":
		return providers.xHigh
	case "high":
		return providers.high
	case "medium":
		return providers.medium
	case "low":
		return providers.low
	default:
		return providers.xLow
	}
}

func normalizeMaximumModelTier(modelTier string) string {
	normalizedModelTier := strings.ToLower(strings.TrimSpace(modelTier))
	for _, supportedModelTier := range []string{"xlow", "low", "medium", "high", "xhigh", "max"} {
		if normalizedModelTier == supportedModelTier {
			return supportedModelTier
		}
	}
	return ""
}

func modelTierRank(modelTier string) int {
	for rank, supportedModelTier := range []string{"xlow", "low", "medium", "high", "xhigh", "max"} {
		if normalizeMaximumModelTier(modelTier) == supportedModelTier {
			return rank
		}
	}
	return 0
}

func deriveLanguageModelRuntimeConfiguration(runtimeConfiguration config.RuntimeConfiguration) config.RuntimeConfiguration {
	if strings.TrimSpace(runtimeConfiguration.LanguageModel.DefaultProvider) == "" {
		runtimeConfiguration.LanguageModel.DefaultProvider = "capabilityLLM"
	}
	runtimeConfiguration.LanguageModel.FallbackProvider = ""
	return runtimeConfiguration
}

func (application *Application) Start() error {
	if application.startupError != nil {
		return application.startupError
	}
	application.runtimeLogger.Logger.Info("application.starting", "stage", "log_retention")
	application.startLogRetentionLoop()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "memory_queue")
	application.startMemoryUpdateQueue()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "connector_runtime")
	application.startConnectorRuntime()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "connector_transports")
	application.startConnectorTransports()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "task_schedule")
	application.startTaskSchedulePoller()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "task_retention")
	application.startTaskRetentionSweeper()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "listen")
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
	application.startInterruptedTaskAutoResume()
	return application.httpServer.Serve(listener)
}

func (application *Application) Shutdown(ctx context.Context) error {
	if application.connectorTransportCancel != nil {
		application.connectorTransportCancel()
	}
	if application.connectorRuntimeCancel != nil {
		application.connectorRuntimeCancel()
	}
	if application.taskScheduleCancel != nil {
		application.taskScheduleCancel()
	}
	if application.taskRetentionCancel != nil {
		application.taskRetentionCancel()
	}
	if application.interruptedTaskResumeCancel != nil {
		application.interruptedTaskResumeCancel()
	}
	if application.logRetentionCancel != nil {
		application.logRetentionCancel()
	}
	if application.memoryUpdateCancel != nil {
		application.memoryUpdateCancel()
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

func (application *Application) startConnectorRuntime() {
	if application.connectorRuntime == nil || application.connectorRuntimeCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	application.connectorRuntimeCancel = cancel
	application.connectorRuntime.Start(ctx)
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

func (application *Application) startTaskSchedulePoller() {
	if application.taskSchedulePoller == nil || application.taskScheduleCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	application.taskScheduleCancel = cancel
	interval := time.Duration(application.taskSchedulePollIntervalSecond()) * time.Second
	go application.taskSchedulePoller.Start(ctx, interval)
}

func (application *Application) startTaskRetentionSweeper() {
	if application.taskRetentionSweeper == nil || application.taskRetentionCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	application.taskRetentionCancel = cancel
	interval := time.Duration(application.taskRetentionIntervalMinuteOrDefault()) * time.Minute
	go application.taskRetentionSweeper.Start(ctx, interval)
}

func (application *Application) startInterruptedTaskAutoResume() {
	if application.taskRunService == nil || application.interruptedTaskResumer == nil || application.interruptedTaskResumeCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	application.interruptedTaskResumeCancel = cancel
	go application.resumeInterruptedTaskRuns(ctx, time.Now())
}

func (application *Application) resumeInterruptedTaskRuns(ctx context.Context, now time.Time) {
	selection := application.taskRunService.SelectInterruptedTaskRunsForAutoResume(now, 5)
	for _, taskRun := range selection.SkippedTaskRuns {
		application.taskRunService.MarkInterruptedTaskRunAutoResumeSkipped(taskRun.TaskRunID, "per_boot_limit_exceeded")
	}
	for index, taskRun := range selection.SelectedTaskRuns {
		if ctx.Err() != nil {
			return
		}
		if index > 0 && !application.waitBeforeInterruptedTaskResume(ctx) {
			return
		}
		if !application.interruptedTaskResumer.CanResumeInterruptedTaskRun(taskRun) {
			application.taskRunService.MarkInterruptedTaskRunAutoResumeSkipped(taskRun.TaskRunID, "resume_context_unavailable")
			continue
		}
		if !application.taskRunService.ClaimInterruptedTaskRunAutoResume(taskRun.TaskRunID, "runtime_restart") {
			continue
		}
		if _, errorValue := application.interruptedTaskResumer.ResumeInterruptedTaskRun(ctx, taskRun); errorValue != nil {
			application.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "task.auto_resume_launch_failed", errorValue.Error())
		}
	}
	application.failUnresumedInterruptedTaskRuns(ctx)
}

func (application *Application) failUnresumedInterruptedTaskRuns(ctx context.Context) {
	for _, taskRun := range application.taskRunService.ListTaskRun() {
		if ctx.Err() != nil {
			return
		}
		if !task.TaskRunWasInterruptedByRuntimeRestart(taskRun) {
			continue
		}
		application.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "task.auto_resume_abandoned", taskRun.FailureReason)
		application.interruptedTaskResumer.FailUnresumedInterruptedTaskRun(ctx, taskRun, "the task was interrupted by a runtime restart and could not be resumed")
	}
}

func (application *Application) waitBeforeInterruptedTaskResume(ctx context.Context) bool {
	delay := application.interruptedTaskResumeDelay
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (application *Application) taskRetentionIntervalMinuteOrDefault() int {
	if application.taskRetentionIntervalMinute > 0 {
		return application.taskRetentionIntervalMinute
	}
	return 60
}

func (application *Application) startMemoryUpdateQueue() {
	if application.memoryUpdateQueue == nil || application.memoryUpdateCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	application.memoryUpdateCancel = cancel
	application.memoryUpdateQueue.Start(ctx)
}

func (application *Application) taskSchedulePollIntervalSecond() int {
	if application.taskSchedulePollSecond > 0 {
		return application.taskSchedulePollSecond
	}
	return 30
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
