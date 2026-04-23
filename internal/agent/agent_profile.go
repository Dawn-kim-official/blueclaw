package agent

type AgentProfile struct {
	Name             string   `json:"name"`
	AllowedToolNames []string `json:"allowedToolNames"`
}
