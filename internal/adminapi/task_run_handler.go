package adminapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"blueclaw/internal/agentruntime"
	"blueclaw/internal/identity"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
)

type TaskRunHandler struct {
	TaskLauncher    *agentruntime.TaskLauncher
	IdentityService *identity.IdentityService
	WorkspaceID     string
}

type taskRunRequest struct {
	RequesterPersonID    string `json:"requesterPersonID"`
	RequesterName        string `json:"requesterName"`
	RequesterCallingName string `json:"requesterCallingName"`
	RequesterHandle      string `json:"requesterHandle"`
	ConversationID       string `json:"conversationID"`
	ProfileName          string `json:"profileName"`
	Prompt               string `json:"prompt"`
}

func (taskRunHandler TaskRunHandler) HandleRunTask(responseWriter http.ResponseWriter, request *http.Request) {
	if taskRunHandler.TaskLauncher == nil || taskRunHandler.IdentityService == nil {
		http.Error(responseWriter, "task launcher is not configured", http.StatusServiceUnavailable)
		return
	}
	var runRequest taskRunRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&runRequest); errorValue != nil {
		http.Error(responseWriter, "invalid task run request", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(runRequest.RequesterPersonID) == "" {
		http.Error(responseWriter, "requesterPersonID is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(runRequest.Prompt) == "" {
		http.Error(responseWriter, "prompt is required", http.StatusBadRequest)
		return
	}
	personAccess := taskRunHandler.IdentityService.ResolvePersonAccess(runRequest.RequesterPersonID)
	conversationID := firstNonEmptyAdminString(runRequest.ConversationID, "admin:"+runRequest.RequesterPersonID)
	launchResult, errorValue := taskRunHandler.TaskLauncher.Launch(request.Context(), agentruntime.TaskLaunchRequest{
		Source:                    agentruntime.TaskLaunchSourceAdmin,
		SourceReference:           "admin:" + runRequest.RequesterPersonID,
		RequesterPersonID:         runRequest.RequesterPersonID,
		RequesterName:             runRequest.RequesterName,
		RequesterCallingName:      runRequest.RequesterCallingName,
		RequesterHandle:           runRequest.RequesterHandle,
		RequesterEmail:            taskRunHandler.IdentityService.ResolvePersonPrimaryEmail(runRequest.RequesterPersonID),
		ProfileName:               runRequest.ProfileName,
		ConversationID:            conversationID,
		Prompt:                    runRequest.Prompt,
		PersonAccess:              personAccess,
		MemoryNamespaces:          taskRunHandler.memoryNamespaces(runRequest.RequesterPersonID, conversationID, personAccess),
		AccessibleConversationIDs: []string{conversationID},
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"taskRun":     launchResult.TurnResult.TaskRun,
		"finalReply":  launchResult.TurnResult.FinalReply,
		"attachments": launchResult.TurnResult.Attachments,
	})
}

func (taskRunHandler TaskRunHandler) memoryNamespaces(personID string, conversationID string, personAccess policy.PersonAccess) []memory.MemoryNamespace {
	namespaces := []memory.MemoryNamespace{
		memory.UserNamespace(personID),
		memory.PrivatePersonNamespace(personID),
		memory.WorkspaceNamespace(taskRunHandler.WorkspaceID, personAccess.SecurityLevelRank, personAccess.GrantedClasses),
		memory.ConversationNamespace(conversationID, personAccess.SecurityLevelRank, personAccess.GrantedClasses),
	}
	for _, circleID := range personAccess.Circles {
		namespaces = append(namespaces, memory.CircleNamespace(taskRunHandler.WorkspaceID, circleID))
	}
	return namespaces
}

func firstNonEmptyAdminString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
