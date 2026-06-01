package agent

import (
	"encoding/json"
	"strings"
)

const (
	failureClassQuality       = "fixable_artifact_quality"
	failureClassWorkspace     = "workspace"
	failureClassDependency    = "dependency"
	failureClassPermission    = "permission"
	failureClassUserInput     = "user_input"
	failureClassNetwork       = "network"
	failureClassProviderLimit = "provider_limit"
	failureClassSchema        = "schema"
	failureClassUnknown       = "unknown"

	retryPolicyAfterPrecondition = "after_precondition"
	retryPolicyDifferentInput    = "different_input"
	retryPolicyDoNotRetry        = "do_not_retry"
)

func buildRecoveryPacket(observation turnObservation) RecoveryPacket {
	failure := observation.Failure
	failureClass := failureClassForObservation(observation)
	packet := RecoveryPacket{
		WhatFailed:          recoveryWhatFailed(observation),
		WhyLikely:           recoveryWhyLikely(observation, failureClass),
		FailureClass:        failureClass,
		RetryPolicy:         retryPolicyForObservation(observation),
		ForbiddenRepeats:    recoveryForbiddenRepeats(observation),
		EvidenceNeeded:      recoveryEvidenceNeeded(observation),
		MustDoNext:          recoveryMustDoNext(observation, failureClass),
		AffectedResources:   nil,
		DiagnosticArtifacts: nil,
	}
	if failure != nil {
		if strings.TrimSpace(failure.FailureClass) != "" {
			packet.FailureClass = strings.TrimSpace(failure.FailureClass)
		}
		if strings.TrimSpace(failure.RetryPolicy) != "" {
			packet.RetryPolicy = strings.TrimSpace(failure.RetryPolicy)
		}
		packet.AffectedResources = append([]AffectedResource{}, failure.AffectedResources...)
		packet.DiagnosticArtifacts = append([]DiagnosticArtifact{}, failure.DiagnosticArtifacts...)
		for _, recoveryHint := range failure.RecoveryHints {
			packet.AllowedTools = appendUniqueRecoveryStrings(append(packet.AllowedTools, recoveryHint.ToolNames...))
		}
	}
	return packet
}

func failureClassForObservation(observation turnObservation) string {
	if observation.Failure != nil && strings.TrimSpace(observation.Failure.FailureClass) != "" {
		return strings.TrimSpace(observation.Failure.FailureClass)
	}
	combinedText := strings.ToLower(strings.TrimSpace(observation.FailureCode() + " " + observation.FailureStage() + " " + observation.FailureSummary() + " " + observation.ContentText()))
	switch {
	case strings.Contains(combinedText, "quality") || strings.Contains(combinedText, "build-quality") || strings.Contains(combinedText, "visual") || strings.Contains(combinedText, "overflow"):
		return failureClassQuality
	case strings.Contains(combinedText, "workspace") || strings.Contains(combinedText, "directory") || strings.Contains(combinedText, "cwd") || strings.Contains(combinedText, "getcwd"):
		return failureClassWorkspace
	case strings.Contains(combinedText, "modulenotfound") || strings.Contains(combinedText, "module not found") || strings.Contains(combinedText, "dependency") || strings.Contains(combinedText, "package"):
		return failureClassDependency
	case strings.Contains(combinedText, "permission") || strings.Contains(combinedText, "access_denied") || strings.Contains(combinedText, "denied"):
		return failureClassPermission
	case strings.Contains(combinedText, "schema") || strings.Contains(combinedText, "json") || strings.Contains(combinedText, "invalid_input"):
		return failureClassSchema
	case strings.Contains(combinedText, "rate_limited") || strings.Contains(combinedText, "too_many"):
		return failureClassProviderLimit
	case strings.Contains(combinedText, "network") || strings.Contains(combinedText, "timeout") || strings.Contains(combinedText, "connection"):
		return failureClassNetwork
	default:
		return failureClassUnknown
	}
}

func retryPolicyForObservation(observation turnObservation) string {
	if observation.Failure != nil && strings.TrimSpace(observation.Failure.RetryPolicy) != "" {
		return strings.TrimSpace(observation.Failure.RetryPolicy)
	}
	if len(requiredPreconditionsForObservation(observation)) > 0 {
		return retryPolicyAfterPrecondition
	}
	if observation.Failure != nil && !observation.Failure.Retryable {
		return retryPolicyDoNotRetry
	}
	return retryPolicyDifferentInput
}

func recoveryWhatFailed(observation turnObservation) string {
	parts := []string{}
	if strings.TrimSpace(observation.Tool) != "" {
		parts = append(parts, "tool="+strings.TrimSpace(observation.Tool))
	}
	if observation.FailureStage() != "" {
		parts = append(parts, "stage="+observation.FailureStage())
	}
	if observation.FailureCode() != "" {
		parts = append(parts, "code="+observation.FailureCode())
	}
	return strings.Join(parts, " ")
}

func recoveryWhyLikely(observation turnObservation, failureClass string) string {
	if observation.FailureSummary() != "" {
		return observation.FailureSummary()
	}
	switch failureClass {
	case failureClassQuality:
		return "A generated artifact failed a deterministic quality gate."
	case failureClassWorkspace:
		return "The tool used a missing, stale, or inaccessible workspace path."
	case failureClassDependency:
		return "The tool command is missing a runtime dependency or used the wrong runtime wrapper."
	default:
		return strings.TrimSpace(observation.ContentText())
	}
}

