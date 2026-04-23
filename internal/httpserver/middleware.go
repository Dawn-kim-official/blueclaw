package httpserver

import "net/http"

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
