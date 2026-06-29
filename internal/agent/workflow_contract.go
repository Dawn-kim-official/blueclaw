package agent

import "strings"

type workflowContract struct {
	WorkKind                  string
	ActiveGoalToolPrefix      string
	ToolNames                 []string
	PromptMatcher             func(workflowScope) bool
	EvidenceTools             []workflowIntentEvidenceTool
	DefaultEvidenceToolName   string
	EffectRequirements        []workflowIntentEffectRequirement
	DefaultEffectRequirements []OutcomeEffect
}

type workflowIntentEvidenceTool struct {
	ToolName string
	Keywords []string
}

type workflowIntentEffectRequirement struct {
	Requirements []OutcomeEffect
	Keywords     []string
}

type workflowScope struct {
	Prompt     string
	ActiveGoal ActiveGoal
	WorkKinds  []string
	ToolSet    *ToolSet
}

var workflowContracts = []workflowContract{
	{
		WorkKind:             WorkKindSitePrototype,
		ActiveGoalToolPrefix: "site.",
		ToolNames:            KernelToolNames(),
		PromptMatcher:        workflowTextLooksLikeSitePrototypeWork,
		EvidenceTools: []workflowIntentEvidenceTool{
			{
				ToolName: "site.status",
				Keywords: []string{"상태", "확인", "조회", "주소", "링크", "url", "status", "inspect", "check", "link"},
			},
		},
		DefaultEvidenceToolName: "site.publish",
		EffectRequirements: []workflowIntentEffectRequirement{
			{
				Requirements: []OutcomeEffect{{
					ObjectType:         "website",
					Effect:             "read",
					Description:        "current request asks to inspect or return the existing website state",
					SuggestedNextTools: []string{TerminalRunToolName, SkillSearchToolName},
				}},
				Keywords: []string{"상태", "확인", "조회", "주소", "링크", "url", "status", "inspect", "check", "link"},
			},
		},
		DefaultEffectRequirements: []OutcomeEffect{
			{
				ObjectType:         "workspace",
				Effect:             "modified",
				Description:        "current request asks for a website change, so a successful source or workspace modification must be observed",
				SuggestedNextTools: []string{TerminalRunToolName, SkillSearchToolName},
			},
			{
				ObjectType:         "website",
				Effect:             "published",
				Description:        "current request asks for a website change, so a successful current publish must be observed",
				SuggestedNextTools: []string{TerminalRunToolName},
			},
		},
	},
	{
		WorkKind:             WorkKindCalendar,
		ActiveGoalToolPrefix: "calendar.",
		ToolNames:            KernelToolNames(),
	},
	{
		WorkKind:             WorkKindFlowTask,
		ActiveGoalToolPrefix: "task.",
		ToolNames:            KernelToolNames(),
		PromptMatcher:        workflowTextLooksLikeFlowTaskWork,
		EvidenceTools: []workflowIntentEvidenceTool{
			{
				ToolName: "task.list",
				Keywords: []string{"목록", "리스트", "조회", "보여", "찾아", "find", "list", "show"},
			},
			{
				ToolName: "task.update",
				Keywords: []string{"수정", "변경", "완료", "처리", "마감", "상태", "update", "change", "complete", "done"},
			},
		},
		DefaultEvidenceToolName: "task.add",
	},
}

func workflowScopeFromAgentRequest(request AgentRequest) workflowScope {
	return workflowScope{
		Prompt:     request.Prompt,
		ActiveGoal: request.ActiveGoal,
		WorkKinds:  append([]string{}, request.WorkKinds...),
		ToolSet:    request.ToolSet,
	}
}

func workflowScopeFromAgentTurnRequest(request AgentTurnRequest) workflowScope {
	return workflowScope{
		Prompt:     request.Prompt,
		ActiveGoal: request.ActiveGoal,
		WorkKinds:  append([]string{}, request.WorkKinds...),
		ToolSet:    request.ToolSet,
	}
}

func deterministicWorkflowWorkKindsForRequest(request AgentRequest) []string {
	scope := workflowScopeFromAgentRequest(request)
	workKinds := []string{}
	for _, contract := range workflowContracts {
		if workflowPromptMatchesContract(scope, contract) {
			workKinds = appendUniqueStrings(workKinds, contract.WorkKind)
		}
	}
	return workKinds
}

func workflowToolNamesForWorkKinds(toolSet *ToolSet, workKinds []string) []string {
	toolNames := []string{}
	for _, contract := range workflowContracts {
		if workKindsContain(workKinds, contract.WorkKind) {
			toolNames = appendUniqueStrings(toolNames, contract.ToolNames...)
		}
	}
	return registeredToolNamesOnly(toolSet, toolNames)
}

func workflowToolNamesForTurnRequest(request AgentTurnRequest) []string {
	scope := workflowScopeFromAgentTurnRequest(request)
	toolNames := []string{}
	for _, contract := range workflowContracts {
		if workflowScopeMatchesContract(scope, contract) {
			toolNames = appendUniqueStrings(toolNames, contract.ToolNames...)
		}
	}
	if scope.ToolSet == nil {
		return toolNames
	}
	return registeredToolNamesOnly(scope.ToolSet, toolNames)
}

func requestMatchesWorkflowKind(request AgentRequest, workKind string) bool {
	scope := workflowScopeFromAgentRequest(request)
	for _, contract := range workflowContracts {
		if contract.WorkKind == workKind && workflowScopeMatchesContract(scope, contract) {
			return true
		}
	}
	return false
}

