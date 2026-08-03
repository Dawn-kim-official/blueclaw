package enrollment

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/config"
)

func homeFixture(t *testing.T) Home {
	t.Helper()
	return Home{DirectoryPath: filepath.Join(t.TempDir(), "blueclaw")}
}

func completeAnswers(home Home) Answers {
	return Answers{
		DisplayName:              "이동혁",
		Email:                    "lee@example.com",
		Mode:                     RunModeHost,
		WorkspaceRootPath:        home.WorkspaceRootPath(),
		DatabaseConnectionString: "postgres://blueclaw@127.0.0.1:5432/blueclaw?sslmode=disable",
		LanguageModel:            LanguageModelAccess{OpenRouterAPIKey: "test-key"},
		Harness:                  HarnessChoice{Name: "claude-code", AgentCommandPath: "/usr/local/bin/claude"},
	}
}

func TestAFreshInstallIsNotEnrolledUntilItHasBothDocuments(t *testing.T) {
	home := homeFixture(t)
	if home.IsEnrolled() {
		t.Fatal("expected a fresh install to know it has nothing yet")
	}

	enrolled, errorValue := NewLocalProvider(home, completeAnswers(home)).Enroll(context.Background())
	if errorValue != nil {
		t.Fatalf("expected a single person to be enough to enroll: %v", errorValue)
	}
	if errorValue := Materialize(home, enrolled); errorValue != nil {
		t.Fatalf("expected the enrollment to be written: %v", errorValue)
	}
	if !home.IsEnrolled() {
		t.Fatal("expected the install to be enrolled once both documents exist")
	}
}

func TestTheWrittenConfigurationIsTheOneTheRuntimeLoads(t *testing.T) {
	home := homeFixture(t)
	enrolled, _ := NewLocalProvider(home, completeAnswers(home)).Enroll(context.Background())
	if errorValue := Materialize(home, enrolled); errorValue != nil {
		t.Fatalf("expected the enrollment to be written: %v", errorValue)
	}

	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(home.RuntimeConfigurationPath())
	if errorValue != nil {
		t.Fatalf("expected the runtime to load what onboarding wrote, because otherwise setup only looks finished: %v", errorValue)
	}
	if runtimeConfiguration.Terminal.WorkspaceRootPath != home.WorkspaceRootPath() {
		t.Fatalf("expected the workspace to be the one enrolled, got %q", runtimeConfiguration.Terminal.WorkspaceRootPath)
	}
	if runtimeConfiguration.Agent.Harness.Name != "claude-code" {
		t.Fatalf("expected the chosen harness to survive setup, got %q", runtimeConfiguration.Agent.Harness.Name)
	}
}

func TestTheOperatorBecomesAPersonTheAgentCanRunAs(t *testing.T) {
	home := homeFixture(t)
	enrolled, _ := NewLocalProvider(home, completeAnswers(home)).Enroll(context.Background())
	Materialize(home, enrolled)

	policyDocument := map[string]any{}
	policyBytes, errorValue := os.ReadFile(home.PolicyPath())
	if errorValue != nil {
		t.Fatalf("expected a policy to be written: %v", errorValue)
	}
	if errorValue := json.Unmarshal(policyBytes, &policyDocument); errorValue != nil {
		t.Fatalf("expected the policy to be readable: %v", errorValue)
	}
	people, _ := policyDocument["people"].([]any)
	if len(people) != 1 {
		t.Fatalf("expected the person who set this up to exist, got %v", policyDocument["people"])
	}
	if !strings.Contains(string(policyBytes), "lee@example.com") {
		t.Fatal("expected the operator's email in the policy, because that is how a message is matched to a person")
	}
}

func TestAnEnrollmentWithNoWayToReachAModelIsRefused(t *testing.T) {
	home := homeFixture(t)
	answers := completeAnswers(home)
	answers.LanguageModel = LanguageModelAccess{}

	if _, errorValue := NewLocalProvider(home, answers).Enroll(context.Background()); errorValue == nil {
		t.Fatal("expected setup to refuse an install that cannot reach a model, rather than failing later at the first turn")
	}
}

