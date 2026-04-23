package app

import (
	"context"
	"net/http"
	"net/url"

	"blueclaw/internal/adminapi"
	"blueclaw/internal/agent"
	"blueclaw/internal/auth"
	"blueclaw/internal/config"
	"blueclaw/internal/httpserver"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
	"blueclaw/internal/task"
	"blueclaw/internal/userapi"
)

type Application struct {
	httpServer *http.Server
}

func NewApplication(runtimeConfiguration config.RuntimeConfiguration, policyPath string) *Application {
	policyLoader := policy.PolicyLoader{}
	policyDocument, _ := policyLoader.LoadPolicyDocument(policyPath)
	policyWatcher := &policy.PolicyWatcher{}
	policyWatcher.ReloadPolicyDocument(policyDocument)

	auditHandler := adminapi.NewAuditHandler()
	taskEventService := task.NewTaskEventService()
	taskStepService := task.NewTaskStepService()
	taskRunService := task.NewTaskRunService(taskEventService)
	magicLinkService := auth.NewMagicLinkService()
	sessionService := auth.NewSessionService()
	taskAuthService := task.NewTaskAuthService(magicLinkService, sessionService, taskRunService)
	_ = agent.NewAgentKernel(taskRunService, taskStepService)
	_ = security.NewTerminalSessionService(runtimeConfiguration.Terminal)

	router := httpserver.NewRouter(httpserver.RouterDependencies{
		PolicyHandler: adminapi.PolicyHandler{
			PolicyPath:    policyPath,
			PolicyLoader:  policyLoader,
			PolicySaver:   policy.PolicySaver{},
			PolicyWatcher: policyWatcher,
			Validator:     policy.PolicyValidator{},
			AuditHandler:  auditHandler,
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
	})

	return &Application{
		httpServer: &http.Server{
			Addr:    deriveListenAddress(runtimeConfiguration.BaseURL),
			Handler: router,
		},
	}
}

func (application *Application) Start() error {
	return application.httpServer.ListenAndServe()
}

func (application *Application) Shutdown(ctx context.Context) error {
	return application.httpServer.Shutdown(ctx)
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
