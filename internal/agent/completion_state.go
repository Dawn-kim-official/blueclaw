package agent

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type completionRecommendedAction string

const (
	completionActionContinueWork            completionRecommendedAction = "continue_work"
	completionActionAttachExistingArtifacts completionRecommendedAction = "attach_existing_artifacts"
	completionActionFinalizeWithEvidence    completionRecommendedAction = "finalize_with_evidence"
	completionActionBlockedMissingTool      completionRecommendedAction = "blocked_missing_tool"
	completionActionBlockedInvalidArtifact  completionRecommendedAction = "blocked_invalid_artifact"
)

type CompletionState struct {
	RecommendedAction   completionRecommendedAction   `json:"recommendedAction"`
	Requirements        []CompletionRequirementState  `json:"requirements,omitempty"`
	ExistingArtifacts   []CompletionArtifact          `json:"existingArtifacts,omitempty"`
	AttachedEvidence    []CompletionAttachedEvidence  `json:"attachedEvidence,omitempty"`
	ValidityState       ValidityState                 `json:"validityState,omitempty"`
	MissingRequirements []string                      `json:"missingRequirements,omitempty"`
	EvidenceReferences  []completionEvidenceReference `json:"completionEvidence,omitempty"`
	AttachmentPaths     []string                      `json:"-"`
}

type CompletionRequirementState struct {
	ToolName        string   `json:"toolName,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Suffixes        []string `json:"suffixes,omitempty"`
	MissingSuffixes []string `json:"missingSuffixes,omitempty"`
	Satisfied       bool     `json:"satisfied"`
}

type CompletionArtifact struct {
	Suffix       string    `json:"suffix"`
	RelativePath string    `json:"relativePath"`
	Filename     string    `json:"filename"`
	ModifiedAt   time.Time `json:"modifiedAt"`
	path         string
}

type CompletionAttachedEvidence struct {
	ObservationID string `json:"observationID"`
	ToolName      string `json:"toolName"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	Title         string `json:"title,omitempty"`
	DevicePath    string `json:"-"`
	ContentBase64 string `json:"-"`
}

func buildCompletionState(request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation) CompletionState {
	state := CompletionState{
		RecommendedAction: completionActionContinueWork,
		Requirements:      completionRequirementStates(requirements, observations),
	}
	if len(requirements) == 0 {
		return state
	}
	state.EvidenceReferences = completionEvidenceReferences(requirements, observations)
	state.AttachedEvidence = completionAttachedEvidence(observations, state.EvidenceReferences)
	state.MissingRequirements = missingCompletionRequirements(state.Requirements)
	state.ExistingArtifacts = newestRequiredWorkspaceArtifacts(request.WorkspaceRootPath, requiredFileAttachmentSuffixes(requirements), request.TurnStartedAt)
	state.AttachmentPaths = completionArtifactPaths(state.ExistingArtifacts)
	state.ValidityState = completionValidityState(request, state)
	state.RecommendedAction = recommendedCompletionAction(request, requirements, observations, state)
	return state
}

func completionValidityState(request AgentTurnRequest, state CompletionState) ValidityState {
	if len(state.AttachedEvidence) > 0 {
		return buildAttachedEvidenceValidityState(request.WorkspaceRootPath, state.AttachedEvidence, request.TurnStartedAt)
	}
	if len(state.ExistingArtifacts) > 0 {
		return buildArtifactValidityState(state.ExistingArtifacts)
	}
	return ValidityState{Passed: true}
}

func completionRequirementStates(requirements []toolUseRequirement, observations []turnObservation) []CompletionRequirementState {
	states := []CompletionRequirementState{}
	for _, requirement := range requirements {
		isSatisfied, missingSuffixes := completionRequirementStatus(requirement, observations)
		states = append(states, CompletionRequirementState{
			ToolName:        strings.TrimSpace(requirement.ToolName),
			Reason:          strings.TrimSpace(requirement.Reason),
			Suffixes:        append([]string{}, requirement.AttachmentSuffixes...),
			MissingSuffixes: missingSuffixes,
			Satisfied:       isSatisfied,
		})
	}
	return states
}

