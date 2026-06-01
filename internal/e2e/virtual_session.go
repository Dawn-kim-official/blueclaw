package e2e

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/agenttest"
	"blueclaw/internal/capability"
	"blueclaw/internal/config"
	"blueclaw/internal/connectors"
	"blueclaw/internal/identity"
	"blueclaw/internal/llm"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
	"blueclaw/internal/security/actortest"
	"blueclaw/internal/skill"
	"blueclaw/internal/task"
)

type VirtualSessionScenario struct {
	Name                  string
	ProfileName           string
	ArtifactDirectoryPath string
	LanguageModel         llm.LanguageModelProvider
	SkillDirectoryPaths   []string
	Skills                []agent.SkillInstruction
	AllowedTools          []string
	CapabilityToolNames   []string
	InitialMemory         []memory.MemoryFact
	Turns                 []VirtualTurn
}

type VirtualTurn struct {
	Prompt                  string
	ActionResponses         []string
	ExpectedSelectedSkills  []string
	ExpectedToolCalls       []string
	ExpectedEvents          []string
	ExpectedToolCallCounts  map[string]int
	ExpectedEventCounts     []VirtualEventCount
	ExpectedAttachments     []string
	ExpectedWorkspaceFiles  []VirtualWorkspaceFileExpectation
	ExpectedModelContexts   []string
	ForbiddenModelContexts  []string
	ExpectedReplyFragments  []string
	ForbiddenReplyFragments []string
}

type VirtualEventCount struct {
	Name         string
	BodyFragment string
	Count        int
}

type VirtualWorkspaceFileExpectation struct {
	PathGlob           string
	ContainsFragments  []string
	ForbiddenFragments []string
}

type VirtualSessionResult struct {
	ScenarioName          string
	ArtifactDirectoryPath string
	TurnResults           []VirtualTurnResult
}

type VirtualTurnResult struct {
	TaskRunID     string
	FinishMessage string
	Attachments   []agent.FileAttachment
	Events        []task.TaskEvent
	ModelContext  string
}

type VirtualSessionHarness struct {
	scenario         VirtualSessionScenario
	artifactPath     string
	workspacePath    string
	scriptedModel    *agenttest.ScriptedLanguageModel
	taskEventService *task.TaskEventService
	memoryStore      *virtualMemoryStore
	runtime          *connectors.ConnectorRuntime
	adapter          *virtualAdapter
	history          []connectors.VisibleContextMessage
	cleanup          func()
}

func BuiltinScenario(name string, artifactDirectoryPath string) (VirtualSessionScenario, error) {
	switch strings.TrimSpace(name) {
	case "", "slides", "slides_local_multiturn_success":
		return SlidesLocalMultiturnSuccessScenario(artifactDirectoryPath), nil
	case "memory", "memory_guided_followup":
		return MemoryGuidedFollowupScenario(artifactDirectoryPath), nil
	case "tool_permission_hides_skill":
		return ToolPermissionHidesSkillScenario(artifactDirectoryPath), nil
	case "gws_disabled":
		return GWSDisabledScenario(artifactDirectoryPath), nil
	case "schedule_create_acceptance":
		return ScheduleCreateAcceptanceScenario(artifactDirectoryPath), nil
	case "site_prototype_acceptance":
		return SitePrototypeAcceptanceScenario(artifactDirectoryPath), nil
	default:
		return VirtualSessionScenario{}, fmt.Errorf("unknown virtual session scenario: %s", name)
	}
}

func RunVirtualSession(ctx context.Context, scenario VirtualSessionScenario) (VirtualSessionResult, error) {
	harness, errorValue := NewVirtualSessionHarness(scenario)
	if errorValue != nil {
		return VirtualSessionResult{}, errorValue
	}
	return harness.Run(ctx)
}

