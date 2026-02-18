package container

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

type AppleContainerRuntime struct{}

func NewAppleContainerRuntime() *AppleContainerRuntime {
	return &AppleContainerRuntime{}
}

func (runtime *AppleContainerRuntime) IsAvailable(context context.Context) error {
	command := exec.CommandContext(context, "container", "--version")
	if err := command.Run(); err != nil {
		return fmt.Errorf("Apple Container CLI is not available: %w", err)
	}
	return nil
}

func (runtime *AppleContainerRuntime) CreateContainer(context context.Context, configuration ContainerConfig) (string, error) {
	arguments := []string{"create", "--name", configuration.Name}
	for _, bindMount := range configuration.Mounts {
		arguments = append(arguments, "--mount", fmt.Sprintf("type=bind,source=%s,target=%s", bindMount.Source, bindMount.Target))
	}
	for key, value := range configuration.Environment {
		arguments = append(arguments, "--env", key+"="+value)
	}
	arguments = append(arguments, configuration.Image)
	command := exec.CommandContext(context, "container", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("creating Apple container: %s: %w", strings.TrimSpace(string(output)), err)
	}
	containerID := strings.TrimSpace(string(output))
	return containerID, nil
}

func (runtime *AppleContainerRuntime) StartContainer(context context.Context, containerID string) error {
	command := exec.CommandContext(context, "container", "start", containerID)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("starting Apple container %s: %s: %w", containerID, strings.TrimSpace(string(output)), err)
	}
	return nil
}

func (runtime *AppleContainerRuntime) StopContainer(context context.Context, containerID string) error {
	command := exec.CommandContext(context, "container", "stop", containerID)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("stopping Apple container %s: %s: %w", containerID, strings.TrimSpace(string(output)), err)
	}
	return nil
}

func (runtime *AppleContainerRuntime) RemoveContainer(context context.Context, containerID string) error {
	command := exec.CommandContext(context, "container", "delete", "--force", containerID)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("removing Apple container %s: %s: %w", containerID, strings.TrimSpace(string(output)), err)
	}
	return nil
}

func (runtime *AppleContainerRuntime) ExecInContainer(context context.Context, containerID string, commandArgs []string, stdin io.Reader) (string, error) {
	arguments := []string{"exec"}
	if stdin != nil {
		arguments = append(arguments, "-i")
	}
	arguments = append(arguments, containerID)
	arguments = append(arguments, commandArgs...)
	command := exec.CommandContext(context, "container", arguments...)
	if stdin != nil {
		command.Stdin = stdin
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("exec in Apple container %s: %s: %w", containerID, strings.TrimSpace(string(output)), err)
	}
	return string(output), nil
}

func (runtime *AppleContainerRuntime) ExecInteractive(execContext context.Context, containerID string, commandArgs []string) (InteractiveSession, error) {
	arguments := append([]string{"exec", "-i", containerID}, commandArgs...)
	command := exec.CommandContext(execContext, "container", arguments...)
	stdinPipe, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdin pipe for Apple container %s: %w", containerID, err)
	}
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		return nil, fmt.Errorf("creating stdout pipe for Apple container %s: %w", containerID, err)
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		stdinPipe.Close()
		stdoutPipe.Close()
		return nil, fmt.Errorf("creating stderr pipe for Apple container %s: %w", containerID, err)
	}
	if err := command.Start(); err != nil {
		stdinPipe.Close()
		stdoutPipe.Close()
		stderrPipe.Close()
		return nil, fmt.Errorf("starting interactive exec in Apple container %s: %w", containerID, err)
	}
	session := &appleInteractiveSession{
		writer:  stdinPipe,
		scanner: newLargeScanner(stdoutPipe),
		command: command,
	}
	go session.captureStderr(stderrPipe)
	return session, nil
}

func newLargeScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)
	return scanner
}

type appleInteractiveSession struct {
	writer      io.WriteCloser
	scanner     *bufio.Scanner
	command     *exec.Cmd
	stderrLines []string
	stderrMutex sync.Mutex
}

func (session *appleInteractiveSession) captureStderr(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		session.stderrMutex.Lock()
		session.stderrLines = append(session.stderrLines, line)
		session.stderrMutex.Unlock()
	}
}

func (session *appleInteractiveSession) stderrOutput() string {
	session.stderrMutex.Lock()
	defer session.stderrMutex.Unlock()
	return strings.Join(session.stderrLines, "\n")
}

func (session *appleInteractiveSession) WriteLine(line string) error {
	_, err := fmt.Fprintln(session.writer, line)
	return err
}

func (session *appleInteractiveSession) ReadLine() (string, error) {
	if !session.scanner.Scan() {
		if err := session.scanner.Err(); err != nil {
			return "", err
		}
		if stderr := session.stderrOutput(); stderr != "" {
			return "", fmt.Errorf("EOF (stderr: %s)", stderr)
		}
		return "", io.EOF
	}
	return session.scanner.Text(), nil
}

func (session *appleInteractiveSession) Close() error {
	session.writer.Close()
	return session.command.Wait()
}

func (runtime *AppleContainerRuntime) ListContainers(context context.Context, labelFilter string) ([]ContainerInfo, error) {
	command := exec.CommandContext(context, "container", "list", "--all", "--format", "json")
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("listing Apple containers: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return parseAppleContainerList(strings.TrimSpace(string(output)))
}

type appleContainerEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	State  string `json:"state"`
}

func parseAppleContainerList(jsonOutput string) ([]ContainerInfo, error) {
	var entries []appleContainerEntry
	if err := json.Unmarshal([]byte(jsonOutput), &entries); err != nil {
		return nil, fmt.Errorf("parsing container list JSON: %w", err)
	}
	containers := make([]ContainerInfo, 0, len(entries))
	for _, entry := range entries {
		status := entry.Status
		if status == "" {
			status = entry.State
		}
		containers = append(containers, ContainerInfo{
			ID:     entry.ID,
			Name:   entry.Name,
			Status: status,
		})
	}
	return containers, nil
}
