package agentruntime

import (
	"github.com/Dawn-kim-official/blueclaw/internal/launchfailure"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
	"github.com/Dawn-kim-official/bluecollar/agentcontract"
	"github.com/Dawn-kim-official/bluecollar/intake"
	"github.com/Dawn-kim-official/bluecollar/model"
)

func routedTaskLauncher(harness agentcontract.Harness, taskRunService *task.TaskRunService, toolCatalogBuilder *ToolCatalogBuilder, routerLanguageModel model.LanguageModelProvider) *TaskLauncher {
	return routedTaskLauncherAuthoringNoticesWith(harness, taskRunService, toolCatalogBuilder, routerLanguageModel, routerLanguageModel)
}

func routedTaskLauncherAuthoringNoticesWith(harness agentcontract.Harness, taskRunService *task.TaskRunService, toolCatalogBuilder *ToolCatalogBuilder, routerLanguageModel model.LanguageModelProvider, noticeLanguageModel model.LanguageModelProvider) *TaskLauncher {
	taskLauncher := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)
	taskLauncher.UseTurnRouter(intake.NewTurnRouter(routerLanguageModel, agentcontract.IntakeOptions{IsEnabled: true}))
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, noticeLanguageModel))
	return taskLauncher
}
