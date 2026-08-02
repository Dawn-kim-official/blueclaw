package agentharness

import (
	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/model"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

type TaskTierLanguageModels struct {
	Low    model.LanguageModelProvider
	XLow   model.LanguageModelProvider
	Medium model.LanguageModelProvider
	High   model.LanguageModelProvider
	XHigh  model.LanguageModelProvider
	Max    model.LanguageModelProvider
	Coding model.LanguageModelProvider
}

type Dependencies struct {
	RuntimeConfiguration        config.RuntimeConfiguration
	TaskRunStore                taskstate.TaskRunStore
	TaskStepStore               taskstate.TaskStepStore
	TaskArtifactStore           taskstate.TaskArtifactStore
	InstructionBundleLoader     func() agentcontract.InstructionBundle
	CompanyProvider             func() agentcontract.CompanyContext
	EmbeddingProvider           model.EmbeddingProvider
	EmbeddingModelName          string
	SkillIndexPath              string
	TaskTierLanguageModels      TaskTierLanguageModels
	IntakeLanguageModelProvider model.LanguageModelProvider
}

type Factory func(Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever)
