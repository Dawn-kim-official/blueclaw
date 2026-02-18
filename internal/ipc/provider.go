package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/blueclaw/blueclaw/internal/provider"
)

const maxScannerBufferSize = 10 * 1024 * 1024

// StdioTransport handles the shared stdin/stdout channel for container agent ↔ daemon communication.
type StdioTransport struct {
	output  io.Writer
	scanner *bufio.Scanner
}

func NewStdioTransport(output io.Writer, input io.Reader) *StdioTransport {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, maxScannerBufferSize), maxScannerBufferSize)
	return &StdioTransport{output: output, scanner: scanner}
}

func NewStdioTransportFromScanner(output io.Writer, scanner *bufio.Scanner) *StdioTransport {
	return &StdioTransport{output: output, scanner: scanner}
}

func (transport *StdioTransport) WriteOutbound(message StdioOutbound) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(transport.output, string(data))
	return err
}

func (transport *StdioTransport) ReadInbound() (StdioInbound, error) {
	if !transport.scanner.Scan() {
		if err := transport.scanner.Err(); err != nil {
			return StdioInbound{}, err
		}
		return StdioInbound{}, io.EOF
	}
	var inbound StdioInbound
	if err := json.Unmarshal(transport.scanner.Bytes(), &inbound); err != nil {
		return StdioInbound{}, fmt.Errorf("parsing inbound message: %w", err)
	}
	return inbound, nil
}

// StdioProvider implements provider.LLMProvider by proxying calls through the stdio transport.
type StdioProvider struct {
	transport *StdioTransport
}

func NewStdioProvider(transport *StdioTransport) *StdioProvider {
	return &StdioProvider{transport: transport}
}

func (stdioProvider *StdioProvider) Name() string { return "stdio" }

func (stdioProvider *StdioProvider) SendMessage(_ context.Context, request provider.Request) (provider.Response, error) {
	if err := stdioProvider.transport.WriteOutbound(StdioOutbound{Type: "llm_request", LLMRequest: &request}); err != nil {
		return provider.Response{}, fmt.Errorf("writing LLM request: %w", err)
	}
	inbound, err := stdioProvider.transport.ReadInbound()
	if err != nil {
		return provider.Response{}, fmt.Errorf("reading LLM response: %w", err)
	}
	if inbound.ErrorMessage != "" {
		return provider.Response{}, fmt.Errorf("LLM error: %s", inbound.ErrorMessage)
	}
	if inbound.LLMResponse == nil {
		return provider.Response{}, fmt.Errorf("missing LLM response payload")
	}
	return *inbound.LLMResponse, nil
}
