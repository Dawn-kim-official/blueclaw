package agentcontract

import "context"

type SkillRetriever interface {
	Available(AgentRequest, []SkillInstruction) []SkillInstruction
	Retrieve(context.Context, AgentRequest, []SkillInstruction, int) SkillRetrievalResult
	Search(context.Context, AgentRequest, []SkillInstruction, SkillSearchQuerySet, int) SkillRetrievalResult
	Refresh(context.Context, []SkillInstruction)
}

type SkillSearchQuery struct {
	Description string `json:"description"`
}

type SkillSearchQuerySet struct {
	Queries []SkillSearchQuery `json:"queries"`
}

type SkillRetrievalResult struct {
	RetrievalMode      string
	IndexStatus        string
	CandidateCount     int
	QueryDescriptions  []string
	SelectedCandidates []SkillCandidate
}

type SkillCandidate struct {
	Name   string
	Score  float64
	Reason string
	Source InstructionSource
}

func VisibleSkillInstructionsForRequester(skillInstructions []SkillInstruction, requesterCircles []string) []SkillInstruction {
	return append([]SkillInstruction{}, skillInstructions...)
}
