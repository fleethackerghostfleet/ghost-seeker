package web

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"net/http"
	"strings"
)

func Handler() http.Handler {
	content, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(content))
	var oceanServer http.Handler
	ocean, archiveErr := files.ReadFile("static/imagery/empty-ocean.zip")
	if !hasDemoImagery {
		archiveErr = nil
	} else if archiveErr != nil {
		panic(archiveErr)
	}
	archive, zipErr := zip.NewReader(bytes.NewReader(ocean), int64(len(ocean)))
	if hasDemoImagery && zipErr != nil {
		archiveErr = zipErr
	}
	if hasDemoImagery && archiveErr == nil {
		oceanServer = http.StripPrefix("/imagery/empty-ocean/", http.FileServer(http.FS(archive)))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		if !hasDemoImagery && strings.HasPrefix(r.URL.Path, "/imagery/") {
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