func TestHomeFollowsTheEnvironmentSoOneMachineCanHoldSeveralInstalls(t *testing.T) {
	t.Setenv("BLUECLAW_HOME", "/tmp/blueclaw-test-home")
	if home := ResolveHome(); home.DirectoryPath != "/tmp/blueclaw-test-home" {
		t.Fatalf("expected the configured home to win, got %q", home.DirectoryPath)
	}
}

func TestAnInstallRemembersTheDatabasePortItChose(t *testing.T) {
	home := homeFixture(t)
	answers := completeAnswers(home)
	answers.DatabaseConnectionString = NewManagedPostgresOnPort(home, 25999).ConnectionString()
	enrolled, _ := NewLocalProvider(home, answers).Enroll(context.Background())
	if errorValue := Materialize(home, enrolled); errorValue != nil {
		t.Fatalf("expected the enrolment to be written: %v", errorValue)
	}

	runtimeConfiguration, _ := config.LoadRuntimeConfiguration(home.RuntimeConfigurationPath())
	managedPostgres, isManaged := ManagedPostgresForConnectionString(home, runtimeConfiguration.Database.ConnectionString)

	if !isManaged {
		t.Fatal("expected the install to recognise the database it manages itself")
	}
	if managedPostgres.Port() != 25999 {
		t.Fatalf("expected the install to start the database on the port it recorded, got %d", managedPostgres.Port())
	}
}

func TestADatabaseSomebodyElseRunsIsNotTreatedAsOurs(t *testing.T) {
	home := homeFixture(t)

	if _, isManaged := ManagedPostgresForConnectionString(home, "postgres://someone@db.example.com:5432/blueclaw"); isManaged {
		t.Fatal("expected a database we did not create to be left alone rather than started by us")
	}
}

func enrolledWithMessenger(t *testing.T, home Home, messenger MessengerChoice) config.RuntimeConfiguration {
	t.Helper()
	answers := completeAnswers(home)
	answers.Messenger = messenger
	enrolled, errorValue := NewLocalProvider(home, answers).Enroll(context.Background())
	if errorValue != nil {
		t.Fatalf("expected enrolment with a messenger to be accepted: %v", errorValue)
	}
	if errorValue := Materialize(home, enrolled); errorValue != nil {
		t.Fatalf("expected the enrolment to be written: %v", errorValue)
	}
	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(home.RuntimeConfigurationPath())
	if errorValue != nil {
		t.Fatalf("expected the runtime to load it: %v", errorValue)
	}
	return runtimeConfiguration
}

func TestChoosingMattermostAtSetupConnectsItAtStartup(t *testing.T) {
	home := homeFixture(t)

	runtimeConfiguration := enrolledWithMessenger(t, home, MessengerChoice{Name: MessengerMattermost, BaseURL: "https://chat.example.com"})

	if runtimeConfiguration.Connectors.Mattermost.BaseURL != "https://chat.example.com" {
		t.Fatalf("expected the workspace people already use to be wired up, got %q", runtimeConfiguration.Connectors.Mattermost.BaseURL)
	}
}

func TestChoosingBuzzAtSetupConnectsItAtStartup(t *testing.T) {
	home := homeFixture(t)

	runtimeConfiguration := enrolledWithMessenger(t, home, MessengerChoice{Name: MessengerBuzz})

	if !runtimeConfiguration.Connectors.Buzz.Enabled {
		t.Fatal("expected buzz to be turned on for an install that chose it")
	}
}

func TestAMattermostConnectionWithNoAddressIsRefusedAtSetup(t *testing.T) {
	home := homeFixture(t)
	answers := completeAnswers(home)
	answers.Messenger = MessengerChoice{Name: MessengerMattermost}

	if _, errorValue := NewLocalProvider(home, answers).Enroll(context.Background()); errorValue == nil {
		t.Fatal("expected setup to refuse a messenger it has no address for, rather than starting with a connector that reaches nothing")
	}
}

func TestAnInstallWithNoMessengerStaysLocal(t *testing.T) {
	home := homeFixture(t)

	runtimeConfiguration := enrolledWithMessenger(t, home, MessengerChoice{Name: MessengerNone})

	if runtimeConfiguration.Connectors.Mattermost.BaseURL != "" || runtimeConfiguration.Connectors.Buzz.Enabled {
		t.Fatal("expected an install that connected nothing to stay local")
	}
}

