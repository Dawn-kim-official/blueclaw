package adminapi

import (
	"net/http"
	"sync"
	"time"
)

type AuditEntry struct {
	ActionName string    `json:"actionName"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
}

type AuditHandler struct {
	mutex        sync.RWMutex
	auditEntries []AuditEntry
}

func NewAuditHandler() *AuditHandler {
	return &AuditHandler{
		auditEntries: []AuditEntry{},
	}
}

func (auditHandler *AuditHandler) RecordPolicySave(backupPath string) {
	auditHandler.mutex.Lock()
	defer auditHandler.mutex.Unlock()
	auditHandler.auditEntries = append(auditHandler.auditEntries, AuditEntry{
		ActionName: "policy.saved",
		Body:       backupPath,
		CreatedAt:  time.Now(),
	})
}

func (auditHandler *AuditHandler) HandleListAudit(responseWriter http.ResponseWriter, request *http.Request) {
	auditHandler.mutex.RLock()
	defer auditHandler.mutex.RUnlock()
	writeJSON(responseWriter, http.StatusOK, auditHandler.auditEntries)
}