func NewVirtualSessionHarness(scenario VirtualSessionScenario) (*VirtualSessionHarness, error) {
	if strings.TrimSpace(scenario.Name) == "" {
		return nil, errors.New("scenario name is required")
	}
	artifactPath, errorValue := prepareArtifactDirectory(scenario)
	if errorValue != nil {
		return nil, errorValue
	}
	workspacePath := filepath.Join(artifactPath, "workspace")
	if errorValue := os.MkdirAll(workspacePath, 0700); errorValue != nil {
		return nil, errorValue
	}

	skillInstructions, errorValue := loadVirtualSkillInstructions(scenario, workspacePath)
	if errorValue != nil {
		return nil, errorValue
	}

	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskStepService := task.NewTaskStepService()
	taskArtifactService := task.NewTaskArtifactService()
	scriptedModel := actionScriptedLanguageModelForScenario(scenario)
	languageModel := scenario.LanguageModel
	if scriptedModel != nil {
		languageModel = scriptedModel
	}
	if languageModel == nil {
		return nil, errors.New("virtual session requires a live language model or explicit scripted model responses")
	}
	agentKernel := agent.NewAgentKernel(taskRunService, taskStepService)
	agentKernel.UseTaskArtifactService(taskArtifactService)
	agentKernel.UseLanguageModelProvider(languageModel)
	agentKernel.UseTurnOptions(agent.TurnOptions{MaxIterationCount: 20, MaxToolCallCount: 16, MaxElapsedSecond: 120})
	agentKernel.UseInstructionBundleLoader(func() agent.InstructionBundle {
		return agent.InstructionBundle{Skills: append([]agent.SkillInstruction{}, skillInstructions...)}
	})
	agentKernel.UseSkillRetriever(agent.NewEmbeddingSkillRetriever(virtualSkillEmbeddingProvider{}, ""))

	identityService := identity.NewIdentityService(testPolicyProjection())
	runtime := connectors.NewConnectorRuntime(identityService, agentKernel, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	adapter := &virtualAdapter{}
	runtime.RegisterAdapter(adapter)
	runtime.UseWorkspaceID("e2e")
	runtime.UseWorkspaceRootPath(workspacePath)
	runtime.UseAllowedToolNames(allowedToolsOrDefault(scenario.AllowedTools))
	terminalService := security.NewTerminalSessionService(terminalConfiguration(workspacePath))
	runtime.UseTerminalService(terminalService)
	runtime.UseWorkspaceActorFactory(actortest.NewDirectWorkspaceActorFactory(terminalService))
	runtime.UseTaskRunService(taskRunService)
	runtime.UseTaskScheduleRepository(&virtualTaskScheduleRepository{})
	cleanup := func() {}
	if len(scenario.CapabilityToolNames) > 0 {
		capabilityClient, capabilityCleanup := startVirtualCapabilityServer(scenario.CapabilityToolNames)
		runtime.UseCapabilityTools(capabilityClient, scenario.CapabilityToolNames)
		cleanup = capabilityCleanup
	}

	memoryStore := newVirtualMemoryStore(scenario.InitialMemory)
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(memoryStore)
	runtime.UseMemoryService(memoryService)
	runtime.UseGraphitiIngestionRouter(memory.NewGraphitiIngestionRouter(nil, "e2e"))

	return &VirtualSessionHarness{
		scenario:         scenario,
		artifactPath:     artifactPath,
		workspacePath:    workspacePath,
		scriptedModel:    scriptedModel,
		taskEventService: taskEventService,
		memoryStore:      memoryStore,
		runtime:          runtime,
		adapter:          adapter,
		cleanup:          cleanup,
	}, nil
}

type virtualSkillEmbeddingProvider struct{}

func (provider virtualSkillEmbeddingProvider) GenerateEmbedding(_ context.Context, input string) ([]float32, error) {
	normalizedInput := strings.ToLower(input)
	return []float32{
		virtualSkillEmbeddingValue(normalizedInput, []string{"피피티", "pptx", "slides", "presentation", "파워포인트", "발표자료"}),
		virtualSkillEmbeddingValue(normalizedInput, []string{"schedule", "scheduled", "cron", "remind", "reminder", "repeat", "예약", "알림", "리마인드", "마다", "분마다", "한 번씩", "10번"}),
		virtualSkillEmbeddingValue(normalizedInput, []string{"website", "web app", "site", "prototype", "deploy", "웹사이트", "사이트", "프로토타입", "배포"}),
	}, nil
}

func virtualSkillEmbeddingValue(input string, keywords []string) float32 {
	for _, keyword := range keywords {
		if strings.Contains(input, keyword) {
			return 1
		}
	}
	return 0
}

func loadVirtualSkillInstructions(scenario VirtualSessionScenario, workspacePath string) ([]agent.SkillInstruction, error) {
	skillInstructions := append([]agent.SkillInstruction{}, scenario.Skills...)
	for _, sourceDirectoryPath := range scenario.SkillDirectoryPaths {
		trimmedSourceDirectoryPath := strings.TrimSpace(sourceDirectoryPath)
		if trimmedSourceDirectoryPath == "" {
			continue
		}
		destinationDirectoryPath := filepath.Join(workspacePath, "skills", filepath.Base(trimmedSourceDirectoryPath))
		if errorValue := copyDirectory(trimmedSourceDirectoryPath, destinationDirectoryPath); errorValue != nil {
			return nil, errorValue
		}
		skillBundle, errorValue := (skill.SkillLoader{}).LoadSkillBundle(destinationDirectoryPath)
		if errorValue != nil {
			return nil, errorValue
		}
		skillInstructions = append(skillInstructions, skillInstructionFromBundle(skillBundle))
	}
	return skillInstructions, nil
}

func skillInstructionFromBundle(skillBundle skill.SkillBundle) agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:            skillBundle.Name,
		Description:     skillBundle.Description,
		Category:        skillBundle.Category,
		Tags:            append([]string{}, skillBundle.Tags...),
		Prompt:          skillBundle.Instruction,
		Activation:      agent.SkillActivation(skillBundle.Activation),
		Completion:      agent.SkillCompletion(skillBundle.Completion),
		Quality:         agent.SkillQuality(skillBundle.Quality),
		AllowedTools:    append([]string{}, skillBundle.AllowedTools...),
		AllowedProfiles: append([]string{}, skillBundle.AllowedProfiles...),
		TriggerHints:    append([]string{}, skillBundle.TriggerHints...),
		References:      append([]string{}, skillBundle.References...),
		Scripts:         append([]string{}, skillBundle.Scripts...),
		Assets:          append([]string{}, skillBundle.Assets...),
		Source: agent.InstructionSource{
			Path:      filepath.Join(skillBundle.DirectoryPath, "SKILL.md"),
			SkillName: skillBundle.Name,
			ByteSize:  fileSize(filepath.Join(skillBundle.DirectoryPath, "SKILL.md")),
			SHA256:    fileSHA256(filepath.Join(skillBundle.DirectoryPath, "SKILL.md")),
		},
	}
}

