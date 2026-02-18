package ipc

import "fmt"

// StdioHeartbeatIntervalSetter implements tool.HeartbeatIntervalSetter by
// forwarding interval change requests to the daemon via the stdio transport.
type StdioHeartbeatIntervalSetter struct {
	transport *StdioTransport
}

func NewStdioHeartbeatIntervalSetter(transport *StdioTransport) *StdioHeartbeatIntervalSetter {
	return &StdioHeartbeatIntervalSetter{transport: transport}
}

func (setter *StdioHeartbeatIntervalSetter) SetInterval(duration string) error {
	outbound := StdioOutbound{
		Type:                    "heartbeat_interval_request",
		HeartbeatIntervalRequest: &HeartbeatIntervalCreate{Duration: duration},
	}
	if err := setter.transport.WriteOutbound(outbound); err != nil {
		return fmt.Errorf("writing heartbeat interval request: %w", err)
	}
	inbound, err := setter.transport.ReadInbound()
	if err != nil {
		return fmt.Errorf("reading heartbeat interval response: %w", err)
	}
	if inbound.ErrorMessage != "" {
		return fmt.Errorf("%s", inbound.ErrorMessage)
	}
	return nil
}
