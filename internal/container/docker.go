package container

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/docker/docker/api/types"
	containerTypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

type DockerRuntime struct {
	client *client.Client
}

func NewDockerRuntime() (*DockerRuntime, error) {
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating Docker client: %w", err)
	}
	return &DockerRuntime{client: dockerClient}, nil
}

func (runtime *DockerRuntime) IsAvailable(context context.Context) error {
	_, err := runtime.client.Ping(context)
	if err != nil {
		return fmt.Errorf("Docker is not available: %w", err)
	}
	return nil
}

func (runtime *DockerRuntime) CreateContainer(context context.Context, configuration ContainerConfig) (string, error) {
	containerConfiguration := &containerTypes.Config{
		Image:  configuration.Image,
		Labels: map[string]string{"managed-by": "blueclaw"},
	}
	environmentVariables := make([]string, 0, len(configuration.Environment))
	for key, value := range configuration.Environment {
		environmentVariables = append(environmentVariables, key+"="+value)
	}
	containerConfiguration.Env = environmentVariables
	hostConfiguration := &containerTypes.HostConfig{}
	if len(configuration.Mounts) > 0 {
		mounts := make([]mount.Mount, len(configuration.Mounts))
		for index, bindMount := range configuration.Mounts {
			mounts[index] = mount.Mount{
				Type:   mount.TypeBind,
				Source: bindMount.Source,
				Target: bindMount.Target,
			}
		}
		hostConfiguration.Mounts = mounts
	}
	if configuration.NetworkMode != "" {
		hostConfiguration.NetworkMode = containerTypes.NetworkMode(configuration.NetworkMode)
	}
	response, err := runtime.client.ContainerCreate(context, containerConfiguration, hostConfiguration, nil, nil, configuration.Name)
	if err != nil {
		return "", fmt.Errorf("creating container: %w", err)
	}
	return response.ID, nil
}

func (runtime *DockerRuntime) StartContainer(context context.Context, containerID string) error {
	if err := runtime.client.ContainerStart(context, containerID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("starting container %s: %w", containerID, err)
	}
	return nil
}

func (runtime *DockerRuntime) StopContainer(context context.Context, containerID string) error {
	if err := runtime.client.ContainerStop(context, containerID, containerTypes.StopOptions{}); err != nil {
		return fmt.Errorf("stopping container %s: %w", containerID, err)
	}
	return nil
}

func (runtime *DockerRuntime) RemoveContainer(context context.Context, containerID string) error {
	if err := runtime.client.ContainerRemove(context, containerID, types.ContainerRemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("removing container %s: %w", containerID, err)
	}
	return nil
}

func (runtime *DockerRuntime) ExecInContainer(ctx context.Context, containerID string, command []string, stdin io.Reader) (string, error) {
	hasStdin := stdin != nil
	execConfiguration := types.ExecConfig{
		Cmd:          command,
		AttachStdin:  hasStdin,
		AttachStdout: true,
		AttachStderr: true,
	}
	execResult, err := runtime.client.ContainerExecCreate(ctx, containerID, execConfiguration)
	if err != nil {
		return "", fmt.Errorf("creating exec in container %s: %w", containerID, err)
	}
	attachResponse, err := runtime.client.ContainerExecAttach(ctx, execResult.ID, types.ExecStartCheck{})
	if err != nil {
		return "", fmt.Errorf("attaching to exec in container %s: %w", containerID, err)
	}
	defer attachResponse.Close()
	if hasStdin {
		if err := writeAndCloseStdin(attachResponse.Conn, stdin); err != nil {
			return "", fmt.Errorf("writing stdin to container %s: %w", containerID, err)
		}
	}
	var stdoutBuilder, stderrBuilder strings.Builder
	if _, err := stdcopy.StdCopy(&stdoutBuilder, &stderrBuilder, attachResponse.Reader); err != nil {
		return "", fmt.Errorf("reading exec output from container %s: %w", containerID, err)
	}
	return stdoutBuilder.String(), nil
}

func (runtime *DockerRuntime) ExecInteractive(executionContext context.Context, containerID string, command []string) (InteractiveSession, error) {
	execConfiguration := types.ExecConfig{
		Cmd:          command,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}
	execResult, err := runtime.client.ContainerExecCreate(executionContext, containerID, execConfiguration)
	if err != nil {
		return nil, fmt.Errorf("creating interactive exec in container %s: %w", containerID, err)
	}
	attachResponse, err := runtime.client.ContainerExecAttach(executionContext, execResult.ID, types.ExecStartCheck{})
	if err != nil {
		return nil, fmt.Errorf("attaching to interactive exec in container %s: %w", containerID, err)
	}
	stdoutReader, stdoutWriter := io.Pipe()
	session := &dockerInteractiveSession{
		connection: attachResponse.Conn,
		scanner:    newDockerScanner(stdoutReader),
		attachment: attachResponse,
	}
	go func() {
		defer stdoutWriter.Close()
		stderrWriter := &dockerStderrWriter{session: session}
		stdcopy.StdCopy(stdoutWriter, stderrWriter, attachResponse.Reader)
	}()
	return session, nil
}

func newDockerScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)
	return scanner
}

type dockerStderrWriter struct {
	session *dockerInteractiveSession
}

func (writer *dockerStderrWriter) Write(data []byte) (int, error) {
	writer.session.stderrMutex.Lock()
	writer.session.stderrCapture.Write(data)
	writer.session.stderrMutex.Unlock()
	return len(data), nil
}

type dockerInteractiveSession struct {
	connection    io.WriteCloser
	scanner       *bufio.Scanner
	attachment    types.HijackedResponse
	stderrCapture strings.Builder
	stderrMutex   sync.Mutex
}

func (session *dockerInteractiveSession) stderrOutput() string {
	session.stderrMutex.Lock()
	defer session.stderrMutex.Unlock()
	return strings.TrimSpace(session.stderrCapture.String())
}

func (session *dockerInteractiveSession) WriteLine(line string) error {
	_, err := fmt.Fprintln(session.connection, line)
	return err
}

func (session *dockerInteractiveSession) ReadLine() (string, error) {
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

func (session *dockerInteractiveSession) Close() error {
	session.connection.Close()
	session.attachment.Close()
	return nil
}

func writeAndCloseStdin(connection io.WriteCloser, stdin io.Reader) error {
	var buffer bytes.Buffer
	if _, err := io.Copy(&buffer, stdin); err != nil {
		return err
	}
	if _, err := connection.Write(buffer.Bytes()); err != nil {
		return err
	}
	return connection.Close()
}

func (runtime *DockerRuntime) ListContainers(context context.Context, labelFilter string) ([]ContainerInfo, error) {
	filterArgs := filters.NewArgs()
	if labelFilter != "" {
		filterArgs.Add("label", labelFilter)
	}
	containers, err := runtime.client.ContainerList(context, types.ContainerListOptions{
		All:     true,
		Filters: filterArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	result := make([]ContainerInfo, len(containers))
	for index, container := range containers {
		name := ""
		if len(container.Names) > 0 {
			name = strings.TrimPrefix(container.Names[0], "/")
		}
		result[index] = ContainerInfo{
			ID:     container.ID,
			Name:   name,
			Status: container.State,
		}
	}
	return result, nil
}

func (runtime *DockerRuntime) PullImage(context context.Context, imageName string) error {
	reader, err := runtime.client.ImagePull(context, imageName, types.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pulling image %s: %w", imageName, err)
	}
	defer reader.Close()
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return fmt.Errorf("reading pull response for %s: %w", imageName, err)
	}
	return nil
}
