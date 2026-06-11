package security

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"blueclaw/internal/config"
	"blueclaw/internal/hooks"

	"github.com/creack/pty"
)

const (
	terminalRunToolName      = "terminal.run"
	defaultRTKExecutablePath = "/workspace/.blueclaw/runtime/current/bin/rtk"
	defaultRTKRewriteTimeout = 10 * time.Second
)

type CommandResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timedOut"`
}

type TerminalSessionStatus struct {
	SessionID     string `json:"sessionID"`
	Status        string `json:"status"`
	ExitCode      int    `json:"exitCode"`
	Stdout        string `json:"stdout,omitempty"`
	Stderr        string `json:"stderr,omitempty"`
	RecentOutput  string `json:"recentOutput,omitempty"`
	OutputTrimmed bool   `json:"outputTrimmed,omitempty"`
}

type TerminalSession struct {
	SessionID           string
	command             *exec.Cmd
	standardInputWriter interface {
		Write([]byte) (int, error)
		Close() error
	}
	cancelFunction       context.CancelFunc
	ptyFile              *os.File
	standardOutputBuffer *outputRingBuffer
	standardErrorBuffer  *outputRingBuffer
	mutex                sync.RWMutex
	isExited             bool
	exitCode             int
}

type TerminalSessionService struct {
	commandGuardrailService CommandGuardrailService
	hookRunner              *hooks.Runner
	mutex                   sync.RWMutex
	terminalSessions        map[string]*TerminalSession
}

func NewTerminalSessionService(terminalConfiguration config.TerminalConfiguration) *TerminalSessionService {
	return newTerminalSessionService(terminalConfiguration, defaultRTKExecutablePath)
}

func newTerminalSessionService(terminalConfiguration config.TerminalConfiguration, rtkExecutablePath string) *TerminalSessionService {
	hookRunner := hooks.NewRunner()
	hookRunner.RegisterHook(hooks.EventPreToolUse, newRTKRewriteHook(rtkExecutablePath))

	return &TerminalSessionService{
		commandGuardrailService: NewCommandGuardrailService(terminalConfiguration),
		hookRunner:              hookRunner,
		terminalSessions:        map[string]*TerminalSession{},
	}
}

func (terminalSessionService *TerminalSessionService) RunCommand(ctx context.Context, commandRequest CommandRequest) (CommandResult, error) {
	commandRequest, errorValue := terminalSessionService.runPreToolUseHooks(commandRequest)
	if errorValue != nil {
		return CommandResult{ExitCode: -1, Stderr: errorValue.Error()}, errorValue
	}

	commandPlan, errorValue := terminalSessionService.commandGuardrailService.BuildCommandPlan(commandRequest)
	if errorValue != nil {
		return CommandResult{ExitCode: -1, Stderr: errorValue.Error()}, errorValue
	}
	if errorValue := terminalSessionService.prepareWorkingDirectory(commandPlan.WorkingDirectoryPath); errorValue != nil {
		return CommandResult{Stderr: errorValue.Error()}, errorValue
	}

	ctx, cancelFunction := context.WithTimeout(ctx, commandPlan.Timeout)
	defer cancelFunction()

	command := exec.CommandContext(ctx, commandPlan.ExecutablePath, commandPlan.Arguments...)
	configureCommandGroupKill(command)
	command.Dir = commandPlan.WorkingDirectoryPath
	command.Env = mapEnvironmentVariables(commandPlan.EnvironmentVariables)
	if strings.TrimSpace(commandPlan.Stdin) != "" {
		command.Stdin = strings.NewReader(commandPlan.Stdin)
	}

	var standardOutputBuffer bytes.Buffer
	var standardErrorBuffer bytes.Buffer
	command.Stdout = &standardOutputBuffer
	command.Stderr = &standardErrorBuffer

	errorValue = command.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return CommandResult{
			ExitCode: -1,
			Stdout:   truncateString(standardOutputBuffer.String(), terminalSessionService.outputMaxBytes()),
			Stderr:   truncateString(standardErrorBuffer.String(), terminalSessionService.outputMaxBytes()),
			TimedOut: true,
		}, errors.New("command timed out")
	}

	exitCode := 0
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}

	if errorValue != nil {
		standardError := standardErrorBuffer.String()
		if strings.TrimSpace(standardError) == "" {
			standardError = errorValue.Error()
		}
		return CommandResult{
			ExitCode: exitCode,
			Stdout:   truncateString(standardOutputBuffer.String(), terminalSessionService.outputMaxBytes()),
			Stderr:   truncateString(standardError, terminalSessionService.outputMaxBytes()),
		}, errorValue
	}

	return CommandResult{
		ExitCode: exitCode,
		Stdout:   truncateString(standardOutputBuffer.String(), terminalSessionService.outputMaxBytes()),
		Stderr:   truncateString(standardErrorBuffer.String(), terminalSessionService.outputMaxBytes()),
	}, nil
}