func completionRequirementStatus(requirement toolUseRequirement, observations []turnObservation) (bool, []string) {
	references := completionReferencesForRequirement(requirement, observations, successfulObservationReferences(observations))
	if len(references) == 0 {
		return false, append([]string{}, requirement.AttachmentSuffixes...)
	}
	if !requirement.RequiresAttachment {
		return true, nil
	}
	attachments := collectReferenceAttachments(observations, references)
	missingSuffixes := missingRequiredAttachmentSuffixes(attachments, requirement.AttachmentSuffixes)
	return len(missingSuffixes) == 0, missingSuffixes
}

func completionEvidenceReferences(requirements []toolUseRequirement, observations []turnObservation) []completionEvidenceReference {
	references := []completionEvidenceReference{}
	seenReference := map[string]bool{}
	successfulReferences := successfulObservationReferences(observations)
	for _, requirement := range requirements {
		matchingReferences := completionReferencesForRequirement(requirement, observations, successfulReferences)
		if requirement.RequiresAttachment && len(requirement.AttachmentSuffixes) > 0 {
			matchingReferences = completionAttachmentReferencesForSuffixes(observations, matchingReferences, requirement.AttachmentSuffixes)
		}
		for _, reference := range matchingReferences {
			key := completionEvidenceReferenceKey(reference)
			if seenReference[key] {
				continue
			}
			seenReference[key] = true
			references = append(references, reference)
		}
	}
	return references
}

func completionEvidenceReferenceKey(reference completionEvidenceReference) string {
	attachmentIndex := ""
	if reference.AttachmentIndex != nil {
		attachmentIndex = strconv.Itoa(*reference.AttachmentIndex)
	}
	return reference.ObservationID + "\x00" + reference.ToolName + "\x00" + attachmentIndex
}

func completionAttachmentReferencesForSuffixes(observations []turnObservation, references []completionEvidenceReference, suffixes []string) []completionEvidenceReference {
	selectedReferences := []completionEvidenceReference{}
	coveredSuffixes := map[string]bool{}
	for _, reference := range references {
		selectedReferences = append(selectedReferences, attachmentIndexReferencesForSuffixes(observations, reference, suffixes, coveredSuffixes)...)
	}
	return selectedReferences
}

func attachmentIndexReferencesForSuffixes(observations []turnObservation, reference completionEvidenceReference, suffixes []string, coveredSuffixes map[string]bool) []completionEvidenceReference {
	observation, isFound := findSuccessfulObservation(observations, reference)
	if !isFound {
		return nil
	}
	references := []completionEvidenceReference{}
	for _, suffix := range suffixes {
		if coveredSuffixes[suffix] {
			continue
		}
		if reference, isFound := attachmentIndexReferenceForSuffix(observation, suffix); isFound {
			coveredSuffixes[suffix] = true
			references = append(references, reference)
		}
	}
	return references
}

func attachmentIndexReferenceForSuffix(observation turnObservation, suffix string) (completionEvidenceReference, bool) {
	for index, attachment := range observation.Attachments {
		if !attachmentMatchesSuffix(attachment, suffix) {
			continue
		}
		attachmentIndex := index
		return completionEvidenceReference{
			ObservationID:   observation.ObservationID,
			ToolName:        observation.Tool,
			AttachmentIndex: &attachmentIndex,
		}, true
	}
	return completionEvidenceReference{}, false
}

func successfulObservationReferences(observations []turnObservation) []completionEvidenceReference {
	references := []completionEvidenceReference{}
	for _, observation := range observations {
		if observation.Failed() || strings.TrimSpace(observation.Tool) == "" {
			continue
		}
		references = append(references, completionEvidenceReference{
			ObservationID: observation.ObservationID,
			ToolName:      observation.Tool,
		})
	}
	return references
}

