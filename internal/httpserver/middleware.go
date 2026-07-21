package httpserver

import (
	"net/http"
	"net/url"
	"strings"
)

func withRecovery(nextHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		defer func() {
			if recoveredValue := recover(); recoveredValue != nil {
				http.Error(responseWriter, "internal server error", http.StatusInternalServerError)
			}
		}()
		nextHandler.ServeHTTP(responseWriter, request)
	})
}

func withOriginCheck(nextHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if isCrossOriginMutatingRequest(request) {
			http.Error(responseWriter, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		nextHandler.ServeHTTP(responseWriter, request)
	})
}

func isCrossOriginMutatingRequest(request *http.Request) bool {
	if request.Method == http.MethodGet || request.Method == http.MethodHead || request.Method == http.MethodOptions {
		return false
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	originURL, errorValue := url.Parse(origin)
	if errorValue != nil || originURL.Host == "" {
		return true
	}
	return !strings.EqualFold(originURL.Host, request.Host)
}
