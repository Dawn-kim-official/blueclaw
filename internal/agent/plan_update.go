package agent

import (
	"encoding/json"

	"blueclaw/internal/task"
)

type planUpdateDocument struct {
	Goal  string     `json:"goal,omitempty"`
	Steps []PlanStep `json:"steps"`
}

func planUpdateFromObservation(observation turnObservation) (planUpdateDocument, bool) {
	if observation.Action != "continue" || observation.Failed() || !ToolNamesMatch(observation.Tool, PlanUpdateToolName) {
		return planUpdateDocument{}, false
	}
	var document planUpdateDocument
	if json.Unmarshal(observation.Output.Data, &document) != nil {
		return planUpdateDocument{}, false
	}
	document.Goal, document.Steps = NormalizePlan(document.Goal, document.Steps)
	return document, true
}

func (agentTurnRunner *AgentTurnRunner) applyPlanUpdateObservation(taskRunID string, state *agentTaskState, observation turnObservation) {
	document, isPlanUpdate := planUpdateFromObservation(observation)
	if !isPlanUpdate {
		return
	}
	if document.Goal != "" {
		state.ExecutionState.Goal = document.Goal
	}
	state.ExecutionState.Steps = document.Steps
	agentTurnRunner.appendEvent(taskRunID, "agent.plan.updated", marshalEventBody(planUpdateDocument{Goal: state.ExecutionState.Goal, Steps: state.ExecutionState.Steps}))
	agentTurnRunner.appendEvent(taskRunID, "agent.execution_state", marshalEventBody(normalizeExecutionState(state.ExecutionState)))
}

func (agentTurnRunner *AgentTurnRunner) nudgePlanBeforeStateChange(taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) toolCallActionOutcome {
	if state.DidNudgePlan || len(state.ExecutionState.Steps) > 0 || !taskLevelRequiresPlan(request.TaskLevel) {
		return toolCallActionOutcome{}
	}
	if request.ToolSet == nil || !requestToolSetCanReachTool(request.ToolSet, PlanUpdateToolName) {
		return toolCallActionOutcome{}
	}
	toolDefinition, isFound := request.ToolSet.ToolDefinition(actionDocument.ToolName)
	if !isFound || !toolDefinitionIsStateChanging(toolDefinition) {
		return toolCallActionOutcome{}
	}
	state.DidNudgePlan = true
	observation := newContentObservation(nextObservationIDForObservations(state.Observations), "policy", actionDocument.ToolName, "Record your goal and step plan with plan.update before the first state-changing call on this multi-step task, then proceed.")
	state.Observations = append(state.Observations, observation)
	agentTurnRunner.appendEvent(taskRunID, "agent.plan.nudged", marshalEventBody(observation))
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "plan_nudged "+observation.Tool, observation.ContentText())
	return toolCallActionOutcome{WasHandled: true}
}

func toolDefinitionIsStateChanging(toolDefinition ToolDefinition) bool {
	switch ToolDefinitionSideEffectClass(toolDefinition) {
	case "", ToolSideEffectNone, ToolSideEffectRead, ToolSideEffectComputation, ToolSideEffectApproval:
		return false
	default:
		return true
	}
}

func latestPlanUpdate(observations []turnObservation) (planUpdateDocument, bool) {
	for index := len(observations) - 1; index >= 0; index-- {
		if document, isPlanUpdate := planUpdateFromObservation(observations[index]); isPlanUpdate {
			return document, true
		}
	}
	return planUpdateDocument{}, false
}
