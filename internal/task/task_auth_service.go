package task

import (
	"errors"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/auth"
)

type TaskAuthService struct {
	magicLinkService *auth.MagicLinkService
	sessionService   *auth.SessionService
	taskRunService   *TaskRunService
}

func NewTaskAuthService(magicLinkService *auth.MagicLinkService, sessionService *auth.SessionService, taskRunService *TaskRunService) *TaskAuthService {
	return &TaskAuthService{
		magicLinkService: magicLinkService,
		sessionService:   sessionService,
		taskRunService:   taskRunService,
	}
}

func (taskAuthService *TaskAuthService) IssueMagicLink(personID string, taskRunID string) (string, error) {
	return taskAuthService.magicLinkService.IssueMagicLink(personID, taskRunID, 15*time.Minute)
}

func (taskAuthService *TaskAuthService) ConsumeMagicLink(token string) (TaskSession, error) {
	magicLink, isFound := taskAuthService.magicLinkService.ConsumeMagicLink(token)
	if !isFound {
		return TaskSession{}, errors.New("magic link is invalid")
	}

	session, errorValue := taskAuthService.sessionService.CreateSession(magicLink.PersonID, 8*time.Hour)
	if errorValue != nil {
		return TaskSession{}, errorValue
	}

	return TaskSession{
		TaskSessionID: session.SessionID,
		PersonID:      session.PersonID,
		ExpiresAt:     session.ExpiresAt,
	}, nil
}

func (taskAuthService *TaskAuthService) CreateTaskSession(personID string) (TaskSession, error) {
	session, errorValue := taskAuthService.sessionService.CreateSession(personID, 8*time.Hour)
	if errorValue != nil {
		return TaskSession{}, errorValue
	}

	return TaskSession{
		TaskSessionID: session.SessionID,
		PersonID:      session.PersonID,
		ExpiresAt:     session.ExpiresAt,
	}, nil
}

func (taskAuthService *TaskAuthService) AuthorizeTaskAccess(taskSessionID string, taskRunID string) (TaskRun, error) {
	session, isFound := taskAuthService.sessionService.ResolveSession(taskSessionID)
	if !isFound {
		return TaskRun{}, errors.New("task session is invalid")
	}

	taskRun, isFound := taskAuthService.taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return TaskRun{}, errors.New("task run not found")
	}
	if taskRun.RequesterPersonID != session.PersonID {
		return TaskRun{}, errors.New("task run access denied")
	}

	return taskRun, nil
}

func (taskAuthService *TaskAuthService) ResolveTaskSession(taskSessionID string) (TaskSession, error) {
	session, isFound := taskAuthService.sessionService.ResolveSession(taskSessionID)
	if !isFound {
		return TaskSession{}, errors.New("task session is invalid")
	}

	return TaskSession{
		TaskSessionID: session.SessionID,
		PersonID:      session.PersonID,
		ExpiresAt:     session.ExpiresAt,
	}, nil
}
