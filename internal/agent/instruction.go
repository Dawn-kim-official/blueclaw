package agent

type InstructionSource struct {
	Path      string `json:"path"`
	SkillName string `json:"skillName,omitempty"`
	ByteSize  int    `json:"byteSize"`
	SHA256    string `json:"sha256,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
}

type InstructionBundle struct {
	Prompt         string                   `json:"prompt"`
	Sources        []InstructionSource      `json:"sources"`
	Skills         []SkillInstruction       `json:"skills,omitempty"`
	SkillDecisions []SkillSelectionDecision `json:"skillDecisions,omitempty"`
}

type SkillInstruction struct {
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	Category        string          `json:"category,omitempty"`
	Tags            []string        `json:"tags,omitempty"`
	Prompt          string          `json:"prompt"`
	Activation      SkillActivation `json:"activation,omitempty"`
	Completion      SkillCompletion `json:"completion,omitempty"`
	Quality         SkillQuality    `json:"quality,omitempty"`
	RequiredTools   []string        `json:"requiredTools,omitempty"`
	AllowedProfiles []string        `json:"allowedProfiles,omitempty"`
	TriggerHints    []string        `json:"triggerHints,omitempty"`
	References      []string        `json:"references,omitempty"`
	Scripts         []string        `json:"scripts,omitempty"`
	Assets          []string        `json:"assets,omitempty"`
	Source          InstructionSource
}

type SkillActivation struct {
	Keywords     []string `json:"keywords,omitempty"`
	ToolNames    []string `json:"toolNames,omitempty"`
	ToolPrefixes []string `json:"toolPrefixes,omitempty"`
}

type SkillCompletion struct {
	RequiredEvidenceTools      []string `json:"requiredEvidenceTools,omitempty"`
	RequiredAttachmentSuffixes []string `json:"requiredAttachmentSuffixes,omitempty"`
}

type SkillQuality struct {
	RecommendedChecks []string `json:"recommendedChecks,omitempty"`
}

type SkillSelectionDecision struct {
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	Reason       string            `json:"reason"`
	ProfileName  string            `json:"profileName,omitempty"`
	MissingTools []string          `json:"missingTools,omitempty"`
	Source       InstructionSource `json:"source,omitempty"`
}