func recoveryForbiddenRepeats(observation turnObservation) []string {
	if strings.TrimSpace(observation.Tool) == "" {
		return nil
	}
	if len(requiredPreconditionsForObservation(observation)) == 0 {
		return []string{"Do not repeat " + strings.TrimSpace(observation.Tool) + " with the same input fingerprint."}
	}
	return []string{"Do not repeat " + strings.TrimSpace(observation.Tool) + " until requiredPreconditions are satisfied."}
}

func recoveryEvidenceNeeded(observation turnObservation) []string {
	evidence := []string{}
	for _, precondition := range requiredPreconditionsForObservation(observation) {
		evidence = append(evidence, precondition+" evidence")
	}
	if len(evidence) == 0 {
		evidence = append(evidence, "different tool/input/route evidence")
	}
	return appendUniqueRecoveryStrings(evidence)
}

func recoveryMustDoNext(observation turnObservation, failureClass string) []string {
	if observation.Failure != nil && len(observation.Failure.RecoveryHints) > 0 {
		steps := []string{}
		for _, recoveryHint := range observation.Failure.RecoveryHints {
			if strings.TrimSpace(recoveryHint.Action) != "" {
				steps = append(steps, strings.TrimSpace(recoveryHint.Action))
			}
			if strings.TrimSpace(recoveryHint.Reason) != "" {
				steps = append(steps, strings.TrimSpace(recoveryHint.Reason))
			}
		}
		if len(steps) > 0 {
			return appendUniqueRecoveryStrings(steps)
		}
	}
	switch failureClass {
	case failureClassQuality:
		return []string{"Inspect the failed output or source.", "Change the artifact source or route before retrying.", "Retry the failed tool only after relevant change evidence exists."}
	case failureClassWorkspace:
		return []string{"Inspect the workspace facts.", "Change or repair the inaccessible path before retrying.", "Retry only after workspace_changed evidence exists."}
	case failureClassDependency:
		return []string{"Inspect the dependency failure.", "Change runtime setup, command, cache, or route before retrying.", "Retry only after dependency_changed evidence exists."}
	default:
		return []string{"Change tool input, route, or use an adjacent tool before retrying."}
	}
}

func requiredPreconditionsForObservation(observation turnObservation) []string {
	if observation.Failure == nil {
		return nil
	}
	return appendUniqueRecoveryStrings(observation.Failure.RequiredPreconditions)
}

func recoveryChoiceIsAllowed(failureDebt FailureDebt, observations []turnObservation, toolName string) (bool, string) {
	failedObservation := failureDebt.LatestFailure
	trimmedToolName := strings.TrimSpace(toolName)
	if strings.TrimSpace(failedObservation.Tool) == trimmedToolName {
		missingPreconditions := missingRecoveryPreconditions(failedObservation, observations)
		if len(missingPreconditions) > 0 {
			return false, "Retrying " + trimmedToolName + " requires evidence first: " + strings.Join(missingPreconditions, ", ")
		}
	}
	return true, ""
}

func missingRecoveryPreconditions(failedObservation turnObservation, observations []turnObservation) []string {
	missing := []string{}
	for _, precondition := range requiredPreconditionsForObservation(failedObservation) {
		if !recoveryPreconditionSatisfied(precondition, failedObservation, observations) {
			missing = append(missing, precondition)
		}
	}
	return missing
}

func recoveryPreconditionSatisfied(precondition string, failedObservation turnObservation, observations []turnObservation) bool {
	normalizedPrecondition := strings.TrimSpace(precondition)
	for _, observation := range observationsAfterFailure(failedObservation, observations) {
		if observation.Failed() || observation.Action != "continue" {
			continue
		}
		switch normalizedPrecondition {
		case "source_changed":
			if observation.Tool == "file.write" || observation.Tool == "file.edit" || observation.Tool == "file.patch" || observation.Tool == "file.promote" {
				return true
			}
		case "workspace_repaired":
			if observation.Tool == "site.app.repair" || observation.Tool == "site.app.status" {
				return true
			}
		case "dependency_changed":
			if observation.Tool == "terminal.run" && terminalInputLooksLikeDependencyChange(observation.ToolInputKey) {
				return true
			}
		case "inspected_failure":
			if observation.Tool == "file.read" || observation.Tool == "tool.describe" || observation.Tool == "site.app.status" {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func observationsAfterFailure(failedObservation turnObservation, observations []turnObservation) []turnObservation {
	for index, observation := range observations {
		if observation.ObservationID == failedObservation.ObservationID {
			return observations[index+1:]
		}
	}
	return nil
}

func terminalInputLooksLikeDependencyChange(toolInputKey string) bool {
	normalizedInput := strings.ToLower(toolInputKey)
	for _, fragment := range []string{" install", " bun install", "npm install", "pip install", "uv pip", "pnpm install", "yarn install"} {
		if strings.Contains(normalizedInput, fragment) {
			return true
		}
	}
	return false
}

func recoveryPacketContent(packet RecoveryPacket) string {
	document, errorValue := json.Marshal(packet)
	if errorValue != nil {
		return "RecoveryPacket unavailable."
	}
	return "RecoveryPacket:\n" + string(document)
}

func appendUniqueRecoveryStrings(values []string) []string {
	seenValues := map[string]bool{}
	uniqueValues := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" || seenValues[trimmedValue] {
			continue
		}
		seenValues[trimmedValue] = true
		uniqueValues = append(uniqueValues, trimmedValue)
	}
	return uniqueValues
}