func copyDirectory(sourcePath string, destinationPath string) error {
	sourceInformation, errorValue := os.Stat(sourcePath)
	if errorValue != nil {
		return errorValue
	}
	if !sourceInformation.IsDir() {
		return errors.New("skill source path is not a directory: " + sourcePath)
	}
	return filepath.WalkDir(sourcePath, func(path string, directoryEntry os.DirEntry, walkError error) error {
		if walkError != nil {
			return walkError
		}
		relativePath, errorValue := filepath.Rel(sourcePath, path)
		if errorValue != nil {
			return errorValue
		}
		destination := filepath.Join(destinationPath, relativePath)
		if directoryEntry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		content, errorValue := os.ReadFile(path)
		if errorValue != nil {
			return errorValue
		}
		return os.WriteFile(destination, content, 0600)
	})
}

func fileSize(path string) int {
	information, errorValue := os.Stat(path)
	if errorValue != nil {
		return 0
	}
	return int(information.Size())
}

func fileSHA256(path string) string {
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return ""
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func startVirtualCapabilityServer(toolNames []string) (capability.Client, func()) {
	toolNameByName := map[string]bool{}
	for _, toolName := range toolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName != "" {
			toolNameByName[trimmedToolName] = true
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		toolName := strings.TrimPrefix(request.URL.Path, "/v1/tools/")
		toolName = strings.TrimSuffix(toolName, "/invoke")
		if !toolNameByName[toolName] {
			http.Error(responseWriter, "unknown virtual capability tool", http.StatusNotFound)
			return
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(virtualCapabilityResponse(toolName)))
	}))
	return capability.Client{Endpoint: server.URL, HTTPClient: server.Client()}, server.Close
}

func virtualCapabilityResponse(toolName string) string {
	switch toolName {
	case "site.app.create":
		return `{"status":"ok","result":{"siteID":"site-1","slug":"demo","workspacePath":"home/sites/site-1","sourceWorkspacePath":"home/sites/site-1","appWorkspacePath":"home/sites/site-1/app"}}`
	case "site.app.publish":
		return `{"status":"ok","result":{"siteID":"site-1","status":"published","publishedURL":"https://demo.device.intern.kim"}}`
	case "site.app.status":
		return `{"status":"ok","result":{"siteID":"site-1","slug":"demo","status":"draft","workspacePath":"home/sites/site-1","sourceWorkspacePath":"home/sites/site-1","appWorkspacePath":"home/sites/site-1/app"}}`
	case "site.app.logs":
		return `{"status":"ok","result":{"logs":[]}}`
	default:
		return `{"status":"ok","result":{"toolName":` + quote(toolName) + `,"ok":true}}`
	}
}

