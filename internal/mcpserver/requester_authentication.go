package mcpserver

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var ErrUnauthenticatedRequester = errors.New("tool catalog requires a bearer token identifying the requester")

type RequesterResolver interface {
	ResolveRequester(bearerToken string) (RequesterToolSet, error)
}

type SessionTokenRequesterResolver struct {
	mutex           sync.RWMutex
	toolSetByToken  map[string]RequesterToolSet
	newSessionToken func() string
}

func NewSessionTokenRequesterResolver(newSessionToken func() string) *SessionTokenRequesterResolver {
	return &SessionTokenRequesterResolver{toolSetByToken: map[string]RequesterToolSet{}, newSessionToken: newSessionToken}
}

func (resolver *SessionTokenRequesterResolver) GrantSessionToken(requesterToolSet RequesterToolSet) (string, error) {
	if strings.TrimSpace(requesterToolSet.RequesterPersonID) == "" || requesterToolSet.ToolSet == nil {
		return "", errors.New("a tool catalog session needs both a requester and a tool set")
	}
	sessionToken := resolver.newSessionToken()
	resolver.mutex.Lock()
	defer resolver.mutex.Unlock()
	resolver.toolSetByToken[sessionToken] = requesterToolSet
	return sessionToken, nil
}

func (resolver *SessionTokenRequesterResolver) RevokeSessionToken(sessionToken string) {
	resolver.mutex.Lock()
	defer resolver.mutex.Unlock()
	delete(resolver.toolSetByToken, sessionToken)
}

func (resolver *SessionTokenRequesterResolver) ResolveRequester(bearerToken string) (RequesterToolSet, error) {
	resolver.mutex.RLock()
	defer resolver.mutex.RUnlock()
	requesterToolSet, isGranted := resolver.toolSetByToken[strings.TrimSpace(bearerToken)]
	if !isGranted {
		return RequesterToolSet{}, ErrUnauthenticatedRequester
	}
	return requesterToolSet, nil
}

func NewToolCatalogHandler(resolver RequesterResolver, version string) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		requesterToolSet, errorValue := resolver.ResolveRequester(bearerTokenFromRequest(request))
		if errorValue != nil {
			return nil
		}
		server, errorValue := NewToolCatalogServer(requesterToolSet, version)
		if errorValue != nil {
			return nil
		}
		return server
	}, nil)
}

func bearerTokenFromRequest(request *http.Request) string {
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
		return ""
	}
	return strings.TrimSpace(authorization[len("bearer "):])
}
