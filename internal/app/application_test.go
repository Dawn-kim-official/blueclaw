package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/config"
	"blueclaw/internal/connectors"
	"blueclaw/internal/llm"
	"blueclaw/internal/runtimecontrol"
	"blueclaw/internal/task"
)

func TestTaskIntakeControllerStartsUnquiesced(t *testing.T) {
	controller := runtimecontrol.NewTaskIntakeController()

	if controller.IsQuiesced() {
		t.Fatal("fresh task intake controller must start unquiesced")
	}
}

func TestResolveLanguageModelProviderDefaultsToCapabilityLLM(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "must-not-be-read")
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.Capability.Model = "gemma-4-E4B-it"

	languageModelProvider := resolveLanguageModelProvider(runtimeConfiguration)
	if languageModelProvider == nil {
		t.Fatal("expected capability provider to be inferred")
	}
	capabilityLLMClient, isCapabilityProvider := languageModelProvider.(llm.CapabilityLLMClient)
	if !isCapabilityProvider {
		t.Fatalf("expected capability provider, got %T", languageModelProvider)
	}
	if capabilityLLMClient.ModelName != "gemma-4-E4B-it" {
		t.Fatalf("expected capability model, got %q", capabilityLLMClient.ModelName)
	}
}

func TestResolveIntakeLanguageModelProviderUsesReliableTaskTierModel(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Agent.Intake.Enabled = true
	runtimeConfiguration.Agent.Intake.ExecutionMode = "auto"

	languageModelProvider := resolveIntakeLanguageModelProvider(runtimeConfiguration, newCapabilityClient(runtimeConfiguration), nil)
	fallbackLanguageModelProvider, isFallbackProvider := languageModelProvider.(llm.FallbackLanguageModelProvider)
	if !isFallbackProvider {
		t.Fatalf("expected fallback intake provider, got %T", languageModelProvider)
	}
	primaryClient, isPrimaryCapabilityClient := fallbackLanguageModelProvider.PrimaryProvider.(llm.CapabilityLLMClient)
	if !isPrimaryCapabilityClient {
		t.Fatalf("expected primary capability intake provider, got %T", fallbackLanguageModelProvider.PrimaryProvider)
	}
	fallbackClient, isFallbackCapabilityClient := fallbackLanguageModelProvider.FallbackProvider.(llm.CapabilityLLMClient)
	if !isFallbackCapabilityClient {
		t.Fatalf("expected fallback capability intake provider, got %T", fallbackLanguageModelProvider.FallbackProvider)
	}
	expectedTierNames := llm.ResolveModelTierNames(deriveLanguageModelRuntimeConfiguration(runtimeConfiguration))
	if primaryClient.ModelName != expectedTierNames.Medium {
		t.Fatalf("expected medium tier intake model %q, got %q", expectedTierNames.Medium, primaryClient.ModelName)
	}
	if fallbackClient.ModelName != expectedTierNames.High {
		t.Fatalf("expected high tier intake fallback model %q, got %q", expectedTierNames.High, fallbackClient.ModelName)
	}
	if primaryClient.ExecutionMode != "auto" || fallbackClient.ExecutionMode != "auto" {
		t.Fatalf("expected automatic intake execution mode, got %q and %q", primaryClient.ExecutionMode, fallbackClient.ExecutionMode)
	}
}

func TestResolveIntakeLanguageModelProviderUsesExplicitModel(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Agent.Intake.Enabled = true
	runtimeConfiguration.Agent.Intake.Model = "x-ai/grok-4.3"

	languageModelProvider := resolveIntakeLanguageModelProvider(runtimeConfiguration, newCapabilityClient(runtimeConfiguration), nil)
	fallbackLanguageModelProvider, isFallbackProvider := languageModelProvider.(llm.FallbackLanguageModelProvider)
	if !isFallbackProvider {
		t.Fatalf("expected fallback intake provider, got %T", languageModelProvider)
	}
	primaryClient, isPrimaryCapabilityClient := fallbackLanguageModelProvider.PrimaryProvider.(llm.CapabilityLLMClient)
	if !isPrimaryCapabilityClient {
		t.Fatalf("expected primary capability intake provider, got %T", fallbackLanguageModelProvider.PrimaryProvider)
	}
	if primaryClient.ModelName != "x-ai/grok-4.3" {
		t.Fatalf("expected explicit intake model, got %q", primaryClient.ModelName)
	}
}

