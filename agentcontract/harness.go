package agentcontract

import (
	"context"

	"github.com/Dawn-kim-official/blueclaw/model"
)

type Harness interface {
	RunTurn(context.Context, AgentTurnRequest) (AgentTurnResult, error)
}

type TaskTierLanguageModels struct {
	Low    model.LanguageModelProvider
	XLow   model.LanguageModelProvider
	Medium model.LanguageModelProvider
	High   model.LanguageModelProvider
	XHigh  model.LanguageModelProvider
	Max    model.LanguageModelProvider
}
