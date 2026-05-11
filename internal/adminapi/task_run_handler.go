package adminapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"blueclaw/internal/agentruntime"
	"blueclaw/internal/identity"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

type TaskRunHandler struct {
	TaskLauncher    *agentruntime.TaskLauncher
	IdentityService *identity.IdentityService
	WorkspaceID     string
	TaskRunService  *task.TaskRunService
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

type taskRunCancelRequest struct {
	TaskRunIDs        []string `json:"taskRunIDs"`
	RequesterPersonID string   `json:"requesterPersonID"`
	ScheduleOnly      bool     `json:"scheduleOnly"`
	StaleBefore       string   `json:"staleBefore"`
	Reason            string   `json:"reason"`
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

func (taskRunHandler TaskRunHandler) HandleCancelTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	if taskRunHandler.TaskRunService == nil {
		http.Error(responseWriter, "task run service is not configured", http.StatusServiceUnavailable)
		return
	}
	var cancelRequest taskRunCancelRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&cancelRequest); errorValue != nil {
		http.Error(responseWriter, "invalid task run cancel request", http.StatusBadRequest)
		return
	}
	staleBefore, errorValue := parseOptionalAdminTime(cancelRequest.StaleBefore)
	if errorValue != nil {
		http.Error(responseWriter, "staleBefore must be RFC3339", http.StatusBadRequest)
		return
	}
	if !hasTaskRunCancelSelector(cancelRequest) {
		http.Error(responseWriter, "taskRunIDs, requesterPersonID, or scheduleOnly is required", http.StatusBadRequest)
		return
	}
	taskRunCancelRequest := task.TaskRunCancelRequest{
		TaskRunIDs:                 cancelRequest.TaskRunIDs,
		RequesterPersonID:          strings.TrimSpace(cancelRequest.RequesterPersonID),
		ScheduleOnly:               cancelRequest.ScheduleOnly,
		StaleBefore:                staleBefore,
		Reason:                     firstNonEmptyAdminString(cancelRequest.Reason, "admin task cancel"),
		OriginConversationIDPrefix: adminScheduleOriginPrefix(cancelRequest.ScheduleOnly),
	}
	cancelledTaskRuns := taskRunHandler.TaskRunService.CancelActiveTaskRuns(taskRunCancelRequest)
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"cancelledTaskRunCount": len(cancelledTaskRuns),
		"taskRuns":              cancelledTaskRuns,
	})
}

func hasTaskRunCancelSelector(request taskRunCancelRequest) bool {
	return len(request.TaskRunIDs) > 0 || strings.TrimSpace(request.RequesterPersonID) != "" || request.ScheduleOnly
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

func parseOptionalAdminTime(value string) (*time.Time, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil, nil
	}
	parsedTime, errorValue := time.Parse(time.RFC3339, trimmedValue)
	if errorValue != nil {
		return nil, errorValue
	}
	return &parsedTime, nil
}

func adminScheduleOriginPrefix(scheduleOnly bool) string {
	if scheduleOnly {
		return "schedule:"
	}
	return ""
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
