package bluecollarharness

import (
	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/harnessdriver"
	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/bluecollar"
	"github.com/Dawn-kim-official/bluecollar/agentcontract"
)

func New(dependencies harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
	agentKernel := bluecollar.NewAgentKernel(dependencies.TaskRunStore, dependencies.TaskStepStore)
	agentKernel.UseTaskArtifactService(dependencies.TaskArtifactStore)
	agentKernel.UseTurnOptions(deriveTurnOptions(dependencies.RuntimeConfiguration))
	agentKernel.UseIntakeOptions(deriveIntakeOptions(dependencies.RuntimeConfiguration))
	agentKernel.UseInstructionBundleLoader(dependencies.InstructionBundleLoader)
	taskTierLanguageModels := dependencies.TaskTierLanguageModels
	if taskTierLanguageModels.Low != nil {
		agentKernel.UseLanguageModelProvider(taskTierLanguageModels.Low)
		agentKernel.UseTaskTierLanguageModels(taskTierLanguageModels)
	}
	skillRetriever := bluecollar.NewEmbeddingSkillRetriever(dependencies.EmbeddingProvider, dependencies.SkillIndexPath)
	skillRetriever.EmbeddingModel = dependencies.EmbeddingModelName
	agentKernel.UseSkillRetriever(skillRetriever)
	agentKernel.UseCompanyProvider(dependencies.CompanyProvider)
	if dependencies.IntakeLanguageModelProvider != nil {
		agentKernel.UseIntakeLanguageModelProvider(dependencies.IntakeLanguageModelProvider)
	}
	return agentKernel, skillRetriever
}

func deriveTurnOptions(runtimeConfiguration config.RuntimeConfiguration) agentcontract.TurnOptions {
	taskLevelProfile := bluecollar.TaskLevelProfileForLevel(agentcontract.NormalizeTaskLevel(runtimeConfiguration.Agent.DefaultTaskLevel))
	return agentcontract.TurnOptions{
		MaxIterationCount:   taskLevelProfile.MaxIterationCount,
		MaxToolCallCount:    taskLevelProfile.MaxToolCallCount,
		MaxElapsedSecond:    int(taskLevelProfile.Duration.Seconds()),
		ContextWindowTokens: runtimeConfiguration.LanguageModel.Capability.ContextWindowTokens,
		TaskLevel:           taskLevelProfile.TaskLevel,
		ToolResultMaxBytes:  runtimeConfiguration.Agent.ToolResultMaxBytes,
		GenerationOptions: llm.GenerationOptions{
			Seed:        runtimeConfiguration.Agent.GenerationOptions.Seed,
			Temperature: runtimeConfiguration.Agent.GenerationOptions.Temperature,
		},
		RecoveryBudget: agentcontract.RecoveryBudget{
			CorrectedRetry: runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.CorrectedRetry,
			AlternateRoute: runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.AlternateRoute,
			AdjacentTool:   runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.AdjacentTool,
			NoToolFallback: runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.NoToolFallback,
		},
	}
}

func deriveIntakeOptions(runtimeConfiguration config.RuntimeConfiguration) agentcontract.IntakeOptions {
	return agentcontract.IntakeOptions{
		IsEnabled:           runtimeConfiguration.Agent.Intake.Enabled,
		DefaultTaskLevel:    agentcontract.NormalizeTaskLevel(runtimeConfiguration.Agent.DefaultTaskLevel),
		SkillTaskLevelFloor: agentcontract.NormalizeTaskLevel(runtimeConfiguration.Agent.SkillTaskLevelFloor),
	}
}
