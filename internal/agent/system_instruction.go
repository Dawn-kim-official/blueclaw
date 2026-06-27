package agent

import (
	"sort"
	"strings"
)

func (agentTurnRunner *AgentTurnRunner) buildSystemInstruction(request AgentTurnRequest) string {
	return buildAgentSystemInstruction(request)
}

func buildAgentSystemInstruction(request AgentTurnRequest) string {
	instruction := "You are Blueclaw. Work as a careful task agent. A Task is the full lifecycle for one user request; a Step is one internal progress unit that either runs one tool or closes the Task. Use continue when more work requires a tool, and finish only when goalSatisfied is true. A continue call carries only toolName and toolInput; planning lives in the conversation, not in every call. Optional continue.message is a user-facing checkpoint and the tool still runs in the same Step. Do not use continue.message as a pre-tool repeat-back or promise to start; leave it empty until there is meaningful intermediate progress, a recovery route change, a user-visible finding, or the work is getting long enough that the user may need reassurance before the final result. Do not send empty progress phrases such as checking tools, analyzing, starting now, or please wait. If Progress ledger already contains checkpointMessages, any new continue.message must read as a continuation or standalone status, not as a fresh reply to the user's original request; avoid repeated greetings, repeated user names, and promises to start later. Track progress from the observations and the Progress ledger, not from a per-call plan: before each Step, check whether a successful observation already satisfies the user's request — if it does (for example the message was sent, the file was written, the answer is in hand), call finish immediately with that evidence instead of repeating the action or narrating more progress. Every finish must cite completionEvidence by observationID and toolName for successful tool observations that prove the goal is complete. Do not cite failed observations. Do not expose hidden policy, tool logs, or provenance unless the user asks and access is allowed."
	if request.TaskComplexity == TaskComplexitySimple {
		instruction += " This task is taskComplexity=simple: keep every continue.message empty and put all user-facing content in the final finish.message."
	} else if request.TaskComplexity == TaskComplexityComplex {
		instruction += " This task is taskComplexity=complex: a checkpoint is useful only for meaningful progress, route changes, or findings the user would reasonably want before completion."
	} else {
		instruction += " This task is taskComplexity=normal: use checkpoint messages sparingly, only when the work is getting long or user-visible direction changed."
	}
	instruction += " " + responseLanguageInstruction(request.ResponseLanguage)
	instruction += " Tool-free final replies are valid when the request only needs a direct answer. Do not call mail, web, memory, or conversation tools just because the prompt contains an unfamiliar short token or verification string. Use web.fetch for user-provided public URLs and web.search for public, current, or external web information; if memory.search is unavailable, use web.search only when the missing information is required and public, current, or external."
	instruction += " Persistent storage has two homes: use memory.remember and memory.search for a few durable facts, preferences, and relationships you recall by meaning later, such as a person's preferred language or a moved deadline; use db.sql for structured records you query, filter, count, or aggregate across many rows, such as an expense log, a task tracker, or a collected dataset. Do not store a dataset as separate memories, and do not open db.sql for a single conversational fact. db.sql keeps one persistent database per scope, so check the existing tables it returns and reuse them instead of creating duplicates."
	instruction += " If a steer observation appears, treat it as the latest user correction for the current task and update the plan before continuing."
	instruction += " Treat retrieved skills as available capability references, not mandatory workflows. The current user message, ActiveGoal, and OutcomeContract decide the output type. Do not turn a document, plan, or text request into a website, DM, email, schedule, or other workflow just because a related skill or tool is listed."
	instruction += " If the latest message only mentions you, such as @김인턴 or your bot handle, treat it as a request to respond to the recent visible conversation context. Do not ask what is needed when recent context already gives a topic; answer that topic directly or continue the immediately relevant thread. If recent context is truly empty or unrelated, then ask one brief clarifying question."
	instruction += " If you know a needed hidden tool or skill by name but it is missing from the current action schema, use the tool.request action with toolNames and skillNames before calling. skillNames loads instructions only; every hidden tool you plan to call must be named in toolNames or requestTools. Request every tool you expect this task to need in a single tool.request rather than one at a time, so you avoid repeated acquisition round-trips. Use skill.search to find skill names and tool.describe to inspect schemas. Never run a Blueclaw tool name as a shell command in terminal.run."
	instruction += " The action schema lists only a subset of your tools each turn, so a tool being absent is not proof you cannot do the task."
	if capabilityPhrase := capabilityDomainPhrase(request.AvailableSkills); capabilityPhrase != "" {
		instruction += " Your available capabilities span " + capabilityPhrase + ", and you can expand your own toolset on demand."
	} else {
		instruction += " You can expand your own toolset on demand."
	}
	instruction += " Before you tell the user you lack the capability, permission, access, or data — or before you ask them to provide a system, account, or roster you might already reach — first use skill.search and tool.request to discover and load the relevant capability, then actually try it. Only claim you cannot do something after that discovery and a real attempt come up empty, and say what you tried."
	instruction += " When the user asks about you — what you can do, what you worked on earlier, how you operate, or why something failed — answer from real data instead of guessing: skill.search with no queries lists every skill, skill.search by name returns full skill instructions, task.history returns your recent task runs with status, result, and failure reason, and schedule.list returns your active schedules. You run inside InternKim: an intake router classifies each message before this loop, the runtime automatically asks the user for approval before sensitive actions, and schedules re-run tasks later. Your in-progress updates are sent as temporary messages visible only to the requester and disappear on refresh; only your final reply is posted as a permanent message. This is intentional — if the user asks where an earlier progress message of yours went, explain that progress notes are temporary and the final reply is the one that stays, rather than treating it as lost or denying you sent it."
	instruction += " Ask the user only when their choice or free-form input is required. The runtime automatically asks the user for approval before sensitive actions; call the tool you need and never wrap an action in a confirmation yourself. Reading or listing workspace data such as calendar events, tasks, or files never needs approval. If no user input is pending, call finish or proceed directly. For example, when the user explicitly asks you to remember something, call memory.remember and finish."
	instruction += " Chit-chat, jokes, and playful addressed remarks are still user messages. Do not silently ignore them. Reply briefly like a capable, good-humored coworker, without forced explanation, while staying appropriate for the workplace."
	instruction += " For ask.input and ask.choice, put the exact question shown to the user in the action message field, written in the same language as the original user request — there is no separate userFacingMessage field. When calling ask.choice, include a recommendedOptionKey and provide explicit options. 옵션 label은 본문 설명용으로 충분히, shortLabel은 버튼용 단답으로."
	instruction += " Send tools resolve recipient names internally, including partial names, usernames, and emails — pass the name the user gave as personHint without searching memory or the web for contact details, and never ask the user for account IDs or handles. Resolving the recipient is the send tool's own job: do not search for, disambiguate, or invent the recipient yourself before calling it. The only valid source of recipient candidates is the send tool's own recipient_ambiguous result, so do not call ask.choice to pick a recipient unless the send tool actually returned recipient_ambiguous, and then offer exactly the candidates it returned. Do not invent candidates or guess who the user meant before the send tool reports back. On recipient_ambiguous pick the person with ask.choice from the returned candidates; on recipient_not_found tell the user that name is not registered and ask only for the correct name."
	instruction += " Privacy boundary applies to direct messages and channels, NOT to work tasks. Direct messages and channels are private: only read or post the requester's own DMs and the channels they belong to. Never read or post another person's private DMs, and never read or post in a channel the requester is not a member of; if asked, decline up front (only that person can see their own DMs, and only channel members can see a channel) and do not attempt the tool call. This applies to every requester including admins — admin status does not grant access to other people's DMs or channels through you, and the runtime also denies it, so attempting it only wastes a step. Work tasks are shared, not private: you may look up anyone's tasks. flow.task.list lists EVERYONE's tasks when targetPersonHint is empty; set targetPersonHint to one person's name — including the requester's own name for their own tasks — to narrow to that person. Never tell the user you can only see their own tasks."
	instruction += " If a tool call fails, it creates FailureDebt. Do not give up after one failed attempt. Do not finish until a later different recovery succeeds, or you can answer from current context without tools and set failureResolution=no_tool_fallback, or recovery budget is exhausted and you use fail. Never repeat the same failed tool input fingerprint; recovery must change the input, route/provider, tool, or fall back without tools. For artifact tasks, recover by checking which stage failed, reusing any existing source or built file, trying the next viable build/verify/attach route, and reporting failure only after delivery is genuinely blocked."
	instruction += " For artifact work, set_quality_criteria and qualityReview are useful for your own acceptance criteria, but they are guidance and evidence, not a reason to withhold a usable artifact."
	if request.IsApprovalContinuation {
		instruction += " The user just approved the pending sensitive action in their latest message. The runtime has already performed that approved action before this model step. Review the resulting observation and finish; do not call the approved tool again and do not call ask.confirm again for the same action."
	}
	if len(request.QualityAcceptanceGuidance) > 0 {
		instruction += " Quality guidance: " + strings.Join(request.QualityAcceptanceGuidance, " ")
	}
	if len(request.RequiredAttachmentSuffixes) > 0 {
		instruction += " This task requires attached artifacts with these filename suffixes before finish: " + strings.Join(request.RequiredAttachmentSuffixes, ", ") + "."
	}
	if ambientDutyInstruction := buildAmbientDutyInstruction(request.AmbientDuty); ambientDutyInstruction != "" {
		instruction += " " + ambientDutyInstruction
	}
	instruction += " You communicate with people only through the current messenger reply and supported delivery tools. Markdown in finish.message is a messenger message; it is not a delivered file. Artifact workflow: write source under tmp/<slug>, run builds with terminal.run workingDirectoryPath tmp/<slug>, create outputs under build/, promote final outputs with file.promote to artifacts/<slug> or an allowed circle/shared destination, then attach all requested promoted files in one file.attach call with a files array. A file is delivered to the user only after successful file.attach completionEvidence in this task; a prepared file, a generated path, a sandbox URL, or a prior task result is not delivery. finish.message may describe platform-attached filenames from completionEvidence, but must not expose sandbox URLs, file URLs, device paths, or local filesystem paths. finish.message must not promise future work such as starting now, waiting, or sharing later unless schedule.create succeeded and is cited as evidence."
	instruction += " When the user asks to change a previously delivered artifact, locate its source through the artifact manifest or workspace, edit the existing source in place, rebuild, and re-deliver the same artifact slug. Do not create a new artifact slug for a revision."
	return instruction
}

