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

const DefaultEndpoint = "http://127.0.0.1:7781"

type Configuration struct {
	Endpoint       string
	Transport      string
	UnixSocketPath string
	VSockCID       uint32
	VSockPort      uint32
	Timeout        time.Duration
}

type Client struct {
	Endpoint   string
	HTTPClient HTTPDoer
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func NewClient(configuration Configuration) Client {
	endpoint := strings.TrimRight(strings.TrimSpace(configuration.Endpoint), "/")
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	timeout := configuration.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	httpClient := &http.Client{
		Timeout: timeout,
	}

	unixSocketPath := strings.TrimSpace(configuration.UnixSocketPath)
	if strings.TrimSpace(configuration.Transport) == "vsock" {
		transport, errorValue := newVSockTransport(configuration.VSockCID, configuration.VSockPort)
		httpClient.Transport = transportWithError(transport, errorValue)
		if strings.TrimSpace(configuration.Endpoint) == "" {
			endpoint = "http://internkim-capability"
		}
	} else if unixSocketPath != "" {
		httpClient.Transport = newUnixSocketTransport(unixSocketPath)
		if strings.TrimSpace(configuration.Endpoint) == "" {
			endpoint = "http://internkim-capability"
		}
	}

	return Client{
		Endpoint:   endpoint,
		HTTPClient: httpClient,
	}
}

func transportWithError(transport *http.Transport, errorValue error) http.RoundTripper {
	if errorValue != nil {
		return roundTripError{errorValue: errorValue}
	}
	return transport
}

type roundTripError struct {
	errorValue error
}

func (roundTripper roundTripError) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, roundTripper.errorValue
}

func (client Client) PostJSON(ctx context.Context, path string, requestDocument any, responseDocument any) error {
	if client.HTTPClient == nil {
		return errors.New("capability http client is not configured")
	}

	requestBody, errorValue := json.Marshal(requestDocument)
	if errorValue != nil {
		return errorValue
	}

	httpRequest, errorValue := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		client.endpointURL(path),
		bytes.NewReader(requestBody),
	)
	if errorValue != nil {
		return errorValue
	}

	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, errorValue := client.HTTPClient.Do(httpRequest)
	if errorValue != nil {
		return errorValue
	}
	defer httpResponse.Body.Close()

	responseBody, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return errorValue
	}

	if httpResponse.StatusCode >= http.StatusBadRequest {
		return errors.New(strings.TrimSpace(string(responseBody)))
	}

	if responseDocument == nil || len(responseBody) == 0 {
		return nil
	}

	return json.Unmarshal(responseBody, responseDocument)
}

func (client Client) endpointURL(path string) string {
	cleanPath := "/" + strings.TrimLeft(path, "/")
	return strings.TrimRight(client.Endpoint, "/") + cleanPath
}

func newUnixSocketTransport(unixSocketPath string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := net.Dialer{}
			return dialer.DialContext(ctx, "unix", unixSocketPath)
		},
	}
}
