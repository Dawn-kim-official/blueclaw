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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/config"
	"blueclaw/internal/connectors"
	"blueclaw/internal/identity"
	"blueclaw/internal/llm"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
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
	InitialMemory         []memory.MemoryFact
	Turns                 []VirtualTurn
}

type VirtualTurn struct {
	Prompt                  string
	ModelResponses          []string
	ExpectedSelectedSkills  []string
	ExpectedToolCalls       []string
	ExpectedAttachments     []string
	ExpectedReplyFragments  []string
	ForbiddenReplyFragments []string
}

type VirtualSessionResult struct {
	ScenarioName          string
	ArtifactDirectoryPath string
	TurnResults           []VirtualTurnResult
}

type VirtualTurnResult struct {
	TaskRunID   string
	FinalReply  string
	Attachments []agent.FileAttachment
	Events      []task.TaskEvent
}

type VirtualSessionHarness struct {
	scenario         VirtualSessionScenario
	artifactPath     string
	workspacePath    string
	scriptedModel    *scriptedLanguageModel
	taskEventService *task.TaskEventService
	memoryStore      *virtualMemoryStore
	runtime          *connectors.ConnectorRuntime
	adapter          *virtualAdapter
	history          []connectors.VisibleContextMessage
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
	scriptedModel := scriptedLanguageModelForScenario(scenario)
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
	agentKernel.UseTurnOptions(agent.TurnOptions{MaxIterations: 16, MaxToolCalls: 12, WallClockSecond: 30})
	agentKernel.UseInstructionBundleLoader(func() agent.InstructionBundle {
		return agent.InstructionBundle{Skills: append([]agent.SkillInstruction{}, skillInstructions...)}
	})

	identityService := identity.NewIdentityService(testPolicyProjection())
	runtime := connectors.NewConnectorRuntime(identityService, agentKernel, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	adapter := &virtualAdapter{}
	runtime.RegisterAdapter(adapter)
	runtime.UseWorkspaceID("e2e")
	runtime.UseWorkspaceRootPath(workspacePath)
	runtime.UseAllowedToolNames(allowedToolsOrDefault(scenario.AllowedTools))
	runtime.UseTerminalService(security.NewTerminalSessionService(terminalConfiguration(workspacePath)))

	memoryStore := newVirtualMemoryStore(scenario.InitialMemory)
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(memoryStore)
	runtime.UseMemoryService(memoryService)
	runtime.UseMemoryScopeRouter(memory.NewMemoryScopeRouter(nil, "e2e"))

	return &VirtualSessionHarness{
		scenario:         scenario,
		artifactPath:     artifactPath,
		workspacePath:    workspacePath,
		scriptedModel:    scriptedModel,
		taskEventService: taskEventService,
		memoryStore:      memoryStore,
		runtime:          runtime,
		adapter:          adapter,
	}, nil
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
		skillInstructions = append(skillInstructions, skillInstructionFromBundle(skillBundle, workspacePath))
	}
	return skillInstructions, nil
}

