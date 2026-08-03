package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Dawn-kim-official/blueclaw/internal/enrollment"
)

func setupModelFixture(t *testing.T) SetupModel {
	t.Helper()
	return NewSetupModel(enrollment.Home{DirectoryPath: filepath.Join(t.TempDir(), "blueclaw")})
}

func typeText(setupModel SetupModel, text string) SetupModel {
	for _, character := range text {
		updated, _ := setupModel.Update(tea.KeyPressMsg{Code: character, Text: string(character)})
		setupModel = updated.(SetupModel)
	}
	return setupModel
}

func pressKey(setupModel SetupModel, keyName string) SetupModel {
	updated, _ := setupModel.Update(keyPressForName(keyName))
	return updated.(SetupModel)
}

func keyPressForName(keyName string) tea.KeyPressMsg {
	switch keyName {
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	return tea.KeyPressMsg{Code: tea.KeySpace}
}

func TestSetupWillNotFinishAnInstallThatCannotStart(t *testing.T) {
	setupModel := setupModelFixture(t)
	setupModel.setTextField(setupFieldOpenRouterAPIKey, "test-key")
	setupModel.answers.DatabaseConnectionString = "postgres://nobody@127.0.0.1:1/blueclaw?sslmode=disable&connect_timeout=1"

	if errorValue := (&setupModel).Finish(); errorValue == nil {
		t.Fatalf("expected setup to refuse while a dependency is unreachable, because the alternative is an install that only looks finished; checks were %+v", setupModel.CheckResults())
	}
	if setupModel.home.IsEnrolled() {
		t.Fatal("expected nothing to be written when the checks did not pass")
	}
	if !hasFailedCheck(setupModel, enrollment.CheckDatabase) {
		t.Fatalf("expected the unreachable database to be named, got %+v", setupModel.CheckResults())
	}
}

func hasFailedCheck(setupModel SetupModel, checkName enrollment.CheckName) bool {
	for _, checkResult := range setupModel.CheckResults() {
		if checkResult.Name == checkName && !checkResult.IsReady {
			return true
		}
	}
	return false
}

func TestSetupNamesEveryDependencyItCouldNotReach(t *testing.T) {
	setupModel := setupModelFixture(t)
	setupModel.setTextField(setupFieldOpenRouterAPIKey, "")
	setupModel.answers.Harness = enrollment.HarnessChoice{Name: "claude-code", AgentCommandPath: "/nonexistent/claude"}

	(&setupModel).RunPreflight()

	if !hasFailedCheck(setupModel, enrollment.CheckLanguageModel) {
		t.Fatal("expected a missing model path to be reported")
	}
	if !hasFailedCheck(setupModel, enrollment.CheckHarness) {
		t.Fatal("expected a harness command that is not installed to be reported")
	}
}

func TestSetupRefusesToFinishWithNoWayToReachAModel(t *testing.T) {
	setupModel := setupModelFixture(t)
	setupModel.setTextField(setupFieldOpenRouterAPIKey, "")

	if errorValue := (&setupModel).Finish(); errorValue == nil {
		t.Fatal("expected setup to refuse, because an install that cannot reach a model fails at the first turn instead")
	}
	if setupModel.failureNotice == "" {
		t.Fatal("expected the person setting this up to be told why")
	}
	if setupModel.home.IsEnrolled() {
		t.Fatal("expected nothing to be written when setup could not finish")
	}
}

func TestTypingEditsTheSelectedFieldOnly(t *testing.T) {
	setupModel := setupModelFixture(t)
	setupModel.setTextField(setupFieldDisplayName, "")
	originalEmail := setupModel.textInputs[setupFieldEmail].Value()

	setupModel = typeText(setupModel, "lee")

	if setupModel.textInputs[setupFieldDisplayName].Value() != "lee" {
		t.Fatalf("expected typing to edit the selected field, got %q", setupModel.textInputs[setupFieldDisplayName].Value())
	}
	if setupModel.textInputs[setupFieldEmail].Value() != originalEmail {
		t.Fatalf("expected other fields to be left alone, got %q", setupModel.textInputs[setupFieldEmail].Value())
	}
}

func TestTheSetupScreenShowsEveryQuestionItWillAsk(t *testing.T) {
	setupModel := setupModelFixture(t)

	renderedScreen := setupModel.View().Content

	for _, expectedLabel := range []string{"blueclaw setup", "Your name", "Workspace", "Postgres", "Harness", "Mode"} {
		if !strings.Contains(renderedScreen, expectedLabel) {
			t.Fatalf("expected the setup screen to show %q, got:\n%s", expectedLabel, renderedScreen)
		}
	}
}

func TestTheKeyIsNeverShownBackInFull(t *testing.T) {
	setupModel := setupModelFixture(t)
	setupModel.setTextField(setupFieldOpenRouterAPIKey, "sk-or-v1-secret-value")

	shownValue := setupModel.fieldValue(setupFieldOpenRouterAPIKey)

	if shownValue == "sk-or-v1-secret-value" {
		t.Fatal("expected the key to be masked on screen, because setup runs where people can see the terminal")
	}
	if shownValue == "" {
		t.Fatal("expected some evidence the key is set, so it is not mistaken for missing")
	}
}
