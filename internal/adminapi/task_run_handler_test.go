package adminapi

import (
	"context"
	"encoding/json"
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
	agentKernel.UseLanguageModelProvider(staticAdminLanguageModel{content: `{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"message":"admin done"}`})
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

func TestTaskRunHandlerLaunchIgnoresClientCancellation(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(contextAwareAdminLanguageModel{content: `{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"message":"admin done"}`})
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
	requestContext, cancelRequest := context.WithCancel(request.Context())
	cancelRequest()
	request = request.WithContext(requestContext)
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunTask(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), "admin done") {
		t.Fatalf("expected final reply, got %s", responseRecorder.Body.String())
	}
}

func TestTaskRunHandlerUsesSDKDTopologyPresetWithoutIntakeCall(t *testing.T) {
	handler, taskRunService, taskEventService, languageModel := newPresetTaskRunHandler(true)
	presetDecision, _, errorValue := handler.resolveTaskDecisionPreset(sdkdTopologyTaskDecisionPreset)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if presetDecision.TaskLevel != agent.TaskLevelXLow {
		t.Fatalf("expected xlow diagnostic task level, got %s", presetDecision.TaskLevel)
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/task/run", strings.NewReader(`{"requesterPersonID":"person-1","prompt":"reply exactly","taskDecisionPreset":"sdkd_topology"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunTask(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if len(languageModel.schemaNames) != 1 || languageModel.schemaNames[0] != "blueclaw_agent_turn_action" {
		t.Fatalf("expected only agent action schema, got %v", languageModel.schemaNames)
	}
	if languageModel.schemaDocumentContains("terminal.run") {
		t.Fatalf("expected diagnostic profile without tool schemas, got %+v", languageModel.schemaDocuments)
	}
	var responseDocument struct {
		TaskRun task.TaskRun `json:"taskRun"`
	}
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &responseDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, isFound := taskRunService.FindTaskRun(responseDocument.TaskRun.TaskRunID); !isFound {
		t.Fatalf("expected persisted task run, got %+v", responseDocument.TaskRun)
	}
	if !taskEventsContainBody(taskEventService.ListTaskEvent(responseDocument.TaskRun.TaskRunID), "agent.task_launched", `"isIntakePrecomputed":true`) {
		t.Fatal("expected precomputed intake audit event")
	}
}

