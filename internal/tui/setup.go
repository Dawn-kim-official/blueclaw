package tui

import (
	"context"
	"errors"
	"strings"

	"charm.land/bubbles/v2/textinput"

	"github.com/Dawn-kim-official/blueclaw/internal/enrollment"
)

type setupFieldID int

const (
	setupFieldDisplayName setupFieldID = iota
	setupFieldEmail
	setupFieldWorkspaceRootPath
	setupFieldDatabaseConnectionString
	setupFieldOpenRouterAPIKey
	setupFieldHarness
	setupFieldMode
	setupFieldMessenger
	setupFieldMessengerBaseURL
	setupFieldMessengerOpenness
)

var setupFieldOrder = []setupFieldID{
	setupFieldDisplayName,
	setupFieldEmail,
	setupFieldWorkspaceRootPath,
	setupFieldDatabaseConnectionString,
	setupFieldOpenRouterAPIKey,
	setupFieldHarness,
	setupFieldMode,
	setupFieldMessenger,
	setupFieldMessengerBaseURL,
	setupFieldMessengerOpenness,
}

type SetupModel struct {
	home             enrollment.Home
	textInputs       map[setupFieldID]textinput.Model
	answers          enrollment.Answers
	availableHarness []enrollment.HarnessChoice
	harnessIndex     int
	cursor           int
	failureNotice    string
	isComplete       bool
	checkResults     []enrollment.CheckResult
	isChecking       bool
}

func NewSetupModel(home enrollment.Home) SetupModel {
	availableHarness := enrollment.AvailableHarnesses()
	answers := enrollment.SuggestedAnswers(home)
	setupModel := SetupModel{
		home:             home,
		textInputs:       map[setupFieldID]textinput.Model{},
		answers:          answers,
		availableHarness: availableHarness,
		harnessIndex:     indexOfHarness(availableHarness, answers.Harness.Name),
	}
	for _, fieldID := range setupFieldOrder {
		if !isTextField(fieldID) {
			continue
		}
		textInput := textinput.New()
		textInput.Prompt = ""
		textInput.SetValue(setupModel.storedFieldValue(fieldID))
		if fieldID == setupFieldOpenRouterAPIKey {
			textInput.EchoMode = textinput.EchoPassword
		}
		setupModel.textInputs[fieldID] = textInput
	}
	setupModel.focusSelectedField()
	return setupModel
}

func isTextField(fieldID setupFieldID) bool {
	return fieldID != setupFieldHarness && fieldID != setupFieldMode && fieldID != setupFieldMessenger && fieldID != setupFieldMessengerOpenness
}

func (setupModel *SetupModel) focusSelectedField() {
	for fieldID, textInput := range setupModel.textInputs {
		if fieldID == setupFieldOrder[setupModel.cursor] {
			textInput.Focus()
		} else {
			textInput.Blur()
		}
		setupModel.textInputs[fieldID] = textInput
	}
}

func (setupModel *SetupModel) setTextField(fieldID setupFieldID, value string) {
	textInput := setupModel.textInputs[fieldID]
	textInput.SetValue(value)
	setupModel.textInputs[fieldID] = textInput
}

func (setupModel SetupModel) storedFieldValue(fieldID setupFieldID) string {
	switch fieldID {
	case setupFieldDisplayName:
		return setupModel.answers.DisplayName
	case setupFieldEmail:
		return setupModel.answers.Email
	case setupFieldWorkspaceRootPath:
		return setupModel.answers.WorkspaceRootPath
	case setupFieldDatabaseConnectionString:
		return setupModel.answers.DatabaseConnectionString
	case setupFieldOpenRouterAPIKey:
		return setupModel.answers.LanguageModel.OpenRouterAPIKey
	case setupFieldMessengerBaseURL:
		return setupModel.answers.Messenger.BaseURL
	}
	return ""
}

func (setupModel *SetupModel) readTextFieldsIntoAnswers() {
	setupModel.answers.DisplayName = setupModel.textInputs[setupFieldDisplayName].Value()
	setupModel.answers.Email = setupModel.textInputs[setupFieldEmail].Value()
	setupModel.answers.WorkspaceRootPath = setupModel.textInputs[setupFieldWorkspaceRootPath].Value()
	setupModel.answers.DatabaseConnectionString = setupModel.textInputs[setupFieldDatabaseConnectionString].Value()
	setupModel.answers.LanguageModel.OpenRouterAPIKey = setupModel.textInputs[setupFieldOpenRouterAPIKey].Value()
	setupModel.answers.Messenger.BaseURL = setupModel.textInputs[setupFieldMessengerBaseURL].Value()
}

func indexOfHarness(availableHarness []enrollment.HarnessChoice, harnessName string) int {
	for harnessIndex, candidate := range availableHarness {
		if candidate.Name == harnessName {
			return harnessIndex
		}
	}
	return 0
}