func (harness *VirtualSessionHarness) Run(ctx context.Context) (VirtualSessionResult, error) {
	if harness.cleanup != nil {
		defer harness.cleanup()
	}
	result := VirtualSessionResult{
		ScenarioName:          harness.scenario.Name,
		ArtifactDirectoryPath: harness.artifactPath,
	}
	for index, virtualTurn := range harness.scenario.Turns {
		if harness.scriptedModel != nil {
			harness.scriptedModel.EnqueueActionResponses(virtualTurn.ActionResponses...)
		}
		turnResult, errorValue := harness.runTurn(ctx, index, virtualTurn)
		if errorValue != nil {
			return result, errorValue
		}
		if errorValue := assertTurnResult(harness.workspacePath, virtualTurn, turnResult); errorValue != nil {
			return result, fmt.Errorf("%s turn %d: %w", harness.scenario.Name, index+1, errorValue)
		}
		result.TurnResults = append(result.TurnResults, turnResult)
		harness.rememberTurn(virtualTurn, turnResult)
	}
	return result, nil
}

func actionScriptedLanguageModelForScenario(scenario VirtualSessionScenario) *agenttest.ScriptedLanguageModel {
	for _, virtualTurn := range scenario.Turns {
		if len(virtualTurn.ActionResponses) > 0 {
			return agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{ProviderName: "virtual", ModelName: "scripted"})
		}
	}
	return nil
}

func (harness *VirtualSessionHarness) runTurn(ctx context.Context, index int, virtualTurn VirtualTurn) (VirtualTurnResult, error) {
	modelRequestStartIndex := 0
	if harness.scriptedModel != nil {
		modelRequestStartIndex = harness.scriptedModel.RequestCount()
	}
	event := connectors.PlatformInboundEvent{
		Platform:       "virtual",
		Source:         "e2e",
		ConversationID: "virtual-conversation-1",
		MessageID:      fmt.Sprintf("virtual-message-%03d", index+1),
		SenderID:       "user-1",
		ReplyTargetID:  fmt.Sprintf("virtual-reply-%03d", index+1),
		Prompt:         virtualTurn.Prompt,
		Context: connectors.VisibleContext{
			Messages: append([]connectors.VisibleContextMessage{}, harness.history...),
			Sender: connectors.VisibleContextSender{
				Platform:    "virtual",
				SenderID:    "user-1",
				Handle:      "dongha",
				Email:       "dongha@example.com",
				Name:        "동하",
				CallingName: "동하 님",
			},
		},
		RawReceivedAt: time.Now().UTC(),
	}
	runtimeResult, errorValue := harness.runtime.HandleInboundEvent(ctx, harness.adapter, event)
	if errorValue != nil {
		return VirtualTurnResult{}, errorValue
	}
	if strings.TrimSpace(runtimeResult.TaskRunID) == "" {
		return VirtualTurnResult{}, errors.New("virtual turn did not create a task run")
	}
	outboundReply, isFound := harness.adapter.FindReply(runtimeResult.ReplyDispatchID)
	if !isFound {
		events := harness.taskEventService.ListTaskEvent(runtimeResult.TaskRunID)
		return VirtualTurnResult{}, fmt.Errorf("virtual turn did not dispatch a reply; events: %s", summarizeEvents(events))
	}
	return VirtualTurnResult{
		TaskRunID:     runtimeResult.TaskRunID,
		FinishMessage: outboundReply.Message,
		Attachments:   outboundReply.Attachments,
		Events:        harness.taskEventService.ListTaskEvent(runtimeResult.TaskRunID),
		ModelContext:  harness.modelContextSince(modelRequestStartIndex),
	}, nil
}

func (harness *VirtualSessionHarness) modelContextSince(startIndex int) string {
	if harness.scriptedModel == nil {
		return ""
	}
	parts := []string{}
	for _, request := range harness.scriptedModel.RequestsSince(startIndex) {
		if request.StructuredOutputSchema.Name != "blueclaw_agent_turn_action" {
			continue
		}
		for _, message := range request.Messages {
			parts = append(parts, message.Role+": "+message.Content)
		}
		parts = append(parts, request.StructuredOutputSchema.Document)
	}
	return strings.Join(parts, "\n")
}

func (harness *VirtualSessionHarness) rememberTurn(virtualTurn VirtualTurn, turnResult VirtualTurnResult) {
	harness.history = append(harness.history,
		connectors.VisibleContextMessage{Speaker: "user", SpeakerCallingName: "동하 님", SpeakerHandle: "dongha", Text: virtualTurn.Prompt},
		connectors.VisibleContextMessage{Speaker: "assistant", SpeakerCallingName: "김인턴", SpeakerHandle: "internkim", Text: turnResult.FinishMessage},
	)
}

