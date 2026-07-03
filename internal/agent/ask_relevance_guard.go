package agent

import (
	"encoding/json"
	"strings"
	"unicode"

	"blueclaw/internal/task"
)

// rejectUnrelatedAskAction guards against the model discarding the active
// goal and pausing the task on a question that has nothing to do with what
// the user asked for (observed in production as an ask.input asking "What is
// Gemma" mid-task). It only fires for the narrow, cheap-to-detect case of a
// meta/off-topic question phrasing that shares no vocabulary with the goal;
// anything else — including legitimate disambiguation questions — passes
// through untouched.
func (agentTurnRunner *AgentTurnRunner) rejectUnrelatedAskAction(taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	question, isAskAction := unrelatedAskCandidateQuestion(actionDocument)
	if !isAskAction || question == "" || !looksLikeOffTopicMetaQuestion(question) {
		return toolCallActionOutcome{}
	}
	goalCorpus := goalRelevanceCorpus(request, state.Observations)
	if goalCorpus == "" || sharesSignificantWord(question, goalCorpus) {
		return toolCallActionOutcome{}
	}
	observation := unrelatedAskRedirectObservation(nextObservationID(len(state.Observations)+1), request, question)
	state.Observations = append(state.Observations, observation)
	agentTurnRunner.appendEvent(taskRunID, "agent.unrelated_ask_rejected", marshalEventBody(observation))
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "unrelated_ask_rejected "+actionDocument.ToolName, observation.ContentText())
	result, shouldStop := stopForNoProgress(stepID)
	return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
}

func unrelatedAskCandidateQuestion(actionDocument turnActionDocument) (string, bool) {
	effectiveToolName, effectiveToolInput := effectiveActionToolNameAndInput(actionDocument.ToolName, actionDocument.ToolInput)
	switch strings.TrimSpace(effectiveToolName) {
	case "ask.input", "ask.confirm":
	default:
		return "", false
	}
	var inputFields struct {
		Question string `json:"question"`
	}
	_ = json.Unmarshal(effectiveToolInput, &inputFields)
	return strings.TrimSpace(firstNonEmptyString(actionDocument.Message, inputFields.Question)), true
}

var offTopicMetaQuestionPrefixes = []string{
	"what is ", "what's ", "what are ", "who is ", "who's ", "who are ",
	"무엇", "뭐야", "뭐예요", "뭔가요", "뭐죠", "뭔지", "누구야", "누구예요",
}

func looksLikeOffTopicMetaQuestion(question string) bool {
	normalizedQuestion := strings.ToLower(strings.TrimSpace(question))
	for _, prefix := range offTopicMetaQuestionPrefixes {
		if strings.HasPrefix(normalizedQuestion, prefix) {
			return true
		}
	}
	return false
}

func goalRelevanceCorpus(request AgentTurnRequest, observations []turnObservation) string {
	parts := []string{
		request.ActiveGoal.OriginalInstruction,
		request.ActiveGoal.CurrentObjective,
		strings.Join(request.ActiveGoal.KnownContext, " "),
		strings.Join(request.ActiveGoal.MissingInformation, " "),
		request.Prompt,
	}
	for _, observation := range observations {
		if observation.Action != "continue" || observation.Failed() {
			continue
		}
		parts = append(parts, observation.Summary, observation.ContentText())
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func sharesSignificantWord(question string, corpus string) bool {
	questionWords := significantWords(question)
	if len(questionWords) == 0 {
		return true
	}
	corpusWords := significantWords(corpus)
	for word := range questionWords {
		if corpusWords[word] {
			return true
		}
	}
	return false
}

var offTopicGuardStopwords = map[string]bool{
	"what": true, "whats": true, "who": true, "whos": true, "is": true, "are": true, "was": true, "were": true,
	"the": true, "a": true, "an": true, "this": true, "that": true, "for": true, "with": true, "and": true, "you": true,
	"do": true, "does": true, "did": true, "please": true, "want": true, "know": true, "tell": true, "me": true,
}

func significantWords(text string) map[string]bool {
	words := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	}) {
		if len([]rune(word)) < 2 || offTopicGuardStopwords[word] {
			continue
		}
		words[word] = true
	}
	return words
}

func unrelatedAskRedirectObservation(observationID string, request AgentTurnRequest, question string) turnObservation {
	goalInstruction := strings.TrimSpace(firstNonEmptyString(request.ActiveGoal.OriginalInstruction, request.Prompt))
	message := "The question \"" + question + "\" is not related to the user's actual request: \"" + goalInstruction + "\". Discard this question, do not pause the task for it, and continue working on the original request with the available tools. Only ask the user something that is directly required to complete that request."
	observation := newContentObservation(observationID, "policy", "", marshalEventBody(map[string]string{
		"directive":           message,
		"rejectedQuestion":    question,
		"originalInstruction": goalInstruction,
	}))
	observation.Summary = message
	return observation
}
