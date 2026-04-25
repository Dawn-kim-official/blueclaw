package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"blueclaw/internal/adminapi"
	"blueclaw/internal/agent"
	"blueclaw/internal/auth"
	"blueclaw/internal/config"
	"blueclaw/internal/connectors"
	"blueclaw/internal/connectors/mattermost"
	"blueclaw/internal/connectors/slack"
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
	connectorTransports           []connectors.ConnectorTransport
	runtimeLogger                 *runtimelogging.PersistentLogger
	startupError                  error
	connectorTransportCancel      context.CancelFunc
	logRetentionCancel            context.CancelFunc
	languageModelDefaultProvider  string
	languageModelFallbackProvider string
}

func NewApplication(runtimeConfiguration config.RuntimeConfiguration, policyPath string) *Application {
	runtimeLogger, startupError := runtimelogging.NewPersistentLogger(runtimeConfiguration, time.Now())
	if startupError != nil {
		runtimeLogger = runtimelogging.NewDiscardLogger()
	}
	if runtimeConfiguration.Connectors.Signal.Enabled && startupError == nil {
		startupError = errors.New("signal connector is experimental-disabled in v1")
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
	mattermostPostClient := mattermost.PostClient{
		BaseURL:  runtimeConfiguration.Connectors.Mattermost.BaseURL,
		BotToken: mattermostBotToken,
	}
	connectorRuntime := connectors.NewConnectorRuntime(
		identityService,
		agentKernel,
		logger,
	)
	connectorRuntime.RegisterAdapter(mattermost.NewAdapter(mattermostUserProfileClient, mattermostPostClient))

	slackBotToken := readSecretFile(runtimeConfiguration.Connectors.Slack.BotTokenPath)
	slackSigningSecret := readSecretFile(runtimeConfiguration.Connectors.Slack.SigningSecretPath)
	slackUserProfileClient := slack.UserProfileClient{
		BaseURL:  runtimeConfiguration.Connectors.Slack.BaseURL,
		BotToken: slackBotToken,
	}
	slackPostClient := slack.PostClient{
		BaseURL:  runtimeConfiguration.Connectors.Slack.BaseURL,
		BotToken: slackBotToken,
	}
	connectorRuntime.RegisterAdapter(slack.NewAdapter(slackUserProfileClient, slackPostClient, slackSigningSecret))
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
		ConnectorEventHandler: connectorEventHandler,
	})

	mattermostWebSocketURL := strings.TrimSpace(runtimeConfiguration.Connectors.Mattermost.WebSocketURL)
	if mattermostWebSocketURL == "" {
		mattermostWebSocketURL = mattermost.DeriveWebSocketURL(runtimeConfiguration.Connectors.Mattermost.BaseURL)
	}

	mattermostWebSocketListener := mattermost.NewWebSocketListener(
		mattermostWebSocketURL,
		mattermostBotToken,
		logger,
		func(ctx context.Context, payload []byte, source string) error {
			_, errorValue := connectorRuntime.HandleRealtimeEvent(ctx, "mattermost", payload, source)
			return errorValue
		},
	)
	connectorTransports := []connectors.ConnectorTransport{
		connectors.NewHTTPWebhookTransport("mattermost-http-webhook", "mattermost"),
		connectors.NewHTTPWebhookTransport("slack-events-api", "slack"),
		mattermostWebSocketListener,
	}

	return &Application{
		httpServer: &http.Server{
			Addr:    deriveListenAddress(runtimeConfiguration.BaseURL),
			Handler: router,
		},
		connectorTransports:           connectorTransports,
		runtimeLogger:                 runtimeLogger,
		startupError:                  startupError,
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
	if errorValue != nil {
		return errorValue
	}
	return closeErrorValue
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