func assertTurnResult(workspacePath string, virtualTurn VirtualTurn, turnResult VirtualTurnResult) error {
	for _, skillName := range virtualTurn.ExpectedSelectedSkills {
		if !eventsContain(turnResult.Events, "agent.instructions_loaded", skillName) {
			return fmt.Errorf("expected selected skill %q; events: %s", skillName, summarizeEvents(turnResult.Events))
		}
	}
	for _, toolName := range virtualTurn.ExpectedToolCalls {
		if !eventsContain(turnResult.Events, "tool."+toolName+".requested", toolName) {
			return fmt.Errorf("expected requested tool %q; events: %s", toolName, summarizeEvents(turnResult.Events))
		}
	}
	for _, eventName := range virtualTurn.ExpectedEvents {
		if !eventsContain(turnResult.Events, eventName, "") {
			return fmt.Errorf("expected event %q; events: %s", eventName, summarizeEvents(turnResult.Events))
		}
	}
	for toolName, expectedCount := range virtualTurn.ExpectedToolCallCounts {
		actualCount := countEvents(turnResult.Events, "tool."+toolName+".requested")
		if actualCount != expectedCount {
			return fmt.Errorf("expected %d requested %s calls, got %d; events: %s", expectedCount, toolName, actualCount, summarizeEvents(turnResult.Events))
		}
	}
	for _, expectedEventCount := range virtualTurn.ExpectedEventCounts {
		actualCount := countEventsWithFragment(turnResult.Events, expectedEventCount.Name, expectedEventCount.BodyFragment)
		if actualCount != expectedEventCount.Count {
			return fmt.Errorf("expected %d events %s containing %q, got %d; events: %s", expectedEventCount.Count, expectedEventCount.Name, expectedEventCount.BodyFragment, actualCount, summarizeEvents(turnResult.Events))
		}
	}
	for _, suffix := range virtualTurn.ExpectedAttachments {
		attachment, isFound := findAttachmentWithSuffix(turnResult.Attachments, suffix)
		if !isFound {
			return fmt.Errorf("expected attachment suffix %q, got %+v; events: %s", suffix, turnResult.Attachments, summarizeEvents(turnResult.Events))
		}
		if errorValue := validateAttachmentContent(workspacePath, attachment, suffix); errorValue != nil {
			return errorValue
		}
	}
	for _, expectedWorkspaceFile := range virtualTurn.ExpectedWorkspaceFiles {
		if errorValue := validateExpectedWorkspaceFile(workspacePath, expectedWorkspaceFile); errorValue != nil {
			return errorValue
		}
	}
	for _, fragment := range virtualTurn.ExpectedModelContexts {
		if !strings.Contains(turnResult.ModelContext, fragment) {
			return fmt.Errorf("expected model context fragment %q", fragment)
		}
	}
	for _, fragment := range virtualTurn.ForbiddenModelContexts {
		if strings.Contains(turnResult.ModelContext, fragment) {
			return fmt.Errorf("forbidden model context fragment %q found", fragment)
		}
	}
	for _, fragment := range virtualTurn.ExpectedReplyFragments {
		if !strings.Contains(turnResult.FinishMessage, fragment) {
			return fmt.Errorf("expected reply fragment %q in %q", fragment, turnResult.FinishMessage)
		}
	}
	for _, fragment := range virtualTurn.ForbiddenReplyFragments {
		if strings.Contains(turnResult.FinishMessage, fragment) {
			return fmt.Errorf("forbidden reply fragment %q found in %q", fragment, turnResult.FinishMessage)
		}
	}
	return nil
}

func validateExpectedWorkspaceFile(workspacePath string, expectation VirtualWorkspaceFileExpectation) error {
	pattern := filepath.Join(workspacePath, expectation.PathGlob)
	matches, errorValue := filepath.Glob(pattern)
	if errorValue != nil {
		return errorValue
	}
	if len(matches) == 0 {
		return fmt.Errorf("expected workspace file matching %q", expectation.PathGlob)
	}
	sort.Strings(matches)
	content, errorValue := os.ReadFile(matches[len(matches)-1])
	if errorValue != nil {
		return errorValue
	}
	document := string(content)
	for _, fragment := range expectation.ContainsFragments {
		if !strings.Contains(document, fragment) {
			return fmt.Errorf("expected %s to contain %q", matches[len(matches)-1], fragment)
		}
	}
	for _, fragment := range expectation.ForbiddenFragments {
		if strings.Contains(document, fragment) {
			return fmt.Errorf("expected %s not to contain %q", matches[len(matches)-1], fragment)
		}
	}
	return nil
}

