package agentcontract

import "errors"

type IntakeClassification string
type TaskShape string
type TurnRoute string
type ApprovalSignal string
type BusyRoute string
type PriorTaskReference string
type DeliverableKind string

const (
	IntakeClassificationQuickReply        IntakeClassification = "quick_reply"
	IntakeClassificationBoundedTask       IntakeClassification = "bounded_task"
	IntakeClassificationNeedsConfirmation IntakeClassification = "needs_confirmation"
	IntakeClassificationUnsupported       IntakeClassification = "unsupported"

	TaskShapeImmediateReply     TaskShape = "immediate_reply"
	TaskShapeResearchTask       TaskShape = "research_task"
	TaskShapeMaintenanceTask    TaskShape = "maintenance_task"
	TaskShapeScheduledTask      TaskShape = "scheduled_task"
	TaskShapeBrowserHandoffTask TaskShape = "browser_handoff_task"
	TaskShapeApprovalGatedTask  TaskShape = "approval_gated_task"

	TurnRouteContinueTask   TurnRoute = "continue_task"
	TurnRouteReviseTask     TurnRoute = "revise_task"
	TurnRouteAnswerQuestion TurnRoute = "answer_question"
	TurnRouteStartTask      TurnRoute = "start_task"
	TurnRouteAnswerMeta     TurnRoute = "answer_meta"
	TurnRouteClarify        TurnRoute = "clarify"
	TurnRouteConsume        TurnRoute = "consume"
	TurnRouteGiveUp         TurnRoute = "give_up"

	BusyRouteStatus    BusyRoute = "status"
	BusyRouteSteer     BusyRoute = "steer"
	BusyRouteReplace   BusyRoute = "replace"
	BusyRouteCancel    BusyRoute = "cancel"
	BusyRouteNewTask   BusyRoute = "new_task"
	BusyRouteUnrelated BusyRoute = "unrelated"

	PriorTaskReferenceNone            PriorTaskReference = "none"
	PriorTaskReferenceOutcomeRecovery PriorTaskReference = "outcome_recovery"

	ApprovalSignalApprove ApprovalSignal = "approve"
	// approve_task approves the whole family of work for the rest of this task, so
	// the person is asked once instead of at every step of the same job.
	ApprovalSignalApproveTask ApprovalSignal = "approve_task"
	ApprovalSignalReject      ApprovalSignal = "reject"
	ApprovalSignalUnclear     ApprovalSignal = "unclear"

	DeliverableKindWebsite      DeliverableKind = "website"
	DeliverableKindPresentation DeliverableKind = "presentation"
	DeliverableKindDocument     DeliverableKind = "document"
	DeliverableKindNone         DeliverableKind = "none"
)

type ClarificationOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value,omitempty"`
}

type IntakeDecision struct {
	Classification         IntakeClassification  `json:"classification"`
	TaskShape              TaskShape             `json:"taskShape"`
	TaskLevel              TaskLevel             `json:"level"`
	EstimatedMinutes       int                   `json:"estimatedMinutes"`
	RequestedOutputFormats []string              `json:"requestedOutputFormats"`
	DeliverableKind        DeliverableKind       `json:"deliverableKind,omitempty"`
	ExpectedResults        []ExpectedResult      `json:"expectedResults,omitempty"`
	ResponseLanguage       string                `json:"responseLanguage"`
	Reason                 string                `json:"reason"`
	UserFacingReply        string                `json:"userFacingReply"`
	InitialToolNames       []string              `json:"initialToolNames,omitempty"`
	PriorTaskReference     PriorTaskReference    `json:"priorTaskReference,omitempty"`
	ClarificationQuestion  string                `json:"clarificationQuestion,omitempty"`
	ClarificationOptions   []ClarificationOption `json:"clarificationOptions,omitempty"`
}

func (intakeDecision IntakeDecision) Validate() error {
	if NormalizeIntakeClassification(intakeDecision.Classification) == "" {
		return errors.New("intake classification is invalid")
	}
	if NormalizeTaskLevel(string(intakeDecision.TaskLevel)) == "" {
		return errors.New("intake task level is invalid")
	}
	return nil
}

