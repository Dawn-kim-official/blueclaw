package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
)

type AdminAssetHandler struct {
	RootDirectoryPath string
}

func (adminAssetHandler AdminAssetHandler) ServeHTTP(responseWriter http.ResponseWriter, request *http.Request) {
	targetPath := filepath.Join(adminAssetHandler.RootDirectoryPath, request.URL.Path)
	if information, errorValue := os.Stat(targetPath); errorValue == nil && !information.IsDir() {
		http.FileServer(http.Dir(adminAssetHandler.RootDirectoryPath)).ServeHTTP(responseWriter, request)
		return
	}

	http.ServeFile(responseWriter, request, filepath.Join(adminAssetHandler.RootDirectoryPath, "index.html"))
}