func requestPromptMatchesWorkflowKind(request AgentRequest, workKind string) bool {
	scope := workflowScopeFromAgentRequest(request)
	for _, contract := range workflowContracts {
		if contract.WorkKind == workKind && workflowPromptMatchesContract(scope, contract) {
			return true
		}
	}
	return false
}

func requiredWorkflowEvidenceToolsForRequest(request AgentRequest) []string {
	scope := workflowScopeFromAgentRequest(request)
	toolNames := []string{}
	for _, contract := range workflowContracts {
		if !workflowScopeMatchesContract(scope, contract) {
			continue
		}
		toolName := workflowEvidenceToolForScope(scope, contract)
		if toolName == "" || !workflowEvidenceToolCanBeSatisfied(scope.ToolSet, toolName) {
			continue
		}
		toolNames = appendUniqueStrings(toolNames, toolName)
	}
	return toolNames
}

func workflowEvidenceToolCanBeSatisfied(toolSet *ToolSet, toolName string) bool {
	if hasTool(toolSet, toolName) {
		return true
	}
	return hasTool(toolSet, TerminalRunToolName) && strings.Contains(strings.TrimSpace(toolName), ".")
}

func requiredWorkflowEffectRequirementsForRequest(request AgentRequest) []OutcomeEffect {
	scope := workflowScopeFromAgentRequest(request)
	requirements := []OutcomeEffect{}
	for _, contract := range workflowContracts {
		if !workflowScopeMatchesContract(scope, contract) {
			continue
		}
		requirements = appendOutcomeEffects(requirements, workflowEffectRequirementsForScope(scope, contract)...)
	}
	return requirements
}

func workflowEvidenceHintMatchesRequest(toolName string, request AgentRequest) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return false
	}
	scope := workflowScopeFromAgentRequest(request)
	for _, contract := range workflowContracts {
		if !workflowScopeMatchesContract(scope, contract) {
			continue
		}
		if workflowEvidenceToolForScope(scope, contract) == trimmedToolName {
			return true
		}
	}
	return false
}

func workflowScopeMatchesContract(scope workflowScope, contract workflowContract) bool {
	if workKindsContain(scope.WorkKinds, contract.WorkKind) {
		return true
	}
	return contract.ActiveGoalToolPrefix != "" && activeGoalRequiresToolPrefix(scope.ActiveGoal, contract.ActiveGoalToolPrefix)
}

func workflowPromptMatchesContract(scope workflowScope, contract workflowContract) bool {
	if contract.PromptMatcher == nil || !workflowToolsAreAvailable(scope, contract) {
		return false
	}
	return contract.PromptMatcher(scope)
}

func workflowToolsAreAvailable(scope workflowScope, contract workflowContract) bool {
	if contract.ActiveGoalToolPrefix != "" && activeGoalRequiresToolPrefix(scope.ActiveGoal, contract.ActiveGoalToolPrefix) {
		return true
	}
	for _, toolName := range contract.ToolNames {
		if hasTool(scope.ToolSet, toolName) {
			return true
		}
	}
	return false
}

func workflowEvidenceToolForScope(scope workflowScope, contract workflowContract) string {
	text := workflowScopeText(scope)
	for _, evidenceTool := range contract.EvidenceTools {
		if containsAny(text, evidenceTool.Keywords) {
			return evidenceTool.ToolName
		}
	}
	return contract.DefaultEvidenceToolName
}

func workflowEffectRequirementsForScope(scope workflowScope, contract workflowContract) []OutcomeEffect {
	text := workflowScopeText(scope)
	for _, effectRequirement := range contract.EffectRequirements {
		if containsAny(text, effectRequirement.Keywords) {
			return normalizeOutcomeEffects(effectRequirement.Requirements)
		}
	}
	return normalizeOutcomeEffects(contract.DefaultEffectRequirements)
}

func appendOutcomeEffects(effects []OutcomeEffect, candidates ...OutcomeEffect) []OutcomeEffect {
	return normalizeOutcomeEffects(append(append([]OutcomeEffect{}, effects...), candidates...))
}

func workflowScopeText(scope workflowScope) string {
	return strings.ToLower(strings.Join(nonEmptyStrings([]string{
		scope.Prompt,
		scope.ActiveGoal.OriginalInstruction,
		scope.ActiveGoal.CurrentObjective,
	}), "\n"))
}

func workflowTextLooksLikeFlowTaskWork(scope workflowScope) bool {
	text := workflowScopeText(scope)
	if strings.Contains(text, "flow.task") {
		return true
	}
	if !containsAny(text, []string{"업무", "할 일", "할일", "todo", "task"}) {
		return false
	}
	return containsAny(text, []string{
		"등록", "추가", "생성", "만들", "넣어", "기록", "요청", "배정",
		"수정", "변경", "완료", "처리", "삭제", "목록", "리스트", "조회",
		"add", "create", "record", "update", "complete", "done", "list",
	})
}

func workflowTextLooksLikeSitePrototypeWork(scope workflowScope) bool {
	text := workflowScopeText(scope)
	if strings.Contains(text, "site.") {
		return true
	}
	if !containsAny(text, []string{"사이트", "웹사이트", "홈페이지", "랜딩", "website", "site", "landing"}) {
		return false
	}
	return containsAny(text, []string{
		"만들", "생성", "수정", "고쳐", "개선", "빌드", "배포", "공개", "퍼블리시",
		"예쁘", "낫게", "품질", "퀄리티", "create", "build", "edit", "fix",
		"update", "publish", "deploy", "better", "prettier", "quality", "improve",
	})
}
