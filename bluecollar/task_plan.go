package bluecollar

type TaskPlan struct {
	Instruction string         `json:"instruction"`
	TaskSteps   []TaskPlanStep `json:"taskSteps"`
}

type TaskPlanStep struct {
	Name                     string   `json:"name"`
	AssignedAgentProfileName string   `json:"assignedAgentProfileName"`
	Instruction              string   `json:"instruction"`
	AllowedToolNames         []string `json:"allowedToolNames"`
	NeedsUserInput           bool     `json:"needsUserInput"`
}