func completionAttachedEvidence(observations []turnObservation, references []completionEvidenceReference) []CompletionAttachedEvidence {
	attachedEvidence := []CompletionAttachedEvidence{}
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if !isFound {
			continue
		}
		for _, attachment := range attachmentsForReference(observation, reference) {
			attachedEvidence = append(attachedEvidence, CompletionAttachedEvidence{
				ObservationID: observation.ObservationID,
				ToolName:      observation.Tool,
				Filename:      attachment.Filename,
				ContentType:   attachment.ContentType,
				SizeBytes:     attachment.SizeBytes,
				Title:         attachment.Title,
				DevicePath:    attachment.DevicePath,
				ContentBase64: attachment.ContentBase64,
			})
		}
	}
	return attachedEvidence
}

func missingCompletionRequirements(requirements []CompletionRequirementState) []string {
	missingRequirements := []string{}
	for _, requirement := range requirements {
		if requirement.Satisfied {
			continue
		}
		missingRequirements = append(missingRequirements, completionRequirementStateLabel(requirement))
	}
	return missingRequirements
}

func completionRequirementStateLabel(requirement CompletionRequirementState) string {
	return requirement.ToolName
}

func recommendedCompletionAction(request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, state CompletionState) completionRecommendedAction {
	if len(state.MissingRequirements) == 0 {
		if requestOnlyOpensBrowser(request) {
			return completionActionFinalizeWithEvidence
		}
		if satisfiedOneShotEvidenceRequirementsCanFinalize(request.ToolSet, requirements) {
			return completionActionFinalizeWithEvidence
		}
		if !allRequirementsAreFileAttachments(requirements) {
			return completionActionContinueWork
		}
		if !state.ValidityState.Passed {
			if hasInvalidArtifactObservationForPaths(observations, completionValidityPaths(state)) {
				return completionActionContinueWork
			}
			return completionActionBlockedInvalidArtifact
		}
		return completionActionFinalizeWithEvidence
	}
	if !allMissingRequirementsAreFileAttachments(requirements, state.Requirements) {
		return completionActionContinueWork
	}
	if request.ToolSet == nil || !request.ToolSet.IsAllowed(FileDeliverToolName) {
		return completionActionBlockedMissingTool
	}
	if hasFailedArtifactDeliveryForPaths(observations, state.AttachmentPaths) {
		return completionActionContinueWork
	}
	if requiredArtifactsSatisfyMissingFileAttachments(state.Requirements, state.ExistingArtifacts) {
		if !state.ValidityState.Passed {
			if hasInvalidArtifactObservationForPaths(observations, state.AttachmentPaths) {
				return completionActionContinueWork
			}
			return completionActionBlockedInvalidArtifact
		}
		return completionActionAttachExistingArtifacts
	}
	return completionActionContinueWork
}

func satisfiedOneShotEvidenceRequirementsCanFinalize(toolSet *ToolSet, requirements []toolUseRequirement) bool {
	if len(requirements) == 0 {
		return false
	}
	for _, requirement := range requirements {
		if requirement.RequiresAttachment || !isOneShotCompletionEvidenceTool(toolSet, requirement.ToolName) {
			return false
		}
	}
	return true
}

func isOneShotCompletionEvidenceTool(toolSet *ToolSet, toolName string) bool {
	toolDefinition, isFound := toolDefinitionForName(toolSet, toolName)
	if !isFound || toolDefinition.Completion.Mode != ToolCompletionObservation {
		return false
	}
	return ToolDefinitionRequiresSideEffectEvidence(toolDefinition)
}

func buildAttachedEvidenceValidityState(workspaceRootPath string, attachedEvidence []CompletionAttachedEvidence, minimumModifiedAt time.Time) ValidityState {
	attachments := []FileAttachment{}
	for _, evidence := range attachedEvidence {
		attachments = append(attachments, FileAttachment{
			DevicePath:    evidence.DevicePath,
			Filename:      evidence.Filename,
			ContentType:   evidence.ContentType,
			SizeBytes:     evidence.SizeBytes,
			Title:         evidence.Title,
			ContentBase64: evidence.ContentBase64,
		})
	}
	return buildAttachmentValidityState(workspaceRootPath, attachments)
}

