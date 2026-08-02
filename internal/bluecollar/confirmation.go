package bluecollar

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/model"
)

type ExecutionPlan struct {
	OriginalInstruction     string   `json:"originalInstruction"`
	Summary                 string   `json:"summary"`
	Targets                 []string `json:"targets"`
	Schedule                string   `json:"schedule"`
	StartAt                 string   `json:"startAt"`
	EndAt                   string   `json:"endAt"`
	Cadence                 string   `json:"cadence"`
	ExternalSend            bool     `json:"externalSend"`
	ThirdPartyExternalSend  bool     `json:"thirdPartyExternalSend"`
	Repeated                bool     `json:"repeated"`
	HighFrequency           bool     `json:"highFrequency"`
	Destructive             bool     `json:"destructive"`
	PermissionChange        bool     `json:"permissionChange"`
	PublicDeploy            bool     `json:"publicDeploy"`
	PaidAction              bool     `json:"paidAction"`
	MissingInformation      []string `json:"missingInformation"`
	ContinuationInstruction string   `json:"continuationInstruction"`
}

type ConfirmationPolicyDecision struct {
	RequiresConfirmation  bool   `json:"requiresConfirmation"`
	RequiresClarification bool   `json:"requiresClarification"`
	Reason                string `json:"reason"`
}

type ConfirmationReplyDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

type ChoiceReplyRequest struct {
	Question      string
	Options       []ChoiceReplyOption
	SelectionMode string
	Reply         string
}

type ChoiceReplyDecision struct {
	Status  string   `json:"status"`
	Choice  string   `json:"choice,omitempty"`
	Choices []string `json:"choices,omitempty"`
}

func (agentKernel *AgentKernel) BuildExecutionPlan(responseContext context.Context, request AgentRequest, requiredEvidenceTools []string) (ExecutionPlan, error) {
	if agentKernel.languageModel == nil {
		return ExecutionPlan{}, errors.New("language model provider is not configured")
	}
	structuredResponse, errorValue := agentKernel.languageModel.GenerateStructuredResponse(
		responseContext,
		model.StructuredResponseRequest{
			Messages: confirmationPlanMessages(request, requiredEvidenceTools),
			StructuredOutputSchema: model.StructuredOutputSchema{
				Name:               "blueclaw_execution_plan",
				Document:           executionPlanSchema(),
				IsStrictlyEnforced: true,
			},
		},
	)
	if errorValue != nil {
		return ExecutionPlan{}, errorValue
	}
	var executionPlan ExecutionPlan
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &executionPlan); errorValue != nil {
		return ExecutionPlan{}, errorValue
	}
	executionPlan.OriginalInstruction = firstNonEmptyString(executionPlan.OriginalInstruction, request.Prompt)
	executionPlan.Summary = strings.TrimSpace(executionPlan.Summary)
	executionPlan.ContinuationInstruction = strings.TrimSpace(executionPlan.ContinuationInstruction)
	return executionPlan, nil
}

func EvaluateConfirmationPolicy(executionPlan ExecutionPlan) ConfirmationPolicyDecision {
	if len(trimNonEmptyConfirmationStrings(executionPlan.MissingInformation)) > 0 {
		return ConfirmationPolicyDecision{RequiresClarification: true, Reason: "missing_information"}
	}
	if executionPlan.Repeated && executionPlan.ThirdPartyExternalSend && strings.TrimSpace(executionPlan.EndAt) == "" {
		return ConfirmationPolicyDecision{RequiresClarification: true, Reason: "repeated_external_send_needs_end"}
	}
	if executionPlan.HighFrequency && executionPlan.Repeated && strings.TrimSpace(executionPlan.EndAt) == "" {
		return ConfirmationPolicyDecision{RequiresClarification: true, Reason: "high_frequency_repeat_needs_end"}
	}
	if executionPlan.ThirdPartyExternalSend || (executionPlan.ExternalSend && executionPlan.Repeated) {
		return ConfirmationPolicyDecision{RequiresConfirmation: true, Reason: "external_send"}
	}
	if executionPlan.HighFrequency || executionPlan.Destructive || executionPlan.PermissionChange || executionPlan.PublicDeploy || executionPlan.PaidAction {
		return ConfirmationPolicyDecision{RequiresConfirmation: true, Reason: "risky_side_effect"}
	}
	return ConfirmationPolicyDecision{}
}