func skillInstructionFromBundle(skillBundle skill.SkillBundle, workspacePath string) agent.SkillInstruction {
	instruction := strings.ReplaceAll(skillBundle.Instruction, "/workspace", workspacePath)
	return agent.SkillInstruction{
		Name:            skillBundle.Name,
		Description:     skillBundle.Description,
		Category:        skillBundle.Category,
		Tags:            append([]string{}, skillBundle.Tags...),
		Prompt:          instruction,
		Activation:      agent.SkillActivation(skillBundle.Activation),
		Completion:      agent.SkillCompletion(skillBundle.Completion),
		RequiredTools:   append([]string{}, skillBundle.RequiredTools...),
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

func (harness *VirtualSessionHarness) Run(ctx context.Context) (VirtualSessionResult, error) {
	result := VirtualSessionResult{
		ScenarioName:          harness.scenario.Name,
		ArtifactDirectoryPath: harness.artifactPath,
	}
	for index, virtualTurn := range harness.scenario.Turns {
		if harness.scriptedModel != nil {
			harness.scriptedModel.Enqueue(virtualTurn.ModelResponses...)
		}
		turnResult, errorValue := harness.runTurn(ctx, index, virtualTurn)
		if errorValue != nil {
			return result, errorValue
		}
		if errorValue := assertTurnResult(virtualTurn, turnResult); errorValue != nil {
			return result, fmt.Errorf("%s turn %d: %w", harness.scenario.Name, index+1, errorValue)
		}
		result.TurnResults = append(result.TurnResults, turnResult)
		harness.rememberTurn(virtualTurn, turnResult)
	}
	return result, nil
}

func scriptedLanguageModelForScenario(scenario VirtualSessionScenario) *scriptedLanguageModel {
	for _, virtualTurn := range scenario.Turns {
		if len(virtualTurn.ModelResponses) > 0 {
			return &scriptedLanguageModel{}
		}
	}
	return nil
}

func (harness *VirtualSessionHarness) runTurn(ctx context.Context, index int, virtualTurn VirtualTurn) (VirtualTurnResult, error) {
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
				Name:        "샘플",
				CallingName: "샘플 님",
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
		return VirtualTurnResult{}, errors.New("virtual turn did not dispatch a reply")
	}
	return VirtualTurnResult{
		TaskRunID:   runtimeResult.TaskRunID,
		FinalReply:  outboundReply.Message,
		Attachments: outboundReply.Attachments,
		Events:      harness.taskEventService.ListTaskEvent(runtimeResult.TaskRunID),
	}, nil
}

func (harness *VirtualSessionHarness) rememberTurn(virtualTurn VirtualTurn, turnResult VirtualTurnResult) {
	harness.history = append(harness.history,
		connectors.VisibleContextMessage{Speaker: "user", SpeakerCallingName: "샘플 님", SpeakerHandle: "dongha", Text: virtualTurn.Prompt},
		connectors.VisibleContextMessage{Speaker: "assistant", SpeakerCallingName: "김인턴", SpeakerHandle: "internkim", Text: turnResult.FinalReply},
	)
}

func assertTurnResult(virtualTurn VirtualTurn, turnResult VirtualTurnResult) error {
	for _, skillName := range virtualTurn.ExpectedSelectedSkills {
		if !eventsContain(turnResult.Events, "agent.instructions_loaded", skillName) {
			return fmt.Errorf("expected selected skill %q", skillName)
		}
	}
	for _, toolName := range virtualTurn.ExpectedToolCalls {
		if !eventsContain(turnResult.Events, "tool."+toolName+".requested", toolName) {
			return fmt.Errorf("expected requested tool %q; events: %s", toolName, summarizeEvents(turnResult.Events))
		}
	}
	for _, suffix := range virtualTurn.ExpectedAttachments {
		attachment, isFound := findAttachmentWithSuffix(turnResult.Attachments, suffix)
		if !isFound {
			return fmt.Errorf("expected attachment suffix %q, got %+v; events: %s", suffix, turnResult.Attachments, summarizeEvents(turnResult.Events))
		}
		if errorValue := validateAttachmentContent(attachment, suffix); errorValue != nil {
			return errorValue
		}
	}
	for _, fragment := range virtualTurn.ExpectedReplyFragments {
		if !strings.Contains(turnResult.FinalReply, fragment) {
			return fmt.Errorf("expected reply fragment %q in %q", fragment, turnResult.FinalReply)
		}
	}
	for _, fragment := range virtualTurn.ForbiddenReplyFragments {
		if strings.Contains(turnResult.FinalReply, fragment) {
			return fmt.Errorf("forbidden reply fragment %q found in %q", fragment, turnResult.FinalReply)
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

func findAttachmentWithSuffix(attachments []agent.FileAttachment, suffix string) (agent.FileAttachment, bool) {
	for _, attachment := range attachments {
		if strings.HasSuffix(attachment.Filename, suffix) || strings.HasSuffix(attachment.DevicePath, suffix) {
			return attachment, true
		}
	}
	return agent.FileAttachment{}, false
}

func validateAttachmentContent(attachment agent.FileAttachment, suffix string) error {
	switch suffix {
	case ".pptx":
		return validatePPTXAttachment(attachment)
	case ".pdf":
		return validateFilePrefix(attachment.DevicePath, "%PDF")
	case ".html":
		return validateFileContains(attachment.DevicePath, "<html")
	case "-notes.txt":
		return validateNonEmptyFile(attachment.DevicePath)
	default:
		return validateNonEmptyFile(attachment.DevicePath)
	}
}

func validatePPTXAttachment(attachment agent.FileAttachment) error {
	reader, errorValue := zip.OpenReader(attachment.DevicePath)
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
	artifactPath := filepath.Join(rootPath, scenario.Name+"-"+time.Now().UTC().Format("20060102T150405.000000000"))
	return artifactPath, os.MkdirAll(artifactPath, 0700)
}

func allowedToolsOrDefault(allowedTools []string) []string {
	if len(allowedTools) > 0 {
		return append([]string{}, allowedTools...)
	}
	return []string{"conversation.history", "memory.search", "terminal.run", "terminal.session", "browser_handoff.openURL", "approval.request", "file.write", "file.attach"}
}

func terminalConfiguration(workspacePath string) config.TerminalConfiguration {
	return config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspacePath,
		DeniedExecutableNames: []string{"sudo", "su", "mount", "umount", "reboot", "shutdown", "systemctl"},
		DeniedPathPrefixes:    []string{"/etc", "/private/etc", "/System", "/Library"},
		TimeoutSecond:         10,
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
			DisplayName:       "샘플",
			Emails:            []string{"dongha@example.com"},
			SecurityLevelRank: 0,
			GrantedClasses:    []string{},
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

type scriptedLanguageModel struct {
	mutex    sync.Mutex
	contents []string
	requests []llm.StructuredResponseRequest
}

func (languageModel *scriptedLanguageModel) Enqueue(contents ...string) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.contents = append(languageModel.contents, contents...)
}

func (languageModel *scriptedLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *scriptedLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.requests = append(languageModel.requests, request)
	if len(languageModel.contents) == 0 {
		return llm.StructuredResponse{}, errors.New("virtual session model response queue is empty")
	}
	content := languageModel.contents[0]
	languageModel.contents = languageModel.contents[1:]
	return llm.StructuredResponse{ProviderName: "virtual", ModelName: "scripted", Content: content}, nil
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
		DisplayName:    "샘플",
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

func newVirtualMemoryStore(initialFacts []memory.MemoryFact) *virtualMemoryStore {
	return &virtualMemoryStore{facts: append([]memory.MemoryFact{}, initialFacts...)}
}

func (store *virtualMemoryStore) AddEpisode(_ context.Context, episode memory.MemoryEpisode) error {
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
			ValidAt:           validAt,
			SecurityLevelRank: namespace.SecurityLevelRank,
			RequiredClasses:   append([]string{}, namespace.RequiredClasses...),
		})
	}
	return nil
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

func actionFinalReply(reply string, evidence ...string) string {
	evidenceDocuments := []string{}
	for _, value := range evidence {
		parts := strings.Split(value, ":")
		if len(parts) != 3 {
			continue
		}
		evidenceDocuments = append(evidenceDocuments, `{"observationID":`+quote(parts[0])+`,"toolName":`+quote(parts[1])+`,"attachmentIndex":`+parts[2]+`}`)
	}
	return `{"action":"final_reply","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[` + strings.Join(evidenceDocuments, ",") + `],"finalReply":` + quote(reply) + `}`
}

func actionCallTool(toolName string, input string) string {
	return `{"action":"call_tool","toolName":` + quote(toolName) + `,"toolInput":` + input + `}`
}

func quote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
