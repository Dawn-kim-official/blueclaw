package firecracker

import (
	"context"
	"errors"
	"io"
	"testing"
)

type staticGuestConnection struct {
	response io.Reader
}

func (staticGuestConnection) Close() error {
	return nil
}

func (staticGuestConnection) Write(message []byte) (int, error) {
	return len(message), nil
}

func (guestConnection staticGuestConnection) Read(buffer []byte) (int, error) {
	return guestConnection.response.Read(buffer)
}

func TestVSockGuestHealthClientChecksHealth(t *testing.T) {
	guestHealthClient := VSockGuestHealthClient{
		DialGuestConnection: func(healthContext context.Context, guestCID uint32, healthPortOrService string) (GuestConnection, error) {
			_ = healthContext
			if guestCID != 52 {
				t.Fatalf("expected guest cid to match, got %d", guestCID)
			}
			if healthPortOrService != "8080" {
				t.Fatalf("expected health service to match, got %q", healthPortOrService)
			}
			return staticGuestConnection{response: stringsReader("ok\n")}, nil
		},
	}

	errorValue := guestHealthClient.CheckHealth(context.Background(), 52, "8080")
	if errorValue != nil {
		t.Fatalf("expected health check to succeed: %v", errorValue)
	}
}

func TestVSockGuestHealthClientFailsOnUnexpectedHealth(t *testing.T) {
	guestHealthClient := VSockGuestHealthClient{
		DialGuestConnection: func(healthContext context.Context, guestCID uint32, healthPortOrService string) (GuestConnection, error) {
			_ = healthContext
			_ = guestCID
			_ = healthPortOrService
			return staticGuestConnection{response: stringsReader("bad\n")}, nil
		},
	}

	errorValue := guestHealthClient.CheckHealth(context.Background(), 52, "8080")
	if errorValue == nil {
		t.Fatal("expected unexpected health response to fail")
	}
}

func stringsReader(value string) io.Reader {
	return &staticStringReader{value: []byte(value)}
}

type staticStringReader struct {
	value []byte
}

func (staticStringReader *staticStringReader) Read(buffer []byte) (int, error) {
	if len(staticStringReader.value) == 0 {
		return 0, io.EOF
	}

	readLength := copy(buffer, staticStringReader.value)
	staticStringReader.value = staticStringReader.value[readLength:]
	return readLength, nil
}

type failingGuestConnection struct{}

func (failingGuestConnection) Close() error {
	return nil
}

func (failingGuestConnection) Write(message []byte) (int, error) {
	_ = message
	return 0, errors.New("write failed")
}

func (failingGuestConnection) Read(buffer []byte) (int, error) {
	_ = buffer
	return 0, io.EOF
}
