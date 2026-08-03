package adminapi

import (
	"encoding/json"
	"net/http"
)

type HarnessStatus struct {
	Name                    string `json:"name"`
	AgentCommandPath        string `json:"agentCommandPath,omitempty"`
	RunsAsRequesterIdentity bool   `json:"runsAsRequesterIdentity"`
	ToolCatalogURL          string `json:"toolCatalogURL,omitempty"`
}

type HarnessStatusHandler struct {
	Status HarnessStatus
}

func (handler HarnessStatusHandler) HandleGetHarnessStatus(responseWriter http.ResponseWriter, _ *http.Request) {
	responseWriter.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(responseWriter).Encode(handler.Status)
}