func TestAColleagueBecomesAPersonTheSandboxCanRunAs(t *testing.T) {
	home := homeFixture(t)
	enrolled, _ := NewLocalProvider(home, completeAnswers(home)).Enroll(context.Background())
	Materialize(home, enrolled)

	wasAdded, errorValue := RegisterPerson(home, Person{DisplayName: "김예시", Email: "seeun@example.com"})
	if errorValue != nil {
		t.Fatalf("expected a colleague to be registered: %v", errorValue)
	}
	if !wasAdded {
		t.Fatal("expected a person who was not there to be added")
	}

	operator, _ := OperatorPerson(home)
	policyBytes, _ := os.ReadFile(home.PolicyPath())
	if !strings.Contains(string(policyBytes), "seeun@example.com") {
		t.Fatal("expected the colleague's email in the policy, because that is how their messages are matched to them")
	}
	if operator.Email == "seeun@example.com" {
		t.Fatal("expected the operator to stay the operator")
	}
}

func TestRegisteringTheSamePersonTwiceChangesNothing(t *testing.T) {
	home := homeFixture(t)
	enrolled, _ := NewLocalProvider(home, completeAnswers(home)).Enroll(context.Background())
	Materialize(home, enrolled)
	RegisterPerson(home, Person{DisplayName: "김예시", Email: "seeun@example.com"})

	wasAdded, errorValue := RegisterPerson(home, Person{DisplayName: "김예시", Email: "SEEUN@example.com"})

	if errorValue != nil {
		t.Fatalf("expected a repeat registration to be harmless: %v", errorValue)
	}
	if wasAdded {
		t.Fatal("expected somebody who already exists not to be added twice under a different capitalisation")
	}
}

func TestAColleagueDoesNotArriveAsAnAdministrator(t *testing.T) {
	home := homeFixture(t)
	enrolled, _ := NewLocalProvider(home, completeAnswers(home)).Enroll(context.Background())
	Materialize(home, enrolled)
	RegisterPerson(home, Person{DisplayName: "김예시", Email: "seeun@example.com"})

	policyBytes, _ := os.ReadFile(home.PolicyPath())
	document := map[string]any{}
	json.Unmarshal(policyBytes, &document)
	people, _ := document["people"].([]any)
	colleague, _ := people[len(people)-1].(map[string]any)

	if colleague["isAdmin"] == true {
		t.Fatal("expected somebody who simply spoke in the messenger not to arrive as an administrator")
	}
	if rank, _ := colleague["securityLevelRank"].(float64); rank >= 100 {
		t.Fatalf("expected a colleague to arrive at the lowest level, got rank %v", rank)
	}
}

func TestAnInstallCanActuallyRouteItsFirstRequest(t *testing.T) {
	home := homeFixture(t)
	enrolled, _ := NewLocalProvider(home, completeAnswers(home)).Enroll(context.Background())
	if errorValue := Materialize(home, enrolled); errorValue != nil {
		t.Fatalf("expected the enrolment to be written: %v", errorValue)
	}

	runtimeConfiguration, _ := config.LoadRuntimeConfiguration(home.RuntimeConfigurationPath())

	if !runtimeConfiguration.Agent.Intake.Enabled {
		t.Fatal("expected a fresh install to route requests, because with intake off every request fails with turn router disabled")
	}
}

func TestEveryEnrolledPersonCarriesWhatTheRecordRequires(t *testing.T) {
	home := homeFixture(t)
	enrolled, _ := NewLocalProvider(home, completeAnswers(home)).Enroll(context.Background())
	Materialize(home, enrolled)
	RegisterPerson(home, Person{DisplayName: "김예시", Email: "seeun@example.com"})

	policyBytes, _ := os.ReadFile(home.PolicyPath())
	document := map[string]any{}
	json.Unmarshal(policyBytes, &document)
	people, _ := document["people"].([]any)

	for _, entry := range people {
		person, _ := entry.(map[string]any)
		grantedClasses, _ := person["grantedClasses"].([]any)
		if len(grantedClasses) == 0 {
			t.Fatalf("expected every person to carry a granted class, because the record refuses one without it and the first request then fails on an unknown requester: %v", person)
		}
	}
}