func summarizeEvents(events []task.TaskEvent) string {
	parts := []string{}
	for _, event := range events {
		body := event.Body
		if len(body) > 160 {
			body = body[:160] + "..."
		}
		parts = append(parts, event.Name+"="+body)
	}
	return strings.Join(parts, " | ")
}

func eventsContain(events []task.TaskEvent, name string, bodyFragment string) bool {
	for _, event := range events {
		if event.Name == name && strings.Contains(event.Body, bodyFragment) {
			return true
		}
	}
	return false
}

func countEvents(events []task.TaskEvent, name string) int {
	count := 0
	for _, event := range events {
		if event.Name == name {
			count++
		}
	}
	return count
}

func countEventsWithFragment(events []task.TaskEvent, name string, bodyFragment string) int {
	count := 0
	for _, event := range events {
		if event.Name != name {
			continue
		}
		if bodyFragment != "" && !strings.Contains(event.Body, bodyFragment) {
			continue
		}
		count++
	}
	return count
}

func findAttachmentWithSuffix(attachments []agent.FileAttachment, suffix string) (agent.FileAttachment, bool) {
	for _, attachment := range attachments {
		if strings.HasSuffix(attachment.Filename, suffix) || strings.HasSuffix(attachment.DevicePath, suffix) {
			return attachment, true
		}
	}
	return agent.FileAttachment{}, false
}

func validateAttachmentContent(workspacePath string, attachment agent.FileAttachment, suffix string) error {
	path := localAttachmentPath(workspacePath, attachment)
	switch suffix {
	case ".pptx":
		return validatePPTXAttachment(path, attachment)
	case ".pdf":
		return validateFilePrefix(path, "%PDF")
	case ".html":
		return validateFileContains(path, "<html")
	case "-notes.txt":
		return validateNonEmptyFile(path)
	default:
		return validateNonEmptyFile(path)
	}
}

func localAttachmentPath(workspacePath string, attachment agent.FileAttachment) string {
	devicePath := strings.TrimSpace(attachment.DevicePath)
	if devicePath == "/workspace" {
		return workspacePath
	}
	if strings.HasPrefix(devicePath, "/workspace/") {
		return filepath.Join(workspacePath, strings.TrimPrefix(devicePath, "/workspace/"))
	}
	return devicePath
}

func validatePPTXAttachment(path string, attachment agent.FileAttachment) error {
	reader, errorValue := zip.OpenReader(path)
	if errorValue != nil {
		return fmt.Errorf("attachment %s is not a valid pptx zip: %w", attachment.DevicePath, errorValue)
	}
	defer reader.Close()
	requiredEntries := map[string]bool{
		"[Content_Types].xml":             false,
		"ppt/presentation.xml":            false,
		"ppt/slides/slide1.xml":           false,
		"ppt/_rels/presentation.xml.rels": false,
	}
	for _, file := range reader.File {
		if _, isRequired := requiredEntries[file.Name]; isRequired {
			requiredEntries[file.Name] = true
		}
	}
	for name, isFound := range requiredEntries {
		if !isFound {
			return fmt.Errorf("attachment %s is missing pptx entry %s", attachment.DevicePath, name)
		}
	}
	return nil
}

func validateFilePrefix(path string, prefix string) error {
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return errorValue
	}
	if !strings.HasPrefix(string(content), prefix) {
		return fmt.Errorf("attachment %s does not start with %q", path, prefix)
	}
	return nil
}

func validateFileContains(path string, fragment string) error {
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return errorValue
	}
	if !strings.Contains(strings.ToLower(string(content)), strings.ToLower(fragment)) {
		return fmt.Errorf("attachment %s does not contain %q", path, fragment)
	}
	return nil
}

func validateNonEmptyFile(path string) error {
	information, errorValue := os.Stat(path)
	if errorValue != nil {
		return errorValue
	}
	if information.Size() <= 0 {
		return fmt.Errorf("attachment %s is empty", path)
	}
	return nil
}

func prepareArtifactDirectory(scenario VirtualSessionScenario) (string, error) {
	rootPath := strings.TrimSpace(scenario.ArtifactDirectoryPath)
	if rootPath == "" {
		return os.MkdirTemp("", "blueclaw-e2e-*")
	}
	absoluteRootPath, errorValue := filepath.Abs(rootPath)
	if errorValue != nil {
		return "", errorValue
	}
	artifactPath := filepath.Join(absoluteRootPath, scenario.Name+"-"+time.Now().UTC().Format("20060102T150405.000000000"))
	return artifactPath, os.MkdirAll(artifactPath, 0700)
}

