//go:build !linux

package capability

import (
	"errors"
	"net/http"
)

func newVSockTransport(cid uint32, port uint32) (*http.Transport, error) {
	return nil, errors.New("vsock capability transport is only available on linux")
}
