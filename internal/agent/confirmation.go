package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"blueclaw/internal/llm"
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

func (agentKernel *AgentKernel) BuildExecutionPlan(responseContext context.Context, request AgentRequest, requiredEvidenceTools []string) (ExecutionPlan, error) {
	if agentKernel.languageModel == nil {
		return ExecutionPlan{}, errors.New("language model provider is not configured")
	}
	structuredResponse, errorValue := agentKernel.languageModel.GenerateStructuredResponse(
		responseContext,
		llm.StructuredResponseRequest{
			Messages: confirmationPlanMessages(request, requiredEvidenceTools),
			StructuredOutputSchema: llm.StructuredOutputSchema{
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

func (agentKernel *AgentKernel) GenerateConfirmationMessage(responseContext context.Context, request AgentRequest, executionPlan ExecutionPlan, decision ConfirmationPolicyDecision) (string, error) {
	return agentKernel.generateConfirmationUserMessage(responseContext, request, executionPlan, decision, "confirmation")
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
		llm.StructuredResponseRequest{
			Messages: []llm.Message{
				{Role: "system", Content: "Write one concise user-facing message for Blueclaw. Do not expose JSON, task IDs, or internal tool names."},
				{Role: "system", Content: responseLanguageInstruction(request.ResponseLanguage)},
				{Role: "system", Content: "For confirmation, state what you understood, how it will run, the target, repeat/start/end conditions, and that approval will proceed. For clarification, ask only for the missing information needed before execution."},
				{Role: "user", Content: strings.Join([]string{
					"Message kind: " + messageKind,
					"Original request: " + strings.TrimSpace(request.Prompt),
					"Execution plan: " + string(planDocument),
					"Policy decision: " + string(decisionDocument),
				}, "\n")},
			},
			StructuredOutputSchema: llm.StructuredOutputSchema{
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

func (agentKernel *AgentKernel) ClassifyConfirmationReply(responseContext context.Context, pendingPrompt string, confirmationQuestion string, reply string) (ConfirmationReplyDecision, error) {
	if agentKernel.languageModel == nil {
		return ConfirmationReplyDecision{}, errors.New("language model provider is not configured")
	}
	structuredResponse, errorValue := agentKernel.languageModel.GenerateStructuredResponse(
		responseContext,
		llm.StructuredResponseRequest{
			Messages: confirmationReplyMessages(pendingPrompt, confirmationQuestion, reply),
			StructuredOutputSchema: llm.StructuredOutputSchema{
				Name:               "blueclaw_confirmation_reply_decision",
				Document:           `{"type":"object","properties":{"decision":{"type":"string","enum":["approved","rejected","modify","question","unrelated"]},"reason":{"type":"string"}},"required":["decision","reason"],"additionalProperties":false}`,
				IsStrictlyEnforced: true,
			},
		},
	)
	if errorValue != nil {
		return ConfirmationReplyDecision{}, errorValue
	}
	var decision ConfirmationReplyDecision
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &decision); errorValue != nil {
		return ConfirmationReplyDecision{}, errorValue
	}
	if strings.TrimSpace(decision.Decision) == "" {
		var legacyDecision ApprovalReplyDecision
		if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &legacyDecision); errorValue == nil && legacyDecision.IsApproval {
			decision.Decision = "approved"
			decision.Reason = legacyDecision.Reason
		}
	}
	decision.Decision = strings.TrimSpace(decision.Decision)
	decision.Reason = strings.TrimSpace(decision.Reason)
	return decision, nil
}

func confirmationPlanMessages(request AgentRequest, requiredEvidenceTools []string) []llm.Message {
	contextDescription := buildVisibleContextDescription(request.VisibleContext)
	if contextDescription == "" {
		contextDescription = "No recent visible context."
	}
	return []llm.Message{
		{Role: "system", Content: strings.Join([]string{
			"You create a structured execution plan before Blueclaw performs risky or recurring work.",
			"Classify side effects accurately. External sends include DM, email, Slack, Mattermost, and messages to people or channels.",
			"Set highFrequency true for repeats more frequent than hourly.",
			"Set missingInformation for required target, end condition, count, or time details that are absent.",
			"Do not ask the user here. Only return the structured plan.",
		}, "\n")},
		{Role: "system", Content: responseLanguageInstruction(request.ResponseLanguage)},
		{Role: "system", Content: "Required evidence tools: " + strings.Join(requiredEvidenceTools, ", ")},
		{Role: "system", Content: contextDescription},
		{Role: "user", Content: strings.TrimSpace(request.Prompt)},
	}
}

func confirmationReplyMessages(pendingPrompt string, confirmationQuestion string, reply string) []llm.Message {
	return []llm.Message{
		{Role: "system", Content: strings.Join([]string{
			"Classify the latest user message against a pending confirmation.",
			"Return approved only when the latest message clearly authorizes the pending action.",
			"Return rejected for cancellation or refusal.",
			"Return modify when the user changes any condition of the pending action.",
			"Return question when the user asks about the pending action.",
			"Return unrelated for a separate new request.",
			"Short Korean affirmatives such as 응, 네, 좋아, 진행해, 해줘, 그래, 해 are approvals only when they answer this confirmation question.",
		}, "\n")},
		{Role: "user", Content: strings.Join([]string{
			"Pending task:",
			strings.TrimSpace(pendingPrompt),
			"",
			"Confirmation question:",
			strings.TrimSpace(confirmationQuestion),
			"",
			"Latest user message:",
			strings.TrimSpace(reply),
		}, "\n")},
	}
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
