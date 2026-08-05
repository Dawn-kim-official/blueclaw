package llm

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

type HTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

// ProtocolIdentityTarget reports where the protocol identity check can reach
// llmd, and the client that can talk to it. A Unix socket deployment has no
// routable endpoint, so it needs a socket-bound client of its own.
func ProtocolIdentityTarget(runtimeConfiguration config.RuntimeConfiguration, defaultHTTPClient HTTPDoer) (string, HTTPDoer) {
	llmdConfiguration := runtimeConfiguration.LanguageModel.LLMD
	unixSocketPath := strings.TrimSpace(llmdConfiguration.UnixSocketPath)
	if unixSocketPath == "" {
		return llmdConfiguration.Endpoint, defaultHTTPClient
	}
	socketClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", unixSocketPath)
			},
		},
	}
	endpoint := strings.TrimSpace(llmdConfiguration.Endpoint)
	if endpoint == "" {
		endpoint = defaultLLMDEndpoint
	}
	return endpoint, socketClient
}
