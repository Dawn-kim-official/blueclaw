package ipc

import (
	"fmt"
	"time"

	"github.com/blueclaw/blueclaw/internal/tool"
)

// StdioJobScheduler implements tool.JobScheduler by forwarding schedule
// creation requests to the daemon via the stdio transport.
type StdioJobScheduler struct {
	transport *StdioTransport
}

func NewStdioJobScheduler(transport *StdioTransport) *StdioJobScheduler {
	return &StdioJobScheduler{transport: transport}
}

func (scheduler *StdioJobScheduler) AddJob(cronExpression string, prompt string) (tool.ScheduledJobInfo, error) {
	outbound := StdioOutbound{
		Type:            "schedule_request",
		ScheduleRequest: &ScheduleCreate{CronExpression: cronExpression, Prompt: prompt},
	}
	if err := scheduler.transport.WriteOutbound(outbound); err != nil {
		return tool.ScheduledJobInfo{}, fmt.Errorf("writing schedule request: %w", err)
	}
	inbound, err := scheduler.transport.ReadInbound()
	if err != nil {
		return tool.ScheduledJobInfo{}, fmt.Errorf("reading schedule response: %w", err)
	}
	if inbound.ErrorMessage != "" {
		return tool.ScheduledJobInfo{}, fmt.Errorf("%s", inbound.ErrorMessage)
	}
	nextRunAt, _ := time.Parse(time.RFC3339, inbound.ScheduleNext)
	return tool.ScheduledJobInfo{ID: inbound.ScheduleID, NextRunAt: nextRunAt}, nil
}
