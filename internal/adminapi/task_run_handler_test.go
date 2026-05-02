package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/identity"
	"blueclaw/internal/llm"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

func TestTaskRunHandlerLaunchesAdminTask(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticAdminLanguageModel{content: `{"action":"final_reply","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"finalReply":"admin done"}`})
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"admin": {"memory.search"},
	}, nil)
	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{"admin@example.com": "person-1"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1", SecurityLevelRank: 100, GrantedClasses: []string{"internal"}},
		},
	})
	handler := TaskRunHandler{
		TaskLauncher:    agentruntime.NewTaskLauncher(agentKernel, toolCatalogBuilder),
		IdentityService: identityService,
		WorkspaceID:     "workspace-1",
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/task/run", strings.NewReader(`{"requesterPersonID":"person-1","prompt":"run admin task","profileName":"admin"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunTask(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), "admin done") {
		t.Fatalf("expected final reply, got %s", responseRecorder.Body.String())
	}
}

type staticAdminLanguageModel struct {
	content string
}

func (languageModel staticAdminLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticAdminLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{Content: languageModel.content}, nil
}
