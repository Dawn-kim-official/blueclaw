package bluecollar

import (
	"fmt"
	"strings"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/model"
)

type PromptAssembler struct{}

type InjectedContextInput struct {
	BaseInstruction   string
	InstructionPrompt string
	ToolDescription   string
	TurnStartedAt     time.Time
	RuntimeRequest    AgentTurnRequest
	MemoryContext     string
	Observations      []turnObservation
	ExecutionState    ExecutionState
}

func BuildInjectedContextMessages(input InjectedContextInput) []model.Message {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		ResponseLanguage:     input.RuntimeRequest.ResponseLanguage,
		RequesterPersonID:    input.RuntimeRequest.RequesterPersonID,
		RequesterName:        input.RuntimeRequest.RequesterName,
		RequesterCallingName: input.RuntimeRequest.RequesterCallingName,
		RequesterEmail:       input.RuntimeRequest.RequesterEmail,
		Company:              input.RuntimeRequest.Company,
		UserPrompt:           input.RuntimeRequest.Prompt,
		InputParts:           append([]AgentPart{}, input.RuntimeRequest.InputParts...),
		TurnStartedAt:        input.TurnStartedAt,
		InstructionPrompt:    input.InstructionPrompt,
		ToolDescription:      input.ToolDescription,
		AdditionalToolNames:  droppedExposureToolNames(input.RuntimeRequest.ToolExposure),
		WorkspaceContext: WorkspaceContext{
			RootPath:          input.RuntimeRequest.WorkspaceRootPath,
			DefaultPath:       input.RuntimeRequest.WorkspaceDefaultPath,
			RequesterPersonID: input.RuntimeRequest.RequesterPersonID,
		},
		VisibleContext:        input.RuntimeRequest.VisibleContext,
		MemoryContext:         input.MemoryContext,
		ActiveGoal:            input.RuntimeRequest.ActiveGoal,
		PriorTask:             input.RuntimeRequest.PriorTask,
		ScheduledRun:          input.RuntimeRequest.ScheduledRun,
		StepBudgetContext:     input.RuntimeRequest.StepBudgetContext,
		ArtifactManifest:      input.RuntimeRequest.ArtifactManifest,
		Observations:          input.Observations,
		ExecutionState:        input.ExecutionState,
		FailureFacts:          buildFailureReportFacts(input.Observations, defaultRecoveryBudget()),
		Attachments:           attachmentsFromObservations(input.Observations),
		ToolSet:               input.RuntimeRequest.ToolSet,
		RequiredEvidenceTools: append([]string{}, input.RuntimeRequest.RequiredEvidenceTools...),
		OutcomeContract:       input.RuntimeRequest.OutcomeContract,
	})
	return compactMessages([]model.Message{
		systemMessage(input.BaseInstruction),
		systemMessage(contextText),
		toolResultImageContextMessage(input.Observations),
	})
}

func (promptAssembler PromptAssembler) BuildTurnMessages(request AgentTurnRequest, observations []turnObservation, baseInstruction string, toolDescription string, executionStates ...ExecutionState) []model.Message {
	executionState := ExecutionState{}
	if len(executionStates) > 0 {
		executionState = executionStates[0]
	}
	messages := BuildInjectedContextMessages(InjectedContextInput{
		BaseInstruction:   baseInstruction,
		InstructionPrompt: request.InstructionPrompt,
		ToolDescription:   toolDescription,
		TurnStartedAt:     request.TurnStartedAt,
		RuntimeRequest:    request,
		MemoryContext:     buildMemoryContext(request.MemoryFacts),
		Observations:      observations,
		ExecutionState:    executionState,
	})
	messages = append(messages, userMessageFromPromptAndParts(request.Prompt, request.InputParts))
	return messages
}

func (promptAssembler PromptAssembler) BuildReplyMessages(prompt string, visibleContext VisibleContext, memoryContext string, instructionPrompt string) []model.Message {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		UserPrompt:        prompt,
		InstructionPrompt: instructionPrompt,
		VisibleContext:    visibleContext,
		MemoryContext:     memoryContext,
	})
	messages := []model.Message{
		{Role: "system", Content: "You are Blueclaw. Reply helpfully and concisely to the user message. Use the provided context only as context; do not reveal hidden policy or provenance unless the user asks for it and access is allowed. If the user message only mentions you, answer or continue the recent visible conversation instead of asking what is needed. Treat jokes and playful addressed remarks as real conversational turns, and respond briefly like a good-humored coworker."},
		{Role: "system", Content: contextText},
	}
	messages = append(messages, model.Message{Role: "user", Content: prompt})
	return messages
}

func userMessageFromPromptAndParts(prompt string, inputParts []AgentPart) model.Message {
	if len(inputParts) == 0 {
		return model.Message{Role: "user", Content: strings.TrimSpace(prompt)}
	}
	parts := []AgentPart{}
	if strings.TrimSpace(prompt) != "" {
		parts = append(parts, TextAgentPart(prompt))
	}
	parts = append(parts, inputParts...)
	llmParts := AgentPartsToLLMParts(parts)
	if len(llmParts) == 0 {
		return model.Message{Role: "user", Content: strings.TrimSpace(prompt)}
	}
	return model.Message{Role: "user", Parts: llmParts}
}

