package bluecollarharness

import (
	"github.com/Dawn-kim-official/blueclaw/internal/harnessdriver"
	"github.com/Dawn-kim-official/bluecollar"
	"github.com/Dawn-kim-official/bluecollar/agentcontract"
)

func NewVirtualSession(dependencies harnessdriver.VirtualSessionDependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
	agentKernel := bluecollar.NewAgentKernel(dependencies.TaskRunStore, dependencies.TaskStepStore)
	agentKernel.UseTaskArtifactService(dependencies.TaskArtifactStore)
	taskTierLanguageModels := dependencies.TaskTierLanguageModels
	agentKernel.UseLanguageModelProvider(taskTierLanguageModels.Low)
	agentKernel.UseTaskTierLanguageModels(taskTierLanguageModels)
	agentKernel.UseIntakeLanguageModelProvider(dependencies.IntakeLanguageModelProvider)
	agentKernel.UseIntakeOptions(dependencies.IntakeOptions)
	agentKernel.UseTurnOptions(virtualTurnOptions(dependencies.ScenarioTurnOptions))
	agentKernel.UseInstructionBundleLoader(dependencies.InstructionBundleLoader)
	skillRetriever := bluecollar.NewEmbeddingSkillRetriever(dependencies.EmbeddingProvider, "")
	skillRetriever.EmbeddingModel = dependencies.EmbeddingModelName
	agentKernel.UseSkillRetriever(skillRetriever)
	return agentKernel, skillRetriever
}

func virtualTurnOptions(scenarioOptions agentcontract.TurnOptions) agentcontract.TurnOptions {
	taskLevelProfile := bluecollar.TaskLevelProfileForLevel(scenarioOptions.TaskLevel)
	turnOptions := agentcontract.TurnOptions{
		TaskLevel:         taskLevelProfile.TaskLevel,
		MaxIterationCount: taskLevelProfile.MaxIterationCount,
		MaxToolCallCount:  taskLevelProfile.MaxToolCallCount,
		MaxElapsedSecond:  int(taskLevelProfile.Duration.Seconds()),
	}
	if scenarioOptions.MaxIterationCount > 0 {
		turnOptions.MaxIterationCount = scenarioOptions.MaxIterationCount
	}
	if scenarioOptions.MaxToolCallCount > 0 {
		turnOptions.MaxToolCallCount = scenarioOptions.MaxToolCallCount
	}
	if scenarioOptions.MaxElapsedSecond > 0 {
		turnOptions.MaxElapsedSecond = scenarioOptions.MaxElapsedSecond
	}
	if scenarioOptions.RecoveryAttemptLimit != 0 {
		turnOptions.RecoveryAttemptLimit = scenarioOptions.RecoveryAttemptLimit
	}
	if scenarioOptions.RecoveryBudget != (agentcontract.RecoveryBudget{}) {
		turnOptions.RecoveryBudget = scenarioOptions.RecoveryBudget
	}
	return turnOptions
}