func configureCommandGroupKill(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 2 * time.Second
}

func (terminalSessionService *TerminalSessionService) runPreToolUseHooks(commandRequest CommandRequest) (CommandRequest, error) {
	toolInput, errorValue := terminalSessionService.hookRunner.Run(context.Background(), hooks.HookRequest{
		EventName: hooks.EventPreToolUse,
		ToolName:  terminalRunToolName,
		ToolInput: commandRequest,
	})
	if errorValue != nil {
		return commandRequest, errorValue
	}

	updatedCommandRequest, isCommandRequest := toolInput.(CommandRequest)
	if !isCommandRequest {
		return commandRequest, errors.New("terminal hook returned invalid tool input")
	}

	return updatedCommandRequest, nil
}

func newRTKRewriteHook(rtkExecutablePath string) hooks.Hook {
	return func(ctx context.Context, request hooks.HookRequest) (hooks.HookResult, error) {
		commandRequest, isCommandRequest := request.ToolInput.(CommandRequest)
		if !isCommandRequest || !canRewriteWithRTK(request, commandRequest) {
			return hooks.HookResult{}, nil
		}

		rewrittenCommand, isRewritten := rewriteCommandWithRTK(ctx, rtkExecutablePath, commandRequest.Command)
		if !isRewritten {
			return hooks.HookResult{}, nil
		}

		commandRequest.Command = rewrittenCommand
		return hooks.HookResult{UpdatedToolInput: commandRequest}, nil
	}
}

func canRewriteWithRTK(request hooks.HookRequest, commandRequest CommandRequest) bool {
	return request.EventName == hooks.EventPreToolUse &&
		request.ToolName == terminalRunToolName &&
		!commandRequest.IsInteractive &&
		!commandRequest.IsPTY &&
		strings.TrimSpace(commandRequest.Command) != ""
}

func rewriteCommandWithRTK(ctx context.Context, rtkExecutablePath string, command string) (string, bool) {
	rewriteContext, cancelFunction := context.WithTimeout(ctx, defaultRTKRewriteTimeout)
	defer cancelFunction()

	rewriteCommand := exec.CommandContext(rewriteContext, rtkExecutablePath, "rewrite", command)
	outputBytes, errorValue := rewriteCommand.Output()
	if errorValue != nil {
		return "", false
	}

	rewrittenCommand := strings.TrimSpace(string(outputBytes))
	if rewrittenCommand == "" || rewrittenCommand == strings.TrimSpace(command) {
		return "", false
	}

	return rewrittenCommand, true
}

func (terminalSessionService *TerminalSessionService) prepareWorkingDirectory(workingDirectoryPath string) error {
	if terminalSessionService.commandGuardrailService.terminalConfiguration.Mode != "firecrackerGuest" {
		return nil
	}
	fileInformation, errorValue := os.Stat(workingDirectoryPath)
	if errorValue != nil {
		return errorValue
	}
	if !fileInformation.IsDir() {
		return errors.New("working directory is not a directory")
	}
	return nil
}

