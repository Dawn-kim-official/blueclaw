package integration

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/auth"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestMagicLinkLoginOwnTaskList(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	magicLinkService := auth.NewMagicLinkService()
	sessionService := auth.NewSessionService()
	taskAuthService := task.NewTaskAuthService(magicLinkService, sessionService, taskRunService)

	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "hello")
	token, errorValue := taskAuthService.IssueMagicLink("person-1", taskRun.TaskRunID)
	if errorValue != nil {
		t.Fatalf("expected magic link issuance to succeed: %v", errorValue)
	}

	taskSession, errorValue := taskAuthService.ConsumeMagicLink(token)
	if errorValue != nil {
		t.Fatalf("expected magic link consumption to succeed: %v", errorValue)
	}

	if taskSession.PersonID != "person-1" {
		t.Fatalf("expected session person to match, got %s", taskSession.PersonID)
	}
	if len(taskRunService.ListTaskRunByPersonID("person-1")) != 1 {
		t.Fatal("expected one visible task run")
	}
}
