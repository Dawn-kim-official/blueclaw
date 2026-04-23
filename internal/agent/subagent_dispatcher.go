package agent

type SubagentDispatcher struct{}

func (subagentDispatcher SubagentDispatcher) RunPlanner(taskPlan TaskPlan) string {
	return "planner:" + taskPlan.Instruction
}

func (subagentDispatcher SubagentDispatcher) RunResearcher(taskPlan TaskPlan) string {
	return "researcher:" + taskPlan.Instruction
}

func (subagentDispatcher SubagentDispatcher) RunPolicyChecker(response string) string {
	return response
}

func (subagentDispatcher SubagentDispatcher) RunResponder(taskPlan TaskPlan) string {
	return "responder:" + taskPlan.Instruction
}

func (subagentDispatcher SubagentDispatcher) RunMemoryCurator(content string) string {
	return content
}