func (terminalSessionService *TerminalSessionService) StartInteractiveSession(commandRequest CommandRequest) (string, error) {
	commandRequest.IsInteractive = true
	commandPlan, errorValue := terminalSessionService.commandGuardrailService.BuildCommandPlan(commandRequest)
	if errorValue != nil {
		return "", errorValue
	}
	if terminalSessionService.sessionCount() >= terminalSessionService.sessionMaxCount() {
		return "", errors.New("terminal session limit reached")
	}
	if errorValue := terminalSessionService.prepareWorkingDirectory(commandPlan.WorkingDirectoryPath); errorValue != nil {
		return "", errorValue
	}

	ctx, cancelFunction := context.WithTimeout(context.Background(), maxDuration(commandPlan.Timeout, 30*time.Minute))
	command := exec.CommandContext(ctx, commandPlan.ExecutablePath, commandPlan.Arguments...)
	command.Dir = commandPlan.WorkingDirectoryPath
	command.Env = mapEnvironmentVariables(commandPlan.EnvironmentVariables)

	sessionID := newIdentifier()
	terminalSession := &TerminalSession{
		SessionID:            sessionID,
		command:              command,
		cancelFunction:       cancelFunction,
		standardOutputBuffer: newOutputRingBuffer(terminalSessionService.outputMaxBytes()),
		standardErrorBuffer:  newOutputRingBuffer(terminalSessionService.outputMaxBytes()),
		exitCode:             -1,
	}
	if commandPlan.IsPTY {
		errorValue = terminalSession.startPTY(commandPlan)
	} else {
		errorValue = terminalSession.startPipe(commandPlan)
	}
	if errorValue != nil {
		cancelFunction()
		return "", errorValue
	}
	go terminalSession.wait()

	terminalSessionService.mutex.Lock()
	terminalSessionService.terminalSessions[sessionID] = terminalSession
	terminalSessionService.mutex.Unlock()

	if strings.TrimSpace(commandPlan.Stdin) != "" {
		_, _ = terminalSession.standardInputWriter.Write([]byte(commandPlan.Stdin + "\n"))
	}
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
	status := terminalSession.status()
	return CommandResult{ExitCode: status.ExitCode, Stdout: status.Stdout, Stderr: status.Stderr}, nil
}

func (terminalSessionService *TerminalSessionService) StatusSession(sessionID string) (TerminalSessionStatus, error) {
	terminalSessionService.mutex.RLock()
	terminalSession, isFound := terminalSessionService.terminalSessions[sessionID]
	terminalSessionService.mutex.RUnlock()
	if !isFound {
		return TerminalSessionStatus{}, errors.New("terminal session not found")
	}
	return terminalSession.status(), nil
}

func (terminalSessionService *TerminalSessionService) CloseSession(sessionID string) error {
	terminalSessionService.mutex.Lock()
	defer terminalSessionService.mutex.Unlock()

	terminalSession, isFound := terminalSessionService.terminalSessions[sessionID]
	if !isFound {
		return errors.New("terminal session not found")
	}

	_ = terminalSession.standardInputWriter.Close()
	if terminalSession.ptyFile != nil {
		_ = terminalSession.ptyFile.Close()
	}
	if terminalSession.command.Process != nil {
		_ = terminalSession.command.Process.Kill()
	}
	if terminalSession.cancelFunction != nil {
		terminalSession.cancelFunction()
	}

	delete(terminalSessionService.terminalSessions, sessionID)
	return nil
}

func (terminalSession *TerminalSession) startPipe(commandPlan CommandPlan) error {
	standardInputWriter, errorValue := terminalSession.command.StdinPipe()
	if errorValue != nil {
		return errorValue
	}
	standardOutputPipe, errorValue := terminalSession.command.StdoutPipe()
	if errorValue != nil {
		return errorValue
	}
	standardErrorPipe, errorValue := terminalSession.command.StderrPipe()
	if errorValue != nil {
		return errorValue
	}
	terminalSession.standardInputWriter = standardInputWriter
	if errorValue = terminalSession.command.Start(); errorValue != nil {
		return errorValue
	}
	go copyBuffer(terminalSession.standardOutputBuffer, standardOutputPipe)
	go copyBuffer(terminalSession.standardErrorBuffer, standardErrorPipe)
	return nil
}

func (terminalSession *TerminalSession) startPTY(commandPlan CommandPlan) error {
	ptyFile, errorValue := pty.Start(terminalSession.command)
	if errorValue != nil {
		return errorValue
	}
	terminalSession.ptyFile = ptyFile
	terminalSession.standardInputWriter = ptyFile
	go copyBuffer(terminalSession.standardOutputBuffer, ptyFile)
	_ = commandPlan
	return nil
}

