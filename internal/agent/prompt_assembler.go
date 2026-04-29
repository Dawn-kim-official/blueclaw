package agent

import (
	"strings"

	"blueclaw/internal/llm"
)

type PromptAssembler struct{}

func (promptAssembler PromptAssembler) BuildTurnMessages(request AgentTurnRequest, observations []turnObservation, baseInstruction string, toolDescription string) []llm.Message {
	messages := []llm.Message{{Role: "system", Content: strings.TrimSpace(baseInstruction)}}
	promptAssembler.appendInstructionMessages(&messages, request.InstructionPrompt)
	promptAssembler.appendToolMessage(&messages, toolDescription)
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
	if len(body) > progressMessageBudget {
		body = body[:progressMessageBudget] + "\n[trimmed]"
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
	if len(body) > progressMessageBudget {
		body = body[:progressMessageBudget] + "\n[trimmed]"
	}
	*messages = append(*messages, llm.Message{
		Role:    "system",
		Content: "Progress ledger. This is the compact source of truth for what has already happened; raw tool output is intentionally omitted:\n" + body,
	})
}
