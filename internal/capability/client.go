package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	Transport  string
	SocketPath string
	Endpoint   string
	VSockCID   uint32
	VSockPort  uint32
	HTTPClient *http.Client
}

func NewClient(transport string, socketPath string, endpoint string, vsockCID uint32, vsockPort uint32) Client {
	return Client{
		Transport:  strings.TrimSpace(transport),
		SocketPath: strings.TrimSpace(socketPath),
		Endpoint:   strings.TrimSpace(endpoint),
		VSockCID:   vsockCID,
		VSockPort:  vsockPort,
	}
}

func (client Client) Post(ctx context.Context, path string, payload any, responseValue any) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("capability path is empty")
	}

	document, errorValue := json.Marshal(payload)
	if errorValue != nil {
		return errorValue
	}

	httpClient := client.httpClient()
	endpoint := client.endpoint(path)
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
		responseDocument, _ := io.ReadAll(httpResponse.Body)
		errorMessage := strings.TrimSpace(string(responseDocument))
		if errorMessage == "" {
			errorMessage = httpResponse.Status
		}
		return errors.New("capability operation failed: " + errorMessage)
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
	if client.transport() == "http" {
		return &http.Client{Timeout: 120 * time.Second}
	}
	socketPath := client.socketPath()
	return &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
				if client.transport() == "vsock" {
					return nil, errors.New("capability vsock transport is not available in this build")
				}
				dialer := net.Dialer{}
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

func (client Client) endpoint(path string) string {
	baseEndpoint := "http://internkim"
	if strings.TrimSpace(client.Endpoint) != "" {
		baseEndpoint = strings.TrimRight(client.Endpoint, "/")
	}
	return baseEndpoint + "/" + strings.TrimLeft(path, "/")
}

func (client Client) socketPath() string {
	if strings.TrimSpace(client.SocketPath) != "" {
		return client.SocketPath
	}
	return "/run/internkim/capability.sock"
}

func (client Client) transport() string {
	if strings.TrimSpace(client.Transport) != "" {
		return strings.TrimSpace(client.Transport)
	}
	return "unix"
}