func (terminalSession *TerminalSession) wait() {
	errorValue := terminalSession.command.Wait()
	exitCode := 0
	if terminalSession.command.ProcessState != nil {
		exitCode = terminalSession.command.ProcessState.ExitCode()
	} else if errorValue != nil {
		exitCode = -1
	}
	terminalSession.mutex.Lock()
	terminalSession.exitCode = exitCode
	terminalSession.isExited = true
	terminalSession.mutex.Unlock()
}

func (terminalSession *TerminalSession) status() TerminalSessionStatus {
	terminalSession.mutex.RLock()
	isExited := terminalSession.isExited
	exitCode := terminalSession.exitCode
	terminalSession.mutex.RUnlock()
	status := "running"
	if isExited {
		status = "exited"
	}
	stdout := terminalSession.standardOutputBuffer.String()
	stderr := terminalSession.standardErrorBuffer.String()
	return TerminalSessionStatus{
		SessionID:     terminalSession.SessionID,
		Status:        status,
		ExitCode:      exitCode,
		Stdout:        stdout,
		Stderr:        stderr,
		RecentOutput:  stdout + stderr,
		OutputTrimmed: terminalSession.standardOutputBuffer.IsTrimmed() || terminalSession.standardErrorBuffer.IsTrimmed(),
	}
}

func (terminalSessionService *TerminalSessionService) sessionCount() int {
	terminalSessionService.mutex.RLock()
	defer terminalSessionService.mutex.RUnlock()
	return len(terminalSessionService.terminalSessions)
}

func (terminalSessionService *TerminalSessionService) sessionMaxCount() int {
	if terminalSessionService.commandGuardrailService.terminalConfiguration.SessionMaxCount <= 0 {
		return 4
	}
	return terminalSessionService.commandGuardrailService.terminalConfiguration.SessionMaxCount
}

func (terminalSessionService *TerminalSessionService) outputMaxBytes() int {
	if terminalSessionService.commandGuardrailService.terminalConfiguration.OutputMaxBytes <= 0 {
		return 32768
	}
	return terminalSessionService.commandGuardrailService.terminalConfiguration.OutputMaxBytes
}

func mapEnvironmentVariables(environmentVariables map[string]string) []string {
	mappedEnvironmentVariables := []string{}
	for name, value := range environmentVariables {
		mappedEnvironmentVariables = append(mappedEnvironmentVariables, name+"="+value)
	}
	return mappedEnvironmentVariables
}

func copyBuffer(buffer interface{ Write([]byte) (int, error) }, reader interface{ Read([]byte) (int, error) }) {
	_, _ = io.Copy(buffer, reader)
}

type outputRingBuffer struct {
	mutex     sync.RWMutex
	maxBytes  int
	content   []byte
	isTrimmed bool
}

func newOutputRingBuffer(maxBytes int) *outputRingBuffer {
	if maxBytes <= 0 {
		maxBytes = 32768
	}
	return &outputRingBuffer{maxBytes: maxBytes}
}

func (outputRingBuffer *outputRingBuffer) Write(value []byte) (int, error) {
	outputRingBuffer.mutex.Lock()
	defer outputRingBuffer.mutex.Unlock()
	outputRingBuffer.content = append(outputRingBuffer.content, value...)
	if len(outputRingBuffer.content) > outputRingBuffer.maxBytes {
		outputRingBuffer.content = outputRingBuffer.content[len(outputRingBuffer.content)-outputRingBuffer.maxBytes:]
		outputRingBuffer.isTrimmed = true
	}
	return len(value), nil
}

func (outputRingBuffer *outputRingBuffer) String() string {
	outputRingBuffer.mutex.RLock()
	defer outputRingBuffer.mutex.RUnlock()
	return string(outputRingBuffer.content)
}

func (outputRingBuffer *outputRingBuffer) IsTrimmed() bool {
	outputRingBuffer.mutex.RLock()
	defer outputRingBuffer.mutex.RUnlock()
	return outputRingBuffer.isTrimmed
}

func truncateString(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes]
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
