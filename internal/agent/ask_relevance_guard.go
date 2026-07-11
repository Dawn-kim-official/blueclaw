package agent

import (
	"context"
	"encoding/json"
	"strings"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

type askRelevanceDecision struct {
	IsRequired bool   `json:"isRequired"`
	Reason     string `json:"reason"`
}

type askRelevanceObservation struct {
	Tool        string `json:"tool,omitempty"`
	Summary     string `json:"summary,omitempty"`
	FailureCode string `json:"failureCode,omitempty"`
}

type askRelevanceInput struct {
	OriginalRequest     string                    `json:"originalRequest"`
	OriginalInstruction string                    `json:"originalInstruction,omitempty"`
	CurrentObjective    string                    `json:"currentObjective,omitempty"`
	KnownContext        []string                  `json:"knownContext,omitempty"`
	MissingInformation  []string                  `json:"missingInformation,omitempty"`
	RecentObservations  []askRelevanceObservation `json:"recentObservations,omitempty"`
	ProposedQuestion    string                    `json:"proposedQuestion"`
}

func (agentTurnRunner *AgentTurnRunner) rejectUnrelatedAskAction(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	question, isAskAction := unrelatedAskCandidateQuestion(actionDocument)
	if !isAskAction || question == "" {
		return toolCallActionOutcome{}
	}
	decision, didDecide := agentTurnRunner.reviewAskRelevance(ctx, request, state.Observations, question)
	if !didDecide || decision.IsRequired {
		return toolCallActionOutcome{}
	}
	observation := unrelatedAskRedirectObservation(nextObservationID(len(state.Observations)+1), request, question, decision.Reason)
	state.Observations = append(state.Observations, observation)
	agentTurnRunner.appendEvent(taskRunID, "agent.unrelated_ask_rejected", marshalEventBody(map[string]string{
		"question": question,
		"reason":   decision.Reason,
	}))
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "unrelated_ask_rejected "+actionDocument.ToolName, observation.ContentText())
	result, shouldStop := stopForNoProgress(stepID)
	return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
}

func unrelatedAskCandidateQuestion(actionDocument turnActionDocument) (string, bool) {
	effectiveToolName, effectiveToolInput := effectiveActionToolNameAndInput(actionDocument.ToolName, actionDocument.ToolInput)
	switch strings.TrimSpace(effectiveToolName) {
	case AskInputToolName, AskConfirmToolName:
	default:
		return "", false
	}
	var inputFields struct {
		Question string `json:"question"`
	}
	_ = json.Unmarshal(effectiveToolInput, &inputFields)
	return strings.TrimSpace(firstNonEmptyString(actionDocument.Message, inputFields.Question)), true
}

func (agentTurnRunner *AgentTurnRunner) reviewAskRelevance(ctx context.Context, request AgentTurnRequest, observations []turnObservation, question string) (askRelevanceDecision, bool) {
	if agentTurnRunner.languageModel == nil {
		return askRelevanceDecision{}, false
	}
	inputDocument, errorValue := json.Marshal(buildAskRelevanceInput(request, observations, question))
	if errorValue != nil {
		return askRelevanceDecision{}, false
	}
	structuredResponse, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: []llm.Message{
			{Role: "system", Content: strings.Join([]string{
				"Decide whether the proposed question is directly required to complete the user's active task.",
				"Set isRequired=true only when execution needs the user's missing approval, bounded choice, or free-form information.",
				"Set isRequired=false when the question changes subject, asks about unrelated background knowledge, repeats information already present, or can be resolved from the supplied context and observations.",
				"Judge the meaning of the full context across languages. Return only the structured decision.",
			}, " ")},
			{Role: "user", Content: string(inputDocument)},
		},
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_ask_relevance",
			Document:           `{"type":"object","properties":{"isRequired":{"type":"boolean"},"reason":{"type":"string"}},"required":["isRequired","reason"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return askRelevanceDecision{}, false
	}
	var decision askRelevanceDecision
	if json.Unmarshal([]byte(structuredResponse.Content), &decision) != nil {
		return askRelevanceDecision{}, false
	}
	decision.Reason = strings.TrimSpace(decision.Reason)
	return decision, true
}

func buildAskRelevanceInput(request AgentTurnRequest, observations []turnObservation, question string) askRelevanceInput {
	return askRelevanceInput{
		OriginalRequest:     strings.TrimSpace(request.Prompt),
		OriginalInstruction: strings.TrimSpace(request.ActiveGoal.OriginalInstruction),
		CurrentObjective:    strings.TrimSpace(request.ActiveGoal.CurrentObjective),
		KnownContext:        append([]string{}, request.ActiveGoal.KnownContext...),
		MissingInformation:  append([]string{}, request.ActiveGoal.MissingInformation...),
		RecentObservations:  askRelevanceObservations(observations),
		ProposedQuestion:    strings.TrimSpace(question),
	}
}

func askRelevanceObservations(observations []turnObservation) []askRelevanceObservation {
	startIndex := len(observations) - 6
	if startIndex < 0 {
		startIndex = 0
	}
	result := []askRelevanceObservation{}
	for _, observation := range observations[startIndex:] {
		result = append(result, askRelevanceObservation{
			Tool:        strings.TrimSpace(observation.Tool),
			Summary:     truncateText(compactWhitespace(observation.Summary), 500),
			FailureCode: strings.TrimSpace(observation.FailureCode()),
		})
	}
	return result
}

func unrelatedAskRedirectObservation(observationID string, request AgentTurnRequest, question string, reason string) turnObservation {
	goalInstruction := strings.TrimSpace(firstNonEmptyString(request.ActiveGoal.OriginalInstruction, request.Prompt))
	message := "A structured relevance review rejected the proposed user question. Continue the active task without asking it."
	observation := newContentObservation(observationID, "policy", "", marshalEventBody(map[string]string{
		"directive":           message,
		"reason":              strings.TrimSpace(reason),
		"rejectedQuestion":    question,
		"originalInstruction": goalInstruction,
	}))
	observation.Summary = message
	return observation
}
