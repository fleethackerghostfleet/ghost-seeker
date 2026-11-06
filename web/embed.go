package web

import (
	"archive/zip"
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static
var files embed.FS

func Imagery() fs.FS {
	content, err := fs.Sub(files, "static/imagery")
	if err != nil {
		panic(err)
	}
	return content
}

func Handler() http.Handler {
	content, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(content))
	ocean, err := files.ReadFile("static/imagery/empty-ocean.zip")
	if err != nil {
		panic(err)
	}
	archive, archiveErr := zip.NewReader(bytes.NewReader(ocean), int64(len(ocean)))
	var oceanServer http.Handler
	if archiveErr == nil {
		oceanServer = http.StripPrefix("/imagery/empty-ocean/", http.FileServer(http.FS(archive)))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/imagery/empty-ocean/") {
			if archiveErr != nil {
				http.Error(w, "Empty-ocean demo frames unavailable: run git lfs pull and rebuild", http.StatusServiceUnavailable)
				return
			}
			oceanServer.ServeHTTP(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
