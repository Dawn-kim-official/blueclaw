package agent

import (
	"fmt"
	"strings"
	"time"

	"blueclaw/internal/llm"
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

func BuildInjectedContextMessages(input InjectedContextInput) []llm.Message {
	return compactMessages([]llm.Message{
		systemMessage(input.BaseInstruction),
		systemMessage(buildTemporalContextDescription(input.TurnStartedAt)),
		systemMessage(buildInstructionContext(input.InstructionPrompt)),
		systemMessage(input.ToolDescription),
		systemMessage(buildRuntimeContextDescription(input.RuntimeRequest)),
		systemMessage(buildSenderAddressingDescription(input.RuntimeRequest)),
		systemMessage(activeGoalDescription(input.RuntimeRequest.ActiveGoal)),
		systemMessage(buildVisibleContextDescription(input.RuntimeRequest.VisibleContext)),
		systemMessage(input.MemoryContext),
		systemMessage(buildProgressContext(input.RuntimeRequest, input.Observations)),
		systemMessage(buildExecutionStateContext(input.ExecutionState, input.Observations)),
		toolResultContextMessage(input.Observations),
		systemMessage(buildObservationContext(input.Observations)),
	})
}

func (promptAssembler PromptAssembler) BuildTurnMessages(request AgentTurnRequest, observations []turnObservation, baseInstruction string, toolDescription string, executionStates ...ExecutionState) []llm.Message {
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
	messages = append(messages, llm.Message{Role: "user", Content: request.Prompt})
	return messages
}

func (promptAssembler PromptAssembler) appendActiveGoalMessage(messages *[]llm.Message, activeGoal ActiveGoal) {
	goalDescription := activeGoalDescription(activeGoal)
	if goalDescription == "" {
		return
	}
	*messages = append(*messages, llm.Message{Role: "system", Content: goalDescription})
}

func (promptAssembler PromptAssembler) BuildReplyMessages(prompt string, visibleContext VisibleContext, memoryContext string, instructionPrompt string) []llm.Message {
	messages := []llm.Message{{Role: "system", Content: "You are Blueclaw. Reply helpfully and concisely to the user message. Use the provided visible conversation context and Blueclaw memory only as context; do not reveal hidden policy or provenance unless the user asks for it and access is allowed."}}
	promptAssembler.appendTemporalContextMessage(&messages, time.Time{})
	promptAssembler.appendInstructionMessages(&messages, instructionPrompt)
	promptAssembler.appendVisibleContextMessage(&messages, visibleContext)
	promptAssembler.appendMemoryMessage(&messages, memoryContext)
	messages = append(messages, llm.Message{Role: "user", Content: prompt})
	return messages
}

func (promptAssembler PromptAssembler) appendTemporalContextMessage(messages *[]llm.Message, turnStartedAt time.Time) {
	*messages = append(*messages, llm.Message{
		Role:    "system",
		Content: buildTemporalContextDescription(turnStartedAt),
	})
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
		"Current time: " + localTime.Format(time.RFC3339),
		"Time zone: " + location.String(),
		"Resolve relative dates such as today, tomorrow, next Friday, 오늘, 내일, and 다음 주 from this context before choosing tool inputs.",
	}, "\n")
}

func buildInstructionContext(instructionPrompt string) string {
	if strings.TrimSpace(instructionPrompt) == "" {
		return ""
	}
	return "Workspace instructions and available skill references:\n" + strings.TrimSpace(instructionPrompt)
}

func systemMessage(content string) llm.Message {
	return llm.Message{Role: "system", Content: strings.TrimSpace(content)}
}

func toolResultContextMessage(observations []turnObservation) llm.Message {
	items := toolResultContextItems(observations)
	if len(items) == 0 {
		return llm.Message{}
	}
	body := marshalEventBody(items)
	message := llm.Message{
		Role:    "system",
		Content: "Tool result context. This is the model-visible representation of tool outputs; use it for the next action instead of guessing from progress labels:\n" + body,
	}
	for _, observation := range observations {
		for index, attachment := range observation.Attachments {
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.ContentType)), "image/") || strings.TrimSpace(attachment.ContentBase64) == "" {
				continue
			}
			message.Parts = append(message.Parts, llm.MessagePart{
				Type:       "image",
				MimeType:   strings.TrimSpace(attachment.ContentType),
				DataBase64: strings.TrimSpace(attachment.ContentBase64),
				Text:       observation.ObservationID + ":" + fmt.Sprintf("%d", index),
			})
		}
	}
	return message
}