func (agentKernel *AgentKernel) GenerateClarificationMessage(responseContext context.Context, request AgentRequest, executionPlan ExecutionPlan, decision ConfirmationPolicyDecision) (string, error) {
	return agentKernel.generateConfirmationUserMessage(responseContext, request, executionPlan, decision, "clarification")
}

func (agentKernel *AgentKernel) generateConfirmationUserMessage(responseContext context.Context, request AgentRequest, executionPlan ExecutionPlan, decision ConfirmationPolicyDecision, messageKind string) (string, error) {
	if agentKernel.languageModel == nil {
		return "", errors.New("language model provider is not configured")
	}
	planDocument, _ := json.Marshal(executionPlan)
	decisionDocument, _ := json.Marshal(decision)
	structuredResponse, errorValue := agentKernel.languageModel.GenerateStructuredResponse(
		responseContext,
		model.StructuredResponseRequest{
			Messages: []model.Message{
				{Role: "system", Content: "Write one concise user-facing message for Blueclaw. Do not expose JSON, task IDs, or internal tool names."},
				{Role: "system", Content: responseLanguageInstruction(request.ResponseLanguage)},
				{Role: "system", Content: buildTemporalContextDescription(request.TurnStartedAt)},
				{Role: "system", Content: "For confirmation, state what you understood, how it will run, the target, and that approval will proceed. Mention repeat, start, or end conditions only when they are present in the execution plan or original request. For clarification, ask only for the missing information needed before execution."},
				{Role: "user", Content: strings.Join([]string{
					"Message kind: " + messageKind,
					"Original request: " + strings.TrimSpace(request.Prompt),
					"Execution plan: " + string(planDocument),
					"Policy decision: " + string(decisionDocument),
				}, "\n")},
			},
			StructuredOutputSchema: model.StructuredOutputSchema{
				Name:               "blueclaw_confirmation_message",
				Document:           `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"],"additionalProperties":false}`,
				IsStrictlyEnforced: true,
			},
		},
	)
	if errorValue != nil {
		return "", errorValue
	}
	var replyDocument struct {
		Reply string `json:"reply"`
	}
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &replyDocument); errorValue != nil {
		return "", errorValue
	}
	reply := strings.TrimSpace(replyDocument.Reply)
	if reply == "" {
		return "", errors.New("confirmation reply is empty")
	}
	return reply, nil
}

func (agentKernel *AgentKernel) ResolveChoiceReply(responseContext context.Context, request ChoiceReplyRequest) (ChoiceReplyDecision, error) {
	if agentKernel.languageModel == nil {
		return ChoiceReplyDecision{}, errors.New("language model provider is not configured")
	}
	structuredResponse, errorValue := agentKernel.languageModel.GenerateStructuredResponse(
		responseContext,
		model.StructuredResponseRequest{
			Messages: choiceReplyMessages(request),
			StructuredOutputSchema: model.StructuredOutputSchema{
				Name:               "blueclaw_choice_reply_decision",
				Document:           choiceReplySchema(request),
				IsStrictlyEnforced: true,
			},
		},
	)
	if errorValue != nil {
		return ChoiceReplyDecision{}, errorValue
	}
	var decision ChoiceReplyDecision
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &decision); errorValue != nil {
		return ChoiceReplyDecision{}, errorValue
	}
	decision.Status = strings.TrimSpace(decision.Status)
	decision.Choice = strings.TrimSpace(decision.Choice)
	decision.Choices = trimNonEmptyConfirmationStrings(decision.Choices)
	return decision, nil
}

func confirmationPlanMessages(request AgentRequest, evidenceHints []string) []model.Message {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		ResponseLanguage: request.ResponseLanguage,
		UserPrompt:       request.Prompt,
		TurnStartedAt:    request.TurnStartedAt,
		VisibleContext:   request.VisibleContext,
		ActiveGoal:       request.ActiveGoal,
		ExtraSections:    []string{"Selected skill evidence hints, not requirements: " + strings.Join(evidenceHints, ", ")},
	})
	return []model.Message{
		{Role: "system", Content: strings.Join([]string{
			"You create a structured execution plan before Blueclaw performs risky or recurring work.",
			"Classify side effects accurately. External sends include DM, email, Slack, Mattermost, and messages to people or channels.",
			"Set highFrequency true for repeats more frequent than hourly.",
			"Set missingInformation for required target, end condition, count, or time details that are absent.",
			"Do not invent schedule, startAt, endAt, or cadence. Leave them empty unless the latest request explicitly asks for scheduled, delayed, recurring, repeated, or future work.",
			"Do not ask the user here. Only return the structured plan.",
		}, "\n")},
		{Role: "system", Content: responseLanguageInstruction(request.ResponseLanguage)},
		{Role: "system", Content: contextText},
		{Role: "user", Content: strings.TrimSpace(request.Prompt)},
	}
}

