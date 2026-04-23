//go:build linux

package firecracker

import (
	"context"
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

type rawSockaddrVM struct {
	Family    uint16
	Reserved1 uint16
	Port      uint32
	CID       uint32
	Zero      [4]byte
}

func DefaultGuestConnectionDialer(healthContext context.Context, guestCID uint32, healthPortOrService string) (GuestConnection, error) {
	_ = healthContext

	guestPort, errorValue := strconv.ParseUint(healthPortOrService, 10, 32)
	if errorValue != nil {
		return nil, errorValue
	}

	fileDescriptor, errorValue := syscall.Socket(syscall.AF_VSOCK, syscall.SOCK_STREAM, 0)
	if errorValue != nil {
		return nil, errorValue
	}

	socketAddress := rawSockaddrVM{
		Family: uint16(syscall.AF_VSOCK),
		Port:   uint32(guestPort),
		CID:    guestCID,
	}

	_, _, errorNumber := syscall.Syscall(
		syscall.SYS_CONNECT,
		uintptr(fileDescriptor),
		uintptr(unsafe.Pointer(&socketAddress)),
		unsafe.Sizeof(socketAddress),
	)
	if errorNumber != 0 {
		_ = syscall.Close(fileDescriptor)
		return nil, errorNumber
	}

	return os.NewFile(uintptr(fileDescriptor), "vsock"), nil
}
