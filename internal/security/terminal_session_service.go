package security

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"

	"blueclaw/internal/config"
)

type CommandResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type TerminalSession struct {
	SessionID           string
	command             *exec.Cmd
	standardInputWriter interface {
		Write([]byte) (int, error)
		Close() error
	}
	standardOutputBuffer *bytes.Buffer
	standardErrorBuffer  *bytes.Buffer
}

type TerminalSessionService struct {
	commandGuardrailService CommandGuardrailService
	mutex                   sync.RWMutex
	terminalSessions        map[string]*TerminalSession
}

func NewTerminalSessionService(terminalConfiguration config.TerminalConfiguration) *TerminalSessionService {
	return &TerminalSessionService{
		commandGuardrailService: NewCommandGuardrailService(terminalConfiguration),
		terminalSessions:        map[string]*TerminalSession{},
	}
}

func (terminalSessionService *TerminalSessionService) RunCommand(commandRequest CommandRequest) (CommandResult, error) {
	commandPlan, errorValue := terminalSessionService.commandGuardrailService.BuildCommandPlan(commandRequest)
	if errorValue != nil {
		return CommandResult{}, errorValue
	}

	ctx, cancelFunction := context.WithTimeout(context.Background(), commandPlan.Timeout)
	defer cancelFunction()

	command := exec.CommandContext(ctx, commandPlan.ExecutablePath, commandPlan.Arguments...)
	command.Dir = commandPlan.WorkingDirectoryPath
	command.Env = mapEnvironmentVariables(commandPlan.EnvironmentVariables)

	var standardOutputBuffer bytes.Buffer
	var standardErrorBuffer bytes.Buffer
	command.Stdout = &standardOutputBuffer
	command.Stderr = &standardErrorBuffer

	errorValue = command.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return CommandResult{
			ExitCode: -1,
			Stdout:   standardOutputBuffer.String(),
			Stderr:   standardErrorBuffer.String(),
		}, errors.New("command timed out")
	}

	exitCode := 0
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}

	if errorValue != nil {
		return CommandResult{
			ExitCode: exitCode,
			Stdout:   standardOutputBuffer.String(),
			Stderr:   standardErrorBuffer.String(),
		}, errorValue
	}

	return CommandResult{
		ExitCode: exitCode,
		Stdout:   standardOutputBuffer.String(),
		Stderr:   standardErrorBuffer.String(),
	}, nil
}

func (terminalSessionService *TerminalSessionService) StartInteractiveSession(commandRequest CommandRequest) (string, error) {
	commandRequest.IsInteractive = true
	commandPlan, errorValue := terminalSessionService.commandGuardrailService.BuildCommandPlan(commandRequest)
	if errorValue != nil {
		return "", errorValue
	}

	ctx, _ := context.WithTimeout(context.Background(), maxDuration(commandPlan.Timeout, 30*time.Minute))
	command := exec.CommandContext(ctx, commandPlan.ExecutablePath, commandPlan.Arguments...)
	command.Dir = commandPlan.WorkingDirectoryPath
	command.Env = mapEnvironmentVariables(commandPlan.EnvironmentVariables)

	standardInputWriter, errorValue := command.StdinPipe()
	if errorValue != nil {
		return "", errorValue
	}

	standardOutputPipe, errorValue := command.StdoutPipe()
	if errorValue != nil {
		return "", errorValue
	}

	standardErrorPipe, errorValue := command.StderrPipe()
	if errorValue != nil {
		return "", errorValue
	}

	standardOutputBuffer := &bytes.Buffer{}
	standardErrorBuffer := &bytes.Buffer{}

	errorValue = command.Start()
	if errorValue != nil {
		return "", errorValue
	}

	go copyBuffer(standardOutputBuffer, standardOutputPipe)
	go copyBuffer(standardErrorBuffer, standardErrorPipe)

	sessionID := newIdentifier()
	terminalSessionService.mutex.Lock()
	terminalSessionService.terminalSessions[sessionID] = &TerminalSession{
		SessionID:            sessionID,
		command:              command,
		standardInputWriter:  standardInputWriter,
		standardOutputBuffer: standardOutputBuffer,
		standardErrorBuffer:  standardErrorBuffer,
	}
	terminalSessionService.mutex.Unlock()

	return sessionID, nil
}

func (terminalSessionService *TerminalSessionService) WriteSessionInput(sessionID string, input string) (CommandResult, error) {
	terminalSessionService.mutex.RLock()
	terminalSession, isFound := terminalSessionService.terminalSessions[sessionID]
	terminalSessionService.mutex.RUnlock()
	if !isFound {
		return CommandResult{}, errors.New("terminal session not found")
	}

	_, errorValue := terminalSession.standardInputWriter.Write([]byte(input))
	if errorValue != nil {
		return CommandResult{}, errorValue
	}

	time.Sleep(50 * time.Millisecond)
	return CommandResult{
		Stdout: terminalSession.standardOutputBuffer.String(),
		Stderr: terminalSession.standardErrorBuffer.String(),
	}, nil
}

func (terminalSessionService *TerminalSessionService) CloseSession(sessionID string) error {
	terminalSessionService.mutex.Lock()
	defer terminalSessionService.mutex.Unlock()

	terminalSession, isFound := terminalSessionService.terminalSessions[sessionID]
	if !isFound {
		return errors.New("terminal session not found")
	}

	_ = terminalSession.standardInputWriter.Close()
	if terminalSession.command.Process != nil {
		_ = terminalSession.command.Process.Kill()
	}

	delete(terminalSessionService.terminalSessions, sessionID)
	return nil
}

func mapEnvironmentVariables(environmentVariables map[string]string) []string {
	mappedEnvironmentVariables := []string{}
	for name, value := range environmentVariables {
		mappedEnvironmentVariables = append(mappedEnvironmentVariables, name+"="+value)
	}
	return mappedEnvironmentVariables
}

func copyBuffer(buffer *bytes.Buffer, reader interface{ Read([]byte) (int, error) }) {
	_, _ = buffer.ReadFrom(reader)
}

func maxDuration(left time.Duration, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}

func newIdentifier() string {
	return time.Now().UTC().Format("20060102150405.000000000")
}
