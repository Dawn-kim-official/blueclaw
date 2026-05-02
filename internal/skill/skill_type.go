package skill

type SkillBundle struct {
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	Category        string          `json:"category,omitempty"`
	Tags            []string        `json:"tags,omitempty"`
	Activation      SkillActivation `json:"activation,omitempty"`
	RequiredTools   []string        `json:"requiredTools,omitempty"`
	AllowedProfiles []string        `json:"allowedProfiles,omitempty"`
	TriggerHints    []string        `json:"triggerHints,omitempty"`
	References      []string        `json:"references,omitempty"`
	Scripts         []string        `json:"scripts,omitempty"`
	Assets          []string        `json:"assets,omitempty"`
	Instruction     string          `json:"instruction"`
	DirectoryPath   string          `json:"directoryPath"`
}

type SkillActivation struct {
	Keywords     []string `json:"keywords,omitempty"`
	ToolNames    []string `json:"toolNames,omitempty"`
	ToolPrefixes []string `json:"toolPrefixes,omitempty"`
}
