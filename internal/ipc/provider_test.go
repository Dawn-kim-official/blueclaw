package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/blueclaw/blueclaw/internal/provider"
)

// setupTransportPair creates a pair of connected pipes where containerTransport
// is used by the container agent side, and daemonReader/daemonWriter are used
// by the test to simulate the daemon side.
func setupTransportPair(t *testing.T) (containerTransport *StdioTransport, daemonReader io.Reader, daemonWriter io.Writer) {
	t.Helper()
	containerIn, daemonOut := io.Pipe()
	daemonIn, containerOut := io.Pipe()
	t.Cleanup(func() {
		containerIn.Close()
		daemonOut.Close()
		daemonIn.Close()
		containerOut.Close()
	})
	return NewStdioTransport(containerOut, containerIn), daemonIn, daemonOut
}

func writeDaemonInbound(t *testing.T, writer io.Writer, inbound StdioInbound) {
	t.Helper()
	data, err := json.Marshal(inbound)
	if err != nil {
		t.Fatalf("marshal inbound: %v", err)
	}
	fmt.Fprintln(writer, string(data))
}

func readDaemonOutbound(t *testing.T, reader io.Reader) StdioOutbound {
	t.Helper()
	var line strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			line.WriteByte(buf[0])
		}
		if err != nil {
			t.Fatalf("reading outbound: %v", err)
		}
	}
	var outbound StdioOutbound
	if err := json.Unmarshal([]byte(line.String()), &outbound); err != nil {
		t.Fatalf("unmarshal outbound: %v", err)
	}
	return outbound
}

func TestStdioProviderSendMessage(t *testing.T) {
	containerTransport, daemonReader, daemonWriter := setupTransportPair(t)
	expectedResponse := provider.Response{
		Message: provider.Message{Role: "assistant", Content: "hello from daemon"},
	}
	go func() {
		outbound := readDaemonOutbound(t, daemonReader)
		if outbound.Type != "llm_request" {
			t.Errorf("expected type %q, got %q", "llm_request", outbound.Type)
		}
		writeDaemonInbound(t, daemonWriter, StdioInbound{Type: "llm_response", LLMResponse: &expectedResponse})
	}()
	stdioProvider := NewStdioProvider(containerTransport)
	if stdioProvider.Name() != "stdio" {
		t.Errorf("expected name %q, got %q", "stdio", stdioProvider.Name())
	}
	request := provider.Request{
		SystemPrompt: "test",
		Messages:     []provider.Message{{Role: "user", Content: "hi"}},
	}
	response, err := stdioProvider.SendMessage(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response.Message.Content != "hello from daemon" {
		t.Errorf("expected %q, got %q", "hello from daemon", response.Message.Content)
	}
}

func TestStdioProviderHandlesDaemonError(t *testing.T) {
	containerTransport, daemonReader, daemonWriter := setupTransportPair(t)
	go func() {
		readDaemonOutbound(t, daemonReader)
		writeDaemonInbound(t, daemonWriter, StdioInbound{Type: "llm_response", ErrorMessage: "LLM unavailable"})
	}()
	stdioProvider := NewStdioProvider(containerTransport)
	request := provider.Request{Messages: []provider.Message{{Role: "user", Content: "hi"}}}
	_, err := stdioProvider.SendMessage(context.Background(), request)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "LLM unavailable") {
		t.Errorf("expected error to contain %q, got %q", "LLM unavailable", err.Error())
	}
}

func TestStdioJobSchedulerAddJob(t *testing.T) {
	containerTransport, daemonReader, daemonWriter := setupTransportPair(t)
	go func() {
		outbound := readDaemonOutbound(t, daemonReader)
		if outbound.Type != "schedule_request" {
			t.Errorf("expected type %q, got %q", "schedule_request", outbound.Type)
		}
		if outbound.ScheduleRequest == nil {
			t.Error("expected schedule request payload")
			return
		}
		if outbound.ScheduleRequest.CronExpression != "0 9 * * 1" {
			t.Errorf("expected cron %q, got %q", "0 9 * * 1", outbound.ScheduleRequest.CronExpression)
		}
		writeDaemonInbound(t, daemonWriter, StdioInbound{
			Type:         "schedule_response",
			ScheduleID:   "job_123",
			ScheduleNext: "2026-02-19T09:00:00Z",
		})
	}()
	scheduler := NewStdioJobScheduler(containerTransport)
	info, err := scheduler.AddJob("0 9 * * 1", "weekly standup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ID != "job_123" {
		t.Errorf("expected ID %q, got %q", "job_123", info.ID)
	}
}

func TestStdioJobSchedulerHandlesError(t *testing.T) {
	containerTransport, daemonReader, daemonWriter := setupTransportPair(t)
	go func() {
		readDaemonOutbound(t, daemonReader)
		writeDaemonInbound(t, daemonWriter, StdioInbound{Type: "schedule_response", ErrorMessage: "invalid cron expression"})
	}()
	scheduler := NewStdioJobScheduler(containerTransport)
	_, err := scheduler.AddJob("bad cron", "test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid cron expression") {
		t.Errorf("expected error to contain %q, got %q", "invalid cron expression", err.Error())
	}
}
