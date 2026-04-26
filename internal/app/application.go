package app

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"blueclaw/internal/adminapi"
	"blueclaw/internal/agent"
	"blueclaw/internal/auth"
	"blueclaw/internal/backup"
	"blueclaw/internal/capability"
	"blueclaw/internal/config"
	"blueclaw/internal/connectors"
	"blueclaw/internal/httpserver"
	"blueclaw/internal/identity"
	"blueclaw/internal/llm"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	runtimelogging "blueclaw/internal/runtime"
	"blueclaw/internal/security"
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
	taskRunService := task.NewTaskRunService(taskEventService)
	if database.SQL != nil {
		taskEventService.UseRepository(postgres.NewTaskEventRepository(database))
		taskStepService.UseRepository(postgres.NewTaskStepRepository(database))
		taskRunService.UseRepository(postgres.NewTaskRunRepository(database))
	}
	magicLinkService := auth.NewMagicLinkService()
	sessionService := auth.NewSessionService()
	taskAuthService := task.NewTaskAuthService(magicLinkService, sessionService, taskRunService)
	agentKernel := agent.NewAgentKernel(taskRunService, taskStepService)
	languageModelRuntimeConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	languageModelProvider := resolveLanguageModelProvider(runtimeConfiguration)
	if languageModelProvider != nil {
		agentKernel.UseLanguageModelProvider(languageModelProvider)
	}
	_ = security.NewTerminalSessionService(runtimeConfiguration.Terminal)
	memoryService := &memory.MemoryService{}
	if database.SQL != nil {
		memoryService.UseRepository(postgres.NewMemoryRecordRepository(database))
	}
	memoryExtractor := memory.NewMemoryExtractionService(languageModelProvider, memoryService)
	backupCoordinator := backup.NewCoordinator(buildBackupManifest(runtimeConfiguration, database))
	capabilityClient := newCapabilityClient(runtimeConfiguration)
	connectorRuntime := connectors.NewConnectorRuntime(
		identityService,
		agentKernel,
		logger,
	)
	connectorRuntime.UseMemoryService(memoryService)
	connectorRuntime.UseMemoryExtractor(memoryExtractor)
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
		SchemaVersion:   "011_scoped_memory_source_metadata",
		PersistentDataRoots: []string{
			"/workspace/.blueclaw",
			runtimeConfiguration.Terminal.WorkspaceRootPath,
		},
		DatabaseKind:            databaseKind,
		RequiredBackupArtifacts: requiredArtifacts,
	}
}

func newCapabilityClient(runtimeConfiguration config.RuntimeConfiguration) capability.Client {
	return capability.NewClient(capability.Configuration{
		Endpoint:       runtimeConfiguration.Capabilities.Endpoint,
		UnixSocketPath: runtimeConfiguration.Capabilities.UnixSocketPath,
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
	return application.httpServer.ListenAndServe()
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
