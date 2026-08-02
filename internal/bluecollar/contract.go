package bluecollar

import "github.com/Dawn-kim-official/blueclaw/agentcontract"

var _ agentcontract.Harness = (*AgentKernel)(nil)

type (
	ActiveGoal                              = agentcontract.ActiveGoal
	ActiveGoalStatus                        = agentcontract.ActiveGoalStatus
	ActiveTaskContext                       = agentcontract.ActiveTaskContext
	ActiveTaskFollowUpClassificationRequest = agentcontract.ActiveTaskFollowUpClassificationRequest
	AddressingClassificationRequest         = agentcontract.AddressingClassificationRequest
	AddressingDecision                      = agentcontract.AddressingDecision
	AddressingTarget                        = agentcontract.AddressingTarget
	AgentCheckpoint                         = agentcontract.AgentCheckpoint
	AgentCheckpointSender                   = agentcontract.AgentCheckpointSender
	AgentFilePart                           = agentcontract.AgentFilePart
	AgentImagePart                          = agentcontract.AgentImagePart
	AgentPart                               = agentcontract.AgentPart
	AgentPartSource                         = agentcontract.AgentPartSource
	AgentRequest                            = agentcontract.AgentRequest
	AgentTurnRequest                        = agentcontract.AgentTurnRequest
	AgentTurnResult                         = agentcontract.AgentTurnResult
	AmbientDutyContext                      = agentcontract.AmbientDutyContext
	ApprovalSignal                          = agentcontract.ApprovalSignal
	ArtifactManifestEntry                   = agentcontract.ArtifactManifestEntry
	BusyRoute                               = agentcontract.BusyRoute
	ChoiceReplyOption                       = agentcontract.ChoiceReplyOption
	ClarificationOption                     = agentcontract.ClarificationOption
	CompanyContext                          = agentcontract.CompanyContext
	ConfirmationReplyDecision               = agentcontract.ConfirmationReplyDecision
	ContractToolWorkingSet                  = agentcontract.ContractToolWorkingSet
	DeliverableKind                         = agentcontract.DeliverableKind
	ExecutionPlan                           = agentcontract.ExecutionPlan
	ExpectedResult                          = agentcontract.ExpectedResult
	FailureNotice                           = agentcontract.FailureNotice
	InstructionBundle                       = agentcontract.InstructionBundle
	InstructionSource                       = agentcontract.InstructionSource
	IntakeClassification                    = agentcontract.IntakeClassification
	IntakeDecision                          = agentcontract.IntakeDecision
	IntakeOptions                           = agentcontract.IntakeOptions
	MemoryFact                              = agentcontract.MemoryFact
	OutcomeContract                         = agentcontract.OutcomeContract
	OutcomeEffect                           = agentcontract.OutcomeEffect
	PendingChoiceContext                    = agentcontract.PendingChoiceContext
	PendingConfirmationContext              = agentcontract.PendingConfirmationContext
	PendingInputContext                     = agentcontract.PendingInputContext
	PriorTaskContext                        = agentcontract.PriorTaskContext
	PriorTaskReference                      = agentcontract.PriorTaskReference
	RecoveryBudget                          = agentcontract.RecoveryBudget
	ScheduledRunContext                     = agentcontract.ScheduledRunContext
	SkillInstruction                        = agentcontract.SkillInstruction
	SkillSelectionDecision                  = agentcontract.SkillSelectionDecision
	TaskControlIntent                       = agentcontract.TaskControlIntent
	TaskControlIntentDecision               = agentcontract.TaskControlIntentDecision
	TaskLevel                               = agentcontract.TaskLevel
	TaskShape                               = agentcontract.TaskShape
	ToolExposureEvent                       = agentcontract.ToolExposureEvent
	TurnDecision                            = agentcontract.TurnDecision
	TurnOptions                             = agentcontract.TurnOptions
	TurnRoute                               = agentcontract.TurnRoute
	VisibleContext                          = agentcontract.VisibleContext
	VisibleContextMaterial                  = agentcontract.VisibleContextMaterial
	VisibleContextMessage                   = agentcontract.VisibleContextMessage
	droppedToolGroup                        = agentcontract.DroppedToolGroup
)