func (setupModel SetupModel) fieldLabel(fieldID setupFieldID) string {
	switch fieldID {
	case setupFieldDisplayName:
		return "Your name"
	case setupFieldEmail:
		return "Your email"
	case setupFieldWorkspaceRootPath:
		return "Workspace"
	case setupFieldDatabaseConnectionString:
		return "Postgres"
	case setupFieldOpenRouterAPIKey:
		return "OpenRouter key"
	case setupFieldHarness:
		return "Harness"
	case setupFieldMode:
		return "Mode"
	case setupFieldMessenger:
		return "Messenger"
	case setupFieldMessengerBaseURL:
		return "Messenger address"
	case setupFieldMessengerOpenness:
		return "Who may ask"
	}
	return ""
}

func (setupModel SetupModel) fieldValue(fieldID setupFieldID) string {
	switch fieldID {
	case setupFieldDisplayName:
		return setupModel.answers.DisplayName
	case setupFieldEmail:
		return setupModel.answers.Email
	case setupFieldWorkspaceRootPath:
		return setupModel.answers.WorkspaceRootPath
	case setupFieldDatabaseConnectionString:
		return setupModel.answers.DatabaseConnectionString
	case setupFieldOpenRouterAPIKey:
		return maskedSecret(setupModel.textInputs[setupFieldOpenRouterAPIKey].Value())
	case setupFieldHarness:
		return setupModel.selectedHarnessLabel()
	case setupFieldMode:
		return string(setupModel.answers.Mode)
	case setupFieldMessenger:
		return string(setupModel.answers.Messenger.Name)
	case setupFieldMessengerBaseURL:
		return setupModel.textInputs[setupFieldMessengerBaseURL].Value()
	case setupFieldMessengerOpenness:
		if setupModel.answers.Messenger.IsOpenToPeople {
			return "anyone in the workspace"
		}
		return "only people already registered"
	}
	return ""
}

func (setupModel SetupModel) selectedHarnessLabel() string {
	if setupModel.harnessIndex < 0 || setupModel.harnessIndex >= len(setupModel.availableHarness) {
		return ""
	}
	selectedHarness := setupModel.availableHarness[setupModel.harnessIndex]
	if strings.TrimSpace(selectedHarness.AgentCommandPath) == "" {
		return selectedHarness.Name
	}
	return selectedHarness.Name + " (" + selectedHarness.AgentCommandPath + ")"
}

func nextMessengerName(current enrollment.MessengerName) enrollment.MessengerName {
	switch current {
	case enrollment.MessengerNone:
		return enrollment.MessengerMattermost
	case enrollment.MessengerMattermost:
		return enrollment.MessengerBuzz
	}
	return enrollment.MessengerNone
}

func maskedSecret(secret string) string {
	trimmedSecret := strings.TrimSpace(secret)
	if trimmedSecret == "" {
		return ""
	}
	if len(trimmedSecret) <= 6 {
		return strings.Repeat("*", len(trimmedSecret))
	}
	return trimmedSecret[:3] + strings.Repeat("*", len(trimmedSecret)-6) + trimmedSecret[len(trimmedSecret)-3:]
}

func (setupModel *SetupModel) cycleSelectedChoice() {
	switch setupFieldOrder[setupModel.cursor] {
	case setupFieldHarness:
		if len(setupModel.availableHarness) == 0 {
			return
		}
		setupModel.harnessIndex = (setupModel.harnessIndex + 1) % len(setupModel.availableHarness)
		setupModel.answers.Harness = setupModel.availableHarness[setupModel.harnessIndex]
	case setupFieldMode:
		if setupModel.answers.Mode == enrollment.RunModeHost {
			setupModel.answers.Mode = enrollment.RunModeGuest
			return
		}
		setupModel.answers.Mode = enrollment.RunModeHost
	case setupFieldMessenger:
		setupModel.answers.Messenger.Name = nextMessengerName(setupModel.answers.Messenger.Name)
	case setupFieldMessengerOpenness:
		setupModel.answers.Messenger.IsOpenToPeople = !setupModel.answers.Messenger.IsOpenToPeople
	}
}

func (setupModel *SetupModel) RunPreflight() {
	setupModel.readTextFieldsIntoAnswers()
	setupModel.checkResults = enrollment.Preflight(context.Background(), setupModel.home, setupModel.answers)
	setupModel.isChecking = false
}

func (setupModel SetupModel) CheckResults() []enrollment.CheckResult {
	return setupModel.checkResults
}

func (setupModel *SetupModel) Finish() error {
	setupModel.readTextFieldsIntoAnswers()
	setupModel.RunPreflight()
	if !enrollment.IsReadyToStart(setupModel.checkResults) {
		setupModel.failureNotice = "blueclaw cannot start with these answers yet. Each ✗ above says what it needs."
		return errors.New(setupModel.failureNotice)
	}
	enrolled, errorValue := enrollment.NewLocalProvider(setupModel.home, setupModel.answers).Enroll(context.Background())
	if errorValue != nil {
		setupModel.failureNotice = errorValue.Error()
		return errorValue
	}
	if errorValue := enrollment.Materialize(setupModel.home, enrolled); errorValue != nil {
		setupModel.failureNotice = errorValue.Error()
		return errorValue
	}
	setupModel.failureNotice = ""
	setupModel.isComplete = true
	return nil
}
