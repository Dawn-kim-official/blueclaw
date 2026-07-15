package agent

import "strings"

const artifactContractKindFile = "file"
const artifactContractKindSite = "site"
const artifactContractKindSlides = "slides"

type artifactContractRequirement struct {
	Kind   string
	Format string
}

func augmentSkillSearchQuerySetForArtifactContract(querySet SkillSearchQuerySet, request AgentRequest) SkillSearchQuerySet {
	queries := append([]SkillSearchQuery{}, querySet.Queries...)
	for _, description := range artifactSkillSearchDescriptionsForRequest(request) {
		queries = append(queries, SkillSearchQuery{Description: description})
	}
	return normalizeSkillSearchQuerySet(SkillSearchQuerySet{Queries: queries})
}

func artifactSkillSearchQueriesForRequest(request AgentRequest) []SkillSearchQuery {
	queries := []SkillSearchQuery{}
	for _, description := range artifactSkillSearchDescriptionsForRequest(request) {
		queries = append(queries, SkillSearchQuery{Description: description})
	}
	return queries
}

func artifactSkillSearchDescriptionsForRequest(request AgentRequest) []string {
	descriptions := []string{}
	for _, contract := range artifactContractRequirementsForRequest(request) {
		switch contract.Kind {
		case artifactContractKindSite:
			descriptions = append(descriptions, "Create, update, build, review, and publish a website artifact with a public URL.")
		case artifactContractKindFile:
			descriptions = append(descriptions, "Create, verify, and deliver a "+artifactFormatDescription(contract.Format)+" file artifact.")
		case artifactContractKindSlides:
			descriptions = append(descriptions, "Create, verify, and deliver a presentation slide deck artifact.")
		}
	}
	return appendUniqueStrings(nil, descriptions...)
}

func artifactContractRequirementsForRequest(request AgentRequest) []artifactContractRequirement {
	requirements := []artifactContractRequirement{}
	if requestNeedsSiteArtifactContract(request) {
		requirements = appendUniqueArtifactContractRequirements(requirements, artifactContractRequirement{Kind: artifactContractKindSite})
	}
	for _, format := range artifactFormatsForRequest(request) {
		requirements = appendUniqueArtifactContractRequirements(requirements, artifactContractRequirement{Kind: artifactContractKindFile, Format: format})
	}
	if requestNeedsSlidesArtifactContract(request) {
		requirements = appendUniqueArtifactContractRequirements(requirements, artifactContractRequirement{Kind: artifactContractKindSlides})
	}
	return requirements
}

func artifactFormatsForRequest(request AgentRequest) []string {
	formats := []string{}
	formats = appendUniqueStrings(formats, artifactFormatsForAttachmentSuffixes(request.ActiveGoal.OutcomeContract.RequiredAttachmentSuffixes)...)
	for _, result := range request.ActiveGoal.OutcomeContract.ExpectedResults {
		formats = appendUniqueStrings(formats, artifactFormatsForAttachmentSuffixes(result.AcceptanceHints)...)
	}
	return normalizeRequestedOutputFormats(formats)
}

func artifactFormatDescription(format string) string {
	format = strings.TrimPrefix(strings.TrimSpace(format), ".")
	if format == "" {
		return "requested"
	}
	return "." + strings.ToLower(format)
}

func artifactFormatsForAttachmentSuffixes(suffixes []string) []string {
	formats := []string{}
	for _, suffix := range suffixes {
		format := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(suffix)), ".")
		formats = appendUniqueStrings(formats, format)
	}
	return normalizeRequestedOutputFormats(formats)
}

func requestNeedsSiteArtifactContract(request AgentRequest) bool {
	if activeGoalRequiresToolPrefix(request.ActiveGoal, "site.") {
		return true
	}
	if outcomeContractHasSiteEffect(request.ActiveGoal.OutcomeContract) {
		return true
	}
	return expectedResultIncludesType(request.ActiveGoal.OutcomeContract, ExpectedResultTypeLink) && activeGoalMentionsToolPrefix(request.ActiveGoal, "site.")
}

func requestNeedsSlidesArtifactContract(request AgentRequest) bool {
	return outcomeContractMentionsAttachmentSuffix(request.ActiveGoal.OutcomeContract, ".pptx") ||
		outcomeContractMentionsAttachmentSuffix(request.ActiveGoal.OutcomeContract, ".ppt")
}

func outcomeContractHasSiteEffect(contract OutcomeContract) bool {
	for _, effect := range contract.RequiredEffects {
		if strings.EqualFold(strings.TrimSpace(effect.ObjectType), "website") {
			return true
		}
	}
	return false
}

func skillSupportsSiteArtifact(skillInstruction SkillInstruction) bool {
	return skillSupportsToolPrefix(skillInstruction, "site.")
}

func skillSupportsFileDelivery(skillInstruction SkillInstruction) bool {
	return skillHasToolName(skillInstruction, FileDeliverToolName) ||
		skillHasToolName(skillInstruction, ArtifactDeliverToolName) ||
		skillHasToolName(skillInstruction, FileAttachToolName) ||
		skillHasEvidenceTool(skillInstruction, FileDeliverToolName) ||
		skillHasEvidenceTool(skillInstruction, ArtifactDeliverToolName) ||
		skillHasEvidenceTool(skillInstruction, FileAttachToolName) ||
		len(skillInstruction.Completion.RequiredAttachmentSuffixes) > 0
}

func skillSupportsToolPrefix(skillInstruction SkillInstruction, prefix string) bool {
	for _, toolName := range appendUniqueStrings(SkillToolNames(skillInstruction), skillInstruction.Completion.RequiredEvidenceTools...) {
		if strings.HasPrefix(strings.TrimSpace(toolName), prefix) {
			return true
		}
	}
	for _, toolPrefix := range skillInstruction.Activation.ToolPrefixes {
		if strings.HasPrefix(strings.TrimSpace(toolPrefix), prefix) || strings.HasPrefix(prefix, strings.TrimSpace(toolPrefix)) {
			return true
		}
	}
	return false
}

func skillHasToolName(skillInstruction SkillInstruction, toolName string) bool {
	for _, candidate := range SkillToolNames(skillInstruction) {
		if strings.TrimSpace(candidate) == toolName {
			return true
		}
	}
	return false
}

func skillHasEvidenceTool(skillInstruction SkillInstruction, toolName string) bool {
	for _, candidate := range skillInstruction.Completion.RequiredEvidenceTools {
		if strings.TrimSpace(candidate) == toolName {
			return true
		}
	}
	return false
}

func appendUniqueArtifactContractRequirements(requirements []artifactContractRequirement, requirement artifactContractRequirement) []artifactContractRequirement {
	if strings.TrimSpace(requirement.Kind) == "" {
		return requirements
	}
	requirement.Kind = strings.TrimSpace(requirement.Kind)
	requirement.Format = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(requirement.Format)), ".")
	for _, existingRequirement := range requirements {
		if existingRequirement.Kind == requirement.Kind && existingRequirement.Format == requirement.Format {
			return requirements
		}
	}
	return append(requirements, requirement)
}
