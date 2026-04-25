package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	SocketPath string
	Endpoint   string
	HTTPClient *http.Client
}

type requestDocument struct {
	Operation string `json:"operation"`
	Payload   any    `json:"payload"`
}

func NewClient(socketPath string, endpoint string) Client {
	return Client{
		SocketPath: strings.TrimSpace(socketPath),
		Endpoint:   strings.TrimSpace(endpoint),
	}
}

func (client Client) Call(ctx context.Context, operation string, payload any, responseValue any) error {
	if strings.TrimSpace(operation) == "" {
		return errors.New("capability operation is empty")
	}

	document, errorValue := json.Marshal(requestDocument{
		Operation: operation,
		Payload:   payload,
	})
	if errorValue != nil {
		return errorValue
	}

	httpClient := client.httpClient()
	endpoint := client.endpoint()
	request, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(document))
	if errorValue != nil {
		return errorValue
	}
	request.Header.Set("Content-Type", "application/json")

	httpResponse, errorValue := httpClient.Do(request)
	if errorValue != nil {
		return errorValue
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return errors.New("capability operation failed: " + httpResponse.Status)
	}
	if responseValue == nil {
		return nil
	}

	return json.NewDecoder(httpResponse.Body).Decode(responseValue)
}

func (client Client) httpClient() *http.Client {
	if client.HTTPClient != nil {
		return client.HTTPClient
	}
	socketPath := client.socketPath()
	return &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
				dialer := net.Dialer{}
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

func (client Client) endpoint() string {
	if strings.TrimSpace(client.Endpoint) != "" {
		return client.Endpoint
	}
	return "http://internkim/v1/capabilities"
}

func (client Client) socketPath() string {
	if strings.TrimSpace(client.SocketPath) != "" {
		return client.SocketPath
	}
	return "/run/internkim/capability.sock"
}
