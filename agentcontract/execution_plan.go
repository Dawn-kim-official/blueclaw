package agentcontract

type ExecutionPlan struct {
	OriginalInstruction     string   `json:"originalInstruction"`
	Summary                 string   `json:"summary"`
	Targets                 []string `json:"targets"`
	Schedule                string   `json:"schedule"`
	StartAt                 string   `json:"startAt"`
	EndAt                   string   `json:"endAt"`
	Cadence                 string   `json:"cadence"`
	ExternalSend            bool     `json:"externalSend"`
	ThirdPartyExternalSend  bool     `json:"thirdPartyExternalSend"`
	Repeated                bool     `json:"repeated"`
	HighFrequency           bool     `json:"highFrequency"`
	Destructive             bool     `json:"destructive"`
	PermissionChange        bool     `json:"permissionChange"`
	PublicDeploy            bool     `json:"publicDeploy"`
	PaidAction              bool     `json:"paidAction"`
	MissingInformation      []string `json:"missingInformation"`
	ContinuationInstruction string   `json:"continuationInstruction"`
}

type ConfirmationReplyDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}
