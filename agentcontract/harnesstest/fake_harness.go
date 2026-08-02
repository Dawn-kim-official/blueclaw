package harnesstest

import (
	"context"
	"sync"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
)

var _ agentcontract.Harness = (*FakeHarness)(nil)

type LaunchFailureCall struct {
	Request  agentcontract.AgentTurnRequest
	Phase    string
	StepName string
	Error    error
}

type ReplyCall struct {
	Prompt         string
	VisibleContext agentcontract.VisibleContext
	MemoryFacts    []agentcontract.MemoryFact
}

type FakeHarness struct {
	mutex sync.Mutex

	TurnResult              agentcontract.AgentTurnResult
	TurnError               error
	TurnDecision            agentcontract.TurnDecision
	RouteError              error
	AgentRequestResult      agentcontract.AgentTurnResult
	AgentRequestError       error
	LaunchFailureResult     agentcontract.AgentTurnResult
	Reply                   string
	ReplyError              error
	AddressingDecision      agentcontract.AddressingDecision
	AddressingError         error
	IsActiveTaskFollowUp    bool
	ActiveTaskFollowUpError error

	turnRequests          []agentcontract.AgentTurnRequest
	routedRequests        []agentcontract.AgentRequest
	agentRequests         []agentcontract.AgentRequest
	launchFailureCalls    []LaunchFailureCall
	replyCalls            []ReplyCall
	addressingRequests    []agentcontract.AddressingClassificationRequest
	activeTaskFollowUps   []agentcontract.ActiveTaskFollowUpClassificationRequest
	refreshedSkillBundles []agentcontract.InstructionBundle
}

func (fakeHarness *FakeHarness) RunTurn(responseContext context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	fakeHarness.mutex.Lock()
	fakeHarness.turnRequests = append(fakeHarness.turnRequests, request)
	fakeHarness.mutex.Unlock()
	return fakeHarness.TurnResult, fakeHarness.TurnError
}

func (fakeHarness *FakeHarness) RouteTurn(responseContext context.Context, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	fakeHarness.mutex.Lock()
	fakeHarness.routedRequests = append(fakeHarness.routedRequests, request)
	fakeHarness.mutex.Unlock()
	return fakeHarness.TurnDecision, fakeHarness.RouteError
}

func (fakeHarness *FakeHarness) RunAgentRequest(responseContext context.Context, request agentcontract.AgentRequest) (agentcontract.AgentTurnResult, error) {
	fakeHarness.mutex.Lock()
	fakeHarness.agentRequests = append(fakeHarness.agentRequests, request)
	fakeHarness.mutex.Unlock()
	return fakeHarness.AgentRequestResult, fakeHarness.AgentRequestError
}

func (fakeHarness *FakeHarness) CompleteLaunchFailure(responseContext context.Context, request agentcontract.AgentTurnRequest, phase string, stepName string, errorValue error) agentcontract.AgentTurnResult {
	fakeHarness.mutex.Lock()
	fakeHarness.launchFailureCalls = append(fakeHarness.launchFailureCalls, LaunchFailureCall{
		Request:  request,
		Phase:    phase,
		StepName: stepName,
		Error:    errorValue,
	})
	fakeHarness.mutex.Unlock()
	return fakeHarness.LaunchFailureResult
}

func (fakeHarness *FakeHarness) GenerateReply(responseContext context.Context, prompt string) (string, error) {
	return fakeHarness.GenerateReplyWithContext(responseContext, prompt, agentcontract.VisibleContext{}, nil)
}

func (fakeHarness *FakeHarness) GenerateReplyWithContext(responseContext context.Context, prompt string, visibleContext agentcontract.VisibleContext, memoryFacts []agentcontract.MemoryFact) (string, error) {
	fakeHarness.mutex.Lock()
	fakeHarness.replyCalls = append(fakeHarness.replyCalls, ReplyCall{
		Prompt:         prompt,
		VisibleContext: visibleContext,
		MemoryFacts:    memoryFacts,
	})
	fakeHarness.mutex.Unlock()
	return fakeHarness.Reply, fakeHarness.ReplyError
}