type TurnDecision struct {
	Route                  TurnRoute             `json:"route"`
	Classification         IntakeClassification  `json:"classification"`
	TaskShape              TaskShape             `json:"taskShape"`
	TaskLevel              TaskLevel             `json:"level"`
	EstimatedMinutes       int                   `json:"estimatedMinutes"`
	RequestedOutputFormats []string              `json:"requestedOutputFormats"`
	DeliverableKind        DeliverableKind       `json:"deliverableKind,omitempty"`
	ExpectedResults        []ExpectedResult      `json:"expectedResults,omitempty"`
	ResponseLanguage       string                `json:"responseLanguage"`
	Reason                 string                `json:"reason"`
	UserFacingReply        string                `json:"userFacingReply"`
	InitialToolNames       []string              `json:"initialToolNames,omitempty"`
	PriorTaskReference     PriorTaskReference    `json:"priorTaskReference,omitempty"`
	Approval               *ApprovalSignal       `json:"approval,omitempty"`
	Choices                []string              `json:"choices,omitempty"`
	ClarificationQuestion  string                `json:"clarificationQuestion,omitempty"`
	ClarificationOptions   []ClarificationOption `json:"clarificationOptions,omitempty"`
	ReactionEmojiName      string                `json:"reactionEmojiName,omitempty"`
	BusyRoute              BusyRoute             `json:"busyRoute,omitempty"`
	BusyInstruction        string                `json:"busyInstruction,omitempty"`
}

func (turnDecision TurnDecision) IntakeDecision() IntakeDecision {
	return IntakeDecision{
		Classification:         turnDecision.Classification,
		TaskShape:              turnDecision.TaskShape,
		TaskLevel:              NormalizeTaskLevel(string(turnDecision.TaskLevel)),
		EstimatedMinutes:       turnDecision.EstimatedMinutes,
		RequestedOutputFormats: append([]string{}, turnDecision.RequestedOutputFormats...),
		DeliverableKind:        turnDecision.DeliverableKind,
		ExpectedResults:        NormalizeExpectedResults(turnDecision.ExpectedResults),
		ResponseLanguage:       turnDecision.ResponseLanguage,
		Reason:                 turnDecision.Reason,
		UserFacingReply:        turnDecision.UserFacingReply,
		InitialToolNames:       append([]string{}, turnDecision.InitialToolNames...),
		PriorTaskReference:     NormalizePriorTaskReference(turnDecision.PriorTaskReference),
		ClarificationQuestion:  turnDecision.ClarificationQuestion,
		ClarificationOptions:   append([]ClarificationOption{}, turnDecision.ClarificationOptions...),
	}
}

func (turnDecision TurnDecision) WithRestoredIntakeState(intakeDecision IntakeDecision) TurnDecision {
	if intakeDecision.EstimatedMinutes < 1 {
		return turnDecision
	}
	turnDecision.Classification = intakeDecision.Classification
	turnDecision.TaskShape = intakeDecision.TaskShape
	turnDecision.TaskLevel = intakeDecision.TaskLevel
	turnDecision.EstimatedMinutes = intakeDecision.EstimatedMinutes
	turnDecision.RequestedOutputFormats = append([]string{}, intakeDecision.RequestedOutputFormats...)
	turnDecision.ExpectedResults = NormalizeExpectedResults(intakeDecision.ExpectedResults)
	turnDecision.InitialToolNames = append([]string{}, intakeDecision.InitialToolNames...)
	return turnDecision
}

func IsApprovingSignal(signal ApprovalSignal) bool {
	return signal == ApprovalSignalApprove || signal == ApprovalSignalApproveTask
}

func NormalizeIntakeClassification(classification IntakeClassification) IntakeClassification {
	switch classification {
	case IntakeClassificationQuickReply, IntakeClassificationBoundedTask, IntakeClassificationNeedsConfirmation, IntakeClassificationUnsupported:
		return classification
	default:
		return ""
	}
}

func NormalizePriorTaskReference(reference PriorTaskReference) PriorTaskReference {
	switch reference {
	case PriorTaskReferenceOutcomeRecovery:
		return PriorTaskReferenceOutcomeRecovery
	default:
		return PriorTaskReferenceNone
	}
}