const (
	ActiveGoalStatusActive           = agentcontract.ActiveGoalStatusActive
	ActiveGoalStatusBlocked          = agentcontract.ActiveGoalStatusBlocked
	ActiveGoalStatusCompleted        = agentcontract.ActiveGoalStatusCompleted
	ActiveGoalStatusWaitingApproval  = agentcontract.ActiveGoalStatusWaitingApproval
	ActiveGoalStatusWaitingUserInput = agentcontract.ActiveGoalStatusWaitingUserInput

	AddressingTargetAnyone  = agentcontract.AddressingTargetAnyone
	AddressingTargetBot     = agentcontract.AddressingTargetBot
	AddressingTargetHuman   = agentcontract.AddressingTargetHuman
	AddressingTargetNone    = agentcontract.AddressingTargetNone
	AddressingTargetUnclear = agentcontract.AddressingTargetUnclear

	AgentPartTypeFile  = agentcontract.AgentPartTypeFile
	AgentPartTypeImage = agentcontract.AgentPartTypeImage
	AgentPartTypeText  = agentcontract.AgentPartTypeText

	ApprovalSignalApprove     = agentcontract.ApprovalSignalApprove
	ApprovalSignalApproveTask = agentcontract.ApprovalSignalApproveTask
	ApprovalSignalReject      = agentcontract.ApprovalSignalReject
	ApprovalSignalUnclear     = agentcontract.ApprovalSignalUnclear

	ArtifactRequirementNone      = agentcontract.ArtifactRequirementNone
	ArtifactRequirementPreferred = agentcontract.ArtifactRequirementPreferred
	ArtifactRequirementRequired  = agentcontract.ArtifactRequirementRequired

	BusyRouteCancel    = agentcontract.BusyRouteCancel
	BusyRouteNewTask   = agentcontract.BusyRouteNewTask
	BusyRouteReplace   = agentcontract.BusyRouteReplace
	BusyRouteStatus    = agentcontract.BusyRouteStatus
	BusyRouteSteer     = agentcontract.BusyRouteSteer
	BusyRouteUnrelated = agentcontract.BusyRouteUnrelated

	DeliverableKindDocument     = agentcontract.DeliverableKindDocument
	DeliverableKindNone         = agentcontract.DeliverableKindNone
	DeliverableKindPresentation = agentcontract.DeliverableKindPresentation
	DeliverableKindWebsite      = agentcontract.DeliverableKindWebsite

	DefaultReactionEmojiName = agentcontract.DefaultReactionEmojiName

	ResponseLanguageEnglish            = agentcontract.ResponseLanguageEnglish
	ResponseLanguageKorean             = agentcontract.ResponseLanguageKorean
	ResponseLanguageSameAsConversation = agentcontract.ResponseLanguageSameAsConversation

	TaskControlIntentNone    = agentcontract.TaskControlIntentNone
	TaskControlIntentStop    = agentcontract.TaskControlIntentStop
	TaskControlIntentStopAll = agentcontract.TaskControlIntentStopAll

	ExpectedResultTypeFile    = agentcontract.ExpectedResultTypeFile
	ExpectedResultTypeLink    = agentcontract.ExpectedResultTypeLink
	ExpectedResultTypeMessage = agentcontract.ExpectedResultTypeMessage

	IntakeClassificationBoundedTask       = agentcontract.IntakeClassificationBoundedTask
	IntakeClassificationNeedsConfirmation = agentcontract.IntakeClassificationNeedsConfirmation
	IntakeClassificationQuickReply        = agentcontract.IntakeClassificationQuickReply
	IntakeClassificationUnsupported       = agentcontract.IntakeClassificationUnsupported

	PriorTaskReferenceNone            = agentcontract.PriorTaskReferenceNone
	PriorTaskReferenceOutcomeRecovery = agentcontract.PriorTaskReferenceOutcomeRecovery

	TaskLevelHigh   = agentcontract.TaskLevelHigh
	TaskLevelLow    = agentcontract.TaskLevelLow
	TaskLevelMax    = agentcontract.TaskLevelMax
	TaskLevelMedium = agentcontract.TaskLevelMedium
	TaskLevelXHigh  = agentcontract.TaskLevelXHigh
	TaskLevelXLow   = agentcontract.TaskLevelXLow

	TaskShapeApprovalGatedTask  = agentcontract.TaskShapeApprovalGatedTask
	TaskShapeBrowserHandoffTask = agentcontract.TaskShapeBrowserHandoffTask
	TaskShapeImmediateReply     = agentcontract.TaskShapeImmediateReply
	TaskShapeMaintenanceTask    = agentcontract.TaskShapeMaintenanceTask
	TaskShapeResearchTask       = agentcontract.TaskShapeResearchTask
	TaskShapeScheduledTask      = agentcontract.TaskShapeScheduledTask

	TurnRouteAnswerMeta     = agentcontract.TurnRouteAnswerMeta
	TurnRouteAnswerQuestion = agentcontract.TurnRouteAnswerQuestion
	TurnRouteClarify        = agentcontract.TurnRouteClarify
	TurnRouteConsume        = agentcontract.TurnRouteConsume
	TurnRouteContinueTask   = agentcontract.TurnRouteContinueTask
	TurnRouteGiveUp         = agentcontract.TurnRouteGiveUp
	TurnRouteReviseTask     = agentcontract.TurnRouteReviseTask
	TurnRouteStartTask      = agentcontract.TurnRouteStartTask
)

var (
	DefaultResponseLanguage        = agentcontract.DefaultResponseLanguage
	IsApprovingSignal              = agentcontract.IsApprovingSignal
	LargerTaskLevel                = agentcontract.LargerTaskLevel
	NormalizeResponseLanguage      = agentcontract.NormalizeResponseLanguage
	NormalizeTaskLevel             = agentcontract.NormalizeTaskLevel
	OutcomeContractHasRequirements = agentcontract.OutcomeContractHasRequirements
	ResolveResponseLanguage        = agentcontract.ResolveResponseLanguage

	appendUniqueStrings         = agentcontract.AppendUniqueStrings
	taskLevelRank               = agentcontract.TaskLevelRank
	normalizeClassification     = agentcontract.NormalizeIntakeClassification
	normalizeExpectedResults    = agentcontract.NormalizeExpectedResults
	normalizePriorTaskReference = agentcontract.NormalizePriorTaskReference
)