func (fakeHarness *FakeHarness) ClassifyAddressing(responseContext context.Context, request agentcontract.AddressingClassificationRequest) (agentcontract.AddressingDecision, error) {
	fakeHarness.mutex.Lock()
	fakeHarness.addressingRequests = append(fakeHarness.addressingRequests, request)
	fakeHarness.mutex.Unlock()
	return fakeHarness.AddressingDecision, fakeHarness.AddressingError
}

func (fakeHarness *FakeHarness) ClassifyActiveTaskFollowUp(responseContext context.Context, request agentcontract.ActiveTaskFollowUpClassificationRequest) (bool, error) {
	fakeHarness.mutex.Lock()
	fakeHarness.activeTaskFollowUps = append(fakeHarness.activeTaskFollowUps, request)
	fakeHarness.mutex.Unlock()
	return fakeHarness.IsActiveTaskFollowUp, fakeHarness.ActiveTaskFollowUpError
}

func (fakeHarness *FakeHarness) RefreshSkillIndex(responseContext context.Context, instructionBundle agentcontract.InstructionBundle) {
	fakeHarness.mutex.Lock()
	fakeHarness.refreshedSkillBundles = append(fakeHarness.refreshedSkillBundles, instructionBundle)
	fakeHarness.mutex.Unlock()
}

func (fakeHarness *FakeHarness) TurnRequests() []agentcontract.AgentTurnRequest {
	fakeHarness.mutex.Lock()
	defer fakeHarness.mutex.Unlock()
	return append([]agentcontract.AgentTurnRequest{}, fakeHarness.turnRequests...)
}

func (fakeHarness *FakeHarness) RoutedRequests() []agentcontract.AgentRequest {
	fakeHarness.mutex.Lock()
	defer fakeHarness.mutex.Unlock()
	return append([]agentcontract.AgentRequest{}, fakeHarness.routedRequests...)
}

func (fakeHarness *FakeHarness) AgentRequests() []agentcontract.AgentRequest {
	fakeHarness.mutex.Lock()
	defer fakeHarness.mutex.Unlock()
	return append([]agentcontract.AgentRequest{}, fakeHarness.agentRequests...)
}

func (fakeHarness *FakeHarness) LaunchFailureCalls() []LaunchFailureCall {
	fakeHarness.mutex.Lock()
	defer fakeHarness.mutex.Unlock()
	return append([]LaunchFailureCall{}, fakeHarness.launchFailureCalls...)
}

func (fakeHarness *FakeHarness) ReplyCalls() []ReplyCall {
	fakeHarness.mutex.Lock()
	defer fakeHarness.mutex.Unlock()
	return append([]ReplyCall{}, fakeHarness.replyCalls...)
}

func (fakeHarness *FakeHarness) AddressingRequests() []agentcontract.AddressingClassificationRequest {
	fakeHarness.mutex.Lock()
	defer fakeHarness.mutex.Unlock()
	return append([]agentcontract.AddressingClassificationRequest{}, fakeHarness.addressingRequests...)
}

func (fakeHarness *FakeHarness) ActiveTaskFollowUpRequests() []agentcontract.ActiveTaskFollowUpClassificationRequest {
	fakeHarness.mutex.Lock()
	defer fakeHarness.mutex.Unlock()
	return append([]agentcontract.ActiveTaskFollowUpClassificationRequest{}, fakeHarness.activeTaskFollowUps...)
}

func (fakeHarness *FakeHarness) RefreshedSkillBundles() []agentcontract.InstructionBundle {
	fakeHarness.mutex.Lock()
	defer fakeHarness.mutex.Unlock()
	return append([]agentcontract.InstructionBundle{}, fakeHarness.refreshedSkillBundles...)
}
