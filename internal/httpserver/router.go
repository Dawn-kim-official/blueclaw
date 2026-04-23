package httpserver

import (
	"net/http"
	"os"

	"blueclaw/internal/adminapi"
	"blueclaw/internal/userapi"
)

type RouterDependencies struct {
	PolicyHandler      adminapi.PolicyHandler
	AuditHandler       *adminapi.AuditHandler
	TaskMonitorHandler adminapi.TaskMonitorHandler
	TaskInboxHandler   userapi.TaskInboxHandler
	TaskActionHandler  userapi.TaskActionHandler
	SSEHandler         SSEHandler
}

func NewRouter(routerDependencies RouterDependencies) http.Handler {
	multiplexer := http.NewServeMux()

	multiplexer.HandleFunc("GET /admin/api/policy", routerDependencies.PolicyHandler.HandleGetPolicy)
	multiplexer.HandleFunc("POST /admin/api/policy/validate", routerDependencies.PolicyHandler.HandleValidatePolicy)
	multiplexer.HandleFunc("POST /admin/api/policy/save", routerDependencies.PolicyHandler.HandleSavePolicy)
	multiplexer.HandleFunc("GET /admin/api/audit", routerDependencies.AuditHandler.HandleListAudit)
	multiplexer.HandleFunc("GET /admin/api/task", routerDependencies.TaskMonitorHandler.HandleListTaskRun)
	multiplexer.HandleFunc("GET /admin/api/task/detail", routerDependencies.TaskMonitorHandler.HandleGetTaskRun)

	multiplexer.HandleFunc("GET /tasks/api/list", routerDependencies.TaskInboxHandler.HandleListOwnTaskRun)
	multiplexer.HandleFunc("GET /tasks/api/detail", routerDependencies.TaskInboxHandler.HandleGetOwnTaskRun)
	multiplexer.HandleFunc("POST /tasks/api/cancel", routerDependencies.TaskActionHandler.HandleCancelOwnTaskRun)
	multiplexer.HandleFunc("GET /tasks/api/events", routerDependencies.SSEHandler.HandleTaskEventStream)

	if _, errorValue := os.Stat("web/admin"); errorValue == nil {
		multiplexer.Handle("/admin/", http.StripPrefix("/admin/", AdminAssetHandler{RootDirectoryPath: "web/admin"}))
		multiplexer.Handle("/tasks/", TaskInboxHandler{RootDirectoryPath: "web/admin"})
		multiplexer.Handle("/login/", TaskInboxHandler{RootDirectoryPath: "web/admin"})
	}

	return withRecovery(multiplexer)
}
