package agent

import (
	"context"

	"github.com/blueclaw/blueclaw/internal/provider"
)

type Runner interface {
	RunAgent(executionContext context.Context, request provider.Request, sessionID string) (provider.Response, error)
}
