package app

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"blueclaw/internal/adminapi"
	"blueclaw/internal/agent"
	"blueclaw/internal/auth"
	"blueclaw/internal/config"
	"blueclaw/internal/connectors/mattermost"
	"blueclaw/internal/httpserver"
	"blueclaw/internal/identity"
	"blueclaw/internal/llm"
	"blueclaw/internal/policy"
	runtimelogging "blueclaw/internal/runtime"
	"blueclaw/internal/security"
	"blueclaw/internal/task"
	"blueclaw/internal/userapi"
)

type Application struct {
	httpServer                    *http.Server
	mattermostListener            *mattermost.WebSocketListener
	runtimeLogger                 *runtimelogging.PersistentLogger
	startupError                  error
	mattermostListenerCancel      context.CancelFunc
	logRetentionCancel            context.CancelFunc
	mattermostBaseURL             string
	languageModelDefaultProvider  string
	languageModelFallbackProvider string
}

func NewApplication(runtimeConfiguration config.RuntimeConfiguration, policyPath string) *Application {
	runtimeLogger, startupError := runtimelogging.NewPersistentLogger(runtimeConfiguration, time.Now())
	if startupError != nil {
		runtimeLogger = runtimelogging.NewDiscardLogger()
	}
	logger := runtimeLogger.Logger
	policyLoader := policy.PolicyLoader{}
	policyDocument, _ := policyLoader.LoadPolicyDocument(policyPath)
	policyProjectionService := policy.PolicyProjectionService{}
	identityService := identity.NewIdentityService(policyProjectionService.ReplacePolicyProjectionTransactionally(policyDocument))
	policyWatcher := &policy.PolicyWatcher{}
	policyWatcher.ReloadPolicyDocument(policyDocument)

	auditHandler := adminapi.NewAuditHandler()
	taskEventService := task.NewTaskEventService()
	taskStepService := task.NewTaskStepService()
	taskRunService := task.NewTaskRunService(taskEventService)
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
	mattermostBotToken := readSecretFile(runtimeConfiguration.Connectors.Mattermost.BotTokenPath)
	mattermostUserProfileClient := mattermost.UserProfileClient{
		BaseURL:  runtimeConfiguration.Connectors.Mattermost.BaseURL,
		BotToken: mattermostBotToken,
	}
	mattermostConnector := mattermost.NewConnectorWithIdentityResolver(mattermostUserProfileClient)
	mattermostPostClient := mattermost.PostClient{
		BaseURL:  runtimeConfiguration.Connectors.Mattermost.BaseURL,
		BotToken: mattermostBotToken,
	}
	mattermostEventHandler := httpserver.NewMattermostEventHandler(
		mattermostConnector,
		identityService,
		agentKernel,
		mattermostUserProfileClient,
		mattermostPostClient,
	)
	mattermostEventHandler.Logger = logger

	router := httpserver.NewRouter(httpserver.RouterDependencies{
		PolicyHandler: adminapi.PolicyHandler{
			PolicyPath:    policyPath,
			PolicyLoader:  policyLoader,
			PolicySaver:   policy.PolicySaver{},
			PolicyWatcher: policyWatcher,
			Validator:     policy.PolicyValidator{},
			AuditHandler:  auditHandler,
			OnPolicyReload: func(policyDocument policy.PolicyDocument) {
				identityService.ReloadPolicyProjection(policyProjectionService.ReplacePolicyProjectionTransactionally(policyDocument))
			},
		},
		AuditHandler: auditHandler,
		TaskMonitorHandler: adminapi.TaskMonitorHandler{
			TaskRunService:   taskRunService,
			TaskStepService:  taskStepService,
			TaskEventService: taskEventService,
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
		MattermostEventHandler: mattermostEventHandler,
	})

	mattermostWebSocketURL := strings.TrimSpace(runtimeConfiguration.Connectors.Mattermost.WebSocketURL)
	if mattermostWebSocketURL == "" {
		mattermostWebSocketURL = mattermost.DeriveWebSocketURL(runtimeConfiguration.Connectors.Mattermost.BaseURL)
	}

	return &Application{
		httpServer: &http.Server{
			Addr:    deriveListenAddress(runtimeConfiguration.BaseURL),
			Handler: router,
		},
		mattermostListener: mattermost.NewWebSocketListener(
			mattermostWebSocketURL,
			mattermostBotToken,
			logger,
			func(ctx context.Context, event mattermost.Event, source string) error {
				_, errorValue := mattermostEventHandler.HandleMattermostEventValue(ctx, event, source)
				return errorValue
			},
		),
		runtimeLogger:                 runtimeLogger,
		startupError:                  startupError,
		mattermostBaseURL:             runtimeConfiguration.Connectors.Mattermost.BaseURL,
		languageModelDefaultProvider:  languageModelRuntimeConfiguration.LanguageModel.DefaultProvider,
		languageModelFallbackProvider: languageModelRuntimeConfiguration.LanguageModel.FallbackProvider,
	}
}

func readSecretFile(path string) string {
	secretBytes, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return ""
	}
	return strings.TrimSpace(string(secretBytes))
}

func resolveLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration) llm.LanguageModelProvider {
	languageModelConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	if strings.TrimSpace(languageModelConfiguration.LanguageModel.DefaultProvider) == "" {
		return nil
	}

	languageModelProvider, errorValue := llm.NewConfiguredLanguageModelProvider(
		languageModelConfiguration,
		os.Getenv("OPENROUTER_API_KEY"),
	)
	if errorValue != nil {
		return nil
	}

	return languageModelProvider
}

func deriveLanguageModelRuntimeConfiguration(runtimeConfiguration config.RuntimeConfiguration) config.RuntimeConfiguration {
	if strings.TrimSpace(runtimeConfiguration.LanguageModel.DefaultProvider) != "" {
		return runtimeConfiguration
	}

	if hasOpenRouterConfiguration(runtimeConfiguration) {
		runtimeConfiguration.LanguageModel.DefaultProvider = "openRouter"
		if strings.TrimSpace(runtimeConfiguration.LanguageModel.FallbackProvider) == "" && hasLiteRTLMConfiguration(runtimeConfiguration) {
			runtimeConfiguration.LanguageModel.FallbackProvider = "liteRTLM"
		}
		return runtimeConfiguration
	}

	if hasLiteRTLMConfiguration(runtimeConfiguration) {
		runtimeConfiguration.LanguageModel.DefaultProvider = "liteRTLM"
	}

	return runtimeConfiguration
}

func hasOpenRouterConfiguration(runtimeConfiguration config.RuntimeConfiguration) bool {
	return firstNonEmpty(
		runtimeConfiguration.LanguageModel.OpenRouter.ModelName,
		runtimeConfiguration.LanguageModel.OpenRouter.BaseURL,
		os.Getenv("OPENROUTER_API_KEY"),
	) != ""
}

func hasLiteRTLMConfiguration(runtimeConfiguration config.RuntimeConfiguration) bool {
	return firstNonEmpty(
		runtimeConfiguration.LanguageModel.LiteRTLM.WrapperPath,
		runtimeConfiguration.LanguageModel.LiteRTLM.ModelPath,
	) != ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}

	return ""
}

func (application *Application) Start() error {
	if application.startupError != nil {
		return application.startupError
	}
	application.startLogRetentionLoop()
	application.startMattermostListener()
	application.runtimeLogger.Logger.Info(
		"application.started",
		"listenAddress",
		application.httpServer.Addr,
		"mattermostWebSocketEnabled",
		application.mattermostListener != nil && strings.TrimSpace(application.mattermostListener.URL) != "",
		"mattermostBaseURL",
		application.mattermostBaseURL,
		"languageModelDefaultProvider",
		application.languageModelDefaultProvider,
		"languageModelFallbackProvider",
		application.languageModelFallbackProvider,
		"logDirectoryPath",
		application.runtimeLogger.DirectoryPath(),
	)
	return application.httpServer.ListenAndServe()
}

func (application *Application) Shutdown(ctx context.Context) error {
	if application.mattermostListenerCancel != nil {
		application.mattermostListenerCancel()
	}
	if application.logRetentionCancel != nil {
		application.logRetentionCancel()
	}
	errorValue := application.httpServer.Shutdown(ctx)
	closeErrorValue := application.runtimeLogger.Close()
	if errorValue != nil {
		return errorValue
	}
	return closeErrorValue
}

func (application *Application) startMattermostListener() {
	if application.mattermostListener == nil || application.mattermostListenerCancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	application.mattermostListenerCancel = cancel
	go application.mattermostListener.Start(ctx)
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