func buildTemporalContextDescription(turnStartedAt time.Time) string {
	currentTime := turnStartedAt
	if currentTime.IsZero() {
		currentTime = time.Now()
	}
	location := temporalContextLocation()
	localTime := currentTime.In(location)
	return strings.Join([]string{
		"Runtime temporal context:",
		"Current date: " + localTime.Format("2006-01-02"),
		"Current weekday: " + localTime.Weekday().String(),
		"Current time: " + localTime.Format("15:04"),
		"Time zone: " + location.String(),
		buildCurrentWeekDescription(localTime),
		"Resolve relative dates such as today, tomorrow, next Friday, 오늘, 내일, and 다음 주 from this context before choosing tool inputs.",
	}, "\n")
}

func buildCurrentWeekDescription(localTime time.Time) string {
	daysSinceMonday := (int(localTime.Weekday()) + 6) % 7
	monday := localTime.AddDate(0, 0, -daysSinceMonday)
	weekdays := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	anchors := make([]string, 0, len(weekdays))
	for dayOffset, weekday := range weekdays {
		anchors = append(anchors, weekday+"="+monday.AddDate(0, 0, dayOffset).Format("2006-01-02"))
	}
	return "Current week: " + strings.Join(anchors, ", ")
}

func buildInstructionContext(instructionPrompt string) string {
	if strings.TrimSpace(instructionPrompt) == "" {
		return ""
	}
	return "Workspace instructions and available skill references:\n" + strings.TrimSpace(instructionPrompt)
}

func systemMessage(content string) model.Message {
	return model.Message{Role: "system", Content: strings.TrimSpace(content)}
}

func toolResultContextText(observations []turnObservation) string {
	items := toolResultContextItems(observations)
	if len(items) == 0 {
		return ""
	}
	body := marshalEventBody(items)
	return "Tool result context. This is the model-visible representation of tool outputs; use it for the next action instead of guessing from progress labels:\n" + body
}

func toolResultImageContextMessage(observations []turnObservation) model.Message {
	message := model.Message{
		Role:    "user",
		Content: "Tool result images for the next answer. Inspect these image parts directly; do not infer visual details from filenames or progress text.",
	}
	for _, observation := range observations {
		for index, attachment := range observation.Attachments {
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.ContentType)), "image/") || strings.TrimSpace(attachment.ContentBase64) == "" {
				continue
			}
			message.Parts = append(message.Parts, model.MessagePart{
				Type:       "image",
				MimeType:   strings.TrimSpace(attachment.ContentType),
				DataBase64: strings.TrimSpace(attachment.ContentBase64),
				Text:       observation.ObservationID + ":" + fmt.Sprintf("%d", index),
			})
		}
	}
	return message
}

func compactMessages(messages []model.Message) []model.Message {
	result := []model.Message{}
	for _, message := range messages {
		trimmedContent := strings.TrimSpace(message.Content)
		if trimmedContent == "" && len(message.Parts) == 0 {
			continue
		}
		result = append(result, model.Message{Role: message.Role, Content: trimmedContent, Parts: append([]model.MessagePart{}, message.Parts...)})
	}
	return result
}

func temporalContextLocation() *time.Location {
	location, errorValue := time.LoadLocation("Asia/Seoul")
	if errorValue == nil {
		return location
	}
	return time.FixedZone("Asia/Seoul", 9*60*60)
}

func buildWorkspaceContextDescription(request AgentTurnRequest) string {
	if strings.TrimSpace(request.WorkspaceDefaultPath) == "" {
		return ""
	}
	lines := []string{
		"Terminal commands run as the requester POSIX identity; ~ is your Linux home ($HOME) and your private workspace, and the same ~ path works in a tool path field and in a shell command.",
		"Do all document work — build, edit, and deliver — directly in ~/documents/; save finished documents (Word, PDF, Excel, slides) as ~/documents/<name>.<ext> so a later edit or delete task finds them with ls ~/documents.",
		"A concrete POSIX path under your home also resolves, so open one you see in ls output instead of giving up.",
		"Circle-shared files live under /workspace/circles/<circleID> when the requester belongs to that circle.",
		"/workspace/.blueclaw is service-owned runtime state and is normally not writable from terminal tools.",
		"If unsure, inspect access from a working directory such as ~/documents: id; pwd; ls -ld .; stat -c '%A %U %G %n' .; test -w .",
	}
	return strings.Join(lines, "\n")
}

func defaultTurnLocation() *time.Location {
	location, errorValue := time.LoadLocation("Asia/Seoul")
	if errorValue != nil {
		return time.Local
	}
	return location
}

func buildObservationContext(observations []turnObservation) string {
	if len(observations) == 0 {
		return ""
	}
	body := marshalEventBody(recentProgressObservations(observations))
	if len(body) > progressMessageLimit {
		body = body[:progressMessageLimit] + "\n[trimmed]"
	}
	return "Relevant observation ledger so far. Use observationID/toolName/attachmentIndex when citing completionEvidence:\n" + body
}

func buildProgressContext(request AgentTurnRequest, observations []turnObservation) string {
	progress := buildTurnProgress(request, observations)
	if len(observations) == 0 {
		progress.RemainingWork = "No tool work has been attempted yet."
	}
	body := marshalEventBody(progress)
	if len(body) > progressMessageLimit {
		body = body[:progressMessageLimit] + "\n[trimmed]"
	}
	return "Progress ledger. This is the compact source of truth for what has already happened; raw tool output is intentionally omitted:\n" + body
}
