package adminapi

import (
	"encoding/json"
	"net/http"

	"blueclaw/internal/backup"
)

type BackupHandler struct {
	Coordinator *backup.Coordinator
}

type prepareBackupRequest struct {
	Holder string `json:"holder"`
}

func (backupHandler BackupHandler) HandleManifest(responseWriter http.ResponseWriter, request *http.Request) {
	if backupHandler.Coordinator == nil {
		http.Error(responseWriter, "backup coordinator not configured", http.StatusServiceUnavailable)
		return
	}
	writeJSON(responseWriter, http.StatusOK, backupHandler.Coordinator.Manifest())
}

func (backupHandler BackupHandler) HandlePrepare(responseWriter http.ResponseWriter, request *http.Request) {
	if backupHandler.Coordinator == nil {
		http.Error(responseWriter, "backup coordinator not configured", http.StatusServiceUnavailable)
		return
	}
	var payload prepareBackupRequest
	_ = json.NewDecoder(request.Body).Decode(&payload)
	if payload.Holder == "" {
		payload.Holder = "admin"
	}
	writeJSON(responseWriter, http.StatusOK, backupHandler.Coordinator.Prepare(payload.Holder))
}

func (backupHandler BackupHandler) HandleComplete(responseWriter http.ResponseWriter, request *http.Request) {
	if backupHandler.Coordinator == nil {
		http.Error(responseWriter, "backup coordinator not configured", http.StatusServiceUnavailable)
		return
	}
	writeJSON(responseWriter, http.StatusOK, backupHandler.Coordinator.Complete())
}
