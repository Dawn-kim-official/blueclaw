package bluecollar

import (
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
)

type planUpdateDocument struct {
	Goal  string     `json:"goal,omitempty"`
	Steps []PlanStep `json:"steps"`
}

func planUpdateFromObservation(observation turnObservation) (planUpdateDocument, bool) {
	if observation.Action != "continue" || observation.Failed() || !toolcontract.ToolNamesMatch(observation.Tool, toolcontract.PlanUpdateToolName) {
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

func (agentTurnRunner *AgentTurnRunner) notePlanMissingBeforeStateChange(taskRunID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) {
	if state.DidNudgePlan || len(state.ExecutionState.Steps) > 0 || !taskLevelRequiresPlan(request.TaskLevel) {
		return
	}
	if request.ToolSet == nil || !requestToolSetCanReachTool(request.ToolSet, toolcontract.PlanUpdateToolName) {
		return
	}
	toolDefinition, isFound := request.ToolSet.ToolDefinition(actionDocument.ToolName)
	if !isFound || !toolDefinitionIsStateChanging(toolDefinition) {
		return
	}
	state.DidNudgePlan = true
	observation := newContentObservation(nextObservationIDForObservations(state.Observations), "policy", actionDocument.ToolName, "This multi-step task has no recorded plan yet. The current call proceeds; after it completes, record your goal and step plan with plan.update, then continue.")
	state.Observations = append(state.Observations, observation)
	agentTurnRunner.appendEvent(taskRunID, "agent.plan.nudged", marshalEventBody(observation))
}

func toolDefinitionIsStateChanging(toolDefinition toolcontract.ToolDefinition) bool {
	switch toolcontract.ToolDefinitionSideEffectClass(toolDefinition) {
	case "", toolcontract.ToolSideEffectNone, toolcontract.ToolSideEffectRead, toolcontract.ToolSideEffectComputation, toolcontract.ToolSideEffectApproval:
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
