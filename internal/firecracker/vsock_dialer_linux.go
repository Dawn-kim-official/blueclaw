//go:build linux

package firecracker

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

func DefaultGuestConnectionDialer(healthContext context.Context, vsockUnixSocketPath string, healthPortOrService string) (GuestConnection, error) {
	guestPort, errorValue := strconv.ParseUint(healthPortOrService, 10, 32)
	if errorValue != nil {
		return nil, errorValue
	}

	var dialer net.Dialer
	connection, errorValue := dialer.DialContext(healthContext, "unix", vsockUnixSocketPath)
	if errorValue != nil {
		return nil, errorValue
	}
	if errorValue = connection.SetDeadline(soonestDeadline(time.Now(), healthContext, firecrackerVSockOperationTimeout)); errorValue != nil {
		_ = connection.Close()
		return nil, errorValue
	}

	reader := bufio.NewReader(connection)
	if _, errorValue = fmt.Fprintf(connection, "CONNECT %d\n", guestPort); errorValue != nil {
		_ = connection.Close()
		return nil, errorValue
	}

	response, errorValue := reader.ReadString('\n')
	if errorValue != nil {
		_ = connection.Close()
		return nil, errorValue
	}
	if !strings.HasPrefix(response, "OK ") {
		_ = connection.Close()
		return nil, fmt.Errorf("firecracker vsock connect failed: %s", strings.TrimSpace(response))
	}
	if errorValue = connection.SetDeadline(time.Time{}); errorValue != nil {
		_ = connection.Close()
		return nil, errorValue
	}

	return firecrackerGuestConnection{Conn: connection, Reader: reader}, nil
}

type firecrackerGuestConnection struct {
	net.Conn
	Reader *bufio.Reader
}

func (firecrackerGuestConnection firecrackerGuestConnection) Read(buffer []byte) (int, error) {
	return firecrackerGuestConnection.Reader.Read(buffer)
}