func compactMessages(messages []llm.Message) []llm.Message {
	result := []llm.Message{}
	for _, message := range messages {
		trimmedContent := strings.TrimSpace(message.Content)
		if trimmedContent == "" && len(message.Parts) == 0 {
			continue
		}
		result = append(result, llm.Message{Role: message.Role, Content: trimmedContent, Parts: append([]llm.MessagePart{}, message.Parts...)})
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

func (promptAssembler PromptAssembler) appendInstructionMessages(messages *[]llm.Message, instructionPrompt string) {
	instructionContext := buildInstructionContext(instructionPrompt)
	if instructionContext == "" {
		return
	}
	*messages = append(*messages, llm.Message{
		Role:    "system",
		Content: instructionContext,
	})
}

func (promptAssembler PromptAssembler) appendToolMessage(messages *[]llm.Message, toolDescription string) {
	if strings.TrimSpace(toolDescription) == "" {
		return
	}
	*messages = append(*messages, llm.Message{
		Role:    "system",
		Content: strings.TrimSpace(toolDescription),
	})
}

func (promptAssembler PromptAssembler) appendRuntimeContextMessage(messages *[]llm.Message, request AgentTurnRequest) {
	runtimeContextDescription := buildRuntimeContextDescription(request)
	if runtimeContextDescription == "" {
		return
	}
	*messages = append(*messages, llm.Message{
		Role:    "system",
		Content: runtimeContextDescription,
	})
}

func (promptAssembler PromptAssembler) appendSenderAddressingMessage(messages *[]llm.Message, senderAddressingDescription string) {
	if strings.TrimSpace(senderAddressingDescription) == "" {
		return
	}
	*messages = append(*messages, llm.Message{
		Role:    "system",
		Content: strings.TrimSpace(senderAddressingDescription),
	})
}

func buildRuntimeContextDescription(request AgentTurnRequest) string {
	if request.TurnStartedAt.IsZero() {
		return ""
	}
	localTime := request.TurnStartedAt.In(defaultTurnLocation())
	lines := []string{
		"Runtime context:",
		"Response language: " + ResolveResponseLanguage(request.ResponseLanguage),
		"Current turn datetime: " + localTime.Format(time.RFC3339),
		"Current turn date: " + localTime.Format("2006-01-02"),
		"Current turn weekday: " + localTime.Weekday().String(),
		"Default calendar timezone: " + defaultTurnLocation().String(),
		"Resolve relative dates such as today, tomorrow, and this Tuesday from the current turn date before calling tools.",
	}
	if workspaceContext := buildWorkspaceContextDescription(request); workspaceContext != "" {
		lines = append(lines, workspaceContext)
	}
	if !nextStepPlanIsEmpty(request.CurrentStepPlan) {
		lines = append(lines, "Previous Step plan for the current working set:\n"+marshalEventBody(normalizeNextStepPlan(request.CurrentStepPlan)))
	}
	return strings.Join(lines, "\n")
}

func buildWorkspaceContextDescription(request AgentTurnRequest) string {
	if strings.TrimSpace(request.WorkspaceDefaultPath) == "" {
		return ""
	}
	lines := []string{
		"Terminal commands run as the requester POSIX identity.",
		"Use virtual workspace paths in tool inputs; do not use concrete private POSIX paths.",
		"Use home/<path> for requester-private durable source work.",
		"Use tmp/<artifact-slug> for draft artifact work.",
		"Use artifacts/<artifact-slug> for accepted final files.",
	}
	if personID := strings.TrimSpace(request.RequesterPersonID); personID != "" {
		lines = append(lines, "The requester has a private POSIX home directory, but tool path fields should refer to it as home/<path>.")
	}
	lines = append(lines,
		"Circle-shared files live under /workspace/circles/<circleID> when the requester belongs to that circle.",
		"/workspace/.blueclaw is service-owned runtime state and is normally not writable from terminal tools.",
		"If unsure, inspect access with virtual-path working directories such as tmp/<slug>: id; pwd; ls -ld .; stat -c '%A %U %G %n' .; test -w .",
	)
	return strings.Join(lines, "\n")
}

func defaultTurnLocation() *time.Location {
	location, errorValue := time.LoadLocation("Asia/Seoul")
	if errorValue != nil {
		return time.Local
	}
	return location
}

func (promptAssembler PromptAssembler) appendVisibleContextMessage(messages *[]llm.Message, visibleContext VisibleContext) {
	visibleContextDescription := buildVisibleContextDescription(visibleContext)
	if visibleContextDescription == "" {
		return
	}
	*messages = append(*messages, llm.Message{Role: "system", Content: visibleContextDescription})
}

func (promptAssembler PromptAssembler) appendMemoryMessage(messages *[]llm.Message, memoryContext string) {
	if strings.TrimSpace(memoryContext) == "" {
		return
	}
	*messages = append(*messages, llm.Message{Role: "system", Content: memoryContext})
}

func (promptAssembler PromptAssembler) appendObservationMessage(messages *[]llm.Message, observations []turnObservation) {
	observationContext := buildObservationContext(observations)
	if observationContext == "" {
		return
	}
	*messages = append(*messages, llm.Message{
		Role:    "system",
		Content: observationContext,
	})
}

func (promptAssembler PromptAssembler) appendProgressMessage(messages *[]llm.Message, request AgentTurnRequest, observations []turnObservation) {
	progressContext := buildProgressContext(request, observations)
	if progressContext == "" {
		return
	}
	*messages = append(*messages, llm.Message{
		Role:    "system",
		Content: progressContext,
	})
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