func TestDeriveAgentTurnOptionsWiresContextWindowTokens(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.Capability.ContextWindowTokens = 128000

	options := deriveAgentTurnOptions(runtimeConfiguration)

	if options.ContextWindowTokens != 128000 {
		t.Fatalf("expected context window tokens to be wired, got %d", options.ContextWindowTokens)
	}
}

func TestLoadAgentInstructionPromptUsesAgentsAndSkills(t *testing.T) {
	workspacePath := t.TempDir()
	skillDirectoryPath := filepath.Join(workspacePath, ".agents", "skills", "browser")
	if errorValue := os.MkdirAll(skillDirectoryPath, 0o755); errorValue != nil {
		t.Fatalf("expected skill directory: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "IDENTITY.md"), []byte("Use the runtime display name."), 0o600); errorValue != nil {
		t.Fatalf("expected identity file: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "BOT_PROFILE.yaml"), []byte("username: internkim\ndisplayName: 김인턴\nenglishDisplayName: Intern Kim\naliases:\n  - 인턴킴\npublicDescription: \"\"\nidentityExtension: Use the display name.\n"), 0o600); errorValue != nil {
		t.Fatalf("expected bot profile file: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "SOUL.md"), []byte("Lead with the result."), 0o600); errorValue != nil {
		t.Fatalf("expected soul file: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "AGENTS.md"), []byte("Use agent-browser for web automation."), 0o600); errorValue != nil {
		t.Fatalf("expected agents file: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(skillDirectoryPath, "SKILL.md"), []byte("Run agent-browser snapshot -i after navigation."), 0o600); errorValue != nil {
		t.Fatalf("expected skill file: %v", errorValue)
	}
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Terminal.WorkspaceRootPath = workspacePath

	instructionBundle := loadAgentInstructionBundle(runtimeConfiguration)
	for _, expectedFragment := range []string{"Use the runtime display name.", "current displayName: 김인턴", "Use the display name.", "Lead with the result.", "Use agent-browser for web automation."} {
		if !strings.Contains(instructionBundle.Prompt, expectedFragment) {
			t.Fatalf("expected instruction prompt to contain %q, got %q", expectedFragment, instructionBundle.Prompt)
		}
	}
	if len(instructionBundle.Skills) == 0 || instructionBundle.Skills[0].Name != "browser" {
		t.Fatalf("expected skill metadata to be loaded: %+v", instructionBundle.Skills)
	}
}

func TestLoadAgentInstructionBundleDiscoversAddedUserSkill(t *testing.T) {
	workspacePath := t.TempDir()
	skillDirectoryPath := filepath.Join(workspacePath, ".agents", "skills", "research-helper")
	if errorValue := os.MkdirAll(skillDirectoryPath, 0o755); errorValue != nil {
		t.Fatalf("expected skill directory: %v", errorValue)
	}
	skillDocument := `---
name: research-helper
description: Help with research tasks.
when_to_use: Use for source lookup requests.
---
Research helper body.
`
	if errorValue := os.WriteFile(filepath.Join(skillDirectoryPath, "SKILL.md"), []byte(skillDocument), 0o600); errorValue != nil {
		t.Fatalf("expected skill file: %v", errorValue)
	}
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Terminal.WorkspaceRootPath = workspacePath

	instructionBundle := loadAgentInstructionBundle(runtimeConfiguration)

	if len(instructionBundle.Skills) != 1 || instructionBundle.Skills[0].Name != "research-helper" {
		t.Fatalf("expected added user skill to be discovered, got %+v", instructionBundle.Skills)
	}
	if instructionBundle.Skills[0].WhenToUse != "Use for source lookup requests." {
		t.Fatalf("expected standard skill fields, got %+v", instructionBundle.Skills[0])
	}
}

func TestDeriveAllowedToolNamesByProfileKeepsDomainCapabilitiesOutOfBaseline(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.AgentProfiles = []config.AgentProfileConfiguration{
		{Name: "default", AllowedToolNames: []string{"terminal.run"}},
	}
	runtimeConfiguration.Capabilities.ToolDescriptors = []config.CapabilityToolDescriptor{{Name: "site.app.create"}}

	allowedToolNamesByProfile := deriveAllowedToolNamesByProfile(runtimeConfiguration)
	defaultProfileToolNames := allowedToolNamesByProfile["default"]

	if containsString(defaultProfileToolNames, "site.app.create") {
		t.Fatalf("expected domain capability to stay out of profile baseline, got %+v", defaultProfileToolNames)
	}
	for _, expectedToolName := range []string{"terminal.run", "skill.search", "web.fetch", "file.write", "schedule.create", "schedule.update"} {
		if !containsString(defaultProfileToolNames, expectedToolName) {
			t.Fatalf("expected baseline tool %q, got %+v", expectedToolName, defaultProfileToolNames)
		}
	}
}

func TestNewApplicationRegistersSecretlessConnectorTransports(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()

	application := NewApplication(runtimeConfiguration, "")

	transportNames := strings.Join(application.connectorTransportNames(), ",")
	for _, expectedName := range []string{"mattermost:mattermost-internal-ingress", "slack:slack-internal-ingress", "signal:signal-internal-ingress"} {
		if !strings.Contains(transportNames, expectedName) {
			t.Fatalf("expected transport %q in %q", expectedName, transportNames)
		}
	}
	if strings.Contains(transportNames, "websocket") {
		t.Fatalf("expected no platform-owned websocket transport, got %q", transportNames)
	}
}

func TestNewApplicationAllowsSignalInternalIngress(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	runtimeConfiguration.Connectors.Signal.Enabled = true

	application := NewApplication(runtimeConfiguration, "")

	if application.startupError != nil {
		t.Fatalf("expected signal internal ingress to be allowed: %v", application.startupError)
	}
}

func TestApplicationConnectorRouteAcceptsNormalizedSlackEvent(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	application := NewApplication(runtimeConfiguration, "")

	payload := []byte(`{}`)
	request := httptest.NewRequest(http.MethodPost, "/connectors/slack/events", bytes.NewReader(payload))
	responseRecorder := httptest.NewRecorder()
	application.httpServer.Handler.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected normalized event status ok, got %d", responseRecorder.Code)
	}
	var responseDocument map[string]any
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &responseDocument); errorValue != nil {
		t.Fatalf("expected response document: %v", errorValue)
	}
	if responseDocument["platform"] != "slack" {
		t.Fatalf("expected slack platform response, got %+v", responseDocument)
	}
	if responseDocument["reason"] != "no_event" {
		t.Fatalf("expected no_event response, got %+v", responseDocument)
	}
}

func TestApplicationRegistersSignalHTTPRoute(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	application := NewApplication(runtimeConfiguration, "")

	payload := []byte(`{}`)
	request := httptest.NewRequest(http.MethodPost, "/connectors/signal/events", bytes.NewReader(payload))
	responseRecorder := httptest.NewRecorder()
	application.httpServer.Handler.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected signal normalized event status ok, got %d", responseRecorder.Code)
	}
}

func TestApplicationAutoResumeLaunchesAtMostFiveInterruptedTaskRuns(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	now := time.Now()
	repository := &applicationAutoResumeRepository{}
	for index := 0; index < 7; index++ {
		taskRun := task.TaskRun{
			TaskRunID:            "task-" + string(rune('a'+index)),
			RequesterPersonID:    "person-1",
			OriginConversationID: "conversation-1",
			OriginReplyTargetID:  "reply-1",
			Status:               task.TaskStatusInterrupted,
			Prompt:               "finish task",
			CreatedAt:            now.Add(time.Duration(index) * time.Minute),
			UpdatedAt:            now.Add(time.Duration(index) * time.Minute),
		}
		repository.taskRuns = append(repository.taskRuns, taskRun)
		taskEventService.AppendTaskEvent(taskRun.TaskRunID, "task.interrupted", "runtime restarted")
	}
	taskRunService.UseRepository(repository)
	resumer := &applicationAutoResumeResumer{}
	application := &Application{
		taskRunService:             taskRunService,
		interruptedTaskResumer:     resumer,
		interruptedTaskResumeDelay: 0,
	}

	application.resumeInterruptedTaskRuns(context.Background(), now.Add(time.Hour))

	if len(resumer.taskRunIDs) != 5 {
		t.Fatalf("resume count = %d, want 5", len(resumer.taskRunIDs))
	}
	for _, taskRunID := range resumer.taskRunIDs {
		if !taskEventsContainApplicationEvent(taskRunService.ListTaskEvent(taskRunID), "task.auto_resume_attempted") {
			t.Fatalf("expected auto-resume attempt event for %s", taskRunID)
		}
	}
	skippedCount := 0
	for _, taskRun := range repository.taskRuns {
		if taskEventsContainApplicationEvent(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.auto_resume_skipped") {
			skippedCount++
		}
	}
	if skippedCount != 2 {
		t.Fatalf("skipped count = %d, want 2", skippedCount)
	}
}

type applicationAutoResumeResumer struct {
	taskRunIDs []string
}

func (resumer *applicationAutoResumeResumer) CanResumeInterruptedTaskRun(task.TaskRun) bool {
	return true
}

func (resumer *applicationAutoResumeResumer) ResumeInterruptedTaskRun(_ context.Context, taskRun task.TaskRun) (connectors.ConnectorRuntimeResult, error) {
	resumer.taskRunIDs = append(resumer.taskRunIDs, taskRun.TaskRunID)
	return connectors.ConnectorRuntimeResult{Handled: true, TaskRunID: taskRun.TaskRunID}, nil
}

type applicationAutoResumeRepository struct {
	taskRuns []task.TaskRun
}

func (repository *applicationAutoResumeRepository) SaveTaskRun(taskRun task.TaskRun) error {
	repository.taskRuns = append(repository.taskRuns, taskRun)
	return nil
}

func (repository *applicationAutoResumeRepository) StartTaskRunAttempt(task.TaskRun, task.TaskAttempt) error {
	return nil
}

func (repository *applicationAutoResumeRepository) FinishTaskRunAttempt(task.TaskRun, task.TaskAttempt) error {
	return nil
}

func (repository *applicationAutoResumeRepository) TransitionTaskRun(transition task.TaskRunTransition) (task.TaskRun, error) {
	for index, taskRun := range repository.taskRuns {
		if taskRun.TaskRunID != transition.TaskRunID {
			continue
		}
		if !applicationTaskRunStatusAllowed(taskRun.Status, transition.FromStates) {
			return task.TaskRun{}, task.ErrIllegalTransition{
				TaskRunID:     transition.TaskRunID,
				CurrentStatus: taskRun.Status,
				FromStates:    append([]task.TaskStatus{}, transition.FromStates...),
				ToState:       transition.ToState,
			}
		}
		taskRun.Status = transition.ToState
		taskRun.UpdatedAt = transition.UpdatedAt
		repository.taskRuns[index] = taskRun
		return taskRun, nil
	}
	return task.TaskRun{}, errors.New("task run not found")
}

func applicationTaskRunStatusAllowed(status task.TaskStatus, allowedStatuses []task.TaskStatus) bool {
	for _, allowedStatus := range allowedStatuses {
		if status == allowedStatus {
			return true
		}
	}
	return false
}

func (repository *applicationAutoResumeRepository) FindTaskRun(taskRunID string) (task.TaskRun, bool, error) {
	for _, taskRun := range repository.taskRuns {
		if taskRun.TaskRunID == taskRunID {
			return taskRun, true, nil
		}
	}
	return task.TaskRun{}, false, nil
}

func (repository *applicationAutoResumeRepository) FindTaskAttempt(string) (task.TaskAttempt, bool, error) {
	return task.TaskAttempt{}, false, nil
}

func (repository *applicationAutoResumeRepository) ListTaskRun() ([]task.TaskRun, error) {
	return append([]task.TaskRun{}, repository.taskRuns...), nil
}

func (repository *applicationAutoResumeRepository) ListTaskRunByPersonID(personID string) ([]task.TaskRun, error) {
	taskRuns := []task.TaskRun{}
	for _, taskRun := range repository.taskRuns {
		if taskRun.RequesterPersonID == personID {
			taskRuns = append(taskRuns, taskRun)
		}
	}
	return taskRuns, nil
}

func (repository *applicationAutoResumeRepository) DeleteTaskRunsBefore(time.Time, []string) ([]string, error) {
	return nil, nil
}

func taskEventsContainApplicationEvent(taskEvents []task.TaskEvent, name string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name {
			return true
		}
	}
	return false
}
