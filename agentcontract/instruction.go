package agentcontract

type InstructionSource struct {
	Path      string `json:"path"`
	SkillName string `json:"skillName,omitempty"`
	ByteSize  int    `json:"byteSize"`
	SHA256    string `json:"sha256,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
}

type InstructionBundle struct {
	Prompt                         string                   `json:"prompt"`
	Sources                        []InstructionSource      `json:"sources"`
	Skills                         []SkillInstruction       `json:"skills,omitempty"`
	SkillDecisions                 []SkillSelectionDecision `json:"skillDecisions,omitempty"`
	RequiredNextTools              []string                 `json:"requiredNextTools,omitempty"`
	RequiredEvidenceTools          []string                 `json:"requiredEvidenceTools,omitempty"`
	HasContractSkillArbitration    bool                     `json:"hasContractSkillArbitration,omitempty"`
	ContractSkillArbitrationFailed bool                     `json:"contractSkillArbitrationFailed,omitempty"`
	RetrievalMode                  string                   `json:"retrievalMode,omitempty"`
	IndexStatus                    string                   `json:"indexStatus,omitempty"`
	CandidateCount                 int                      `json:"candidateCount,omitempty"`
	SkillQueries                   []string                 `json:"skillQueries,omitempty"`
}

type SkillInstruction struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	WhenToUse      string   `json:"whenToUse,omitempty"`
	Category       string   `json:"category,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	TriggerHints   []string `json:"triggerHints,omitempty"`
	Prompt         string   `json:"prompt"`
	ToolReferences []string `json:"toolReferences,omitempty"`
	Source         InstructionSource
}

type SkillSelectionDecision struct {
	Name                  string            `json:"name"`
	Status                string            `json:"status"`
	Reason                string            `json:"reason"`
	ProfileName           string            `json:"profileName,omitempty"`
	Score                 float64           `json:"score,omitempty"`
	MissingToolReferences []string          `json:"missingToolReferences,omitempty"`
	Source                InstructionSource `json:"source,omitempty"`
}
