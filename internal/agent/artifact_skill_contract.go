package agent

import "strings"

const artifactContractCandidateScore = 1.05
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

func augmentSkillRetrievalResultForArtifactContract(request AgentRequest, skillInstructions []SkillInstruction, result SkillRetrievalResult, limit int) SkillRetrievalResult {
	contracts := artifactContractRequirementsForRequest(request)
	if len(contracts) == 0 {
		return result
	}
	skillInstructionByName := skillInstructionByName(skillInstructions)
	candidateByName := skillCandidateByName(result.SelectedCandidates)
	candidates := append([]SkillCandidate{}, result.SelectedCandidates...)
	queryText := skillSearchQueryText(SkillSearchQuerySet{Queries: artifactSkillSearchQueriesForRequest(request)})
	for _, score := range rankSkillInstructionsByBM25(skillInstructions, nil, queryText) {
		if _, isFound := candidateByName[score.Name]; isFound {
			continue
		}
		skillInstruction, isFound := skillInstructionByName[score.Name]
		if !isFound || !isSkillAllowedForAutomaticRetrieval(skillInstruction, request) {
			continue
		}
		if !skillMatchesAnyArtifactContract(skillInstruction, contracts) {
			continue
		}
		candidate := SkillCandidate{
			Name:   skillInstruction.Name,
			Score:  score.Score + artifactContractCandidateScore,
			Reason: "artifact_contract",
			Source: skillInstruction.Source,
		}
		candidateByName[skillInstruction.Name] = candidate
		candidates = append(candidates, candidate)
	}
	sortSkillCandidates(candidates)
	result.SelectedCandidates = limitSkillCandidates(candidates, limit)
	result.CandidateCount = len(result.SelectedCandidates)
	result.QueryDescriptions = appendUniqueStrings(result.QueryDescriptions, skillSearchQueryDescriptions(SkillSearchQuerySet{Queries: artifactSkillSearchQueriesForRequest(request)})...)
	return result
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

func skillMatchesAnyArtifactContract(skillInstruction SkillInstruction, contracts []artifactContractRequirement) bool {
	for _, contract := range contracts {
		if skillMatchesArtifactContract(skillInstruction, contract) {
			return true
		}
	}
	return false
}

func skillMatchesArtifactContract(skillInstruction SkillInstruction, contract artifactContractRequirement) bool {
	switch contract.Kind {
	case artifactContractKindSite:
		return skillSupportsSiteArtifact(skillInstruction)
	case artifactContractKindFile:
		return skillSupportsFileDelivery(skillInstruction) && skillMentionsArtifactFormat(skillInstruction, contract.Format)
	case artifactContractKindSlides:
		return skillSupportsFileDelivery(skillInstruction) && skillTextContainsAny(skillContractSearchText(skillInstruction), []string{
			"slide", "slides", "deck", "presentation", "presentations", "ppt", "pptx", "powerpoint",
			"슬라이드", "발표", "발표자료", "프레젠테이션", "프리젠테이션", "피피티", "파워포인트",
		})
	default:
		return false
	}
}

func skillSupportsSiteArtifact(skillInstruction SkillInstruction) bool {
	if skillSupportsToolPrefix(skillInstruction, "site.") {
		return true
	}
	text := skillContractSearchText(skillInstruction)
	return skillTextContainsAny(text, []string{
		"website", "web app", "webpage", "homepage", "landing page", "site", "prototype", "public url",
		"웹사이트", "웹 앱", "웹앱", "홈페이지", "사이트", "프로토타입",
	}) && skillTextContainsAny(text, []string{
		"create", "build", "publish", "deploy", "update", "fix", "restore", "delete", "make",
		"생성", "제작", "배포", "게시", "수정", "개선", "삭제", "복구",
	})
}

func skillSupportsFileDelivery(skillInstruction SkillInstruction) bool {
	return skillHasToolName(skillInstruction, FileDeliverToolName) ||
		skillHasToolName(skillInstruction, ArtifactDeliverToolName) ||
		skillHasToolName(skillInstruction, FileAttachToolName) ||
		skillHasEvidenceTool(skillInstruction, FileDeliverToolName) ||
		skillHasEvidenceTool(skillInstruction, ArtifactDeliverToolName) ||
		skillHasEvidenceTool(skillInstruction, FileAttachToolName) ||
		len(skillInstruction.Completion.RequiredAttachmentSuffixes) > 0 ||
		skillTextContainsAny(skillContractSearchText(skillInstruction), []string{"attach", "attachment", "deliverable", "file artifact", "첨부", "파일"})
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

func skillMentionsArtifactFormat(skillInstruction SkillInstruction, format string) bool {
	format = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".")
	if format == "" {
		return false
	}
	for _, suffix := range skillInstruction.Completion.RequiredAttachmentSuffixes {
		if strings.TrimPrefix(strings.ToLower(strings.TrimSpace(suffix)), ".") == format {
			return true
		}
	}
	text := skillContractSearchText(skillInstruction)
	for _, token := range artifactFormatTokens(format) {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func artifactFormatTokens(format string) []string {
	switch strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".") {
	case "docx":
		return []string{"docx", ".docx", "word", "워드"}
	case "pptx":
		return []string{"pptx", ".pptx", "ppt", "powerpoint", "slides", "slide deck", "피피티", "파워포인트", "발표자료"}
	case "xlsx":
		return []string{"xlsx", ".xlsx", "xlsm", "excel", "spreadsheet", "엑셀", "스프레드시트"}
	case "csv":
		return []string{"csv", ".csv", "spreadsheet", "table", "표"}
	case "pdf":
		return []string{"pdf", ".pdf"}
	case "html":
		return []string{"html", ".html", "web page", "webpage"}
	default:
		return []string{strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".")}
	}
}

func skillContractSearchText(skillInstruction SkillInstruction) string {
	return strings.ToLower(strings.Join([]string{
		skillInstruction.Name,
		skillInstruction.Description,
		skillInstruction.WhenToUse,
		skillInstruction.Category,
		strings.Join(skillInstruction.Tags, " "),
		strings.Join(skillInstruction.TriggerHints, " "),
		strings.Join(skillInstruction.Activation.Keywords, " "),
		strings.Join(skillInstruction.Completion.RequiredAttachmentSuffixes, " "),
		strings.Join(skillInstruction.Completion.RequiredEvidenceTools, " "),
		strings.Join(skillInstruction.AllowedTools, " "),
	}, " "))
}

func skillTextContainsAny(text string, values []string) bool {
	for _, value := range values {
		if strings.Contains(text, strings.ToLower(strings.TrimSpace(value))) {
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
