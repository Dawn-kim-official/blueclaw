package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/blueclaw/blueclaw/internal/provider"
	"github.com/blueclaw/blueclaw/internal/tool"
)

type mockProvider struct {
	responses []provider.Response
	callIndex int
}

func (mock *mockProvider) Name() string { return "mock" }

func (mock *mockProvider) SendMessage(_ context.Context, _ provider.Request) (provider.Response, error) {
	if mock.callIndex >= len(mock.responses) {
		return provider.Response{}, fmt.Errorf("no more mock responses")
	}
	response := mock.responses[mock.callIndex]
	mock.callIndex++
	return response, nil
}

type echoTool struct{}

func (echoTool *echoTool) Name() string        { return "echo" }
func (echoTool *echoTool) Description() string  { return "Echoes input" }
func (echoTool *echoTool) ParameterSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (echoTool *echoTool) Execute(_ context.Context, arguments map[string]any) (tool.Result, error) {
	text, _ := arguments["text"].(string)
	return tool.Result{Output: "echo: " + text}, nil
}

func TestLoopNoToolCalls(t *testing.T) {
	mockProvider := &mockProvider{
		responses: []provider.Response{
			{Message: provider.Message{Role: "assistant", Content: "Hello!"}},
		},
	}
	registry := tool.NewRegistry()
	loop := NewLoop(mockProvider, registry)
	request := provider.Request{
		SystemPrompt: "test",
		Messages:     []provider.Message{{Role: "user", Content: "hi"}},
	}
	response, err := loop.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response.Message.Content != "Hello!" {
		t.Errorf("expected %q, got %q", "Hello!", response.Message.Content)
	}
	if mockProvider.callIndex != 1 {
		t.Errorf("expected 1 LLM call, got %d", mockProvider.callIndex)
	}
}

func TestLoopOneToolCallRoundTrip(t *testing.T) {
	mockProvider := &mockProvider{
		responses: []provider.Response{
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: "",
					ToolCalls: []provider.ToolCall{
						{ID: "call-1", Name: "echo", Arguments: map[string]any{"text": "test"}},
					},
				},
				ToolCalls: []provider.ToolCall{
					{ID: "call-1", Name: "echo", Arguments: map[string]any{"text": "test"}},
				},
			},
			{Message: provider.Message{Role: "assistant", Content: "Got echo: test"}},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&echoTool{})
	loop := NewLoop(mockProvider, registry)
	request := provider.Request{
		SystemPrompt: "test",
		Messages:     []provider.Message{{Role: "user", Content: "echo test"}},
	}
	response, err := loop.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response.Message.Content != "Got echo: test" {
		t.Errorf("expected %q, got %q", "Got echo: test", response.Message.Content)
	}
	if mockProvider.callIndex != 2 {
		t.Errorf("expected 2 LLM calls, got %d", mockProvider.callIndex)
	}
}

func TestLoopStopsOnContextCancellation(t *testing.T) {
	loopContext, cancel := context.WithCancel(context.Background())
	callCount := 0
	cancellingProvider := &cancelOnCallProvider{cancel: cancel, callCount: &callCount}
	registry := tool.NewRegistry()
	registry.Register(&echoTool{})
	loop := NewLoop(cancellingProvider, registry)
	request := provider.Request{
		SystemPrompt: "test",
		Messages:     []provider.Message{{Role: "user", Content: "loop forever"}},
	}
	response, err := loop.Run(loopContext, request)
	if err != nil {
		t.Fatalf("expected graceful partial response, got error: %v", err)
	}
	if response.Message.Content == "" {
		t.Fatal("expected non-empty partial response content")
	}
	if callCount == 0 {
		t.Fatal("expected at least one LLM call before cancellation")
	}
}

func TestLoopContextDeadlineExceeded(t *testing.T) {
	slowProvider := &slowMockProvider{delay: 200 * time.Millisecond}
	registry := tool.NewRegistry()
	loop := NewLoop(slowProvider, registry)
	loopContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	request := provider.Request{
		SystemPrompt: "test",
		Messages:     []provider.Message{{Role: "user", Content: "slow"}},
	}
	response, err := loop.Run(loopContext, request)
	if err != nil {
		t.Fatalf("expected graceful partial response, got error: %v", err)
	}
	if response.Message.Content == "" {
		t.Fatal("expected non-empty partial response content")
	}
}

func TestLoopUnknownToolReturnsError(t *testing.T) {
	mockProvider := &mockProvider{
		responses: []provider.Response{
			{
				Message: provider.Message{
					Role: "assistant",
					ToolCalls: []provider.ToolCall{
						{ID: "call-1", Name: "nonexistent", Arguments: map[string]any{}},
					},
				},
				ToolCalls: []provider.ToolCall{
					{ID: "call-1", Name: "nonexistent", Arguments: map[string]any{}},
				},
			},
			{Message: provider.Message{Role: "assistant", Content: "handled error"}},
		},
	}
	registry := tool.NewRegistry()
	loop := NewLoop(mockProvider, registry)
	request := provider.Request{
		SystemPrompt: "test",
		Messages:     []provider.Message{{Role: "user", Content: "use unknown tool"}},
	}
	response, err := loop.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response.Message.Content != "handled error" {
		t.Errorf("expected %q, got %q", "handled error", response.Message.Content)
	}
}


type cancelOnCallProvider struct {
	cancel    context.CancelFunc
	callCount *int
}

func (p *cancelOnCallProvider) Name() string { return "cancel-on-call" }
func (p *cancelOnCallProvider) SendMessage(requestContext context.Context, _ provider.Request) (provider.Response, error) {
	*p.callCount++
	p.cancel()
	select {
	case <-requestContext.Done():
		return provider.Response{}, requestContext.Err()
	default:
		return provider.Response{
			Message: provider.Message{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{
					{ID: "call-1", Name: "echo", Arguments: map[string]any{"text": "loop"}},
				},
			},
			ToolCalls: []provider.ToolCall{
				{ID: "call-1", Name: "echo", Arguments: map[string]any{"text": "loop"}},
			},
		}, nil
	}
}

type slowMockProvider struct {
	delay time.Duration
}

func (mock *slowMockProvider) Name() string { return "slow-mock" }

func (mock *slowMockProvider) SendMessage(requestContext context.Context, _ provider.Request) (provider.Response, error) {
	select {
	case <-time.After(mock.delay):
		return provider.Response{Message: provider.Message{Role: "assistant", Content: "done"}}, nil
	case <-requestContext.Done():
		return provider.Response{}, requestContext.Err()
	}
}