func completionValidityPaths(state CompletionState) []string {
	paths := append([]string{}, state.AttachmentPaths...)
	for _, evidence := range state.AttachedEvidence {
		if strings.TrimSpace(evidence.DevicePath) != "" {
			paths = append(paths, evidence.DevicePath)
		}
	}
	return paths
}

func hasInvalidArtifactObservationForPaths(observations []turnObservation, paths []string) bool {
	for _, observation := range observations {
		if !observation.Failed() || observation.Action != "policy" || observation.PolicyCode != evidenceKindAttachmentValid {
			continue
		}
		if stringSliceContainsAll(observation.RelatedPaths, paths) {
			return true
		}
	}
	return false
}

func allRequirementsAreFileAttachments(requirements []toolUseRequirement) bool {
	if len(requirements) == 0 {
		return false
	}
	for _, requirement := range requirements {
		if !IsArtifactDeliveryTool(requirement.ToolName) || !requirement.RequiresAttachment {
			return false
		}
	}
	return true
}

func hasFailedArtifactDeliveryForPaths(observations []turnObservation, paths []string) bool {
	for _, observation := range observations {
		if !observation.Failed() || !IsArtifactDeliveryTool(observation.Tool) {
			continue
		}
		if stringSliceContainsAll(observation.RelatedPaths, paths) {
			return true
		}
	}
	return false
}

func stringSliceContainsAll(values []string, expectedValues []string) bool {
	if len(expectedValues) == 0 {
		return false
	}
	for _, expectedValue := range expectedValues {
		if !stringSliceContains(values, expectedValue) {
			return false
		}
	}
	return true
}

func allMissingRequirementsAreFileAttachments(requirements []toolUseRequirement, states []CompletionRequirementState) bool {
	for index, state := range states {
		if state.Satisfied {
			continue
		}
		if index >= len(requirements) || !IsArtifactDeliveryTool(requirements[index].ToolName) || !requirements[index].RequiresAttachment {
			return false
		}
	}
	return true
}

func requiredArtifactsSatisfyMissingFileAttachments(requirements []CompletionRequirementState, artifacts []CompletionArtifact) bool {
	artifactBySuffix := map[string]bool{}
	for _, artifact := range artifacts {
		artifactBySuffix[artifact.Suffix] = true
	}
	hasMissingSuffix := false
	for _, requirement := range requirements {
		if requirement.Satisfied {
			continue
		}
		for _, suffix := range requirement.MissingSuffixes {
			hasMissingSuffix = true
			if !artifactBySuffix[suffix] {
				return false
			}
		}
	}
	return hasMissingSuffix
}

func requiredFileAttachmentSuffixes(requirements []toolUseRequirement) []string {
	suffixes := []string{}
	seenSuffix := map[string]bool{}
	for _, requirement := range requirements {
		if !IsArtifactDeliveryTool(requirement.ToolName) || !requirement.RequiresAttachment {
			continue
		}
		for _, suffix := range requirement.AttachmentSuffixes {
			trimmedSuffix := strings.TrimSpace(suffix)
			if trimmedSuffix == "" || seenSuffix[trimmedSuffix] {
				continue
			}
			seenSuffix[trimmedSuffix] = true
			suffixes = append(suffixes, trimmedSuffix)
		}
	}
	return suffixes
}

func newestRequiredWorkspaceArtifacts(workspaceRootPath string, suffixes []string, minimumModifiedAt time.Time) []CompletionArtifact {
	if len(suffixes) == 0 {
		return nil
	}
	candidatesBySuffix, errorValue := workspaceArtifactsBySuffix(workspaceRootPath, suffixes, minimumModifiedAt)
	if errorValue != nil {
		return nil
	}
	artifacts := []CompletionArtifact{}
	for _, suffix := range suffixes {
		candidates := candidatesBySuffix[suffix]
		if len(candidates) == 0 {
			continue
		}
		sort.Slice(candidates, func(leftIndex int, rightIndex int) bool {
			return candidates[leftIndex].ModifiedAt.After(candidates[rightIndex].ModifiedAt)
		})
		artifacts = append(artifacts, candidates[0])
	}
	return uniqueCompletionArtifacts(artifacts)
}

