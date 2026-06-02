package agent

import (
	"strings"
	"time"

	"blueclaw/internal/memory"
)

type LLMContextBuilder struct{}

type LLMContextInput struct {
	ResponseLanguage  string
	UserPrompt        string
	TurnStartedAt     time.Time
	InstructionPrompt string
	ToolDescription   string
	WorkspaceContext  WorkspaceContext
	VisibleContext    VisibleContext
	MemoryFacts       []memory.MemoryFact
	MemoryContext     string
	ActiveGoal        ActiveGoal
	ActiveTask        ActiveTaskContext
	PendingInput      PendingInputContext
	CurrentStepPlan   NextStepPlan
	StepBudgetContext string
	Observations      []turnObservation
	ExecutionState    ExecutionState
	FailureFacts      failureReportFacts
	Attachments       []FileAttachment
	ExtraSections     []string
}

type WorkspaceContext struct {
	RootPath            string
	DefaultPath         string
	RequesterPersonID   string
	TerminalInstruction string
}

func (builder LLMContextBuilder) Build(input LLMContextInput) string {
	return strings.Join(nonEmptyStrings([]string{
		builder.runtimeContext(input),
		buildInstructionContext(input.InstructionPrompt),
		strings.TrimSpace(input.ToolDescription),
		builder.workspaceContext(input.WorkspaceContext),
		builder.conversationContext(input.VisibleContext),
		builder.taskContext(input),
		builder.memoryContext(input),
		strings.TrimSpace(input.StepBudgetContext),
		builder.progressContext(input),
		buildExecutionStateContext(input.ExecutionState, input.Observations),
		toolResultContextText(input.Observations),
		buildObservationContext(input.Observations),
		builder.failureContext(input),
		builder.attachmentContext(input.Attachments),
		strings.Join(nonEmptyStrings(input.ExtraSections), "\n\n"),
	}), "\n\n")
}

func (builder LLMContextBuilder) runtimeContext(input LLMContextInput) string {
	startedAt := input.TurnStartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	return strings.Join([]string{
		"Runtime:",
		"Response language: " + ResolveResponseLanguage(input.ResponseLanguage),
		strings.TrimPrefix(buildTemporalContextDescription(startedAt), "Runtime temporal context:\n"),
	}, "\n")
}

func (builder LLMContextBuilder) workspaceContext(workspaceContext WorkspaceContext) string {
	sections := []string{}
	if strings.TrimSpace(workspaceContext.TerminalInstruction) != "" {
		sections = append(sections, strings.TrimSpace(workspaceContext.TerminalInstruction))
	}
	request := AgentTurnRequest{
		RequesterPersonID:     workspaceContext.RequesterPersonID,
		WorkspaceRootPath:     workspaceContext.RootPath,
		WorkspaceDefaultPath:  workspaceContext.DefaultPath,
		CurrentStepPlan:       NextStepPlan{},
		ToolSet:               nil,
		ResponseLanguage:      DefaultResponseLanguage(),
		RequiredEvidenceTools: nil,
	}
	if description := buildWorkspaceContextDescription(request); description != "" {
		sections = append(sections, "Workspace:\n"+description)
	}
	return strings.Join(nonEmptyStrings(sections), "\n")
}

func (builder LLMContextBuilder) conversationContext(visibleContext VisibleContext) string {
	description := buildVisibleContextDescription(visibleContext)
	if strings.TrimSpace(description) == "" {
		return ""
	}
	return "Conversation:\n" + description
}

func (builder LLMContextBuilder) taskContext(input LLMContextInput) string {
	sections := []string{}
	if prompt := strings.TrimSpace(input.UserPrompt); prompt != "" {
		sections = append(sections, "Original user request:\n"+prompt)
	}
	if activeGoal := activeGoalDescription(input.ActiveGoal); activeGoal != "" {
		sections = append(sections, activeGoal)
	}
	if activeTask := builder.activeTaskContext(input.ActiveTask); activeTask != "" {
		sections = append(sections, activeTask)
	}
	if pendingInput := builder.pendingInputContext(input.PendingInput); pendingInput != "" {
		sections = append(sections, pendingInput)
	}
	if !nextStepPlanIsEmpty(input.CurrentStepPlan) {
		sections = append(sections, "Previous Step plan for the current working set:\n"+marshalEventBody(normalizeNextStepPlan(input.CurrentStepPlan)))
	}
	if len(sections) == 0 {
		return ""
	}
	return "Task:\n" + strings.Join(sections, "\n\n")
}

func (builder LLMContextBuilder) activeTaskContext(activeTask ActiveTaskContext) string {
	if strings.TrimSpace(activeTask.TaskRunID) == "" && strings.TrimSpace(activeTask.Prompt) == "" && strings.TrimSpace(activeTask.Summary) == "" {
		return ""
	}
	return "Active task:\n" + marshalEventBody(map[string]string{
		"taskRunID": activeTask.TaskRunID,
		"prompt":    activeTask.Prompt,
		"status":    activeTask.Status,
		"summary":   activeTask.Summary,
	})
}

func (builder LLMContextBuilder) pendingInputContext(pendingInput PendingInputContext) string {
	if strings.TrimSpace(pendingInput.TaskRunID) == "" && strings.TrimSpace(pendingInput.Question) == "" {
		return ""
	}
	return "Pending user input:\n" + marshalEventBody(pendingInput)
}

func (builder LLMContextBuilder) memoryContext(input LLMContextInput) string {
	memoryContext := strings.TrimSpace(input.MemoryContext)
	if memoryContext == "" {
		memoryContext = buildMemoryContext(input.MemoryFacts)
	}
	if memoryContext == "" {
		return ""
	}
	return "Memory:\n" + memoryContext
}

func (builder LLMContextBuilder) failureContext(input LLMContextInput) string {
	sections := []string{}
	if len(input.FailureFacts.Attempts) > 0 {
		sections = append(sections, "Failure report facts:\n"+marshalEventBody(input.FailureFacts))
	}
	if summary := builder.failureObservationContext(input.Observations); summary != "" {
		sections = append(sections, "Failure observations:\n"+summary)
	}
	if len(sections) == 0 {
		return ""
	}
	return "Failure:\n" + strings.Join(sections, "\n\n")
}

func (builder LLMContextBuilder) progressContext(input LLMContextInput) string {
	if len(input.Observations) == 0 {
		return ""
	}
	return buildProgressContext(agentTurnRequestForContext(input), input.Observations)
}

func (builder LLMContextBuilder) failureObservationContext(observations []turnObservation) string {
	for _, observation := range observations {
		if observation.Failed() {
			return buildFailureObservationSummary(observations)
		}
	}
	return ""
}

func (builder LLMContextBuilder) attachmentContext(attachments []FileAttachment) string {
	if summary := buildLimitAttachmentSummary(attachments); summary != "" {
		return "Attachments:\n" + summary
	}
	return ""
}

func agentTurnRequestForContext(input LLMContextInput) AgentTurnRequest {
	return AgentTurnRequest{
		RequesterPersonID:     input.WorkspaceContext.RequesterPersonID,
		ResponseLanguage:      input.ResponseLanguage,
		TurnStartedAt:         input.TurnStartedAt,
		WorkspaceRootPath:     input.WorkspaceContext.RootPath,
		WorkspaceDefaultPath:  input.WorkspaceContext.DefaultPath,
		CurrentStepPlan:       input.CurrentStepPlan,
		VisibleContext:        input.VisibleContext,
		ActiveGoal:            input.ActiveGoal,
		RequiredEvidenceTools: nil,
	}
}