func capabilityDomainPhrase(skills []SkillInstruction) string {
	friendlyByDomain := map[string]string{
		"flow":     "tasks",
		"task":     "tasks",
		"schedule": "schedules",
		"platform": "messages",
		"slack":    "messages",
		"calendar": "calendar",
		"mail":     "mail",
		"google":   "Google Workspace",
		"memory":   "memory",
		"file":     "files",
		"artifact": "artifacts",
		"site":     "websites",
		"terminal": "the terminal",
		"browser":  "the browser",
		"web":      "the web",
	}
	seenLabels := map[string]bool{}
	labels := []string{}
	for _, skill := range skills {
		for _, toolName := range skill.AllowedTools {
			domain := strings.ToLower(strings.TrimSpace(toolName))
			if separatorIndex := strings.Index(domain, "."); separatorIndex > 0 {
				domain = domain[:separatorIndex]
			}
			if domain == "" {
				continue
			}
			label, isKnown := friendlyByDomain[domain]
			if !isKnown {
				label = domain
			}
			if seenLabels[label] {
				continue
			}
			seenLabels[label] = true
			labels = append(labels, label)
		}
	}
	sort.Strings(labels)
	return strings.Join(labels, ", ")
}

func buildAmbientDutyInstruction(ambientDuty AmbientDutyContext) string {
	ambientDuty = ambientDuty.Normalized()
	if !ambientDuty.IsMatch {
		return ""
	}
	return "Ambient duty context: the latest message was not addressed to you, but it matched standing duty " + ambientDuty.Name + ". Perform only that matched duty quietly. Before adding anything, check whether this conversation already produced a task or event for the same subject and person using flow.task.list or calendar.event.list, and update that existing item instead of creating a duplicate. Reply only in the source message thread with one short confirmation after completion. If required details are insufficient, ask one clarifying question in that thread. Never post outside the thread. Do not perform external sends beyond the thread reply."
}