func allowedToolsOrDefault(allowedTools []string) []string {
	if len(allowedTools) > 0 {
		return append([]string{}, allowedTools...)
	}
	return []string{"conversation.history", "memory.search", "terminal.run", "terminal.session", "browser_handoff.openURL", "ask.confirm", "ask.choice", "ask.input", "file.read", "file.write", "file.edit", "file.patch", "file.promote", "file.attach"}
}

func terminalConfiguration(workspacePath string) config.TerminalConfiguration {
	return config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspacePath,
		DeniedExecutableNames: []string{"sudo", "su", "mount", "umount", "reboot", "shutdown", "systemctl"},
		DeniedPathPrefixes:    []string{"/etc", "/private/etc", "/System", "/Library"},
		TimeoutSecond:         120,
		OutputMaxBytes:        32768,
		SessionMaxCount:       2,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
	}
}

func testPolicyProjection() policy.PolicyProjection {
	policyDocument := policy.PolicyDocument{
		People: []policy.PersonPolicy{{
			PersonID:          "person-1",
			DisplayName:       "동하",
			Emails:            []string{"dongha@example.com"},
			Circles:           []string{"staff"},
			SecurityLevelRank: 0,
			GrantedClasses:    []string{},
		}},
		Circles: []policy.CirclePolicy{{
			CircleID:               "staff",
			DisplayName:            "Staff",
			WorkspaceDirectoryPath: "/workspace/circles/staff",
		}},
		Channels: []policy.ChannelPolicy{{
			Platform:                 "virtual",
			ExternalConversationID:   "virtual-conversation-1",
			ConversationType:         "test",
			DisplayName:              "Virtual Session",
			DefaultSecurityLevelRank: 0,
			DefaultRequiredClasses:   []string{},
			IsCollectEnabled:         true,
			IsReplyEnabled:           true,
		}},
		Retention: policy.RetentionPolicy{RawEventDays: 30},
	}
	return policy.PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(policyDocument)
}

type virtualAdapter struct {
	mutex   sync.Mutex
	replies map[string]connectors.OutboundReply
}

func (adapter *virtualAdapter) Name() string { return "virtual" }

func (adapter *virtualAdapter) ParseHTTPEvent(context.Context, *http.Request) (connectors.HTTPParseResult, error) {
	return connectors.HTTPParseResult{}, errors.New("virtual adapter does not parse http")
}

func (adapter *virtualAdapter) ParseRealtimeEvent(context.Context, []byte, string) (connectors.PlatformInboundEvent, bool, error) {
	return connectors.PlatformInboundEvent{}, false, errors.New("virtual adapter does not parse realtime")
}

func (adapter *virtualAdapter) ResolveIdentity(context.Context, string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{
		Platform:       "virtual",
		ExternalUserID: "user-1",
		Email:          "dongha@example.com",
		DisplayName:    "동하",
	}, nil
}

func (adapter *virtualAdapter) StartProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter *virtualAdapter) StopProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter *virtualAdapter) SendReply(_ context.Context, _ connectors.ReplyTarget, reply connectors.OutboundReply) (string, error) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	if adapter.replies == nil {
		adapter.replies = map[string]connectors.OutboundReply{}
	}
	dispatchID := fmt.Sprintf("virtual-dispatch-%03d", len(adapter.replies)+1)
	adapter.replies[dispatchID] = reply
	return dispatchID, nil
}

func (adapter *virtualAdapter) FindReply(dispatchID string) (connectors.OutboundReply, bool) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	reply, isFound := adapter.replies[dispatchID]
	return reply, isFound
}

func (adapter *virtualAdapter) FetchHistory(context.Context, string, int) (connectors.VisibleContext, error) {
	return connectors.VisibleContext{}, nil
}

func (adapter *virtualAdapter) NotInvitedReply() string {
	return "not invited"
}

type virtualMemoryStore struct {
	mutex sync.Mutex
	facts []memory.MemoryFact
}

type virtualTaskScheduleRepository struct {
	mutex         sync.Mutex
	taskSchedules []task.TaskSchedule
}

func (repository *virtualTaskScheduleRepository) UpsertTaskSchedule(taskSchedule task.TaskSchedule) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.taskSchedules = append(repository.taskSchedules, taskSchedule)
	return nil
}

func (repository *virtualTaskScheduleRepository) ClaimDueTaskSchedules(int, time.Duration, time.Time, string) ([]task.TaskSchedule, error) {
	return nil, nil
}

func (repository *virtualTaskScheduleRepository) MarkTaskScheduleSucceeded(task.TaskSchedule) error {
	return nil
}

