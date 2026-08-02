package harnessstub

import (
	"context"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

// Stub answers the agent harness port with canned decisions so host tests can
// exercise the host without the agent turn loop. It creates a real task run for
// every turn because the host reads the run the loop reports back.
type Stub struct {
	taskRunService *taskstate.TaskRunService

	TurnResult           agentcontract.AgentTurnResult
	TurnStatus           taskstate.TaskStatus
	TurnError            error
	TurnDecision         agentcontract.TurnDecision
	Reply                string
	AddressingDecision   agentcontract.AddressingDecision
	IsActiveTaskFollowUp bool

	lastTurnRequest             agentcontract.AgentTurnRequest
	runTurnCallCount            int
	classifyAddressingCallCount int
}

func New(taskRunService *taskstate.TaskRunService) *Stub {
	return &Stub{
		taskRunService: taskRunService,
		TurnStatus:     taskstate.TaskStatusCompleted,
	}
}

func (stub *Stub) RunTurn(_ context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	stub.runTurnCallCount++
	stub.lastTurnRequest = request
	if stub.TurnError != nil {
		return stub.TurnResult, stub.TurnError
	}
	turnResult := stub.TurnResult
	settledTaskRun, errorValue := stub.settleTaskRun(request, stub.TurnStatus, turnResult.FinishMessage)
	if errorValue != nil {
		return turnResult, errorValue
	}
	turnResult.TaskRun = settledTaskRun
	return turnResult, nil
}

func (stub *Stub) RouteTurn(context.Context, agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	return stub.TurnDecision, nil
}

func (stub *Stub) RunAgentRequest(context.Context, agentcontract.AgentRequest) (agentcontract.AgentTurnResult, error) {
	return agentcontract.AgentTurnResult{}, nil
}

func (stub *Stub) CompleteLaunchFailure(_ context.Context, request agentcontract.AgentTurnRequest, phase string, stepName string, errorValue error) agentcontract.AgentTurnResult {
	failedTaskRun, transitionError := stub.settleTaskRun(request, taskstate.TaskStatusFailed, errorValue.Error())
	if transitionError != nil {
		return agentcontract.AgentTurnResult{}
	}
	return agentcontract.AgentTurnResult{
		TaskRun: failedTaskRun,
		FailureNotice: agentcontract.FailureNotice{
			Message:           errorValue.Error(),
			Source:            "raw_error",
			DiagnosticEventID: failedTaskRun.TaskRunID + ":" + phase + ":" + stepName,
			IsSendable:        true,
		},
	}
}

func (stub *Stub) GenerateReply(context.Context, string) (string, error) {
	return stub.Reply, nil
}

func (stub *Stub) GenerateReplyWithContext(context.Context, string, agentcontract.VisibleContext, []agentcontract.MemoryFact) (string, error) {
	return stub.Reply, nil
}

func (stub *Stub) ClassifyAddressing(context.Context, agentcontract.AddressingClassificationRequest) (agentcontract.AddressingDecision, error) {
	stub.classifyAddressingCallCount++
	return stub.AddressingDecision, nil
}

func (stub *Stub) ClassifyActiveTaskFollowUp(context.Context, agentcontract.ActiveTaskFollowUpClassificationRequest) (bool, error) {
	return stub.IsActiveTaskFollowUp, nil
}

func (stub *Stub) RefreshSkillIndex(context.Context, agentcontract.InstructionBundle) {}

func (stub *Stub) LastTurnRequest() agentcontract.AgentTurnRequest {
	return stub.lastTurnRequest
}

func (stub *Stub) RunTurnCallCount() int {
	return stub.runTurnCallCount
}

func (stub *Stub) ClassifyAddressingCallCount() int {
	return stub.classifyAddressingCallCount
}

func (stub *Stub) settleTaskRun(request agentcontract.AgentTurnRequest, status taskstate.TaskStatus, message string) (taskstate.TaskRun, error) {
	taskRun := stub.taskRunService.CreateTaskRunWithOrigin(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
	runningTaskRun, errorValue := stub.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, request.ProfileName)
	if errorValue != nil {
		return taskstate.TaskRun{}, errorValue
	}
	switch status {
	case taskstate.TaskStatusRunning:
		return runningTaskRun, nil
	case taskstate.TaskStatusCompleted:
		return stub.taskRunService.CompleteTaskRun(taskRun.TaskRunID, message)
	case taskstate.TaskStatusFailed:
		return stub.taskRunService.FailTaskRun(taskRun.TaskRunID, message)
	default:
		return stub.taskRunService.PauseTaskRun(taskRun.TaskRunID, status, message)
	}
}
