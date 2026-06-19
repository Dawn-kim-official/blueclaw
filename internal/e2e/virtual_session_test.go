package e2e

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/agenttest"
	"blueclaw/internal/capability"
	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

func TestSlidesScenarioDoesNotScriptToolCalls(t *testing.T) {
	scenario := SlidesLocalMultiturnSuccessScenario(t.TempDir())
	if len(scenario.Turns) != 1 {
		t.Fatalf("expected one slides turn, got %d", len(scenario.Turns))
	}
	if len(scenario.Turns[0].ActionResponses) != 0 {
		t.Fatal("slides scenario must not script model tool calls or artifact creation")
	}
}

func TestSlidesLocalMultiturnSuccessLive(t *testing.T) {
	if !truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")) {
		t.Skip("set BLUECLAW_E2E_LIVE=1 to explicitly run costed live slides virtual session")
	}
	endpoint := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT"))
	socketPath := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET"))
	if endpoint == "" && socketPath == "" {
		t.Skip("set BLUECLAW_E2E_LLM_ENDPOINT or BLUECLAW_E2E_LLM_UNIX_SOCKET to run live slides virtual session")
	}
	scenario := SlidesLocalMultiturnSuccessScenario(t.TempDir())
	if skillDirectoryPath := rootSimpleSlidesSkillPath(); skillDirectoryPath != "" {
		scenario.Skills = nil
		scenario.SkillDirectoryPaths = []string{skillDirectoryPath}
	}
	scenario.LanguageModel = llm.CapabilityLLMClient{
		CapabilityClient: capability.NewClient(capability.Configuration{
			Endpoint:       endpoint,
			UnixSocketPath: socketPath,
			Timeout:        90 * time.Second,
		}),
		ModelName:     os.Getenv("BLUECLAW_E2E_LLM_MODEL"),
		ExecutionMode: firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_LLM_EXECUTION_MODE"), "auto"),
	}

	result, errorValue := RunVirtualSession(context.Background(), scenario)
	if errorValue != nil {
		t.Fatalf("expected slides scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.terminal.run.result", "exitCode") {
		t.Fatal("expected terminal build to succeed")
	}
}

func rootSimpleSlidesSkillPath() string {
	candidatePath := filepath.Clean("../../../../assets/blueclaw-workspace/skills/simple-slides")
	if _, errorValue := os.Stat(candidatePath); errorValue == nil {
		return candidatePath
	}
	return ""
}

func TestMemoryGuidedFollowup(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), MemoryGuidedFollowupScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected memory scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	secondTurn := result.TurnResults[1]
	if !eventsContain(secondTurn.Events, "agent.task_launched", `"memoryFactCount":`) {
		t.Fatal("expected task launch memory fact count")
	}
	if strings.Contains(secondTurn.FinishMessage, "아까") {
		t.Fatalf("expected concrete recalled preference, got %q", secondTurn.FinishMessage)
	}
}

func TestPlainQuestionAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), PlainQuestionAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected plain question acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if strings.TrimSpace(turnResult.FinishMessage) == "" {
		t.Fatal("expected non-empty final reply")
	}
	if toolEventCount(turnResult.Events) != 0 {
		t.Fatalf("expected no tool events, got events: %s", summarizeEvents(turnResult.Events))
	}
	if failureEventCount(turnResult.Events) != 0 {
		t.Fatalf("expected no failure events, got events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestLanguageModelCassetteRoundTripVirtualSession(t *testing.T) {
	recordingLanguageModel := NewRecordingLanguageModel(agenttest.NewActionScriptedLanguageModel(
		actionFinishMessage("cassette reply sequence"),
	))
	recordedScenario := cassetteRoundTripScenario(t.TempDir(), recordingLanguageModel)
	recordedResult, errorValue := RunVirtualSession(context.Background(), recordedScenario)
	if errorValue != nil {
		t.Fatalf("expected recording session to pass: %v", errorValue)
	}

	cassettePath := filepath.Join(t.TempDir(), "model-cassette.json")
	if errorValue := SaveLanguageModelCassette(cassettePath, recordingLanguageModel.Cassette()); errorValue != nil {
		t.Fatalf("expected cassette save to pass: %v", errorValue)
	}
	cassette, errorValue := LoadLanguageModelCassette(cassettePath)
	if errorValue != nil {
		t.Fatalf("expected cassette load to pass: %v", errorValue)
	}

	replayingLanguageModel := NewReplayingLanguageModel(cassette)
	replayedScenario := cassetteRoundTripScenario(t.TempDir(), replayingLanguageModel)
	replayedResult, errorValue := RunVirtualSession(context.Background(), replayedScenario)
	if errorValue != nil {
		t.Fatalf("expected replay session to pass: %v", errorValue)
	}

	recordedReply := recordedResult.TurnResults[0].FinishMessage
	replayedReply := replayedResult.TurnResults[0].FinishMessage
	if recordedReply != replayedReply {
		t.Fatalf("expected replayed reply %q to match recorded reply %q", replayedReply, recordedReply)
	}
	if !reflect.DeepEqual(cassette.Entries, replayingLanguageModel.ReturnedEntries()) {
		t.Fatalf("expected replayed model response sequence to match cassette")
	}
}

func cassetteRoundTripScenario(artifactDirectoryPath string, languageModel llm.LanguageModelProvider) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "cassette_round_trip",
		ArtifactDirectoryPath: artifactDirectoryPath,
		LanguageModel:         languageModel,
		Turns: []VirtualTurn{{
			Prompt:                 "도구 없이 짧게 답해줘.",
			ExpectedReplyFragments: []string{"cassette reply sequence"},
		}},
	}
}

func TestWebSearchAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), WebSearchAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected web search acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if countEvents(turnResult.Events, "tool.web.search.requested") != 1 {
		t.Fatalf("expected one web.search request, got events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.FinishMessage, "BlueclawSearchStubToken") {
		t.Fatalf("expected final reply to contain search stub token, got %q", turnResult.FinishMessage)
	}
}

func TestToolPermissionScenarioReturnsPlannedFallback(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ToolPermissionHidesSkillScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected permission scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !strings.Contains(turnResult.FinishMessage, "필요한 도구") {
		t.Fatalf("expected planned fallback reply, got %q", turnResult.FinishMessage)
	}
}

func TestAmbientTaskCaptureAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AmbientTaskCaptureAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected ambient task capture scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "agent.ambient_duty_launch", `"dutyName":"team_flow_update"`) {
		t.Fatalf("expected ambient duty launch for an other-person-mentioned task assignment; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "agent.tool_palette.fixed", "ambient_capture") {
		t.Fatalf("expected request_tools to be blocked by the fixed ambient palette; events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.terminal.run.requested", "") {
		t.Fatalf("ambient capture must not reach terminal.run; events: %s", summarizeEvents(turnResult.Events))
	}
	reviseResult := result.TurnResults[1]
	if !eventsContain(reviseResult.Events, "tool.flow.task.update.requested", "flow.task.update") {
		t.Fatalf("expected a same-thread follow-up to update the existing task; events: %s", summarizeEvents(reviseResult.Events))
	}
	if eventsContain(reviseResult.Events, "tool.flow.task.add.requested", "flow.task.add") {
		t.Fatalf("same-thread revision must update, not add a duplicate task; events: %s", summarizeEvents(reviseResult.Events))
	}
}

func TestGWSDisabled(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), GWSDisabledScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected gws disabled scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.google.drive.import_pptx.requested", "google.drive.import_pptx") {
		t.Fatal("expected attempted google tool request to be audited")
	}
	if !eventsContain(turnResult.Events, "tool.google.drive.import_pptx.result", "tool is not allowed") {
		t.Fatal("expected google tool to be denied by catalog allowlist")
	}
}

func TestScheduleCreateAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ScheduleCreateAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected schedule acceptance scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "schedule.created", `"intervalSecond":60`) {
		t.Fatal("expected schedule.create to persist an interval schedule")
	}
	if !strings.Contains(turnResult.ModelContext, "schedule.create") {
		t.Fatal("expected model context to expose schedule.create")
	}
}

func TestScheduleLifecycleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ScheduleLifecycleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected schedule lifecycle acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 3 {
		t.Fatalf("expected three turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	thirdTurnResult := result.TurnResults[2]
	if !eventsContain(firstTurnResult.Events, "schedule.created", `"intervalSecond":1800`) {
		t.Fatalf("expected initial interval schedule; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "schedule.updated", `"intervalSecond":3600`) {
		t.Fatalf("expected modification to update existing schedule; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(thirdTurnResult.Events, "schedule.cancelled", `"cancelledScheduleCount":1`) {
		t.Fatalf("expected deletion to cancel active schedule; events: %s", summarizeEvents(thirdTurnResult.Events))
	}
	if activeScheduleCount(result.TaskSchedules) != 0 {
		t.Fatalf("expected zero active schedules, got %+v", result.TaskSchedules)
	}
}

func TestCalendarEventLifecycleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), CalendarEventLifecycleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected calendar event lifecycle acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 3 {
		t.Fatalf("expected three turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	thirdTurnResult := result.TurnResults[2]
	if countEvents(firstTurnResult.Events, "tool.calendar.event.add.requested") != 1 {
		t.Fatalf("expected one calendar add request; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countEvents(secondTurnResult.Events, "tool.calendar.event.update.requested") != 1 {
		t.Fatalf("expected one calendar update request; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.calendar.event.update.requested", "2026-06-13T14:00:00+09:00") {
		t.Fatalf("expected updated time in calendar update input; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if countEvents(thirdTurnResult.Events, "tool.calendar.event.delete.requested") != 1 {
		t.Fatalf("expected one calendar delete request; events: %s", summarizeEvents(thirdTurnResult.Events))
	}
	if !strings.Contains(thirdTurnResult.FinishMessage, "삭제했습니다") {
		t.Fatalf("expected deletion confirmation, got %q", thirdTurnResult.FinishMessage)
	}
}

func TestAmbientDutyCalendarAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AmbientDutyCalendarAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected ambient duty calendar acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if countEvents(turnResult.Events, "tool.calendar.event.add.requested") != 1 {
		t.Fatalf("expected one calendar add request; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "agent.ambient_duty_launch", `"dutyName":"calendar_upkeep"`) {
		t.Fatalf("expected ambient duty launch event; events: %s", summarizeEvents(turnResult.Events))
	}
	if turnResult.ReplyTargetID != "virtual-message-001" {
		t.Fatalf("expected thread reply target, got %q", turnResult.ReplyTargetID)
	}
}

func TestSkillLifecycleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SkillLifecycleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected skill lifecycle acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	if countEvents(firstTurnResult.Events, "tool.skill.add.requested") != 1 {
		t.Fatalf("expected one skill.add request; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countEvents(secondTurnResult.Events, "tool.skill.remove.requested") != 1 {
		t.Fatalf("expected one skill.remove request; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	skillDirectoryPath := filepath.Join(result.ArtifactDirectoryPath, "workspace", ".agents", "skills", "memo-helper")
	if _, errorValue := os.Stat(skillDirectoryPath); !os.IsNotExist(errorValue) {
		t.Fatalf("expected memo-helper skill directory to be removed, got %v", errorValue)
	}
}

func TestCapabilityQuestionAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), CapabilityQuestionAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected capability question acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	requestedBodies := eventBodies(turnResult.Events, "tool.skill.search.requested")
	if len(requestedBodies) != 1 {
		t.Fatalf("expected one skill.search request, got events: %s", summarizeEvents(turnResult.Events))
	}
	if strings.Contains(requestedBodies[0], "queries") || strings.Contains(requestedBodies[0], "limit") {
		t.Fatalf("expected empty skill.search input, got %s", requestedBodies[0])
	}
	if !eventsContain(turnResult.Events, "tool.skill.search.result", "simple-slides") {
		t.Fatalf("expected skill.search result to include simple-slides; events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.FinishMessage, "simple-slides") {
		t.Fatalf("expected final reply to mention simple-slides, got %q", turnResult.FinishMessage)
	}
}

func TestTaskHistoryQuestionAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), TaskHistoryQuestionAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected task history question acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	secondTurnResult := result.TurnResults[1]
	if countEvents(secondTurnResult.Events, "tool.task.history.requested") != 1 {
		t.Fatalf("expected one task.history request; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.task.history.result", "계약서 확인 요약 작업") {
		t.Fatalf("expected task.history result to include prior task prompt; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "계약서 확인 요약") {
		t.Fatalf("expected final reply to mention prior task, got %q", secondTurnResult.FinishMessage)
	}
}

func TestMemoryExplicitToolAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), MemoryExplicitToolAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected memory explicit tool acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	if firstTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected first turn success, got %s", firstTurnResult.TaskStatus)
	}
	if secondTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected second turn success, got %s", secondTurnResult.TaskStatus)
	}
	if countEvents(firstTurnResult.Events, "tool.memory.remember.requested")+countEvents(secondTurnResult.Events, "tool.memory.remember.requested") != 1 {
		t.Fatalf("expected exactly one memory.remember request; first events: %s second events: %s", summarizeEvents(firstTurnResult.Events), summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(firstTurnResult.Events, "tool.memory.remember.requested", "Korean") {
		t.Fatalf("expected memory.remember input to include Korean; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countEvents(firstTurnResult.Events, "tool.memory.search.requested")+countEvents(secondTurnResult.Events, "tool.memory.search.requested") != 1 {
		t.Fatalf("expected exactly one memory.search request; first events: %s second events: %s", summarizeEvents(firstTurnResult.Events), summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "Korean") {
		t.Fatalf("expected final reply to mention Korean, got %q", secondTurnResult.FinishMessage)
	}
}

func TestFailureExplanationAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), FailureExplanationAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected failure explanation acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	if firstTurnResult.TaskStatus != task.TaskStatusFailed {
		t.Fatalf("expected first turn failure, got %s", firstTurnResult.TaskStatus)
	}
	if !strings.Contains(firstTurnResult.FailureReason, "permission denied") {
		t.Fatalf("expected failure reason to mention permission denied, got %q", firstTurnResult.FailureReason)
	}
	if secondTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected second turn success, got %s", secondTurnResult.TaskStatus)
	}
	if countEvents(secondTurnResult.Events, "tool.task.history.requested") == 0 {
		t.Fatalf("expected task.history request in second turn; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.task.history.result", "failureReason") || !eventsContain(secondTurnResult.Events, "tool.task.history.result", "permission denied") {
		t.Fatalf("expected task.history result to include failure reason; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "permission denied") {
		t.Fatalf("expected final reply to mention permission denied, got %q", secondTurnResult.FinishMessage)
	}
}

func TestOneTimeScheduleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), OneTimeScheduleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected one-time schedule acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "schedule.created", `"kind":"once"`) {
		t.Fatalf("expected one-time schedule creation event; events: %s", summarizeEvents(turnResult.Events))
	}
	if len(result.TaskSchedules) != 1 {
		t.Fatalf("expected one stored schedule, got %+v", result.TaskSchedules)
	}
	taskSchedule := result.TaskSchedules[0]
	if taskSchedule.Kind != task.TaskScheduleKindOnce {
		t.Fatalf("expected one-time schedule kind, got %+v", taskSchedule)
	}
	if taskSchedule.RunAt == nil || taskSchedule.NextRunAt == nil {
		t.Fatalf("expected one-time schedule run time, got %+v", taskSchedule)
	}
}

func TestSitePrototypeAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SitePrototypeAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected site prototype acceptance scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "agent.instructions_loaded", "site-prototype") {
		t.Fatal("expected site-prototype skill to be selected")
	}
	if !eventsContain(turnResult.Events, "tool.site.app.publish.result", "publishedURL") {
		t.Fatalf("expected site publish result to include a public URL; events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.ModelContext, "site.app.create") || !strings.Contains(turnResult.ModelContext, "site.app.publish") {
		t.Fatal("expected model context to expose site app tools")
	}
}

func TestSiteEditRedeployAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SiteEditRedeployAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected site edit redeploy acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	secondTurnResult := result.TurnResults[1]
	if secondTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected second turn success, got %s", secondTurnResult.TaskStatus)
	}
	fileMutationCount := countEvents(secondTurnResult.Events, "tool.file.edit.requested") + countEvents(secondTurnResult.Events, "tool.file.write.requested") + countEvents(secondTurnResult.Events, "tool.file.patch.requested")
	if fileMutationCount == 0 {
		t.Fatalf("expected a file mutation tool in turn two; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if countEvents(secondTurnResult.Events, "tool.terminal.run.requested") == 0 {
		t.Fatalf("expected terminal.run in turn two; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if countEvents(secondTurnResult.Events, "tool.site.app.publish.requested") == 0 {
		t.Fatalf("expected site.app.publish in turn two; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "https://") {
		t.Fatalf("expected final assistant message to contain a URL, got %q", secondTurnResult.FinishMessage)
	}
}

func TestAskChoiceReplyAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AskChoiceReplyAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected ask choice reply acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turns, got %+v", result)
	}
}

func TestAskConfirmReplyAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AskConfirmReplyAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected ask confirm reply acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turns, got %+v", result)
	}
}

func TestDirectMessageSendConfirmAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), DirectMessageSendConfirmAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected direct message send confirm acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turns, got %+v", result)
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	if !eventsContain(firstTurnResult.Events, "confirmation.requested", "external_send") {
		t.Fatalf("expected confirmation request before send; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countEvents(firstTurnResult.Events, "tool.platform.message.send.requested") != 0 {
		t.Fatalf("expected no send before approval; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countEvents(secondTurnResult.Events, "tool.platform.message.send.requested") != 1 {
		t.Fatalf("expected one send after approval; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.platform.message.send.result", "virtual-platform-message-001") {
		t.Fatalf("expected send result message id observation; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "보냈습니다") {
		t.Fatalf("expected successful delivery reply, got %q", secondTurnResult.FinishMessage)
	}
}

func TestChannelPostAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ChannelPostAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected channel post acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn, got %+v", result)
	}
	turnResult := result.TurnResults[0]
	if countEvents(turnResult.Events, "tool.platform.message.send.requested") != 1 {
		t.Fatalf("expected one send request, got events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.platform.message.send.requested", `"type":"channel"`) {
		t.Fatalf("expected channel delivery target; events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.platform.message.send.requested", `"type":"directMessage"`) {
		t.Fatalf("expected no direct message target; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestPlatformMessageEditAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), PlatformMessageEditAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected platform message edit acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn, got %+v", result)
	}
	turnResult := result.TurnResults[0]
	if countEvents(turnResult.Events, "tool.platform.message.update.requested") != 1 {
		t.Fatalf("expected one message update request; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.platform.message.update.requested", `"messageID":"virtual-platform-message-001"`) {
		t.Fatalf("expected message ID in update input; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.platform.message.update.requested", `"text":"오늘 오후 6시에 전체 공지 회의가 있습니다."`) {
		t.Fatalf("expected new text in update input; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestAttachmentMaterialRead(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AttachmentMaterialReadScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected attachment material read scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.image.read.requested", `"materialID":"mattermost:file-1"`) {
		t.Fatalf("expected image.read to use materialID; events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.terminal.run.requested", "terminal.run") {
		t.Fatalf("expected attachment read not to search the workspace; events: %s", summarizeEvents(turnResult.Events))
	}
	if turnResult.UserModelImagePartCount == 0 {
		t.Fatalf("expected image.read result to reach the model as a user image part; context: %s", turnResult.ModelContext)
	}
	if len(turnResult.Attachments) != 0 {
		t.Fatalf("expected image.read result not to be reattached, got %+v", turnResult.Attachments)
	}
}

func TestAttachmentHTMLPreviewRecovery(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AttachmentHTMLPreviewRecoveryScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected attachment html preview recovery scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if eventsContain(turnResult.Events, "tool.terminal.run.requested", "terminal.run") {
		t.Fatalf("expected html attachment preview not to search the workspace; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.file.preview.result", "Virtual HTML Title") {
		t.Fatalf("expected html preview content in tool result; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestAttachmentHTMLPreviousPreviewRecovery(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AttachmentHTMLPreviousPreviewRecoveryScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected previous attachment html preview recovery scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if eventsContain(turnResult.Events, "tool.terminal.run.requested", "terminal.run") {
		t.Fatalf("expected previous html attachment preview not to search the workspace; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.file.preview.result", "Virtual HTML Title") {
		t.Fatalf("expected previous html preview content in tool result; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestAttachmentCurrentImageInput(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AttachmentCurrentImageInputScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected current image input scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if turnResult.ModelImagePartCount == 0 {
		t.Fatalf("expected current image attachment to reach model input; context: %s", turnResult.ModelContext)
	}
	if turnResult.UserModelImagePartCount == 0 {
		t.Fatalf("expected current image attachment to reach the model as a user image part; context: %s", turnResult.ModelContext)
	}
	if eventsContain(turnResult.Events, "tool.image.read.requested", "image.read") {
		t.Fatalf("expected current image input not to require image.read; events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.terminal.run.requested", "terminal.run") {
		t.Fatalf("expected current image input not to search the workspace; events: %s", summarizeEvents(turnResult.Events))
	}
	if len(turnResult.Attachments) != 0 {
		t.Fatalf("expected current image input not to be reattached, got %+v", turnResult.Attachments)
	}
}

func firstNonEmptyTestString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func truthyEnvironmentValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func toolEventCount(events []task.TaskEvent) int {
	count := 0
	for _, event := range events {
		if strings.HasPrefix(event.Name, "tool.") {
			count++
		}
	}
	return count
}

func failureEventCount(events []task.TaskEvent) int {
	count := 0
	for _, event := range events {
		normalizedName := strings.ToLower(event.Name)
		if strings.Contains(normalizedName, "fail") || strings.Contains(normalizedName, "error") {
			count++
		}
	}
	return count
}

func activeScheduleCount(taskSchedules []task.TaskSchedule) int {
	count := 0
	for _, taskSchedule := range taskSchedules {
		if taskSchedule.NextRunAt != nil {
			count++
		}
	}
	return count
}

func eventBodies(events []task.TaskEvent, name string) []string {
	bodies := []string{}
	for _, event := range events {
		if event.Name == name {
			bodies = append(bodies, event.Body)
		}
	}
	return bodies
}
