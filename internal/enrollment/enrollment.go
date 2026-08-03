package enrollment

import (
	"context"
	"errors"
	"strings"
)

type RunMode string

const (
	RunModeHost  RunMode = "host"
	RunModeGuest RunMode = "guest"
)

type Person struct {
	PersonID    string
	DisplayName string
	Email       string
}

type LanguageModelAccess struct {
	LLMDUnixSocketPath string
	LLMDAuthKeyPath    string
	OpenRouterAPIKey   string
}

func (access LanguageModelAccess) IsConfigured() bool {
	return strings.TrimSpace(access.LLMDUnixSocketPath) != "" || strings.TrimSpace(access.OpenRouterAPIKey) != ""
}

type MessengerName string

const (
	MessengerNone       MessengerName = "none"
	MessengerMattermost MessengerName = "mattermost"
	MessengerBuzz       MessengerName = "buzz"
)

type MessengerChoice struct {
	Name           MessengerName
	BaseURL        string
	IsOpenToPeople bool
}

func (choice MessengerChoice) IsConnected() bool {
	return choice.Name == MessengerMattermost || choice.Name == MessengerBuzz
}

type HarnessChoice struct {
	Name             string
	AgentCommandPath string
}

type Enrollment struct {
	TenantID                 string
	Mode                     RunMode
	Operator                 Person
	WorkspaceRootPath        string
	DatabaseConnectionString string
	LanguageModel            LanguageModelAccess
	Harness                  HarnessChoice
	Messenger                MessengerChoice
}

type Provider interface {
	Enroll(context.Context) (Enrollment, error)
}

var ErrEnrollmentIncomplete = errors.New("this install has no enrollment yet, so there is nobody for the agent to run as")

func (enrollment Enrollment) Validate() error {
	if strings.TrimSpace(enrollment.TenantID) == "" {
		return errors.New("an enrollment needs a tenant, because every person and every task belongs to one")
	}
	if strings.TrimSpace(enrollment.Operator.PersonID) == "" || strings.TrimSpace(enrollment.Operator.Email) == "" {
		return errors.New("an enrollment needs the person the agent runs as, because tools execute under their identity")
	}
	if enrollment.Mode != RunModeHost && enrollment.Mode != RunModeGuest {
		return errors.New("an enrollment needs a run mode, host or guest, because they join the workspace differently")
	}
	if strings.TrimSpace(enrollment.WorkspaceRootPath) == "" {
		return errors.New("an enrollment needs a workspace root, because that is where the agent's work lives")
	}
	if enrollment.Messenger.Name == MessengerMattermost && strings.TrimSpace(enrollment.Messenger.BaseURL) == "" {
		return errors.New("a Mattermost connection needs the server address the workspace lives at")
	}
	if !enrollment.LanguageModel.IsConfigured() {
		return errors.New("an enrollment needs a way to reach a language model, either a local llmd socket or an OpenRouter key")
	}
	return nil
}
