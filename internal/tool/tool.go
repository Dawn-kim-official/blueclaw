package tool

import (
	"context"
	"fmt"
	"sync"
)

type Definition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Result struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

type Tool interface {
	Name() string
	Description() string
	ParameterSchema() map[string]any
	Execute(context context.Context, arguments map[string]any) (Result, error)
}

type Registry struct {
	tools map[string]Tool
	mutex sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

func (registry *Registry) Register(tool Tool) error {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	name := tool.Name()
	if _, exists := registry.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}
	registry.tools[name] = tool
	return nil
}

func (registry *Registry) Get(name string) (Tool, error) {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	tool, exists := registry.tools[name]
	if !exists {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return tool, nil
}

func (registry *Registry) ListDefinitions() []Definition {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	definitions := make([]Definition, 0, len(registry.tools))
	for _, tool := range registry.tools {
		definitions = append(definitions, Definition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.ParameterSchema(),
		})
	}
	return definitions
}
