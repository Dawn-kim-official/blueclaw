//go:build !linux

package firecracker

import (
	"context"
	"errors"
)

func DefaultGuestConnectionDialer(healthContext context.Context, guestCID uint32, healthPortOrService string) (GuestConnection, error) {
	_ = healthContext
	_ = guestCID
	_ = healthPortOrService
	return nil, errors.New("vsock guest health is only available on linux")
}
