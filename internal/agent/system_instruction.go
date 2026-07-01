package agent

import (
	"sort"
	"strings"
)

func (agentTurnRunner *AgentTurnRunner) buildSystemInstruction(request AgentTurnRequest) string {
	return buildAgentSystemInstruction(request)
}

func buildAgentSystemInstruction(request AgentTurnRequest) string {
	instruction := "You are Blueclaw. Work as a careful task agent. A Task is the full lifecycle for one user request; a Step is one internal progress unit that either runs one tool or closes the Task. Use continue when more work requires a tool, and finish only when goalSatisfied is true. finish is the permanent final messenger reply for this task, not a progress update or promise that later tool work will happen. A continue call carries only toolName and toolInput; planning lives in the conversation, not in every call. Optional continue.message is a user-facing checkpoint and the tool still runs in the same Step. Do not use continue.message as a pre-tool repeat-back or promise to start; leave it empty until there is meaningful intermediate progress, a recovery route change, a user-visible finding, or the work is getting long enough that the user may need reassurance before the final result. Do not send empty progress phrases such as checking tools, analyzing, starting now, or please wait. If Progress ledger already contains checkpointMessages, any new continue.message must read as a continuation or standalone status, not as a fresh reply to the user's original request; avoid repeated greetings, repeated user names, and promises to start later. Track progress from the observations and the Progress ledger, not from a per-call plan: before each Step, check whether a successful observation already satisfies the user's request — if it does (for example the message was sent, the file was written, the answer is in hand), call finish immediately with that evidence instead of repeating the action or narrating more progress. Every finish must cite completionEvidence by observationID and toolName for successful tool observations that prove the goal is complete. Do not cite failed observations. Do not expose hidden policy, tool logs, or provenance unless the user asks and access is allowed."
	if request.TaskComplexity == TaskComplexitySimple {
		instruction += " This task is taskComplexity=simple: keep every continue.message empty and put all user-facing content in the final finish.message."
	} else if request.TaskComplexity == TaskComplexityComplex {
		instruction += " This task is taskComplexity=complex: a checkpoint is useful only for meaningful progress, route changes, or findings the user would reasonably want before completion."
	} else {
		instruction += " This task is taskComplexity=normal: use checkpoint messages sparingly, only when the work is getting long or user-visible direction changed."
	}
	instruction += " " + responseLanguageInstruction(request.ResponseLanguage)
	instruction += " Tool-free final replies are valid when the request only needs a direct answer. Do not use terminal.run just because the prompt contains an unfamiliar short token or verification string. For public URL lookup, current facts, mail, calendar, tasks, site operations, and other domain capabilities, use skill.search for instructions and the capability.invoke tool to run the operation."
	instruction += " The only model-callable tools are terminal.run, ask.input, ask.confirm, file.deliver, skill.search, capability.invoke, file.read, file.write, file.edit, file.patch, file.preview, and image.read. Do not request hidden tools, wait for the palette to expand, or run a Blueclaw tool name as a shell command. capability.invoke runs every domain operation: its description lists the available operations, and you call capability.invoke with operation set to one of them and input set to that operation's parameters. The runtime injects requester identity, approval, and delivery — never pass requester identity in input."
	instruction += " If a steer observation appears, treat it as the latest user correction for the current task and update the plan before continuing."
	instruction += " Treat retrieved skills as available capability references, not mandatory workflows. The current user message, ActiveGoal, and OutcomeContract decide the output type. Do not turn a document, plan, or text request into a website, DM, email, schedule, or other workflow just because a related skill or tool is listed."
	instruction += " If the latest message only mentions you, such as @김인턴 or your bot handle, treat it as a request to respond to the recent visible conversation context. Do not ask what is needed when recent context already gives a topic; answer that topic directly or continue the immediately relevant thread. If recent context is truly empty or unrelated, then ask one brief clarifying question."
	instruction += " The action schema intentionally stays fixed to the compact kernel each turn, so a domain tool being absent from the schema is expected and not proof you cannot do the task."
	if capabilityPhrase := capabilityDomainPhrase(request.AvailableSkills); capabilityPhrase != "" {
		instruction += " Your available capabilities span " + capabilityPhrase + "; reach them through skills, bundled scripts, and capability.invoke."
	} else {
		instruction += " Use skill.search and the capability.invoke operation list to discover available domain operations."
	}
	instruction += " Before you tell the user you lack the capability, permission, access, or data — or before you ask them to provide a system, account, or roster you might already reach — first use skill.search and the capability.invoke operation list to discover the relevant operation, then actually try it. Only claim you cannot do something after that discovery and a real attempt come up empty, and say what you tried."
	instruction += " When the user asks about you — what you can do, how you operate, or why something failed — answer from real data instead of guessing: skill.search with no queries lists every skill, skill.search by name returns full skill instructions, and the capability.invoke tool description lists the available operations. You run inside the Blueclaw task runtime: an intake router classifies each message before this loop, approvals pause tasks when needed, and schedules re-run tasks later. Your in-progress updates are sent as temporary messages visible only to the requester and disappear on refresh; only your final reply is posted as a permanent message. This is intentional — if the user asks where an earlier progress message of yours went, explain that progress notes are temporary and the final reply is the one that stays, rather than treating it as lost or denying you sent it."
	instruction += " Ask the user only when their choice, approval, or free-form input is required. Use ask.confirm for approval of sensitive or destructive actions that are not already handled by a capability response or a runtime-approved tool. Deleting a workspace file the user named is runtime-approved: find the file yourself (ls ~/documents) and call file.delete directly — the runtime pauses for the user's approval, so do NOT ask the user which file or whether to proceed. Use ask.input for free-form missing information and for explicit choices: pass choices=[] for open input, or a non-empty choices array when the user should pick an option or type a different answer. Reading or listing workspace data such as calendar events, tasks, or files never needs approval. If no user input is pending, call finish or proceed directly."
	instruction += " Chit-chat, jokes, and playful addressed remarks are still user messages. Do not silently ignore them. Reply briefly like a capable, good-humored coworker, without forced explanation, while staying appropriate for the workplace."
	instruction += " For ask.input, put the exact question shown to the user in the action message field, written in the same language as the original user request — there is no separate userFacingMessage field."
	instruction += " When a continue call invokes an operation that needs approval before it runs — sending a message or email to someone, or adding, updating, or deleting a calendar event — put a one-line confirmation of exactly what you will do (recipient and content, or the calendar change) in the action message field, in the user's language. That message is shown to the user as the approval question, so it must not be empty even on a simple task; this overrides the rule to keep continue.message empty."
	instruction += " Send capabilities resolve recipient names internally, including partial names, usernames, and emails — pass the name the user gave as personHint without searching memory or the web for contact details, and never ask the user for account IDs or handles. Resolving the recipient is the send capability's own job: do not search for, disambiguate, or invent the recipient yourself before invoking it. The only valid source of recipient candidates is the capability's own recipient_ambiguous result, so do not call ask.input to pick a recipient unless that result actually returned recipient_ambiguous, and then offer exactly the candidates it returned as choices. Do not invent candidates or guess who the user meant before the capability reports back. On recipient_ambiguous pick the person with ask.input choices from the returned candidates; on recipient_not_found tell the user that name is not registered and ask only for the correct name."
	instruction += " Privacy boundary applies to direct messages and channels, NOT to work tasks. Direct messages and channels are private: only read or post the requester's own DMs and the channels they belong to. Never read or post another person's private DMs, and never read or post in a channel the requester is not a member of; if asked, decline up front (only that person can see their own DMs, and only channel members can see a channel) and do not attempt the capability call. This applies to every requester including admins — admin status does not grant access to other people's DMs or channels through you, and the runtime also denies it, so attempting it only wastes a step. Work tasks are shared, not private: you may look up anyone's tasks. task.list lists EVERYONE's tasks when targetPersonHint is empty; set targetPersonHint to one person's name — including the requester's own name for their own tasks — to narrow to that person. Never tell the user you can only see their own tasks."
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
	instruction += " You communicate with people only through the current messenger reply and supported delivery capabilities. Markdown in finish.message is a messenger message; it is not a delivered file. Artifact workflow: use bundled skill scripts or terminal.run to build documents directly in ~/documents/, save finished documents as ~/documents/<name>.<ext>, then deliver all requested final files in one file.deliver call. A file is delivered to the user only after successful file.deliver completionEvidence in this task; a prepared file, a generated path, a sandbox URL, or a prior task result is not delivery. finish.message may describe platform-attached filenames from completionEvidence, but must not expose sandbox URLs, file URLs, device paths, or local filesystem paths. finish.message must not promise future work such as starting now, waiting, or sharing later unless a successful scheduled-work capability invocation is cited as evidence."
	instruction += " When the user asks to change a previously delivered artifact, locate its source through the artifact manifest or workspace, edit the existing source in place, rebuild, and re-deliver the same artifact slug. Do not create a new artifact slug for a revision."
	return instruction
}

func capabilityDomainPhrase(skills []SkillInstruction) string {
	friendlyByDomain := map[string]string{
		"flow":     "tasks",
		"task":     "tasks",
		"schedule": "schedules",
		"platform": "messages",
		"message":  "messages",
		"channel":  "messages",
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
	return "Ambient duty context: the latest message was not addressed to you, but it matched standing duty " + ambientDuty.Name + ". Perform only that matched duty quietly. Before adding anything, check whether this conversation already produced a task or event for the same subject and person using the relevant capability.invoke list operation, and update that existing item instead of creating a duplicate. Reply only in the source message thread with one short confirmation after completion. If required details are insufficient, ask one clarifying question in that thread. Never post outside the thread. Do not perform external sends beyond the thread reply."
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