func TestTaskRunHandlerRejectsTaskDecisionPresetOverrides(t *testing.T) {
	overrides := []string{
		`"profileName":"default"`,
		`"pinnedToolNames":["terminal.run"]`,
		`"pinnedSkillNames":["mail"]`,
	}
	for _, override := range overrides {
		t.Run(override, func(t *testing.T) {
			handler, taskRunService, _, _ := newPresetTaskRunHandler(true)
			body := `{"requesterPersonID":"person-1","prompt":"reply exactly","taskDecisionPreset":"sdkd_topology",` + override + `}`
			request := httptest.NewRequest(http.MethodPost, "/admin/api/task/run", strings.NewReader(body))
			responseRecorder := httptest.NewRecorder()

			handler.HandleRunTask(responseRecorder, request)

			if responseRecorder.Code != http.StatusBadRequest {
				t.Fatalf("expected bad request, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
			}
			if len(taskRunService.ListTaskRun()) != 0 {
				t.Fatalf("expected no task runs, got %+v", taskRunService.ListTaskRun())
			}
		})
	}
}

func TestTaskRunHandlerRejectsDisabledTaskDecisionPresetBeforeLaunch(t *testing.T) {
	handler, taskRunService, _, _ := newPresetTaskRunHandler(false)
	request := httptest.NewRequest(http.MethodPost, "/admin/api/task/run", strings.NewReader(`{"requesterPersonID":"person-1","prompt":"reply exactly","taskDecisionPreset":"sdkd_topology"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunTask(responseRecorder, request)

	if responseRecorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if len(taskRunService.ListTaskRun()) != 0 {
		t.Fatalf("expected no task runs, got %+v", taskRunService.ListTaskRun())
	}
}

func TestTaskRunHandlerRejectsUnsupportedTaskDecisionPreset(t *testing.T) {
	handler, taskRunService, _, _ := newPresetTaskRunHandler(true)
	request := httptest.NewRequest(http.MethodPost, "/admin/api/task/run", strings.NewReader(`{"requesterPersonID":"person-1","prompt":"reply exactly","taskDecisionPreset":"unsafe"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunTask(responseRecorder, request)

	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if len(taskRunService.ListTaskRun()) != 0 {
		t.Fatalf("expected no task runs, got %+v", taskRunService.ListTaskRun())
	}
}

func TestTaskRunHandlerCancelsActiveTaskRun(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "schedule:schedule-1", "stale schedule")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	handler := TaskRunHandler{TaskRunService: taskRunService}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/task/cancel", strings.NewReader(`{"taskRunIDs":["`+taskRun.TaskRunID+`"],"reason":"cleanup"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleCancelTaskRun(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	cancelledTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected cancelled task run, got found=%v run=%+v", isFound, cancelledTaskRun)
	}
	if !strings.Contains(responseRecorder.Body.String(), `"cancelledTaskRunCount":1`) {
		t.Fatalf("expected cancel count in response, got %s", responseRecorder.Body.String())
	}
}

func TestTaskRunHandlerStopsRequesterTasksOnly(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	requesterTaskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")
	otherTaskRun := taskRunService.CreateTaskRun("person-2", "direct-2", "other task")
	for _, taskRun := range []task.TaskRun{requesterTaskRun, otherTaskRun} {
		if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	handler := TaskRunHandler{TaskRunService: taskRunService}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/task/cancel", strings.NewReader(`{"mode":"stop_all","requesterPersonID":"person-1","reason":"slash stop-all"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleCancelTaskRun(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	cancelledTaskRun, _ := taskRunService.FindTaskRun(requesterTaskRun.TaskRunID)
	unchangedTaskRun, _ := taskRunService.FindTaskRun(otherTaskRun.TaskRunID)
	if cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("requester task status = %s, want cancelled", cancelledTaskRun.Status)
	}
	if unchangedTaskRun.Status != task.TaskStatusRunning {
		t.Fatalf("other task status = %s, want running", unchangedTaskRun.Status)
	}
	if !strings.Contains(responseRecorder.Body.String(), `"scheduleTouched":false`) {
		t.Fatalf("expected scheduleTouched false in response, got %s", responseRecorder.Body.String())
	}
}

type staticAdminLanguageModel struct {
	content string
}

type schemaRecordingAdminLanguageModel struct {
	schemaNames     []string
	schemaDocuments []string
}

func (languageModel *schemaRecordingAdminLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *schemaRecordingAdminLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.schemaNames = append(languageModel.schemaNames, request.StructuredOutputSchema.Name)
	languageModel.schemaDocuments = append(languageModel.schemaDocuments, request.StructuredOutputSchema.Document)
	return llm.StructuredResponse{Content: `{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"message":"diagnostic done"}`}, nil
}

func (languageModel *schemaRecordingAdminLanguageModel) schemaDocumentContains(fragment string) bool {
	return strings.Contains(strings.Join(languageModel.schemaDocuments, "\n"), fragment)
}

func newPresetTaskRunHandler(isPresetAllowed bool) (TaskRunHandler, *task.TaskRunService, *task.TaskEventService, *schemaRecordingAdminLanguageModel) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	languageModel := &schemaRecordingAdminLanguageModel{}
	agentKernel.UseLanguageModelProvider(languageModel)
	agentKernel.UseIntakeLanguageModelProvider(languageModel)
	agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true, DefaultTaskLevel: agent.TaskLevelLow})
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		sdkdTopologyDiagnosticProfileName: {"sdkd.diagnostic.no_tools"},
	}, nil)
	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1", SecurityLevelRank: 100, GrantedClasses: []string{"internal"}},
		},
	})
	return TaskRunHandler{
		TaskLauncher:            agentruntime.NewTaskLauncher(agentKernel, toolCatalogBuilder),
		IdentityService:         identityService,
		WorkspaceID:             "workspace-1",
		TaskRunService:          taskRunService,
		AllowTaskDecisionPreset: isPresetAllowed,
	}, taskRunService, taskEventService, languageModel
}

func taskEventsContainBody(taskEvents []task.TaskEvent, name string, bodyFragment string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name && strings.Contains(taskEvent.Body, bodyFragment) {
			return true
		}
	}
	return false
}

func (languageModel staticAdminLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticAdminLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{Content: languageModel.content}, nil
}

type contextAwareAdminLanguageModel struct {
	content string
}

func (languageModel contextAwareAdminLanguageModel) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
		return staticAdminLanguageModel{content: languageModel.content}.GenerateResponse(ctx, prompt)
	}
}

func (languageModel contextAwareAdminLanguageModel) GenerateStructuredResponse(ctx context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	select {
	case <-ctx.Done():
		return llm.StructuredResponse{}, ctx.Err()
	default:
		return staticAdminLanguageModel{content: languageModel.content}.GenerateStructuredResponse(ctx, request)
	}
}