func choiceReplyMessages(request ChoiceReplyRequest) []model.Message {
	optionLines := []string{}
	for index, option := range request.Options {
		optionLines = append(optionLines, strings.TrimSpace(option.Key)+" / "+strconv.Itoa(index+1)+". "+strings.TrimSpace(option.Label))
	}
	return []model.Message{
		{Role: "system", Content: strings.Join([]string{
			"Resolve the latest user reply against a pending choice question.",
			"Return only the short option key from the enum. Do not return labels.",
			"Return ambiguous when the reply could refer to more than one valid option or violates single/multiple selection.",
			"Return unrelated when the reply is a separate request, not an answer to the choice question.",
			"Do not invent ambiguity: casual wording, typos, or an ordinal number that clearly names exactly one listed option resolves to that option.",
		}, "\n")},
		{Role: "system", Content: (LLMContextBuilder{}).Build(LLMContextInput{})},
		{Role: "user", Content: strings.Join([]string{
			"Question: " + strings.TrimSpace(request.Question),
			"Selection mode: " + strings.TrimSpace(request.SelectionMode),
			"Options:",
			strings.Join(optionLines, "\n"),
			"",
			"Latest user reply: " + strings.TrimSpace(request.Reply),
		}, "\n")},
	}
}

func choiceReplySchema(request ChoiceReplyRequest) string {
	optionKeys := []string{}
	for _, option := range request.Options {
		optionKeys = append(optionKeys, strings.TrimSpace(option.Key))
	}
	statusSchema := map[string]any{"type": "string", "enum": []string{"resolved", "ambiguous", "unrelated"}}
	choiceSchema := map[string]any{"type": "string", "enum": optionKeys}
	if strings.TrimSpace(request.SelectionMode) == "multiple" {
		document, errorValue := json.Marshal(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status":  statusSchema,
				"choices": map[string]any{"type": "array", "items": choiceSchema},
			},
			"required":             []string{"status"},
			"additionalProperties": false,
		})
		if errorValue == nil {
			return string(document)
		}
	}
	document, errorValue := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": statusSchema,
			"choice": choiceSchema,
		},
		"required":             []string{"status"},
		"additionalProperties": false,
	})
	if errorValue != nil {
		return `{"type":"object","properties":{"status":{"type":"string","enum":["resolved","ambiguous","unrelated"]}},"required":["status"],"additionalProperties":false}`
	}
	return string(document)
}

func executionPlanSchema() string {
	return `{"type":"object","properties":{"originalInstruction":{"type":"string"},"summary":{"type":"string"},"targets":{"type":"array","items":{"type":"string"}},"schedule":{"type":"string"},"startAt":{"type":"string"},"endAt":{"type":"string"},"cadence":{"type":"string"},"externalSend":{"type":"boolean"},"thirdPartyExternalSend":{"type":"boolean"},"repeated":{"type":"boolean"},"highFrequency":{"type":"boolean"},"destructive":{"type":"boolean"},"permissionChange":{"type":"boolean"},"publicDeploy":{"type":"boolean"},"paidAction":{"type":"boolean"},"missingInformation":{"type":"array","items":{"type":"string"}},"continuationInstruction":{"type":"string"}},"required":["originalInstruction","summary","targets","schedule","startAt","endAt","cadence","externalSend","thirdPartyExternalSend","repeated","highFrequency","destructive","permissionChange","publicDeploy","paidAction","missingInformation","continuationInstruction"],"additionalProperties":false}`
}

func trimNonEmptyConfirmationStrings(values []string) []string {
	trimmedValues := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			trimmedValues = append(trimmedValues, trimmedValue)
		}
	}
	return trimmedValues
}