func (repository *virtualTaskScheduleRepository) MarkTaskScheduleFailed(task.TaskSchedule, string, time.Time) error {
	return nil
}

func (repository *virtualTaskScheduleRepository) CancelTaskSchedules(request task.TaskScheduleCancelRequest) (task.TaskScheduleCancelResult, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	cancelledTaskSchedules := []task.TaskSchedule{}
	for index, taskSchedule := range repository.taskSchedules {
		if taskSchedule.CreatorPersonID != request.RequesterPersonID || taskSchedule.NextRunAt == nil {
			continue
		}
		repository.taskSchedules[index].ExpiresAt = &request.CancelledAt
		repository.taskSchedules[index].NextRunAt = nil
		cancelledTaskSchedules = append(cancelledTaskSchedules, repository.taskSchedules[index])
	}
	return task.TaskScheduleCancelResult{TaskSchedules: cancelledTaskSchedules}, nil
}

func newVirtualMemoryStore(initialFacts []memory.MemoryFact) *virtualMemoryStore {
	return &virtualMemoryStore{facts: append([]memory.MemoryFact{}, initialFacts...)}
}

func (store *virtualMemoryStore) AddEpisode(_ context.Context, episode memory.MemoryEpisode) (memory.MemoryIngestionResult, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	validAt := episode.OccurredAt
	if validAt.IsZero() {
		validAt = time.Now().UTC()
	}
	for _, namespace := range episode.Namespaces {
		store.facts = append(store.facts, memory.MemoryFact{
			FactID:            episode.EpisodeID + ":" + namespace.NamespaceID,
			ScopeType:         namespace.ScopeType,
			NamespaceID:       namespace.NamespaceID,
			Content:           episode.Prompt,
			Score:             0.5,
			SourceEpisodeID:   episode.EpisodeID,
			SourceKind:        memory.MemorySourceKindFact,
			ValidAt:           validAt,
			SecurityLevelRank: namespace.SecurityLevelRank,
			RequiredClasses:   append([]string{}, namespace.RequiredClasses...),
		})
	}
	return memory.MemoryIngestionResult{EpisodeID: episode.EpisodeID, NamespaceCount: len(episode.Namespaces)}, nil
}

func (store *virtualMemoryStore) SearchFacts(_ context.Context, request memory.MemorySearchRequest) ([]memory.MemoryFact, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	namespaceByID := map[string]bool{}
	for _, namespace := range request.Namespaces {
		namespaceByID[namespace.NamespaceID] = true
	}
	candidates := []memory.MemoryFact{}
	for _, fact := range store.facts {
		if !namespaceByID[fact.NamespaceID] || request.ReaderSecurityLevelRank < fact.SecurityLevelRank {
			continue
		}
		candidates = append(candidates, fact)
	}
	sort.SliceStable(candidates, func(leftIndex int, rightIndex int) bool {
		leftScore := virtualRelevanceScore(candidates[leftIndex], request.Query)
		rightScore := virtualRelevanceScore(candidates[rightIndex], request.Query)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return candidates[leftIndex].ValidAt.After(candidates[rightIndex].ValidAt)
	})
	if request.Limit > 0 && len(candidates) > request.Limit {
		return append([]memory.MemoryFact{}, candidates[:request.Limit]...), nil
	}
	return append([]memory.MemoryFact{}, candidates...), nil
}

func virtualRelevanceScore(fact memory.MemoryFact, query string) float64 {
	score := fact.Score
	normalizedContent := strings.ToLower(fact.Content)
	for _, queryTerm := range strings.Fields(strings.ToLower(strings.TrimSpace(query))) {
		if strings.Contains(normalizedContent, queryTerm) {
			score += 0.25
		}
	}
	return score
}

func actionFinishMessage(reply string, evidence ...string) string {
	evidenceDocuments := []string{}
	for _, value := range evidence {
		parts := strings.Split(value, ":")
		if len(parts) != 3 {
			continue
		}
		evidenceDocuments = append(evidenceDocuments, `{"observationID":`+quote(parts[0])+`,"toolName":`+quote(parts[1])+`,"attachmentIndex":`+parts[2]+`}`)
	}
	return `{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[` + strings.Join(evidenceDocuments, ",") + `],"finishMessage":` + quote(reply) + `}`
}

func actionNoToolFallbackFinishMessage(reply string) string {
	return `{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"failureResolution":"no_tool_fallback","finishMessage":` + quote(reply) + `}`
}

func actionCallTool(toolName string, input string) string {
	return `{"action":"continue","toolName":` + quote(toolName) + `,"toolInput":` + input + `}`
}

func quote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
