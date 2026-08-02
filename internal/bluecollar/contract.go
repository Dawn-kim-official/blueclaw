package bluecollar

import (
	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

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
	PlanStep                                = toolcontract.PlanStep
	ToolConflictResolution                  = toolcontract.ToolConflictResolution
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
	SkillCandidate                          = agentcontract.SkillCandidate
	SkillInstruction                        = agentcontract.SkillInstruction
	SkillRetrievalResult                    = agentcontract.SkillRetrievalResult
	SkillRetriever                          = agentcontract.SkillRetriever
	SkillSearchQuery                        = agentcontract.SkillSearchQuery
	SkillSearchQuerySet                     = agentcontract.SkillSearchQuerySet
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

	ResponseLanguageEnglish            = toolcontract.ResponseLanguageEnglish
	ResponseLanguageKorean             = toolcontract.ResponseLanguageKorean
	ResponseLanguageSameAsConversation = toolcontract.ResponseLanguageSameAsConversation

	ToolConflictResolutionAllowDuplicate = toolcontract.ToolConflictResolutionAllowDuplicate

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
	prepareFailureNoticeWithGenerator    = agentcontract.PrepareFailureNoticeWithGenerator
	buildIntakeNoticePrompt              = agentcontract.BuildIntakeNoticePrompt
	buildFailureNoticeCompressionPrompt  = agentcontract.BuildFailureNoticeCompressionPrompt
	buildFailureNoticeRepairPrompt       = agentcontract.BuildFailureNoticeRepairPrompt
	buildFailureNoticePrompt             = agentcontract.BuildFailureNoticePrompt
	failureReportAttachmentFilenames     = agentcontract.FailureReportAttachmentFilenames
	redactRawFailureNotice               = agentcontract.RedactRawFailureNotice
	DefaultResponseLanguage              = toolcontract.DefaultResponseLanguage
	IsApprovingSignal                    = agentcontract.IsApprovingSignal
	LargerTaskLevel                      = agentcontract.LargerTaskLevel
	NormalizeResponseLanguage            = toolcontract.NormalizeResponseLanguage
	NormalizeTaskLevel                   = agentcontract.NormalizeTaskLevel
	OutcomeContractHasRequirements       = agentcontract.OutcomeContractHasRequirements
	VisibleSkillInstructionsForRequester = agentcontract.VisibleSkillInstructionsForRequester
	ResolveResponseLanguage              = toolcontract.ResolveResponseLanguage
	NormalizePlan                        = toolcontract.NormalizePlan
	normalizePlanSteps                   = toolcontract.NormalizePlanSteps

	ObservationIDFromContext          = toolcontract.ObservationIDFromContext
	ResponseLanguageFromContext       = toolcontract.ResponseLanguageFromContext
	TaskRunIDFromContext              = toolcontract.TaskRunIDFromContext
	ToolConflictResolutionFromContext = toolcontract.ToolConflictResolutionFromContext
	UserFacingMessageFromContext      = toolcontract.UserFacingMessageFromContext
	WithObservationID                 = toolcontract.WithObservationID
	WithResponseLanguage              = toolcontract.WithResponseLanguage
	WithTaskRunID                     = toolcontract.WithTaskRunID
	WithToolConflictResolution        = toolcontract.WithToolConflictResolution
	WithUserFacingMessage             = toolcontract.WithUserFacingMessage

	appendUniqueStrings         = agentcontract.AppendUniqueStrings
	taskLevelRank               = agentcontract.TaskLevelRank
	normalizeClassification     = agentcontract.NormalizeIntakeClassification
	normalizeExpectedResults    = agentcontract.NormalizeExpectedResults
	normalizePriorTaskReference = agentcontract.NormalizePriorTaskReference
)

var reactionEmojiNames = agentcontract.ReactionEmojiNames

var formatContextTimestamp = agentcontract.FormatContextTimestamp

var buildVisibleContextDescription = agentcontract.BuildVisibleContextDescription

const (
	MemoryScopeUser         = agentcontract.MemoryScopeUser
	MemoryScopeWorkspace    = agentcontract.MemoryScopeWorkspace
	MemoryScopeCircle       = agentcontract.MemoryScopeCircle
	MemoryScopeConversation = agentcontract.MemoryScopeConversation
)

var buildMemoryContext = agentcontract.BuildMemoryContext

var (
	buildTemporalContextDescription = agentcontract.BuildTemporalContextDescription
	temporalContextLocation         = agentcontract.TemporalContextLocation
)

var (
	responseLanguageInstruction = agentcontract.ResponseLanguageInstruction
	redactUnsafeText            = agentcontract.RedactUnsafeText
)

type (
	FailureReport                 = agentcontract.FailureReport
	FailureNoticeGenerator        = agentcontract.FailureNoticeGenerator
	FailureNoticeGenerationStatus = agentcontract.FailureNoticeGenerationStatus
	IntakeReport                  = agentcontract.IntakeReport
)

var (
	diagnosticEventID              = agentcontract.DiagnosticEventID
	failureNoticeMessageIsSendable = agentcontract.FailureNoticeMessageIsSendable
	buildRawErrorFailureNotice     = agentcontract.BuildRawErrorFailureNotice
)

var (
	elapsedLimitRawErrorSummary            = agentcontract.ElapsedLimitRawErrorSummary
	textExceedsCharacterBudget             = agentcontract.TextExceedsCharacterBudget
	finishMessageMaximumCharacters         = agentcontract.FinishMessageMaximumCharacters
	buildFinishMessageCompressionPrompt    = agentcontract.BuildFinishMessageCompressionPrompt
	generateRecoveryChatText               = agentcontract.GenerateRecoveryChatText
	recoveryContextError                   = agentcontract.RecoveryContextError
	generateLocalRecoveryChatText          = agentcontract.GenerateLocalRecoveryChatText
	recoveryChatCompletionRequest          = agentcontract.RecoveryChatCompletionRequest
	buildElapsedLimitRawErrorFailureNotice = agentcontract.BuildElapsedLimitRawErrorFailureNotice
	failureNoticeRequiresReview            = agentcontract.FailureNoticeRequiresReview
	normalizeFailureReport                 = agentcontract.NormalizeFailureReport
	buildFailureNotice                     = agentcontract.BuildFailureNotice
)