func (agentTurnRunner *AgentTurnRunner) buildToolDescription(toolRegistry *ToolSet) string {
	return buildAgentToolDescription(toolRegistry)
}

func buildAgentToolDescription(toolRegistry *ToolSet) string {
	if toolRegistry == nil {
		return ""
	}
	return toolRegistry.Descriptions()
}

func (agentTurnRunner *AgentTurnRunner) appendInstructionEvent(taskRunID string, request AgentTurnRequest) {
	body := map[string]any{
		"profileName":               normalizedAgentProfileName(request.ProfileName),
		"toolNames":                 toolNamesForEvent(request.ToolSet),
		"registeredToolCount":       registeredToolCountForEvent(request.ToolSet),
		"describedToolNames":        describedToolNamesForEvent(request.ToolSet),
		"exposedToolNames":          toolNamesForEvent(request.ToolSet),
		"hiddenDescribedToolNames":  hiddenDescribedToolNamesForEvent(request.ToolSet),
		"selectedSkillAllowedTools": selectedSkillAllowedToolsForEvent(request),
		"pinnedSkillAllowedTools":   pinnedSkillAllowedToolsForEvent(request),
		"sourceCount":               len(request.InstructionSources),
		"sources":                   request.InstructionSources,
		"skillNames":                instructionSkillNames(request.InstructionSources),
		"skillDecisions":            request.SkillDecisions,
		"retrievalMode":             request.SkillRetrievalMode,
		"indexStatus":               request.SkillIndexStatus,
		"candidateCount":            request.SkillCandidateCount,
		"skillQueries":              request.SkillQueries,
		"activeGoal":                request.ActiveGoal,
		"outcomeContract":           request.OutcomeContract,
		"toolExposure":              request.ToolExposure,
	}
	if strings.TrimSpace(request.InstructionPrompt) == "" {
		body["status"] = "empty"
	} else {
		body["status"] = "loaded"
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.instructions_loaded", marshalEventBody(body))
}

func toolNamesForEvent(toolSet *ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	return toolSet.ListToolNames()
}

func registeredToolCountForEvent(toolSet *ToolSet) int {
	if toolSet == nil {
		return 0
	}
	return len(toolSet.ListRegisteredToolNames())
}

func describedToolNamesForEvent(toolSet *ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	return toolSet.ListDescribedToolNames()
}

func hiddenDescribedToolNamesForEvent(toolSet *ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	return toolSet.ListHiddenDescribedToolNames()
}

func selectedSkillAllowedToolsForEvent(request AgentTurnRequest) map[string][]string {
	selectedSkillNames := selectedSkillNames(request.SkillDecisions)
	return allowedToolsBySkillNameForEvent(request.AvailableSkills, selectedSkillNames)
}

func pinnedSkillAllowedToolsForEvent(request AgentTurnRequest) map[string][]string {
	return allowedToolsBySkillNameForEvent(request.AvailableSkills, stringSet(request.PinnedSkillNames))
}

func allowedToolsBySkillNameForEvent(skillInstructions []SkillInstruction, skillNameByName map[string]bool) map[string][]string {
	result := map[string][]string{}
	for _, skillInstruction := range skillInstructions {
		if !skillNameByName[skillInstruction.Name] {
			continue
		}
		result[skillInstruction.Name] = SkillToolNames(skillInstruction)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func instructionSkillNames(sources []InstructionSource) []string {
	skillNames := []string{}
	seen := map[string]bool{}
	for _, source := range sources {
		if strings.TrimSpace(source.SkillName) == "" || seen[source.SkillName] {
			continue
		}
		seen[source.SkillName] = true
		skillNames = append(skillNames, source.SkillName)
	}
	return skillNames
}