func workspaceArtifactsBySuffix(workspaceRootPath string, suffixes []string, minimumModifiedAt time.Time) (map[string][]CompletionArtifact, error) {
	searchRootPath := strings.TrimSpace(workspaceRootPath)
	if searchRootPath == "" {
		return nil, errors.New("workspace root path is not configured")
	}
	candidatesBySuffix := map[string][]CompletionArtifact{}
	errorValue := filepath.WalkDir(searchRootPath, func(path string, directoryEntry os.DirEntry, walkError error) error {
		if walkError != nil {
			return nil
		}
		if directoryEntry.IsDir() {
			if shouldSkipArtifactDirectory(workspaceRootPath, path) {
				return filepath.SkipDir
			}
			return nil
		}
		return appendArtifactCandidates(candidatesBySuffix, workspaceRootPath, path, directoryEntry, suffixes, minimumModifiedAt)
	})
	if errorValue != nil {
		return nil, errorValue
	}
	return candidatesBySuffix, nil
}

func shouldSkipArtifactDirectory(workspaceRootPath string, path string) bool {
	relativePath := relativeWorkspacePath(workspaceRootPath, path)
	switch relativePath {
	case "skills", ".git", ".cache", ".blueclaw", "node_modules", "tmp":
		return true
	default:
		return strings.HasPrefix(relativePath, "skills"+string(os.PathSeparator)) ||
			strings.HasPrefix(relativePath, ".git"+string(os.PathSeparator)) ||
			strings.HasPrefix(relativePath, ".blueclaw"+string(os.PathSeparator)) ||
			strings.HasPrefix(relativePath, "tmp"+string(os.PathSeparator)) ||
			isPrivateTmpArtifactDirectory(relativePath) ||
			strings.HasPrefix(relativePath, "node_modules"+string(os.PathSeparator))
	}
}

func isPrivateTmpArtifactDirectory(relativePath string) bool {
	parts := strings.Split(filepath.ToSlash(relativePath), "/")
	return len(parts) >= 4 && parts[0] == "private" && parts[1] == "people" && parts[3] == "tmp"
}

func appendArtifactCandidates(candidatesBySuffix map[string][]CompletionArtifact, workspaceRootPath string, path string, directoryEntry os.DirEntry, suffixes []string, minimumModifiedAt time.Time) error {
	for _, suffix := range suffixes {
		if !strings.HasSuffix(path, suffix) {
			continue
		}
		fileInformation, errorValue := directoryEntry.Info()
		if errorValue != nil {
			continue
		}
		if !minimumModifiedAt.IsZero() && fileInformation.ModTime().Before(minimumModifiedAt) {
			continue
		}
		candidatesBySuffix[suffix] = append(candidatesBySuffix[suffix], CompletionArtifact{
			Suffix:       suffix,
			RelativePath: relativeWorkspacePath(workspaceRootPath, path),
			Filename:     filepath.Base(path),
			ModifiedAt:   fileInformation.ModTime(),
			path:         path,
		})
	}
	return nil
}

func relativeWorkspacePath(workspaceRootPath string, path string) string {
	relativePath, errorValue := filepath.Rel(workspaceRootPath, path)
	if errorValue != nil || strings.HasPrefix(relativePath, "..") {
		return filepath.Base(path)
	}
	return relativePath
}

func uniqueCompletionArtifacts(artifacts []CompletionArtifact) []CompletionArtifact {
	uniqueArtifacts := []CompletionArtifact{}
	seenPath := map[string]bool{}
	for _, artifact := range artifacts {
		if artifact.path == "" || seenPath[artifact.path] {
			continue
		}
		seenPath[artifact.path] = true
		uniqueArtifacts = append(uniqueArtifacts, artifact)
	}
	return uniqueArtifacts
}

func completionArtifactPaths(artifacts []CompletionArtifact) []string {
	paths := []string{}
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.path) != "" {
			paths = append(paths, artifact.path)
		}
	}
	return paths
}
