package agent

type InstructionSource struct {
	Path      string `json:"path"`
	SkillName string `json:"skillName,omitempty"`
	ByteSize  int    `json:"byteSize"`
	SHA256    string `json:"sha256,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
}

type InstructionBundle struct {
	Prompt  string              `json:"prompt"`
	Sources []InstructionSource `json:"sources"`
	Skills  []SkillInstruction  `json:"skills,omitempty"`
}

type SkillInstruction struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
	Source InstructionSource
}
