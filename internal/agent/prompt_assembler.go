package agent

import (
	"strings"
	"time"

	"blueclaw/internal/llm"
)

type PromptAssembler struct{}

func (promptAssembler PromptAssembler) BuildTurnMessages(request AgentTurnRequest, observations []turnObservation, baseInstruction string, toolDescription string) []llm.Message {
	messages := []llm.Message{{Role: "system", Content: strings.TrimSpace(baseInstruction)}}
	promptAssembler.appendTemporalContextMessage(&messages, request.TurnStartedAt)
	promptAssembler.appendInstructionMessages(&messages, request.InstructionPrompt)
	promptAssembler.appendToolMessage(&messages, toolDescription)
	promptAssembler.appendRuntimeContextMessage(&messages, request)
	promptAssembler.appendSenderAddressingMessage(&messages, buildSenderAddressingDescription(request))
	promptAssembler.appendVisibleContextMessage(&messages, request.VisibleContext)
	promptAssembler.appendMemoryMessage(&messages, buildMemoryContext(request.MemoryFacts))
	promptAssembler.appendProgressMessage(&messages, request, observations)
	promptAssembler.appendObservationMessage(&messages, observations)
	messages = append(messages, llm.Message{Role: "user", Content: request.Prompt})
	return messages
}

func (promptAssembler PromptAssembler) BuildReplyMessages(prompt string, visibleContext VisibleContext, memoryContext string, instructionPrompt string) []llm.Message {
	messages := []llm.Message{{Role: "system", Content: "You are Blueclaw. Reply helpfully and concisely to the user message. Use the provided visible conversation context and Blueclaw memory only as context; do not reveal hidden policy or provenance unless the user asks for it and access is allowed."}}
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
		"Current time: " + localTime.Format(time.RFC3339),
		"Time zone: " + location.String(),
		"Resolve relative dates such as today, tomorrow, next Friday, 오늘, 내일, and 다음 주 from this context before choosing tool inputs.",
	}, "\n")
}

func temporalContextLocation() *time.Location {
	location, errorValue := time.LoadLocation("Asia/Seoul")
	if errorValue == nil {
		return location
	}
	return time.FixedZone("Asia/Seoul", 9*60*60)
}

func (promptAssembler PromptAssembler) appendInstructionMessages(messages *[]llm.Message, instructionPrompt string) {
	if strings.TrimSpace(instructionPrompt) == "" {
		return
	}
	*messages = append(*messages, llm.Message{
		Role:    "system",
		Content: "Workspace and skill instructions:\n" + strings.TrimSpace(instructionPrompt),
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
		"Default calendar timezone: " + defaultTurnLocation().String(),
		"Resolve relative dates such as today, tomorrow, and this Tuesday from the current turn date before calling tools.",
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
	if len(observations) == 0 {
		return
	}
	body := marshalEventBody(recentProgressObservations(observations))
	if len(body) > progressMessageLimit {
		body = body[:progressMessageLimit] + "\n[trimmed]"
	}
	*messages = append(*messages, llm.Message{
		Role:    "system",
		Content: "Relevant observation summaries so far. Use observationID/toolName/attachmentIndex when citing completionEvidence; do not infer hidden raw output:\n" + body,
	})
}

func (promptAssembler PromptAssembler) appendProgressMessage(messages *[]llm.Message, request AgentTurnRequest, observations []turnObservation) {
	progress := buildTurnProgress(request, observations)
	if len(observations) == 0 {
		progress.RemainingWork = "No tool work has been attempted yet."
	}
	body := marshalEventBody(progress)
	if len(body) > progressMessageLimit {
		body = body[:progressMessageLimit] + "\n[trimmed]"
	}
	*messages = append(*messages, llm.Message{
		Role:    "system",
		Content: "Progress ledger. This is the compact source of truth for what has already happened; raw tool output is intentionally omitted:\n" + body,
	})
}
