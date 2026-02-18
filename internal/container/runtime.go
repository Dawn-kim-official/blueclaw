package container

import (
	"context"
	"io"
	"log"
	"strings"
)

type BindMount struct {
	Source string
	Target string
}

type ContainerConfig struct {
	Image       string
	Name        string
	Mounts      []BindMount
	Environment map[string]string
	NetworkMode string
}

type ContainerInfo struct {
	ID     string
	Name   string
	Status string
}

// InteractiveSession represents a bidirectional line-oriented connection to a container process.
type InteractiveSession interface {
	WriteLine(line string) error
	ReadLine() (string, error)
	Close() error
}

func CleanOrphanedContainers(cleanContext context.Context, runtime ContainerRuntime) {
	containers, err := runtime.ListContainers(cleanContext, "")
	if err != nil {
		log.Printf("warning: could not list containers for cleanup: %v", err)
		return
	}
	for _, containerInfo := range containers {
		if !strings.HasPrefix(containerInfo.Name, "blueclaw-") {
			continue
		}
		if err := runtime.RemoveContainer(cleanContext, containerInfo.ID); err != nil {
			log.Printf("warning: could not remove orphaned container %s: %v", containerInfo.Name, err)
		} else {
			log.Printf("removed orphaned container %s", containerInfo.Name)
		}
	}
}

type ContainerRuntime interface {
	IsAvailable(context context.Context) error
	CreateContainer(context context.Context, configuration ContainerConfig) (string, error)
	StartContainer(context context.Context, containerID string) error
	StopContainer(context context.Context, containerID string) error
	RemoveContainer(context context.Context, containerID string) error
	ExecInContainer(context context.Context, containerID string, command []string, stdin io.Reader) (string, error)
	ExecInteractive(context context.Context, containerID string, command []string) (InteractiveSession, error)
	ListContainers(context context.Context, labelFilter string) ([]ContainerInfo, error)
}
