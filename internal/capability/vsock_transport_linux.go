//go:build linux

package capability

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/mdlayher/vsock"
)

func newVSockTransport(cid uint32, port uint32) (*http.Transport, error) {
	if cid == 0 {
		return nil, fmt.Errorf("vsock cid is required")
	}
	if port == 0 {
		return nil, fmt.Errorf("vsock port is required")
	}
	return &http.Transport{
		DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
			return dialVSock(ctx, cid, port)
		},
	}, nil
}

func dialVSock(ctx context.Context, cid uint32, port uint32) (net.Conn, error) {
	done := make(chan error, 1)
	var connection *vsock.Conn
	go func() {
		var errorValue error
		connection, errorValue = vsock.Dial(cid, port, nil)
		done <- errorValue
	}()
	select {
	case errorValue := <-done:
		if errorValue != nil {
			return nil, errorValue
		}
		return connection, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
