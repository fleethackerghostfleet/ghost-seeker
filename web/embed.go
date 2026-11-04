package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static
var files embed.FS

func Handler() http.Handler {
	content, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(content))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
