package agentcontract

type TaskControlIntent string

const (
	TaskControlIntentNone    TaskControlIntent = "none"
	TaskControlIntentStop    TaskControlIntent = "stop"
	TaskControlIntentStopAll TaskControlIntent = "stop_all"
)

type TaskControlIntentDecision struct {
	Intent TaskControlIntent `json:"intent"`
	Reason string            `json:"reason"`
}
