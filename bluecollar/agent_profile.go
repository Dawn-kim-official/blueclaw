package bluecollar

type AgentProfile struct {
	Name             string   `json:"name"`
	AllowedToolNames []string `json:"allowedToolNames"`
}
