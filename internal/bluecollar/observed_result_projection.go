package bluecollar

import (
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

import "strings"

type ObservedFact struct {
	ObjectType    string `json:"objectType"`
	Effect        string `json:"effect"`
	ObservationID string `json:"observationID,omitempty"`
	ToolName      string `json:"toolName,omitempty"`
	ID            string `json:"id,omitempty"`
	Path          string `json:"path,omitempty"`
	URL           string `json:"url,omitempty"`
}

type ProjectionMissingRequirement struct {
	Description        string   `json:"description"`
	ObjectType         string   `json:"objectType"`
	Effect             string   `json:"effect"`
	SuggestedNextTools []string `json:"suggestedNextTools,omitempty"`
}

type ProjectionRecoverableAction struct {
	ToolName string `json:"toolName"`
	Reason   string `json:"reason"`
}

type ObservedResultProjection struct {
	ObservedFacts       []ObservedFact                 `json:"observedFacts,omitempty"`
	MissingRequirements []ProjectionMissingRequirement `json:"missingRequirements,omitempty"`
	RecoverableActions  []ProjectionRecoverableAction  `json:"recoverableActions,omitempty"`
}

func buildObservedResultProjection(request AgentTurnRequest, observations []turnObservation, _ []toolcontract.FileAttachment, actionDocument turnActionDocument) ObservedResultProjection {
	facts := observedFactsFromObservations(request.ToolSet, observations)
	facts = deduplicateObservedFacts(facts)
	return ObservedResultProjection{
		ObservedFacts:       facts,
		MissingRequirements: missingRequirementsForFinishClaims(request, facts, actionDocument),
		RecoverableActions:  recoverableActionsForProjection(observations),
	}
}

func observedFactsFromObservations(toolSet *toolcontract.ToolSet, observations []turnObservation) []ObservedFact {
	facts := []ObservedFact{}
	for _, observation := range observations {
		if observation.Failed() {
			continue
		}
		facts = append(facts, factsFromObservation(toolSet, observation)...)
	}
	return facts
}

func factsFromObservation(toolSet *toolcontract.ToolSet, observation turnObservation) []ObservedFact {
	if toolSet == nil || len(observation.Effects) == 0 {
		return nil
	}
	descriptor, isRegistered := toolSet.ToolDefinition(observation.Tool)
	if !isRegistered || descriptor.ResultContract == nil {
		return nil
	}
	result := toolcontract.ToolResult{Output: observation.Output, Effects: observation.Effects}
	if toolcontract.ValidateSuccessfulToolResult(*descriptor.ResultContract, result) != nil {
		return nil
	}
	return observedFactsFromResourceEffects(observation)
}

func observedFactsFromResourceEffects(observation turnObservation) []ObservedFact {
	facts := make([]ObservedFact, 0, len(observation.Effects))
	for _, resourceEffect := range observation.Effects {
		facts = append(facts, ObservedFact{
			ObjectType:    strings.TrimSpace(resourceEffect.ObjectType),
			Effect:        strings.TrimSpace(resourceEffect.Effect),
			ObservationID: observation.ObservationID,
			ToolName:      observation.Tool,
			ID:            strings.TrimSpace(resourceEffect.ID),
			Path:          strings.TrimSpace(resourceEffect.Path),
			URL:           strings.TrimSpace(resourceEffect.URL),
		})
	}
	return facts
}

func missingRequirementsForFinishClaims(request AgentTurnRequest, facts []ObservedFact, actionDocument turnActionDocument) []ProjectionMissingRequirement {
	if strings.TrimSpace(actionDocument.Action) != "finish" {
		return nil
	}
	requirements := requiredProjectionRequirementsFromContract(request)
	missingRequirements := []ProjectionMissingRequirement{}
	for _, requirement := range requirements {
		if projectionHasObservedFact(facts, requirement.ObjectType, requirement.Effect) {
			continue
		}
		missingRequirements = append(missingRequirements, requirement)
	}
	return missingRequirements
}

func requiredProjectionRequirementsFromContract(request AgentTurnRequest) []ProjectionMissingRequirement {
	requirements := []ProjectionMissingRequirement{}
	for _, effect := range normalizeOutcomeEffects(request.OutcomeContract.RequiredEffects) {
		description := effect.Description
		if description == "" {
			description = "finish is missing required observed effect " + effect.ObjectType + "/" + effect.Effect
		}
		requirements = append(requirements, projectionRequirement(request, description, effect.ObjectType, effect.Effect, effect.SuggestedNextTools))
	}
	return deduplicateProjectionRequirements(requirements)
}

func projectionRequirement(request AgentTurnRequest, description string, objectType string, effect string, suggestedTools []string) ProjectionMissingRequirement {
	return ProjectionMissingRequirement{
		Description:        description,
		ObjectType:         objectType,
		Effect:             effect,
		SuggestedNextTools: projectionSuggestedToolNames(request, suggestedTools),
	}
}

func projectionSuggestedToolNames(request AgentTurnRequest, suggestedTools []string) []string {
	if request.ToolSet == nil {
		return appendUniqueStrings(nil, suggestedTools...)
	}
	registeredTools := registeredToolNamesOnly(request.ToolSet, suggestedTools)
	if len(registeredTools) > 0 {
		return registeredTools
	}
	return appendUniqueStrings(nil, suggestedTools...)
}

func projectionHasObservedFact(facts []ObservedFact, objectType string, effect string) bool {
	for _, fact := range facts {
		if fact.ObjectType == objectType && fact.Effect == effect {
			return true
		}
	}
	return false
}

func recoverableActionsForProjection(observations []turnObservation) []ProjectionRecoverableAction {
	actions := []ProjectionRecoverableAction{}
	for _, observation := range observations {
		if !observation.Failed() || strings.TrimSpace(observation.Tool) == "" {
			continue
		}
		if observation.RecoveryPacket == nil && !observation.SafeRetry() {
			continue
		}
		actions = append(actions, ProjectionRecoverableAction{
			ToolName: observation.Tool,
			Reason:   firstNonEmptyString(observation.FailureSummary(), observation.ContentText()),
		})
	}
	return actions
}

func deduplicateObservedFacts(facts []ObservedFact) []ObservedFact {
	seenFacts := map[string]bool{}
	deduplicatedFacts := []ObservedFact{}
	for _, fact := range facts {
		key := strings.Join([]string{fact.ObjectType, fact.Effect, fact.ObservationID, fact.ToolName, fact.ID, fact.Path, fact.URL}, "\x00")
		if seenFacts[key] {
			continue
		}
		seenFacts[key] = true
		deduplicatedFacts = append(deduplicatedFacts, fact)
	}
	return deduplicatedFacts
}

func deduplicateProjectionRequirements(requirements []ProjectionMissingRequirement) []ProjectionMissingRequirement {
	seenRequirements := map[string]bool{}
	deduplicatedRequirements := []ProjectionMissingRequirement{}
	for _, requirement := range requirements {
		key := requirement.ObjectType + "\x00" + requirement.Effect
		if seenRequirements[key] {
			continue
		}
		seenRequirements[key] = true
		deduplicatedRequirements = append(deduplicatedRequirements, requirement)
	}
	return deduplicatedRequirements
}
