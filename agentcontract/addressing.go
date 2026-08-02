package agentcontract

import "time"

type AddressingTarget string

const (
	AddressingTargetBot     AddressingTarget = "bot"
	AddressingTargetHuman   AddressingTarget = "human"
	AddressingTargetAnyone  AddressingTarget = "anyone"
	AddressingTargetNone    AddressingTarget = "none"
	AddressingTargetUnclear AddressingTarget = "unclear"
)

type AddressingClassificationRequest struct {
	Prompt           string
	BotMentioned     bool
	MessageSentAt    time.Time
	ConversationType string
	SenderName       string
	SenderHandle     string
	VisibleContext   VisibleContext
}

type AddressingDecision struct {
	Target         AddressingTarget
	ShouldRespond  bool
	ReactionEmoji  string
	DutyMatch      bool
	DutyName       string
	DutyConfidence float64
}

type ActiveTaskFollowUpClassificationRequest struct {
	ActiveTaskPrompt string
	ActiveTaskStatus string
	LatestMessage    string
}
