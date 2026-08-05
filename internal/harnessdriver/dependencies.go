package harnessdriver

import (
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

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
	TaskTierLanguageModels      agentcontract.TaskTierLanguageModels
	IntakeLanguageModelProvider model.LanguageModelProvider
}

type Factory func(Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever)

type VirtualSessionDependencies struct {
	TaskRunStore                taskstate.TaskRunStore
	TaskStepStore               taskstate.TaskStepStore
	TaskArtifactStore           taskstate.TaskArtifactStore
	TaskTierLanguageModels      agentcontract.TaskTierLanguageModels
	IntakeLanguageModelProvider model.LanguageModelProvider
	IntakeOptions               agentcontract.IntakeOptions
	ScenarioTurnOptions         agentcontract.TurnOptions
	InstructionBundleLoader     func() agentcontract.InstructionBundle
	EmbeddingProvider           model.EmbeddingProvider
	EmbeddingModelName          string
}

type VirtualSessionFactory func(VirtualSessionDependencies) (agentcontract.Harness, agentcontract.SkillRetriever)
