package mattermost

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type WebSocketListener struct {
	URL          string
	BotToken     string
	Logger       *slog.Logger
	EventHandler func(context.Context, []byte, string) error
	dialTimeout  time.Duration
	backoff      time.Duration
}

func NewWebSocketListener(webSocketURL string, botToken string, logger *slog.Logger, eventHandler func(context.Context, []byte, string) error) *WebSocketListener {
	return &WebSocketListener{
		URL:          webSocketURL,
		BotToken:     botToken,
		Logger:       logger,
		EventHandler: eventHandler,
		dialTimeout:  10 * time.Second,
		backoff:      time.Second,
	}
}

func (listener *WebSocketListener) Name() string {
	return "mattermost-websocket"
}

func (listener *WebSocketListener) Platform() string {
	return "mattermost"
}

func DeriveWebSocketURL(baseURL string) string {
	parsedURL, errorValue := url.Parse(baseURL)
	if errorValue != nil || parsedURL.Host == "" {
		return ""
	}

	switch parsedURL.Scheme {
	case "https":
		parsedURL.Scheme = "wss"
	default:
		parsedURL.Scheme = "ws"
	}
	parsedURL.Path = "/api/v4/websocket"
	parsedURL.RawQuery = ""
	return parsedURL.String()
}

func (listener *WebSocketListener) Start(ctx context.Context) {
	logger := listener.logger()
	if strings.TrimSpace(listener.URL) == "" || strings.TrimSpace(listener.BotToken) == "" || listener.EventHandler == nil {
		logger.Warn("connector.mattermost.transport.disabled")
		return
	}

	backoff := listener.backoff
	if backoff <= 0 {
		backoff = time.Second
	}

	for ctx.Err() == nil {
		errorValue := listener.runOnce(ctx)
		if errorValue != nil && ctx.Err() == nil {
			logger.Warn("connector.mattermost.transport.disconnected", slog.String("error", errorValue.Error()), slog.Duration("backoff", backoff))
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (listener *WebSocketListener) runOnce(ctx context.Context) error {
	connection, reader, errorValue := listener.connect(ctx)
	if errorValue != nil {
		return errorValue
	}
	defer connection.Close()

	errorValue = writeWebSocketTextFrame(connection, listener.authenticationChallenge())
	if errorValue != nil {
		return errorValue
	}

	listener.logger().Info("connector.mattermost.transport.connected")
	for ctx.Err() == nil {
		payload, errorValue := readWebSocketFrame(connection, reader)
		if errorValue != nil {
			return errorValue
		}
		errorValue = listener.EventHandler(ctx, payload, "websocket")
		if errorValue != nil {
			listener.logger().Error("connector.mattermost.realtime.failed", slog.String("error", errorValue.Error()))
		}
	}

	return ctx.Err()
}

func (listener *WebSocketListener) connect(ctx context.Context) (net.Conn, *bufio.Reader, error) {
	parsedURL, errorValue := url.Parse(listener.URL)
	if errorValue != nil {
		return nil, nil, errorValue
	}
	if parsedURL.Host == "" {
		return nil, nil, errors.New("mattermost websocket host is required")
	}

	dialTimeout := listener.dialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}

	dialer := net.Dialer{Timeout: dialTimeout}
	address := parsedURL.Host
	if !strings.Contains(address, ":") {
		if parsedURL.Scheme == "wss" {
			address += ":443"
		} else {
			address += ":80"
		}
	}

	connection, errorValue := dialer.DialContext(ctx, "tcp", address)
	if errorValue != nil {
		return nil, nil, errorValue
	}

	if parsedURL.Scheme == "wss" {
		tlsConnection := tls.Client(connection, &tls.Config{ServerName: parsedURL.Hostname(), MinVersion: tls.VersionTLS12})
		errorValue = tlsConnection.HandshakeContext(ctx)
		if errorValue != nil {
			_ = connection.Close()
			return nil, nil, errorValue
		}
		connection = tlsConnection
	}

	reader := bufio.NewReader(connection)
	secWebSocketKey, errorValue := randomWebSocketKey()
	if errorValue != nil {
		_ = connection.Close()
		return nil, nil, errorValue
	}

	requestPath := parsedURL.RequestURI()
	if requestPath == "" {
		requestPath = "/api/v4/websocket"
	}
	request := "GET " + requestPath + " HTTP/1.1\r\n" +
		"Host: " + parsedURL.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + secWebSocketKey + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	_, errorValue = connection.Write([]byte(request))
	if errorValue != nil {
		_ = connection.Close()
		return nil, nil, errorValue
	}

	httpResponse, errorValue := http.ReadResponse(reader, nil)
	if errorValue != nil {
		_ = connection.Close()
		return nil, nil, errorValue
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusSwitchingProtocols {
		_ = connection.Close()
		return nil, nil, errors.New("mattermost websocket upgrade failed: " + httpResponse.Status)
	}
	if httpResponse.Header.Get("Sec-WebSocket-Accept") != expectedWebSocketAccept(secWebSocketKey) {
		_ = connection.Close()
		return nil, nil, errors.New("mattermost websocket accept key mismatch")
	}

	return connection, reader, nil
}

func (listener *WebSocketListener) authenticationChallenge() []byte {
	document, _ := json.Marshal(map[string]interface{}{
		"seq":    1,
		"action": "authentication_challenge",
		"data": map[string]string{
			"token": listener.BotToken,
		},
	})
	return document
}

func (listener *WebSocketListener) logger() *slog.Logger {
	if listener.Logger != nil {
		return listener.Logger
	}
	return slog.Default()
}

func randomWebSocketKey() (string, error) {
	value := make([]byte, 16)
	_, errorValue := rand.Read(value)
	if errorValue != nil {
		return "", errorValue
	}
	return base64.StdEncoding.EncodeToString(value), nil
}

func expectedWebSocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func readWebSocketFrame(writer io.Writer, reader *bufio.Reader) ([]byte, error) {
	header := make([]byte, 2)
	_, errorValue := io.ReadFull(reader, header)
	if errorValue != nil {
		return nil, errorValue
	}

	opcode := header[0] & 0x0f
	isMasked := header[1]&0x80 != 0
	payloadLength := uint64(header[1] & 0x7f)
	if payloadLength == 126 {
		lengthBytes := make([]byte, 2)
		_, errorValue = io.ReadFull(reader, lengthBytes)
		if errorValue != nil {
			return nil, errorValue
		}
		payloadLength = uint64(binary.BigEndian.Uint16(lengthBytes))
	}
	if payloadLength == 127 {
		lengthBytes := make([]byte, 8)
		_, errorValue = io.ReadFull(reader, lengthBytes)
		if errorValue != nil {
			return nil, errorValue
		}
		payloadLength = binary.BigEndian.Uint64(lengthBytes)
	}

	var maskKey []byte
	if isMasked {
		maskKey = make([]byte, 4)
		_, errorValue = io.ReadFull(reader, maskKey)
		if errorValue != nil {
			return nil, errorValue
		}
	}

	payload := make([]byte, payloadLength)
	_, errorValue = io.ReadFull(reader, payload)
	if errorValue != nil {
		return nil, errorValue
	}
	if isMasked {
		for index := range payload {
			payload[index] ^= maskKey[index%4]
		}
	}

	switch opcode {
	case 1:
		return payload, nil
	case 8:
		return nil, io.EOF
	case 9:
		_ = writeWebSocketControlFrame(writer, 10, payload)
		return readWebSocketFrame(writer, reader)
	default:
		return []byte{}, nil
	}
}

func writeWebSocketTextFrame(writer io.Writer, payload []byte) error {
	return writeWebSocketFrame(writer, 1, payload)
}

func writeWebSocketControlFrame(writer io.Writer, opcode byte, payload []byte) error {
	return writeWebSocketFrame(writer, opcode, payload)
}

func writeWebSocketFrame(writer io.Writer, opcode byte, payload []byte) error {
	maskKey := make([]byte, 4)
	_, errorValue := rand.Read(maskKey)
	if errorValue != nil {
		return errorValue
	}

	header := []byte{0x80 | opcode}
	payloadLength := len(payload)
	switch {
	case payloadLength < 126:
		header = append(header, 0x80|byte(payloadLength))
	case payloadLength <= 65535:
		header = append(header, 0x80|126)
		lengthBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(lengthBytes, uint16(payloadLength))
		header = append(header, lengthBytes...)
	default:
		header = append(header, 0x80|127)
		lengthBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(lengthBytes, uint64(payloadLength))
		header = append(header, lengthBytes...)
	}
	header = append(header, maskKey...)

	maskedPayload := make([]byte, payloadLength)
	for index, value := range payload {
		maskedPayload[index] = value ^ maskKey[index%4]
	}

	_, errorValue = writer.Write(append(header, maskedPayload...))
	return errorValue
}
