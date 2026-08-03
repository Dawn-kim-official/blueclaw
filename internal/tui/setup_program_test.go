package tui

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Dawn-kim-official/blueclaw/internal/enrollment"
)

type synchronizedBuffer struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (writer *synchronizedBuffer) Write(payload []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.buffer.Write(payload)
}

func (writer *synchronizedBuffer) String() string {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.buffer.String()
}

func runSetupProgram(t *testing.T, keystrokes string) (SetupModel, string) {
	t.Helper()
	home := enrollment.Home{DirectoryPath: filepath.Join(t.TempDir(), "blueclaw")}
	inputReader, inputWriter := io.Pipe()
	renderedOutput := &synchronizedBuffer{}

	program := tea.NewProgram(NewSetupModel(home),
		tea.WithInput(inputReader),
		tea.WithOutput(renderedOutput),
		tea.WithWindowSize(120, 40),
		tea.WithoutSignals(),
	)

	finished := make(chan tea.Model, 1)
	go func() {
		finalModel, _ := program.Run()
		finished <- finalModel
	}()

	time.Sleep(150 * time.Millisecond)
	inputWriter.Write([]byte(keystrokes))
	time.Sleep(250 * time.Millisecond)
	program.Quit()

	select {
	case finalModel := <-finished:
		setupModel, _ := finalModel.(SetupModel)
		inputWriter.Close()
		return setupModel, renderedOutput.String()
	case <-time.After(5 * time.Second):
		t.Fatal("expected the setup program to stop when asked")
	}
	return SetupModel{}, ""
}

func TestTheSetupScreenActuallyDrawsItsQuestions(t *testing.T) {
	_, renderedOutput := runSetupProgram(t, "")

	for _, expectedLabel := range []string{"blueclaw setup", "Your name", "Workspace", "Postgres", "Harness", "Mode"} {
		if !strings.Contains(renderedOutput, expectedLabel) {
			t.Fatalf("expected the setup screen to draw %q, got:\n%s", expectedLabel, renderedOutput)
		}
	}
}

func TestTypingAndArrowKeysReachTheModelThroughARealProgram(t *testing.T) {
	setupModel, _ := runSetupProgram(t, "\x1b[B\x1b[B\x1b[Bmine")

	if !strings.Contains(setupModel.textInputs[setupFieldDatabaseConnectionString].Value(), "mine") {
		t.Fatalf("expected three downs and typing to edit the fourth field, got %q", setupModel.textInputs[setupFieldDatabaseConnectionString].Value())
	}
}

func TestArrowKeysCycleTheModeWithoutStealingTheSpaceBar(t *testing.T) {
	setupModel, _ := runSetupProgram(t, "\x1b[B\x1b[B\x1b[B\x1b[B\x1b[B\x1b[B\x1b[C")

	if setupModel.answers.Mode != enrollment.RunModeGuest {
		t.Fatalf("expected an arrow on the mode field to change it, got %q", setupModel.answers.Mode)
	}
}

func runDashboardProgram(t *testing.T, client *Client, keystrokes string) (Model, string) {
	t.Helper()
	inputReader, inputWriter := io.Pipe()
	renderedOutput := &synchronizedBuffer{}

	program := tea.NewProgram(NewModel(client, "").UseRequester("person-1"),
		tea.WithInput(inputReader),
		tea.WithOutput(renderedOutput),
		tea.WithWindowSize(120, 40),
		tea.WithoutSignals(),
	)

	finished := make(chan tea.Model, 1)
	go func() {
		finalModel, _ := program.Run()
		finished <- finalModel
	}()

	time.Sleep(150 * time.Millisecond)
	inputWriter.Write([]byte(keystrokes))
	time.Sleep(300 * time.Millisecond)
	program.Quit()

	select {
	case finalModel := <-finished:
		dashboardModel, _ := finalModel.(Model)
		inputWriter.Close()
		return dashboardModel, renderedOutput.String()
	case <-time.After(5 * time.Second):
		t.Fatal("expected the dashboard program to stop when asked")
	}
	return Model{}, ""
}

func TestTheDashboardCanStartAPieceOfWorkFromTheTerminal(t *testing.T) {
	submittedPrompts := []string{}
	taskServer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/admin/api/task/run" {
			requestBody, _ := io.ReadAll(request.Body)
			submittedPrompts = append(submittedPrompts, string(requestBody))
			responseWriter.Write([]byte(`{"taskRunID":"task-run-1","status":"running"}`))
			return
		}
		responseWriter.Write([]byte(`[]`))
	}))
	t.Cleanup(taskServer.Close)

	runDashboardProgram(t, NewClient(taskServer.URL, nil), "n회의록 정리해줘\r")

	if len(submittedPrompts) != 1 {
		t.Fatalf("expected one request to reach the sandbox, got %v", submittedPrompts)
	}
	if !strings.Contains(submittedPrompts[0], "회의록 정리해줘") || !strings.Contains(submittedPrompts[0], "person-1") {
		t.Fatalf("expected the typed request and the requester to be sent, got %s", submittedPrompts[0])
	}
}

func TestTheComposeScreenIsDrawnWhileWriting(t *testing.T) {
	taskServer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Write([]byte(`[]`))
	}))
	t.Cleanup(taskServer.Close)

	dashboardModel, _ := runDashboardProgram(t, NewClient(taskServer.URL, nil), "n회의록 정리")

	renderedScreen := dashboardModel.renderScreenBody()
	if !strings.Contains(renderedScreen, "New request") {
		t.Fatalf("expected the compose screen to be drawn, got:\n%s", renderedScreen)
	}
	if !strings.Contains(renderedScreen, "회의록 정리") {
		t.Fatal("expected what is being typed, including the space, to appear on screen")
	}
}

func TestAnEmptyRequestIsNotSent(t *testing.T) {
	submittedCount := 0
	taskServer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/admin/api/task/run" {
			submittedCount++
		}
		responseWriter.Write([]byte(`[]`))
	}))
	t.Cleanup(taskServer.Close)

	runDashboardProgram(t, NewClient(taskServer.URL, nil), "n\r")

	if submittedCount != 0 {
		t.Fatal("expected an empty request to start nothing")
	}
}
