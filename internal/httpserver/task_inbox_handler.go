package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
)

type TaskInboxHandler struct {
	RootDirectoryPath string
}

func (taskInboxHandler TaskInboxHandler) ServeHTTP(responseWriter http.ResponseWriter, request *http.Request) {
	targetPath := filepath.Join(taskInboxHandler.RootDirectoryPath, request.URL.Path)
	if information, errorValue := os.Stat(targetPath); errorValue == nil && !information.IsDir() {
		http.FileServer(http.Dir(taskInboxHandler.RootDirectoryPath)).ServeHTTP(responseWriter, request)
		return
	}

	http.ServeFile(responseWriter, request, filepath.Join(taskInboxHandler.RootDirectoryPath, "index.html"))
}
