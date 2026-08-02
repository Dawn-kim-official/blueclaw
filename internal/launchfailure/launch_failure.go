package launchfailure

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/model"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

type Completer struct {
	taskRunService taskstate.TaskRunStore
	languageModel  model.LanguageModelProvider
}

func NewCompleter(taskRunService taskstate.TaskRunStore, languageModel model.LanguageModelProvider) *Completer {
	return &Completer{taskRunService: taskRunService, languageModel: languageModel}
}

func (completer *Completer) CompleteLaunchFailure(responseContext context.Context, request agentcontract.AgentTurnRequest, phase string, stepName string, errorValue error) agentcontract.AgentTurnResult {
	taskRun, createError := completer.taskRunForLaunchFailure(request)
	reason := firstNonEmptyString(errorText(errorValue), errorText(createError))
	if createError != nil {
		reason = strings.TrimSpace(reason + "; task_run_create=" + createError.Error())
	}
	failedTaskRun, failError := completer.taskRunService.FailTaskRun(taskRun.TaskRunID, reason)
	if failError != nil {
		taskRun.Status = taskstate.TaskStatusFailed
		taskRun.FailureReason = firstNonEmptyString(reason, failError.Error())
		failedTaskRun = taskRun
	}
	launchFailureReport := agentcontract.FailureReport{
		Phase:              phase,
		StepName:           stepName,
		StopReason:         reason,
		SafeFailureSummary: reason,
		RawError:           reason,
		OriginalRequest:    request.Prompt,
		ResponseLanguage:   request.ResponseLanguage,
		DiagnosticEventID:  agentcontract.DiagnosticEventID(request, taskRun.TaskRunID, phase),
	}
	failureNotice, noticeStatus := (agentcontract.FailureNoticeGenerator{LanguageModel: completer.languageModel}).Generate(responseContext, launchFailureReport)
	completer.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.failure_reply", marshalEventBody(noticeStatus))
	completer.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.failure_report", marshalEventBody(map[string]any{
		"phase":      phase,
		"report":     launchFailureReport,
		"generation": noticeStatus,
	}))
	failedTaskRun = persistTaskRunResult(completer.taskRunService, failedTaskRun, failureNotice.SendableMessage())
	return agentcontract.AgentTurnResult{
		TaskRun:       failedTaskRun,
		UserNotice:    failedTaskRun.Result,
		FailureNotice: failureNotice,
		ToolNames:     toolNamesForEvent(request.ToolSet),
	}
}

func (completer *Completer) taskRunForLaunchFailure(request agentcontract.AgentTurnRequest) (taskstate.TaskRun, error) {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := completer.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun, nil
		}
	}
	return completer.taskRunService.CreateTaskRunWithOriginAndError(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}

func persistTaskRunResult(taskRunService taskstate.TaskRunStore, taskRun taskstate.TaskRun, result string) taskstate.TaskRun {
	persistedTaskRun, errorValue := taskRunService.RecordTaskRunResult(taskRun.TaskRunID, result)
	if errorValue != nil {
		taskRun.Result = result
		return taskRun
	}
	return persistedTaskRun
}

func toolNamesForEvent(toolSet *toolcontract.ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	return toolSet.ListToolNames()
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func errorText(errorValue error) string {
	if errorValue == nil {
		return ""
	}
	return errorValue.Error()
}

func marshalEventBody(value any) string {
	body, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ""
	}
	return string(body)
}
